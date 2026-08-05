package compile

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"rotor/tsgo/vfs/osvfs"
)

// solutionOverlayMatches collects, across every project of a solution census,
// the overlay keys that named a file some project's program actually held.
//
// It exists because the single-project rule cannot be applied per project under
// --build. A solution's files are partitioned between its projects, so an
// overlay for one project's file matches nothing in all the others — applying
// matchOverlaysToProgram per project would fail every overlay in every project
// but one. The invariant the check protects is unchanged, only its scope is:
// an overlay that matched nothing ANYWHERE is still a silent wrong answer, and
// still fails the run.
type solutionOverlayMatches struct {
	mu      sync.Mutex
	matched map[string]struct{}
}

func newSolutionOverlayMatches() *solutionOverlayMatches {
	return &solutionOverlayMatches{matched: map[string]struct{}{}}
}

// record adds one project's matched keys, already normalized by
// overlayKeysInProgram. Projects drain in parallel, so this is locked.
func (m *solutionOverlayMatches) record(matched map[string]struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range matched {
		m.matched[key] = struct{}{}
	}
}

// unmatched returns the caller's overlay keys, in their original spelling, that
// no project matched.
func (m *solutionOverlayMatches) unmatched(overlays map[string]string) []string {
	caseSensitive := osvfs.FS().UseCaseSensitiveFileNames()
	m.mu.Lock()
	defer m.mu.Unlock()
	var unmatched []string
	for path := range overlays {
		if _, ok := m.matched[normalizeOverlayPath(path, caseSensitive)]; !ok {
			unmatched = append(unmatched, path)
		}
	}
	sort.Strings(unmatched)
	return unmatched
}

// solutionCensusDrainer censuses one project of a solution and keeps the
// result, standing in for solutionBuildDrainer's write-to-disk Drain.
//
// It never returns an error. SolutionCoordinator.Drain blocks the dependents of
// a failed project — right for a build, whose dependents consume the failed
// project's missing output, and wrong for a census, where a blocked project is
// simply a project that goes unreported. Nothing a census reads is produced by
// another project: TypeScript redirects a project reference to the referenced
// project's SOURCE, so no census depends on another having emitted. Per-project
// failures are folded into that project's diagnostics and re-raised by
// CompileSolutionDiagnostics after every project has had its turn.
type solutionCensusDrainer struct {
	importPathMap  map[string]string
	overlays       map[string]string
	overlayMatches *solutionOverlayMatches

	mu       sync.Mutex
	censuses map[string]*ProjectDiagnostics
	errs     map[string]error
}

func (d *solutionCensusDrainer) Drain(project SolutionProject) (*BuildResult, []string, error) {
	options := project.Options
	options.TsConfigPath = project.ConfigPath
	options.crossProjectImportPathMap = d.importPathMap
	options.Overlays = d.overlays
	options.solutionOverlays = d.overlayMatches
	// The same census policy `rotor diagnostics` applies to a single project,
	// applied to every project of the solution: rotor's own "comment directives
	// are not supported" diagnostic must be reported rather than suppressed by
	// a referenced project's own rbxts key, or one project's setting would
	// silently change how its files are classified.
	options.AllowCommentDirectives = false

	census, err := CompileProjectDiagnostics(filepath.Dir(project.ConfigPath), options)
	if err != nil && len(census.Diagnostics) == 0 {
		census.Diagnostics = append(census.Diagnostics, DiagnosticInfo{Message: err.Error()})
	}

	d.mu.Lock()
	d.censuses[project.ConfigPath] = census
	d.errs[project.ConfigPath] = err
	d.mu.Unlock()
	return &BuildResult{Outputs: map[string]string{}}, nil, nil
}

// CompileSolutionDiagnostics is CompileProjectDiagnostics for a solution: it
// censuses every project the entry tsconfig references (and everything they
// reference, transitively), in dependency order, and returns one
// *ProjectDiagnostics per project.
//
// ProjectDiagnostics already carries ProjectDir and ConfigPath, so the slice
// needs no new type to say which project a census belongs to. It carries no
// per-project error, though, so a project that could not be set up at all has
// that failure folded into its Diagnostics — exactly the conversion
// `rotor diagnostics` already does at the top level — and the first such
// failure is returned as the run's error once every project has been censused.
//
// Like the single-project entry point it writes nothing: no outDir, no include
// folder, no rotor.d.ts, no .tsbuildinfo.
//
// A non-nil error does NOT mean the returned slice is empty or useless. Every
// project that produced a census is in it.
func CompileSolutionDiagnostics(tsConfigPath string, entry ProjectOptions) ([]*ProjectDiagnostics, error) {
	graph, err := BuildSolutionGraph(tsConfigPath, entry)
	if err != nil {
		return nil, err
	}

	// The same cross-project import path map a solution BUILD computes. Without
	// it each project falls back to a project-local map, and imports that cross
	// a project boundary transform differently than they would in a real build
	// — a census whose verdicts do not match what `rotor build --build` would
	// say is worth less than no census.
	importPathMap, metadata := populateCrossProjectMetadata(graph)
	drainer := &solutionCensusDrainer{
		importPathMap:  importPathMap,
		overlays:       entry.Overlays,
		overlayMatches: newSolutionOverlayMatches(),
		censuses:       map[string]*ProjectDiagnostics{},
		errs:           map[string]error{},
	}
	coordinator, err := newSolutionCoordinator(graph, drainer, metadata, effectiveSolutionBuilders(entry))
	if err != nil {
		return nil, err
	}
	if _, _, err := coordinator.Drain(); err != nil {
		return nil, err
	}

	projects := make([]*ProjectDiagnostics, 0, len(graph.Projects))
	var firstErr error
	for _, project := range graph.Projects {
		census, ok := drainer.censuses[project.ConfigPath]
		if !ok {
			continue
		}
		projects = append(projects, census)
		if projectErr := drainer.errs[project.ConfigPath]; projectErr != nil && firstErr == nil {
			firstErr = projectErr
		}
	}
	if firstErr != nil {
		// Deliberately ahead of the overlay check: a project that never built a
		// program contributed no files to match against, so reporting its
		// overlays as unmatched would name a symptom instead of the cause.
		return projects, firstErr
	}
	if unmatched := drainer.overlayMatches.unmatched(entry.Overlays); len(unmatched) > 0 {
		return projects, fmt.Errorf("compile: overlay matches no file in the solution: %s", strings.Join(unmatched, ", "))
	}
	return projects, nil
}
