package compile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"rotor/tsgo/vfs/osvfs"
)

type outputWriterOperations struct {
	readFile  func(string) ([]byte, error)
	mkdirAll  func(string, fs.FileMode) error
	writeFile func(string, []byte, fs.FileMode) error
	lstat     func(string) (fs.FileInfo, error)
}

type outputWriter struct {
	operations    outputWriterOperations
	caseSensitive bool
	secureRoot    bool
	root          *os.Root
	prepared      sync.Map
	projectDir    string
	previous      map[string]string
	current       map[string]string
	hashSkips     int
	mu            sync.Mutex
}

func newOutputWriter() *outputWriter {
	writer := newOutputWriterWithOperations(outputWriterOperations{
		readFile:  os.ReadFile,
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
		lstat:     os.Lstat,
	}, osvfs.FS().UseCaseSensitiveFileNames())
	writer.secureRoot = true
	return writer
}

func (writer *outputWriter) useHashes(projectDir string, previous, current map[string]string) error {
	writer.projectDir = filepath.Clean(projectDir)
	writer.previous = previous
	writer.current = current
	if writer.secureRoot {
		root, err := os.OpenRoot(writer.projectDir)
		if err != nil {
			return err
		}
		writer.root = root
	}
	return nil
}

func newOutputWriterWithOperations(operations outputWriterOperations, caseSensitive bool) *outputWriter {
	return &outputWriter{
		operations:    operations,
		caseSensitive: caseSensitive,
	}
}

func (writer *outputWriter) write(path string, text string, writeOnlyChanged bool) (bool, error) {
	contents := []byte(text)
	sum := sha256.Sum256(contents)
	hash := hex.EncodeToString(sum[:])
	key := writer.outputKey(path)
	if previous, ok := writer.previous[key]; ok && previous == hash {
		info, err := writer.lstat(path)
		if err == nil && info.Mode().IsRegular() {
			writer.mu.Lock()
			writer.current[key] = hash
			writer.hashSkips++
			writer.mu.Unlock()
			return false, nil
		}
	}
	if writeOnlyChanged {
		var existing []byte
		var err error
		if writer.root != nil {
			existing, err = writer.root.ReadFile(key)
		} else {
			existing, err = writer.operations.readFile(path)
		}
		if err == nil && bytes.Equal(existing, contents) {
			writer.recordHash(key, hash)
			return false, nil
		}
	}
	var err error
	if writer.root != nil {
		err = writer.root.WriteFile(key, contents, 0o644)
	} else {
		err = writer.operations.writeFile(path, contents, 0o644)
	}
	if err != nil {
		return false, err
	}
	writer.recordHash(key, hash)
	return true, nil
}

func (writer *outputWriter) lstat(path string) (fs.FileInfo, error) {
	key := writer.outputKey(path)
	if !filepath.IsLocal(filepath.FromSlash(key)) {
		return nil, fmt.Errorf("compile: refusing to inspect output outside the project directory: %q", path)
	}
	if writer.root != nil {
		return writer.root.Lstat(filepath.FromSlash(key))
	}
	return writer.operations.lstat(path)
}

func (writer *outputWriter) prepare(paths []string) error {
	for _, path := range paths {
		if err := writer.prepareParent(path); err != nil {
			return err
		}
	}
	return nil
}

func (writer *outputWriter) outputKey(path string) string {
	if writer.projectDir == "" {
		return ""
	}
	relative, err := filepath.Rel(writer.projectDir, filepath.Clean(path))
	if err != nil {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(relative)
}

func (writer *outputWriter) recordHash(key, hash string) {
	if writer.current == nil {
		return
	}
	writer.mu.Lock()
	writer.current[key] = hash
	writer.mu.Unlock()
}

func (writer *outputWriter) hashSkipCount() int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.hashSkips
}

func (writer *outputWriter) prepareParent(path string) error {
	if writer.projectDir != "" {
		relative, err := filepath.Rel(writer.projectDir, filepath.Clean(path))
		if err != nil || !filepath.IsLocal(relative) {
			return fmt.Errorf("compile: refusing to prepare output outside the project directory: %q", path)
		}
	}
	parent, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	parent = filepath.Clean(parent)
	key := parent
	if !writer.caseSensitive {
		key = strings.ToLower(key)
	}
	prepare := sync.OnceValue(func() error {
		if writer.root != nil {
			relative, err := filepath.Rel(writer.projectDir, parent)
			if err != nil {
				return err
			}
			return writer.root.MkdirAll(relative, 0o755)
		}
		return writer.operations.mkdirAll(parent, 0o755)
	})
	actual, _ := writer.prepared.LoadOrStore(key, prepare)
	return actual.(func() error)()
}

func (writer *outputWriter) close() error {
	if writer.root == nil {
		return nil
	}
	err := writer.root.Close()
	writer.root = nil
	return err
}
