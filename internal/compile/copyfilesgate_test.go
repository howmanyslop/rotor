package compile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rotor/internal/rojo"
)

func TestCopyFilesManifest(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "src")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(rootDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "asset.luau"), "asset")
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "nested", "data.json"), "{}")
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "source.ts"), "export {}")
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "node_modules", "pkg", "ignored.luau"), "ignored")
	if err := os.MkdirAll(filepath.Join(outDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCopyFilesTestFile(t, filepath.Join(outDir, "asset.luau"), "asset")

	translator := rojo.NewPathTranslator(rootDir, outDir, "", false, true)
	manifest := buildCopyFilesManifest(CopyFilesManifestInputs{
		RootDirs:       []string{rootDir},
		OutDir:         outDir,
		Key:            buildCopyFilesManifestKey(outDir, []string{rootDir}),
		PathTranslator: translator,
	})

	if len(manifest.Files) != 2 {
		t.Fatalf("manifest files = %d, want 2", len(manifest.Files))
	}
	if len(manifest.Dirs) != 2 {
		t.Fatalf("manifest dirs = %d, want root and nested", len(manifest.Dirs))
	}
	for _, file := range manifest.Files {
		if file.SrcMtimeMs <= 0 {
			t.Fatalf("source mtime = %d, want positive", file.SrcMtimeMs)
		}
		if file.Src == filepath.Join(rootDir, "asset.luau") && file.DestMtimeMs <= 0 {
			t.Fatalf("destination mtime = %d, want positive", file.DestMtimeMs)
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CopyFilesManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Files) != len(manifest.Files) || len(decoded.Dirs) != len(manifest.Dirs) {
		t.Fatalf("decoded manifest = %+v, want same entry counts", decoded)
	}
}

func TestCopyFilesGate(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "src")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "asset.luau"), "v1")
	writeCopyFilesTestFile(t, filepath.Join(outDir, "asset.luau"), "v1")
	translator := rojo.NewPathTranslator(rootDir, outDir, "", false, true)
	inputs := func(snapshot BuildStateSnapshot) copyFilesGateInputs {
		return copyFilesGateInputs{
			RootDirs:       []string{rootDir},
			OutDir:         outDir,
			PathTranslator: translator,
			Snapshot:       snapshot,
		}
	}

	cold := loadCopyFilesGatePreBuild(inputs(BuildStateSnapshot{}))
	if cold.SkipCleanup || cold.SkipCopyFiles {
		t.Fatalf("cold gate = %+v, want both skips false", cold)
	}
	cold.Persist()
	warm := loadCopyFilesGatePreBuild(inputs(BuildStateSnapshot{}))
	if !warm.SkipCleanup || !warm.SkipCopyFiles {
		t.Fatalf("warm gate = %+v, want both skips true", warm)
	}

	changed := loadCopyFilesGatePreBuild(inputs(BuildStateSnapshot{
		ChangedFilePaths: map[string]struct{}{filepath.Join(rootDir, "source.ts"): {}},
	}))
	if changed.SkipCleanup || !changed.SkipCopyFiles {
		t.Fatalf("changed TS gate = %+v, want cleanup false and copy true", changed)
	}

	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(rootDir, "asset.luau"), future, future); err != nil {
		t.Fatal(err)
	}
	drifted := loadCopyFilesGatePreBuild(inputs(BuildStateSnapshot{}))
	if drifted.SkipCopyFiles {
		t.Fatal("source mtime drift incorrectly skipped copy")
	}

	if err := os.Remove(filepath.Join(outDir, "asset.luau")); err != nil {
		t.Fatal(err)
	}
	missingDest := loadCopyFilesGatePreBuild(inputs(BuildStateSnapshot{}))
	if missingDest.SkipCopyFiles {
		t.Fatal("missing destination incorrectly skipped copy")
	}
}

func TestCopyOutputRecursionGuard(t *testing.T) {
	dir := t.TempDir()
	rootDir := dir
	outDir := filepath.Join(dir, "out")
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "asset.luau"), "asset")
	writeCopyFilesTestFile(t, filepath.Join(outDir, "stale.luau"), "stale")
	writeCopyFilesTestFile(t, filepath.Join(rootDir, "node_modules", "pkg", "ignored.luau"), "ignored")
	translator := rojo.NewPathTranslator(rootDir, outDir, "", false, true)

	if err := copyNonCompiledFiles(translator, []string{rootDir}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "asset.luau")); err != nil {
		t.Fatalf("asset was not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "out")); !os.IsNotExist(err) {
		t.Fatalf("recursive outDir = %v, want not-exist", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("node_modules output = %v, want not-exist", err)
	}
}

func TestProtectedOutputs(t *testing.T) {
	dir := t.TempDir()
	translator := rojo.NewPathTranslator(filepath.Join(dir, "src"), filepath.Join(dir, "out"), "", false, true)
	for _, name := range []string{
		copyFilesCacheFilename,
		copyFilesResolverCacheFilename,
		copyFilesTypeRootsCacheFilename,
	} {
		if isOutputFileOrphaned(translator, filepath.Join(dir, "out", name)) {
			t.Fatalf("%s is orphaned, want protected", name)
		}
	}
	if !isOutputFileOrphaned(translator, filepath.Join(dir, "out", "ordinary.json")) {
		t.Fatal("ordinary output is not orphaned, want orphaned")
	}
}

func writeCopyFilesTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
