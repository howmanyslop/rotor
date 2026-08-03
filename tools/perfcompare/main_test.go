package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_writes_machine_readable_pass_and_fail_verdicts(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		wantCode int
		want     Status
	}{
		{
			name:     "passing manifest",
			manifest: passingManifest(),
			wantCode: 0,
			want:     StatusPass,
		},
		{
			name: "failing manifest",
			manifest: func() Manifest {
				manifest := passingManifest()
				setCandidateDuration(&manifest, PhaseCold, 900)
				manifest.Records[1].OutputTreeDigest = "changed"
				return manifest
			}(),
			wantCode: 1,
			want:     StatusFail,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			inputPath := filepath.Join(t.TempDir(), "manifest.json")
			outputPath := filepath.Join(t.TempDir(), "verdict.json")
			input, err := json.Marshal(test.manifest)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			if err := os.WriteFile(inputPath, input, 0o600); err != nil {
				t.Fatalf("write input: %v", err)
			}
			var stderr bytes.Buffer

			// When
			code := run([]string{"--input", inputPath, "--output", outputPath}, &stderr)

			// Then
			if code != test.wantCode {
				t.Errorf("exit code = %d, want %d; stderr = %s", code, test.wantCode, stderr.String())
			}
			output, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("read verdict: %v", err)
			}
			var verdict Verdict
			if err := json.Unmarshal(output, &verdict); err != nil {
				t.Fatalf("decode verdict: %v", err)
			}
			if verdict.Status != test.want {
				t.Errorf("verdict status = %q, want %q", verdict.Status, test.want)
			}
		})
	}
}

func TestRun_writes_structured_error_for_malformed_manifest(t *testing.T) {
	// Given
	inputPath := filepath.Join(t.TempDir(), "manifest.json")
	outputPath := filepath.Join(t.TempDir(), "verdict.json")
	if err := os.WriteFile(inputPath, []byte(`{"schema":"bad"}`), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	var stderr bytes.Buffer

	// When
	code := run([]string{"--input", inputPath, "--output", outputPath}, &stderr)

	// Then
	if code != 1 {
		t.Errorf("exit code = %d, want 1; stderr = %s", code, stderr.String())
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read verdict: %v", err)
	}
	var verdict Verdict
	if err := json.Unmarshal(output, &verdict); err != nil {
		t.Fatalf("decode verdict: %v", err)
	}
	if verdict.Status != StatusFail || !hasReason(verdict.Reasons, ReasonInvalidManifest) {
		t.Errorf("verdict = %#v, want structured invalid-manifest error", verdict)
	}
}
