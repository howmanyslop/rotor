package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var concurrencyTimingLine = regexp.MustCompile(`(?m)(compiled\s+\d+\s+files\s+in\s+)\d+(?:\.\d+)?\s*(?:ns|us|µs|ms|s)`)
var concurrencyRate = regexp.MustCompile(`(?m)(\s)\d+ files/s`)
var concurrencyPathPrefix = regexp.MustCompile(`^(?:[A-Za-z]:)?/+`)

type concurrencySystemRun struct {
	code     int
	stdout   string
	stderr   string
	manifest map[string]string
}

type concurrencySystemFixture struct {
	root    string
	outDirs []string
}

func TestConcurrencySystem_BuildersPreserveCleanSolution(t *testing.T) {
	var baseline *concurrencySystemRun
	for _, builders := range []int{1, 2, 4} {
		fixture := writeConcurrencySystemFixture(t, false)
		run := runConcurrencySystemBuild(t, fixture, builders, 2)

		if run.code != 0 {
			t.Fatalf("builders %d exit = %d, want 0; stdout=%q stderr=%q", builders, run.code, run.stdout, run.stderr)
		}
		if run.stderr != "" {
			t.Fatalf("builders %d emitted unexpected diagnostics: %q", builders, run.stderr)
		}
		run.manifest = concurrencyArtifactManifest(t, fixture)
		writeConcurrencyManifest(t, fmt.Sprintf("task-9-builders-%d.json", builders), run.manifest)
		if len(run.manifest) == 0 {
			t.Fatalf("builders %d emitted no artifacts", builders)
		}
		if baseline == nil {
			baseline = &run
			continue
		}
		assertConcurrencySystemRunEqual(t, *baseline, run, fmt.Sprintf("builders 1 and %d", builders))
	}
}

func TestConcurrencySystem_BuildersPreserveDiagnosticGraph(t *testing.T) {
	var baseline *concurrencySystemRun
	for _, builders := range []int{1, 2, 4} {
		fixture := writeConcurrencySystemFixture(t, true)
		run := runConcurrencySystemBuild(t, fixture, builders, 2)
		if run.code != 1 {
			t.Fatalf("builders %d exit = %d, want 1; stdout=%q stderr=%q", builders, run.code, run.stdout, run.stderr)
		}
		errorIndex := strings.Index(run.stderr, "left/src/value.ts")
		blockedIndex := strings.Index(run.stderr, "blocked by failed dependency")
		if errorIndex < 0 || blockedIndex < 0 || errorIndex >= blockedIndex {
			t.Fatalf("builders %d diagnostics are not graph ordered: %q", builders, run.stderr)
		}
		if !strings.Contains(run.stderr, "game/tsconfig.json") {
			t.Fatalf("builders %d diagnostics omit blocked dependent: %q", builders, run.stderr)
		}
		if baseline == nil {
			baseline = &run
			continue
		}
		if run.stdout != baseline.stdout || run.stderr != baseline.stderr {
			t.Fatalf("builders 1 and %d diagnostics differ:\nstdout 1=%q\nstdout %d=%q\nstderr 1=%q\nstderr %d=%q", builders, baseline.stdout, builders, run.stdout, baseline.stderr, builders, run.stderr)
		}
	}
}

func TestConcurrencySystem_CheckerCountsAreFixtureCompatible(t *testing.T) {
	var baseline map[string]string
	for _, checkers := range []int{1, 2, 4} {
		fixture := writeConcurrencySystemFixture(t, false)
		run := runConcurrencySystemBuild(t, fixture, 2, checkers)

		if run.code != 0 {
			t.Fatalf("checkers %d exit = %d, want 0; stdout=%q stderr=%q", checkers, run.code, run.stdout, run.stderr)
		}
		if run.stderr != "" {
			t.Fatalf("checkers %d emitted unexpected diagnostics: %q", checkers, run.stderr)
		}
		run.manifest = concurrencyArtifactManifest(t, fixture)
		writeConcurrencyManifest(t, fmt.Sprintf("task-9-checkers-%d.json", checkers), run.manifest)
		if len(run.manifest) == 0 {
			t.Fatalf("checkers %d emitted no artifacts", checkers)
		}
		if baseline == nil {
			baseline = run.manifest
			continue
		}
		if !reflect.DeepEqual(baseline, run.manifest) {
			t.Fatalf("checker-count fixture compatibility failed for %d: baseline=%v current=%v", checkers, baseline, run.manifest)
		}
	}
}

func TestConcurrencySystem_OneShotCommandsAcceptCheckerOverride(t *testing.T) {
	buildDir := writeBuildableProject(t, "")
	if _, code := captureStdout(t, func() int {
		return cmdBuild([]string{"--checkers", "2", buildDir})
	}); code != 0 {
		t.Fatalf("one-shot build exit = %d, want 0", code)
	}

	checkDir := writeCheckableProject(t, "")
	if _, code := captureStdout(t, func() int {
		return cmdCheck([]string{"--checkers", "2", checkDir})
	}); code != 0 {
		t.Fatalf("one-shot check exit = %d, want 0", code)
	}
}

func runConcurrencySystemBuild(t *testing.T, fixture concurrencySystemFixture, builders, checkers int) concurrencySystemRun {
	t.Helper()
	var stderr string
	var code int
	stdout, _ := captureStdout(t, func() int {
		stderr, code = captureStderr(t, func() int {
			return cmdBuild([]string{
				"--build", "--builders", fmt.Sprint(builders), "--checkers", fmt.Sprint(checkers), fixture.root,
			})
		})
		return code
	})
	return concurrencySystemRun{
		code:   code,
		stdout: normalizeConcurrencyOutput(stdout, fixture.root),
		stderr: normalizeConcurrencyOutput(stderr, fixture.root),
	}
}

func assertConcurrencySystemRunEqual(t *testing.T, want, got concurrencySystemRun, label string) {
	t.Helper()
	if want.code != got.code || want.stdout != got.stdout || want.stderr != got.stderr || !reflect.DeepEqual(want.manifest, got.manifest) {
		t.Fatalf("%s differ:\nwant=%+v\ngot=%+v", label, want, got)
	}
}

func writeConcurrencySystemFixture(t *testing.T, withError bool) concurrencySystemFixture {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "tsconfig.json"), `{"files":[],"references":[{"path":"./left"},{"path":"./right"},{"path":"./game"}]}`)
	projectConfig := `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"]}`
	for _, project := range []string{"left", "right"} {
		dir := filepath.Join(root, project)
		mustWrite(t, filepath.Join(dir, "tsconfig.json"), projectConfig)
		mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"@scope/`+project+`"}`)
		mustWrite(t, filepath.Join(dir, "src", "globals.d.ts"), noLibGlobalStubs)
		source := `export const ` + project + `Value: string = "` + project + `";` + "\n"
		if project == "left" && withError {
			source = `export const leftValue: string = 123;` + "\n"
		}
		mustWrite(t, filepath.Join(dir, "src", "value.ts"), source)
	}
	gameDir := filepath.Join(root, "game")
	mustWrite(t, filepath.Join(gameDir, "tsconfig.json"), `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"],"references":[{"path":"../left"},{"path":"../right"}],"rbxts":{"rojo":"./game.project.json","type":"game"}}`)
	mustWrite(t, filepath.Join(gameDir, "package.json"), `{"name":"@scope/game"}`)
	mustWrite(t, filepath.Join(gameDir, "game.project.json"), `{"name":"game","tree":{"$className":"DataModel","include":{"$path":"include"},"left":{"$path":"../left/out"},"right":{"$path":"../right/out"},"game":{"$path":"out"}}}`)
	mustWrite(t, filepath.Join(gameDir, "include", "RuntimeLib.lua"), "return {}\n")
	mustWrite(t, filepath.Join(gameDir, "src", "globals.d.ts"), noLibGlobalStubs)
	mustWrite(t, filepath.Join(gameDir, "src", "main.ts"), "import { leftValue } from \"../../left/src/value\";\nimport { rightValue } from \"../../right/src/value\";\nexport const gameValue: string = leftValue + rightValue;\n")
	return concurrencySystemFixture{
		root:    root,
		outDirs: []string{filepath.Join(root, "left", "out"), filepath.Join(root, "right", "out"), filepath.Join(root, "game", "out")},
	}
}

func concurrencyArtifactManifest(t *testing.T, fixture concurrencySystemFixture) map[string]string {
	t.Helper()
	manifest := make(map[string]string)
	for _, outDir := range fixture.outDirs {
		err := filepath.WalkDir(outDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			// This generated cache records absolute source paths and mtimes. It is
			// build state, not an emitted artifact, so it is excluded rather than
			// normalizing its contents.
			if entry.Name() == "rbxts.copyfiles.json" {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(contents)
			relative, err := filepath.Rel(fixture.root, path)
			if err != nil {
				return err
			}
			manifest[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
			return nil
		})
		if err != nil {
			t.Fatalf("walk artifact directory %s: %v", outDir, err)
		}
	}
	return manifest
}

func writeConcurrencyManifest(t *testing.T, name string, manifest map[string]string) {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while locating evidence directory")
	}
	evidenceDir := filepath.Join(filepath.Dir(sourceFile), "..", "..", ".omo", "evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, name), append(contents, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func normalizeConcurrencyOutput(value, tempRoot string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	// Separators are normalized by hand instead of with filepath.ToSlash and
	// filepath.Base, which ignore backslashes off Windows: doing it this way
	// makes the redaction identical on every platform, so the Windows spellings
	// stay testable from a Linux run.
	slashRoot := strings.ReplaceAll(tempRoot, `\`, "/")
	value = strings.ReplaceAll(value, slashRoot, "<TEMP_ROOT>")
	// Diagnostics can render a fixture path relative to the working directory,
	// which drops the volume and leading separator; on Windows that spelling
	// never matched the absolute root. The prefix is stripped by pattern rather
	// than filepath.VolumeName so the behavior is identical (and testable) on
	// every platform.
	if volumeless := concurrencyPathPrefix.ReplaceAllString(slashRoot, ""); volumeless != "" && volumeless != slashRoot {
		value = strings.ReplaceAll(value, volumeless, "<TEMP_ROOT>")
	}
	// The banner prints the project directory on its own, so the bare base name
	// needs redacting too — but only as a whole word. An unguarded "001" also
	// matched digits inside the enclosing temp directory name, which made two
	// builders normalize identical Windows output to different text.
	baseName := slashRoot[strings.LastIndex(slashRoot, "/")+1:]
	base := regexp.MustCompile(`\b` + regexp.QuoteMeta(baseName) + `\b`)
	value = base.ReplaceAllString(value, "<TEMP_ROOT>")
	value = concurrencyTimingLine.ReplaceAllString(value, `${1}<TIME>`)
	return concurrencyRate.ReplaceAllString(value, `${1}<RATE> files/s`)
}

// TestNormalizeConcurrencyOutputRedactsWindowsSpellings pins the Windows shapes
// no Linux run reaches: a diagnostic path rendered relative to the working
// directory (no volume), and a temp directory name whose digits must survive a
// redaction of the bare base component.
func TestNormalizeConcurrencyOutputRedactsWindowsSpellings(t *testing.T) {
	tempRoot := `C:\Users\RUNNER~1\AppData\Local\Temp\TestGraph1003578272\003`
	value := "  rotor v0.0.0  -  003\n" +
		"  --> ../../../../Users/RUNNER~1/AppData/Local/Temp/TestGraph1003578272/003/left/src/value.ts:1:14\n" +
		"  wrote TestGraph1003578272.json\n"

	got := normalizeConcurrencyOutput(value, tempRoot)

	want := "  rotor v0.0.0  -  <TEMP_ROOT>\n" +
		"  --> ../../../../<TEMP_ROOT>/left/src/value.ts:1:14\n" +
		"  wrote TestGraph1003578272.json\n"
	if got != want {
		t.Fatalf("normalizeConcurrencyOutput() = %q, want %q", got, want)
	}
}
