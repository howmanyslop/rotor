package main

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rotor/internal/compile"
	"rotor/internal/luau/cst"
)

// TestParseBuildArgs covers the rbxtsc-compatible flag surface
// (CLI/commands/build.ts L49-118).
func TestParseBuildArgs(t *testing.T) {
	t.Run("no args defaults project to dot with empty partial", func(t *testing.T) {
		got, err := parseBuildArgs(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.project != "." {
			t.Errorf("project = %q, want \".\"", got.project)
		}
		if got.opts != (partialProjectOptions{}) {
			t.Errorf("opts = %+v, want all-nil (no yargs defaults below --project)", got.opts)
		}
	})

	t.Run("usePolling without watch errors", func(t *testing.T) {
		_, err := parseBuildArgs([]string{"--usePolling"})
		if err == nil || err.Error() != "Implications failed:\n usePolling -> watch" {
			t.Errorf("err = %v, want yargs implication error", err)
		}
	})

	t.Run("usePolling with watch ok", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--usePolling", "-w"})
		if err != nil {
			t.Fatal(err)
		}
		if got.opts.usePolling == nil || !*got.opts.usePolling || got.opts.watch == nil || !*got.opts.watch {
			t.Errorf("opts = %+v", got.opts)
		}
	})

	t.Run("boolean negation forms", func(t *testing.T) {
		for _, args := range [][]string{{"--no-luau"}, {"--luau=false"}, {"--luau=0"}} {
			got, err := parseBuildArgs(args)
			if err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if got.opts.luau == nil || *got.opts.luau {
				t.Errorf("%v: luau = %v, want false", args, got.opts.luau)
			}
		}
		got, err := parseBuildArgs([]string{"--optimizedLoops=false"})
		if err != nil {
			t.Fatal(err)
		}
		if got.opts.optimizedLoops == nil || *got.opts.optimizedLoops {
			t.Error("--optimizedLoops=false not parsed")
		}
	})

	t.Run("plain boolean flags set true", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--verbose", "--noInclude", "--logTruthyChanges",
			"--writeOnlyChanged", "--writeTransformedFiles", "--allowCommentDirectives"})
		if err != nil {
			t.Fatal(err)
		}
		for name, p := range map[string]*bool{
			"verbose":                got.opts.verbose,
			"noInclude":              got.opts.noInclude,
			"logTruthyChanges":       got.opts.logTruthyChanges,
			"writeOnlyChanged":       got.opts.writeOnlyChanged,
			"writeTransformedFiles":  got.opts.writeTransformedFiles,
			"allowCommentDirectives": got.opts.allowCommentDirectives,
		} {
			if p == nil || !*p {
				t.Errorf("--%s not parsed", name)
			}
		}
	})

	t.Run("type choices", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--type", "model"})
		if err != nil {
			t.Fatal(err)
		}
		if got.opts.typeName == nil || *got.opts.typeName != "model" {
			t.Errorf("type = %v", got.opts.typeName)
		}
		if _, err := parseBuildArgs([]string{"--type", "bogus"}); err == nil {
			t.Error("invalid --type accepted")
		}
		if _, err := parseBuildArgs([]string{"--type"}); err == nil {
			t.Error("--type with no value accepted (yargs choices reject \"\")")
		}
	})

	t.Run("string flag forms", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"-p", "proj", "-i", "inc", "--rojo=custom.project.json"})
		if err != nil {
			t.Fatal(err)
		}
		if got.project != "proj" {
			t.Errorf("project = %q", got.project)
		}
		if got.opts.includePath == nil || *got.opts.includePath != "inc" {
			t.Errorf("includePath = %v", got.opts.includePath)
		}
		if got.opts.rojo == nil || *got.opts.rojo != "custom.project.json" {
			t.Errorf("rojo = %v", got.opts.rojo)
		}
	})

	t.Run("rojo with no value is empty string", func(t *testing.T) {
		// QUIRK: `--rojo` / `--rojo ""` yields "" which falls through to
		// Rojo config auto-discovery (createProjectData.ts L33-43).
		got, err := parseBuildArgs([]string{"--rojo", "--verbose"})
		if err != nil {
			t.Fatal(err)
		}
		if got.opts.rojo == nil || *got.opts.rojo != "" {
			t.Errorf("rojo = %v, want present-and-empty", got.opts.rojo)
		}
		if got.opts.verbose == nil || !*got.opts.verbose {
			t.Error("--verbose after valueless --rojo not parsed")
		}
	})

	t.Run("positional project path", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"some/dir", "--verbose"})
		if err != nil {
			t.Fatal(err)
		}
		if got.project != "some/dir" {
			t.Errorf("project = %q", got.project)
		}
	})

	t.Run("positional plus --project errors", func(t *testing.T) {
		if _, err := parseBuildArgs([]string{"a", "-p", "b"}); err == nil {
			t.Error("conflicting project paths accepted")
		}
	})

	t.Run("unknown flag errors", func(t *testing.T) {
		if _, err := parseBuildArgs([]string{"--bogus"}); err == nil {
			t.Error("unknown flag accepted")
		}
	})
}

// TestUsageErrorsExitOne pins the Phase 4 exit-code policy change: usage
// errors exit 1, matching upstream rbxtsc (CLI/cli.ts L30-35), not rotor's
// former 2.
func TestUsageErrorsExitOne(t *testing.T) {
	if got := run([]string{"frobnicate"}); got != 1 {
		t.Errorf("unknown command exit = %d, want 1", got)
	}
	if got := cmdBuild([]string{"--bogus"}); got != 1 {
		t.Errorf("unknown build flag exit = %d, want 1", got)
	}
	if got := cmdBuild([]string{"--usePolling"}); got != 1 {
		t.Errorf("--usePolling without --watch exit = %d, want 1", got)
	}
	if got := cmdCheck([]string{"--bogus"}); got != 1 {
		t.Errorf("unknown check flag exit = %d, want 1", got)
	}
}

func TestParseBuildArgsJSON(t *testing.T) {
	got, err := parseBuildArgs([]string{"--json", "."})
	if err != nil {
		t.Fatal(err)
	}
	if !got.jsonOut {
		t.Error("--json not parsed")
	}
}

func TestBuildModeArgs(t *testing.T) {
	// Given
	tests := []struct {
		name    string
		args    []string
		wantErr string
		build   bool
		path    string
		emit    bool
	}{
		{name: "short build path", args: []string{"-b", "tsconfig.solution.json"}, build: true, path: "tsconfig.solution.json"},
		{name: "bare build", args: []string{"--build"}, build: true},
		{name: "declaration only build", args: []string{"--build", "--emitDeclarationOnly"}, build: true, emit: true},
		{name: "declaration only requires build", args: []string{"--emitDeclarationOnly"}, wantErr: "Implications failed:\n emitDeclarationOnly -> build"},
		{name: "declaration watch incompatible", args: []string{"--build", "--watch", "--emitDeclarationOnly"}, wantErr: "--build --watch is incompatible with --emitDeclarationOnly (no Luau emit to incrementally watch)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got, err := parseBuildArgs(tt.args)

			// Then
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseBuildArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.build != tt.build || got.buildPath != tt.path || got.emitDeclarationOnly != tt.emit {
				t.Errorf("parseBuildArgs(%v) = %+v", tt.args, got)
			}
		})
	}
}

func TestBuildModeArgs_emitDeclarationOnly_skipsLuau(t *testing.T) {
	// Given
	dir := writeBuildableProject(t, "")
	configPath := filepath.Join(dir, "tsconfig.json")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), `"outDir": "out"`, `"declaration": true,
		"outDir": "out"`, 1))
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatal(err)
	}

	// When
	_, code := captureStdout(t, func() int {
		return cmdBuild([]string{"--build", "--emitDeclarationOnly", dir})
	})

	// Then
	if code != 0 {
		t.Fatalf("declaration-only build exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.luau")); !os.IsNotExist(err) {
		t.Errorf("declaration-only build wrote main.luau: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.d.ts")); err != nil {
		t.Errorf("declaration-only build did not write main.d.ts: %v", err)
	}
}

// noLibGlobalStubs declares the fundamental global types the checker needs under
// noLib; mirrored from internal/compile's test helper so cmd-level tests build
// self-contained projects with no node_modules.
const noLibGlobalStubs = "declare function print(...params: Array<unknown>): void;\n" +
	"interface Array<T> {}\ninterface Boolean {}\ninterface CallableFunction {}\n" +
	"interface Function {}\ninterface IArguments {}\ninterface NewableFunction {}\n" +
	"interface Number {}\ninterface Object {}\ninterface RegExp {}\ninterface String {}\n"

// writeBuildableProject writes a minimal, self-contained Package project (a
// scoped name needs no Rojo config) that builds cleanly. mainSrc overrides
// src/main.ts when non-empty (e.g. to inject a diagnostic).
func writeBuildableProject(t *testing.T, mainSrc string) string {
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
	mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"@scope/build-json-fixture"}`)
	mustWrite(t, filepath.Join(dir, "src", "globals.d.ts"), noLibGlobalStubs)
	if mainSrc == "" {
		mainSrc = "export {};\n"
	}
	mustWrite(t, filepath.Join(dir, "src", "main.ts"), mainSrc)
	return dir
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// wrote, plus fn's return value.
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := fn()
	_ = w.Close()
	os.Stdout = prev
	data, _ := io.ReadAll(r)
	return string(data), code
}

func TestCmdBuildJSONClean(t *testing.T) {
	dir := writeBuildableProject(t, "")

	output, code := captureStdout(t, func() int {
		return cmdBuild([]string{"--json", dir})
	})
	if code != 0 {
		t.Fatalf("cmdBuild --json (clean) exit = %d, want 0; output:\n%s", code, output)
	}

	var res jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if !res.OK {
		t.Errorf("ok = false on a clean build; diags: %+v", res.Diagnostics)
	}
	if res.Version == "" {
		t.Error("version is empty")
	}
	if res.Files <= 0 {
		t.Errorf("files = %d, want > 0", res.Files)
	}
	if res.Diagnostics == nil {
		t.Error("diagnostics must be [] not null")
	}
}

func TestCmdBuildJSONWithDiagnostic(t *testing.T) {
	// A type error: assign a number to a string-typed const.
	dir := writeBuildableProject(t, "export const s: string = 5;\n")

	output, code := captureStdout(t, func() int {
		return cmdBuild([]string{"--json", dir})
	})
	if code != 1 {
		t.Fatalf("cmdBuild --json (error) exit = %d, want 1; output:\n%s", code, output)
	}

	var res jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if res.OK {
		t.Error("ok = true on a failing build")
	}
	if len(res.Diagnostics) == 0 {
		t.Error("expected at least one diagnostic")
	}
	if res.Diagnostics[0].Severity != "error" {
		t.Errorf("severity = %q, want error", res.Diagnostics[0].Severity)
	}
	if res.Diagnostics[0].Message == "" {
		t.Error("diagnostic message is empty")
	}
	// Structured location: file/line/col must be populated for a TS type error.
	if res.Diagnostics[0].File == "" {
		t.Error("diagnostic file is empty (want structured location)")
	}
	if res.Diagnostics[0].Line == 0 {
		t.Error("diagnostic line is 0 (want ≥ 1)")
	}
	if res.Diagnostics[0].Col == 0 {
		t.Error("diagnostic col is 0 (want ≥ 1)")
	}
}

// TestRenderDiagFrames is a focused unit test for renderDiagFrames: it creates
// a synthetic .ts file, constructs a DiagnosticInfo pointing at a span in it,
// and asserts the rendered output contains the source line and a caret.
func TestRenderDiagFrames(t *testing.T) {
	dir := t.TempDir()
	src := "export const s: string = 5;\n"
	tsFile := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(tsFile, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Offset 25 points at "5" (the numeric literal): "export const s: string = "
	diags := []compile.DiagnosticInfo{
		{
			Message:  "Type 'number' is not assignable to type 'string'.",
			FileName: tsFile,
			Offset:   25,
			Len:      1,
		},
	}

	out := renderDiagFrames(os.Stderr, diags, 0)

	// The rendered frame must contain the source line.
	if !strings.Contains(out, "export const s: string = 5;") {
		t.Errorf("frame does not contain source line\noutput:\n%s", out)
	}
	// The rendered frame must contain a caret pointing at the error.
	if !strings.Contains(out, "^") {
		t.Errorf("frame does not contain caret '^'\noutput:\n%s", out)
	}
	// The rendered frame must contain the '-->' locator arrow.
	if !strings.Contains(out, "-->") {
		t.Errorf("frame does not contain '-->' locator\noutput:\n%s", out)
	}
	// The summary line must mention 'error'.
	if !strings.Contains(out, "error") {
		t.Errorf("frame does not contain 'error' summary\noutput:\n%s", out)
	}
}

// TestParseBuildArgsMaxErrors verifies that --max-errors is parsed correctly.
func TestParseBuildArgsMaxErrors(t *testing.T) {
	t.Run("default is 50", func(t *testing.T) {
		got, err := parseBuildArgs(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.maxErrors != 50 {
			t.Errorf("maxErrors = %d, want 50", got.maxErrors)
		}
	})

	t.Run("--max-errors value", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--max-errors", "10"})
		if err != nil {
			t.Fatal(err)
		}
		if got.maxErrors != 10 {
			t.Errorf("maxErrors = %d, want 10", got.maxErrors)
		}
	})

	t.Run("--max-errors=N form", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--max-errors=5"})
		if err != nil {
			t.Fatal(err)
		}
		if got.maxErrors != 5 {
			t.Errorf("maxErrors = %d, want 5", got.maxErrors)
		}
	})

	t.Run("--max-errors 0 means unlimited", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--max-errors", "0"})
		if err != nil {
			t.Fatal(err)
		}
		if got.maxErrors != 0 {
			t.Errorf("maxErrors = %d, want 0", got.maxErrors)
		}
	})

	t.Run("--max-errors with negative value errors", func(t *testing.T) {
		_, err := parseBuildArgs([]string{"--max-errors", "-1"})
		if err == nil {
			t.Error("expected error for negative --max-errors")
		}
	})
}

// TestParseBuildArgsWatchDXFlags verifies the watch DX booleans --bell and
// --no-clear (and their defaults).
func TestParseBuildArgsWatchDXFlags(t *testing.T) {
	t.Run("defaults: bell off, clear on", func(t *testing.T) {
		got, err := parseBuildArgs(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.bell {
			t.Error("bell default = true, want false")
		}
		if !got.clearScreen {
			t.Error("clearScreen default = false, want true")
		}
	})
	t.Run("--bell enables the bell", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--bell"})
		if err != nil {
			t.Fatal(err)
		}
		if !got.bell {
			t.Error("--bell not parsed")
		}
	})
	t.Run("--no-clear disables clear-on-rebuild", func(t *testing.T) {
		got, err := parseBuildArgs([]string{"--no-clear"})
		if err != nil {
			t.Fatal(err)
		}
		if got.clearScreen {
			t.Error("--no-clear did not disable clearScreen")
		}
	})
}

// TestCmdBuildMinify verifies that `rotor build --minify` produces smaller,
// still-valid Luau with the header comment stripped, while a normal build keeps
// it — i.e. the flag is what minifies, and only when set.
func TestCmdBuildMinify(t *testing.T) {
	src := "export const greeting = \"hi\";\nexport function greet() {\n\treturn greeting;\n}\n"
	normalDir := writeBuildableProject(t, src)
	minDir := writeBuildableProject(t, src)

	if _, code := captureStdout(t, func() int { return cmdBuild([]string{normalDir}) }); code != 0 {
		t.Fatalf("normal build exit = %d", code)
	}
	if _, code := captureStdout(t, func() int { return cmdBuild([]string{"--minify", minDir}) }); code != 0 {
		t.Fatalf("--minify build exit = %d", code)
	}

	normalLuau := collectLuau(t, filepath.Join(normalDir, "out"))
	minLuau := collectLuau(t, filepath.Join(minDir, "out"))
	if len(normalLuau) == 0 || len(minLuau) == 0 {
		t.Fatal("expected .luau outputs in both builds")
	}

	var normalSize, minSize int
	for _, c := range normalLuau {
		normalSize += len(c)
	}
	for p, c := range minLuau {
		minSize += len(c)
		// Minify strips comments, including rotor's header.
		if strings.Contains(c, "-- Compiled with rotor") {
			t.Errorf("%s still carries the header comment (not minified)", p)
		}
		// Minified output must still be valid Luau.
		if _, diags := cst.Parse(c); len(diags) != 0 {
			t.Errorf("%s minified output does not parse: %v", p, diags)
		}
	}
	// A normal build keeps the header comment (proves the flag is the cause).
	keptHeader := false
	for _, c := range normalLuau {
		if strings.Contains(c, "-- Compiled with rotor") {
			keptHeader = true
		}
	}
	if !keptHeader {
		t.Error("normal build should keep the header comment")
	}
	if minSize >= normalSize {
		t.Errorf("minified total (%d B) not smaller than normal (%d B)", minSize, normalSize)
	}
}

// collectLuau reads every .luau file under dir into a path->content map.
func collectLuau(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".luau") {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		out[p] = string(data)
		return nil
	})
	return out
}

// TestCmdBuildFailureCodeFrame verifies that a build failure renders code frames
// (containing '-->' and '^') to stderr.
func TestCmdBuildFailureCodeFrame(t *testing.T) {
	// A type error: assign a number to a string-typed const.
	dir := writeBuildableProject(t, "export const s: string = 5;\n")

	stderr, code := captureStderr(t, func() int {
		return cmdBuild([]string{dir})
	})
	if code != 1 {
		t.Fatalf("cmdBuild (error) exit = %d, want 1; stderr:\n%s", code, stderr)
	}
	// The code frame must contain the '-->' locator line.
	if !strings.Contains(stderr, "-->") {
		t.Errorf("stderr does not contain '-->' locator\nstderr:\n%s", stderr)
	}
	// The code frame must contain a caret.
	if !strings.Contains(stderr, "^") {
		t.Errorf("stderr does not contain caret '^'\nstderr:\n%s", stderr)
	}
	// The failure summary must mention 'error'.
	if !strings.Contains(stderr, "error") {
		t.Errorf("stderr does not contain 'error'\nstderr:\n%s", stderr)
	}
}
