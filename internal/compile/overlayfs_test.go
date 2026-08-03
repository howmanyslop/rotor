package compile

import (
	"sync"
	"testing"

	"rotor/tsgo/vfs"
	"rotor/tsgo/vfs/vfstest"
	"rotor/tsgo/vfs/wrapvfs"
)

func TestOverlayProgramMetadataCachedOnce(t *testing.T) {
	t.Parallel()

	// Given
	const filePath = "/project/src/main.ts"
	const directoryPath = "/project/src"
	baseFS := newCountingFS(vfstest.FromMap(map[string]string{filePath: "export {};\n"}, true))
	fs := newOverlayFS(baseFS, "", map[string]string{})

	// When
	for range 2 {
		_ = fs.Stat(filePath)
		_ = fs.DirectoryExists(directoryPath)
		_ = fs.FileExists(filePath)
		_ = fs.Realpath(filePath)
		_ = fs.GetAccessibleEntries(directoryPath)
	}

	// Then
	baseFS.requireCalls(t, "Stat", filePath, 1)
	baseFS.requireCalls(t, "DirectoryExists", directoryPath, 1)
	baseFS.requireCalls(t, "FileExists", filePath, 1)
	baseFS.requireCalls(t, "Realpath", filePath, 1)
	baseFS.requireCalls(t, "GetAccessibleEntries", directoryPath, 1)
}

func TestOverlayProgramCacheIsBuildScoped(t *testing.T) {
	t.Parallel()

	// Given
	const filePath = "/project/src/generated.ts"
	present := false
	baseFS := newCountingFS(wrapvfs.Wrap(vfstest.FromMap(map[string]string{}, true), wrapvfs.Replacements{
		FileExists: func(path string) bool {
			return path == filePath && present
		},
	}))
	firstProgramFS := newOverlayFS(baseFS, "", map[string]string{})

	// When
	if firstProgramFS.FileExists(filePath) {
		t.Fatal("first Program saw file before it existed")
	}
	present = true
	secondProgramFS := newOverlayFS(baseFS, "", map[string]string{})

	// Then
	if !secondProgramFS.FileExists(filePath) {
		t.Fatal("second Program retained the first Program's missing-file metadata")
	}
	baseFS.requireCalls(t, "FileExists", filePath, 2)
}

func TestOverlayProgramOverlayWinsNegativeBaseCache(t *testing.T) {
	t.Parallel()

	// Given
	const filePath = "/project/src/transformed.ts"
	baseFS := newCountingFS(vfstest.FromMap(map[string]string{}, true))
	overlays := map[string]string{}
	fs := newOverlayFS(baseFS, "", overlays)

	// When
	if fs.FileExists(filePath) {
		t.Fatal("base filesystem unexpectedly contains transformed file")
	}
	overlays[filePath] = "export const transformed = true;\n"
	text, ok := fs.ReadFile(filePath)

	// Then
	if !fs.FileExists(filePath) {
		t.Fatal("overlay did not override cached missing-file metadata")
	}
	if !ok || text != overlays[filePath] {
		t.Fatalf("ReadFile(%q) = (%q, %t), want overlay text", filePath, text, ok)
	}
	baseFS.requireCalls(t, "FileExists", filePath, 1)
}

type countingFS struct {
	vfs.FS

	mu    sync.Mutex
	calls map[string]int
}

func newCountingFS(inner vfs.FS) *countingFS {
	return &countingFS{
		FS:    inner,
		calls: map[string]int{},
	}
}

func (fs *countingFS) FileExists(path string) bool {
	fs.record("FileExists", path)
	return fs.FS.FileExists(path)
}

func (fs *countingFS) DirectoryExists(path string) bool {
	fs.record("DirectoryExists", path)
	return fs.FS.DirectoryExists(path)
}

func (fs *countingFS) GetAccessibleEntries(path string) vfs.Entries {
	fs.record("GetAccessibleEntries", path)
	return fs.FS.GetAccessibleEntries(path)
}

func (fs *countingFS) Realpath(path string) string {
	fs.record("Realpath", path)
	return fs.FS.Realpath(path)
}

func (fs *countingFS) Stat(path string) vfs.FileInfo {
	fs.record("Stat", path)
	return fs.FS.Stat(path)
}

func (fs *countingFS) record(method, path string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.calls[method+":"+path]++
}

func (fs *countingFS) requireCalls(t *testing.T, method, path string, want int) {
	t.Helper()

	fs.mu.Lock()
	got := fs.calls[method+":"+path]
	fs.mu.Unlock()
	if got != want {
		t.Fatalf("%s(%q) calls = %d, want %d", method, path, got, want)
	}
}
