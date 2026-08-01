package forkparity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TransformerFixture is one self-contained TypeScript semantic probe and its
// archive-authoritative compiler result.
type TransformerFixture struct {
	Name         string
	TSCode       string
	ExpectedLuau string
	Diagnostics  []string
	Category     string
}

// FixtureProvenance identifies the immutable compiler input used for fixtures.
type FixtureProvenance struct {
	ZipDigest       string `json:"zipDigest"`
	CaptureCommand  string `json:"captureCommand"`
	CompilerVersion string `json:"compilerVersion"`
}

type transformerFixtureManifest struct {
	Fixtures []transformerFixtureEntry `json:"fixtures"`
}

type transformerFixtureEntry struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Diagnostics []string `json:"diagnostics"`
}

// LoadTransformerFixtures reads the materialized, archive-captured transformer
// fixtures in their deterministic compiler-feature order.
func LoadTransformerFixtures(repoRoot string) ([]TransformerFixture, error) {
	dir := filepath.Join(repoRoot, "testdata", "forkparity", "transformer")
	var manifest transformerFixtureManifest
	if err := readFixtureJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return nil, fmt.Errorf("read transformer fixture manifest: %w", err)
	}

	fixtures := make([]TransformerFixture, 0, len(manifest.Fixtures))
	for _, entry := range manifest.Fixtures {
		tsCode, err := os.ReadFile(filepath.Join(dir, entry.Name+".ts"))
		if err != nil {
			return nil, fmt.Errorf("read transformer fixture %q TypeScript: %w", entry.Name, err)
		}
		expectedLuau, err := os.ReadFile(filepath.Join(dir, entry.Name+".luau"))
		if err != nil {
			return nil, fmt.Errorf("read transformer fixture %q Luau: %w", entry.Name, err)
		}
		fixtures = append(fixtures, TransformerFixture{
			Name:         entry.Name,
			TSCode:       string(tsCode),
			ExpectedLuau: string(expectedLuau),
			Diagnostics:  entry.Diagnostics,
			Category:     entry.Category,
		})
	}
	return fixtures, nil
}

// TransformerFixtureProvenance reads the capture provenance beside the
// materialized transformer fixtures.
func TransformerFixtureProvenance(repoRoot string) (FixtureProvenance, error) {
	return readFixtureProvenance(repoRoot, "transformer")
}

func readFixtureProvenance(repoRoot, fixtureKind string) (FixtureProvenance, error) {
	var provenance FixtureProvenance
	path := filepath.Join(repoRoot, "testdata", "forkparity", fixtureKind, "provenance.json")
	if err := readFixtureJSON(path, &provenance); err != nil {
		return FixtureProvenance{}, fmt.Errorf("read %s fixture provenance: %w", fixtureKind, err)
	}
	return provenance, nil
}

func readFixtureJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}
