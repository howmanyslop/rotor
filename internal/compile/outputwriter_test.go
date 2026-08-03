package compile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

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

	wrote, err := writer.write(filepath.Join("out", "missing", "main.luau"), "unchanged", true)
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
	errs := make([]error, outputs)
	var started sync.WaitGroup
	started.Add(outputs)
	ready := make(chan struct{})
	go func() {
		started.Wait()
		close(ready)
	}()
	var completed sync.WaitGroup
	completed.Add(outputs)
	for index := range outputs {
		go func() {
			defer completed.Done()
			started.Done()
			<-ready
			_, errs[index] = writer.write(filepath.Join("out", "blocked", fmt.Sprintf("%d.luau", index)), "output", true)
		}()
	}
	completed.Wait()

	if got := mkdirCalls.Load(); got != 1 {
		t.Fatalf("MkdirAll calls = %d, want 1", got)
	}
	if got := writeCalls.Load(); got != 0 {
		t.Fatalf("WriteFile calls = %d, want 0", got)
	}
	for index, err := range errs {
		if !errors.Is(err, failure) {
			t.Errorf("write %d error = %v, want %v", index, err, failure)
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

func TestParallelWriteFailureSemantics(t *testing.T) {
	failure := errors.New("blocked parent")
	otherStarted := make(chan struct{})
	var otherWrites atomic.Int32
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile: func(string) ([]byte, error) {
			return nil, os.ErrNotExist
		},
		mkdirAll: func(path string, _ fs.FileMode) error {
			if filepath.Base(path) == "failed" {
				<-otherStarted
				return failure
			}
			close(otherStarted)
			return nil
		},
		writeFile: func(path string, _ []byte, _ fs.FileMode) error {
			if filepath.Base(filepath.Dir(path)) == "other" {
				otherWrites.Add(1)
			}
			return nil
		},
	}, true)

	jobs := make([]func() error, 16)
	jobs[0] = func() error {
		_, err := writer.write(filepath.Join("out", "failed", "first.luau"), "output", true)
		return err
	}
	jobs[1] = func() error {
		_, err := writer.write(filepath.Join("out", "other", "first.luau"), "output", true)
		return err
	}
	for index := 2; index < len(jobs); index++ {
		jobs[index] = func() error { return nil }
	}

	if err := parallelize(2, jobs); !errors.Is(err, failure) {
		t.Fatalf("parallel writes error = %v, want %v", err, failure)
	}
	if got := otherWrites.Load(); got != 1 {
		t.Fatalf("started unrelated writes = %d, want 1", got)
	}
}
