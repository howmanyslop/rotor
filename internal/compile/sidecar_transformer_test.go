package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const orderingTransformerPlugin = `const ts = require("typescript");

function prefixTransformer(prefix) {
	return (context) => {
		const visit = (node) => {
			if (ts.isStringLiteral(node)) {
				return ts.factory.createStringLiteral(prefix + ":" + node.text);
			}
			return ts.visitEachChild(node, visit, context);
		};
		return (sourceFile) => ts.visitNode(sourceFile, visit);
	};
}

module.exports = function (program, config, helpers) {
	if (!program.getTypeChecker() || helpers.ts !== ts) {
		throw new Error("missing program transformer inputs");
	}
	return prefixTransformer(config.prefix);
};

module.exports.named = function (config) {
	return prefixTransformer(config.prefix);
};
`

const hookTransformerPlugin = `const ts = require("typescript");

function prefixTransformer(prefix) {
	return (context) => {
		const visit = (node) => {
			if (ts.isStringLiteral(node)) {
				return ts.factory.createStringLiteral(prefix + ":" + node.text);
			}
			return ts.visitEachChild(node, visit, context);
		};
		return (sourceFile) => ts.visitNode(sourceFile, visit);
	};
}

module.exports = function () {
	return {
		before: prefixTransformer("before"),
		after: prefixTransformer("after"),
		afterDeclarations: prefixTransformer("afterDeclarations"),
	};
};

module.exports.shouldTransformSourceFile = function (sourceFile, program, config) {
	if (!program.getTypeChecker() || config.transform !== "./plugins/hooks.js") {
		throw new Error("missing source-file hook inputs");
	}
	return sourceFile.fileName.endsWith("main.ts");
};
`

func TestTransformerOrdering(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/plugin-ordering-fixture", "")
	// Registered after writeProject's t.TempDir so it runs before the temp
	// dir is removed (the worker's cwd is the project dir).
	t.Cleanup(closeSidecarSessions)
	// The base declares a plugin the child's list leaves out: the child's
	// `plugins` replaces it, so "inherited" must never reach the output.
	writeSidecarPluginFixture(t, dir, `{
	"compilerOptions": {
		"plugins": [{
			"transform": "./plugins/ordering.js",
			"import": "named",
			"type": "config",
			"prefix": "inherited"
		}]
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
		"outDir": "out",
		"plugins": [
			{ "transform": "./plugins/ordering.js", "prefix": "child" },
			{ "transform": "./plugins/ordering.js", "import": "named", "type": "config", "prefix": "parent" },
			{ "transform": "./plugins/ordering.js", "prefix": "after", "after": true }
		]
	},
	"include": ["src"]
}`)
	if err := os.WriteFile(filepath.Join(dir, "plugins", "ordering.js"), []byte(orderingTransformerPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if got := result.Outputs["out/main.luau"]; !strings.Contains(got, `local phase = "after:parent:child:start"`) {
		t.Fatalf("out/main.luau transformer order =\n%s", got)
	}
	if got := result.Outputs["out/main.luau"]; strings.Contains(got, "inherited:") {
		t.Fatalf("out/main.luau ran a plugin the child's `plugins` replaced =\n%s", got)
	}
}

// TestTransformerPluginsChildOverride pins the `extends` rule `tsc --showConfig`
// reports: `plugins` is an array-valued compiler option, so a child that
// declares it replaces the inherited list. `"plugins": []` is how a project —
// a test-only config extending its package's config, say — opts out of a
// transform it inherits. rbxtsc concatenates the chain instead and runs the
// parent's transformers regardless; rotor diverges deliberately.
func TestTransformerPluginsChildOverride(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/plugin-override-fixture", "")
	// Registered after writeProject's t.TempDir so it runs before the temp
	// dir is removed (the worker's cwd is the project dir).
	t.Cleanup(closeSidecarSessions)
	writeSidecarPluginFixture(t, dir, `{
	"compilerOptions": {
		"plugins": [{
			"transform": "./plugins/ordering.js",
			"import": "named",
			"type": "config",
			"prefix": "inherited"
		}]
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
		"outDir": "out",
		"plugins": []
	},
	"include": ["src"]
}`)
	if err := os.WriteFile(filepath.Join(dir, "plugins", "ordering.js"), []byte(orderingTransformerPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if got := result.Outputs["out/main.luau"]; !strings.Contains(got, `local phase = "start"`) {
		t.Fatalf("out/main.luau ran an inherited transformer the child opted out of =\n%s", got)
	}
	// The resolved list is empty, so the compile must not pay for a Node
	// worker that would find nothing to run.
	sidecarMu.Lock()
	sessions := len(sidecarSessions)
	sidecarMu.Unlock()
	if sessions != 0 {
		t.Fatalf("spawned %d sidecar session(s) for a project with no resolved plugins", sessions)
	}
}

// TestTransformerPluginsInherited is the other half of the rule: a child that
// declares no `plugins` of its own still inherits the parent's list.
func TestTransformerPluginsInherited(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/plugin-inherit-fixture", "")
	// Registered after writeProject's t.TempDir so it runs before the temp
	// dir is removed (the worker's cwd is the project dir).
	t.Cleanup(closeSidecarSessions)
	writeSidecarPluginFixture(t, dir, `{
	"compilerOptions": {
		"plugins": [{
			"transform": "./plugins/ordering.js",
			"import": "named",
			"type": "config",
			"prefix": "inherited"
		}]
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
	if err := os.WriteFile(filepath.Join(dir, "plugins", "ordering.js"), []byte(orderingTransformerPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if got := result.Outputs["out/main.luau"]; !strings.Contains(got, `local phase = "inherited:start"`) {
		t.Fatalf("out/main.luau dropped an inherited transformer =\n%s", got)
	}
}

func TestTransformerHooks(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/plugin-hooks-fixture", "")
	// Registered after writeProject's t.TempDir so it runs before the temp
	// dir is removed (the worker's cwd is the project dir).
	t.Cleanup(closeSidecarSessions)
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
			{ "transform": "./plugins/hooks.js" },
			{ "transform": "./plugins/no-hooks.js" }
		]
	},
	"include": ["src"]
}`)
	if err := os.WriteFile(filepath.Join(dir, "plugins", "hooks.js"), []byte(hookTransformerPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "no-hooks.js"), []byte("module.exports = function () { return {}; };\nmodule.exports.shouldTransformSourceFile = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const selected = \"selected\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "skipped.ts"), []byte("export const skipped = \"skipped\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if got := result.Outputs["out/main.luau"]; !strings.Contains(got, `local selected = "after:before:selected"`) || strings.Contains(got, "afterDeclarations") {
		t.Fatalf("out/main.luau hook output =\n%s", got)
	}
	if got := result.Outputs["out/skipped.luau"]; !strings.Contains(got, `local skipped = "skipped"`) || strings.Contains(got, "before:") {
		t.Fatalf("out/skipped.luau ignored hook output =\n%s", got)
	}
}
