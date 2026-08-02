package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSolutionWriteRoots(t *testing.T) {
	tests := []struct {
		name        string
		emitInclude bool
		includePath func(string) string
		firstOut    func(string) string
		secondOut   func(string) string
		wantEdge    bool
	}{
		{name: "disjoint outputs",
			firstOut:  func(root string) string { return filepath.Join(root, "outputs", "first") },
			secondOut: func(root string) string { return filepath.Join(root, "outputs", "second") },
		},
		{name: "identical outputs",
			firstOut:  func(root string) string { return filepath.Join(root, "outputs") },
			secondOut: func(root string) string { return filepath.Join(root, "outputs") }, wantEdge: true,
		},
		{name: "nested outputs",
			firstOut:  func(root string) string { return filepath.Join(root, "outputs") },
			secondOut: func(root string) string { return filepath.Join(root, "outputs", "nested") }, wantEdge: true,
		},
		{name: "shared include output enabled",
			emitInclude: true,
			includePath: func(root string) string { return filepath.Join(root, "shared-include") },
			firstOut:    func(root string) string { return filepath.Join(root, "outputs", "first") },
			secondOut:   func(root string) string { return filepath.Join(root, "outputs", "second") }, wantEdge: true,
		},
		{name: "shared include output disabled",
			includePath: func(root string) string { return filepath.Join(root, "shared-include") },
			firstOut:    func(root string) string { return filepath.Join(root, "outputs", "first") },
			secondOut:   func(root string) string { return filepath.Join(root, "outputs", "second") },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			firstDir := filepath.Join(root, "first")
			secondDir := filepath.Join(root, "second")
			writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
			firstOut := test.firstOut(root)
			secondOut := test.secondOut(root)
			directories := []string{firstOut, secondOut}
			if test.includePath != nil {
				directories = append(directories, test.includePath(root))
			}
			for _, directory := range directories {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeMetadataSolutionProject(t, firstDir, firstOut)
			writeMetadataSolutionProject(t, secondDir, secondOut)

			entry := ProjectOptions{EmitIncludeFiles: test.emitInclude}
			if test.includePath != nil {
				entry.IncludePath = test.includePath(root)
			}
			metadata := solutionWriteMetadataForTest(t, filepath.Join(root, "tsconfig.json"), entry)
			coordinator, err := NewSolutionCoordinatorWithDrainer(
				filepath.Join(root, "tsconfig.json"), entry, &recordingSolutionDrainer{},
			)
			if err != nil {
				t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
			}

			firstConfig := filepath.Join(firstDir, "tsconfig.json")
			secondConfig := filepath.Join(secondDir, "tsconfig.json")
			wantFirst := []string{canonicalMetadataPath(t, firstOut)}
			wantSecond := []string{canonicalMetadataPath(t, secondOut)}
			if test.emitInclude {
				includePath := test.includePath(root)
				wantFirst = append(wantFirst, canonicalMetadataPath(t, includePath))
				wantSecond = append(wantSecond, canonicalMetadataPath(t, includePath))
			}
			if got := metadata.writeRoots[firstConfig]; !reflect.DeepEqual(got, wantFirst) {
				t.Fatalf("first write roots = %v, want %v", got, wantFirst)
			}
			if got := metadata.writeRoots[secondConfig]; !reflect.DeepEqual(got, wantSecond) {
				t.Fatalf("second write roots = %v, want %v", got, wantSecond)
			}
			if test.wantEdge {
				want := map[string][]string{secondConfig: {firstConfig}}
				if !reflect.DeepEqual(coordinator.waitOnlyDependencies, want) {
					t.Fatalf("wait-only dependencies = %v, want %v", coordinator.waitOnlyDependencies, want)
				}
			} else if len(coordinator.waitOnlyDependencies) != 0 {
				t.Fatalf("wait-only dependencies = %v, want none", coordinator.waitOnlyDependencies)
			}
		})
	}
}

func TestSolutionWriteRootsLeavesInvalidConfigEmpty(t *testing.T) {
	root := t.TempDir()
	childDir := filepath.Join(root, "child")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./child"}, true)
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "tsconfig.json"), []byte(`{"compilerOptions":{"outDir":42}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	childConfig := filepath.Join(childDir, "tsconfig.json")
	metadata := solutionWriteMetadataForTest(t, filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if got := metadata.writeRoots[childConfig]; len(got) != 0 {
		t.Fatalf("invalid config write roots = %v, want empty", got)
	}
}

func TestSolutionWriteRootsCanonicalizesSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	sharedOut := filepath.Join(root, "shared-out")
	aliasOut := filepath.Join(root, "shared-out-alias")
	if err := os.MkdirAll(sharedOut, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedOut, aliasOut); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	firstDir := filepath.Join(root, "first")
	secondDir := filepath.Join(root, "second")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
	writeMetadataSolutionProject(t, firstDir, sharedOut)
	writeMetadataSolutionProject(t, secondDir, aliasOut)

	firstConfig := filepath.Join(firstDir, "tsconfig.json")
	secondConfig := filepath.Join(secondDir, "tsconfig.json")
	metadata := solutionWriteMetadataForTest(t, filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if !reflect.DeepEqual(metadata.writeRoots[firstConfig], metadata.writeRoots[secondConfig]) {
		t.Fatalf("symlinked write roots differ: %v vs %v", metadata.writeRoots[firstConfig], metadata.writeRoots[secondConfig])
	}
}

func TestSolutionConflictDependencies(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "first")
	secondDir := filepath.Join(root, "second")
	rootConfig := filepath.Join(root, "tsconfig.json")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./first", "./second"}, true)
	writeMetadataSolutionProject(t, firstDir, filepath.Join(root, "outputs", "same"))
	writeMetadataSolutionProject(t, secondDir, filepath.Join(root, "outputs", "same"))
	coordinator, err := NewSolutionCoordinatorWithDrainer(rootConfig, ProjectOptions{}, &recordingSolutionDrainer{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	secondConfig := filepath.Join(secondDir, "tsconfig.json")
	firstConfig := filepath.Join(firstDir, "tsconfig.json")
	want := map[string][]string{secondConfig: {firstConfig}}
	if !reflect.DeepEqual(coordinator.waitOnlyDependencies, want) {
		t.Fatalf("wait-only dependencies = %v, want %v", coordinator.waitOnlyDependencies, want)
	}
	writeMetadataSolutionProject(t, secondDir, filepath.Join(root, "outputs", "different"))
	if err := coordinator.Reload(rootConfig, ProjectOptions{}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(coordinator.waitOnlyDependencies) != 0 {
		t.Fatalf("reloaded wait-only dependencies = %v, want none", coordinator.waitOnlyDependencies)
	}
}

func TestSolutionConflictDependenciesScheduler(t *testing.T) {
	tests := []struct {
		name     string
		tasks    []solutionTask
		conflict bool
	}{
		{name: "conflicting outputs", tasks: []solutionTask{{index: 0}, {index: 1, waitOnly: []int{0}}}, conflict: true},
		{name: "independent outputs", tasks: []solutionTask{{index: 0}, {index: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan int, len(test.tasks))
			release := make(chan struct{})
			runDone := make(chan []error, 1)
			go func() {
				runDone <- RunSolutionTasks(test.tasks, 2, func(index int) error { started <- index; <-release; return nil })
			}()
			if test.conflict {
				if got := <-started; got != 0 {
					t.Fatalf("started task %d, want 0", got)
				}
				select {
				case got := <-started:
					t.Fatalf("conflicting task %d started before predecessor finished", got)
				default:
				}
				release <- struct{}{}
				if got := <-started; got != 1 {
					t.Fatalf("started task %d, want 1", got)
				}
			} else {
				assertTaskIndexes(t, started, len(test.tasks))
			}
			close(release)
			assertNoErrors(t, <-runDone)
		})
	}
}

func writeMetadataSolutionProject(t *testing.T, dir, outDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":%q},"include":["src"]}`, outDir)
	for path, content := range map[string]string{
		"tsconfig.json":    config,
		"package.json":     `{"name":"solution-metadata-project"}`,
		"src/globals.d.ts": noLibGlobalStubs,
		"src/main.ts":      "export const value = 1;\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func solutionWriteMetadataForTest(t *testing.T, configPath string, entry ProjectOptions) solutionWriteMetadata {
	t.Helper()
	graph, err := BuildSolutionGraph(configPath, entry)
	if err != nil {
		t.Fatalf("BuildSolutionGraph: %v", err)
	}
	_, metadata := populateCrossProjectMetadata(graph)
	return metadata
}

func canonicalMetadataPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}
