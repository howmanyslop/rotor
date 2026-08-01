package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"rotor/internal/compile"
	"rotor/tsgo/fswatch"
)

type compileGate struct {
	mu               sync.Mutex
	running, pending bool
	done             chan struct{}
	run              func()
}

func newCompileGate(run func()) *compileGate { return &compileGate{run: run} }

func (g *compileGate) Trigger() {
	g.mu.Lock()
	if g.running {
		g.pending = true
		g.mu.Unlock()
		return
	}
	g.running, g.done = true, make(chan struct{})
	g.mu.Unlock()
	go func() {
		for {
			g.run()
			g.mu.Lock()
			if !g.pending {
				g.running = false
				close(g.done)
				g.mu.Unlock()
				return
			}
			g.pending = false
			g.mu.Unlock()
		}
	}()
}

func (g *compileGate) Drain() {
	for {
		g.mu.Lock()
		if !g.running {
			g.mu.Unlock()
			return
		}
		done := g.done
		g.mu.Unlock()
		<-done
	}
}

type solutionWatchEvents struct {
	projects map[string]struct{}
	configs  map[string]struct{}
	assets   map[string]map[string]bool
	paths    map[string]struct{}
}

func runBuildSolutionWatch(tsConfigPath string, opts projectOptions, reload func() (projectOptions, error), wopts watchOptions) int {
	s := &solutionWatchEvents{projects: map[string]struct{}{}, configs: map[string]struct{}{}, assets: map[string]map[string]bool{}, paths: map[string]struct{}{}}
	coordinator, err := compile.NewSolutionCoordinator(tsConfigPath, projectCompileOptions(tsConfigPath, opts))
	if err != nil {
		reportBuildPass(newUI(fmtWriter{}), nil, nil, 0, err, &watchStats{})
		return 1
	}
	var mu sync.Mutex
	var watches []fswatch.Watch
	watcher := fswatch.Default()
	refresh := func() {}
	cycle := func() {
		mu.Lock()
		events := *s
		s = &solutionWatchEvents{projects: map[string]struct{}{}, configs: map[string]struct{}{}, assets: map[string]map[string]bool{}, paths: map[string]struct{}{}}
		mu.Unlock()
		if len(events.configs) > 0 {
			for _, set := range coordinator.WatchSets() {
				for _, config := range set.TsConfigPaths {
					if _, changed := events.configs[config]; changed {
						events.projects[set.ProjectPath] = struct{}{}
					}
				}
			}
			if next, reloadErr := reload(); reloadErr != nil {
				fmt.Fprintln(stderrWriter{}, reloadErr)
				return
			} else {
				opts = next
			}
			if reloadErr := coordinator.Reload(tsConfigPath, projectCompileOptions(tsConfigPath, opts)); reloadErr != nil {
				fmt.Fprintln(stderrWriter{}, reloadErr)
				return
			}
			refresh()
			if len(events.projects) == 0 {
				for _, set := range coordinator.WatchSets() {
					events.projects[set.ProjectPath] = struct{}{}
				}
			}
		}
		for project := range events.projects {
			coordinator.Invalidate(project)
		}
		if len(events.projects) > 0 || len(events.configs) > 0 {
			result, diags, drainErr := coordinator.Drain()
			reportBuildPass(newUI(fmtWriter{}), result, diagsToInfos(diags), 0, drainErr, &watchStats{maxErrors: wopts.maxErrors})
			refresh()
		}
		for project, paths := range events.assets {
			changes := make([]compile.WatchAssetEvent, 0, len(paths))
			for path, deleted := range paths {
				changes = append(changes, compile.WatchAssetEvent{Path: path, Deleted: deleted})
			}
			sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
			if err := coordinator.DrainAssets(project, changes); err != nil {
				fmt.Fprintln(stderrWriter{}, err)
			}
		}
	}
	gate := newCompileGate(cycle)
	refresh = func() {
		for _, watch := range watches {
			_ = watch.Close()
		}
		watches = nil
		for _, set := range coordinator.WatchSets() {
			project := set.ProjectPath
			callback := func(events []fswatch.Event, watchErr error) {
				mu.Lock()
				defer mu.Unlock()
				if watchErr != nil && !errors.Is(watchErr, fswatch.ErrOverflow) {
					return
				}
				for _, event := range events {
					s.paths[event.Path] = struct{}{}
					if watchCompilable(event.Path) {
						s.projects[project] = struct{}{}
					} else {
						if s.assets[project] == nil {
							s.assets[project] = map[string]bool{}
						}
						s.assets[project][event.Path] = event.Kind == fswatch.EventDelete
					}
				}
				if watchErr != nil {
					s.projects[project] = struct{}{}
				}
				gate.Trigger()
			}
			for _, root := range set.RootDirs {
				if watch, watchErr := watcher.WatchDirectory(root, callback, fswatch.WithRecursive()); watchErr == nil {
					watches = append(watches, watch)
				}
			}
			for _, config := range append(set.TsConfigPaths, set.RojoConfigs...) {
				config := config
				if watch, watchErr := watcher.WatchFile(config, func(events []fswatch.Event, watchErr error) {
					mu.Lock()
					s.configs[config] = struct{}{}
					for _, event := range events {
						s.paths[event.Path] = struct{}{}
					}
					mu.Unlock()
					gate.Trigger()
				}); watchErr == nil {
					watches = append(watches, watch)
				}
			}
		}
	}
	refresh()
	for _, set := range coordinator.WatchSets() {
		s.projects[set.ProjectPath] = struct{}{}
	}
	gate.Trigger()
	gate.Drain()
	select {}
}

func watchCompilable(path string) bool {
	return filepath.Ext(path) == ".ts" || filepath.Ext(path) == ".tsx" || filepath.Ext(path) == ".d.ts"
}
func diagsToInfos(messages []string) []compile.DiagnosticInfo {
	result := make([]compile.DiagnosticInfo, len(messages))
	for i, message := range messages {
		result[i] = compile.DiagnosticInfo{Message: message}
	}
	return result
}

type fmtWriter struct{}

func (fmtWriter) Write(p []byte) (int, error) { return fmt.Print(string(p)) }

type stderrWriter struct{}

func (stderrWriter) Write(p []byte) (int, error) { return fmt.Print(string(p)) }
