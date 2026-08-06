package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadPackageVersion(t *testing.T) {
	nodeModules := t.TempDir()
	writeTestFile(t, filepath.Join(nodeModules, "@rbxts", "types", "package.json"),
		`{"name": "@rbxts/types", "version": "1.0.812"}`)

	version, ok := readPackageVersion(nodeModules, "@rbxts/types")
	if !ok || version != "1.0.812" {
		t.Fatalf("readPackageVersion = (%q, %v), want (1.0.812, true)", version, ok)
	}
	if _, ok := readPackageVersion(nodeModules, "@rbxts/compiler-types"); ok {
		t.Fatal("readPackageVersion reported a missing package as installed")
	}
}

func TestTsconfigTransformerPlugins(t *testing.T) {
	dir := t.TempDir()
	tsConfig := filepath.Join(dir, "tsconfig.json")
	writeTestFile(t, tsConfig, `{
		// JSONC comments must parse
		"compilerOptions": {
			"plugins": [
				{ "transform": "rbxts-transformer-flamework" },
				{ "name": "not-a-transformer" },
				{ "transform": "rbxts-transform-env", "verbose": true },
			],
		},
	}`)

	got := tsconfigTransformerPlugins(tsConfig)
	want := []string{"rbxts-transformer-flamework", "rbxts-transform-env"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tsconfigTransformerPlugins = %v, want %v", got, want)
	}

	if got := tsconfigTransformerPlugins(filepath.Join(dir, "missing.json")); got != nil {
		t.Fatalf("missing tsconfig should list no plugins, got %v", got)
	}
}

func TestRunDoctorMissingTsConfigFails(t *testing.T) {
	checks, _ := runDoctor(t.TempDir())
	if len(checks) != 1 || checks[0].status != doctorFail || checks[0].label != "tsconfig.json" {
		t.Fatalf("checks = %+v, want a single tsconfig.json failure", checks)
	}
}

func TestRunDoctorReportsProjectState(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tsconfig.json"), `{"compilerOptions": {}}`)
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"name": "fixture"}`)
	writeTestFile(t, filepath.Join(dir, "node_modules", "@rbxts", "compiler-types", "package.json"),
		`{"version": "3.0.0-types.0"}`)
	writeTestFile(t, filepath.Join(dir, "default.project.json"), `{"name": "fixture", "tree": {}}`)

	// Resolve symlinks (macOS /var vs /private/var style aliasing) so the
	// upward tsconfig search lands on the same path we wrote.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}

	checks, _ := runDoctor(resolved)
	byLabel := map[string]doctorCheck{}
	for _, c := range checks {
		byLabel[c.label] = c
	}

	for label, status := range map[string]doctorStatus{
		"tsconfig.json":         doctorOK,
		"node_modules":          doctorOK,
		"@rbxts/compiler-types": doctorOK,
		"@rbxts/types":          doctorFail, // not installed in the fixture
		"Rojo project":          doctorOK,
	} {
		c, ok := byLabel[label]
		if !ok {
			t.Errorf("missing check %q in %v", label, checks)
			continue
		}
		if c.status != status {
			t.Errorf("check %q status = %v, want %v (detail: %s)", label, c.status, status, c.detail)
		}
	}
	if c := byLabel["@rbxts/compiler-types"]; c.detail != "v3.0.0-types.0" {
		t.Errorf("compiler-types detail = %q, want version string", c.detail)
	}
	// No transformer plugins configured: no sidecar or typescript checks.
	if _, ok := byLabel["transformer sidecar"]; ok {
		t.Error("sidecar check should only run when transformer plugins are configured")
	}
}

// cloudFixtureDir writes the minimal project skeleton the doctor needs to
// reach the cloud section, plus an optional rotor.toml body.
func cloudFixtureDir(t *testing.T, configBody string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tsconfig.json"), `{"compilerOptions": {}}`)
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"name": "fixture"}`)
	if configBody != "" {
		writeTestFile(t, filepath.Join(dir, "rotor.toml"), configBody)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	return resolved
}

func cloudChecksByLabel(checks []doctorCheck) map[string][]doctorCheck {
	byLabel := map[string][]doctorCheck{}
	for _, c := range checks {
		byLabel[c.label] = append(byLabel[c.label], c)
	}
	return byLabel
}

func TestRunDoctorCloudConfigValidationError(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	dir := cloudFixtureDir(t, `
[assets]
paths = ["assets"]
[assets.creator]
type = "banana"
id = 1
`)

	checks, _ := runDoctor(dir)
	byLabel := cloudChecksByLabel(checks)

	configRows := byLabel["rotor.toml"]
	if len(configRows) == 0 {
		t.Fatalf("no rotor.toml check in %+v", checks)
	}
	foundValidationFail := false
	for _, c := range configRows {
		if c.status == doctorFail && strings.Contains(c.detail, "assets.creator.type") {
			foundValidationFail = true
		}
	}
	if !foundValidationFail {
		t.Errorf("expected a fail row carrying the Validate() message, got %+v", configRows)
	}

	keyRows := byLabel["ROBLOX_API_KEY"]
	if len(keyRows) != 1 {
		t.Fatalf("ROBLOX_API_KEY rows = %+v, want exactly one", keyRows)
	}
	if keyRows[0].status != doctorWarn {
		t.Errorf("unset key with config present should warn, got %+v", keyRows[0])
	}
	if !strings.Contains(keyRows[0].hint, "set ROBLOX_API_KEY") {
		t.Errorf("unset key hint = %q, want the remedy hint", keyRows[0].hint)
	}
}

func TestRunDoctorCloudValidConfigAndKeyPresence(t *testing.T) {
	const secret = "rotor-test-secret-value-1234"
	t.Setenv("ROBLOX_API_KEY", secret)
	dir := cloudFixtureDir(t, "# empty config\n")

	checks, _ := runDoctor(dir)
	byLabel := cloudChecksByLabel(checks)

	configRows := byLabel["rotor.toml"]
	if len(configRows) != 1 || configRows[0].status != doctorOK {
		t.Errorf("valid config rows = %+v, want a single ok row", configRows)
	}
	keyRows := byLabel["ROBLOX_API_KEY"]
	if len(keyRows) != 1 || keyRows[0].status != doctorOK {
		t.Fatalf("key rows = %+v, want a single ok row", keyRows)
	}
	// The key value must never leak into any part of any row.
	for _, c := range checks {
		for _, field := range []string{c.label, c.detail, c.hint} {
			if strings.Contains(field, secret) {
				t.Fatalf("ROBLOX_API_KEY value leaked into doctor output: %+v", c)
			}
		}
	}
}

func TestRunDoctorCloudNoConfigSuggestsInit(t *testing.T) {
	t.Setenv("ROBLOX_API_KEY", "")
	dir := cloudFixtureDir(t, "")

	checks, _ := runDoctor(dir)
	byLabel := cloudChecksByLabel(checks)

	// A missing rotor.toml warns and points the user at `rotor init` (the
	// doctor<->init synergy); it only fires for projects that already have a
	// tsconfig, so plain bundle projects (no tsconfig) never reach this row.
	configRows := byLabel["rotor.toml"]
	if len(configRows) != 1 || configRows[0].status != doctorWarn {
		t.Fatalf("no-config rows = %+v, want a single warn row", configRows)
	}
	if !strings.Contains(configRows[0].hint, "rotor init") {
		t.Errorf("no-config hint = %q, want a `rotor init` suggestion", configRows[0].hint)
	}
	// The API-key row stays muted info when no config is present (cloud
	// commands aren't in use yet).
	keyRows := byLabel["ROBLOX_API_KEY"]
	if len(keyRows) != 1 || keyRows[0].status != doctorInfo {
		t.Errorf("unset key without config should stay muted info, got %+v", keyRows)
	}
}

func TestRunDoctorNodeModulesMissingFails(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "tsconfig.json"), `{}`)
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"name": "fixture"}`)

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	checks, _ := runDoctor(resolved)
	found := false
	for _, c := range checks {
		if c.label == "node_modules" {
			found = true
			if c.status != doctorFail {
				t.Errorf("node_modules status = %v, want fail", c.status)
			}
		}
	}
	if !found {
		t.Fatalf("no node_modules check in %+v", checks)
	}
}
