package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOverlayPluginProject is the shared fixture for the sidecar overlay
// tests: the prefix-string plugin over a single source file, so the output
// names both the text the transformer saw and the fact that it ran.
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

func TestBuildProjectOverlaysReachTransformerPlugins(t *testing.T) {
	// Given a plugin project whose file on disk says one thing, and an overlay
	// that says another
	dir := writeOverlayPluginProject(t, "@scope/overlay-plugin-fixture", "export const phase = \"disk\";\n")
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
