package compile

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCrossProjectImportMap(t *testing.T) {
	root, libDir, gameDir := writeCrossProjectSolution(t)

	_, messages, err := BuildSolutionWithOptions(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildSolutionWithOptions: %v (%v)", err, messages)
	}

	gameOutput, err := os.ReadFile(filepath.Join(gameDir, "out", "init.luau"))
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"regular", "handauth"} {
		if !strings.Contains(string(gameOutput), `"shared", "`+module+`"`) {
			t.Fatalf("game import for %s did not use the library mount:\n%s", module, gameOutput)
		}
		if strings.Contains(string(gameOutput), `"game", "`+module+`"`) {
			t.Fatalf("game import for %s used the game mount:\n%s", module, gameOutput)
		}
	}

	if _, err := os.Stat(filepath.Join(libDir, "out", "regular.d.ts")); err != nil {
		t.Fatalf("library declaration: %v", err)
	}
}

func TestDrainInvalidatedProject(t *testing.T) {
	root, libDir, gameDir := writeCrossProjectSolution(t)
	coordinator, err := NewSolutionCoordinator(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err != nil {
		t.Fatalf("NewSolutionCoordinator: %v", err)
	}
	if _, messages, err := coordinator.Drain(); err != nil {
		t.Fatalf("Drain: %v (%v)", err, messages)
	}
	for _, path := range []string{
		filepath.Join(libDir, "out", "regular.luau"),
		filepath.Join(libDir, "out", "regular.d.ts"),
		filepath.Join(gameDir, "out", "init.luau"),
		filepath.Join(gameDir, "out", "index.d.ts"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("drain did not emit %s: %v", path, err)
		}
	}
}

func TestCrossProjectDeclarations(t *testing.T) {
	root, libDir, gameDir := writeCrossProjectSolution(t)

	if _, messages, err := BuildSolutionWithOptions(filepath.Join(root, "tsconfig.json"), ProjectOptions{}); err != nil {
		t.Fatalf("first BuildSolutionWithOptions: %v (%v)", err, messages)
	}
	if _, messages, err := BuildSolutionWithOptions(filepath.Join(gameDir, "tsconfig.json"), ProjectOptions{}); err != nil {
		t.Fatalf("second BuildSolutionWithOptions: %v (%v)", err, messages)
	}

	gameOutput, err := os.ReadFile(filepath.Join(gameDir, "out", "init.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gameOutput), `"shared", "handauth"`) {
		t.Fatalf("handwritten declaration import did not use library output:\n%s", gameOutput)
	}
	if _, err := os.Stat(filepath.Join(libDir, "out", "handauth", "index.d.ts")); err != nil {
		t.Fatalf("handwritten declaration was not copied: %v", err)
	}
}

func TestSolutionFailureRollback(t *testing.T) {
	root, libDir, gameDir := writeCrossProjectSolution(t)
	libConfigPath := filepath.Join(libDir, "tsconfig.json")
	libConfig := strings.Replace(string(mustReadFile(t, libConfigPath)), `"outDir":"out"`, `"outDir":"out","incremental":true,"tsBuildInfoFile":"out/cache.tsbuildinfo"`, 1)
	if err := os.WriteFile(libConfigPath, []byte(libConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, messages, err := BuildSolutionWithOptions(filepath.Join(root, "tsconfig.json"), ProjectOptions{}); err != nil {
		t.Fatalf("first BuildSolutionWithOptions: %v (%v)", err, messages)
	}

	buildInfoPath := filepath.Join(libDir, "out", "cache.rbxtsc.tsbuildinfo")
	copyCachePath := filepath.Join(libDir, "out", copyFilesCacheFilename)
	rojoCachePath := onlyCacheFile(t, filepath.Join(libDir, ".rotor", "cache", "rojo"))
	priorBuildInfo := mustReadFile(t, buildInfoPath)
	priorCopyCache := mustReadFile(t, copyCachePath)
	priorRojoCache := mustReadFile(t, rojoCachePath)
	priorGameOutput := mustReadFile(t, filepath.Join(gameDir, "out", "init.luau"))

	if err := os.WriteFile(filepath.Join(libDir, "src", "regular.ts"), []byte("export function regular(): string { return \"changed\"; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(libDir, "out", "regular.luau")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(libDir, "out", "regular.luau"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(libDir, "default.project.json"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	_, _, err := BuildSolutionWithOptions(filepath.Join(root, "tsconfig.json"), ProjectOptions{})
	if err == nil {
		t.Fatal("BuildSolutionWithOptions succeeded after the library output became a directory")
	}
	if got := mustReadFile(t, buildInfoPath); string(got) != string(priorBuildInfo) {
		t.Fatalf("build info changed after failed library emit:\ngot:  %q\nwant: %q", got, priorBuildInfo)
	}
	if got := mustReadFile(t, copyCachePath); string(got) != string(priorCopyCache) {
		t.Fatalf("copy cache changed after failed library emit:\ngot:  %q\nwant: %q", got, priorCopyCache)
	}
	if got := mustReadFile(t, rojoCachePath); string(got) != string(priorRojoCache) {
		t.Fatalf("Rojo cache changed after failed library emit:\ngot:  %q\nwant: %q", got, priorRojoCache)
	}
	if got := mustReadFile(t, filepath.Join(gameDir, "out", "init.luau")); string(got) != string(priorGameOutput) {
		t.Fatal("dependent output changed after failed library emit")
	}
}

func writeCrossProjectSolution(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	gameDir := filepath.Join(root, "game")
	for _, dir := range []string{filepath.Join(libDir, "src", "handauth"), filepath.Join(gameDir, "src"), filepath.Join(gameDir, "include"), filepath.Join(libDir, "node_modules", "@rbxts"), filepath.Join(gameDir, "node_modules", "@rbxts")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeSolutionFile(t, root, "tsconfig.json", `{"files":[],"references":[{"path":"./lib"},{"path":"./game"}]}`)
	writeSolutionFile(t, libDir, "package.json", `{"name":"@scope/lib"}`)
	writeSolutionFile(t, libDir, "default.project.json", `{"name":"lib","tree":{"$path":"out"}}`)
	writeSolutionFile(t, libDir, "tsconfig.json", crossProjectCompilerOptions(true)+`,"include":["src"]}`)
	writeSolutionFile(t, libDir, "src/globals.d.ts", noLibGlobalStubs)
	writeSolutionFile(t, libDir, "src/regular.ts", "export function regular(): string { return \"regular\"; }\n")
	writeSolutionFile(t, libDir, "src/handauth/index.d.ts", "export declare function handauth(): string;\n")
	writeSolutionFile(t, libDir, "src/handauth/init.luau", "return { handauth = function() return \"handauth\" end }\n")

	writeSolutionFile(t, gameDir, "package.json", `{"name":"game"}`)
	writeSolutionFile(t, gameDir, "tsconfig.json", crossProjectCompilerOptions(true)+`,"rbxts":{"rojo":"./game.project.json","type":"game"},"include":["src"],"references":[{"path":"../lib"}]}`)
	writeSolutionFile(t, gameDir, "src/globals.d.ts", noLibGlobalStubs)
	writeSolutionFile(t, gameDir, "src/index.ts", "import { handauth } from \"../../lib/src/handauth\";\nimport { regular } from \"../../lib/src/regular\";\nexport const value = regular() + handauth();\n")
	writeSolutionFile(t, gameDir, "include/RuntimeLib.lua", "return {}\n")
	writeSolutionFile(t, gameDir, "game.project.json", `{"name":"game","tree":{"$className":"DataModel","ReplicatedStorage":{"include":{"$path":"include"},"shared":{"$path":"../lib/out"},"game":{"$path":"out"}}}}`)
	return root, libDir, gameDir
}

func crossProjectCompilerOptions(declaration bool) string {
	return `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":` + strconv.FormatBool(declaration) + `,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"}`
}

func writeSolutionFile(t *testing.T, dir, path, text string) {
	t.Helper()
	fullPath := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func onlyCacheFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache files = %v, want exactly one", entries)
	}
	return filepath.Join(dir, entries[0].Name())
}
