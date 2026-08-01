package compile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const declarationMarkerPlugin = `const ts = require("typescript");

module.exports = function () {
	return (context) => (sourceFile) => {
		const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
			context.factory.createVariableDeclaration("__DECLARATION_MARKER__", undefined, undefined, context.factory.createStringLiteral("after-declarations")),
		], ts.NodeFlags.Const));
		return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
	};
};
`

const insertStatementPlugin = `const ts = require("typescript");

module.exports = function () {
	return (context) => (sourceFile) => {
		const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
			context.factory.createVariableDeclaration("__INJECTED__", undefined, undefined, context.factory.createStringLiteral("transformer-was-here")),
		], ts.NodeFlags.Const));
		return context.factory.updateSourceFile(sourceFile, [marker].concat(sourceFile.statements));
	};
};
`

func TestAfterDeclarationsOnly(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	t.Cleanup(closeSidecarSessions)

	dir := writeProject(t, "@scope/after-declarations-fixture", "")
	writeSidecarPluginFixture(t, dir, "", sidecarDeclarationConfig(`[{ "transform": "./plugins/declaration-marker.js", "afterDeclarations": true }]`))
	if err := os.WriteFile(filepath.Join(dir, "plugins", "declaration-marker.js"), []byte(declarationMarkerPlugin), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("build: %v (diags: %v)", err, diags)
	}
	declaration, err := os.ReadFile(filepath.Join(dir, "out", "main.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(declaration), "__DECLARATION_MARKER__") {
		t.Fatalf("declaration marker missing:\n%s", declaration)
	}
	if strings.Contains(result.Outputs["out/main.luau"], "__DECLARATION_MARKER__") {
		t.Fatalf("declaration marker leaked into Luau:\n%s", result.Outputs["out/main.luau"])
	}
}

func TestTransformerParentFix(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	t.Cleanup(closeSidecarSessions)

	dir := writeProject(t, "@scope/parent-fix-fixture", "")
	plugin := `const ts = require("typescript");
function inject(context) {
	return (sourceFile) => ts.visitNode(sourceFile, function visit(node) {
		if (ts.isStringLiteral(node) && node.text === "start") return ts.factory.createStringLiteral("synthetic");
		return ts.visitEachChild(node, visit, context);
	});
}
function requireParent(context) {
	return (sourceFile) => ts.visitNode(sourceFile, function visit(node) {
		if (ts.isStringLiteral(node) && node.text === "synthetic" && !ts.isVariableDeclaration(node.parent)) throw new Error("missing synthetic parent");
		return ts.visitEachChild(node, visit, context);
	});
}
module.exports = function () { return { before: inject, after: requireParent }; };`
	writeSidecarPluginFixture(t, dir, "", sidecarDeclarationConfig(`[{ "transform": "./plugins/parent-fix.js" }]`))
	if err := os.WriteFile(filepath.Join(dir, "plugins", "parent-fix.js"), []byte(plugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const value = \"start\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("build: %v (diags: %v)", err, diags)
	}
	if !strings.Contains(result.Outputs["out/main.luau"], `"synthetic"`) {
		t.Fatalf("parent-fix transform missing:\n%s", result.Outputs["out/main.luau"])
	}
}

func TestDeclarationPathRewrite(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	t.Cleanup(closeSidecarSessions)

	dir := writeProject(t, "@scope/declaration-path-fixture", "")
	config := strings.Replace(
		sidecarDeclarationConfig(`[]`),
		`"outDir": "out",`,
		`"outDir": "out", "baseUrl": ".", "paths": { "@alias/*": ["src/*"] },`,
		1,
	)
	writeSidecarPluginFixture(t, dir, "", config)
	if err := os.WriteFile(filepath.Join(dir, "src", "value.ts"), []byte("export interface Value { label: string; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("import type { Value } from \"@alias/value\";\nexport type Output = Value;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("build: %v (diags: %v)", err, diags)
	}
	declaration, err := os.ReadFile(filepath.Join(dir, "out", "main.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(declaration), `from "./value"`) {
		t.Fatalf("declaration alias was not rewritten:\n%s", declaration)
	}
}

func TestTransformerSourceMapOriginalContent(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	t.Cleanup(closeSidecarSessions)

	dir := writeProject(t, "@scope/transformer-source-map-fixture", "")
	config := strings.Replace(sidecarDeclarationConfig(`[{ "transform": "./plugins/insert.js" }]`), `"declaration": true,`, `"declaration": true, "sourceMap": true,`, 1)
	writeSidecarPluginFixture(t, dir, "", config)
	if err := os.WriteFile(filepath.Join(dir, "plugins", "insert.js"), []byte(insertStatementPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	original := "export const value = \"start\";\n"
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("build: %v (diags: %v)", err, diags)
	}
	mapBytes, err := os.ReadFile(filepath.Join(dir, "out", "main.luau.map"))
	if err != nil {
		t.Fatal(err)
	}
	var sourceMap struct {
		SourcesContent []string `json:"sourcesContent"`
		Mappings       string   `json:"mappings"`
	}
	if err := json.Unmarshal(mapBytes, &sourceMap); err != nil {
		t.Fatal(err)
	}
	if len(sourceMap.SourcesContent) != 1 || sourceMap.SourcesContent[0] != original {
		t.Fatalf("sourcesContent = %q, want original source %q", sourceMap.SourcesContent, original)
	}
	if sourceMap.Mappings == "" {
		t.Fatal("transformed source map has no mappings")
	}
}

func sidecarDeclarationConfig(plugins string) string {
	return `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"declaration": true,
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
		"plugins": ` + plugins + `
	},
	"include": ["src"]
}`
}
