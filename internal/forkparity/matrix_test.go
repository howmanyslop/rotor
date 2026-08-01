package forkparity

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestDivergenceLedgerCoversMatrixCases(t *testing.T) {
	// Given
	ledgerPath := filepath.Join(repoRoot(t), "testdata", "forkparity", "divergence-ledger.json")

	// When
	ledger, err := ReadDivergenceLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if err := ledger.Validate(AllMatrixCaseIDs()); err != nil {
		t.Fatal(err)
	}
}

func TestDivergenceLedgerRejectsUnknownMatrixCase(t *testing.T) {
	// Given
	ledger := DivergenceLedger{
		SchemaVersion: 1,
		ZipDigest:     committedZipDigest,
		Rows: []DivergenceRow{{
			ID:             "unknown/case",
			Classification: DivergenceForkAuthoritative,
			Surface:        MatrixSurfaceByte,
			Contract:       "unknown fixture",
			Verification:   "not applicable",
		}},
	}

	// When
	err := ledger.Validate(nil)

	// Then
	if err == nil {
		t.Fatal("Validate() succeeded for an unknown matrix case")
	}
}

func TestCompareMatrixObservationsAttributesEveryDrift(t *testing.T) {
	// Given
	want := MatrixObservation{
		ExitCode:    0,
		Diagnostics: "TS1000: expected\n",
		Artifacts: map[string][]byte{
			"out/main.luau":                []byte("return 1\n"),
			"out/main.luau.map":            []byte(`{"mappings":"AAAA"}`),
			"out/rbxts.copyfiles.json":     []byte(`{"version":2}`),
			"out/cache.rbxtsc.tsbuildinfo": []byte(`{"version":1}`),
		},
		WriteTrace:      []string{"create out/main.luau"},
		PluginTrace:     []string{"before:main.ts", "after:main.ts"},
		RuntimeResult:   "42",
		WatchTranscript: []string{"invalidate shared", "drain shared,game"},
	}
	got := cloneMatrixObservation(want)
	got.Diagnostics = "TS1001: actual\n"
	got.Artifacts["out/main.luau.map"] = []byte(`{"mappings":"AAAB"}`)
	got.Artifacts["out/rbxts.copyfiles.json"] = []byte(`{"version":3}`)
	got.Artifacts["out/main.luau"] = []byte("return 2\n")
	got.WatchTranscript[1] = "drain shared"

	// When
	drifts := CompareMatrixObservations(want, got)

	// Then
	wantSurfaces := map[MatrixSurface]bool{
		MatrixSurfaceByte:       false,
		MatrixSurfaceDiagnostic: false,
		MatrixSurfaceSourceMap:  false,
		MatrixSurfaceCache:      false,
		MatrixSurfaceWatchEvent: false,
	}
	for _, drift := range drifts {
		if _, ok := wantSurfaces[drift.Surface]; ok {
			wantSurfaces[drift.Surface] = true
		}
	}
	for surface, found := range wantSurfaces {
		if !found {
			t.Errorf("drifts = %#v, want %s drift", drifts, surface)
		}
	}
}

func TestMatrixRowFromResultPreservesLedgerAttribution(t *testing.T) {
	// Given
	ledgerRow := DivergenceRow{
		ID:                   "runtime/lune-execution",
		Classification:       DivergenceGoInapplicable,
		Surface:              MatrixSurfaceRuntime,
		Contract:             "runtime output remains compatible",
		Verification:         "internal/runtime TestContract",
		ImplementationDetail: "external runtime scheduling",
		Reason:               "lune is unavailable",
	}

	// When
	reportRow := matrixRowFromResult(ledgerRow, matrixFixtureResult{})

	// Then
	if reportRow.Surface != ledgerRow.Surface {
		t.Fatalf("surface = %q, want %q", reportRow.Surface, ledgerRow.Surface)
	}
	if reportRow.Contract != ledgerRow.Contract {
		t.Fatalf("contract = %q, want %q", reportRow.Contract, ledgerRow.Contract)
	}
	if reportRow.Verification != ledgerRow.Verification {
		t.Fatalf("verification = %q, want %q", reportRow.Verification, ledgerRow.Verification)
	}
	if reportRow.ImplementationDetail != ledgerRow.ImplementationDetail {
		t.Fatalf("implementation detail = %q, want %q", reportRow.ImplementationDetail, ledgerRow.ImplementationDetail)
	}
	if reportRow.Reason != ledgerRow.Reason {
		t.Fatalf("reason = %q, want %q", reportRow.Reason, ledgerRow.Reason)
	}
}

func cloneMatrixObservation(observation MatrixObservation) MatrixObservation {
	clone := observation
	clone.Artifacts = make(map[string][]byte, len(observation.Artifacts))
	for path, contents := range observation.Artifacts {
		clone.Artifacts[path] = slices.Clone(contents)
	}
	clone.WriteTrace = slices.Clone(observation.WriteTrace)
	clone.PluginTrace = slices.Clone(observation.PluginTrace)
	clone.WatchTranscript = slices.Clone(observation.WatchTranscript)
	return clone
}
