package forkparity

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const matrixTSConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "downlevelIteration": true,
    "module": "commonjs",
    "moduleDetection": "force",
    "moduleResolution": "Node",
    "noLib": true,
    "outDir": "out",
    "rootDir": "src",
    "strict": true,
    "target": "ESNext",
    "typeRoots": ["node_modules/@rbxts"]
  },
  "include": ["src"]
}
`

var matrixANSISequence = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type matrixFixtureResult struct {
	Artifacts map[string][]byte
	Drifts    []MatrixDrift
}

type matrixStage struct {
	dir string
}

func (r MatrixRunner) runTransformerFixture(
	ctx context.Context,
	rotorBin string,
	nodeModules string,
	fixture TransformerFixture,
) (matrixFixtureResult, error) {
	stage, err := stageMatrixFixture(matrixTransformerFiles(fixture), nodeModules)
	if err != nil {
		return matrixFixtureResult{}, err
	}
	defer os.RemoveAll(stage.dir)

	runner := Runner{RotorBinPath: rotorBin}
	result, err := runner.RunRotor(ctx, rotorBin, stage.dir, filepath.Join(stage.dir, "out"))
	if err != nil {
		return matrixFixtureResult{}, err
	}
	artifacts, err := collectMatrixArtifacts(stage.dir)
	if err != nil {
		return matrixFixtureResult{}, err
	}
	return matrixFixtureResult{
		Artifacts: matrixNormalizeArtifacts(artifacts, stage.dir),
		Drifts:    transformerFixtureDrifts(fixture, result),
	}, nil
}

func (r MatrixRunner) runProjectFixture(
	ctx context.Context,
	rotorBin string,
	nodeModules string,
	fixture ProjectFixture,
) (matrixFixtureResult, error) {
	stage, err := stageMatrixFixture(fixture.Files, nodeModules)
	if err != nil {
		return matrixFixtureResult{}, err
	}
	defer os.RemoveAll(stage.dir)

	runner := Runner{RotorBinPath: rotorBin}
	run := RotorRun{
		Binary:     rotorBin,
		FixtureDir: stage.dir,
		OutputDir:  filepath.Join(stage.dir, "out"),
		Args:       matrixProjectArgs(fixture),
	}
	result, err := runner.RunRotorWithArgs(ctx, run)
	if err != nil {
		return matrixFixtureResult{}, err
	}
	artifacts, err := collectMatrixArtifacts(stage.dir)
	if err != nil {
		return matrixFixtureResult{}, err
	}
	drifts := projectFixtureDrifts(fixture, result, artifacts)
	if fixture.Name == "build-no-change" && len(drifts) == 0 {
		second, err := runner.RunRotorWithArgs(ctx, run)
		if err != nil {
			return matrixFixtureResult{}, err
		}
		afterSecond, err := collectMatrixArtifacts(stage.dir)
		if err != nil {
			return matrixFixtureResult{}, err
		}
		trace := matrixWriteTrace(artifacts, afterSecond)
		if second.ExitCode != fixture.ExpectedExitCode {
			drifts = append(drifts, matrixExitDrift(fixture.ExpectedExitCode, second.ExitCode))
		}
		if !slices.Equal(trace, fixture.ArtifactChecks.ExpectedWriteTrace) {
			drifts = append(drifts, matrixSequenceDrift(MatrixSurfaceWrite, "no-change write trace", fixture.ArtifactChecks.ExpectedWriteTrace, trace))
		}
		artifacts = afterSecond
	}
	return matrixFixtureResult{Artifacts: matrixNormalizeArtifacts(artifacts, stage.dir), Drifts: drifts}, nil
}

func matrixTransformerFiles(fixture TransformerFixture) map[string]string {
	files := map[string]string{
		"package.json":  `{"name":"@forkparity/transformer"}` + "\n",
		"tsconfig.json": matrixTSConfig,
		"src/main.ts":   fixture.TSCode,
	}
	if fixture.Name == "shared-table-iteration" {
		files["src/sharedtable.d.ts"] = "interface SharedTable extends Iterable<[string | number, unknown]> {}\n"
	}
	if fixture.Name == "range-literal-step" {
		files["src/globals.d.ts"] = "declare function print(...args: unknown[]): void;\n"
	}
	return files
}

func stageMatrixFixture(files map[string]string, nodeModules string) (matrixStage, error) {
	dir, err := os.MkdirTemp("", "forkparity-matrix-*")
	if err != nil {
		return matrixStage{}, fmt.Errorf("create matrix fixture: %w", err)
	}
	stage := matrixStage{dir: dir}
	if err := writeMatrixFiles(stage.dir, files); err != nil {
		_ = os.RemoveAll(stage.dir)
		return matrixStage{}, err
	}
	if err := linkMatrixNodeModules(stage.dir, files, nodeModules); err != nil {
		_ = os.RemoveAll(stage.dir)
		return matrixStage{}, err
	}
	return stage, nil
}

func linkMatrixNodeModules(dir string, files map[string]string, nodeModules string) error {
	dirs := map[string]struct{}{dir: {}}
	for path := range files {
		if filepath.Base(path) == "tsconfig.json" {
			dirs[filepath.Join(dir, filepath.Dir(filepath.FromSlash(path)))] = struct{}{}
		}
	}
	for projectDir := range dirs {
		link := filepath.Join(projectDir, "node_modules")
		if err := os.Symlink(nodeModules, link); err != nil {
			return fmt.Errorf("link matrix Node dependencies at %q: %w", projectDir, err)
		}
	}
	return nil
}

func writeMatrixFiles(dir string, files map[string]string) error {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if !matrixFixturePathValid(path) {
			return fmt.Errorf("invalid matrix fixture path %q", path)
		}
		fullPath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("create fixture parent %q: %w", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(files[path]), 0o644); err != nil {
			return fmt.Errorf("write fixture file %q: %w", path, err)
		}
	}
	return nil
}

func matrixFixturePathValid(path string) bool {
	clean := filepath.Clean(filepath.FromSlash(path))
	return !filepath.IsAbs(clean) && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func collectMatrixArtifacts(projectDir string) (map[string][]byte, error) {
	artifacts := map[string][]byte{}
	err := filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			return err
		}
		if !matrixArtifactPath(filepath.ToSlash(rel)) {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		artifacts[filepath.ToSlash(rel)] = contents
		return nil
	})
	if err != nil {
		return nil, err
	}
	return artifacts, nil
}

func matrixArtifactPath(path string) bool {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if part == "out" || part == "include" {
			return true
		}
		if part == ".rotor" && index+1 < len(parts) && parts[index+1] == "cache" {
			return true
		}
	}
	return false
}

func matrixWriteTrace(before, after map[string][]byte) []string {
	trace := []string{}
	for _, path := range matrixArtifactPaths(before, after) {
		beforeBytes, beforeOK := before[path]
		afterBytes, afterOK := after[path]
		switch {
		case !beforeOK && afterOK:
			trace = append(trace, "create "+path)
		case beforeOK && !afterOK:
			trace = append(trace, "remove "+path)
		case !bytes.Equal(beforeBytes, afterBytes):
			trace = append(trace, "update "+path)
		}
	}
	return trace
}

func matrixProjectArgs(fixture ProjectFixture) []string {
	for _, invocation := range fixture.Invocations {
		if slices.Contains(invocation.Arguments, "--build") {
			return []string{"--build"}
		}
	}
	if strings.Contains(fixture.Files["tsconfig.json"], `"references"`) {
		return []string{"--build"}
	}
	return []string{}
}
