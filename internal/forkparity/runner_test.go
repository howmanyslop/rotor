package forkparity

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerCapturesArtifacts(t *testing.T) {
	// Given
	root := repoRoot(t)
	zipPath, err := FindZip(root)
	if err != nil {
		t.Fatal(err)
	}
	extractDir, cleanup, err := VerifyAndExtract(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	nodeModules := forkNodeModules(t, extractDir)
	rotorBin := filepath.Join(t.TempDir(), "rotor")
	build := exec.Command("go", "build", "-o", rotorBin, "./cmd/rotor")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rotor: %v\n%s", err, output)
	}

	forkFixture := writeCompilerFixture(t, nodeModules)
	rotorFixture := writeCompilerFixture(t, nodeModules)
	runner := Runner{
		ForkCLIPath:  filepath.Join(extractDir, "roblox-ts", "dist", "CLI", "cli.cjs"),
		RotorBinPath: rotorBin,
	}

	// When
	forkResult, err := runner.RunFork(
		context.Background(),
		forkFixture,
		filepath.Join(forkFixture, "out"),
	)
	if err != nil {
		t.Fatal(err)
	}
	rotorResult, err := runner.RunRotor(
		context.Background(),
		rotorBin,
		rotorFixture,
		filepath.Join(rotorFixture, "out"),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if forkResult.ExitCode != 0 {
		t.Fatalf("fork exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", forkResult.ExitCode, forkResult.Stdout, forkResult.Stderr)
	}
	if rotorResult.ExitCode != 0 {
		t.Fatalf("rotor exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", rotorResult.ExitCode, rotorResult.Stdout, rotorResult.Stderr)
	}
	if !hasOutputWithSuffix(forkResult.OutputTree, ".luau") {
		t.Fatalf("fork output tree = %v, want a .luau artifact", outputPaths(forkResult.OutputTree))
	}
	if !hasOutputWithSuffix(rotorResult.OutputTree, ".luau") {
		t.Fatalf("rotor output tree = %v, want a .luau artifact", outputPaths(rotorResult.OutputTree))
	}
}

func TestNormalizerBoundaries(t *testing.T) {
	t.Parallel()

	// Given
	tempRoot := filepath.Join(t.TempDir(), "fixture")
	result := &RunResult{
		Stdout: tempRoot + "\\out\\main.luau\ncompiled 2 files in 123ms\n",
		Stderr: tempRoot + "\\include\\runtime.luau\ncompiled 1 files in 1.5s\n",
		OutputTree: map[string][]byte{
			"nested\\artifact.txt": []byte(tempRoot + "\\out\\nested\\artifact.txt\ncompiled 3 files in 42us\n"),
		},
	}

	// When
	Normalize(result, tempRoot)

	// Then
	if want := "<TEMP_ROOT>/out/main.luau\ncompiled 2 files in <TIME>\n"; result.Stdout != want {
		t.Fatalf("normalized stdout = %q, want %q", result.Stdout, want)
	}
	if want := "<TEMP_ROOT>/include/runtime.luau\ncompiled 1 files in <TIME>\n"; result.Stderr != want {
		t.Fatalf("normalized stderr = %q, want %q", result.Stderr, want)
	}
	if _, ok := result.OutputTree["nested/artifact.txt"]; !ok {
		t.Fatalf("normalized output paths = %v, want nested/artifact.txt", outputPaths(result.OutputTree))
	}
	if want := "<TEMP_ROOT>/out/nested/artifact.txt\ncompiled 3 files in <TIME>\n"; string(result.OutputTree["nested/artifact.txt"]) != want {
		t.Fatalf("normalized artifact = %q, want %q", result.OutputTree["nested/artifact.txt"], want)
	}
}

func TestComparatorDetectsOneByteDrift(t *testing.T) {
	t.Parallel()

	// Given
	a := &RunResult{OutputTree: map[string][]byte{"out/main.luau": []byte("return 1\n")}}
	b := &RunResult{OutputTree: map[string][]byte{"out/main.luau": []byte("return 1\n")}}
	b.OutputTree["out/main.luau"][7] = '2'

	// When
	report, err := Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if report.Match {
		t.Fatal("comparison matched, want byte drift")
	}
	if len(report.Diffs) != 1 {
		t.Fatalf("diff count = %d, want 1: %#v", len(report.Diffs), report.Diffs)
	}
	diff := report.Diffs[0]
	if diff.Path != "out/main.luau" {
		t.Fatalf("diff path = %q, want out/main.luau", diff.Path)
	}
	if diff.ADigest == diff.BDigest {
		t.Fatalf("diff digests match = %q, want different digests", diff.ADigest)
	}
}

func TestComparatorMatch(t *testing.T) {
	t.Parallel()

	// Given
	a := &RunResult{
		ExitCode: 0,
		Stdout:   "compiled 1 files in <TIME>\n",
		OutputTree: map[string][]byte{
			"out/main.luau": []byte("return 1\n"),
		},
	}
	b := &RunResult{
		ExitCode: 0,
		Stdout:   "compiled 1 files in <TIME>\n",
		OutputTree: map[string][]byte{
			"out/main.luau": []byte("return 1\n"),
		},
	}

	// When
	report, err := Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if !report.Match {
		t.Fatalf("comparison diffs = %#v, want match", report.Diffs)
	}
	if len(report.Diffs) != 0 {
		t.Fatalf("diff count = %d, want 0", len(report.Diffs))
	}
}

func TestComparatorReportsFilesPresentInOnlyOneResult(t *testing.T) {
	t.Parallel()

	// Given
	a := &RunResult{OutputTree: map[string][]byte{"only-a.luau": []byte("return 1\n")}}
	b := &RunResult{OutputTree: map[string][]byte{"only-b.luau": []byte("return 2\n")}}

	// When
	report, err := Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if report.Match {
		t.Fatal("comparison matched, want presence differences")
	}
	if len(report.Diffs) != 2 {
		t.Fatalf("diff count = %d, want 2: %#v", len(report.Diffs), report.Diffs)
	}
	if report.Diffs[0].Path != "only-a.luau" || report.Diffs[0].BDigest != "<missing>" {
		t.Fatalf("first diff = %#v, want only-a.luau missing from B", report.Diffs[0])
	}
	if report.Diffs[1].Path != "only-b.luau" || report.Diffs[1].ADigest != "<missing>" {
		t.Fatalf("second diff = %#v, want only-b.luau missing from A", report.Diffs[1])
	}
}

func forkNodeModules(t *testing.T, extractDir string) string {
	t.Helper()

	for _, nodeModules := range []string{
		filepath.Join(extractDir, "node_modules"),
		filepath.Join(extractDir, "roblox-ts", "node_modules"),
	} {
		if hasForkDependencies(nodeModules) {
			return nodeModules
		}
	}
	t.Skip("fork node_modules not installed")
	return ""
}

func hasForkDependencies(nodeModules string) bool {
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

func writeCompilerFixture(t *testing.T, nodeModules string) string {
	t.Helper()

	fixtureDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fixtureDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nodeModules, filepath.Join(fixtureDir, "node_modules")); err != nil {
		t.Fatalf("link fixture node_modules: %v", err)
	}
	const packageJSON = `{
  "name": "@forkparity/fixture"
}
`
	if err := os.WriteFile(filepath.Join(fixtureDir, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	const tsConfig = `{
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
	if err := os.WriteFile(filepath.Join(fixtureDir, "tsconfig.json"), []byte(tsConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, "src", "main.ts"), []byte("export const answer = 42;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fixtureDir
}

func hasOutputWithSuffix(tree map[string][]byte, suffix string) bool {
	for path := range tree {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func outputPaths(tree map[string][]byte) []string {
	paths := make([]string, 0, len(tree))
	for path := range tree {
		paths = append(paths, path)
	}
	return paths
}
