package compile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWatchSetsCanonicalizeConfigPaths(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	writeBuildableSolutionProject(t, projectDir)
	projectAlias := filepath.Join(root, "project-alias")
	if err := os.Symlink(projectDir, projectAlias); err != nil {
		t.Skipf("cannot create project symlink: %v", err)
	}

	configPath := filepath.Join(projectDir, "tsconfig.json")
	config := string(mustReadFile(t, configPath))
	if err := os.WriteFile(configPath, []byte(`{"extends":"./tsconfig.base.json",`+config[1:]), 0o644); err != nil {
		t.Fatal(err)
	}
	baseConfig := filepath.Join(projectDir, "tsconfig.base.json")
	if err := os.WriteFile(baseConfig, []byte(`{"rbxts":{"rojo":"./game.project.json"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rojoConfig := filepath.Join(projectDir, "game.project.json")
	if err := os.WriteFile(rojoConfig, []byte(`{"name":"watch-configs","tree":{"Nested":{"$path":"nested.project.json"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	nestedRojoConfig := filepath.Join(projectDir, "nested.project.json")
	if err := os.WriteFile(nestedRojoConfig, []byte(`{"name":"nested","tree":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	coordinator, err := NewSolutionCoordinator(filepath.Join(projectAlias, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	watchSets := coordinator.WatchSets()
	if len(watchSets) != 1 {
		t.Fatalf("WatchSets() returned %d sets, want 1", len(watchSets))
	}
	canonicalProjectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := watchSets[0].TsConfigPaths, []string{
		filepath.Join(canonicalProjectDir, "tsconfig.json"),
		filepath.Join(canonicalProjectDir, "tsconfig.base.json"),
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("TsConfigPaths = %v, want %v", got, want)
	}
	if got, want := watchSets[0].RojoConfigs, []string{
		filepath.Join(canonicalProjectDir, "game.project.json"),
		filepath.Join(canonicalProjectDir, "nested.project.json"),
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RojoConfigs = %v, want %v", got, want)
	}
}
