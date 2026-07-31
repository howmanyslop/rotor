package forkparity

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ansiEscapeSequence = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func TestTransformerFixtureProvenance(t *testing.T) {
	t.Parallel()

	provenance := TransformerFixtureProvenance()
	if provenance.ZipDigest != committedZipDigest {
		t.Fatalf("zip digest = %q, want %q", provenance.ZipDigest, committedZipDigest)
	}
	if provenance.ExtractionCommand == "" {
		t.Fatal("extraction command is empty")
	}
	if provenance.CompilerInvocation == "" {
		t.Fatal("compiler invocation is empty")
	}
}

func TestTransformerOracleGoldens(t *testing.T) {
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
	runner := Runner{
		ForkCLIPath: filepath.Join(extractDir, "roblox-ts", "dist", "CLI", "cli.cjs"),
	}

	for _, fixture := range AllTransformerFixtures() {
		t.Run(fixture.Name, func(t *testing.T) {
			// Given
			fixtureDir := writeTransformerFixture(t, nodeModules, fixture.TSCode)

			// When
			result, err := runner.RunFork(
				context.Background(),
				fixtureDir,
				filepath.Join(fixtureDir, "out"),
			)
			if err != nil {
				t.Fatal(err)
			}

			// Then
			assertTransformerFixtureResult(t, fixture, result)
		})
	}
}

func writeTransformerFixture(t *testing.T, nodeModules, tsCode string) string {
	t.Helper()

	fixtureDir := writeCompilerFixture(t, nodeModules)
	mainPath := filepath.Join(fixtureDir, "src", "main.ts")
	if err := os.WriteFile(mainPath, []byte(tsCode), 0o644); err != nil {
		t.Fatalf("write transformer fixture: %v", err)
	}
	return fixtureDir
}

func assertTransformerFixtureResult(t *testing.T, fixture TransformerFixture, result *RunResult) {
	t.Helper()

	if len(fixture.Diagnostics) > 0 {
		if result.ExitCode == 0 {
			t.Fatalf("exit code = 0, want diagnostic failure\nstdout:\n%s\nstderr:\n%s", result.Stdout, result.Stderr)
		}
		if len(result.OutputTree) != 0 {
			t.Fatalf("output tree = %v, want no output after diagnostics", outputPaths(result.OutputTree))
		}

		diagnostics := ansiEscapeSequence.ReplaceAllString(result.Stdout+result.Stderr, "")
		for _, expected := range fixture.Diagnostics {
			if !strings.Contains(diagnostics, expected) {
				t.Fatalf("diagnostics = %q, want %q", diagnostics, expected)
			}
		}
		return
	}

	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if len(result.OutputTree) != 1 {
		t.Fatalf("output tree = %v, want exactly main.luau", outputPaths(result.OutputTree))
	}

	actual, ok := result.OutputTree["main.luau"]
	if !ok {
		t.Fatalf("output tree = %v, want main.luau", outputPaths(result.OutputTree))
	}
	if string(actual) != fixture.ExpectedLuau {
		t.Fatalf("main.luau differs for %s\nwant:\n%s\ngot:\n%s", fixture.Name, fixture.ExpectedLuau, actual)
	}
}
