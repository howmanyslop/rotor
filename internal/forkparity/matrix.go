package forkparity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const forkRuntimeUnavailableReason = "zip lacks fork runtime deps"

type DivergenceClass string

const (
	DivergenceForkAuthoritative DivergenceClass = "fork-authoritative"
	DivergenceRbxtsc3Compatible DivergenceClass = "rbxtsc-3.0-compatible"
	DivergenceGoInapplicable    DivergenceClass = "go-inapplicable"
)

type MatrixSurface string

const (
	MatrixSurfaceByte        MatrixSurface = "byte"
	MatrixSurfaceCache       MatrixSurface = "cache"
	MatrixSurfaceDeclaration MatrixSurface = "declaration"
	MatrixSurfaceDiagnostic  MatrixSurface = "diagnostic"
	MatrixSurfaceExit        MatrixSurface = "exit-status"
	MatrixSurfacePlugin      MatrixSurface = "plugin-trace"
	MatrixSurfaceRuntime     MatrixSurface = "runtime-result"
	MatrixSurfaceSourceMap   MatrixSurface = "source-map"
	MatrixSurfaceWatchEvent  MatrixSurface = "watch-event"
	MatrixSurfaceWrite       MatrixSurface = "write-trace"
)

type DivergenceLedger struct {
	SchemaVersion int             `json:"schemaVersion"`
	ZipDigest     string          `json:"zipDigest"`
	Rows          []DivergenceRow `json:"rows"`
}

type DivergenceRow struct {
	ID                   string          `json:"id"`
	Classification       DivergenceClass `json:"classification"`
	Surface              MatrixSurface   `json:"surface"`
	Contract             string          `json:"contract"`
	Verification         string          `json:"verification"`
	ImplementationDetail string          `json:"implementationDetail,omitempty"`
	Reason               string          `json:"reason,omitempty"`
}

func ReadDivergenceLedger(path string) (*DivergenceLedger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read divergence ledger: %w", err)
	}

	var ledger DivergenceLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return nil, fmt.Errorf("parse divergence ledger: %w", err)
	}
	return &ledger, nil
}

func (l DivergenceLedger) Validate(expectedIDs []string) error {
	if l.SchemaVersion != 1 {
		return fmt.Errorf("ledger schema version = %d, want 1", l.SchemaVersion)
	}
	if l.ZipDigest != committedZipDigest {
		return fmt.Errorf("ledger zip digest = %q, want %q", l.ZipDigest, committedZipDigest)
	}

	expected := make(map[string]struct{}, len(expectedIDs))
	for _, id := range expectedIDs {
		expected[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(l.Rows))
	for _, row := range l.Rows {
		if row.ID == "" {
			return fmt.Errorf("ledger row has an empty id")
		}
		if _, exists := expected[row.ID]; !exists {
			return fmt.Errorf("ledger row %q does not identify a matrix case", row.ID)
		}
		if _, exists := seen[row.ID]; exists {
			return fmt.Errorf("ledger has duplicate row %q", row.ID)
		}
		seen[row.ID] = struct{}{}
		if !validDivergenceClass(row.Classification) {
			return fmt.Errorf("ledger row %q has invalid classification %q", row.ID, row.Classification)
		}
		if row.Surface == "" || row.Contract == "" || row.Verification == "" {
			return fmt.Errorf("ledger row %q is missing surface, contract, or verification", row.ID)
		}
		if row.Classification == DivergenceGoInapplicable {
			if row.ImplementationDetail == "" {
				return fmt.Errorf("inapplicable ledger row %q is missing implementation detail", row.ID)
			}
			if row.Reason == "" {
				return fmt.Errorf("inapplicable ledger row %q is missing reason", row.ID)
			}
		}
	}
	for id := range expected {
		if _, exists := seen[id]; !exists {
			return fmt.Errorf("matrix case %q is unclassified", id)
		}
	}
	return nil
}

func validDivergenceClass(classification DivergenceClass) bool {
	switch classification {
	case DivergenceForkAuthoritative, DivergenceRbxtsc3Compatible, DivergenceGoInapplicable:
		return true
	default:
		return false
	}
}

func matrixCaseIDs(transformerFixtures []TransformerFixture, projectFixtures []ProjectFixture) []string {
	ids := make([]string, 0, len(transformerFixtures)+len(projectFixtures)+8)
	for _, fixture := range transformerFixtures {
		ids = append(ids, "transformer/"+fixture.Name)
	}
	for _, fixture := range projectFixtures {
		ids = append(ids, "project/"+fixture.Name)
	}
	ids = append(ids,
		"oracle-execution/transformer-fixtures",
		"oracle-execution/project-fixtures",
		"runtime/lune-execution",
		"watch/chokidar-scheduling",
		"plugin/node-language-service-trace",
		"write/node-fs-call-order",
		"rbxtsc-3.0/differential-fixtures",
		"rbxtsc-3.0/conformance-fixtures",
	)
	return ids
}

type MatrixObservation struct {
	ExitCode        int
	Diagnostics     string
	Artifacts       map[string][]byte
	WriteTrace      []string
	PluginTrace     []string
	RuntimeResult   string
	WatchTranscript []string
}

type MatrixDrift struct {
	Surface        MatrixSurface `json:"surface"`
	Path           string        `json:"path,omitempty"`
	ExpectedDigest string        `json:"expectedDigest,omitempty"`
	ActualDigest   string        `json:"actualDigest,omitempty"`
	Detail         string        `json:"detail"`
}

func CompareMatrixObservations(want, got MatrixObservation) []MatrixDrift {
	drifts := []MatrixDrift{}
	if want.ExitCode != got.ExitCode {
		drifts = append(drifts, MatrixDrift{
			Surface: MatrixSurfaceExit,
			Detail:  fmt.Sprintf("exit code = %d, want %d", got.ExitCode, want.ExitCode),
		})
	}
	if want.Diagnostics != got.Diagnostics {
		drifts = append(drifts, matrixTextDrift(MatrixSurfaceDiagnostic, "diagnostics", want.Diagnostics, got.Diagnostics))
	}
	for _, path := range matrixArtifactPaths(want.Artifacts, got.Artifacts) {
		wantBytes, wantOK := want.Artifacts[path]
		gotBytes, gotOK := got.Artifacts[path]
		switch {
		case !wantOK:
			drifts = append(drifts, matrixArtifactDrift(path, nil, gotBytes, "artifact exists only in actual result"))
		case !gotOK:
			drifts = append(drifts, matrixArtifactDrift(path, wantBytes, nil, "artifact missing from actual result"))
		case !bytes.Equal(wantBytes, gotBytes):
			drifts = append(drifts, matrixArtifactDrift(path, wantBytes, gotBytes, "artifact bytes differ"))
		}
	}
	if !slices.Equal(want.WriteTrace, got.WriteTrace) {
		drifts = append(drifts, matrixSequenceDrift(MatrixSurfaceWrite, "write trace", want.WriteTrace, got.WriteTrace))
	}
	if !slices.Equal(want.PluginTrace, got.PluginTrace) {
		drifts = append(drifts, matrixSequenceDrift(MatrixSurfacePlugin, "plugin trace", want.PluginTrace, got.PluginTrace))
	}
	if want.RuntimeResult != got.RuntimeResult {
		drifts = append(drifts, matrixTextDrift(MatrixSurfaceRuntime, "runtime result", want.RuntimeResult, got.RuntimeResult))
	}
	if !slices.Equal(want.WatchTranscript, got.WatchTranscript) {
		drifts = append(drifts, matrixSequenceDrift(MatrixSurfaceWatchEvent, "watch transcript", want.WatchTranscript, got.WatchTranscript))
	}
	return drifts
}

func matrixArtifactPaths(want, got map[string][]byte) []string {
	paths := make([]string, 0, len(want)+len(got))
	seen := make(map[string]struct{}, len(want)+len(got))
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
	return paths
}

func matrixArtifactDrift(path string, want, got []byte, detail string) MatrixDrift {
	if want != nil && got != nil {
		detail += ": " + matrixFirstDifference(string(want), string(got))
	}
	return MatrixDrift{
		Surface:        surfaceForArtifact(path),
		Path:           filepath.ToSlash(path),
		ExpectedDigest: matrixDigest(want),
		ActualDigest:   matrixDigest(got),
		Detail:         detail,
	}
}

func matrixFirstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for index := range min(len(wantLines), len(gotLines)) {
		if wantLines[index] != gotLines[index] {
			return fmt.Sprintf("line %d got %q want %q", index+1, gotLines[index], wantLines[index])
		}
	}
	return fmt.Sprintf("line count got %d want %d", len(gotLines), len(wantLines))
}

func matrixTextDrift(surface MatrixSurface, path, want, got string) MatrixDrift {
	return MatrixDrift{
		Surface:        surface,
		Path:           path,
		ExpectedDigest: digest([]byte(want)),
		ActualDigest:   digest([]byte(got)),
		Detail:         path + " differs",
	}
}

func matrixSequenceDrift(surface MatrixSurface, path string, want, got []string) MatrixDrift {
	return matrixTextDrift(surface, path, strings.Join(want, "\n"), strings.Join(got, "\n"))
}

func surfaceForArtifact(path string) MatrixSurface {
	path = filepath.ToSlash(path)
	switch {
	case strings.HasSuffix(path, ".luau.map"):
		return MatrixSurfaceSourceMap
	case strings.HasSuffix(path, ".d.ts"):
		return MatrixSurfaceDeclaration
	case strings.Contains(path, "copyfiles") || strings.Contains(path, "rojocache") || strings.HasSuffix(path, ".tsbuildinfo"):
		return MatrixSurfaceCache
	default:
		return MatrixSurfaceByte
	}
}

func matrixDigest(contents []byte) string {
	if contents == nil {
		return "<missing>"
	}
	return digest(contents)
}
