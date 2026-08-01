package compile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRojoWatchInvalidation(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./app"}, true)
	writeSolutionConfig(t, filepath.Join(root, "app"), "tsconfig.json", []string{"../shared"}, false)
	writeSolutionConfig(t, filepath.Join(root, "shared"), "tsconfig.json", nil, false)
	drainer := &recordingSolutionDrainer{}

	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{}, drainer)
	if err != nil {
		t.Fatalf("NewSolutionCoordinatorWithDrainer: %v", err)
	}
	if _, _, err := coordinator.Drain(); err != nil {
		t.Fatalf("initial Drain: %v", err)
	}
	drainer.drained = nil

	sharedConfig := filepath.Join(root, "shared", "tsconfig.json")
	if got := coordinator.Invalidate(sharedConfig); !reflect.DeepEqual(got, []string{sharedConfig, filepath.Join(root, "app", "tsconfig.json")}) {
		t.Fatalf("Invalidate() = %v", got)
	}
	if _, _, err := coordinator.Drain(); err != nil {
		t.Fatalf("Drain after Rojo invalidation: %v", err)
	}
	if want := []string{"shared", "app"}; !reflect.DeepEqual(drainer.drained, want) {
		t.Fatalf("drained projects = %v, want %v", drainer.drained, want)
	}
}

func TestTsconfigChainReload(t *testing.T) {
	root := t.TempDir()
	childDir := filepath.Join(root, "child")
	rootConfig := filepath.Join(root, "tsconfig.json")
	childConfig := filepath.Join(childDir, "tsconfig.json")
	baseConfig := filepath.Join(root, "base.json")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./child"}, true)
	writeSolutionConfig(t, childDir, "tsconfig.json", nil, false)
	if err := os.WriteFile(baseConfig, []byte(`{"rbxts":{"rojo":"./first.project.json"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childConfig, []byte(`{"extends":"../base.json"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinator, err := NewSolutionCoordinator(rootConfig, ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	watchSets := coordinator.WatchSets()
	if len(watchSets) != 1 || !reflect.DeepEqual(watchSets[0].TsConfigPaths, []string{childConfig, baseConfig}) {
		t.Fatalf("TsConfigPaths = %#v, want [%s %s]", watchSets, childConfig, baseConfig)
	}

	if err := os.WriteFile(baseConfig, []byte(`{"rbxts":{"rojo":"./second.project.json"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Reload(rootConfig, ProjectOptions{}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	state, ok := coordinator.ProjectState(childConfig)
	if !ok {
		t.Fatal("child project state missing after Reload")
	}
	if want := filepath.Join(root, "second.project.json"); state.Project.Options.RojoConfigPath != want {
		t.Fatalf("RojoConfigPath = %q, want %q", state.Project.Options.RojoConfigPath, want)
	}
}

func TestWatchAssetDrain(t *testing.T) {
	root, libDir, _ := writeCrossProjectSolution(t)
	libConfig := filepath.Join(libDir, "tsconfig.json")
	coordinator, err := NewSolutionCoordinator(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	if _, messages, err := coordinator.Drain(); err != nil {
		t.Fatalf("initial Drain: %v (%v)", err, messages)
	}

	assetPath := filepath.Join(libDir, "src", "watch-asset.luau")
	assetOutput := filepath.Join(libDir, "out", "watch-asset.luau")
	if err := os.WriteFile(assetPath, []byte("return { watch = true }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.DrainAssets(libConfig, []WatchAssetEvent{{Path: assetPath}}); err != nil {
		t.Fatalf("DrainAssets copy: %v", err)
	}
	if got, err := os.ReadFile(assetOutput); err != nil || string(got) != "return { watch = true }\n" {
		t.Fatalf("copied asset = %q, err = %v", got, err)
	}

	if err := os.Remove(assetPath); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.DrainAssets(libConfig, []WatchAssetEvent{{Path: assetPath, Deleted: true}}); err != nil {
		t.Fatalf("DrainAssets delete: %v", err)
	}
	if _, err := os.Stat(assetOutput); !os.IsNotExist(err) {
		t.Fatalf("asset output stat error = %v, want not exists", err)
	}
}
