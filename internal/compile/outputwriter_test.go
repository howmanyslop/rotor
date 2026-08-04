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
