package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"rotor/tsgo/checker"
)

// writeCheckableProject writes a minimal project `sloptor check` can typecheck
// without node_modules (noLib + local global stubs). mainSrc overrides
// src/main.ts when non-empty.
func writeCheckableProject(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	tsconfig := `{
	"compilerOptions": {
		"noLib": true,
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
	mustWrite(t, filepath.Join(dir, "src", "globals.d.ts"), noLibGlobalStubs)
	if mainSrc == "" {
		mainSrc = "export {};\n"
	}
	mustWrite(t, filepath.Join(dir, "src", "main.ts"), mainSrc)
	return dir
}

func TestCmdCheckJSONClean(t *testing.T) {
	dir := writeCheckableProject(t, "")

	output, code := captureStdout(t, func() int {
		return cmdCheck([]string{"--json", dir})
	})
	if code != 0 {
		t.Fatalf("cmdCheck --json (clean) exit = %d, want 0; output:\n%s", code, output)
	}

	var res jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if !res.OK {
		t.Errorf("ok = false on a clean check; diags: %+v", res.Diagnostics)
	}
	if res.Files <= 0 {
		t.Errorf("files = %d, want > 0", res.Files)
	}
	if res.Diagnostics == nil {
		t.Error("diagnostics must be [] not null")
	}
}

func TestCmdCheckJSONWithDiagnostic(t *testing.T) {
	dir := writeCheckableProject(t, "export const s: string = 5;\n")

	output, code := captureStdout(t, func() int {
		return cmdCheck([]string{"--json", dir})
	})
	if code != 1 {
		t.Fatalf("cmdCheck --json (error) exit = %d, want 1; output:\n%s", code, output)
	}

	var res jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if res.OK {
		t.Error("ok = true on a check with errors")
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	d := res.Diagnostics[0]
	if d.Severity != "error" {
		t.Errorf("severity = %q, want error", d.Severity)
	}
	// check has structured locations: line/col are 1-based, file is set.
	if d.Line < 1 || d.Col < 1 {
		t.Errorf("line/col = %d/%d, want >= 1/1", d.Line, d.Col)
	}
	if !strings.Contains(filepath.ToSlash(d.File), "main.ts") {
		t.Errorf("file = %q, want it to reference main.ts", d.File)
	}
	if d.Message == "" {
		t.Error("diagnostic message is empty")
	}
}

func TestCmdCheckJSONCarriesTheDiagnosticCode(t *testing.T) {
	// Given a file TypeScript rejects
	dir := writeCheckableProject(t, "export const s: string = 5;\n")

	// When `sloptor check --json` reports it
	output, code := captureStdout(t, func() int {
		return cmdCheck([]string{"--json", dir})
	})
	if code != 1 {
		t.Fatalf("cmdCheck --json (error) exit = %d, want 1; output:\n%s", code, output)
	}

	// Then the code reads the same as it does everywhere else. check builds its
	// entries straight from ast.Diagnostic and never goes through
	// DiagnosticInfo, so this is the one place the format could drift.
	var res jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	if got := res.Diagnostics[0].Code; got != "TS2322" {
		t.Errorf("code = %q, want TS2322 (message %q)", got, res.Diagnostics[0].Message)
	}
}

func TestParseCheckArgsConcurrencyControls(t *testing.T) {
	intPtr := func(value int) *int { return &value }
	tests := []struct {
		name         string
		args         []string
		wantProject  string
		wantCheckers *int
		wantErr      string
	}{
		{name: "omitted", args: nil, wantProject: "."},
		{
			name:         "separated value",
			args:         []string{"--checkers", "3", "project"},
			wantProject:  "project",
			wantCheckers: intPtr(3),
		},
		{
			name:         "equals value",
			args:         []string{"--checkers=3", "project"},
			wantProject:  "project",
			wantCheckers: intPtr(3),
		},
		{
			name:    "missing value",
			args:    []string{"--checkers"},
			wantErr: "flag needs an argument: --checkers",
		},
		{
			name:    "non integer",
			args:    []string{"--checkers=many"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "zero",
			args:    []string{"--checkers=0"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "negative",
			args:    []string{"--checkers", "-2"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders remains unknown",
			args:    []string{"--builders", "2"},
			wantErr: "unknown flag: --builders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr != "" {
				stderr, code := captureStderr(t, func() int { return cmdCheck(tt.args) })
				if code != 1 {
					t.Fatalf("cmdCheck(%v) exit = %d, want 1", tt.args, code)
				}
				if !strings.Contains(stderr, tt.wantErr) {
					t.Fatalf("cmdCheck(%v) stderr = %q, want substring %q", tt.args, stderr, tt.wantErr)
				}
				return
			}
			got := parseCheckArgsForTest(t, tt.args)
			if got.project != tt.wantProject {
				t.Errorf("project = %q, want %q", got.project, tt.wantProject)
			}
			if (got.checkers == nil) != (tt.wantCheckers == nil) {
				t.Errorf("checkers = %v, want %v", got.checkers, tt.wantCheckers)
			}
			if got.checkers != nil && *got.checkers != *tt.wantCheckers {
				t.Errorf("checkers = %d, want %d", *got.checkers, *tt.wantCheckers)
			}
		})
	}
}

func TestCheckProgramCheckerOverride(t *testing.T) {
	cliCheckers := 3
	singleThreadedCheckers := 4
	tests := []struct {
		name          string
		configOptions string
		override      *int
		wantCheckers  int
		wantEffective int
		wantSingle    bool
	}{
		{
			name:          "config is preserved without CLI",
			configOptions: `,"checkers": 1`,
			wantCheckers:  1,
			wantEffective: 1,
		},
		{
			name:          "CLI overrides config",
			configOptions: `,"checkers": 1`,
			override:      &cliCheckers,
			wantCheckers:  3,
			wantEffective: 3,
		},
		{
			name:          "single threaded config still wins over CLI count",
			configOptions: `,"singleThreaded": true`,
			override:      &singleThreadedCheckers,
			wantCheckers:  4,
			wantEffective: 1,
			wantSingle:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeCheckerCheckProject(t, tt.configOptions)
			configPath := filepath.ToSlash(filepath.Join(dir, "tsconfig.json"))
			for construction := 1; construction <= 2; construction++ {
				program, parsed, configDiags := newCheckProgram(dir, configPath, tt.override)
				if len(configDiags) != 0 {
					t.Fatalf("newCheckProgram diagnostics on construction %d: %v", construction, configDiags)
				}
				if program == nil || parsed == nil {
					t.Fatalf("newCheckProgram construction %d returned nil program or parsed config", construction)
				}
				if got := parsed.CompilerOptions().Checkers; got == nil || *got != tt.wantCheckers {
					t.Fatalf("parsed checkers on construction %d = %v, want %d", construction, got, tt.wantCheckers)
				}
				if got := program.Options().Checkers; got == nil || *got != tt.wantCheckers {
					t.Fatalf("program checkers on construction %d = %v, want %d", construction, got, tt.wantCheckers)
				}
				if program.Options().SingleThreaded.IsTrue() != tt.wantSingle {
					t.Fatalf("singleThreaded on construction %d = %v, want %v", construction, program.Options().SingleThreaded, tt.wantSingle)
				}
				var effective atomic.Int32
				program.ForEachCheckerParallel(func(int, *checker.Checker) {
					effective.Add(1)
				})
				if got := int(effective.Load()); got != tt.wantEffective {
					t.Fatalf("effective checker count on construction %d = %d, want %d", construction, got, tt.wantEffective)
				}
			}
		})
	}
}

func TestCmdCheckCheckers(t *testing.T) {
	dir := writeCheckerCheckProject(t, `,"checkers": 1`)

	styledOutput, styledCode := captureStdout(t, func() int {
		return cmdCheck([]string{"--checkers", "3", dir})
	})
	if styledCode != 0 {
		t.Fatalf("styled check exit = %d, want 0; output:\n%s", styledCode, styledOutput)
	}

	jsonOutput, jsonCode := captureStdout(t, func() int {
		return cmdCheck([]string{"--json", "--checkers=3", dir})
	})
	if jsonCode != 0 {
		t.Fatalf("JSON check exit = %d, want 0; output:\n%s", jsonCode, jsonOutput)
	}
	var result jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &result); err != nil {
		t.Fatalf("JSON check output is invalid: %v\noutput:\n%s", err, jsonOutput)
	}
	if !result.OK || result.Diagnostics == nil {
		t.Fatalf("JSON check result = %+v, want clean result with non-nil diagnostics", result)
	}

	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing",
			args:    []string{"--checkers"},
			wantErr: "flag needs an argument: --checkers",
		},
		{
			name:    "zero",
			args:    []string{"--checkers=0"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "negative",
			args:    []string{"--checkers", "-1"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "non integer",
			args:    []string{"--checkers=many"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders unknown",
			args:    []string{"--builders", "2"},
			wantErr: "unknown flag: --builders",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stderr, code := captureStderr(t, func() int { return cmdCheck(tt.args) })
			if code != 1 {
				t.Fatalf("cmdCheck(%v) exit = %d, want 1; stderr:\n%s", tt.args, code, stderr)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Fatalf("cmdCheck(%v) stderr = %q, want substring %q", tt.args, stderr, tt.wantErr)
			}
		})
	}
}

func writeCheckerCheckProject(t *testing.T, compilerOptions string) string {
	t.Helper()
	dir := writeCheckableProject(t, "")
	tsconfig := `{
	"compilerOptions": {
		"noLib": true,
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out"` + compilerOptions + `
	},
	"include": ["src"]
}`
	mustWrite(t, filepath.Join(dir, "tsconfig.json"), tsconfig)
	for _, name := range []string{"a.ts", "b.ts", "c.ts", "d.ts"} {
		mustWrite(t, filepath.Join(dir, "src", name), "export {};\n")
	}
	return dir
}
