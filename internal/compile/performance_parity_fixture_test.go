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
	"regexp"
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
		redacted := contents
		for _, spelling := range performanceRootSpellings(root) {
			redacted = bytes.ReplaceAll(redacted, []byte(spelling), []byte(performanceOutputRoot))
		}
		withOutputPrefix["out/"+path] = slashRedactedPerformancePaths(redacted)
	}
	return withOutputPrefix
}

// redactedPerformancePath matches the remainder of a path whose root redaction
// has already replaced, up to the first delimiter. It is anchored so only text
// directly following the placeholder can match, and the separator run is \\+
// because a path embedded in JSON (the copyfiles manifest cache key) carries
// escaped separators.
var redactedPerformancePath = regexp.MustCompile(`^(?:\\+[^"|\\\s]+)+`)

// slashRedactedPerformancePaths rewrites the separators inside already-redacted
// paths to forward slashes. The remaining segments come from filepath.Join, so
// they are native: the copyfiles manifest key is <ROOT>\out|<ROOT>\src on
// Windows and <ROOT>/out|<ROOT>/src everywhere else. Only text following the
// placeholder is touched, so genuine backslashes in emitted code survive.
func slashRedactedPerformancePaths(contents []byte) []byte {
	placeholder := []byte(performanceOutputRoot)
	if !bytes.Contains(contents, placeholder) {
		return contents
	}
	result := make([]byte, 0, len(contents))
	for {
		index := bytes.Index(contents, placeholder)
		if index < 0 {
			return append(result, contents...)
		}
		result = append(result, contents[:index+len(placeholder)]...)
		contents = contents[index+len(placeholder):]
		tail := redactedPerformancePath.Find(contents)
		if tail == nil {
			continue
		}
		result = append(result, bytes.ReplaceAll(bytes.ReplaceAll(tail, []byte(`\\`), []byte("/")), []byte(`\`), []byte("/"))...)
		contents = contents[len(tail):]
	}
}

func normalizePerformancePaths(paths []string, root string) []string {
	spellings := performanceRootSpellings(root)
	normalized := make([]string, len(paths))
	for index, path := range paths {
		redacted := path
		for _, spelling := range spellings {
			redacted = strings.ReplaceAll(redacted, spelling, performanceOutputRoot)
		}
		normalized[index] = filepath.ToSlash(redacted)
	}
	return normalized
}

// performanceRootSpellings returns every spelling of root that can reach
// emitted output, so redaction is separator-agnostic. Windows adds three
// wrinkles the native path alone misses: emitted paths use forward slashes
// while filepath.Join yields backslashes, TEMP resolves through 8.3 short
// names (RUNNER~1), and paths embedded in JSON (.map files) have their
// separators escaped. Longest first so a shorter spelling cannot shadow a
// longer match.
func performanceRootSpellings(root string) []string {
	candidates := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		candidates = append(candidates, resolved)
	}

	seen := make(map[string]struct{}, len(candidates)*3)
	spellings := make([]string, 0, len(candidates)*3)
	add := func(spelling string) {
		if spelling == "" {
			return
		}
		if _, duplicate := seen[spelling]; duplicate {
			return
		}
		seen[spelling] = struct{}{}
		spellings = append(spellings, spelling)
	}
	for _, candidate := range candidates {
		add(candidate)
		add(filepath.ToSlash(candidate))
		add(strings.ReplaceAll(candidate, `\`, `\\`))
	}
	slices.SortStableFunc(spellings, func(left, right string) int { return len(right) - len(left) })
	return spellings
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
