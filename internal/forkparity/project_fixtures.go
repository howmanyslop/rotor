package forkparity

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ProjectFixture is a self-contained project used to compare Rotor with the
// archived fork contract.
type ProjectFixture struct {
	Name             string
	Description      string
	Category         string
	Files            map[string]string
	ExpectedExitCode int
	ExpectedStdout   string
	ExpectedStderr   string
	ArtifactChecks   ProjectArtifactChecks
	Invocations      []FixtureInvocation
}

// ProjectArtifactChecks is the materialized artifact contract for one project
// fixture transcript.
type ProjectArtifactChecks struct {
	RequireLuau        bool                `json:"requireLuau"`
	RequiredSuffixes   []string            `json:"requiredSuffixes"`
	RequiredComponents []string            `json:"requiredComponents"`
	RequiredTexts      []ArtifactTextCheck `json:"requiredTexts"`
	ForbiddenTexts     []ArtifactTextCheck `json:"forbiddenTexts"`
	RequireSourceMap   bool                `json:"requireSourceMap"`
	ExpectedWriteTrace []string            `json:"expectedWriteTrace"`
}

// ArtifactTextCheck asserts text in an artifact identified by its suffix.
type ArtifactTextCheck struct {
	Suffix string `json:"suffix"`
	Text   string `json:"text"`
}

// FixtureInvocation describes one archive-captured CLI invocation in a project
// fixture transcript.
type FixtureInvocation struct {
	Name      string   `json:"name"`
	Arguments []string `json:"arguments"`
}

type projectFixtureManifest struct {
	Fixtures []string `json:"fixtures"`
}

type projectFixtureTranscript struct {
	Description      string                `json:"description"`
	Category         string                `json:"category"`
	ExpectedExitCode int                   `json:"expectedExitCode"`
	ExpectedStdout   string                `json:"expectedStdout"`
	ExpectedStderr   string                `json:"expectedStderr"`
	ArtifactChecks   ProjectArtifactChecks `json:"artifactChecks"`
	Invocations      []FixtureInvocation   `json:"invocations"`
}

// LoadProjectFixtures reads the materialized project trees and their
// archive-captured transcripts in stable order.
func LoadProjectFixtures(repoRoot string) ([]ProjectFixture, error) {
	dir := filepath.Join(repoRoot, "testdata", "forkparity", "project")
	var manifest projectFixtureManifest
	if err := readFixtureJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return nil, fmt.Errorf("read project fixture manifest: %w", err)
	}

	fixtures := make([]ProjectFixture, 0, len(manifest.Fixtures))
	for _, name := range manifest.Fixtures {
		fixtureDir := filepath.Join(dir, name)
		var transcript projectFixtureTranscript
		if err := readFixtureJSON(filepath.Join(fixtureDir, "transcript.json"), &transcript); err != nil {
			return nil, fmt.Errorf("read project fixture %q transcript: %w", name, err)
		}
		files, err := readProjectFixtureTree(filepath.Join(fixtureDir, "tree"))
		if err != nil {
			return nil, fmt.Errorf("read project fixture %q tree: %w", name, err)
		}
		fixtures = append(fixtures, ProjectFixture{
			Name:             name,
			Description:      transcript.Description,
			Category:         transcript.Category,
			Files:            files,
			ExpectedExitCode: transcript.ExpectedExitCode,
			ExpectedStdout:   transcript.ExpectedStdout,
			ExpectedStderr:   transcript.ExpectedStderr,
			ArtifactChecks:   transcript.ArtifactChecks,
			Invocations:      transcript.Invocations,
		})
	}
	return fixtures, nil
}

// ProjectFixtureProvenance reads the capture provenance beside the materialized
// project fixture corpus.
func ProjectFixtureProvenance(repoRoot string) (FixtureProvenance, error) {
	return readFixtureProvenance(repoRoot, "project")
}

func readProjectFixtureTree(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("find fixture-relative path: %w", err)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read fixture file %q: %w", rel, err)
		}
		files[filepath.ToSlash(rel)] = string(contents)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
