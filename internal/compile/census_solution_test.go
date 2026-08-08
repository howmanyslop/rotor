package compile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeCensusSolutionProject lays down one referenced project of a census
// solution: a scoped package name (so no Rojo file is needed), composite +
// declaration (what a referenced project must be), and one file per entry of
// files under src/.
func writeCensusSolutionProject(t *testing.T, dir, pkgName string, references []string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The spacing after "compilerOptions" is what addTransformerPlugin splices
	// into, so plugin-project fixtures can reuse it.
	config := `{"compilerOptions": {"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"]`
	if len(references) > 0 {
		config += `,"references":` + solutionReferences(references)
	}
	config += `}`

	written := map[string]string{
		"tsconfig.json":    config,
		"package.json":     `{"name":"` + pkgName + `"}`,
		"src/globals.d.ts": noLibGlobalStubs,
	}
	for name, text := range files {
		written[filepath.ToSlash(filepath.Join("src", name))] = text
	}
	for path, content := range written {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// solutionCensusByDir keys a solution census by the project's directory name,
// which is what the fixtures name projects by.
func solutionCensusByDir(solution *SolutionDiagnostics) map[string]*ProjectDiagnostics {
	byDir := make(map[string]*ProjectDiagnostics, len(solution.Projects))
	for _, project := range solution.Projects {
		byDir[filepath.Base(filepath.FromSlash(project.ProjectDir))] = project
	}
	return byDir
}

func TestCompileSolutionDiagnosticsAttributesEveryProject(t *testing.T) {
	// Given a solution of two referenced projects, each with its own files
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./alpha", "./beta"}, true)
	writeCensusSolutionProject(t, filepath.Join(root, "alpha"), "@scope/alpha", nil, map[string]string{
		"alpha.ts": censusFiles["noany.ts"],
	})
	writeCensusSolutionProject(t, filepath.Join(root, "beta"), "@scope/beta", nil, map[string]string{
		"beta.ts": censusFiles["clean.ts"],
	})

	// When the solution census runs
	solution, err := CompileSolutionDiagnostics(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("CompileSolutionDiagnostics: %v", err)
	}

	// Then every project is reported separately, carrying its own files and its
	// own attribution — one flat census could not say which project a file
	// belonged to
	if len(solution.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(solution.Projects))
	}
	byDir := solutionCensusByDir(solution)
	for _, name := range []string{"alpha", "beta"} {
		project, ok := byDir[name]
		if !ok {
			t.Fatalf("%s missing from the census: %+v", name, solution.Projects)
		}
		if !strings.HasSuffix(filepath.ToSlash(project.ConfigPath), name+"/tsconfig.json") {
			t.Errorf("%s ConfigPath = %q, want it to name that project's tsconfig", name, project.ConfigPath)
		}
		wantFile := name + ".ts"
		got := censusFileNames(censusByFile(project))
		if !reflect.DeepEqual(got, []string{wantFile}) {
			t.Errorf("%s files = %v, want [%s]", name, got, wantFile)
		}
	}
	if got := censusByFile(byDir["alpha"])["alpha.ts"].Outcome; got != FileOutcomeTransformerDiagnostic {
		t.Errorf("alpha.ts outcome = %q, want %q", got, FileOutcomeTransformerDiagnostic)
	}
	if got := censusByFile(byDir["beta"])["beta.ts"].Outcome; got != FileOutcomeOK {
		t.Errorf("beta.ts outcome = %q, want %q", got, FileOutcomeOK)
	}
}

func TestCompileSolutionDiagnosticsFailingProjectDoesNotStopTheRest(t *testing.T) {
	// Given a solution whose first project cannot be set up at all — a
	// non-package project with no Rojo file — and a second that depends on it
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./app"}, true)
	writeCensusSolutionProject(t, filepath.Join(root, "broken"), "broken", nil, map[string]string{
		"broken.ts": censusFiles["clean.ts"],
	})
	writeCensusSolutionProject(t, filepath.Join(root, "app"), "@scope/app", []string{"../broken"}, map[string]string{
		"app.ts": censusFiles["noany.ts"],
	})

	// When the solution census runs
	solution, err := CompileSolutionDiagnostics(filepath.Join(root, "tsconfig.json"), ProjectOptions{})

	// Then the failure is reported, and the dependent project is censused
	// anyway. A build blocks dependents of a failed project; a census that did
	// the same would silently shrink, which is the one failure mode it exists
	// to prevent.
	if err == nil {
		t.Fatal("a project that could not be set up did not fail the run")
	}
	byDir := solutionCensusByDir(solution)
	broken, ok := byDir["broken"]
	if !ok {
		t.Fatalf("broken missing from the census: %+v", solution.Projects)
	}
	if len(broken.Diagnostics) == 0 {
		t.Error("the failing project carries no diagnostic explaining why")
	}
	app, ok := byDir["app"]
	if !ok {
		t.Fatalf("app missing from the census: %+v", solution.Projects)
	}
	if got := censusByFile(app)["app.ts"].Outcome; got != FileOutcomeTransformerDiagnostic {
		t.Errorf("app.ts outcome = %q, want %q — the dependent project was not censused", got, FileOutcomeTransformerDiagnostic)
	}
}

func TestCompileSolutionDiagnosticsOverlayLandsInTheOwningProject(t *testing.T) {
	// Given two sibling projects that are both clean on disk
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./alpha", "./beta"}, true)
	writeCensusSolutionProject(t, filepath.Join(root, "alpha"), "@scope/alpha", nil, map[string]string{
		"alpha.ts": censusFiles["clean.ts"],
	})
	writeCensusSolutionProject(t, filepath.Join(root, "beta"), "@scope/beta", nil, map[string]string{
		"beta.ts": censusFiles["clean.ts"],
	})

	// When one project's file is overlaid with something that fails
	solution, err := CompileSolutionDiagnostics(filepath.Join(root, "tsconfig.json"), ProjectOptions{
		Overlays: map[string]string{
			filepath.Join(root, "beta", "src", "beta.ts"): censusFiles["noany.ts"],
		},
	})
	if err != nil {
		t.Fatalf("CompileSolutionDiagnostics: %v", err)
	}

	// Then the overlay applies to that project only, and is counted there. An
	// overlay is keyed by absolute path precisely so it routes to one project.
	byDir := solutionCensusByDir(solution)
	if solution.OverlayMatches != 1 {
		t.Errorf("solution OverlayMatches = %d, want 1", solution.OverlayMatches)
	}
	if got := censusByFile(byDir["beta"])["beta.ts"].Outcome; got != FileOutcomeTransformerDiagnostic {
		t.Errorf("beta.ts outcome = %q, want %q — the overlay did not apply", got, FileOutcomeTransformerDiagnostic)
	}
	if got := censusByFile(byDir["alpha"])["alpha.ts"].Outcome; got != FileOutcomeOK {
		t.Errorf("alpha.ts outcome = %q, want %q — the overlay leaked into another project", got, FileOutcomeOK)
	}
	if got := byDir["beta"].OverlayMatches; got != 1 {
		t.Errorf("beta OverlayMatches = %d, want 1", got)
	}
	if got := byDir["alpha"].OverlayMatches; got != 0 {
		t.Errorf("alpha OverlayMatches = %d, want 0", got)
	}
}

func TestCompileSolutionDiagnosticsOverlayMatchingNothingAnywhereIsAnError(t *testing.T) {
	// Given a solution and an overlay for a file no project holds
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./alpha", "./beta"}, true)
	writeCensusSolutionProject(t, filepath.Join(root, "alpha"), "@scope/alpha", nil, map[string]string{
		"alpha.ts": censusFiles["clean.ts"],
	})
	writeCensusSolutionProject(t, filepath.Join(root, "beta"), "@scope/beta", nil, map[string]string{
		"beta.ts": censusFiles["clean.ts"],
	})

	// When the solution census runs
	_, err := CompileSolutionDiagnostics(filepath.Join(root, "tsconfig.json"), ProjectOptions{
		Overlays: map[string]string{
			filepath.Join(root, "alpha", "src", "typo.ts"): censusFiles["noany.ts"],
		},
	})

	// Then it refuses — the check is against the UNION of every project's
	// files, but an overlay that matched nothing anywhere is still the silent
	// wrong answer the single-project check exists to prevent
	if err == nil {
		t.Fatal("an overlay matching no file in any project was accepted")
	}
	if !strings.Contains(err.Error(), "typo.ts") {
		t.Errorf("error = %v, want it to name the unmatched overlay", err)
	}
}

// solutionOverlayProgram builds one project of a solution census the way
// CompileSolutionDiagnostics does — stopping short of the sidecar itself, which
// needs Node — so the overlay rules can be exercised at the seam that decides
// them.
func solutionOverlayProgram(t *testing.T, dir string, overlays map[string]string) (*solutionOverlayMatches, error) {
	t.Helper()
	tracker := newSolutionOverlayMatches()
	_, _, _, err := newProjectProgramWithOptions(dir, "", ProjectOptions{
		Overlays:         overlays,
		solutionOverlays: tracker,
	})
	return tracker, err
}

func TestSolutionOverlaysAcceptAPluginProjectTheyReach(t *testing.T) {
	// Given a project that declares a transformer plugin, and an overlay for a
	// file it holds
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	addTransformerPlugin(t, dir)

	// When this project of the solution is prepared
	overlaid := filepath.Join(dir, "src", "clean.ts")
	tracker, err := solutionOverlayProgram(t, dir, map[string]string{overlaid: censusFiles["noany.ts"]})

	// Then it is accepted, exactly as a single-project run is, and the overlay
	// is recorded against the union
	if err != nil {
		t.Fatalf("an overlay landing in a transformer-plugin project was refused: %v", err)
	}
	if got := tracker.unmatched(map[string]string{overlaid: ""}); len(got) != 0 {
		t.Errorf("unmatched = %v, want the overlay recorded as matched", got)
	}
}

func TestSolutionOverlaysDeferTheUnmatchedCheckToTheUnion(t *testing.T) {
	// Given a project that holds none of the overlaid files — every project of
	// a solution but one is in this position for any given overlay
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	elsewhere := filepath.Join(t.TempDir(), "src", "other.ts")

	// When this project of the solution is prepared
	tracker, err := solutionOverlayProgram(t, dir, map[string]string{elsewhere: censusFiles["clean.ts"]})

	// Then it does not fail here. Applying the single-project rule per project
	// would fail every overlay in every project but its owner.
	if err != nil {
		t.Fatalf("a project that holds none of the overlaid files was refused: %v", err)
	}
	if got := tracker.unmatched(map[string]string{elsewhere: ""}); len(got) != 1 {
		t.Errorf("unmatched = %v, want the overlay recorded as still unmatched", got)
	}
}

func TestCompileSolutionDiagnosticsWritesNothingToDisk(t *testing.T) {
	// Given a solution and a snapshot of its tree
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./alpha", "./beta"}, true)
	writeCensusSolutionProject(t, filepath.Join(root, "alpha"), "@scope/alpha", nil, map[string]string{
		"alpha.ts": censusFiles["clean.ts"],
	})
	writeCensusSolutionProject(t, filepath.Join(root, "beta"), "@scope/beta", nil, map[string]string{
		"beta.ts": censusFiles["typebad.ts"],
	})
	before := treeSnapshot(t, root)

	// When the solution census runs
	if _, err := CompileSolutionDiagnostics(filepath.Join(root, "tsconfig.json"), ProjectOptions{}); err != nil {
		t.Fatalf("CompileSolutionDiagnostics: %v", err)
	}

	// Then no project wrote an outDir, an include folder, a rotor.d.ts or a
	// .tsbuildinfo — a solution build writes all four
	after := treeSnapshot(t, root)
	if len(after) != len(before) {
		t.Fatalf("tree changed: %d entries before, %d after\nbefore: %v\nafter:  %v", len(before), len(after), before, after)
	}
	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("%s changed on disk", path)
		}
	}
}
