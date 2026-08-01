package compile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordingSolutionDrainer struct {
	drained []string
	fail    string
}

func (d *recordingSolutionDrainer) Drain(project SolutionProject) (*BuildResult, []string, error) {
	d.drained = append(d.drained, filepath.Base(filepath.Dir(project.ConfigPath)))
	if project.ConfigPath == d.fail {
		return &BuildResult{Diagnostics: []DiagnosticInfo{{Message: "failed project"}}}, []string{"failed project"}, errors.New("failed project")
	}
	return &BuildResult{Outputs: map[string]string{}}, nil, nil
}

func TestSolutionGraph(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./app"}, true)
	writeSolutionConfig(t, filepath.Join(root, "app"), "tsconfig.json", []string{"../left", "../right"}, false)
	writeSolutionConfig(t, filepath.Join(root, "left"), "tsconfig.json", []string{"../shared"}, false)
	writeSolutionConfig(t, filepath.Join(root, "right"), "tsconfig.json", []string{"../shared"}, false)
	writeSolutionConfig(t, filepath.Join(root, "shared"), "tsconfig.json", nil, false)

	graph, err := BuildSolutionGraph(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildSolutionGraph: %v", err)
	}

	got := solutionProjectNames(graph.Projects)
	want := []string{"shared", "left", "right", "app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("project order = %v, want %v", got, want)
	}
}

func TestSolutionCoordinator(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./left", "./right"}, true)
	writeSolutionConfig(t, filepath.Join(root, "left"), "tsconfig.json", []string{"../shared"}, false)
	writeSolutionConfig(t, filepath.Join(root, "right"), "tsconfig.json", []string{"../shared"}, false)
	writeSolutionConfig(t, filepath.Join(root, "shared"), "tsconfig.json", nil, false)
	drainer := &recordingSolutionDrainer{}

	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	_, _, err = coordinator.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}

	want := []string{"shared", "left", "right"}
	if !reflect.DeepEqual(drainer.drained, want) {
		t.Fatalf("drained projects = %v, want %v", drainer.drained, want)
	}
	sharedConfig := filepath.Join(root, "shared", "tsconfig.json")
	state, ok := coordinator.ProjectState(sharedConfig)
	if !ok || !state.UpToDate {
		t.Fatalf("shared state = %+v, found = %t, want up-to-date", state, ok)
	}
}

func TestSolutionCoordinatorBlocksDependentAfterFailure(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./app"}, true)
	writeSolutionConfig(t, filepath.Join(root, "app"), "tsconfig.json", []string{"../broken"}, false)
	writeSolutionConfig(t, filepath.Join(root, "broken"), "tsconfig.json", nil, false)
	brokenConfig := filepath.Join(root, "broken", "tsconfig.json")
	drainer := &recordingSolutionDrainer{fail: brokenConfig}

	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	_, _, err = coordinator.Drain()
	if err == nil {
		t.Fatal("Drain unexpectedly succeeded")
	}

	if want := []string{"broken"}; !reflect.DeepEqual(drainer.drained, want) {
		t.Fatalf("drained projects = %v, want %v", drainer.drained, want)
	}
	state, ok := coordinator.ProjectState(filepath.Join(root, "app", "tsconfig.json"))
	if !ok || state.BlockedBy != brokenConfig {
		t.Fatalf("app state = %+v, found = %t, want blocked by %s", state, ok, brokenConfig)
	}

	drainer.fail = ""
	_, _, err = coordinator.Drain()
	if err != nil {
		t.Fatalf("Drain after dependency recovery: %v", err)
	}
	if want := []string{"broken", "broken", "app"}; !reflect.DeepEqual(drainer.drained, want) {
		t.Fatalf("drained projects after recovery = %v, want %v", drainer.drained, want)
	}
}

func TestSolutionCoordinatorSkipsUpToDateProjects(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./child"}, true)
	writeBuildableSolutionProject(t, child)

	coordinator, err := NewSolutionCoordinator(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	first, _, err := coordinator.Drain()
	if err != nil {
		t.Fatalf("first Drain: %v", err)
	}
	if len(first.EmittedFiles) == 0 {
		t.Fatal("first Drain emitted no files")
	}
	second, _, err := coordinator.Drain()
	if err != nil {
		t.Fatalf("second Drain: %v", err)
	}
	if len(second.EmittedFiles) != 0 {
		t.Fatalf("second Drain emitted files = %v, want none", second.EmittedFiles)
	}
}

func TestSolutionBuildOrder(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./app"}, true)
	writeSolutionConfig(t, filepath.Join(root, "app"), "tsconfig.json", []string{"../dependency"}, false)
	writeSolutionConfig(t, filepath.Join(root, "dependency"), "tsconfig.json", nil, false)
	drainer := &recordingSolutionDrainer{}

	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	_, _, err = coordinator.Drain()
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}

	if want := []string{"dependency", "app"}; !reflect.DeepEqual(drainer.drained, want) {
		t.Fatalf("drained projects = %v, want %v", drainer.drained, want)
	}
}

func TestSolutionCycle(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first"}, true)
	writeSolutionConfig(t, filepath.Join(root, "first"), "tsconfig.json", []string{"../second"}, false)
	writeSolutionConfig(t, filepath.Join(root, "second"), "tsconfig.json", []string{"../first"}, false)

	_, err := BuildSolutionGraph(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err == nil {
		t.Fatal("BuildSolutionGraph unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "first") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("cycle error = %q, want cycle path", err)
	}
}

func TestEmitDeclarationOnly(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./child"}, true)
	writeBuildableSolutionProject(t, child)

	_, messages, err := BuildSolutionWithOptions(filepath.Join(root, "tsconfig.json"), ProjectOptions{EmitDeclarationOnly: true})
	if err != nil {
		t.Fatalf("BuildSolutionWithOptions: %v (%v)", err, messages)
	}
	if _, err := os.Stat(filepath.Join(child, "out", "main.d.ts")); err != nil {
		t.Fatalf("declaration output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(child, "out", "main.luau")); !os.IsNotExist(err) {
		t.Fatalf("Luau output stat error = %v, want not exists", err)
	}
}

func writeSolutionConfig(t *testing.T, dir, name string, references []string, coordinator bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{}`
	if coordinator {
		config = `{"files":[],"references":` + solutionReferences(references) + `}`
	} else if len(references) > 0 {
		config = `{"references":` + solutionReferences(references) + `}`
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
}

func solutionReferences(paths []string) string {
	result := "["
	for index, path := range paths {
		if index > 0 {
			result += ","
		}
		result += `{"path":"` + path + `"}`
	}
	return result + "]"
}

func solutionProjectNames(projects []SolutionProject) []string {
	names := make([]string, len(projects))
	for index, project := range projects {
		names[index] = filepath.Base(filepath.Dir(project.ConfigPath))
	}
	return names
}

func writeBuildableSolutionProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"]}`
	files := map[string]string{
		"tsconfig.json":    config,
		"package.json":     `{"name":"@scope/solution-child"}`,
		"src/globals.d.ts": noLibGlobalStubs,
		"src/main.ts":      "export const value = 1;\n",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
