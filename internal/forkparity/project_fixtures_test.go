package forkparity

import (
	"context"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestProjectFixtures(t *testing.T) {
	t.Parallel()

	// Given
	fixtures, err := LoadProjectFixtures(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	expectedNames := []string{
		"build-basic",
		"build-declarations",
		"build-no-change",
		"cross-project-dts",
		"per-project-rojo",
		"transformer-declarations",
		"transformer-ordering",
		"transformer-sourcemap",
		"diagnostics",
		"duplicate-output",
	}

	// Then
	if names := fixtureNames(fixtures); !slices.Equal(names, expectedNames) {
		t.Fatalf("fixture names = %v, want %v", names, expectedNames)
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()

			// Then
			if fixture.Description == "" {
				t.Fatal("fixture description is empty")
			}
			if fixture.Category == "" {
				t.Fatal("fixture category is empty")
			}
			for _, requiredPath := range []string{"package.json", "tsconfig.json"} {
				if _, ok := fixture.Files[requiredPath]; !ok {
					t.Fatalf("fixture files = %v, want %s", fixturePaths(fixture.Files), requiredPath)
				}
			}
			if !hasSuffix(fixture.Files, ".ts") {
				t.Fatalf("fixture files = %v, want TypeScript source", fixturePaths(fixture.Files))
			}
			for filePath, contents := range fixture.Files {
				if path.IsAbs(filePath) || filePath == "." || strings.HasPrefix(filePath, "../") || strings.Contains(filePath, "/../") {
					t.Fatalf("fixture path = %q, want relative path without traversal", filePath)
				}
				if contents == "" {
					t.Fatalf("fixture file %q is empty", filePath)
				}
			}
		})
	}
}

func TestProjectFixtures_coverForkScenarios(t *testing.T) {
	t.Parallel()

	// Given
	loaded, err := LoadProjectFixtures(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := fixturesByName(loaded)
	tests := []struct {
		name        string
		fixtureName string
		filePath    string
		want        string
	}{
		{name: "declaration emit", fixtureName: "build-declarations", filePath: "tsconfig.json", want: `"declaration": true`},
		{name: "no-change rebuild", fixtureName: "build-no-change", filePath: "tsconfig.json", want: `"composite": true`},
		{name: "project references", fixtureName: "cross-project-dts", filePath: "tsconfig.json", want: `"./lib"`},
		{name: "per-project rojo", fixtureName: "per-project-rojo", filePath: "custom-pkg/tsconfig.json", want: `"rojo":"./custom.project.json"`},
		{name: "declaration transformer", fixtureName: "transformer-declarations", filePath: "tsconfig.json", want: `"afterDeclarations": true`},
		{name: "transformer ordering", fixtureName: "transformer-ordering", filePath: "tsconfig.json", want: `"after": true`},
		{name: "source map", fixtureName: "transformer-sourcemap", filePath: "tsconfig.json", want: `"sourceMap": true`},
		{name: "diagnostic", fixtureName: "diagnostics", want: "TS2322"},
		{name: "duplicate output", fixtureName: "duplicate-output", want: "duplicate output path detected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			fixture, ok := fixtures[tt.fixtureName]

			// Then
			if !ok {
				t.Fatalf("fixture %q is missing", tt.fixtureName)
			}
			contents := fixture.Files[tt.filePath]
			if tt.filePath == "" {
				contents = fixture.ExpectedStdout + fixture.ExpectedStderr
			}
			if !strings.Contains(contents, tt.want) {
				t.Fatalf("fixture %q content = %q, want %q", tt.fixtureName, contents, tt.want)
			}
			if tt.fixtureName == "diagnostics" || tt.fixtureName == "duplicate-output" {
				if fixture.ExpectedExitCode == 0 {
					t.Fatalf("fixture %q exit code = 0, want failure", tt.fixtureName)
				}
			}
		})
	}
}

func TestProjectFixtureManifest(t *testing.T) {
	t.Parallel()

	// Given
	root := repoRoot(t)
	provenance, err := ProjectFixtureProvenance(root)
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := LoadProjectFixtures(root)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if provenance.ZipDigest != committedZipDigest {
		t.Fatalf("provenance zip digest = %q, want %q", provenance.ZipDigest, committedZipDigest)
	}
	if provenance.CaptureCommand == "" {
		t.Fatal("provenance capture command is empty")
	}

	invocationCount := make(map[string]int, len(fixtures))
	for _, fixture := range fixtures {
		for _, invocation := range fixture.Invocations {
			if invocation.Name == "" || !slices.Contains(invocation.Arguments, "--project") {
				t.Fatalf("fixture %q invocation = %#v, want named --project invocation", fixture.Name, invocation)
			}
			invocationCount[fixture.Name]++
		}
	}
	for _, fixture := range fixtures {
		want := 1
		if fixture.Name == "build-no-change" {
			want = 2
		}
		if got := invocationCount[fixture.Name]; got != want {
			t.Fatalf("invocation count for %q = %d, want %d", fixture.Name, got, want)
		}
	}
}

func TestProjectOracleGoldens(t *testing.T) {
	// Given
	root := repoRoot(t)
	fixtures, err := LoadProjectFixtures(root)
	if err != nil {
		t.Fatal(err)
	}
	rotorBin := filepath.Join(t.TempDir(), "rotor")
	if runtime.GOOS == "windows" {
		rotorBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", rotorBin, "./cmd/rotor")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build rotor: %v\n%s", err, output)
	}
	runner := MatrixRunner{RepoRoot: root}
	nodeModules, err := runner.rotorNodeModules()
	if err != nil {
		t.Fatal(err)
	}

	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			// When
			result, err := runner.runProjectFixture(context.Background(), rotorBin, nodeModules, fixture)
			if err != nil {
				t.Fatal(err)
			}

			// Then
			if len(result.Drifts) != 0 {
				t.Fatalf("project oracle drift: %#v", result.Drifts)
			}
		})
	}
}

func fixtureNames(fixtures []ProjectFixture) []string {
	names := make([]string, len(fixtures))
	for index, fixture := range fixtures {
		names[index] = fixture.Name
	}
	return names
}

func fixturePaths(files map[string]string) []string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	slices.Sort(paths)
	return paths
}

func fixturesByName(fixtures []ProjectFixture) map[string]ProjectFixture {
	byName := make(map[string]ProjectFixture, len(fixtures))
	for _, fixture := range fixtures {
		byName[fixture.Name] = fixture
	}
	return byName
}

func hasSuffix(files map[string]string, suffix string) bool {
	for filePath := range files {
		if strings.HasSuffix(filePath, suffix) {
			return true
		}
	}
	return false
}
