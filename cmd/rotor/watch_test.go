package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"rotor/internal/compile"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTreeWatcherSnapshotPrunesAndDetectsAddedFiles(t *testing.T) {
	root := t.TempDir()
	srcFile := filepath.Join(root, "src", "main.ts")
	writeTestFile(t, srcFile, "export {};\n")
	writeTestFile(t, filepath.Join(root, "out", "main.luau"), "-- generated\n")
	writeTestFile(t, filepath.Join(root, "include", "RuntimeLib.luau"), "-- runtime\n")
	writeTestFile(t, filepath.Join(root, "node_modules", "@rbxts", "types", "package.json"), "{}\n")
	writeTestFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/master\n")
	writeTestFile(t, filepath.Join(root, ".vscode", "settings.json"), "{}\n")
	writeTestFile(t, filepath.Join(root, "src", "main.ts.swp"), "vim\n")
	writeTestFile(t, filepath.Join(root, "package.json"), "{}\n")

	w := newTreeWatcher(root, filepath.Join(root, "out"), filepath.Join(root, "include"))
	before := w.snapshot()

	for path, want := range map[string]bool{
		srcFile:                                           true,
		filepath.Join(root, "package.json"):               true,
		filepath.Join(root, "out", "main.luau"):           false,
		filepath.Join(root, "include", "RuntimeLib.luau"): false,
		filepath.Join(root, "node_modules", "@rbxts", "types", "package.json"): false,
		filepath.Join(root, ".git", "HEAD"):                                    false,
		filepath.Join(root, ".vscode", "settings.json"):                        false,
		filepath.Join(root, "src", "main.ts.swp"):                              false,
	} {
		if _, ok := before[path]; ok != want {
			t.Errorf("snapshot[%s] present = %v, want %v", path, ok, want)
		}
	}

	added := filepath.Join(root, "src", "extra.json")
	writeTestFile(t, added, "{}\n")
	changed := diffStamps(before, w.snapshot())
	if !reflect.DeepEqual(changed, []string{added}) {
		t.Fatalf("diffStamps = %v, want [%s]", changed, added)
	}
}

func TestTreeWatcherSnapshotIgnoresFlameworkBuild(t *testing.T) {
	root := t.TempDir()
	sourceFile := filepath.Join(root, "src", "main.ts")
	flameworkBuild := filepath.Join(root, "flamework.build")
	writeTestFile(t, sourceFile, "export {}\n")
	writeTestFile(t, flameworkBuild, "{\"version\":1}\n")

	w := newTreeWatcher(root)
	before := w.snapshot()
	writeTestFile(t, flameworkBuild, "{\"version\":2}\n")
	writeTestFile(t, sourceFile, "export const value = 1\n")

	changed := diffStamps(before, w.snapshot())
	if !reflect.DeepEqual(changed, []string{sourceFile}) {
		t.Fatalf("diffStamps = %v, want [%s]", changed, sourceFile)
	}
}

func TestDiffStampsReportsRemovedAndModifiedFiles(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "src", "gone.ts")
	edited := filepath.Join(root, "src", "edited.ts")
	writeTestFile(t, gone, "export {};\n")
	writeTestFile(t, edited, "export {};\n")

	w := newTreeWatcher(root)
	before := w.snapshot()
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, edited, "export {}; // edited\n")
	changed := diffStamps(before, w.snapshot())
	if !reflect.DeepEqual(changed, []string{edited, gone}) {
		t.Fatalf("diffStamps = %v, want [%s %s]", changed, edited, gone)
	}
}

func TestIsJunkFile(t *testing.T) {
	for name, want := range map[string]bool{
		"main.ts":              false,
		"default.project.json": false,
		"main.ts.swp":          true,
		"main.ts.swo":          true,
		"scratch.tmp":          true,
		"main.ts~":             true,
		"4913":                 true,
		".#main.ts":            true,
		".DS_Store":            true,
		"Thumbs.db":            true,
	} {
		if got := isJunkFile(name); got != want {
			t.Errorf("isJunkFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestShouldPruneDir(t *testing.T) {
	for name, want := range map[string]bool{
		"src":          false,
		"server":       false,
		"node_modules": true,
		".git":         true,
		".vscode":      true,
	} {
		if got := shouldPruneDir(name); got != want {
			t.Errorf("shouldPruneDir(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestSettleChangesBatchesWriteBursts simulates an editor "save all": the
// first tick sees one file, two more land during the quiet window, and the
// settled result reports all three changes against the baseline in one batch.
func TestSettleChangesBatchesWriteBursts(t *testing.T) {
	stampA := fileStamp{exists: true, size: 1}
	stampB := fileStamp{exists: true, size: 2}
	base := map[string]fileStamp{"a.ts": stampA, "b.ts": stampA, "c.ts": stampA}
	first := map[string]fileStamp{"a.ts": stampB, "b.ts": stampA, "c.ts": stampA}

	burst := []map[string]fileStamp{
		{"a.ts": stampB, "b.ts": stampB, "c.ts": stampA}, // b.ts lands
		{"a.ts": stampB, "b.ts": stampB, "c.ts": stampB}, // c.ts lands
		{"a.ts": stampB, "b.ts": stampB, "c.ts": stampB}, // quiet
		{"a.ts": stampB, "b.ts": stampB, "c.ts": stampB}, // must not be reached
	}
	calls := 0
	snap := func() map[string]fileStamp {
		s := burst[calls]
		calls++
		return s
	}
	noSleep := func(time.Duration) {}

	settled, changed := settleChanges(base, first, snap, noSleep)
	if want := []string{"a.ts", "b.ts", "c.ts"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}
	if calls != 3 {
		t.Fatalf("snapshot calls = %d, want 3 (stop on first quiet tick)", calls)
	}
	if !reflect.DeepEqual(settled, burst[2]) {
		t.Fatalf("settled snapshot = %v, want the quiet snapshot", settled)
	}
}

// TestSettleChangesCapsRunawayChurn proves the settle loop gives up after the
// cap even when every snapshot differs (e.g. something rewrites a file in a
// tight loop), so the watch never deadlocks waiting for quiet.
func TestSettleChangesCapsRunawayChurn(t *testing.T) {
	base := map[string]fileStamp{}
	n := 0
	snap := func() map[string]fileStamp {
		n++
		return map[string]fileStamp{"a.ts": {exists: true, size: int64(n)}}
	}
	noSleep := func(time.Duration) {}

	maxTicks := int(watchSettleCap / watchQuietPeriod)
	_, changed := settleChanges(base, snap(), snap, noSleep)
	if len(changed) != 1 {
		t.Fatalf("changed = %v, want the churning file", changed)
	}
	if n > maxTicks+1 {
		t.Fatalf("snapshot calls = %d, want <= %d (cap respected)", n, maxTicks+1)
	}
}

func TestMergePreStampsKeepsPreBuildStampsForSurvivors(t *testing.T) {
	preStamp := fileStamp{exists: true, size: 1}
	postStamp := fileStamp{exists: true, size: 2}
	pre := map[string]fileStamp{"a.ts": preStamp, "gone.ts": preStamp}
	post := map[string]fileStamp{"a.ts": postStamp, "new.ts": postStamp}

	merged := mergePreStamps(post, pre)
	if merged["a.ts"] != preStamp {
		t.Errorf("survivor a.ts stamp = %v, want pre-build stamp %v", merged["a.ts"], preStamp)
	}
	if merged["new.ts"] != postStamp {
		t.Errorf("new file stamp = %v, want fresh stamp %v", merged["new.ts"], postStamp)
	}
	if _, ok := merged["gone.ts"]; ok {
		t.Errorf("gone.ts should not be resurrected into the merged snapshot")
	}
}

func TestPruneStampsDropsEntriesUnderSkipDirs(t *testing.T) {
	out := filepath.Join("proj", "dist")
	stamps := map[string]fileStamp{
		filepath.Join("proj", "src", "a.ts"):     {exists: true},
		filepath.Join(out, "a.luau"):             {exists: true},
		filepath.Join(out, "sub", "b.luau"):      {exists: true},
		filepath.Join("proj", "distance.ts"):     {exists: true}, // prefix sibling, must survive
		filepath.Join("proj", "include", "r.lu"): {exists: true},
	}
	pruneStamps(stamps, []string{out, filepath.Join("proj", "include")})

	want := []string{filepath.Join("proj", "src", "a.ts"), filepath.Join("proj", "distance.ts")}
	if len(stamps) != len(want) {
		t.Fatalf("stamps after prune = %v, want only %v", stamps, want)
	}
	for _, path := range want {
		if _, ok := stamps[path]; !ok {
			t.Errorf("pruneStamps dropped %s, want kept", path)
		}
	}
}

func TestTreeWatcherIntervalClamps(t *testing.T) {
	w := newTreeWatcher(t.TempDir())
	w.walkCost = 0
	if got := w.interval(); got != watchMinInterval {
		t.Errorf("interval for free walk = %v, want %v", got, watchMinInterval)
	}
	w.walkCost = 5 * time.Millisecond
	if got := w.interval(); got != watchMinInterval {
		t.Errorf("interval for 5ms walk = %v, want clamp to %v", got, watchMinInterval)
	}
	w.walkCost = 30 * time.Millisecond
	if got := w.interval(); got != 300*time.Millisecond {
		t.Errorf("interval for 30ms walk = %v, want 300ms", got)
	}
	w.walkCost = time.Second
	if got := w.interval(); got != watchMaxInterval {
		t.Errorf("interval for 1s walk = %v, want clamp to %v", got, watchMaxInterval)
	}
}

// TestReportBuildPassPersistentErrorBanner proves a failed build leaves the
// idle line in an error state (✗ + error count) and recovers on the next pass.
func TestReportBuildPassPersistentErrorBanner(t *testing.T) {
	var buf bytes.Buffer
	u := newUI(&buf)
	stats := &watchStats{}

	// First build fails. (record() runs before reportBuildPass in the loop.)
	stats.record(50 * time.Millisecond)
	reportBuildPass(u, nil,
		[]compile.DiagnosticInfo{{Message: "boom"}},
		50*time.Millisecond,
		errors.New("compile: TypeScript diagnostics"), stats)
	out := buf.String()
	if !strings.Contains(out, "watching for changes") {
		t.Errorf("missing idle line:\n%s", out)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("expected error count in idle banner after failure:\n%s", out)
	}
	if !stats.lastFailed {
		t.Error("stats.lastFailed not set after a failed build")
	}

	// Next build succeeds: error state clears.
	buf.Reset()
	stats.record(40 * time.Millisecond)
	reportBuildPass(u, &compile.BuildResult{}, nil, 40*time.Millisecond, nil, stats)
	if stats.lastFailed {
		t.Error("stats.lastFailed should clear after a successful build")
	}
	if stats.lastErrCount != 0 {
		t.Errorf("lastErrCount = %d, want 0 after success", stats.lastErrCount)
	}
}

// TestClearForRebuild covers the three branches of the clear-on-rebuild gate.
func TestClearForRebuild(t *testing.T) {
	t.Run("clears when enabled and interactive", func(t *testing.T) {
		t.Setenv("FORCE_COLOR", "1")
		var buf bytes.Buffer
		clearForRebuild(&buf, &watchStats{clearScreen: true})
		if !strings.Contains(buf.String(), "\x1b[2J") {
			t.Errorf("expected a clear sequence, got %q", buf.String())
		}
	})
	t.Run("--no-clear suppresses", func(t *testing.T) {
		t.Setenv("FORCE_COLOR", "1")
		var buf bytes.Buffer
		clearForRebuild(&buf, &watchStats{clearScreen: false})
		if buf.Len() != 0 {
			t.Errorf("expected no output with clearScreen=false, got %q", buf.String())
		}
	})
	t.Run("non-interactive suppresses", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		var buf bytes.Buffer
		clearForRebuild(&buf, &watchStats{clearScreen: true})
		if buf.Len() != 0 {
			t.Errorf("expected no output when color disabled, got %q", buf.String())
		}
	})
}

func TestWatchStatsRecordCapsHistory(t *testing.T) {
	s := &watchStats{}
	for i := 0; i < watchHistoryLen+5; i++ {
		s.record(time.Duration(i) * time.Millisecond)
	}
	if s.builds != watchHistoryLen+5 {
		t.Errorf("builds = %d, want %d", s.builds, watchHistoryLen+5)
	}
	if len(s.history) != watchHistoryLen {
		t.Errorf("history length = %d, want cap %d", len(s.history), watchHistoryLen)
	}
	if s.history[len(s.history)-1] != time.Duration(watchHistoryLen+4)*time.Millisecond {
		t.Errorf("history tail = %v, want most recent build", s.history[len(s.history)-1])
	}
}
