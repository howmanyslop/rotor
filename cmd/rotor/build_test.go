package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"rotor/internal/compile"
	"rotor/internal/luau/cst"
)

// TestParseBuildArgs covers the rbxtsc-compatible flag surface
// (CLI/commands/build.ts L49-118), parsed through the real Cobra flag set.
// Implication failures (which live in the command runner) are exercised
// behaviorally through the run() surface.
func TestParseBuildArgs(t *testing.T) {
	t.Run("no args defaults project to dot with empty partial", func(t *testing.T) {
		got := parseBuildArgsForTest(t, nil)
		if got.project != "." {
			t.Errorf("project = %q, want \".\"", got.project)
		}
		if got.opts != (partialProjectOptions{}) {
			t.Errorf("opts = %+v, want all-nil (no yargs defaults below --project)", got.opts)
		}
	})

	t.Run("usePolling without watch errors", func(t *testing.T) {
		stderr, code := captureStderr(t, func() int { return cmdBuild([]string{"--usePolling"}) })
		if code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr, "usePolling -> watch") {
			t.Errorf("stderr = %q, want the usePolling->watch implication error", stderr)
		}
	})

	t.Run("usePolling with watch ok", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"--usePolling", "-w"})
		if got.opts.usePolling == nil || !*got.opts.usePolling || got.opts.watch == nil || !*got.opts.watch {
			t.Errorf("opts = %+v", got.opts)
		}
	})

	t.Run("boolean negation forms", func(t *testing.T) {
		for _, args := range [][]string{{"--no-luau"}, {"--luau=false"}, {"--luau=0"}} {
			got := parseBuildArgsForTest(t, args)
			if got.opts.luau == nil || *got.opts.luau {
				t.Errorf("%v: luau = %v, want false", args, got.opts.luau)
			}
		}
		got := parseBuildArgsForTest(t, []string{"--optimizedLoops=false"})
		if got.opts.optimizedLoops == nil || *got.opts.optimizedLoops {
			t.Error("--optimizedLoops=false not parsed")
		}
	})

	t.Run("plain boolean flags set true", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{
			"--verbose", "--noInclude", "--logTruthyChanges",
			"--writeOnlyChanged", "--writeTransformedFiles", "--allowCommentDirectives",
		})
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
		got := parseBuildArgsForTest(t, []string{"--type", "model"})
		if got.opts.typeName == nil || *got.opts.typeName != "model" {
			t.Errorf("type = %v", got.opts.typeName)
		}
		if code := cmdBuild([]string{"--type", "bogus"}); code != 1 {
			t.Errorf("invalid --type exit = %d, want 1", code)
		}
		if code := cmdBuild([]string{"--type"}); code != 1 {
			t.Errorf("--type with no value exit = %d, want 1", code)
		}
	})

	t.Run("string flag forms", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"-p", "proj", "-i", "inc", "--rojo=custom.project.json"})
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
		got := parseBuildArgsForTest(t, []string{"--rojo", "--verbose"})
		if got.opts.rojo == nil || *got.opts.rojo != "" {
			t.Errorf("rojo = %v, want present-and-empty", got.opts.rojo)
		}
		if got.opts.verbose == nil || !*got.opts.verbose {
			t.Error("--verbose after valueless --rojo not parsed")
		}
	})

	t.Run("positional project path", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"some/dir", "--verbose"})
		if got.project != "some/dir" {
			t.Errorf("project = %q", got.project)
		}
	})

	t.Run("positional plus --project errors", func(t *testing.T) {
		if code := cmdBuild([]string{"a", "-p", "b"}); code != 1 {
			t.Error("conflicting project paths accepted")
		}
	})

	t.Run("unknown flag errors", func(t *testing.T) {
		if code := cmdBuild([]string{"--bogus"}); code != 1 {
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

	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "builders missing value",
			args:    []string{"--build", "--builders"},
			wantErr: "flag needs an argument: --builders",
		},
		{
			name:    "checkers missing value",
			args:    []string{"--checkers"},
			wantErr: "flag needs an argument: --checkers",
		},
		{
			name:    "builders zero",
			args:    []string{"--build", "--builders", "0"},
			wantErr: `"--builders" flag: must be a positive integer`,
		},
		{
			name:    "checkers zero",
			args:    []string{"--checkers=0"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders negative",
			args:    []string{"--build", "--builders", "-1"},
			wantErr: `"--builders" flag: must be a positive integer`,
		},
		{
			name:    "checkers negative",
			args:    []string{"--checkers=-1"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders non-integer",
			args:    []string{"--build", "--builders", "many"},
			wantErr: `"--builders" flag: must be a positive integer`,
		},
		{
			name:    "checkers non-integer",
			args:    []string{"--checkers=many"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders without build mode",
			args:    []string{"--builders", "2"},
			wantErr: "--builders requires --build",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stderr, code := captureStderr(t, func() int {
				return cmdBuild(tt.args)
			})
			if code != 1 {
				t.Errorf("cmdBuild(%v) exit = %d, want 1", tt.args, code)
			}
			firstLine := strings.SplitN(stderr, "\n", 2)[0]
			if !strings.HasPrefix(firstLine, "error: ") {
				t.Errorf("cmdBuild(%v) first stderr line = %q, want a red error: prefix", tt.args, firstLine)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("cmdBuild(%v) stderr = %q, want substring %q", tt.args, stderr, tt.wantErr)
			}
		})
	}
}

func TestParseBuildArgsJSON(t *testing.T) {
	got := parseBuildArgsForTest(t, []string{"--json", "."})
	if !got.jsonOut {
		t.Error("--json not parsed")
	}
}

func TestBuildTimingsFlag(t *testing.T) {
	for _, args := range [][]string{{"--timings", "report.json"}, {"--timings=report.json"}} {
		parsed := parseBuildArgsForTest(t, args)
		if parsed.timings != "report.json" {
			t.Errorf("parseBuildArgs(%v) timings = %q, want report.json", args, parsed.timings)
		}
	}

	// Given
	dir := writeBuildableProject(t, "")
	timingPath := filepath.Join(t.TempDir(), "build-timings.json")

	// When
	_, _, code := captureBuildOutput(t, []string{"--timings", timingPath, dir})

	// Then
	if code != 0 {
		t.Fatalf("cmdBuild --timings exit = %d, want 0", code)
	}
	data, err := os.ReadFile(timingPath)
	if err != nil {
		t.Fatalf("read timing output: %v", err)
	}
	var timings compile.BuildTimings
	if err := json.Unmarshal(data, &timings); err != nil {
		t.Fatalf("decode timing output: %v", err)
	}
	if timings.SchemaVersion != compile.BuildTimingSchemaVersion || !timings.OK {
		t.Errorf("timing schemaVersion = %d, ok = %t; want current successful schema", timings.SchemaVersion, timings.OK)
	}
	if timings.Counts.TotalSources != 1 || timings.Counts.SelectedSources != 1 || timings.Counts.EmittedEntries != 1 {
		t.Errorf("timing counts = %+v", timings.Counts)
	}
	if timings.Stages.SidecarRoundTripMs != 0 || timings.Stages.OverlayProgramMs != 0 {
		t.Errorf("pluginless sidecar stages = %+v, want zero", timings.Stages)
	}
	if timings.Stages.IncrementalManifestMs < 0 {
		t.Errorf("incremental manifest stage = %dms, want nonnegative", timings.Stages.IncrementalManifestMs)
	}
	assertTimingJSONShape(t, data)

	t.Run("failed build writes an unsuccessful report", func(t *testing.T) {
		// Given
		failingDir := writeBuildableProject(t, "export const value: string = 1;\n")
		failingTimingPath := filepath.Join(t.TempDir(), "failed-build-timings.json")

		// When
		_, _, failureCode := captureBuildOutput(t, []string{"--timings", failingTimingPath, failingDir})

		// Then
		if failureCode != 1 {
			t.Fatalf("failing cmdBuild --timings exit = %d, want 1", failureCode)
		}
		failureData, err := os.ReadFile(failingTimingPath)
		if err != nil {
			t.Fatalf("read failed-build timing output: %v", err)
		}
		var failedTimings compile.BuildTimings
		if err := json.Unmarshal(failureData, &failedTimings); err != nil {
			t.Fatalf("decode failed-build timing output: %v", err)
		}
		if failedTimings.OK {
			t.Errorf("failed-build timing ok = %t, want false", failedTimings.OK)
		}
	})
}

func TestBuildProfilingFlags(t *testing.T) {
	args := []string{
		"--trace-out", "trace.out",
		"--blockprofile", "block.prof",
		"--mutexprofile", "mutex.prof",
		"--heapprofile", "heap.prof",
	}
	parsed := parseBuildArgsForTest(t, args)
	if parsed.traceOut != "trace.out" || parsed.blockprofile != "block.prof" ||
		parsed.mutexprofile != "mutex.prof" || parsed.heapprofile != "heap.prof" {
		t.Fatalf("parsed profile paths = %#v", parsed)
	}
}

func TestBuildFiniteDiagnosticsRejectWatch(t *testing.T) {
	for _, flag := range []string{"--cpuprofile", "--trace-out", "--blockprofile", "--mutexprofile", "--heapprofile", "--timings"} {
		stderr, code := captureStderr(t, func() int {
			return cmdBuild([]string{"--watch", flag, "profile.out"})
		})
		if code != 1 {
			t.Errorf("cmdBuild with %s exit = %d, want 1", flag, code)
		}
		if !strings.Contains(stderr, "cannot be used with --watch") {
			t.Errorf("cmdBuild with %s stderr = %q, want the watch conflict", flag, stderr)
		}
	}
}

func TestBuildFiniteDiagnosticsRejectDuplicatePaths(t *testing.T) {
	for _, args := range [][]string{
		{"--cpuprofile", "profile.out", "--trace-out", filepath.Join(".", "profile.out")},
		{"--heapprofile", "profile.out", "--timings", "profile.out"},
	} {
		parsed := parseBuildArgsForTest(t, args)
		if err := validateBuildDiagnosticPaths(parsed); err == nil {
			t.Fatalf("duplicate diagnostic paths accepted for %v", args)
		}
	}
}

func TestBuildFiniteDiagnosticsRejectAliasedPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.prof")
	if err := os.WriteFile(target, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, makeAlias := range map[string]func(string) error{
		"symlink":  func(path string) error { return os.Symlink(target, path) },
		"hardlink": func(path string) error { return os.Link(target, path) },
	} {
		t.Run(name, func(t *testing.T) {
			alias := filepath.Join(dir, name+".prof")
			if err := makeAlias(alias); err != nil {
				t.Fatal(err)
			}
			parsed := parseBuildArgsForTest(t, []string{"--cpuprofile", target, "--trace-out", alias})
			if err := validateBuildDiagnosticPaths(parsed); err == nil {
				t.Fatal("aliased diagnostic paths accepted")
			}
		})
	}
}

func TestBuildFiniteDiagnosticsRejectDanglingSymlinkAlias(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.prof")
	alias := filepath.Join(dir, "alias.prof")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	parsed := parseBuildArgsForTest(t, []string{"--cpuprofile", target, "--trace-out", alias})
	if err := validateBuildDiagnosticPaths(parsed); err == nil {
		t.Fatal("dangling symlink diagnostic alias accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target stat error = %v, want not-exist", err)
	}
}

func TestBuildWritesCombinedProfiles(t *testing.T) {
	dir := writeBuildableProject(t, "")
	profileDir := t.TempDir()
	paths := []string{
		filepath.Join(profileDir, "cpu.prof"),
		filepath.Join(profileDir, "trace.out"),
		filepath.Join(profileDir, "block.prof"),
		filepath.Join(profileDir, "mutex.prof"),
		filepath.Join(profileDir, "heap.prof"),
	}
	args := []string{
		"--cpuprofile", paths[0], "--trace-out", paths[1],
		"--blockprofile", paths[2], "--mutexprofile", paths[3],
		"--heapprofile", paths[4], dir,
	}

	_, _, code := captureBuildOutput(t, args)

	if code != 0 {
		t.Fatalf("profiled build exit = %d, want 0", code)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("profile %s: %v", filepath.Base(path), err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("profile %s is empty", filepath.Base(path))
		}
	}
}

func TestFailedBuildFinalizesCombinedProfiles(t *testing.T) {
	profileDir := t.TempDir()
	paths := []string{
		filepath.Join(profileDir, "cpu.prof"),
		filepath.Join(profileDir, "trace.out"),
		filepath.Join(profileDir, "block.prof"),
		filepath.Join(profileDir, "mutex.prof"),
		filepath.Join(profileDir, "heap.prof"),
	}
	args := []string{
		"--cpuprofile", paths[0], "--trace-out", paths[1],
		"--blockprofile", paths[2], "--mutexprofile", paths[3],
		"--heapprofile", paths[4], filepath.Join(t.TempDir(), "missing"),
	}

	_, _, code := captureBuildOutput(t, args)

	if code != 1 {
		t.Fatalf("failed profiled build exit = %d, want 1", code)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("profile %s: %v", filepath.Base(path), err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("profile %s is empty", filepath.Base(path))
		}
	}
}

func assertTimingJSONShape(t *testing.T, data []byte) {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode timing JSON shape: %v", err)
	}
	for _, field := range []string{"schemaVersion", "ok", "totalMs", "stages", "counts"} {
		if _, ok := value[field]; !ok {
			t.Errorf("timing JSON missing %q", field)
		}
	}
}

func TestBuildTimingsWatchRejected(t *testing.T) {
	// Given
	timingPath := filepath.Join(t.TempDir(), "build-timings.json")

	// When
	_, stderr, code := captureBuildOutput(t, []string{"--watch", "--timings", timingPath, filepath.Join(t.TempDir(), "missing")})

	// Then
	if code != 1 {
		t.Errorf("cmdBuild --watch --timings exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "--timings cannot be used with --watch") {
		t.Errorf("stderr = %q, want timing/watch rejection", stderr)
	}
	if _, err := os.Stat(timingPath); !os.IsNotExist(err) {
		t.Errorf("timing output exists after rejected watch build: %v", err)
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
			got := parseBuildArgsForTest(t, tt.args)

			// Then
			if tt.wantErr != "" {
				stderr, code := captureStderr(t, func() int { return cmdBuild(tt.args) })
				if code != 1 {
					t.Fatalf("cmdBuild(%v) exit = %d, want 1", tt.args, code)
				}
				if !strings.Contains(stderr, tt.wantErr) {
					t.Fatalf("cmdBuild(%v) stderr = %q, want substring %q", tt.args, stderr, tt.wantErr)
				}
				return
			}
			if got.build != tt.build || got.buildPath != tt.path || got.emitDeclarationOnly != tt.emit {
				t.Errorf("parseBuildArgs(%v) = %+v", tt.args, got)
			}
		})
	}
}

func TestParseBuildArgsConcurrencyControls(t *testing.T) {
	intPtr := func(value int) *int { return &value }
	tests := []struct {
		name         string
		args         []string
		wantBuilders *int
		wantCheckers *int
		wantErr      string
	}{
		{name: "omitted", args: nil},
		{
			name:         "builders separated value one",
			args:         []string{"--build", "--builders", "1"},
			wantBuilders: intPtr(1),
		},
		{
			name:         "builders equals value four",
			args:         []string{"--build", "--builders=4"},
			wantBuilders: intPtr(4),
		},
		{
			name:         "checkers separated value one",
			args:         []string{"--checkers", "1"},
			wantCheckers: intPtr(1),
		},
		{
			name:         "checkers equals value four",
			args:         []string{"--checkers=4"},
			wantCheckers: intPtr(4),
		},
		{
			name:         "accepts_builders_and_checkers",
			args:         []string{"--build", "--builders", "4", "--checkers", "2"},
			wantBuilders: intPtr(4),
			wantCheckers: intPtr(2),
		},
		{
			name:         "builders with short build alias",
			args:         []string{"-b", "--builders=1"},
			wantBuilders: intPtr(1),
		},
		{
			name:    "builders missing value",
			args:    []string{"--build", "--builders"},
			wantErr: "flag needs an argument: --builders",
		},
		{
			name:    "checkers missing value",
			args:    []string{"--checkers"},
			wantErr: "flag needs an argument: --checkers",
		},
		{
			name:    "builders zero",
			args:    []string{"--build", "--builders", "0"},
			wantErr: `"--builders" flag: must be a positive integer`,
		},
		{
			name:    "checkers zero",
			args:    []string{"--checkers=0"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders negative",
			args:    []string{"--build", "--builders", "-1"},
			wantErr: `"--builders" flag: must be a positive integer`,
		},
		{
			name:    "checkers negative",
			args:    []string{"--checkers=-1"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "builders non-integer",
			args:    []string{"--build", "--builders", "many"},
			wantErr: `"--builders" flag: must be a positive integer`,
		},
		{
			name:    "checkers non-integer",
			args:    []string{"--checkers=many"},
			wantErr: `"--checkers" flag: must be a positive integer`,
		},
		{
			name:    "rejects_builders_without_build",
			args:    []string{"--builders", "2"},
			wantErr: "--builders requires --build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr != "" {
				stderr, code := captureStderr(t, func() int { return cmdBuild(tt.args) })
				if code != 1 {
					t.Fatalf("cmdBuild(%v) exit = %d, want 1", tt.args, code)
				}
				if !strings.Contains(stderr, tt.wantErr) {
					t.Fatalf("cmdBuild(%v) stderr = %q, want substring %q", tt.args, stderr, tt.wantErr)
				}
				return
			}
			got := parseBuildArgsForTest(t, tt.args)
			if (got.builders == nil) != (tt.wantBuilders == nil) {
				t.Errorf("builders = %v, want %v", got.builders, tt.wantBuilders)
			}
			if got.builders != nil && *got.builders != *tt.wantBuilders {
				t.Errorf("builders = %d, want %d", *got.builders, *tt.wantBuilders)
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

// TestBuildHelpShowsConcurrencyControls pins the rendered build/root help:
// the build flag descriptions and the root environment notes must survive the
// Cobra migration.
func TestBuildHelpShowsConcurrencyControls(t *testing.T) {
	var out, errOut strings.Builder
	if code := execute([]string{"build", "--help"}, cliStreams{in: strings.NewReader(""), out: &out, err: &errOut}); code != 0 {
		t.Fatalf("build --help exit = %d, stderr: %s", code, errOut.String())
	}
	for _, want := range []string{
		"--builders <n>",
		"default 4",
		"only with --build",
		"--checkers <n>",
		"build and check",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("build --help does not contain %q", want)
		}
	}

	out.Reset()
	if code := execute([]string{"--help"}, cliStreams{in: strings.NewReader(""), out: &out, err: &errOut}); code != 0 {
		t.Fatalf("root --help exit = %d, stderr: %s", code, errOut.String())
	}
	for _, want := range []string{
		"UV_THREADPOOL_SIZE",
		"Node sidecar libuv pool size",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("root --help does not contain %q", want)
		}
	}
}

func TestProjectCompileOptionsConcurrencyControls(t *testing.T) {
	// Given
	builders := 3
	checkers := 3
	opts := projectOptions{
		builders: &builders,
		checkers: &checkers,
	}

	// When
	got := projectCompileOptions("tsconfig.json", opts)

	// Then
	if got.Builders != opts.builders || got.Checkers != opts.checkers {
		t.Fatalf("concurrency pointers = builders %p/checkers %p, want %p/%p", got.Builders, got.Checkers, opts.builders, opts.checkers)
	}
	if *got.Builders != 3 || *got.Checkers != 3 {
		t.Fatalf("concurrency values = builders %d/checkers %d, want 3/3", *got.Builders, *got.Checkers)
	}

	missing := projectCompileOptions("tsconfig.json", projectOptions{})
	if missing.Builders != nil || missing.Checkers != nil {
		t.Fatalf("missing CLI overrides = builders %v/checkers %v, want nil", missing.Builders, missing.Checkers)
	}
	present := projectCompileOptions("tsconfig.json", projectOptions{checkers: &checkers})
	if present.Checkers == nil || *present.Checkers != 3 {
		t.Fatalf("present checker override = %v, want 3", present.Checkers)
	}
}

func TestBuildWatchConcurrencyOptions(t *testing.T) {
	// Given
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tsconfig.json")
	mustWrite(t, configPath, `{"rbxts":{"luau":false}}`)
	parsed := parseBuildArgsForTest(t, []string{"--build", "--watch", "--builders", "3", "--checkers", "2"})

	// When
	reload := newBuildOptionsReload(configPath, parsed)
	got, err := reload()
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if got.builders != parsed.builders || got.checkers != parsed.checkers {
		t.Fatalf("reloaded pointers = builders %p/checkers %p, want %p/%p", got.builders, got.checkers, parsed.builders, parsed.checkers)
	}
	if *got.builders != 3 || *got.checkers != 2 {
		t.Fatalf("reloaded values = builders %d/checkers %d, want 3/2", *got.builders, *got.checkers)
	}
	if got.luau {
		t.Fatal("rbxts options were not re-read during reload")
	}
}

func TestBuildSolutionPropagatesCheckers(t *testing.T) {
	// Given
	root, projectConfigs := writeConcurrencySolution(t)
	tests := []struct {
		name     string
		checkers int
	}{
		{name: "CLI checker 2 reaches every referenced project", checkers: 2},
		{name: "CLI checker 3 overrides configured checker 1", checkers: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			parsed := parseBuildArgsForTest(t, []string{"--build", "--builders", "3", "--checkers", fmt.Sprint(tt.checkers)})
			opts := mergeProjectOptions(defaultProjectOptions, nil, &parsed.opts)
			opts.builders = parsed.builders
			opts.checkers = parsed.checkers
			coordinator, err := compile.NewSolutionCoordinator(
				filepath.Join(root, "tsconfig.json"),
				projectCompileOptions(filepath.Join(root, "tsconfig.json"), opts),
			)
			// Then
			if err != nil {
				t.Fatal(err)
			}
			for _, configPath := range projectConfigs {
				state, ok := coordinator.ProjectState(configPath)
				if !ok {
					t.Fatalf("missing referenced project state for %s", configPath)
				}
				if state.Project.Options.Checkers == nil || *state.Project.Options.Checkers != tt.checkers {
					t.Fatalf("%s checkers = %v, want CLI %d", configPath, state.Project.Options.Checkers, tt.checkers)
				}
				if state.Project.Options.Builders == nil || *state.Project.Options.Builders != 3 {
					t.Fatalf("%s builders = %v, want CLI 3", configPath, state.Project.Options.Builders)
				}
			}
		})
	}
}

func writeConcurrencySolution(t *testing.T) (string, []string) {
	t.Helper()
	root := t.TempDir()
	configs := []string{}
	mustWrite(t, filepath.Join(root, "tsconfig.json"), `{"files":[],"references":[{"path":"./left"},{"path":"./right"}]}`)
	for _, name := range []string{"left", "right"} {
		dir := filepath.Join(root, name)
		configPath := filepath.Join(dir, "tsconfig.json")
		configs = append(configs, configPath)
		mustWrite(t, configPath, `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"rootDir":"src","outDir":"out","checkers":1},"include":["src"]}`)
		mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"@scope/`+name+`"}`)
		mustWrite(t, filepath.Join(dir, "src", "globals.d.ts"), noLibGlobalStubs)
		mustWrite(t, filepath.Join(dir, "src", "main.ts"), "export {};\n")
	}
	return root, configs
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
// wrote, plus fn's return value. The pipe is drained concurrently (Windows
// pipes buffer only ~4KB; reading after fn returns would deadlock).
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var data []byte
	go func() {
		data, _ = io.ReadAll(r)
		close(done)
	}()
	code := fn()
	_ = w.Close()
	os.Stdout = prev
	<-done
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
	if got := res.Diagnostics[0].Code; got != "TS2322" {
		t.Errorf("code = %q, want TS2322 (message %q)", got, res.Diagnostics[0].Message)
	}
}

func TestCmdBuildJSONCarriesTheTransformerDiagnosticCode(t *testing.T) {
	// Given a file the transformer rejects rather than the checker
	dir := writeBuildableProject(t, "declare const loose: any;\nexport const taken = loose.field;\n")

	output, code := captureStdout(t, func() int {
		return cmdBuild([]string{"--json", dir})
	})
	if code != 1 {
		t.Fatalf("cmdBuild --json (error) exit = %d, want 1; output:\n%s", code, output)
	}

	// Then it is named by its upstream factory, not by a TS number — the
	// distinction a consumer of the census needs and could not previously make
	var res jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &res); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	if got := res.Diagnostics[0].Code; got != "noAny" {
		t.Errorf("code = %q, want noAny (message %q)", got, res.Diagnostics[0].Message)
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
		got := parseBuildArgsForTest(t, nil)
		if got.maxErrors != 50 {
			t.Errorf("maxErrors = %d, want 50", got.maxErrors)
		}
	})

	t.Run("--max-errors value", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"--max-errors", "10"})
		if got.maxErrors != 10 {
			t.Errorf("maxErrors = %d, want 10", got.maxErrors)
		}
	})

	t.Run("--max-errors=N form", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"--max-errors=5"})
		if got.maxErrors != 5 {
			t.Errorf("maxErrors = %d, want 5", got.maxErrors)
		}
	})

	t.Run("--max-errors 0 means unlimited", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"--max-errors", "0"})
		if got.maxErrors != 0 {
			t.Errorf("maxErrors = %d, want 0", got.maxErrors)
		}
	})

	t.Run("--max-errors with negative value errors", func(t *testing.T) {
		if code := cmdBuild([]string{"--max-errors", "-1"}); code != 1 {
			t.Error("expected error for negative --max-errors")
		}
	})
}

// TestParseBuildArgsWatchDXFlags verifies the watch DX booleans --bell and
// --no-clear (and their defaults).
func TestParseBuildArgsWatchDXFlags(t *testing.T) {
	t.Run("defaults: bell off, clear on", func(t *testing.T) {
		got := parseBuildArgsForTest(t, nil)
		if got.bell {
			t.Error("bell default = true, want false")
		}
		if !got.clearScreen {
			t.Error("clearScreen default = false, want true")
		}
	})
	t.Run("--bell enables the bell", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"--bell"})
		if !got.bell {
			t.Error("--bell not parsed")
		}
	})
	t.Run("--no-clear disables clear-on-rebuild", func(t *testing.T) {
		got := parseBuildArgsForTest(t, []string{"--no-clear"})
		if got.clearScreen {
			t.Error("--no-clear did not disable clearScreen")
		}
	})
}

// TestCmdBuildMinify verifies that `sloptor build --minify` produces smaller,
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
		if strings.Contains(c, "-- Compiled with @isentinel/roblox-ts") {
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
		if strings.Contains(c, "-- Compiled with @isentinel/roblox-ts") {
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

func TestBuildTimingsNormalOutputUnchanged(t *testing.T) {
	// Given
	dir := writeBuildableProject(t, "")

	// When
	firstStdout, firstStderr, firstCode := captureBuildOutput(t, []string{dir})
	firstFiles := outputFileTree(t, filepath.Join(dir, "out"))
	if err := os.RemoveAll(filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".rotor")); err != nil {
		t.Fatal(err)
	}
	timingPath := filepath.Join(t.TempDir(), "build-timings.json")
	secondStdout, secondStderr, secondCode := captureBuildOutput(t, []string{"--timings", timingPath, dir})
	secondFiles := outputFileTree(t, filepath.Join(dir, "out"))

	// Then
	if firstCode != 0 || secondCode != 0 {
		t.Fatalf("build exits = %d, %d; stdout: %q, %q; stderr: %q, %q", firstCode, secondCode, firstStdout, secondStdout, firstStderr, secondStderr)
	}
	if got, want := normalizeBuildOutput(firstStdout), normalizeBuildOutput(secondStdout); got != want {
		t.Errorf("normalized stdout differs:\nfirst:  %q\nsecond: %q", got, want)
	}
	if firstStderr != secondStderr {
		t.Errorf("stderr differs:\nfirst:  %q\nsecond: %q", firstStderr, secondStderr)
	}
	if !equalFileTrees(firstFiles, secondFiles) {
		t.Errorf("output artifacts differ:\nfirst:  %#v\nsecond: %#v", firstFiles, secondFiles)
	}
}

var buildTimingText = regexp.MustCompile(`in [0-9]+(?:\.[0-9]+)? ?(?:ns|µs|ms|s)|[0-9]+ files/s|\n    [0-9]+ written(?: - [0-9]+ files/s)?`)

func normalizeBuildOutput(output string) string {
	return buildTimingText.ReplaceAllString(output, "<timing>")
}

func captureBuildOutput(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	previousStdout := os.Stdout
	previousStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	var stdout []byte
	var stderr []byte
	go func() {
		stdout, _ = io.ReadAll(stdoutReader)
		close(stdoutDone)
	}()
	go func() {
		stderr, _ = io.ReadAll(stderrReader)
		close(stderrDone)
	}()
	code := cmdBuild(args)
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = previousStdout
	os.Stderr = previousStderr
	<-stdoutDone
	<-stderrDone
	return string(stdout), string(stderr), code
}

func outputFileTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Base(path) == "rbxts.copyfiles.json" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func equalFileTrees(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftContents := range left {
		if right[path] != leftContents {
			return false
		}
	}
	return true
}
