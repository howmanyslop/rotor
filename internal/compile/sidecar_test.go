package compile

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoSidecarDir returns tools/sidecar in this repo checkout. Synthetic
// plugin fixtures have no typescript of their own, so tests point
// ROTOR_SIDECAR_PATH here and the worker falls back to the sidecar's
// pinned typescript install.
func repoSidecarDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "tools", "sidecar")
	if _, err := os.Stat(filepath.Join(dir, "main.js")); err != nil {
		t.Fatalf("repo sidecar missing: %v", err)
	}
	return filepath.Clean(dir)
}

func setRepoSidecarPath(t *testing.T) {
	t.Helper()
	t.Setenv("ROTOR_SIDECAR_PATH", repoSidecarDir(t))
}

const prefixStringPlugin = `const ts = require("typescript");

module.exports = function programTransformer(program, config, helpers) {
	if (!program.getTypeChecker()) {
		throw new Error("missing program checker");
	}
	if (!helpers || helpers.ts !== ts) {
		throw new Error("missing ts helper");
	}

	return (context) => {
		const visit = (node) => {
			if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
				return node;
			}
			if (ts.isStringLiteral(node)) {
				return ts.factory.createStringLiteral(config.prefix + ":" + node.text);
			}
			return ts.visitEachChild(node, visit, context);
		};
		return (sourceFile) => ts.visitNode(sourceFile, visit);
	};
};
`

func TestBuildProjectTransformerPluginSidecar(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/plugin-fixture", "")
	// Registered after writeProject's t.TempDir so it runs before the temp
	// dir is removed (the worker's cwd is the project dir).
	t.Cleanup(closeSidecarSessions)
	writeSidecarPluginFixture(t, dir, `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"plugins": [
			{
				"transform": "./plugins/prefix-string.js",
				"prefix": "plugin"
			}
		]
	}
}`, `{
	"extends": "./tsconfig.base.json",
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out"
	},
	"include": ["src"]
}`)

	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const phase = \"start\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	got := result.Outputs["out/main.luau"]
	if !strings.Contains(got, `local phase = "plugin:start"`) {
		t.Fatalf("out/main.luau missing transformed string:\n%s", got)
	}
}

func TestBuildProjectWithoutPluginsDoesNotRequireNode(t *testing.T) {
	dir := writeProject(t, "@scope/no-plugin-fixture", "")
	t.Setenv("ROTOR_NODE_PATH", filepath.Join(dir, "missing-node"))

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if result == nil {
		t.Fatal("nil result")
	}
}

func TestSidecarRoundTripTimesOut(t *testing.T) {
	// Given
	t.Setenv(sidecarResponseTimeoutEnv, "10ms")
	timeout, err := sidecarResponseTimeout()
	if err != nil {
		t.Fatal(err)
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdinReader.Close() })
	t.Cleanup(func() { _ = stdinWriter.Close() })
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdoutReader.Close() })
	t.Cleanup(func() { _ = stdoutWriter.Close() })
	session := &sidecarSession{
		stdin:  stdinWriter,
		stdout: bufio.NewReader(stdoutReader),
		stderr: newSidecarStderrTail(strings.NewReader("")),
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	// When
	_, err = session.roundTrip(ctx, sidecarRequest{})

	// Then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("roundTrip error = %v, want context deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "response timed out") {
		t.Fatalf("roundTrip error = %q, want timeout context", err)
	}
	if !session.dead {
		t.Fatal("timed-out sidecar session remains reusable")
	}
}

func TestBuildProjectTransformerPluginRequiresNode(t *testing.T) {
	setRepoSidecarPath(t)
	dir := writeProject(t, "@scope/plugin-node-fixture", "")
	writeSidecarPluginFixture(t, dir, "", `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"plugins": [
			{
				"transform": "./plugins/prefix-string.js",
				"prefix": "plugin"
			}
		]
	},
	"include": ["src"]
}`)
	t.Setenv("ROTOR_NODE_PATH", filepath.Join(dir, "missing-node"))

	_, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err == nil {
		t.Fatal("expected missing-node error")
	}
	if len(diags) != 1 || !strings.Contains(diags[0], "node executable not found") {
		t.Fatalf("diags = %v, want missing-node diagnostic", diags)
	}
}

func TestTransformerMissingIsError(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/plugin-missing-fixture", "")
	t.Cleanup(closeSidecarSessions)
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"plugins": [
			{
				"transform": "./plugins/does-not-exist.js",
				"prefix": "plugin"
			}
		]
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const phase = \"start\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err == nil {
		t.Fatal("BuildProjectWithOptions succeeded with a missing transformer")
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil", result)
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %v, want one missing-transformer error", diags)
	}
	if !strings.Contains(diags[0], "Transformer `./plugins/does-not-exist.js` failed to load!") {
		t.Fatalf("diagnostic = %q, want fork missing-transformer text", diags[0])
	}
	if !strings.Contains(diags[0], "Suggestion: Did you forget to install the package, or to build it?") {
		t.Fatalf("diagnostic = %q, want fork suggestion text", diags[0])
	}
}

const countingPlugin = `let buildCount = 0;

module.exports = function (program, config, helpers) {
	const ts = helpers.ts;
	buildCount += 1;
	return (context) => (sourceFile) => {
		const visit = (node) => {
			if (ts.isStringLiteral(node) && node.text === "BUILD_COUNT") {
				return ts.factory.createStringLiteral("build:" + buildCount);
			}
			return ts.visitEachChild(node, visit, context);
		};
		return ts.visitNode(sourceFile, visit);
	};
};
`

func TestTransformerWarmSession(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()

	dir := writeProject(t, "@scope/plugin-warm-fixture", "")
	t.Cleanup(closeSidecarSessions)
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "counting.js"), []byte(countingPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"plugins": [{ "transform": "./plugins/counting.js" }]
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const tag = \"BUILD_COUNT\";\nexport const phase = \"first\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}
	got := result.Outputs["out/main.luau"]
	if !strings.Contains(got, `local tag = "build:1"`) || !strings.Contains(got, `local phase = "first"`) {
		t.Fatalf("first build output unexpected:\n%s", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const tag = \"BUILD_COUNT\";\nexport const phase = \"second\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err = BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	got = result.Outputs["out/main.luau"]
	if !strings.Contains(got, `local tag = "build:2"`) {
		t.Fatalf("second build did not reuse a warm sidecar process:\n%s", got)
	}
	if !strings.Contains(got, `local phase = "second"`) {
		t.Fatalf("warm sidecar served a stale snapshot (changedFiles overlay broken):\n%s", got)
	}
}

func writeSidecarPluginFixture(t *testing.T, dir, baseTSConfig, rootTSConfig string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "prefix-string.js"), []byte(prefixStringPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if baseTSConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "tsconfig.base.json"), []byte(baseTSConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rootTSConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(rootTSConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
