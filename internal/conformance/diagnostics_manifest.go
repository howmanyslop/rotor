package conformance

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"rotor/internal/compile"
)

type DiagnosticFixture struct {
	Name        string
	Path        string
	ExpectedIDs []string
	Exact       bool
}

type diagnosticFixtureProjectPlan struct {
	sourceRel      string
	rojoConfigFile string
}

var skippedDiagnosticIDs = map[string]string{}

// forkDiagnosticExpectations reclassifies fixtures whose rbxtsc expectation no
// longer holds for rotor. An empty slice means "compiles clean".
//
// The noLabeledStatement.* trio predates rotor's loop-label support: labels are
// a deliberate rotor extension (see transformer/labeledstatement.go), so `.1.ts`
// now compiles, and `.2.ts`/`.3.ts` — a `// @ts-ignore`d break/continue naming a
// label that does not exist — trade the blanket ban for rotor's own
// unknown-label error instead of emitting a misdirected `break`.
var forkDiagnosticExpectations = map[string][]string{
	"noFunctionExpressionName.ts": {},
	"noSpreadDestructuring.1.ts":  {},
	"noSpreadDestructuring.2.ts":  {"noRestSpreadingOfRobloxTypes"},
	"noLabeledStatement.1.ts":     {},
	"noLabeledStatement.2.ts":     {"rotorUnknownLabel"},
	"noLabeledStatement.3.ts":     {"rotorUnknownLabel"},
}

var installConformanceDepsOnce struct {
	once sync.Once
	err  error
}

func loadDiagnosticFixtures(dir string) ([]DiagnosticFixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var fixtures []DiagnosticFixture
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch filepath.Ext(name) {
		case ".ts", ".tsx":
		default:
			continue
		}
		expectedIDs := []string{diagnosticBaseName(name)}
		exact := false
		if forkExpectedIDs, ok := forkDiagnosticExpectations[name]; ok {
			expectedIDs = forkExpectedIDs
			exact = true
		}
		fixtures = append(fixtures, DiagnosticFixture{
			Name:        name,
			Path:        filepath.Join(dir, name),
			ExpectedIDs: expectedIDs,
			Exact:       exact,
		})
	}
	slices.SortFunc(fixtures, func(a, b DiagnosticFixture) int {
		return strings.Compare(a.Name, b.Name)
	})
	return fixtures, nil
}

func compileDiagnosticFixture(root, fixturePath string) ([]string, error) {
	projectDir := filepath.Join(root, "testdata", "conformance", "project")
	if err := ensureConformanceProjectDeps(projectDir); err != nil {
		return nil, err
	}

	name := filepath.Base(fixturePath)
	plan := diagnosticFixtureProjectPlan{
		sourceRel:      filepath.ToSlash(filepath.Join("src", "__diagnostics", name)),
		rojoConfigFile: filepath.Join(projectDir, "default.project.json"),
	}
	switch name {
	case "noIsolatedImport.ts", "noRojoData.ts":
		plan.sourceRel = filepath.ToSlash(filepath.Join("src", "diagnostics", name))
		plan.rojoConfigFile = filepath.Join(root, "reference", "roblox-ts", "tests", "default.project.json")
	}
	return compileDiagnosticFixtureWithProjectPlan(projectDir, fixturePath, plan)
}

func compileDiagnosticFixtureWithProjectPlan(baseProjectDir, fixturePath string, plan diagnosticFixtureProjectPlan) ([]string, error) {
	tmpProj, err := os.MkdirTemp(baseProjectDir, ".phase5-diagnostic-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpProj)

	if err := os.WriteFile(filepath.Join(tmpProj, "package.json"), []byte(`{"name":"conformance-diagnostic-fixture"}`), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmpProj, "tsconfig.json"), []byte(diagnosticFixtureTSConfig()), 0o644); err != nil {
		return nil, err
	}
	if err := copyDiagnosticFile(plan.rojoConfigFile, filepath.Join(tmpProj, "default.project.json")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(tmpProj, "include"), 0o755); err != nil {
		return nil, err
	}
	if err := ensureDiagnosticTypeRoots(baseProjectDir, tmpProj); err != nil {
		return nil, err
	}
	if err := copyDiagnosticTree(filepath.Join(baseProjectDir, "src", "helpers"), filepath.Join(tmpProj, "src", "helpers")); err != nil {
		return nil, err
	}
	if err := copyDiagnosticFile(filepath.Join(baseProjectDir, "src", "services.d.ts"), filepath.Join(tmpProj, "src", "services.d.ts")); err != nil {
		return nil, err
	}
	if err := copyDiagnosticFile(fixturePath, filepath.Join(tmpProj, filepath.FromSlash(plan.sourceRel))); err != nil {
		return nil, err
	}

	_, diags, err := compile.CompileFileDetailedWithOptions(tmpProj, plan.sourceRel, compile.ProjectOptions{
		AllowCommentDirectives: true,
	})
	if err != nil && len(diags) == 0 {
		return nil, err
	}

	ids := make([]string, 0, len(diags))
	for _, diag := range diags {
		ids = append(ids, diag.Code)
	}
	return ids, nil
}

func diagnosticBaseName(name string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".tsx"), ".ts")
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 {
		if _, err := strconv.Atoi(base[dot+1:]); err == nil {
			base = base[:dot]
		}
	}
	return base
}

func ensureConformanceProjectDeps(projectDir string) error {
	installConformanceDepsOnce.once.Do(func() {
		if _, err := os.Stat(filepath.Join(projectDir, "node_modules")); err == nil {
			return
		}
		name := "bun"
		args := []string{"install", "--no-save"}
		if _, err := exec.LookPath(name); err != nil {
			name = "npm"
			args = []string{"install", "--no-audit", "--no-fund"}
		}
		cmd := exec.Command(name, args...)
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		installConformanceDepsOnce.err = cmd.Run()
		if installConformanceDepsOnce.err != nil {
			installConformanceDepsOnce.err = fmt.Errorf("install conformance project dependencies with %s: %w", name, installConformanceDepsOnce.err)
		}
	})
	return installConformanceDepsOnce.err
}

func skipReasonForDiagnosticFixture(tc DiagnosticFixture) string {
	if len(tc.ExpectedIDs) != 1 {
		return ""
	}
	return skippedDiagnosticIDs[tc.ExpectedIDs[0]]
}

func diagnosticFixtureTSConfig() string {
	return `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"downlevelIteration": true,
		"jsx": "react",
		"jsxFactory": "Roact.jsx",
		"module": "commonjs",
		"moduleResolution": "Node",
		"noLib": true,
		"resolveJsonModule": true,
		"forceConsistentCasingInFileNames": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"typeRoots": ["node_modules/@rbxts"],
		"experimentalDecorators": true,
		"rootDir": "src",
		"outDir": "out",
		"checkers": 1,
		"baseUrl": "src"
	},
	"include": ["src"]
}`
}

func copyDiagnosticTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyDiagnosticFile(path, target)
	})
}

func copyDiagnosticFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func ensureDiagnosticTypeRoots(baseProjectDir, tmpProj string) error {
	src := filepath.Join(baseProjectDir, "node_modules", "@rbxts")
	dst := filepath.Join(tmpProj, "node_modules", "@rbxts")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	if err := createDiagnosticJunction(dst, src); err == nil {
		return nil
	}
	return copyDiagnosticTree(src, dst)
}

func createDiagnosticJunction(linkPath, targetPath string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("junctions unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command("cmd", "/c", fmt.Sprintf(`mklink /J "%s" "%s"`, linkPath, targetPath))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %s -> %s: %w (%s)", linkPath, targetPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}
