package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdBuildJSONReferenceOnlySolution(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	mustWrite(t, filepath.Join(root, "tsconfig.json"), `{"files":[],"include":[],"references":[{"path":"./child"}]}`)
	mustWrite(t, filepath.Join(child, "tsconfig.json"), `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"]}`)
	mustWrite(t, filepath.Join(child, "package.json"), `{"name":"@scope/solution-child"}`)
	mustWrite(t, filepath.Join(child, "src", "globals.d.ts"), noLibGlobalStubs)
	mustWrite(t, filepath.Join(child, "src", "main.ts"), "export const value = 1;\n")

	output, code := captureStdout(t, func() int {
		return cmdBuild([]string{
			"--build",
			"--project", filepath.Join(root, "tsconfig.json"),
			"--builders", "1",
			"--checkers", "1",
			"--json",
		})
	})
	if code != 0 {
		t.Fatalf("solution build exit = %d, want 0; output:\n%s", code, output)
	}

	var result jsonResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, output)
	}
	if !result.OK || result.Files == 0 {
		t.Fatalf("solution result = %+v, want successful child outputs", result)
	}
	if _, err := os.Stat(filepath.Join(child, "out", "main.luau")); err != nil {
		t.Fatalf("child output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "out")); !os.IsNotExist(err) {
		t.Fatalf("coordinator output stat error = %v, want not exists", err)
	}
}
