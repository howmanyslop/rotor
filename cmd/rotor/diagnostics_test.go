package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"rotor/internal/compile"
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

	// When `sloptor diagnostics --json` runs over it
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

func TestCmdDiagnosticsReportsOverlayMatchCount(t *testing.T) {
	// Given a project and an overlay for one of its files
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			filepath.Join(dir, "src", "main.ts"): "export const replaced = 2;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then the count of overlays that actually matched a file is reported, so a
	// consumer can assert its edit was really compiled
	if got := decodeDiagnosticsResult(t, output).OverlayMatches; got != 1 {
		t.Errorf("overlayMatches = %d, want 1", got)
	}
}

func TestCmdDiagnosticsUnmatchedOverlayFails(t *testing.T) {
	// Given an overlay naming a file that is not in the project
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			filepath.Join(dir, "src", "typo.ts"): "export const nothing = 1;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then it fails loudly instead of reporting a green census of the
	// unmodified tree
	if code != 1 {
		t.Fatalf("exit = %d, want 1; output:\n%s", code, output)
	}
	if res := decodeDiagnosticsResult(t, output); res.OK {
		t.Error("ok = true on a census whose overlay matched nothing")
	}
}

func TestCmdDiagnosticsRejectsUnknownStdinFields(t *testing.T) {
	// Given a request with a typo'd wrapper key — it would otherwise parse to
	// an empty overlay set and census the unmodified tree
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	request := `{"overlay":{"` + filepath.ToSlash(filepath.Join(dir, "src", "main.ts")) + `":"export const x = 1;\n"}}`

	// When the census runs
	_, code := captureStdout(t, func() int {
		return withStdin(t, request, func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then it is rejected
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestReadDiagnosticsRequestAcceptsTheKnownShape(t *testing.T) {
	request, err := readDiagnosticsRequest(strings.NewReader(`{"overlays":{"/a/b.ts":"x"}}`))
	if err != nil {
		t.Fatalf("readDiagnosticsRequest: %v", err)
	}
	if request.Overlays["/a/b.ts"] != "x" {
		t.Errorf("overlays = %v, want the one entry", request.Overlays)
	}
}

func TestCmdDiagnosticsOverlayPositionsComeFromTheOverlay(t *testing.T) {
	// Given a one-line file on disk, and an overlay that appends a type error
	// well past the end of it — positions resolved against disk would be lost
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	mainPath := filepath.Join(dir, "src", "main.ts")
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			mainPath: "export const clean = 1;\n\n\n\nexport const broken: string = 5;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then the diagnostic points at the overlay's line 5, not at nothing
	res := decodeDiagnosticsResult(t, output)
	file := diagnosticsByFile(res)["main.ts"]
	if len(file.Diagnostics) == 0 {
		t.Fatalf("main.ts carries no diagnostics: %+v", file)
	}
	d := file.Diagnostics[0]
	if d.Line != 5 {
		t.Errorf("line = %d, want 5 (the overlay's broken line)", d.Line)
	}
	if d.Col != 14 {
		t.Errorf("col = %d, want 14 (the `broken` identifier)", d.Col)
	}
}

func TestCmdDiagnosticsPositionsAgreeWithTheDiskReader(t *testing.T) {
	// Given a type error in a file with nothing overlaid on it
	dir := writeDiagnosticsProject(t, map[string]string{
		"main.ts": "export const clean = 1;\n\nexport const broken: string = 5;\n",
	})

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then its position matches what build's and check's disk-reading resolver
	// would have produced — the two must not drift apart
	res := decodeDiagnosticsResult(t, output)
	file := diagnosticsByFile(res)["main.ts"]
	if len(file.Diagnostics) == 0 {
		t.Fatalf("main.ts carries no diagnostics: %+v", file)
	}
	d := file.Diagnostics[0]
	if d.Line != 3 || d.Col != 14 {
		t.Errorf("line/col = %d/%d, want 3/14", d.Line, d.Col)
	}
}

func TestCmdDiagnosticsPositionsAreCorrectOnABOMFile(t *testing.T) {
	// Given the same source as TestCmdDiagnosticsPositionsAgreeWithTheDiskReader
	// but prefixed with a UTF-8 BOM
	const utf8BOM = "\ufeff"
	dir := writeDiagnosticsProject(t, nil)
	mustWrite(t, filepath.Join(dir, "src", "main.ts"),
		utf8BOM+"export const clean = 1;\n\nexport const broken: string = 5;\n")

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then the position counts the source the compiler saw, whose BOM is
	// already stripped: `broken` is still line 3, column 14, exactly as it is
	// without the BOM. build's disk-reading lineColOf counts the three raw BOM
	// bytes as well and answers column 11 for the same file — a pre-existing
	// bug left alone here because fixing it would move `build --json` output.
	// This resolver is the correct one.
	res := decodeDiagnosticsResult(t, output)
	file := diagnosticsByFile(res)["main.ts"]
	if len(file.Diagnostics) == 0 {
		t.Fatalf("main.ts carries no diagnostics: %+v", file)
	}
	if d := file.Diagnostics[0]; d.Line != 3 || d.Col != 14 {
		t.Errorf("line/col = %d/%d, want 3/14", d.Line, d.Col)
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
	// `sloptor check`'s rotor.d.ts
	after := treeEntries(t, dir)
	if strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("tree changed:\nbefore:\n%s\nafter:\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func TestCmdDiagnosticsHonorsTheRbxtsKey(t *testing.T) {
	// Given a project whose package name is NOT scoped, so it is inferred as a
	// model and needs a Rojo file — and whose tsconfig `rbxts` key overrides
	// the type to package instead
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"unscoped-fixture"}`)
	config := mustRead(t, filepath.Join(dir, "tsconfig.json"))
	mustWrite(t, filepath.Join(dir, "tsconfig.json"),
		strings.Replace(config, "{\n", "{\n\t\"rbxts\": { \"type\": \"package\" },\n", 1))

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})

	// Then the override applied and the project censused
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	if res := decodeDiagnosticsResult(t, output); !res.OK {
		t.Errorf("ok = false; diagnostics: %+v", res.Diagnostics)
	}
}

func TestCmdDiagnosticsNeverAllowsCommentDirectives(t *testing.T) {
	// Given a project that turns allowCommentDirectives ON and a file using
	// one — a census that honored it would silently under-report
	dir := writeDiagnosticsProject(t, map[string]string{
		"main.ts": "// @ts-ignore\nexport const clean = 1;\n",
	})
	config := mustRead(t, filepath.Join(dir, "tsconfig.json"))
	mustWrite(t, filepath.Join(dir, "tsconfig.json"),
		strings.Replace(config, "{\n", "{\n\t\"rbxts\": { \"allowCommentDirectives\": true },\n", 1))

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then the directive is still reported, against the file and line it is on
	res := decodeDiagnosticsResult(t, output)
	file := diagnosticsByFile(res)["main.ts"]
	if file.Outcome != "transformerDiagnostic" {
		t.Fatalf("main.ts outcome = %q (diags %+v), want transformerDiagnostic", file.Outcome, file.Diagnostics)
	}
	if len(file.Diagnostics) == 0 {
		t.Fatalf("main.ts carries no diagnostics: %+v", file)
	}
	d := file.Diagnostics[0]
	if !strings.Contains(filepath.ToSlash(d.File), "main.ts") {
		t.Errorf("file = %q, want it to name main.ts", d.File)
	}
	if d.Line != 1 {
		t.Errorf("line = %d, want 1 (the directive's own line)", d.Line)
	}
}

func TestCmdDiagnosticsJSONCarriesDiagnosticCodes(t *testing.T) {
	// Given one file TypeScript rejects and one the transformer rejects
	dir := writeDiagnosticsProject(t, map[string]string{
		"typebad.ts": "export const s: string = 5;\n",
		"noany.ts":   "declare const loose: any;\nexport const taken = loose.field;\n",
	})

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}

	// Then each diagnostic names its own class. A file's outcome cannot answer
	// this: a file can carry a type error AND a transformer diagnostic at once,
	// and outcome reports only the most severe. Without the code the message is
	// the only thing a consumer can group or route on.
	byName := diagnosticsByFile(decodeDiagnosticsResult(t, output))
	for _, tc := range []struct {
		name     string
		wantCode string
	}{
		{"typebad.ts", "TS2322"},
		{"noany.ts", "noAny"},
	} {
		file, ok := byName[tc.name]
		if !ok || len(file.Diagnostics) == 0 {
			t.Errorf("%s carries no diagnostics: %+v", tc.name, file)
			continue
		}
		if got := file.Diagnostics[0].Code; got != tc.wantCode {
			t.Errorf("%s code = %q, want %q (message %q)", tc.name, got, tc.wantCode, file.Diagnostics[0].Message)
		}
	}
}

func TestCmdDiagnosticsOmitsTheCodeKeyWhenThereIsNone(t *testing.T) {
	// Given a run that fails as a whole rather than at a diagnostic — an
	// overlay matching no file — so the reported failure is a bare message that
	// never had a code to carry
	dir := writeDiagnosticsProject(t, map[string]string{"main.ts": "export const clean = 1;\n"})
	request, err := json.Marshal(map[string]any{
		"overlays": map[string]string{
			filepath.Join(dir, "src", "typo.ts"): "export const nothing = 1;\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, string(request), func() int {
			return cmdDiagnostics([]string{"--project", dir, "--json"})
		})
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1; output:\n%s", code, output)
	}

	// Then the key is absent rather than present-and-empty, so an entry that
	// never had a code reads exactly as it did before the field existed
	if strings.Contains(output, `"code"`) {
		t.Errorf("output carries a code key for a codeless diagnostic:\n%s", output)
	}
	res := decodeDiagnosticsResult(t, output)
	if len(res.Diagnostics) == 0 {
		t.Fatal("setup failure reported no diagnostic at all")
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

func TestParseDiagnosticsArgs(t *testing.T) {
	intPtr := func(value int) *int { return &value }
	tests := []struct {
		name         string
		args         []string
		wantProject  string
		wantJSON     bool
		wantCheckers *int
	}{
		{name: "omitted", args: nil, wantProject: "."},
		{name: "json only", args: []string{"--json"}, wantProject: ".", wantJSON: true},
		{name: "positional", args: []string{"project"}, wantProject: "project"},
		{name: "project separated", args: []string{"--project", "proj"}, wantProject: "proj"},
		{name: "project equals", args: []string{"--project=proj"}, wantProject: "proj"},
		{name: "project short", args: []string{"-p", "proj"}, wantProject: "proj"},
		{
			name:         "checkers separated",
			args:         []string{"--checkers", "3", "project"},
			wantProject:  "project",
			wantCheckers: intPtr(3),
		},
		{
			name:         "checkers equals",
			args:         []string{"--checkers=3"},
			wantProject:  ".",
			wantCheckers: intPtr(3),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDiagnosticsArgsForTest(t, tt.args)
			if got.project != tt.wantProject {
				t.Errorf("project = %q, want %q", got.project, tt.wantProject)
			}
			if got.jsonOut != tt.wantJSON {
				t.Errorf("jsonOut = %t, want %t", got.jsonOut, tt.wantJSON)
			}
			if (got.checkers == nil) != (tt.wantCheckers == nil) {
				t.Fatalf("checkers = %v, want %v", got.checkers, tt.wantCheckers)
			}
			if got.checkers != nil && *got.checkers != *tt.wantCheckers {
				t.Errorf("checkers = %d, want %d", *got.checkers, *tt.wantCheckers)
			}
		})
	}

	// Usage errors surface through the command runner (help is Cobra's own
	// exit-0 path).
	errorCases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "duplicate positional", args: []string{"project", "other"}, wantErr: "accepts at most 1 arg"},
		{name: "project missing value", args: []string{"--project"}, wantErr: `flag "--project" needs a value`},
		{
			// The bug this pins: consuming the next token unconditionally set
			// the project to "--json", then walked up to the cwd and censused
			// a different project with no error and no JSON.
			name:    "project followed by a flag",
			args:    []string{"--project", "--json"},
			wantErr: `flag "--project" needs a value`,
		},
		{name: "project empty equals", args: []string{"--project="}, wantErr: `flag "--project" needs a value`},
		{name: "triple dashed project", args: []string{"---project", "proj"}, wantErr: "bad flag syntax"},
		{name: "checkers missing value", args: []string{"--checkers"}, wantErr: "flag needs an argument: --checkers"},
		{name: "checkers non integer", args: []string{"--checkers=many"}, wantErr: "must be a positive integer"},
		// --builders is no longer unknown; see
		// TestParseDiagnosticsArgsRejectsBuildersWithoutBuild for what it does
		// on its own now. --watch is a build flag this command has no meaning
		// for.
		{name: "unknown flag", args: []string{"--watch"}, wantErr: "unknown flag: --watch"},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			stderr, code := captureStderr(t, func() int { return cmdDiagnostics(tt.args) })
			if code != 1 {
				t.Fatalf("cmdDiagnostics(%v) exit = %d, want 1; stderr:\n%s", tt.args, code, stderr)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Fatalf("cmdDiagnostics(%v) stderr = %q, want substring %q", tt.args, stderr, tt.wantErr)
			}
		})
	}

	if code := cmdDiagnostics([]string{"-h"}); code != 0 {
		t.Errorf("help exit = %d, want 0", code)
	}
}

func TestWriteDiagnosticsTextRendersEveryFailingFile(t *testing.T) {
	// Given a census with one file of each failing kind and a project-level
	// diagnostic
	census := &compile.ProjectDiagnostics{
		Transformed: 2,
		Files: []compile.FileDiagnostics{
			{FileName: "/p/src/clean.ts", Outcome: compile.FileOutcomeOK, Transformed: true},
			{
				FileName:    "/p/src/noany.ts",
				Outcome:     compile.FileOutcomeTransformerDiagnostic,
				Transformed: true,
				Diagnostics: []compile.DiagnosticInfo{{Message: "not supported\nSuggestion: do something else"}},
			},
			{
				FileName:      "/p/src/panicking.ts",
				Outcome:       compile.FileOutcomeInternalCompilerError,
				InternalError: &compile.InternalCompilerError{Value: "identifier has no symbol"},
			},
		},
		Diagnostics: []compile.DiagnosticInfo{{Message: "a project-level problem"}},
	}

	// When it is rendered as text
	var out, errOut strings.Builder
	writeDiagnosticsText(&out, &errOut, []*compile.ProjectDiagnostics{census}, nil, 7*time.Millisecond, false)

	// Then every failing file is named with its outcome, the clean one is not,
	// multi-line diagnostics are flattened, and the summary counts them
	text := out.String()
	for _, want := range []string{
		"transformerDiagnostic —",
		"noany.ts",
		"not supported Suggestion: do something else",
		"internalCompilerError —",
		"panicking.ts",
		"internal compiler error: identifier has no symbol",
		"(project)",
		"a project-level problem",
		"3 files, 2 transformed in 7 ms — ok 1, typeError 0, transformerDiagnostic 1, internalCompilerError 1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "clean.ts") {
		t.Errorf("a clean file was listed:\n%s", text)
	}
	if errOut.Len() != 0 {
		t.Errorf("a successful census wrote to stderr: %q", errOut.String())
	}
}

func TestWriteDiagnosticsTextRoutesFailureToStderr(t *testing.T) {
	// Given a census that could not be produced at all
	census := &compile.ProjectDiagnostics{
		Diagnostics: []compile.DiagnosticInfo{{Message: "no tsconfig.json\nhere"}},
	}

	// When it is rendered as text
	var out, errOut strings.Builder
	writeDiagnosticsText(&out, &errOut, []*compile.ProjectDiagnostics{census}, errors.New("setup failed"), time.Millisecond, false)

	// Then nothing lands in the stdout census stream, and the failure carries
	// the command prefix every other failure of this command uses
	if out.Len() != 0 {
		t.Errorf("failure text leaked into stdout: %q", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "sloptor diagnostics: census failed: setup failed") ||
		!strings.Contains(got, "  no tsconfig.json here") {
		t.Errorf("stderr = %q", got)
	}
}

func TestOneLineFlattensEmbeddedNewlines(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "plain", want: "plain"},
		{in: "message\nSuggestion: fix it", want: "message Suggestion: fix it"},
		{in: "windows\r\nnewline", want: "windows newline"},
		{in: "a\n\nb", want: "a  b"},
	}
	for _, tt := range tests {
		if got := oneLine(tt.in); got != tt.want {
			t.Errorf("oneLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCmdDiagnosticsTextOutputRunsEndToEnd(t *testing.T) {
	// Given a project with a failing file and no --json
	dir := writeDiagnosticsProject(t, map[string]string{
		"clean.ts": "export const clean = 1;\n",
		"noany.ts": "declare const loose: any;\nexport const taken = loose.field;\n",
	})

	// When the census runs
	output, code := captureStdout(t, func() int {
		return withStdin(t, "", func() int {
			return cmdDiagnostics([]string{"--project", dir})
		})
	})

	// Then the text report names the failing file and summarizes
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, output)
	}
	if !strings.Contains(output, "transformerDiagnostic") || !strings.Contains(output, "noany.ts") {
		t.Errorf("text output does not report the failing file:\n%s", output)
	}
	if !strings.Contains(output, "2 files, 2 transformed") {
		t.Errorf("text output has no summary line:\n%s", output)
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
	var out, errOut strings.Builder
	if code := execute([]string{"--help"}, cliStreams{in: strings.NewReader(""), out: &out, err: &errOut}); code != 0 {
		t.Fatalf("root --help exit = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "diagnostics") {
		t.Error("root help does not mention the diagnostics subcommand")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
