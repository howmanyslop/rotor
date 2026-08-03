package compile

import (
	"bytes"
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
}

type outputWriter struct {
	operations    outputWriterOperations
	caseSensitive bool
	prepared      sync.Map
}

func newOutputWriter() *outputWriter {
	return newOutputWriterWithOperations(outputWriterOperations{
		readFile:  os.ReadFile,
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
	}, osvfs.FS().UseCaseSensitiveFileNames())
}

func newOutputWriterWithOperations(operations outputWriterOperations, caseSensitive bool) *outputWriter {
	return &outputWriter{
		operations:    operations,
		caseSensitive: caseSensitive,
	}
}

func (writer *outputWriter) write(path string, text string, writeOnlyChanged bool) (bool, error) {
	contents := []byte(text)
	if writeOnlyChanged {
		if existing, err := writer.operations.readFile(path); err == nil && bytes.Equal(existing, contents) {
			return false, nil
		}
	}
	if err := writer.prepareParent(path); err != nil {
		return false, err
	}
	if err := writer.operations.writeFile(path, contents, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func (writer *outputWriter) prepareParent(path string) error {
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
		return writer.operations.mkdirAll(parent, 0o755)
	})
	actual, _ := writer.prepared.LoadOrStore(key, prepare)
	return actual.(func() error)()
}
