package compile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPerformanceOutputByteParity(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()

	// Given
	fixture, transcript, golden := stagePerformanceOutputFixture(t)
	// Registered after staging so it runs before the fixture TempDir is
	// removed (the sidecar worker holds the project dir open on Windows).
	t.Cleanup(closeSidecarSessions)

	// When
	result, err := runPerformanceOutputBinary(t, fixture)

	// Then
	if result.ExitCode != transcript.ExitCode {
		t.Fatalf("exit code = %d, want %d (diagnostics: %s)", result.ExitCode, transcript.ExitCode, result.Diagnostics)
	}
	if err != nil {
		t.Fatalf("build: %v (stderr: %s)", err, result.Stderr)
	}
	if !bytes.Equal(result.Diagnostics, transcript.Diagnostics) {
		t.Fatalf("diagnostics = %s, want %s", result.Diagnostics, transcript.Diagnostics)
	}
	if err := comparePerformanceTree(golden, collectPerformanceTree(t, fixture)); err != nil {
		t.Fatal(err)
	}
}

func TestDeclarationEmitRemainsPerSource(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()

	// Given
	fixture, transcript, _ := stagePerformanceOutputFixture(t)
	t.Cleanup(closeSidecarSessions)

	// When
	result, diagnostics, err := BuildProjectWithOptions(fixture, ProjectOptions{})

	// Then
	if err != nil {
		t.Fatalf("build: %v (diagnostics: %v)", err, diagnostics)
	}
	if got := normalizePerformancePaths(result.EmittedFiles, fixture); !slices.Equal(got, transcript.EmittedFiles) {
		t.Fatalf("emitted files = %q, want %q", got, transcript.EmittedFiles)
	}
	declarationFiles := make([]string, 0, 2)
	for _, path := range transcript.EmittedFiles {
		if strings.HasSuffix(path, ".d.ts") {
			declarationFiles = append(declarationFiles, filepath.Base(path))
		}
	}
	for index, file := range declarationFiles {
		declaration, readErr := os.ReadFile(filepath.Join(fixture, "out", file))
		if readErr != nil {
			t.Fatalf("read declaration %q: %v", file, readErr)
		}
		marker := fmt.Sprintf("__DECLARATION_EMIT_%d__", index+1)
		if !bytes.Contains(declaration, []byte(marker)) {
			t.Fatalf("declaration %q does not contain %q:\n%s", file, marker, declaration)
		}
	}
	declarationEntries := make([]string, 0, 4)
	for _, path := range normalizePerformancePaths(result.EmittedFiles, fixture) {
		if strings.HasSuffix(path, ".d.ts") || strings.HasSuffix(path, ".d.ts.map") {
			declarationEntries = append(declarationEntries, path)
		}
	}
	want := transcript.EmittedFiles[len(transcript.EmittedFiles)-len(declarationEntries):]
	if !slices.Equal(declarationEntries, want) {
		t.Fatalf("declaration emitted files = %q, want %q", declarationEntries, want)
	}
}

func TestDeclarationMapByteParity(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()

	// Given
	fixture, _, golden := stagePerformanceOutputFixture(t)
	t.Cleanup(closeSidecarSessions)

	// When
	_, diagnostics, err := BuildProjectWithOptions(fixture, ProjectOptions{})

	// Then
	if err != nil {
		t.Fatalf("build: %v (diagnostics: %v)", err, diagnostics)
	}
	actual := collectPerformanceTree(t, fixture)
	for path, want := range golden {
		if !strings.HasSuffix(path, ".d.ts.map") {
			continue
		}
		if got := actual[path]; !bytes.Equal(got, want) {
			t.Fatal(performanceByteDiff(path, want, got))
		}
	}
}

func TestPerformanceOutputParityMutations(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()

	// Given
	fixture, _, golden := stagePerformanceOutputFixture(t)
	t.Cleanup(closeSidecarSessions)
	_, diagnostics, err := BuildProjectWithOptions(fixture, ProjectOptions{})
	if err != nil {
		t.Fatalf("build: %v (diagnostics: %v)", err, diagnostics)
	}
	actual := collectPerformanceTree(t, fixture)

	// When
	byteMutated := clonePerformanceTree(golden)
	byteMutated["out/alpha.d.ts"][0] ^= 1
	byteMutationErr := comparePerformanceTree(byteMutated, actual)
	mapMutated := clonePerformanceTree(golden)
	mapMutated["out/alpha.d.ts.map"] = reorderPerformanceMapKeys(t, mapMutated["out/alpha.d.ts.map"])
	mapMutationErr := comparePerformanceTree(mapMutated, actual)

	// Then
	if got, want := fmt.Sprint(byteMutationErr), `path="out/alpha.d.ts" offset=0`; !strings.Contains(got, want) {
		t.Fatalf("one-byte mutation diff = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(mapMutationErr), `path="out/alpha.d.ts.map" offset=2`; !strings.Contains(got, want) {
		t.Fatalf("map-key mutation diff = %q, want %q", got, want)
	}
}
