package forkparity

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFullForkParity(t *testing.T) {
	// Given
	runner := MatrixRunner{
		RepoRoot:  repoRoot(t),
		ReportDir: t.ArtifactDir(),
	}

	// When
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if !report.ArchiveVerified {
		t.Fatal("matrix report did not verify the committed archive")
	}
	if !report.ZeroDrift {
		t.Fatalf("full fork parity drift:\n%s", report.String())
	}
	transformerFixtures, err := LoadTransformerFixtures(runner.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	projectFixtures, err := LoadProjectFixtures(runner.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	caseIDs := matrixCaseIDs(transformerFixtures, projectFixtures)
	if len(report.Rows) != len(caseIDs) {
		t.Fatalf("matrix report rows = %d, want %d", len(report.Rows), len(caseIDs))
	}
	for _, row := range report.Rows {
		if row.Surface == "" || row.Contract == "" || row.Verification == "" {
			t.Fatalf("matrix report row %q is missing ledger attribution: %#v", row.ID, row)
		}
		if row.Classification == DivergenceGoInapplicable && (row.ImplementationDetail == "" || row.Reason == "") {
			t.Fatalf("inapplicable matrix report row %q is missing implementation detail or reason: %#v", row.ID, row)
		}
	}
	reportPath := filepath.Join(t.ArtifactDir(), "fork-parity-report.json")
	if err := WriteMatrixReport(reportPath, report); err != nil {
		t.Fatal(err)
	}
	t.Logf("zero-drift matrix report: %s", reportPath)
}
