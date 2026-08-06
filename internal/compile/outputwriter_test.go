package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestOutputHashMatchUsesLstatWithoutOpening(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "out", "main.luau")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("output")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	hash := hex.EncodeToString(sum[:])
	var reads atomic.Int32
	var writes atomic.Int32
	var lstats atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			reads.Add(1)
			return nil, errors.New("unexpected read")
		},
		mkdirAll: os.MkdirAll,
		writeFile: func(string, []byte, fs.FileMode) error {
			writes.Add(1)
			return nil
		},
		lstat: func(path string) (fs.FileInfo, error) {
			lstats.Add(1)
			return os.Lstat(path)
		},
	}, true)
	current := map[string]string{}
	writer.useHashes(projectDir, map[string]string{"out/main.luau": hash}, current)

	wrote, err := writer.write(path, string(contents), true)
	if err != nil {
		t.Fatal(err)
	}
	if wrote || reads.Load() != 0 || writes.Load() != 0 || lstats.Load() != 1 {
		t.Fatalf("wrote=%v reads=%d writes=%d lstats=%d", wrote, reads.Load(), writes.Load(), lstats.Load())
	}
	if current["out/main.luau"] != hash || writer.hashSkipCount() != 1 {
		t.Fatalf("current hashes = %v, skips = %d", current, writer.hashSkipCount())
	}
}

func TestOutputWriterPrepareRejectsPathOutsideProject(t *testing.T) {
	projectDir := t.TempDir()
	writes := 0
	writer := newOutputWriterWithOperations(outputWriterOperations{
		mkdirAll: func(string, fs.FileMode) error {
			writes++
			return nil
		},
	}, true)
	writer.useHashes(projectDir, map[string]string{}, map[string]string{})

	err := writer.prepare([]string{filepath.Join(projectDir, "..", "outside", "main.luau")})
	if err == nil {
		t.Fatal("prepare accepted an output outside the project")
	}
	if writes != 0 {
		t.Fatalf("mkdir calls = %d, want 0", writes)
	}
}

func TestOutputWriterLstatRejectsPathOutsideProject(t *testing.T) {
	projectDir := t.TempDir()
	stats := 0
	writer := newOutputWriterWithOperations(outputWriterOperations{
		lstat: func(string) (fs.FileInfo, error) {
			stats++
			return nil, os.ErrNotExist
		},
	}, true)
	writer.useHashes(projectDir, map[string]string{}, map[string]string{})

	if _, err := writer.lstat(filepath.Join(projectDir, "..", "outside.luau")); err == nil {
		t.Fatal("lstat accepted an output outside the project")
	}
	if stats != 0 {
		t.Fatalf("underlying stat calls = %d, want 0", stats)
	}
}

func TestOutputWriterRejectsSymlinkEscape(t *testing.T) {
	projectDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(projectDir, "out")); err != nil {
		t.Fatal(err)
	}

	writer := newOutputWriter()
	if err := writer.useHashes(projectDir, map[string]string{}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.close() })
	path := filepath.Join(projectDir, "out", "main.luau")
	if err := writer.prepare([]string{path}); err == nil {
		t.Fatal("prepare accepted a parent symlink outside the project")
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "main.luau")); !os.IsNotExist(err) {
		t.Fatalf("outside output stat error = %v, want not-exist", err)
	}
}

func TestOutputWriterRejectsFinalSymlinkEscape(t *testing.T) {
	projectDir := t.TempDir()
	outDir := filepath.Join(projectDir, "out")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.luau")
	if err := os.WriteFile(outsidePath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(outDir, "main.luau")
	if err := os.Symlink(outsidePath, outputPath); err != nil {
		t.Fatal(err)
	}

	writer := newOutputWriter()
	if err := writer.useHashes(projectDir, map[string]string{}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.close() })
	if _, err := writer.write(outputPath, "replacement", false); err == nil {
		t.Fatal("write accepted a final symlink outside the project")
	}
	contents, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("outside output = %q, want original", contents)
	}
}

func TestOutputDirectoryCreatedOnce(t *testing.T) {
	var mkdirCalls atomic.Int32
	var writeCalls atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
		mkdirAll: func(string, fs.FileMode) error {
			mkdirCalls.Add(1)
			return nil
		},
		writeFile: func(string, []byte, fs.FileMode) error {
			writeCalls.Add(1)
			return nil
		},
	}, true)

	const outputs = 32
	jobs := make([]func() error, outputs)
	for index := range jobs {
		path := filepath.Join("out", "shared", fmt.Sprintf("%d.luau", index))
		jobs[index] = func() error {
			wrote, err := writer.write(path, "output", true)
			if err != nil {
				return err
			}
			if !wrote {
				return errors.New("output was unexpectedly skipped")
			}
			return nil
		}
	}
	paths := make([]string, outputs)
	for index := range paths {
		paths[index] = filepath.Join("out", "shared", fmt.Sprintf("%d.luau", index))
	}
	if err := writer.prepare(paths); err != nil {
		t.Fatal(err)
	}

	if err := parallelize(outputs, jobs); err != nil {
		t.Fatalf("parallel writes: %v", err)
	}
	if got := mkdirCalls.Load(); got != 1 {
		t.Fatalf("MkdirAll calls = %d, want 1", got)
	}
	if got := writeCalls.Load(); got != outputs {
		t.Fatalf("WriteFile calls = %d, want %d", got, outputs)
	}
}

func TestOutputDirectoryUnchangedDoesNotCreate(t *testing.T) {
	var mkdirCalls atomic.Int32
	var writeCalls atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			return []byte("unchanged"), nil
		},
		mkdirAll: func(string, fs.FileMode) error {
			mkdirCalls.Add(1)
			return nil
		},
		writeFile: func(string, []byte, fs.FileMode) error {
			writeCalls.Add(1)
			return nil
		},
	}, true)

	path := filepath.Join("out", "missing", "main.luau")
	wrote, err := writer.write(path, "unchanged", true)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if wrote {
		t.Fatal("write = true, want false")
	}
	if got := mkdirCalls.Load(); got != 0 {
		t.Fatalf("MkdirAll calls = %d, want 0", got)
	}
	if got := writeCalls.Load(); got != 0 {
		t.Fatalf("WriteFile calls = %d, want 0", got)
	}
}

func TestOutputDirectoryFailureMemoized(t *testing.T) {
	failure := errors.New("inaccessible parent")
	var mkdirCalls atomic.Int32
	var writeCalls atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
		mkdirAll: func(string, fs.FileMode) error {
			mkdirCalls.Add(1)
			return failure
		},
		writeFile: func(string, []byte, fs.FileMode) error {
			writeCalls.Add(1)
			return nil
		},
	}, true)

	const outputs = 32
	paths := make([]string, outputs)
	for index := range paths {
		paths[index] = filepath.Join("out", "blocked", fmt.Sprintf("%d.luau", index))
	}
	first := writer.prepare(paths)
	second := writer.prepare(paths)

	if got := mkdirCalls.Load(); got != 1 {
		t.Fatalf("MkdirAll calls = %d, want 1", got)
	}
	if got := writeCalls.Load(); got != 0 {
		t.Fatalf("WriteFile calls = %d, want 0", got)
	}
	for index, err := range []error{first, second} {
		if !errors.Is(err, failure) {
			t.Errorf("prepare %d error = %v, want %v", index, err, failure)
		}
	}
}

func TestOutputDirectoryCaseCanonicalized(t *testing.T) {
	var mkdirCalls atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
		mkdirAll: func(string, fs.FileMode) error {
			mkdirCalls.Add(1)
			return nil
		},
		writeFile: func(string, []byte, fs.FileMode) error {
			return nil
		},
	}, false)

	for _, path := range []string{
		filepath.Join("out", "Shared", "first.luau"),
		filepath.Join("out", "shared", "second.luau"),
	} {
		if err := writer.prepare([]string{path}); err != nil {
			t.Fatal(err)
		}
		wrote, err := writer.write(path, "output", true)
		if err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
		if !wrote {
			t.Fatalf("write %q = false, want true", path)
		}
	}
	if got := mkdirCalls.Load(); got != 1 {
		t.Fatalf("MkdirAll calls = %d, want 1", got)
	}
}

func TestOutputPresenceIndex(t *testing.T) {
	projectDir := t.TempDir()
	outDir := filepath.Join(projectDir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"out/a.luau", "out/b.luau"} {
		if err := os.WriteFile(filepath.Join(projectDir, filepath.FromSlash(rel)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(outDir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outDir, "a.luau"), filepath.Join(outDir, "alink.luau")); err != nil {
		t.Fatal(err)
	}

	writer := newOutputWriter()
	if err := writer.useHashes(projectDir, map[string]string{}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.close() })
	index := writer.newOutputPresenceIndex(map[string]string{
		"out/a.luau":       "hash",
		"out/b.luau":       "hash",
		"out/alink.luau":   "hash",
		"out/adir":         "hash",
		"out/missing.luau": "hash",
		"../outside.luau":  "hash",
	})
	for _, present := range []string{"out/a.luau", "out/b.luau"} {
		if !index.hasRegular(present) {
			t.Errorf("hasRegular(%q) = false, want true", present)
		}
	}
	for _, absent := range []string{"out/alink.luau", "out/adir", "out/missing.luau", "../outside.luau"} {
		if index.hasRegular(absent) {
			t.Errorf("hasRegular(%q) = true, want false", absent)
		}
	}
}

func TestOutputPresenceIndexCaseNormalization(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "out", "Shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "out", "Shared", "X.luau"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "out", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "out", "deep", "x.luau"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	sensitive := newOutputWriterWithOperations(outputWriterOperations{}, true)
	sensitive.secureRoot = true
	if err := sensitive.useHashes(projectDir, map[string]string{}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sensitive.close() })
	sensitiveIndex := sensitive.newOutputPresenceIndex(map[string]string{
		"out/Shared/X.luau": "hash",
		"out/deep/x.luau":   "hash",
	})
	for _, present := range []string{"out/Shared/X.luau", "out/deep/x.luau"} {
		if !sensitiveIndex.hasRegular(present) {
			t.Errorf("case-sensitive hasRegular(%q) = false, want true", present)
		}
	}
	if sensitiveIndex.hasRegular("out/Shared/x.luau") {
		t.Error("case-sensitive hasRegular(out/Shared/x.luau) = true, want false")
	}

	insensitive := newOutputWriterWithOperations(outputWriterOperations{}, false)
	insensitive.secureRoot = true
	if err := insensitive.useHashes(projectDir, map[string]string{}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = insensitive.close() })
	insensitiveIndex := insensitive.newOutputPresenceIndex(map[string]string{"out/deep/x.luau": "hash"})
	for _, present := range []string{"out/deep/x.luau", "out/DEEP/x.luau", "out/deep/X.luau"} {
		if !insensitiveIndex.hasRegular(present) {
			t.Errorf("case-insensitive hasRegular(%q) = false, want true", present)
		}
	}
	if insensitiveIndex.hasRegular("out/deep/missing.luau") {
		t.Error("case-insensitive hasRegular(out/deep/missing.luau) = true, want false")
	}
}

func TestBatchPrepareFailurePreventsWrites(t *testing.T) {
	failure := errors.New("blocked parent")
	var otherWrites atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
		mkdirAll: func(path string, _ fs.FileMode) error {
			if filepath.Base(path) == "failed" {
				return failure
			}
			return nil
		},
		writeFile: func(path string, _ []byte, _ fs.FileMode) error {
			if filepath.Base(filepath.Dir(path)) == "other" {
				otherWrites.Add(1)
			}
			return nil
		},
	}, true)

	if err := writer.prepare([]string{
		filepath.Join("out", "failed", "first.luau"),
		filepath.Join("out", "other", "first.luau"),
	}); !errors.Is(err, failure) {
		t.Fatalf("prepare error = %v, want %v", err, failure)
	}
	if got := otherWrites.Load(); got != 0 {
		t.Fatalf("writes after prepare failure = %d, want 0", got)
	}
}
