package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeDiagnosticsProject lays down a package-type project (a scoped name, so
// no Rojo project file is needed) whose src/ holds one file per entry of files
// plus the noLib global stubs.
func writeDiagnosticsProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out"
	},
	"include": ["src"]
}`
	mustWrite(t, filepath.Join(dir, "tsconfig.json"), tsconfig)
	mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"@scope/diagnostics-fixture"}`)
	mustWrite(t, filepath.Join(dir, "src", "globals.d.ts"), noLibGlobalStubs)
	for name, text := range files {
		mustWrite(t, filepath.Join(dir, "src", name), text)
	}
	return dir
}

// withStdin runs fn with os.Stdin replaced by a pipe carrying text.
func withStdin(t *testing.T, text string, fn func() int) int {
	t.Helper()
	prev := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	go func() {
		_, _ = io.WriteString(w, text)
		_ = w.Close()
	}()
	defer func() { os.Stdin = prev; _ = r.Close() }()
	return fn()
}

func decodeDiagnosticsResult(t *testing.T, output string) jsonDiagnosticsResult {
	t.Helper()
	var res jsonDiagnosticsResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	return res
}

func diagnosticsByFile(res jsonDiagnosticsResult) map[string]jsonFileDiagnostics {
	byName := make(map[string]jsonFileDiagnostics, len(res.FileDiagnostics))
	for _, file := range res.FileDiagnostics {
		byName[filepath.Base(filepath.FromSlash(file.File))] = file
	}
	return byName
}

func TestCmdDiagnosticsJSONClassifiesEveryFile(t *testing.T) {
	// Given a project where files fail in every way the census classifies
	dir := writeDiagnosticsProject(t, map[string]string{
		"clean.ts":     "export const clean = 1;\n",
		"typebad.ts":   "export const s: string = 5;\n",
		"noany.ts":     "declare const loose: any;\nexport const taken = loose.field;\n",
		"panicking.ts": "export const x = neverDeclared;\n",
	})

	// When `rotor diagnostics --json` runs over it
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then the census ran (exit 0 — the command reports, it does not gate) and
	// every file carries its outcome
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	res := decodeDiagnosticsResult(t, output)
	if res.OK {
		t.Error("ok = true on a project with diagnostics")
	}
	byName := diagnosticsByFile(res)
	want := map[string]string{
		"clean.ts":     "ok",
		"globals.d.ts": "",
		"typebad.ts":   "typeError",
		"noany.ts":     "transformerDiagnostic",
		"panicking.ts": "internalCompilerError",
	}
	for name, wantOutcome := range want {
		if wantOutcome == "" {
			if _, ok := byName[name]; ok {
				t.Errorf("%s should not be censused (declaration file)", name)
			}
			continue
		}
		file, ok := byName[name]
		if !ok {
			t.Errorf("%s missing from fileDiagnostics", name)
			continue
		}
		if file.Outcome != wantOutcome {
			t.Errorf("%s outcome = %q (diags %+v), want %q", name, file.Outcome, file.Diagnostics, wantOutcome)
		}
	}
	if res.Files != 4 {
		t.Errorf("files = %d, want 4", res.Files)
	}
	if res.Transformed != 3 {
		t.Errorf("transformed = %d, want 3 (the panicking file never finished)", res.Transformed)
	}
}

func TestCmdDiagnosticsJSONCleanProject(t *testing.T) {
	// Given a project with nothing wrong with it
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then it reports ok with every file transformed
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	res := decodeDiagnosticsResult(t, output)
	if !res.OK {
		t.Errorf("ok = false on a clean project; diagnostics: %+v", res.Diagnostics)
	}
	if res.Transformed != res.Files || res.Files != 1 {
		t.Errorf("transformed/files = %d/%d, want 1/1", res.Transformed, res.Files)
	}
	if res.Diagnostics == nil {
		t.Error("diagnostics must be [] not null")
	}
	if res.FileDiagnostics == nil {
		t.Error("fileDiagnostics must be [] not null")
	}
}

func TestCmdDiagnosticsOverlaysFromStdin(t *testing.T) {
	// Given a project that is clean on disk
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	mainPath := filepath.Join(dir, "src", "main.ts")
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			mainPath: "declare const loose: any;\nexport const taken = loose.field;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When an overlay that breaks it arrives on stdin
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then the census reports the overlay's failure, not the disk's success
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	res := decodeDiagnosticsResult(t, output)
	file, ok := diagnosticsByFile(res)["main.ts"]
	if !ok {
		t.Fatalf("main.ts missing from fileDiagnostics: %+v", res.FileDiagnostics)
	}
	if file.Outcome != "transformerDiagnostic" {
		t.Errorf("main.ts outcome = %q, want transformerDiagnostic", file.Outcome)
	}
	if len(file.Diagnostics) == 0 {
		t.Error("main.ts carries no diagnostics")
	}
}

func TestCmdDiagnosticsInternalErrorCarriesStack(t *testing.T) {
	// Given a file that makes the transformer panic
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const x = neverDeclared;\n"})

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then the internal error is reported structurally, stack included — a
	// consumer never has to match on a message prefix
	res := decodeDiagnosticsResult(t, output)
	file := diagnosticsByFile(res)["main.ts"]
	if file.Outcome != "internalCompilerError" {
		t.Fatalf("main.ts outcome = %q, want internalCompilerError", file.Outcome)
	}
	if file.InternalError == nil {
		t.Fatal("internalError is absent")
	}
	if !strings.Contains(file.InternalError.Message, "identifier has no symbol") {
		t.Errorf("internalError.message = %q", file.InternalError.Message)
	}
	if !strings.Contains(file.InternalError.Stack, "rotor/internal/transformer") {
		t.Errorf("internalError.stack does not name a transformer frame:\n%s", file.InternalError.Stack)
	}
}

func TestCmdDiagnosticsWritesNothingToDisk(t *testing.T) {
	// Given a project and a snapshot of its tree
	dir := writeDiagnosticsProject(t, map[string]string{
		"main.ts":    "export const clean = 1;\n",
		"typebad.ts": "export const s: string = 5;\n",
	})
	before := treeEntries(t, dir)

	// When the census runs
	if _, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	// Then nothing was written: no outDir, no include folder, and none of
	// `rotor check`'s rotor.d.ts
	after := treeEntries(t, dir)
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("tree changed:\nbefore:\n%s\nafter:\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func TestCmdDiagnosticsSetupFailureExitsNonZero(t *testing.T) {
	// Given a directory with no project in it
	dir := t.TempDir()

	// When the census is asked for one
	_, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then the command fails: it could not produce a census at all, which is
	// the only condition that makes it exit nonzero
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestCmdDiagnosticsRejectsUnknownFlag(t *testing.T) {
	if code := cmdDiagnostics([]string{"--nope"}); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestCmdDiagnosticsMalformedStdinExitsNonZero(t *testing.T) {
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})

	_, code := captureStdout(t, func() int {
		return withStdin(t, "{ not json", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestDiagnosticsRoutedFromMain(t *testing.T) {
	// Given a clean project
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})

	// When the subcommand is reached through the top-level dispatch
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return run([]string{"diagnostics", "--project", dir, "--json"})
		})
	})

	// Then it ran
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	if !decodeDiagnosticsResult(t, output).OK {
		t.Error("ok = false on a clean project")
	}
}

func TestUsageMentionsDiagnostics(t *testing.T) {
	var sb strings.Builder
	usage(&sb)
	if !strings.Contains(sb.String(), "diagnostics") {
		t.Error("usage does not mention the diagnostics subcommand")
	}
}

// treeEntries lists every path under dir, sorted, as "<rel> <size>".
func treeEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		size := "dir"
		if !entry.IsDir() {
			size = strconv.FormatInt(info.Size(), 10)
		}
		out = append(out, filepath.ToSlash(rel)+" "+size)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}
