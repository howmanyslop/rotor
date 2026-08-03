package compile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const performanceOutputRoot = "<PERFORMANCE_OUTPUT_ROOT>"

var performanceFixtureTime = time.Unix(946684800, 0)

type performanceOutputTranscript struct {
	ExitCode     int             `json:"exitCode"`
	Diagnostics  json.RawMessage `json:"diagnostics"`
	EmittedFiles []string        `json:"emittedFiles"`
}

type performanceOutputCommandResult struct {
	ExitCode    int
	Diagnostics []byte
	Stderr      []byte
}

func stagePerformanceOutputFixture(t *testing.T) (string, performanceOutputTranscript, map[string][]byte) {
	t.Helper()
	root := performanceFixtureRoot(t)
	fixture := t.TempDir()
	copyPerformanceTree(t, filepath.Join(root, "tree"), fixture)
	copyPerformanceTree(t, filepath.Join(root, "support", "globals"), filepath.Join(fixture, "node_modules", "@rbxts", "globals"))
	if err := os.Chtimes(filepath.Join(fixture, "src"), performanceFixtureTime, performanceFixtureTime); err != nil {
		t.Fatal(err)
	}
	transcriptBytes, err := os.ReadFile(filepath.Join(root, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var transcript performanceOutputTranscript
	if err := json.Unmarshal(transcriptBytes, &transcript); err != nil {
		t.Fatal(err)
	}
	if transcript.Diagnostics == nil {
		t.Fatal("transcript diagnostics are missing")
	}
	return fixture, transcript, readPerformanceTree(t, filepath.Join(root, "golden", "tree"))
}

func runPerformanceOutputBinary(t *testing.T, fixture string) (performanceOutputCommandResult, error) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "rotor")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/rotor")
	build.Dir = performanceRepoRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build candidate binary: %v\n%s", err, output)
	}
	command := exec.Command(binary, "build", "--json", "--project", fixture)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := performanceOutputCommandResult{
		ExitCode: commandExitCode(err),
		Stderr:   stderr.Bytes(),
	}
	var response struct {
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &response); decodeErr != nil {
		return result, fmt.Errorf("decode candidate JSON output: %w\n%s", decodeErr, stdout.Bytes())
	}
	result.Diagnostics = response.Diagnostics
	return result, err
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func performanceFixtureRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find performance parity test source")
	}
	return filepath.Join(filepath.Dir(sourceFile), "..", "..", "testdata", "forkparity", "project", "performance-output")
}

func performanceRepoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join(performanceFixtureRoot(t), "..", "..", "..", ".."))
}

func copyPerformanceTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(destination, relative), 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		output := filepath.Join(destination, relative)
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		return os.WriteFile(output, contents, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}

func readPerformanceTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = contents
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func collectPerformanceTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := readPerformanceTree(t, filepath.Join(root, "out"))
	withOutputPrefix := make(map[string][]byte, len(result))
	for path, contents := range result {
		withOutputPrefix["out/"+path] = bytes.ReplaceAll(contents, []byte(root), []byte(performanceOutputRoot))
	}
	return withOutputPrefix
}

func normalizePerformancePaths(paths []string, root string) []string {
	normalized := make([]string, len(paths))
	for index, path := range paths {
		normalized[index] = filepath.ToSlash(strings.ReplaceAll(path, root, performanceOutputRoot))
	}
	return normalized
}

func clonePerformanceTree(tree map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(tree))
	for path, contents := range tree {
		clone[path] = slices.Clone(contents)
	}
	return clone
}

func comparePerformanceTree(want, got map[string][]byte) error {
	paths := make([]string, 0, len(want)+len(got))
	seen := make(map[string]struct{}, len(want))
	for path := range want {
		paths = append(paths, path)
		seen[path] = struct{}{}
	}
	for path := range got {
		if _, exists := seen[path]; !exists {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		wantBytes, wantOK := want[path]
		gotBytes, gotOK := got[path]
		switch {
		case !wantOK:
			return fmt.Errorf("output path unexpectedly present path=%q", path)
		case !gotOK:
			return fmt.Errorf("output path missing path=%q", path)
		case !bytes.Equal(wantBytes, gotBytes):
			return performanceByteDiff(path, wantBytes, gotBytes)
		}
	}
	return nil
}

func performanceByteDiff(path string, want, got []byte) error {
	offset := 0
	for offset < len(want) && offset < len(got) && want[offset] == got[offset] {
		offset++
	}
	return fmt.Errorf("output bytes differ path=%q offset=%d want=%d got=%d", path, offset, byteAt(want, offset), byteAt(got, offset))
}

func byteAt(value []byte, offset int) int {
	if offset == len(value) {
		return -1
	}
	return int(value[offset])
}

func reorderPerformanceMapKeys(t *testing.T, value []byte) []byte {
	t.Helper()
	const before = `{"version":3,"file":`
	const after = `{"file":`
	if !bytes.HasPrefix(value, []byte(before)) {
		t.Fatalf("unexpected declaration map prefix: %q", value[:min(len(value), 64)])
	}
	fileEnd := bytes.IndexByte(value[len(after):], ',') + len(after)
	if fileEnd < len(after) {
		t.Fatal("declaration map lacks file key terminator")
	}
	fileValue := value[len(after):fileEnd]
	return append(append([]byte(`{"file":`), fileValue...), append([]byte(`,"version":3`), value[fileEnd+1:]...)...)
}
