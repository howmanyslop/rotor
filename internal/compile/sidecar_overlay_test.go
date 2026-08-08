package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// taggingPlugin rewrites string literals the way prefixStringPlugin does, but
// leaves import and export specifiers alone so a fixture can span more than one
// file. It has to change something: the worker reports a file as transformed
// only when the transform returned a new node, and an unchanged project takes
// the early return instead of the program rebuild.
const taggingPlugin = `const ts = require("typescript");

module.exports = function programTransformer() {
	return (context) => {
		const visit = (node) => {
			if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
				return node;
			}
			if (ts.isStringLiteral(node)) {
				return ts.factory.createStringLiteral("seen:" + node.text);
			}
			return ts.visitEachChild(node, visit, context);
		};
		return (sourceFile) => ts.visitNode(sourceFile, visit);
	};
};
`

// writeOverlayPluginProject is the shared fixture for the sidecar overlay
// tests: one named plugin over a src/main.ts, which is enough to route the
// project through the sidecar.
func writeOverlayPluginProject(t *testing.T, pkgName, pluginFile, pluginSource, diskText string) string {
	t.Helper()
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, pkgName, "")
	t.Cleanup(closeSidecarSessions)
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", pluginFile), []byte(pluginSource), 0o644); err != nil {
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
		"plugins": [
			{
				"transform": "./plugins/` + pluginFile + `",
				"prefix": "plugin"
			}
		]
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(diskText), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// newOverlayTestSession is a session with only the bookkeeping changedFilesFor
// touches — no worker, no pipes.
func newOverlayTestSession() *sidecarSession {
	return &sidecarSession{stamps: map[string]sidecarFileStamp{}, overlaid: map[string]string{}}
}

// writeOverlaySourceFile writes one file and returns the pair changedFilesFor
// works in: the slash-form name a program reports, and the native path a
// request carries.
func writeOverlaySourceFile(t *testing.T, text string) (fileName, nativePath string) {
	t.Helper()
	nativePath = filepath.Join(t.TempDir(), "main.ts")
	if err := os.WriteFile(nativePath, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(nativePath), nativePath
}

func TestChangedFilesForShipsOverlaysOnAFreshSession(t *testing.T) {
	// Given a fresh session — one that ships nothing, because the worker reads
	// disk itself — and an overlay for its only file
	fileName, nativePath := writeOverlaySourceFile(t, "disk\n")
	session := newOverlayTestSession()

	// When the round trip's changed files are collected
	changed, err := session.changedFilesFor([]string{fileName}, normalizeOverlays(map[string]string{nativePath: "overlay\n"}))
	if err != nil {
		t.Fatalf("changedFilesFor: %v", err)
	}

	// Then the overlay ships anyway. An overlay exists nowhere on disk, so a
	// worker left to read disk would answer on text the caller never sent.
	if len(changed) != 1 {
		t.Fatalf("changed = %v, want the overlay", changed)
	}
	if changed[0].Text != "overlay\n" {
		t.Errorf("text = %q, want the overlay's", changed[0].Text)
	}
	if changed[0].FileName != nativePath {
		t.Errorf("fileName = %q, want %q", changed[0].FileName, nativePath)
	}
}

func TestChangedFilesForLeavesUnoverlaidFilesToTheWorker(t *testing.T) {
	// Given a fresh session and no overlays
	fileName, _ := writeOverlaySourceFile(t, "disk\n")
	session := newOverlayTestSession()

	// When the round trip's changed files are collected
	changed, err := session.changedFilesFor([]string{fileName}, nil)
	if err != nil {
		t.Fatalf("changedFilesFor: %v", err)
	}

	// Then nothing ships — the fresh-session contract is unchanged
	if len(changed) != 0 {
		t.Fatalf("changed = %v, want nothing on a fresh session", changed)
	}
}

func TestChangedFilesForRevertsAnOverlayThatWentAway(t *testing.T) {
	// Given a session that shipped an overlay on an earlier round trip
	fileName, nativePath := writeOverlaySourceFile(t, "disk\n")
	session := newOverlayTestSession()
	if _, err := session.changedFilesFor([]string{fileName}, normalizeOverlays(map[string]string{nativePath: "overlay\n"})); err != nil {
		t.Fatalf("first round trip: %v", err)
	}

	// When the next round trip has no overlay for it
	changed, err := session.changedFilesFor([]string{fileName}, nil)
	if err != nil {
		t.Fatalf("second round trip: %v", err)
	}

	// Then the disk text ships, because the worker's overrides map outlives the
	// round trip that filled it and would otherwise serve the stale overlay
	if len(changed) != 1 || changed[0].Text != "disk\n" {
		t.Fatalf("changed = %v, want the disk text resent", changed)
	}

	// And once reverted it stays quiet
	changed, err = session.changedFilesFor([]string{fileName}, nil)
	if err != nil {
		t.Fatalf("third round trip: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("changed = %v, want nothing once the revert landed", changed)
	}
}

func TestCompileFileKeepsOverlaysOnFilesTheSidecarDidNotTransform(t *testing.T) {
	// Given a plugin project compiled one file at a time — so the sidecar is
	// asked to transform main.ts and nothing else — and overlays on both files
	dir := writeOverlayPluginProject(t, "@scope/overlay-partial-fixture", "tagging.js", taggingPlugin,
		"import { label } from \"./helper\";\nexport const tag = \"main\";\nexport const joined = label + label;\n")
	helperPath := filepath.Join(dir, "src", "helper.ts")
	if err := os.WriteFile(helperPath, []byte("export const label = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// When main.ts is compiled with helper.ts overlaid to a string
	got, diags, err := CompileFileDetailedWithOptions(dir, "src/main.ts", ProjectOptions{
		Overlays: map[string]string{helperPath: "export const label = \"text\";\n"},
	})
	if err != nil {
		t.Fatalf("CompileFileDetailedWithOptions: %v (diags: %v)", err, diags)
	}

	// Then the checker still saw the overlay: `+` lowers to string
	// concatenation. The sidecar returns transformed text only for the files it
	// was asked to compile, so rebuilding the program on that alone drops every
	// other overlay the caller sent.
	if !strings.Contains(got, "label .. label") {
		t.Fatalf("helper.ts overlay was dropped from the rebuilt program:\n%s", got)
	}
}

func TestBuildProjectOverlaysReachTransformerPlugins(t *testing.T) {
	// Given a plugin project whose file on disk says one thing, and an overlay
	// that says another
	dir := writeOverlayPluginProject(t, "@scope/overlay-plugin-fixture", "prefix-string.js", prefixStringPlugin,
		"export const phase = \"disk\";\n")
	mainPath := filepath.Join(dir, "src", "main.ts")

	// When the project is built with that overlay
	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{
		Overlays: map[string]string{mainPath: "export const phase = \"overlaid\";\n"},
	})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}

	// Then the transformer ran over the overlay's text, not the disk's
	got := result.Outputs["out/main.luau"]
	if !strings.Contains(got, `local phase = "plugin:overlaid"`) {
		t.Fatalf("transformer did not see the overlay:\n%s", got)
	}
	if strings.Contains(got, "disk") {
		t.Fatalf("disk text survived the overlay:\n%s", got)
	}
}
