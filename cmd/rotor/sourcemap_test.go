package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSourcemapProject lays out a tiny native-subset Rojo project and returns
// its directory.
func writeSourcemapProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"default.project.json": `{"name":"t","tree":{"$className":"Folder","Src":{"$path":"src"}}}`,
		"src/init.luau":        "return {}",
		"src/main.server.luau": "print(1)",
		"src/note.txt":         "hi",
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type sourcemapNode struct {
	Name      string          `json:"name"`
	ClassName string          `json:"className"`
	FilePaths []string        `json:"filePaths"`
	Children  []sourcemapNode `json:"children"`
}

func assertSourcemapPaths(t *testing.T, n sourcemapNode) {
	t.Helper()
	for _, p := range n.FilePaths {
		if strings.Contains(p, "\\") {
			t.Errorf("filePath %q contains a backslash", p)
		}
		if filepath.IsAbs(p) || strings.Contains(p, ":") {
			t.Errorf("filePath %q is not project-relative", p)
		}
	}
	for _, c := range n.Children {
		assertSourcemapPaths(t, c)
	}
}

func TestCmdSourcemapToFile(t *testing.T) {
	dir := writeSourcemapProject(t)
	out := filepath.Join(dir, "sourcemap.json")
	if code := cmdSourcemap([]string{dir, "-o", out}); code != 0 {
		t.Fatalf("cmdSourcemap exit code = %d", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var root sourcemapNode
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("sourcemap is not valid JSON: %v\n%s", err, data)
	}
	if root.Name != "t" || root.ClassName != "Folder" {
		t.Fatalf("root = %s %s, want t Folder", root.Name, root.ClassName)
	}
	// Even on Windows, every path must be project-relative with forward
	// slashes.
	assertSourcemapPaths(t, root)
}

func TestCmdSourcemapStdout(t *testing.T) {
	dir := writeSourcemapProject(t)

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var data []byte
	go func() {
		data, _ = io.ReadAll(r)
		close(done)
	}()
	code := cmdSourcemap([]string{dir})
	_ = w.Close()
	os.Stdout = old
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("cmdSourcemap exit code = %d", code)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("stdout output should end with a newline, got %q", data)
	}
	var root sourcemapNode
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("stdout sourcemap is not valid JSON: %v\n%s", err, data)
	}
	assertSourcemapPaths(t, root)
}

func TestCmdSourcemapArgErrors(t *testing.T) {
	if code := cmdSourcemap([]string{"--bogus"}); code != 1 {
		t.Errorf("unknown flag: exit %d, want 1", code)
	}
	if code := cmdSourcemap([]string{"a", "b"}); code != 1 {
		t.Errorf("extra positional: exit %d, want 1", code)
	}
	if code := cmdSourcemap([]string{"-o"}); code != 1 {
		t.Errorf("-o without value: exit %d, want 1", code)
	}
	if code := cmdSourcemap([]string{filepath.Join(t.TempDir(), "missing")}); code != 1 {
		t.Errorf("missing project path: exit %d, want 1", code)
	}
}
