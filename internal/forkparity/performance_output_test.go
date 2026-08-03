package forkparity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPerformanceOutputFixture(t *testing.T) {
	// Given
	root := filepath.Join(repoRoot(t), "testdata", "forkparity", "project", "performance-output")
	transcriptBytes, err := os.ReadFile(filepath.Join(root, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var transcript struct {
		ExitCode      int             `json:"exitCode"`
		Diagnostics   json.RawMessage `json:"diagnostics"`
		ArtifactPaths []string        `json:"artifactPaths"`
		EmittedFiles  []string        `json:"emittedFiles"`
	}
	if err := json.Unmarshal(transcriptBytes, &transcript); err != nil {
		t.Fatal(err)
	}
	provenanceBytes, err := os.ReadFile(filepath.Join(root, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		BaselineCommit                  string `json:"baselineCommit"`
		CompilerVersion                 string `json:"compilerVersion"`
		ZipDigest                       string `json:"zipDigest"`
		BaselineCaptureCommand          string `json:"baselineCaptureCommand"`
		DirectArchiveExecutionAvailable bool   `json:"directArchiveExecutionAvailable"`
	}
	if err := json.Unmarshal(provenanceBytes, &provenance); err != nil {
		t.Fatal(err)
	}

	// When
	artifacts, err := collectOutputTree(filepath.Join(root, "golden", "tree"))
	if err != nil {
		t.Fatal(err)
	}
	zipPath, err := FindZip(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	extractDir, cleanup, err := VerifyAndExtract(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// Then
	if provenance.BaselineCommit != "11d0bc2" {
		t.Fatalf("baseline commit = %q, want 11d0bc2", provenance.BaselineCommit)
	}
	if provenance.CompilerVersion != "@isentinel/roblox-ts@4.0.11" || provenance.ZipDigest != committedZipDigest {
		t.Fatalf("unexpected provenance: %+v", provenance)
	}
	if provenance.BaselineCaptureCommand == "" {
		t.Fatal("baseline capture command is empty")
	}
	if got := forkRuntimeDependenciesAvailable(extractDir); got != provenance.DirectArchiveExecutionAvailable {
		t.Fatalf("direct archive availability = %t, want provenance %t", got, provenance.DirectArchiveExecutionAvailable)
	}
	if transcript.ExitCode != 0 || !bytes.Equal(transcript.Diagnostics, []byte("[]")) {
		t.Fatalf("transcript result = exit %d diagnostics %s, want successful clean build", transcript.ExitCode, transcript.Diagnostics)
	}
	if !slices.Equal(matrixArtifactPaths(artifacts, nil), transcript.ArtifactPaths) {
		t.Fatalf("golden paths = %q, want %q", matrixArtifactPaths(artifacts, nil), transcript.ArtifactPaths)
	}
	if len(transcript.EmittedFiles) != 6 {
		t.Fatalf("emitted files = %q, want ordered compiled and declaration entries", transcript.EmittedFiles)
	}
	for _, suffix := range []string{".luau", ".luau.map", ".d.ts", ".d.ts.map", "rbxts.copyfiles.json"} {
		if !hasArtifactSuffix(artifacts, suffix) {
			t.Fatalf("golden artifacts lack %q: %q", suffix, matrixArtifactPaths(artifacts, nil))
		}
	}
	alpha, err := os.ReadFile(filepath.Join(root, "tree", "src", "alpha.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(alpha, []byte{0xef, 0xbb, 0xbf}) || !bytes.Contains(alpha, []byte("\r\n")) {
		t.Fatal("alpha.ts must preserve its UTF-8 BOM and CRLF input")
	}
	beta, err := os.ReadFile(filepath.Join(root, "tree", "src", "beta.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(beta, []byte("\r\n")) {
		t.Fatal("beta.ts must preserve CRLF input")
	}
}
