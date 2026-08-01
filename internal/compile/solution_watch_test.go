package compile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRojoWatchInvalidation(t *testing.T) {
	root := t.TempDir()
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./app"}, true)
	writeSolutionConfig(t, filepath.Join(root, "app"), "tsconfig.json", []string{"../shared"}, false)
	writeSolutionConfig(t, filepath.Join(root, "shared"), "tsconfig.json", nil, false)
	drainer := &recordingSolutionDrainer{}
	builders := 1

	coordinator, err := NewSolutionCoordinatorWithDrainer(filepath.Join(root, "tsconfig.json"), ProjectOptions{Builders: &builders}, drainer)
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

func TestWatchExcludesOutDir(t *testing.T) {
	projectDir := t.TempDir()
	writeBuildableSolutionProject(t, projectDir)
	configPath := filepath.Join(projectDir, "tsconfig.json")
	config := strings.Replace(
		string(mustReadFile(t, configPath)),
		`,"include"`,
		`,"rbxts":{"rojo":"./game.project.json"},"include"`,
		1,
	)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "game.project.json"), []byte(`{
		"name": "watch-artifacts",
		"tree": {
			"assets": {"$path": "assets"},
			"out": {"$path": "out"},
			"include": {"$path": "include"},
			"cache": {"$path": ".rotor"}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	assetDir := filepath.Join(projectDir, "assets")
	for _, directory := range []string{
		assetDir,
		filepath.Join(projectDir, "out", "nested"),
		filepath.Join(projectDir, "include", "nested"),
		filepath.Join(projectDir, ".rotor", "cache"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	coordinator, err := NewSolutionCoordinator(configPath, ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	watchSets := coordinator.WatchSets()
	if len(watchSets) != 1 {
		t.Fatalf("WatchSets() returned %d sets, want 1", len(watchSets))
	}
	set := watchSets[0]
	if !pathWithinAnyRoot(assetDir, set.RojoDirectories) {
		t.Fatalf("RojoDirectories = %v, want asset directory %q", set.RojoDirectories, assetDir)
	}
	for _, artifactDir := range []string{
		filepath.Join(projectDir, "out"),
		filepath.Join(projectDir, "include"),
		filepath.Join(projectDir, ".rotor"),
	} {
		if pathWithinAnyRoot(artifactDir, set.RojoDirectories) {
			t.Fatalf("RojoDirectories = %v, should exclude %q and descendants", set.RojoDirectories, artifactDir)
		}
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
	wantTsConfigPaths := canonicalWatchPaths([]string{childConfig, baseConfig})
	if len(watchSets) != 1 || !reflect.DeepEqual(watchSets[0].TsConfigPaths, wantTsConfigPaths) {
		t.Fatalf("TsConfigPaths = %#v, want %#v", watchSets, wantTsConfigPaths)
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

func TestTsconfigChainReloadCleansStaleLuaExtension(t *testing.T) {
	root := t.TempDir()
	childDir := filepath.Join(root, "child")
	rootConfig := filepath.Join(root, "tsconfig.json")
	childConfig := filepath.Join(childDir, "tsconfig.json")
	baseConfig := filepath.Join(root, "base.json")
	writeSolutionConfig(t, root, "tsconfig.json", []string{"./child"}, true)
	writeBuildableSolutionProject(t, childDir)
	childContents := string(mustReadFile(t, childConfig))
	if err := os.WriteFile(childConfig, []byte(`{"extends":"../base.json",`+childContents[1:]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseConfig, []byte(`{"rbxts":{"luau":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinator, err := NewSolutionCoordinator(rootConfig, ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	if _, messages, err := coordinator.Drain(); err != nil {
		t.Fatalf("initial Drain: %v (%v)", err, messages)
	}
	oldOutput := filepath.Join(childDir, "out", "main.luau")
	if _, err := os.Stat(oldOutput); err != nil {
		t.Fatalf("initial output: %v", err)
	}

	if err := os.WriteFile(baseConfig, []byte(`{"rbxts":{"luau":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanStaleOutputs, err := coordinator.ReloadForWatch(rootConfig, ProjectOptions{})
	if err != nil {
		t.Fatalf("ReloadForWatch: %v", err)
	}
	coordinator.Invalidate(childConfig)
	if _, messages, err := coordinator.Drain(); err != nil {
		t.Fatalf("reloaded Drain: %v (%v)", err, messages)
	}
	if err := cleanStaleOutputs(); err != nil {
		t.Fatalf("clean stale outputs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(childDir, "out", "main.lua")); err != nil {
		t.Fatalf("new extension output: %v", err)
	}
	if _, err := os.Stat(oldOutput); !os.IsNotExist(err) {
		t.Fatalf("old extension output stat error = %v, want not exists", err)
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

func TestWatchAssetDrainNormalizesCanonicalEventPath(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	writeBuildableSolutionProject(t, projectDir)
	projectAlias := filepath.Join(root, "project-alias")
	if err := os.Symlink(projectDir, projectAlias); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(projectAlias, "tsconfig.json")
	coordinator, err := NewSolutionCoordinator(configPath, ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	if _, messages, err := coordinator.Drain(); err != nil {
		t.Fatalf("initial Drain: %v (%v)", err, messages)
	}

	assetPath := filepath.Join(projectDir, "src", "watch-asset.luau")
	assetOutput := filepath.Join(projectDir, "out", "watch-asset.luau")
	if err := os.WriteFile(assetPath, []byte("return { watch = true }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.DrainAssets(configPath, []WatchAssetEvent{{Path: assetPath}}); err != nil {
		t.Fatalf("DrainAssets copy: %v", err)
	}
	if got, err := os.ReadFile(assetOutput); err != nil || string(got) != "return { watch = true }\n" {
		t.Fatalf("copied asset = %q, err = %v", got, err)
	}

	if err := os.Remove(assetPath); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.DrainAssets(configPath, []WatchAssetEvent{{Path: assetPath, Deleted: true}}); err != nil {
		t.Fatalf("DrainAssets delete: %v", err)
	}
	if _, err := os.Stat(assetOutput); !os.IsNotExist(err) {
		t.Fatalf("asset output stat error = %v, want not exists", err)
	}
}
