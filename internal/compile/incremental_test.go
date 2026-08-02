package compile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildProjectIncrementalRebuildsChangedFilesAndImporters(t *testing.T) {
	dir := writeProject(t, "@scope/incremental-fixture", "")
	enableIncrementalBuilds(t, dir)
	writeIncrementalFixture(t, dir)

	first, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("first build diagnostics: %v", diags)
	}

	buildInfoPath := filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")
	buildInfo, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatalf("read build info: %v", err)
	}
	if !strings.Contains(string(buildInfo), "\"salt\"") {
		t.Fatalf("build info = %q, want rotor incremental manifest JSON", string(buildInfo))
	}

	old := time.Unix(100, 0)
	for _, rel := range []string{"out/main.luau", "out/util.luau", "out/side.luau"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "src", "util.ts"), []byte("export const VALUE = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("second build diagnostics: %v", diags)
	}

	if got, want := emittedFileBases(second), []string{"main.luau", "util.luau"}; !slices.Equal(got, want) {
		t.Fatalf("second build emitted files = %v, want %v", got, want)
	}

	sideInfo, err := os.Stat(filepath.Join(dir, "out", "side.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if !sideInfo.ModTime().Equal(old) {
		t.Fatalf("side.luau modtime = %v, want preserved %v", sideInfo.ModTime(), old)
	}

	_ = first
}

func enableIncrementalBuilds(t *testing.T, dir string) {
	t.Helper()
	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfigBytes, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	tsconfig := strings.Replace(string(tsconfigBytes), `"outDir": "out"`, `"outDir": "out",
		"incremental": true,
		"tsBuildInfoFile": "out/cache.tsbuildinfo"`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeIncrementalFixture(t *testing.T, dir string) {
	t.Helper()
	for rel, text := range map[string]string{
		"src/main.ts": "import { VALUE } from \"./util\";\nexport const main = VALUE;\n",
		"src/util.ts": "export const VALUE = 1;\n",
		"src/side.ts": "export const side = 1;\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func emittedFileBases(result *BuildResult) []string {
	if result == nil {
		return nil
	}
	bases := make([]string, 0, len(result.EmittedFiles))
	for _, path := range result.EmittedFiles {
		bases = append(bases, filepath.Base(path))
	}
	slices.Sort(bases)
	return bases
}
