package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOverlayPluginProject is the shared fixture for the sidecar overlay
// tests: prefixStringPlugin over a src/main.ts, which is enough to route the
// project through the sidecar. The plugin has to change something — the worker
// reports a file as transformed only when the transform returned a new node,
// and an unchanged project takes the early return instead of the program
// rebuild — so every diskText here carries a string literal.
func writeOverlayPluginProject(t *testing.T, pkgName, diskText string) string {
	t.Helper()
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, pkgName, "")
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
			{
				"transform": "./plugins/prefix-string.js",
				"prefix": "plugin"
			}
		]
	},
	"include": ["src"]
}`)
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
	changed, err := session.changedFilesFor([]string{fileName}, map[string]string{nativePath: "overlay\n"})
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

func TestChangedFilesForShipsOverlaysOutsideTheStampedSet(t *testing.T) {
	// Given an overlay for a file the stamp list does not carry. The stamp list
	// is projectSourceFiles, which drops declaration files, project-reference
	// sources and anything that is not .ts/.tsx — while an overlay only has to
	// name a file the program holds.
	stamped, _ := writeOverlaySourceFile(t, "disk\n")
	ambient := filepath.Join(filepath.Dir(stamped), "types.d.ts")
	if err := os.WriteFile(ambient, []byte("declare const disk: number;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := newOverlayTestSession()

	// When the round trip's changed files are collected
	changed, err := session.changedFilesFor([]string{stamped}, map[string]string{ambient: "declare const overlaid: number;\n"})
	if err != nil {
		t.Fatalf("changedFilesFor: %v", err)
	}

	// Then it ships anyway, under the caller's spelling. An overlay the worker
	// never hears about leaves its plugins typing against disk while rotor's
	// own program types against the overlay — the two disagree silently.
	if len(changed) != 1 {
		t.Fatalf("changed = %v, want the unstamped overlay", changed)
	}
	if changed[0].FileName != ambient {
		t.Errorf("fileName = %q, want %q", changed[0].FileName, ambient)
	}
	if changed[0].Text != "declare const overlaid: number;\n" {
		t.Errorf("text = %q, want the overlay's", changed[0].Text)
	}
}

func TestChangedFilesForRevertsAnOverlayThatWentAway(t *testing.T) {
	// Given a session that shipped an overlay on an earlier round trip
	fileName, nativePath := writeOverlaySourceFile(t, "disk\n")
	session := newOverlayTestSession()
	if _, err := session.changedFilesFor([]string{fileName}, map[string]string{nativePath: "overlay\n"}); err != nil {
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

// TestCompileProjectDiagnosticsCensusesAPluginProjectUnderOverlays is the
// product case the refusal used to block: `sloptor diagnostics` over a real
// build tsconfig, plugins and all, reporting on the caller's text.
func TestCompileProjectDiagnosticsCensusesAPluginProjectUnderOverlays(t *testing.T) {
	// Given a plugin project that censuses clean, and an overlay that breaks
	// one file's types and removes the export another file imports
	dir := writeOverlayPluginProject(t, "@scope/overlay-census-fixture", "import { label } from \"./helper\";\nexport const shout = label + \"!\";\n")
	helperPath := filepath.Join(dir, "src", "helper.ts")
	if err := os.WriteFile(helperPath, []byte("export const label = \"disk\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	control, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("census of the unmodified tree: %v (%v)", err, control.Diagnostics)
	}
	for _, file := range control.Files {
		if file.Outcome != FileOutcomeOK {
			t.Fatalf("unmodified tree is not clean: %s is %q", file.FileName, file.Outcome)
		}
	}

	// When the census runs with that overlay
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{
		Overlays: map[string]string{helperPath: "export const gone: number = \"not a number\";\n"},
	})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v (%v)", err, census.Diagnostics)
	}

	// Then the overlay is counted, its own type error is reported against it,
	// and the file that imported what it removed is reported too
	if census.OverlayMatches != 1 {
		t.Errorf("OverlayMatches = %d, want 1", census.OverlayMatches)
	}
	byFile := censusByFile(census)
	if got := byFile["helper.ts"].Outcome; got != FileOutcomeTypeError {
		t.Errorf("helper.ts outcome = %q, want %q", got, FileOutcomeTypeError)
	}
	if got := byFile["main.ts"].Outcome; got != FileOutcomeTypeError {
		t.Errorf("main.ts outcome = %q, want %q — the overlay's removed export did not propagate", got, FileOutcomeTypeError)
	}
}

func TestCompileFileKeepsOverlaysOnFilesTheSidecarDidNotTransform(t *testing.T) {
	// Given a plugin project compiled one file at a time — so the sidecar is
	// asked to transform main.ts and nothing else — and overlays on both files
	dir := writeOverlayPluginProject(t, "@scope/overlay-partial-fixture",
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

func TestBuildProjectOverlaysReachDeclarationPathRewriting(t *testing.T) {
	// Given a project that emits declarations through `paths`, and an overlay
	// that is the only thing importing the alias
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeProject(t, "@scope/overlay-declaration-fixture", "")
	t.Cleanup(closeSidecarSessions)
	writeSidecarPluginFixture(t, dir, "", strings.Replace(
		sidecarDeclarationConfig(`[]`),
		`"outDir": "out",`,
		`"outDir": "out", "baseUrl": ".", "paths": { "@alias/*": ["src/*"] },`,
		1,
	))
	if err := os.WriteFile(filepath.Join(dir, "src", "value.ts"), []byte("export interface Value { label: string; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "src", "main.ts")
	if err := os.WriteFile(mainPath, []byte("export type Output = string;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// When the project is built with that overlay
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{
		Overlays: map[string]string{mainPath: "import type { Value } from \"@alias/value\";\nexport type Output = Value;\n"},
	}); err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}

	// Then the declaration carries the overlay's import, rewritten off the
	// alias — the emit saw the overlay and resolved against the same file view
	declaration, err := os.ReadFile(filepath.Join(dir, "out", "main.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(declaration), `from "./value"`) {
		t.Fatalf("declaration did not come from the overlay:\n%s", declaration)
	}
}

func TestBuildProjectOverlaysReachTransformerPlugins(t *testing.T) {
	// Given a plugin project whose file on disk says one thing, and an overlay
	// that says another
	dir := writeOverlayPluginProject(t, "@scope/overlay-plugin-fixture",
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
