package forkparity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

type MatrixRunner struct {
	RepoRoot     string
	ReportDir    string
	RotorBinPath string
}

type MatrixReport struct {
	SchemaVersion         int               `json:"schemaVersion"`
	ZipDigest             string            `json:"zipDigest"`
	ArchiveVerified       bool              `json:"archiveVerified"`
	ForkExecutable        bool              `json:"forkExecutable"`
	ForkUnavailableReason string            `json:"forkUnavailableReason,omitempty"`
	Rows                  []MatrixRowResult `json:"rows"`
	ZeroDrift             bool              `json:"zeroDrift"`
}

type MatrixRowResult struct {
	ID                   string            `json:"id"`
	Classification       DivergenceClass   `json:"classification"`
	Surface              MatrixSurface     `json:"surface"`
	Contract             string            `json:"contract"`
	Verification         string            `json:"verification"`
	ImplementationDetail string            `json:"implementationDetail,omitempty"`
	Reason               string            `json:"reason,omitempty"`
	Status               string            `json:"status"`
	Artifacts            map[string]string `json:"artifacts,omitempty"`
	Drifts               []MatrixDrift     `json:"drifts"`
}

func (r MatrixRunner) Run(ctx context.Context) (MatrixReport, error) {
	transformerFixtures, err := LoadTransformerFixtures(r.RepoRoot)
	if err != nil {
		return MatrixReport{}, err
	}
	projectFixtures, err := LoadProjectFixtures(r.RepoRoot)
	if err != nil {
		return MatrixReport{}, err
	}
	caseIDs := matrixCaseIDs(transformerFixtures, projectFixtures)
	ledger, err := ReadDivergenceLedger(filepath.Join(r.RepoRoot, "testdata", "forkparity", "divergence-ledger.json"))
	if err != nil {
		return MatrixReport{}, err
	}
	if err := ledger.Validate(caseIDs); err != nil {
		return MatrixReport{}, fmt.Errorf("validate divergence ledger: %w", err)
	}

	zipPath, err := FindZip(r.RepoRoot)
	if err != nil {
		return MatrixReport{}, err
	}
	extractDir, cleanup, err := VerifyAndExtract(zipPath)
	if err != nil {
		return MatrixReport{}, err
	}
	defer cleanup()

	rotorBin, err := r.rotorBinary()
	if err != nil {
		return MatrixReport{}, err
	}
	projectNodeModules, err := r.rotorNodeModules()
	if err != nil {
		return MatrixReport{}, err
	}
	report := MatrixReport{
		SchemaVersion:   1,
		ZipDigest:       committedZipDigest,
		ArchiveVerified: true,
		ForkExecutable:  forkRuntimeDependenciesAvailable(extractDir),
		Rows:            make([]MatrixRowResult, 0, len(caseIDs)),
	}
	if !report.ForkExecutable {
		report.ForkUnavailableReason = forkRuntimeUnavailableReason
	}

	rowsByID := ledgerRowsByID(ledger.Rows)
	for _, fixture := range transformerFixtures {
		result, err := r.runTransformerFixture(ctx, rotorBin, projectNodeModules, fixture)
		if err != nil {
			return MatrixReport{}, fmt.Errorf("run transformer fixture %q: %w", fixture.Name, err)
		}
		report.Rows = append(report.Rows, matrixRowFromResult(rowsByID["transformer/"+fixture.Name], result))
	}
	for _, fixture := range projectFixtures {
		result, err := r.runProjectFixture(ctx, rotorBin, projectNodeModules, fixture)
		if err != nil {
			return MatrixReport{}, fmt.Errorf("run project fixture %q: %w", fixture.Name, err)
		}
		report.Rows = append(report.Rows, matrixRowFromResult(rowsByID["project/"+fixture.Name], result))
	}
	for _, id := range caseIDs {
		row := rowsByID[id]
		if row.Classification == DivergenceForkAuthoritative {
			continue
		}
		report.Rows = append(report.Rows, matrixReferenceRow(row))
	}
	report.ZeroDrift = matrixRowsMatch(report.Rows)
	return report, nil
}

func (r MatrixRunner) rotorBinary() (string, error) {
	if r.RotorBinPath != "" {
		return r.RotorBinPath, nil
	}
	if r.ReportDir == "" {
		return "", fmt.Errorf("matrix report directory is required when Rotor binary is not supplied")
	}
	if err := os.MkdirAll(r.ReportDir, 0o755); err != nil {
		return "", fmt.Errorf("create matrix report directory: %w", err)
	}
	binPath := filepath.Join(r.ReportDir, "rotor")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/rotor")
	cmd.Dir = r.RepoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build Rotor binary: %w\n%s", err, output)
	}
	return binPath, nil
}

func (r MatrixRunner) rotorNodeModules() (string, error) {
	path := filepath.Join(r.RepoRoot, "testdata", "transformers", "project", "node_modules")
	if _, err := os.Stat(filepath.Join(path, "typescript", "package.json")); err != nil {
		return "", fmt.Errorf("read matrix Node dependencies: %w", err)
	}
	return path, nil
}

func WriteMatrixReport(path string, report MatrixReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal matrix report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create matrix report parent: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write matrix report: %w", err)
	}
	return nil
}

func (r MatrixReport) String() string {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshal matrix report: %v", err)
	}
	return string(data)
}

func forkRuntimeDependenciesAvailable(extractDir string) bool {
	return slices.ContainsFunc([]string{
		filepath.Join(extractDir, "node_modules"),
		filepath.Join(extractDir, "roblox-ts", "node_modules"),
	}, matrixHasForkDependencies)
}

func matrixHasForkDependencies(nodeModules string) bool {
	for _, dependency := range []string{
		"arktype/package.json",
		"chokidar/package.json",
		"fs-extra/package.json",
		"kleur/package.json",
		"resolve/package.json",
		"typescript/package.json",
		"yargs/package.json",
		"@jridgewell/gen-mapping/package.json",
		"@jridgewell/trace-mapping/package.json",
		"@rbxts/compiler-types/package.json",
		"@rbxts/types/package.json",
		"@roblox-ts/luau-ast/package.json",
		"@roblox-ts/path-translator/package.json",
	} {
		if _, err := os.Stat(filepath.Join(nodeModules, filepath.FromSlash(dependency))); err != nil {
			return false
		}
	}
	return true
}

func ledgerRowsByID(rows []DivergenceRow) map[string]DivergenceRow {
	byID := make(map[string]DivergenceRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	return byID
}

func matrixRowsMatch(rows []MatrixRowResult) bool {
	for _, row := range rows {
		if len(row.Drifts) > 0 {
			return false
		}
	}
	return true
}

func matrixRowFromResult(row DivergenceRow, result matrixFixtureResult) MatrixRowResult {
	return MatrixRowResult{
		ID:                   row.ID,
		Classification:       row.Classification,
		Surface:              row.Surface,
		Contract:             row.Contract,
		Verification:         row.Verification,
		ImplementationDetail: row.ImplementationDetail,
		Reason:               row.Reason,
		Status:               matrixResultStatus(result.Drifts),
		Artifacts:            matrixArtifactDigests(result.Artifacts),
		Drifts:               result.Drifts,
	}
}

func matrixReferenceRow(row DivergenceRow) MatrixRowResult {
	return MatrixRowResult{
		ID:                   row.ID,
		Classification:       row.Classification,
		Surface:              row.Surface,
		Contract:             row.Contract,
		Verification:         row.Verification,
		ImplementationDetail: row.ImplementationDetail,
		Reason:               row.Reason,
		Status:               string(row.Classification),
		Artifacts:            map[string]string{},
		Drifts:               []MatrixDrift{},
	}
}

func matrixResultStatus(drifts []MatrixDrift) string {
	if len(drifts) == 0 {
		return "matched"
	}
	return "drift"
}

func matrixArtifactDigests(artifacts map[string][]byte) map[string]string {
	digests := make(map[string]string, len(artifacts))
	for path, contents := range artifacts {
		normalizedPath, artifactDigest := matrixArtifactDigest(path, contents)
		digests[normalizedPath] = artifactDigest
	}
	return digests
}
