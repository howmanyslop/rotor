package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rotor/internal/assets"
	"rotor/internal/compile"
	"rotor/internal/term"
)

// Polling cadence. Snapshots are cheap (pruned os.ReadDir walks that reuse the
// enumeration's own metadata), so the default tick is 100ms; the interval
// self-throttles to 10x the measured walk cost on pathological trees. After a
// change is detected, the watcher keeps re-snapshotting on a short quiet timer
// so an editor "save all" burst lands in one rebuild instead of several.
const (
	watchMinInterval = 100 * time.Millisecond
	watchMaxInterval = time.Second
	watchQuietPeriod = 50 * time.Millisecond
	watchSettleCap   = 500 * time.Millisecond
)

// watchHistoryLen caps the build-duration history kept for the idle sparkline.
const watchHistoryLen = 12

// fileStamp captures enough of a file's state to detect edits via polling.
type fileStamp struct {
	exists  bool
	modTime time.Time
	size    int64
}

// watchStats accumulates per-session build counts, durations, and the last
// build's outcome for the watch idle line. The trailing fields are watch
// configuration (set once at session start) rather than running state.
type watchStats struct {
	builds  int
	history []time.Duration // most recent last, capped at watchHistoryLen

	// Last-build outcome, used for the persistent error banner on the idle
	// line and for fail<->pass transition cues.
	lastFailed   bool
	lastErrCount int

	// Session configuration.
	maxErrors   int  // cap for rendered code frames (0 = unlimited)
	bell        bool // ring the terminal bell on a fail<->pass flip (TTY only)
	clearScreen bool // clear the screen before each rebuild (TTY only)
}

// watchOptions carries the rebuild-display configuration into runBuildWatch.
type watchOptions struct {
	maxErrors   int
	bell        bool
	clearScreen bool
}

func (s *watchStats) record(d time.Duration) {
	s.builds++
	s.history = append(s.history, d)
	if len(s.history) > watchHistoryLen {
		s.history = s.history[len(s.history)-watchHistoryLen:]
	}
}

// treeWatcher snapshots a project tree for poll-based watching. node_modules,
// dot-directories, and the build-written output/include dirs are pruned, and
// editor junk files are ignored, so the walk stays proportional to the
// project's own sources.
type treeWatcher struct {
	root     string
	skipDirs []string      // absolute dirs pruned from the walk (out/, include/)
	walkCost time.Duration // last snapshot duration; drives the adaptive interval
}

func newTreeWatcher(root string, skipDirs ...string) *treeWatcher {
	w := &treeWatcher{root: root}
	w.setSkipDirs(skipDirs...)
	return w
}

func (w *treeWatcher) setSkipDirs(dirs ...string) {
	w.skipDirs = w.skipDirs[:0]
	for _, d := range dirs {
		if d != "" {
			w.skipDirs = append(w.skipDirs, filepath.Clean(d))
		}
	}
}

func (w *treeWatcher) snapshot() map[string]fileStamp {
	start := time.Now()
	stamps := make(map[string]fileStamp)
	w.walk(w.root, stamps)
	w.walkCost = time.Since(start)
	return stamps
}

func (w *treeWatcher) walk(dir string, stamps map[string]fileStamp) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if entry.IsDir() {
			if shouldPruneDir(name) || w.isSkipDir(path) {
				continue
			}
			w.walk(path, stamps)
			continue
		}
		if isJunkFile(name) {
			continue
		}
		if strings.EqualFold(name, "flamework.build") ||
			strings.EqualFold(name, compile.RotorTypesFileName) ||
			strings.EqualFold(name, compile.EnvDeclFileName) ||
			strings.EqualFold(name, compile.AssetDeclFileName) ||
			strings.EqualFold(name, compile.MacroDeclFileName) {
			continue
		}
		// entry.Info() reuses the metadata the directory enumeration already
		// produced (free on Windows, one lstat elsewhere) instead of a fresh
		// os.Stat per file.
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stamps[path] = fileStamp{exists: true, modTime: info.ModTime(), size: info.Size()}
	}
}

func (w *treeWatcher) isSkipDir(path string) bool {
	for _, d := range w.skipDirs {
		if strings.EqualFold(path, d) {
			return true
		}
	}
	return false
}

// interval is the adaptive poll cadence: 10x the last walk cost, clamped.
func (w *treeWatcher) interval() time.Duration {
	iv := w.walkCost * 10
	if iv < watchMinInterval {
		return watchMinInterval
	}
	if iv > watchMaxInterval {
		return watchMaxInterval
	}
	return iv
}

// shouldPruneDir reports whether a directory is never watched: dependency
// trees and dot-directories (.git, .vscode, ...). Root lockfiles and
// package.json stay in the walk, so installs still trigger rebuilds.
func shouldPruneDir(name string) bool {
	return name == "node_modules" || strings.HasPrefix(name, ".")
}

// isJunkFile reports whether a basename is editor write-noise that should
// never trigger a rebuild (vim swap/backup probes, emacs locks, OS droppings).
func isJunkFile(name string) bool {
	if strings.HasPrefix(name, ".#") || name == "4913" || name == ".DS_Store" || name == "Thumbs.db" {
		return true
	}
	if strings.HasSuffix(name, "~") {
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".swp", ".swo", ".tmp":
		return true
	}
	return false
}

// diffStamps returns every path whose stamp differs between two snapshots
// (modified, added, or removed), sorted.
func diffStamps(before, after map[string]fileStamp) []string {
	var changed []string
	for path, st := range after {
		if before[path] != st {
			changed = append(changed, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

// settleChanges waits out an editor write burst: starting from the first
// changed snapshot, it re-snapshots on the quiet timer until a snapshot adds
// nothing new (capped at watchSettleCap), then returns the settled snapshot
// and every path that changed vs base.
func settleChanges(base, first map[string]fileStamp, snap func() map[string]fileStamp, sleep func(time.Duration)) (map[string]fileStamp, []string) {
	current := first
	for waited := time.Duration(0); waited < watchSettleCap; waited += watchQuietPeriod {
		sleep(watchQuietPeriod)
		next := snap()
		quiet := len(diffStamps(current, next)) == 0
		current = next
		if quiet {
			break
		}
	}
	return current, diffStamps(base, current)
}

// runWatch runs an initial check, then polls the watched file set (the parsed
// file list plus tsconfig.json) and re-runs the full check whenever anything
// changes. Exits only via Ctrl+C.
func runWatch(dir string, out io.Writer, checkers *int) int {
	u := newUI(out)
	stats := &watchStats{}

	res := runCheck(dir, out, checkers)
	stats.record(res.elapsed)
	stamps := snapshotFiles(res.watchFiles)
	u.watchBanner(len(res.watchFiles), stats)

	for {
		time.Sleep(watchMinInterval)
		next := snapshotFiles(res.watchFiles)
		changed := diffStamps(stamps, next)
		if len(changed) == 0 {
			continue
		}
		snap := func() map[string]fileStamp { return snapshotFiles(res.watchFiles) }
		var settled map[string]fileStamp
		settled, changed = settleChanges(stamps, next, snap, time.Sleep)
		if len(changed) == 0 {
			stamps = settled
			continue
		}
		u.watchChanges(changed)

		res = runCheck(dir, out, checkers)
		stats.record(res.elapsed)
		// Stamp NEW files at their current state, but keep the pre-check
		// stamps for surviving files so an edit made while the check ran is
		// still detected on the next tick.
		stamps = mergePreStamps(snapshotFiles(res.watchFiles), settled)
		u.watchBanner(len(res.watchFiles), stats)
	}
}

// mergePreStamps overlays pre-build stamps onto a fresh post-build snapshot:
// files present in both keep their pre-build stamp (mid-build edits diff on
// the next tick); files only in the fresh snapshot keep their fresh stamp.
func mergePreStamps(post, pre map[string]fileStamp) map[string]fileStamp {
	for path, st := range pre {
		if _, ok := post[path]; ok {
			post[path] = st
		}
	}
	return post
}

func snapshotFiles(files []string) map[string]fileStamp {
	stamps := make(map[string]fileStamp, len(files))
	for _, f := range files {
		stamps[f] = stat(f)
	}
	return stamps
}

func runBuildWatch(dir, tsConfigPath string, opts projectOptions, wopts watchOptions) int {
	u := newUI(os.Stdout)
	stats := &watchStats{
		maxErrors:   wopts.maxErrors,
		bell:        wopts.bell,
		clearScreen: wopts.clearScreen,
	}

	w := newTreeWatcher(dir)
	w.setSkipDirs(guessedOutputDir(dir, nil), watchIncludeDir(dir, opts))

	// The baseline snapshot is taken BEFORE the build, so a file saved while
	// the build runs still differs from the baseline on the next tick instead
	// of being silently absorbed (the v1 lost-update bug). Build outputs land
	// in the pruned out/include dirs and never dirty the baseline.
	baseline := w.snapshot()
	result, diags, elapsed, err := runBuildOnce(dir, tsConfigPath, opts)
	stats.record(elapsed)
	reportBuildPass(u, result, diags, elapsed, err, stats)
	// The build reveals the real output dir (it may not be out/); prune the
	// baseline to match the refreshed skip set, or the now-unwalked entries
	// would read as deletions and trigger a spurious rebuild.
	w.setSkipDirs(guessedOutputDir(dir, result), watchIncludeDir(dir, opts))
	pruneStamps(baseline, w.skipDirs)

	for {
		time.Sleep(w.interval())
		next := w.snapshot()
		changed := diffStamps(baseline, next)
		if len(changed) == 0 {
			continue
		}
		baseline, changed = settleChanges(baseline, next, w.snapshot, time.Sleep)
		if len(changed) == 0 {
			continue
		}
		clearForRebuild(os.Stdout, stats)
		u.watchChanges(changed)

		result, diags, elapsed, err = runBuildOnce(dir, tsConfigPath, opts)
		stats.record(elapsed)
		reportBuildPass(u, result, diags, elapsed, err, stats)
		w.setSkipDirs(guessedOutputDir(dir, result), watchIncludeDir(dir, opts))
		pruneStamps(baseline, w.skipDirs)
	}
}

// pruneStamps drops snapshot entries that live under any of the skip dirs,
// keeping an existing baseline consistent after the skip set changes.
func pruneStamps(stamps map[string]fileStamp, skipDirs []string) {
	for path := range stamps {
		for _, d := range skipDirs {
			if strings.EqualFold(path, d) ||
				strings.HasPrefix(strings.ToLower(path), strings.ToLower(d)+string(filepath.Separator)) {
				delete(stamps, path)
				break
			}
		}
	}
}

// watchIncludeDir mirrors compile.resolveIncludePath for tree pruning: the
// include dir is rewritten by every build, so leaving it in the walk would
// make the pre-build baseline self-trigger an endless rebuild loop.
func watchIncludeDir(dir string, opts projectOptions) string {
	if opts.includePath == "" {
		return filepath.Join(dir, "include")
	}
	abs, err := filepath.Abs(filepath.FromSlash(opts.includePath))
	if err != nil {
		return ""
	}
	return abs
}

// clearForRebuild wipes the screen (and scrollback) before a rebuild so each
// pass starts clean, like vite/tsc --watch. TTY only (gated on color, the
// interactive proxy) and opt-out via --no-clear; piped output keeps the full
// scroll history with the per-rebuild separator rule instead.
func clearForRebuild(w io.Writer, stats *watchStats) {
	if stats.clearScreen && term.ColorEnabled(w) {
		fmt.Fprint(w, "\x1b[2J\x1b[3J\x1b[H")
	}
}

func reportBuildPass(u *ui, result *compile.BuildResult, diags []compile.DiagnosticInfo, elapsed time.Duration, err error, stats *watchStats) {
	failed := err != nil
	// Ring the bell on a fail<->pass flip (not on the first build, which has no
	// prior state to transition from) so an unattended watch surfaces the
	// change audibly. Opt-in via --bell; TTY only.
	if stats.builds > 1 && failed != stats.lastFailed && stats.bell && isTerminal(os.Stderr) {
		fmt.Fprint(os.Stderr, "\a")
	}
	stats.lastFailed = failed
	if failed {
		n := len(diags)
		if n == 0 {
			n = 1 // a config/validation error with no structured diagnostics
		}
		stats.lastErrCount = n
		newUI(os.Stderr).buildFailure(err.Error(), diags, stats.maxErrors)
	} else if result != nil {
		stats.lastErrCount = 0
		if result.WroteRotorTypes {
			u.noteLine(compile.RotorTypesFileName + "  (generated — editor types for rotor macros)")
		}
		if result.WroteLockfile {
			u.noteLine(assets.LockfileName + "  (updated — uploaded new $asset assets)")
		}
		u.buildSuccess(len(result.Outputs), len(result.EmittedFiles), len(result.Outputs)-len(result.EmittedFiles), elapsed)
	}
	u.watchIdle(stats)
}

func guessedOutputDir(projectDir string, result *compile.BuildResult) string {
	if result != nil && result.OutputDir != "" {
		return result.OutputDir
	}
	if result == nil || len(result.Outputs) == 0 {
		return filepath.Join(projectDir, "out")
	}
	relPaths := make([]string, 0, len(result.Outputs))
	for rel := range result.Outputs {
		relPaths = append(relPaths, rel)
	}
	sort.Strings(relPaths)
	first := filepath.Clean(filepath.FromSlash(relPaths[0]))
	parts := strings.Split(first, string(filepath.Separator))
	if len(parts) == 0 {
		return filepath.Join(projectDir, "out")
	}
	return filepath.Join(projectDir, parts[0])
}

func stat(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, modTime: info.ModTime(), size: info.Size()}
}
