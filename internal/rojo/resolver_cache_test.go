package rojo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestResolverState_roundTripsDeterministically(t *testing.T) {
	// Given
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"game","tree":{"$className":"DataModel","z":{"$path":"misc/z.json"},"a":{"$path":"misc/a.json"},"src":{"$path":"out"}}}`,
		"misc/a.json":          "{}",
		"misc/z.json":          "{}",
		"out/":                 "",
	})
	resolver := FromPath(filepath.Join(dir, "default.project.json"))

	// When
	state := resolver.GetState()
	first, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(resolver.GetState())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := FromState(state)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if string(first) != string(second) {
		t.Fatalf("state serialization is not deterministic:\nfirst:  %s\nsecond: %s", first, second)
	}
	if !reflect.DeepEqual(state, restored.GetState()) {
		t.Fatalf("restored state = %#v, want %#v", restored.GetState(), state)
	}
	if got := mustRbxPath(t, restored, filepath.Join(dir, "out", "main.luau")); !reflect.DeepEqual(got, RbxPath{"src", "main"}) {
		t.Errorf("restored output path = %v, want [src main]", got)
	}
	if got := mustRbxPath(t, restored, filepath.Join(dir, "misc", "a.json")); !reflect.DeepEqual(got, RbxPath{"a"}) {
		t.Errorf("restored exact path = %v, want [a]", got)
	}
	if len(state.FilePaths) != 2 || state.FilePaths[0].Path > state.FilePaths[1].Path {
		t.Errorf("file path state is not sorted: %#v", state.FilePaths)
	}
	if !state.IsGame {
		t.Error("restored state must preserve the game flag")
	}
}

func TestResolverStateRejectsMalformedMappings(t *testing.T) {
	tests := []struct {
		name  string
		state ResolverState
	}{
		{
			name: "empty partition path",
			state: ResolverState{Partitions: []PartitionInfo{{
				FsPath:  "",
				RbxPath: RbxPath{"ReplicatedStorage"},
			}}},
		},
		{
			name: "empty mapped file path",
			state: ResolverState{FilePaths: []ResolverFilePathState{{
				Path:    "",
				RbxPath: RbxPath{"ReplicatedStorage"},
			}}},
		},
		{
			name: "duplicate mapped file path",
			state: ResolverState{FilePaths: []ResolverFilePathState{
				{Path: "/project/src/module.luau", RbxPath: RbxPath{"ReplicatedStorage", "Module"}},
				{Path: "/project/src/module.luau", RbxPath: RbxPath{"ServerStorage", "Module"}},
			}},
		},
		{
			name: "Lua file mapping aliases Luau",
			state: ResolverState{FilePaths: []ResolverFilePathState{{
				Path:    "/project/src/module.lua",
				RbxPath: RbxPath{"ReplicatedStorage", "Module"},
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			resolver, err := FromState(tt.state)

			// Then
			if err == nil {
				t.Fatalf("FromState(%#v) succeeded with resolver %#v", tt.state, resolver)
			}
			if resolver != nil {
				t.Fatalf("FromState(%#v) resolver = %#v, want nil", tt.state, resolver)
			}
		})
	}
}

func TestRojoCache_loadsResolverStateFromDisk(t *testing.T) {
	// Given
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"p","tree":{"src":{"$path":"out"}}}`,
		"out/":                 "",
	})
	configPath := filepath.Join(dir, "default.project.json")
	cacheDir := filepath.Join(dir, ".cache")
	firstCache := NewRojoResolverCache(cacheDir, "compiler-v1")

	// When
	first := firstCache.Load(configPath)
	secondCache := NewRojoResolverCache(cacheDir, "compiler-v1")
	second := secondCache.Load(configPath)

	// Then
	if got := mustRbxPath(t, first, filepath.Join(dir, "out", "main.luau")); !reflect.DeepEqual(got, RbxPath{"src", "main"}) {
		t.Errorf("first cache path = %v, want [src main]", got)
	}
	if got := mustRbxPath(t, second, filepath.Join(dir, "out", "main.luau")); !reflect.DeepEqual(got, RbxPath{"src", "main"}) {
		t.Errorf("disk cache path = %v, want [src main]", got)
	}
	data, err := os.ReadFile(firstCache.cachePath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	var file resolverCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("cache file must be valid JSON: %v", err)
	}
	if file.Version != resolverCacheFormatVersion || file.CompilerVersion != "compiler-v1" || file.Key != cacheKey(configPath) {
		t.Errorf("cache header = %#v", file)
	}
	if len(file.MtimeManifest) == 0 {
		t.Error("cache file must persist the walked-path mtime manifest")
	}
}

func TestRojoCache_rebuildsWhenWalkedDirectoryChanges(t *testing.T) {
	// Given
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"p","tree":{"outer":{"$path":"src"}}}`,
		"src/pkg/lib/":         "",
	})
	configPath := filepath.Join(dir, "default.project.json")
	cache := NewRojoResolverCache(filepath.Join(dir, ".cache"), "compiler-v1")
	before := mustRbxPath(t, cache.Load(configPath), filepath.Join(dir, "src", "pkg", "lib", "item.luau"))

	if err := os.WriteFile(filepath.Join(dir, "src", "pkg", "default.project.json"), []byte(`{"name":"inner","tree":{"$path":"lib"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(filepath.Join(dir, "src", "pkg"), changedAt, changedAt); err != nil {
		t.Fatal(err)
	}

	// When
	after := mustRbxPath(t, cache.Load(configPath), filepath.Join(dir, "src", "pkg", "lib", "item.luau"))

	// Then
	if !reflect.DeepEqual(before, RbxPath{"outer", "pkg", "lib", "item"}) {
		t.Fatalf("initial path = %v, want [outer pkg lib item]", before)
	}
	if !reflect.DeepEqual(after, RbxPath{"outer", "inner", "item"}) {
		t.Errorf("rebuilt path = %v, want [outer inner item]", after)
	}
}

func TestRojoCache_rebuildsWhenCompilerVersionOrDiskDataChanges(t *testing.T) {
	// Given
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"p","tree":{"src":{"$path":"out"}}}`,
		"out/":                 "",
	})
	configPath := filepath.Join(dir, "default.project.json")
	cacheDir := filepath.Join(dir, ".cache")
	firstCache := NewRojoResolverCache(cacheDir, "compiler-v1")
	_ = firstCache.Load(configPath)

	// When
	versionCache := NewRojoResolverCache(cacheDir, "compiler-v2")
	versionResolver := versionCache.Load(configPath)
	if err := os.WriteFile(versionCache.cachePath(configPath), []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptResolver := NewRojoResolverCache(cacheDir, "compiler-v2").Load(configPath)

	// Then
	if got := mustRbxPath(t, versionResolver, filepath.Join(dir, "out", "main.luau")); !reflect.DeepEqual(got, RbxPath{"src", "main"}) {
		t.Errorf("version-invalidated path = %v, want [src main]", got)
	}
	if got := mustRbxPath(t, corruptResolver, filepath.Join(dir, "out", "main.luau")); !reflect.DeepEqual(got, RbxPath{"src", "main"}) {
		t.Errorf("corruption fallback path = %v, want [src main]", got)
	}
	data, err := os.ReadFile(versionCache.cachePath(configPath))
	if err != nil {
		t.Fatal(err)
	}
	var file resolverCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("corruption fallback did not atomically replace cache file: %v", err)
	}
	if file.CompilerVersion != "compiler-v2" {
		t.Errorf("compiler version = %q, want compiler-v2", file.CompilerVersion)
	}
}
