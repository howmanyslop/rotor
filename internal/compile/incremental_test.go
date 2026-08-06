package compile

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIncrementalManifestV1HardCutover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(`{"salt":"old","files":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := readIncrementalManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest != nil {
		t.Fatalf("v1 manifest accepted: %+v", manifest)
	}
}

func TestBuildProjectIncrementalRebuildsChangedFilesAndImporters(t *testing.T) {
	dir := writeProject(t, "@scope/incremental-fixture", "")
	enableIncrementalBuilds(t, dir)
	writeIncrementalFixture(t, dir)

	first, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("first build diagnostics: %v", diags)
	}

	buildInfoPath := filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")
	buildInfo, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatalf("read build info: %v", err)
	}
	if !strings.Contains(string(buildInfo), "\"salt\"") {
		t.Fatalf("build info = %q, want rotor incremental manifest JSON", string(buildInfo))
	}

	old := time.Unix(100, 0)
	for _, rel := range []string{"out/main.luau", "out/util.luau", "out/side.luau"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "src", "util.ts"), []byte("export const VALUE = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("second build diagnostics: %v", diags)
	}

	if got, want := emittedFileBases(second), []string{"util.luau"}; !slices.Equal(got, want) {
		t.Fatalf("second build emitted files = %v, want %v", got, want)
	}

	sideInfo, err := os.Stat(filepath.Join(dir, "out", "side.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if !sideInfo.ModTime().Equal(old) {
		t.Fatalf("side.luau modtime = %v, want preserved %v", sideInfo.ModTime(), old)
	}

	_ = first
}

func TestBuildProjectIncrementalRecreatesMissingOutputs(t *testing.T) {
	for _, relativePath := range []string{
		"out/main.luau",
		"out/main.luau.map",
		"out/main.d.ts",
		"out/main.d.ts.map",
	} {
		t.Run(relativePath, func(t *testing.T) {
			allOutputs := []string{
				"out/main.luau",
				"out/main.luau.map",
				"out/main.d.ts",
				"out/main.d.ts.map",
			}
			dir := writeProject(t, "@scope/incremental-missing-output-fixture", "")
			enableIncrementalBuilds(t, dir)

			tsconfigPath := filepath.Join(dir, "tsconfig.json")
			tsconfigBytes, err := os.ReadFile(tsconfigPath)
			if err != nil {
				t.Fatal(err)
			}
			tsconfig := strings.Replace(
				string(tsconfigBytes),
				`"outDir": "out",`,
				`"outDir": "out", "sourceMap": true, "declaration": true, "declarationMap": true,`,
				1,
			)
			if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
				t.Fatalf("first build: %v (diags: %v)", err, diags)
			}
			old := time.Unix(100, 0)
			for _, output := range allOutputs {
				if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(output)), old, old); err != nil {
					t.Fatal(err)
				}
			}

			path := filepath.Join(dir, filepath.FromSlash(relativePath))
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}

			timings := NewBuildTimings()
			if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings}); err != nil {
				t.Fatalf("rebuild: %v (diags: %v)", err, diags)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing output was not recreated: %v", err)
			}
			if string(got) != string(want) {
				t.Fatal("recreated output differs from the first build")
			}
			if timings.Counts.ActualWrites != 1 {
				var changed []string
				for _, output := range allOutputs {
					info, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(output)))
					if statErr != nil || !info.ModTime().Equal(old) {
						changed = append(changed, output)
					}
				}
				t.Fatalf("actual writes = %d, want 1; changed outputs = %v", timings.Counts.ActualWrites, changed)
			}
		})
	}
}

func TestBuildProjectIncrementalNoChangeKeepsManifestAndOutputs(t *testing.T) {
	dir := writeProject(t, "@scope/incremental-nochange-fixture", "")
	enableIncrementalBuilds(t, dir)
	writeIncrementalFixture(t, dir)
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("seed build: %v (diags: %v)", err, diags)
	}

	manifestPath := filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Unix(100, 0)
	outputPaths := []string{"out/main.luau", "out/util.luau", "out/side.luau"}
	for _, rel := range outputPaths {
		if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(rel)), old, old); err != nil {
			t.Fatal(err)
		}
	}

	timings := NewBuildTimings()
	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings})
	if err != nil {
		t.Fatalf("no-change build: %v (diags: %v)", err, diags)
	}
	if timings.Counts.SelectedSources != 0 {
		t.Fatalf("selected sources = %d, want 0", timings.Counts.SelectedSources)
	}
	if timings.Counts.ActualWrites != 0 {
		t.Fatalf("actual writes = %d, want 0", timings.Counts.ActualWrites)
	}
	if len(result.EmittedFiles) != 0 {
		t.Fatalf("emitted files = %v, want none", result.EmittedFiles)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("no-change build rewrote the incremental manifest")
	}
	for _, rel := range outputPaths {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Fatalf("%s modtime = %v, want preserved %v", rel, info.ModTime(), old)
		}
	}
}

func TestBuildIncrementalManifestRecomputesAllReferencesAfterSourceChange(t *testing.T) {
	dir := writeProject(t, "@scope/incremental-refs-fixture", "")
	enableIncrementalBuilds(t, dir)
	for rel, text := range map[string]string{
		"src/main.ts": "import { VALUE } from \"./util\";\nexport const main = VALUE;\n",
		"src/util.ts": "export const VALUE = 1;\n",
		"src/side.ts": "import { VALUE } from \"./util\";\nexport const side = VALUE;\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}

	manifestPath := filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")
	seeded, err := readIncrementalManifest(manifestPath)
	if err != nil || seeded == nil {
		t.Fatalf("read seeded manifest: %v", err)
	}
	mainKey := normalizeSourceFilePath(filepath.Join(dir, "src", "main.ts"))
	utilKey := normalizeSourceFilePath(filepath.Join(dir, "src", "util.ts"))
	sideKey := normalizeSourceFilePath(filepath.Join(dir, "src", "side.ts"))
	sideState := seeded.Files[sideKey]
	if !slices.Equal(sideState.Refs, []string{utilKey}) {
		t.Fatalf("seeded side.ts refs = %v, want [%s]", sideState.Refs, utilKey)
	}
	sideState.Refs = []string{"sentinel"}
	seeded.Files[sideKey] = sideState
	if err := writeIncrementalManifest(manifestPath, seeded); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "src", "util.ts"), []byte("export const VALUE = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}

	rebuilt, err := readIncrementalManifest(manifestPath)
	if err != nil || rebuilt == nil {
		t.Fatalf("read rebuilt manifest: %v", err)
	}
	if got := rebuilt.Files[sideKey].Refs; !slices.Equal(got, []string{utilKey}) {
		t.Fatalf("side.ts refs = %v, want recomputed [%s]", got, utilKey)
	}
	if got := rebuilt.Files[mainKey].Refs; !slices.Equal(got, []string{utilKey}) {
		t.Fatalf("main.ts refs = %v, want recomputed [%s]", got, utilKey)
	}
}

func TestBuildProjectIncrementalInvalidatesEmitOptionChanges(t *testing.T) {
	dir := writeProject(t, "@scope/incremental-options-fixture", "")
	enableIncrementalBuilds(t, dir)
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}

	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfigBytes, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	tsconfig := strings.Replace(string(tsconfigBytes), `"outDir": "out",`, `"outDir": "out", "sourceMap": true,`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}

	timings := NewBuildTimings()
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings}); err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	if timings.Counts.SelectedSources != 1 {
		t.Fatalf("selected sources = %d, want 1", timings.Counts.SelectedSources)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.luau.map")); err != nil {
		t.Fatalf("source map missing after sourceMap option change: %v", err)
	}
}

func TestBuildProjectIncrementalInvalidatesPathChanges(t *testing.T) {
	dir := writeProject(t, "@scope/incremental-paths-fixture", "")
	enableIncrementalBuilds(t, dir)
	for relativePath, contents := range map[string]string{
		"src/main.ts": "import { value } from \"alias\";\nexport const result = value;\n",
		"src/one.ts":  "export const value = 1;\n",
		"src/two.ts":  "export const value = 2;\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(relativePath)), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfigBytes, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	tsconfig := strings.Replace(
		string(tsconfigBytes),
		`"outDir": "out",`,
		`"outDir": "out", "baseUrl": ".", "paths": {"alias": ["src/one"]},`,
		1,
	)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}

	tsconfig = strings.Replace(tsconfig, `"src/one"`, `"src/two"`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	timings := NewBuildTimings()
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings}); err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	if timings.Counts.SelectedSources == 0 {
		t.Fatal("paths option change selected no sources")
	}
}

func enableIncrementalBuilds(t *testing.T, dir string) {
	t.Helper()
	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfigBytes, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	tsconfig := strings.Replace(string(tsconfigBytes), `"outDir": "out"`, `"outDir": "out",
		"incremental": true,
		"tsBuildInfoFile": "out/cache.tsbuildinfo"`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeIncrementalFixture(t *testing.T, dir string) {
	t.Helper()
	for rel, text := range map[string]string{
		"src/main.ts": "import { VALUE } from \"./util\";\nexport const main = VALUE;\n",
		"src/util.ts": "export const VALUE = 1;\n",
		"src/side.ts": "export const side = 1;\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func emittedFileBases(result *BuildResult) []string {
	if result == nil {
		return nil
	}
	bases := make([]string, 0, len(result.EmittedFiles))
	for _, path := range result.EmittedFiles {
		bases = append(bases, filepath.Base(path))
	}
	slices.Sort(bases)
	return bases
}
