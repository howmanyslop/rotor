package forkparity

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var compiledTimingLine = regexp.MustCompile(`(?mi)^(\s*compiled\s+\d+\s+files\s+in\s+)\d+(?:\.\d+)?(?:ns|us|µs|ms|s)([ \t]*\r?)$`)

// DiffReport describes all observable differences between two compiler runs.
type DiffReport struct {
	Match bool
	Diffs []FileDiff
}

// FileDiff identifies a single differing artifact using SHA-256 digests.
type FileDiff struct {
	Path    string
	ADigest string
	BDigest string
	Detail  string
}

// Normalize removes execution-environment variance from a compiler result.
func Normalize(result *RunResult, tempRoot string) {
	if result == nil {
		return
	}
	result.Stdout = normalizeText(result.Stdout, tempRoot)
	result.Stderr = normalizeText(result.Stderr, tempRoot)

	normalizedTree := make(map[string][]byte, len(result.OutputTree))
	for path, contents := range result.OutputTree {
		normalizedPath := normalizePath(path, tempRoot)
		normalizedTree[normalizedPath] = []byte(normalizeText(string(contents), tempRoot))
	}
	result.OutputTree = normalizedTree
}

// Compare reports byte-level differences between two normalized compiler runs.
func Compare(a, b *RunResult) (*DiffReport, error) {
	if a == nil || b == nil {
		return nil, fmt.Errorf("compare run results: both results are required")
	}

	report := &DiffReport{
		Match: true,
		Diffs: []FileDiff{},
	}
	if a.ExitCode != b.ExitCode {
		report.Diffs = append(report.Diffs, newFileDiff(
			"<exit-code>",
			[]byte(strconv.Itoa(a.ExitCode)),
			[]byte(strconv.Itoa(b.ExitCode)),
			"exit codes differ",
		))
	}
	if a.Stdout != b.Stdout {
		report.Diffs = append(report.Diffs, newFileDiff(
			"<stdout>",
			[]byte(a.Stdout),
			[]byte(b.Stdout),
			"stdout differs",
		))
	}
	if a.Stderr != b.Stderr {
		report.Diffs = append(report.Diffs, newFileDiff(
			"<stderr>",
			[]byte(a.Stderr),
			[]byte(b.Stderr),
			"stderr differs",
		))
	}

	for _, path := range outputPathsForComparison(a.OutputTree, b.OutputTree) {
		aContents, aExists := a.OutputTree[path]
		bContents, bExists := b.OutputTree[path]
		switch {
		case !aExists:
			report.Diffs = append(report.Diffs, newMissingFileDiff(path, bContents, false))
		case !bExists:
			report.Diffs = append(report.Diffs, newMissingFileDiff(path, aContents, true))
		case !bytes.Equal(aContents, bContents):
			report.Diffs = append(report.Diffs, newFileDiff(path, aContents, bContents, "contents differ"))
		}
	}
	report.Match = len(report.Diffs) == 0
	return report, nil
}

func normalizeText(value, tempRoot string) string {
	value = normalizePath(value, tempRoot)
	return compiledTimingLine.ReplaceAllString(value, "${1}<TIME>${2}")
}

func normalizePath(value, tempRoot string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	tempRoot = strings.ReplaceAll(tempRoot, `\`, "/")
	if tempRoot != "" {
		value = strings.ReplaceAll(value, tempRoot, "<TEMP_ROOT>")
	}
	return value
}

func outputPathsForComparison(a, b map[string][]byte) []string {
	paths := make([]string, 0, len(a)+len(b))
	seen := make(map[string]struct{}, len(a)+len(b))
	for path := range a {
		paths = append(paths, path)
		seen[path] = struct{}{}
	}
	for path := range b {
		if _, exists := seen[path]; !exists {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func newFileDiff(path string, a, b []byte, detail string) FileDiff {
	return FileDiff{
		Path:    path,
		ADigest: digest(a),
		BDigest: digest(b),
		Detail:  detail,
	}
}

func newMissingFileDiff(path string, contents []byte, missingA bool) FileDiff {
	diff := FileDiff{
		Path:   path,
		Detail: "file exists only in result B",
	}
	if missingA {
		diff.ADigest = digest(contents)
		diff.BDigest = "<missing>"
		diff.Detail = "file exists only in result A"
		return diff
	}
	diff.ADigest = "<missing>"
	diff.BDigest = digest(contents)
	return diff
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return fmt.Sprintf("%x", sum)
}
