package rojo

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// writeProject creates files (path -> contents) under a temp dir and returns
// the dir. Keys use forward slashes; directories are created as needed. A
// trailing "/" key creates an empty directory.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, contents := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(abs, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mustRbxPath(t *testing.T, r *RojoResolver, filePath string) RbxPath {
	t.Helper()
	rbxPath, ok := r.GetRbxPathFromFilePath(filePath)
	if !ok {
		t.Fatalf("GetRbxPathFromFilePath(%q): no Rojo data", filePath)
	}
	return rbxPath
}

// --- fixture project (Model type, the diff harness project) ---

func fixtureProjectDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "diff", "project"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("fixture project not present: %v", err)
	}
	return dir
}

func TestFromPathFixtureProject(t *testing.T) {
	dir := fixtureProjectDir(t)
	r := FromPath(filepath.Join(dir, "default.project.json"))

	if len(r.GetWarnings()) != 0 {
		t.Fatalf("unexpected warnings: %v", r.GetWarnings())
	}
	if r.IsGame {
		t.Error("fixture project should not be a game")
	}

	// fromPath passes doNotPush=true, so the project name ("fixture") is NOT
	// part of any rbxPath (RojoResolver.ts L197-201, L243-244).
	tests := []struct {
		rel  string
		want RbxPath
	}{
		{"out/01_literals.luau", RbxPath{"01_literals"}},
		// .lua converts to .luau before lookup (convertToLuau, L154-158).
		{"include/RuntimeLib.lua", RbxPath{"include", "RuntimeLib"}},
		{"include/RuntimeLib.luau", RbxPath{"include", "RuntimeLib"}},
		{"node_modules/@rbxts/types/x.luau", RbxPath{"node_modules", "@rbxts", "types", "x"}},
	}
	for _, tt := range tests {
		got := mustRbxPath(t, r, filepath.Join(dir, filepath.FromSlash(tt.rel)))
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("GetRbxPathFromFilePath(%s) = %v, want %v", tt.rel, got, tt.want)
		}
	}

	// Partitions are LIFO: later tree entries were unshifted to the front
	// (RojoResolver.ts L272-275). The leading "src" partition is
	// @rbxts/react's own nested default.project.json (`"tree": {"$path":
	// "src"}`), parsed while walking the node_modules/@rbxts partition —
	// upstream parses nested project files the same way.
	parts := r.GetPartitions()
	var suffixes []string
	for _, p := range parts {
		suffixes = append(suffixes, filepath.Base(p.FsPath))
	}
	want := []string{"src", "@rbxts", "include", "out"}
	if !reflect.DeepEqual(suffixes, want) {
		t.Errorf("partition order = %v, want %v", suffixes, want)
	}

	// Files outside every partition have no Rojo data.
	if _, ok := r.GetRbxPathFromFilePath(filepath.Join(dir, "src", "01_literals.ts")); ok {
		t.Error("src file should have no Rojo data (only out is mapped)")
	}

	// Runtime lib relative chain (digest §5.3): out/X.luau ->
	// include/RuntimeLib: [Parent, include, RuntimeLib].
	fileRbx := mustRbxPath(t, r, filepath.Join(dir, "out", "01_literals.luau"))
	libRbx := mustRbxPath(t, r, filepath.Join(dir, "include", "RuntimeLib.lua"))
	rel := Relative(fileRbx, libRbx)
	wantRel := RelativeRbxPath{RbxPathParent, {Name: "include"}, {Name: "RuntimeLib"}}
	if !reflect.DeepEqual(rel, wantRel) {
		t.Errorf("Relative = %v, want %v", rel, wantRel)
	}
}

// --- game-shaped project ---

const gameProjectJSON = `{
	"name": "game",
	"tree": {
		"$className": "DataModel",
		"ServerScriptService": {
			"$className": "ServerScriptService",
			"TS": { "$path": "out/server" }
		},
		"ReplicatedStorage": {
			"$className": "ReplicatedStorage",
			"rbxts_include": {
				"$path": "include",
				"node_modules": {
					"$className": "Folder",
					"@rbxts": { "$path": "node_modules/@rbxts" }
				}
			},
			"TS": { "$path": "out/shared" }
		},
		"StarterPlayer": {
			"$className": "StarterPlayer",
			"StarterPlayerScripts": {
				"$className": "StarterPlayerScripts",
				"TS": { "$path": "out/client" }
			}
		}
	}
}`

func gameResolver(t *testing.T) (*RojoResolver, string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"default.project.json": gameProjectJSON,
		"out/server/":          "",
		"out/shared/":          "",
		"out/client/":          "",
		"include/":             "",
		"node_modules/@rbxts/": "",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))
	if len(r.GetWarnings()) != 0 {
		t.Fatalf("unexpected warnings: %v", r.GetWarnings())
	}
	return r, dir
}

func TestGameProject(t *testing.T) {
	r, dir := gameResolver(t)
	if !r.IsGame {
		t.Fatal("$className DataModel must set IsGame")
	}

	serverMain := mustRbxPath(t, r, filepath.Join(dir, "out", "server", "main.server.luau"))
	if want := (RbxPath{"ServerScriptService", "TS", "main"}); !reflect.DeepEqual(serverMain, want) {
		t.Errorf("server main = %v, want %v", serverMain, want)
	}

	sharedMod := mustRbxPath(t, r, filepath.Join(dir, "out", "shared", "mod.luau"))
	if want := (RbxPath{"ReplicatedStorage", "TS", "mod"}); !reflect.DeepEqual(sharedMod, want) {
		t.Errorf("shared mod = %v, want %v", sharedMod, want)
	}

	// init.luau collapses into its directory (script exts only).
	clientFoo := mustRbxPath(t, r, filepath.Join(dir, "out", "client", "foo", "init.luau"))
	if want := (RbxPath{"StarterPlayer", "StarterPlayerScripts", "TS", "foo"}); !reflect.DeepEqual(clientFoo, want) {
		t.Errorf("client foo init = %v, want %v", clientFoo, want)
	}

	runtimeLib := mustRbxPath(t, r, filepath.Join(dir, "include", "RuntimeLib.lua"))
	if want := (RbxPath{"ReplicatedStorage", "rbxts_include", "RuntimeLib"}); !reflect.DeepEqual(runtimeLib, want) {
		t.Errorf("runtime lib = %v, want %v", runtimeLib, want)
	}

	// Network types (SERVER_CONTAINERS checked before CLIENT_CONTAINERS).
	if got := r.GetNetworkType(serverMain); got != NetworkTypeServer {
		t.Errorf("server network type = %v, want Server", got)
	}
	if got := r.GetNetworkType(clientFoo); got != NetworkTypeClient {
		t.Errorf("client network type = %v, want Client", got)
	}
	if got := r.GetNetworkType(sharedMod); got != NetworkTypeUnknown {
		t.Errorf("shared network type = %v, want Unknown", got)
	}
	if got := r.GetNetworkType(runtimeLib); got != NetworkTypeUnknown {
		t.Errorf("runtime lib network type = %v, want Unknown", got)
	}

	// Isolation: StarterPlayer/StarterPlayerScripts is a default isolated
	// container; ServerScriptService and ReplicatedStorage are not.
	if !r.IsIsolated(clientFoo) {
		t.Error("StarterPlayerScripts path must be isolated")
	}
	if r.IsIsolated(serverMain) || r.IsIsolated(sharedMod) || r.IsIsolated(runtimeLib) {
		t.Error("non-Starter containers must not be isolated")
	}

	// File relations.
	relTests := []struct {
		name         string
		file, module RbxPath
		want         FileRelation
	}{
		{"server->shared", serverMain, sharedMod, FileRelationOutToOut},
		{"client->shared", clientFoo, sharedMod, FileRelationInToOut},
		{"shared->client", sharedMod, clientFoo, FileRelationOutToIn},
		{"client->client", clientFoo, RbxPath{"StarterPlayer", "StarterPlayerScripts", "TS", "other"}, FileRelationInToIn},
		// Different isolated containers: OutToIn.
		{"gui->client", RbxPath{"StarterGui", "x"}, clientFoo, FileRelationOutToIn},
	}
	for _, tt := range relTests {
		if got := r.GetFileRelation(tt.file, tt.module); got != tt.want {
			t.Errorf("GetFileRelation(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsolationOnlyAppliesToGames(t *testing.T) {
	// getContainer requires isGame (RojoResolver.ts L351-361); a Model project
	// treats Starter-shaped paths as ordinary.
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"model","tree":{"$path":"out"}}`,
		"out/":                 "",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))
	p := RbxPath{"StarterPlayer", "StarterPlayerScripts", "x"}
	if r.IsIsolated(p) {
		t.Error("non-game resolver must not isolate")
	}
	if got := r.GetFileRelation(p, RbxPath{"y"}); got != FileRelationOutToOut {
		t.Errorf("non-game relation = %v, want OutToOut", got)
	}
	// Network containers do NOT require isGame (getNetworkType uses
	// getContainer too... which checks isGame) — so Unknown here.
	if got := r.GetNetworkType(RbxPath{"ServerScriptService", "x"}); got != NetworkTypeUnknown {
		t.Errorf("non-game network type = %v, want Unknown", got)
	}
}

// --- synthetic ---

func TestSynthetic(t *testing.T) {
	dir := t.TempDir()
	r := Synthetic(dir)
	if r.IsGame {
		t.Error("synthetic resolver is never a game")
	}
	tests := []struct {
		rel  string
		want RbxPath
	}{
		{"foo.luau", RbxPath{"foo"}},
		{"a/b.luau", RbxPath{"a", "b"}},
		{"foo/init.luau", RbxPath{"foo"}},
		{"init.luau", RbxPath{}},
		{"data.json", RbxPath{"data"}},
		// Sub-extensions strip before the init check.
		{"foo/init.client.luau", RbxPath{"foo"}},
		// The trailing-init pop only applies to script exts: init.json keeps
		// its name (RojoResolver.ts L331 checks ROJO_SCRIPT_EXTS).
		{"foo/init.json", RbxPath{"foo", "init"}},
	}
	for _, tt := range tests {
		got := mustRbxPath(t, r, filepath.Join(dir, filepath.FromSlash(tt.rel)))
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("synthetic GetRbxPathFromFilePath(%s) = %v, want %v", tt.rel, got, tt.want)
		}
	}
	if _, ok := r.GetRbxPathFromFilePath(filepath.Join(filepath.Dir(dir), "outside.luau")); ok {
		t.Error("file outside basePath must have no Rojo data")
	}
}

// --- partition LIFO + exact file mappings ---

func TestPartitionsLIFO(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"p","tree":{"all":{"$path":"src"},"sub":{"$path":"src/special"}}}`,
		"src/":                 "",
		"src/special/":         "",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))
	// src/special is covered by BOTH partitions; the later tree entry was
	// unshifted to the front and wins.
	got := mustRbxPath(t, r, filepath.Join(dir, "src", "special", "x.luau"))
	if want := (RbxPath{"sub", "x"}); !reflect.DeepEqual(got, want) {
		t.Errorf("overlapping partition = %v, want %v", got, want)
	}
	got = mustRbxPath(t, r, filepath.Join(dir, "src", "y.luau"))
	if want := (RbxPath{"all", "y"}); !reflect.DeepEqual(got, want) {
		t.Errorf("outer partition = %v, want %v", got, want)
	}
}

func TestExactFileMapping(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"default.project.json": `{"name":"p","tree":{"data":{"$path":"misc/data.json"},"legacy":{"$path":"misc/legacy.lua"}}}`,
		"misc/data.json":       "{}",
		"misc/legacy.lua":      "",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))
	got := mustRbxPath(t, r, filepath.Join(dir, "misc", "data.json"))
	if want := (RbxPath{"data"}); !reflect.DeepEqual(got, want) {
		t.Errorf("json mapping = %v, want %v", got, want)
	}
	// $path "legacy.lua" was stored under the .luau key; both spellings hit.
	for _, name := range []string{"legacy.lua", "legacy.luau"} {
		got = mustRbxPath(t, r, filepath.Join(dir, "misc", name))
		if want := (RbxPath{"legacy"}); !reflect.DeepEqual(got, want) {
			t.Errorf("lua mapping (%s) = %v, want %v", name, got, want)
		}
	}
	// Sibling files in the same dir are NOT covered (no partition was added).
	if _, ok := r.GetRbxPathFromFilePath(filepath.Join(dir, "misc", "other.luau")); ok {
		t.Error("sibling of exact file mapping must have no Rojo data")
	}
}

// --- nested project files ---

func TestNestedDefaultProjectViaPath(t *testing.T) {
	// $path pointing at a dir that contains default.project.json parses that
	// config WITHOUT pushing its name (parsePath L268-270 -> parseConfig
	// doNotPush=true).
	dir := writeProject(t, map[string]string{
		"default.project.json":     `{"name":"p","tree":{"child":{"$path":"pkg"}}}`,
		"pkg/default.project.json": `{"name":"pkgname","tree":{"$path":"src"}}`,
		"pkg/src/":                 "",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))
	got := mustRbxPath(t, r, filepath.Join(dir, "pkg", "src", "x.luau"))
	if want := (RbxPath{"child", "x"}); !reflect.DeepEqual(got, want) {
		t.Errorf("nested default project = %v, want %v", got, want)
	}
}

func TestNestedProjectFileViaPath(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"default.project.json":          `{"name":"root","tree":{"Pkg":{"$path":"projects/shared.project.json"}}}`,
		"projects/shared.project.json":  `{"name":"IgnoredName","tree":{"$path":"src","Nested":{"$path":"nested.project.json"}}}`,
		"projects/nested.project.json":  `{"name":"AlsoIgnored","tree":{"$path":"nested-src"}}`,
		"projects/src/root.luau":        "return {}",
		"projects/nested-src/leaf.luau": "return {}",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))

	if got := mustRbxPath(t, r, filepath.Join(dir, "projects", "src", "root.luau")); !reflect.DeepEqual(got, RbxPath{"Pkg", "root"}) {
		t.Errorf("direct nested project path = %v, want [Pkg root]", got)
	}
	if got := mustRbxPath(t, r, filepath.Join(dir, "projects", "nested-src", "leaf.luau")); !reflect.DeepEqual(got, RbxPath{"Pkg", "Nested", "leaf"}) {
		t.Errorf("recursive nested project path = %v, want [Pkg Nested leaf]", got)
	}

	state := r.GetState()
	sharedConfig := filepath.Clean(realpathOr(filepath.Join(dir, "projects", "shared.project.json")))
	nestedConfig := filepath.Clean(realpathOr(filepath.Join(dir, "projects", "nested.project.json")))
	if !slices.Contains(state.WalkedConfigs, sharedConfig) || !slices.Contains(state.WalkedConfigs, nestedConfig) {
		t.Errorf("walked configs = %v, want %q and %q", state.WalkedConfigs, sharedConfig, nestedConfig)
	}
	for _, filePath := range state.FilePaths {
		if filePath.Path == sharedConfig || filePath.Path == nestedConfig {
			t.Errorf("nested project was registered as an exact module mapping: %#v", filePath)
		}
	}
	if _, ok := r.GetRbxPathFromFilePath(sharedConfig); ok {
		t.Error("nested project config itself must not resolve as a Roblox module")
	}
}

func TestNestedProjectFileWarnings(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := writeProject(t, map[string]string{
			"default.project.json": `{"name":"root","tree":{"Pkg":{"$path":"missing.project.json"}}}`,
		})
		r := FromPath(filepath.Join(dir, "default.project.json"))
		if warnings := r.GetWarnings(); len(warnings) != 1 || !strings.Contains(warnings[0], "Path does not exist") {
			t.Fatalf("warnings = %v, want one missing-path warning", warnings)
		}
		if _, ok := r.GetRbxPathFromFilePath(filepath.Join(dir, "missing.project.json")); ok {
			t.Error("missing nested project must not become an exact module mapping")
		}
	})

	t.Run("malformed", func(t *testing.T) {
		dir := writeProject(t, map[string]string{
			"default.project.json": `{"name":"root","tree":{"Pkg":{"$path":"broken.project.json"}}}`,
			"broken.project.json":  "{not json",
		})
		r := FromPath(filepath.Join(dir, "default.project.json"))
		if warnings := r.GetWarnings(); len(warnings) != 1 || !strings.Contains(warnings[0], "Invalid configuration") {
			t.Fatalf("warnings = %v, want one invalid-configuration warning", warnings)
		}
		if _, ok := r.GetRbxPathFromFilePath(filepath.Join(dir, "broken.project.json")); ok {
			t.Error("malformed nested project must not become an exact module mapping")
		}
	})
}

func TestNestedProjectFileSymlink(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"default.project.json":     `{"name":"root","tree":{"Pkg":{"$path":"links/shared.project.json"}}}`,
		"real/shared.project.json": `{"name":"ignored","tree":{"$path":"../content"}}`,
		"content/value.luau":       "return {}",
		"links/":                   "",
	})
	linkPath := filepath.Join(dir, "links", "shared.project.json")
	if err := os.Symlink(filepath.Join(dir, "real", "shared.project.json"), linkPath); err != nil {
		t.Skipf("cannot create project-file symlink: %v", err)
	}

	r := FromPath(filepath.Join(dir, "default.project.json"))
	if got := mustRbxPath(t, r, filepath.Join(dir, "content", "value.luau")); !reflect.DeepEqual(got, RbxPath{"Pkg", "value"}) {
		t.Errorf("symlinked nested project path = %v, want [Pkg value]", got)
	}
	if want := filepath.Clean(realpathOr(filepath.Join(dir, "real", "shared.project.json"))); !slices.Contains(r.GetState().WalkedConfigs, want) {
		t.Errorf("walked configs = %v, want real target %q", r.GetState().WalkedConfigs, want)
	}
}

func TestSearchDirectoryFindsNestedProjects(t *testing.T) {
	// A default.project.json discovered while WALKING a partition directory
	// goes through parseConfig with doNotPush=false: its name IS pushed
	// (searchDirectory L288-291), and the dir name it lives in is not.
	dir := writeProject(t, map[string]string{
		"default.project.json":            `{"name":"p","tree":{"parent":{"$path":"area"}}}`,
		"area/y.luau":                     "",
		"area/inner/default.project.json": `{"name":"innerproj","tree":{"$path":"lib"}}`,
		"area/inner/lib/":                 "",
		"area/other.project.json":         `{"name":"other","tree":{"$path":"otherlib"}}`,
		"area/otherlib/":                  "",
	})
	r := FromPath(filepath.Join(dir, "default.project.json"))

	got := mustRbxPath(t, r, filepath.Join(dir, "area", "y.luau"))
	if want := (RbxPath{"parent", "y"}); !reflect.DeepEqual(got, want) {
		t.Errorf("partition file = %v, want %v", got, want)
	}
	// LIFO also guarantees the inner partition beats the outer one.
	got = mustRbxPath(t, r, filepath.Join(dir, "area", "inner", "lib", "x.luau"))
	if want := (RbxPath{"parent", "innerproj", "x"}); !reflect.DeepEqual(got, want) {
		t.Errorf("nested-by-walk project = %v, want %v", got, want)
	}
	// Non-default *.project.json files are parsed too (name pushed).
	got = mustRbxPath(t, r, filepath.Join(dir, "area", "otherlib", "z.luau"))
	if want := (RbxPath{"parent", "other", "z"}); !reflect.DeepEqual(got, want) {
		t.Errorf("named project file = %v, want %v", got, want)
	}
}

// --- GetRbxTypeFromFilePath ---

func TestGetRbxTypeFromFilePath(t *testing.T) {
	r := Synthetic(t.TempDir())
	tests := []struct {
		in   string
		want RbxType
	}{
		{"foo.luau", RbxTypeModuleScript},
		{"foo.server.luau", RbxTypeScript},
		{"foo.client.luau", RbxTypeLocalScript},
		// .lua converts before classification.
		{"foo.server.lua", RbxTypeScript},
		// Unrecognized sub-extension on a script ext -> Unknown.
		{"foo.weird.luau", RbxTypeUnknown},
		// Non-script exts cannot use sub-extensions: always ModuleScript.
		{"foo.json", RbxTypeModuleScript},
		{"foo.server.json", RbxTypeModuleScript},
		{"foo.toml", RbxTypeModuleScript},
		{"foo.txt", RbxTypeModuleScript},
	}
	for _, tt := range tests {
		if got := r.GetRbxTypeFromFilePath(tt.in); got != tt.want {
			t.Errorf("GetRbxTypeFromFilePath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// --- Relative ---

func TestRelative(t *testing.T) {
	seg := func(name string) RelativeRbxPathSegment { return RelativeRbxPathSegment{Name: name} }
	tests := []struct {
		name     string
		from, to RbxPath
		want     RelativeRbxPath
	}{
		{"identical", RbxPath{"a", "b"}, RbxPath{"a", "b"}, RelativeRbxPath{}},
		{"sibling", RbxPath{"a", "b"}, RbxPath{"a", "c"}, RelativeRbxPath{RbxPathParent, seg("c")}},
		{"descend", RbxPath{"a"}, RbxPath{"a", "b", "c"}, RelativeRbxPath{seg("b"), seg("c")}},
		{"ascend", RbxPath{"a", "b", "c"}, RbxPath{"a"}, RelativeRbxPath{RbxPathParent, RbxPathParent}},
		{"disjoint", RbxPath{"x"}, RbxPath{"y"}, RelativeRbxPath{RbxPathParent, seg("y")}},
		{"empty from", RbxPath{}, RbxPath{"a"}, RelativeRbxPath{seg("a")}},
	}
	for _, tt := range tests {
		got := Relative(tt.from, tt.to)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Relative(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// --- FindRojoConfigFilePath ---

func TestFindRojoConfigFilePath(t *testing.T) {
	t.Run("default wins", func(t *testing.T) {
		dir := writeProject(t, map[string]string{
			"default.project.json": "{}",
			"other.project.json":   "{}",
		})
		got, warnings := FindRojoConfigFilePath(dir)
		if got != filepath.Join(dir, "default.project.json") {
			t.Errorf("path = %q", got)
		}
		if len(warnings) != 0 {
			t.Errorf("warnings = %v", warnings)
		}
	})
	t.Run("single candidate", func(t *testing.T) {
		dir := writeProject(t, map[string]string{"game.project.json": "{}"})
		got, warnings := FindRojoConfigFilePath(dir)
		if got != filepath.Join(dir, "game.project.json") {
			t.Errorf("path = %q", got)
		}
		if len(warnings) != 0 {
			t.Errorf("warnings = %v", warnings)
		}
	})
	t.Run("legacy name", func(t *testing.T) {
		dir := writeProject(t, map[string]string{"roblox-project.json": "{}"})
		got, _ := FindRojoConfigFilePath(dir)
		if got != filepath.Join(dir, "roblox-project.json") {
			t.Errorf("path = %q", got)
		}
	})
	t.Run("multiple candidates warn", func(t *testing.T) {
		dir := writeProject(t, map[string]string{
			"a.project.json": "{}",
			"b.project.json": "{}",
		})
		got, warnings := FindRojoConfigFilePath(dir)
		if got != filepath.Join(dir, "a.project.json") {
			t.Errorf("path = %q", got)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "Multiple *.project.json files found") {
			t.Errorf("warnings = %v", warnings)
		}
	})
	t.Run("none", func(t *testing.T) {
		got, warnings := FindRojoConfigFilePath(t.TempDir())
		if got != "" || len(warnings) != 0 {
			t.Errorf("path = %q, warnings = %v", got, warnings)
		}
	})
}

// --- config warnings ---

func TestInvalidConfigWarnings(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		r := FromPath(filepath.Join(t.TempDir(), "default.project.json"))
		w := r.GetWarnings()
		if len(w) != 1 || !strings.Contains(w[0], "Path does not exist") {
			t.Errorf("warnings = %v", w)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		dir := writeProject(t, map[string]string{"default.project.json": "{not json"})
		r := FromPath(filepath.Join(dir, "default.project.json"))
		w := r.GetWarnings()
		if len(w) != 1 || !strings.Contains(w[0], "Invalid configuration") {
			t.Errorf("warnings = %v", w)
		}
	})
	t.Run("schema violations", func(t *testing.T) {
		bad := []string{
			`{"tree":{}}`,                             // missing name
			`{"name":"p"}`,                            // missing tree
			`{"name":5,"tree":{}}`,                    // name not string
			`{"name":"p","tree":[]}`,                  // tree not object
			`{"name":"p","tree":{"$className":5}}`,    // $className not string
			`{"name":"p","tree":{"$path":5}}`,         // $path neither string nor object
			`{"name":"p","tree":{"child":"x"}}`,       // child not a tree
			`{"name":"p","servePort":"80","tree":{}}`, // servePort not integer
		}
		for _, src := range bad {
			dir := writeProject(t, map[string]string{"default.project.json": src})
			r := FromPath(filepath.Join(dir, "default.project.json"))
			w := r.GetWarnings()
			if len(w) != 1 || !strings.Contains(w[0], "Invalid configuration") {
				t.Errorf("config %s: warnings = %v", src, w)
			}
		}
	})
	t.Run("optional path object form", func(t *testing.T) {
		dir := writeProject(t, map[string]string{
			"default.project.json": `{"name":"p","tree":{"opt":{"$path":{"optional":"maybe"}}}}`,
		})
		r := FromPath(filepath.Join(dir, "default.project.json"))
		if len(r.GetWarnings()) != 0 {
			t.Fatalf("warnings = %v", r.GetWarnings())
		}
		// Nonexistent optional paths still create a partition (parsePath does
		// not require existence).
		got := mustRbxPath(t, r, filepath.Join(dir, "maybe", "x.luau"))
		if want := (RbxPath{"opt", "x"}); !reflect.DeepEqual(got, want) {
			t.Errorf("optional path = %v, want %v", got, want)
		}
	})
}

// --- FromTree ---

func TestFromTree(t *testing.T) {
	dir := writeProject(t, map[string]string{"src/": "", "lib/": ""})
	srcPath := filepath.Join(dir, "src")
	libPath := filepath.Join(dir, "lib")
	tree := &Tree{
		Children: []TreeEntry{
			{Name: "Game", Tree: &Tree{
				ClassName: "DataModel",
				Children: []TreeEntry{
					{Name: "ReplicatedStorage", Tree: &Tree{
						Children: []TreeEntry{
							{Name: "src", Tree: &Tree{Path: &srcPath}},
							{Name: "lib", Tree: &Tree{Path: &libPath}},
						},
					}},
				},
			}},
		},
	}
	r := FromTree(dir, tree)
	if !r.IsGame {
		t.Error("DataModel in tree must set IsGame")
	}
	got := mustRbxPath(t, r, filepath.Join(srcPath, "x.luau"))
	if want := (RbxPath{"Game", "ReplicatedStorage", "src", "x"}); !reflect.DeepEqual(got, want) {
		t.Errorf("fromTree path = %v, want %v", got, want)
	}
}
