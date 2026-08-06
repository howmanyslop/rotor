package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// hoistingPositionPlugin mimics the shape of a real roblox-ts transformer
// plugin: it returns a new SourceFile, so the sidecar reprints the whole file
// and the overlay program's text no longer matches the bytes on disk. Hoisting
// a declaration above the existing statements is what a React-compiler-style
// `after` transformer does, and it is the shape that moves positions furthest.
const hoistingPositionPlugin = `const ts = require("typescript");

module.exports = function () {
	return (context) => (sourceFile) =>
		ts.factory.updateSourceFile(sourceFile, [
			ts.factory.createVariableStatement(
				undefined,
				ts.factory.createVariableDeclarationList(
					[
						ts.factory.createVariableDeclaration(
							"__hoisted_by_the_transformer",
							undefined,
							undefined,
							ts.factory.createNumericLiteral("1"),
						),
					],
					ts.NodeFlags.Const,
				),
			),
			...sourceFile.statements,
		]);
};
`

// positionFixtureSource puts the unresolved name on line 12, column 2 (a tab then the
// identifier). Everything above it is comment/blank-line padding that a
// reprint collapses, so a position resolved against the transformed text drifts
// forward.
const positionFixtureSource = "export const SEED = 1;\n" + // 1
	"\n" + // 2
	"// The import that would declare the call below is deliberately absent.\n" + // 3
	"\n" + // 4
	"/**\n" + // 5
	" * Doc comment padding.\n" + // 6
	" *\n" + // 7
	" * @returns Nothing.\n" + // 8
	" */\n" + // 9
	"export function enclosingFunction(): void {\n" + // 10
	"\tconst first = SEED;\n" + // 11
	"\tuseMountEffect(first);\n" + // 12  <- want 12:2
	"\n" + // 13
	"\tconst second = first;\n" + // 14
	"\n" + // 15
	"\tfunction nestedHelper(): void {\n" + // 16
	"\t\tconst third = second;\n" + // 17
	"\t\tprint(third);\n" + // 18
	"\t}\n" + // 19
	"\n" + // 20
	"\tnestedHelper();\n" + // 21
	"}\n" // 22

const positionFixtureTSConfig = `{
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
		"outDir": "out",%s
		"plugins": [{ "transform": "./plugins/hoist.js" }]
	},
	"include": ["src"]
}`

func writePositionFixture(t *testing.T, withPlugin, sourceMap bool) string {
	t.Helper()
	dir := writeProject(t, "@scope/pos-repro-fixture", "")
	sourceMapOption := ""
	if sourceMap {
		sourceMapOption = "\n\t\t\"sourceMap\": true,"
	}
	tsconfig := fmt.Sprintf(positionFixtureTSConfig, sourceMapOption)
	if !withPlugin {
		tsconfig = `{
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
}`
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "hoist.js"), []byte(hoistingPositionPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(positionFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func positionOfUnresolvedName(t *testing.T, withPlugin, sourceMap bool) DiagnosticInfo {
	t.Helper()
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writePositionFixture(t, withPlugin, sourceMap)
	t.Cleanup(closeSidecarSessions)

	census, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		if census != nil {
			for _, d := range census.Diagnostics {
				t.Logf("census diag: %s", d.Message)
			}
		}
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}
	for _, file := range census.Files {
		for _, d := range file.Diagnostics {
			t.Logf("%s: %s at offset %d len %d -> %d:%d", filepath.Base(file.FileName), d.Code, d.Offset, d.Len, d.Line, d.Col)
			if d.Code == "TS2304" {
				return d
			}
		}
	}
	for _, d := range census.Diagnostics {
		t.Logf("project: %s at offset %d len %d -> %d:%d — %s", d.Code, d.Offset, d.Len, d.Line, d.Col, d.Message)
		if d.Code == "TS2304" {
			return d
		}
	}
	t.Fatalf("no TS2304 diagnostic in census")
	return DiagnosticInfo{}
}

// assertLocatesUseMountEffect pins the whole contract a reported position has
// to satisfy, in the two coordinate systems rotor's two consumers use.
//
// Offset is the one that matters and the one that was wrong: the code frame
// (cmd/rotor/ui.go renderDiagFrames) reads the file off DISK and hands the
// offset to diagframe, which derives line:col from the disk line map. So an
// offset that indexes anything else does not merely mislocate the caret, it
// makes the frame's own line number a third answer, agreeing with neither the
// authored file nor the text the offset came from.
func assertLocatesUseMountEffect(t *testing.T, d DiagnosticInfo) {
	t.Helper()
	if d.Line != 12 || d.Col != 2 {
		t.Errorf("line/col = %d/%d, want 12/2 (useMountEffect in main.ts on disk)", d.Line, d.Col)
	}
	if want := len("useMountEffect"); d.Len != want {
		t.Errorf("len = %d, want %d", d.Len, want)
	}
	disk, err := os.ReadFile(filepath.FromSlash(d.FileName))
	if err != nil {
		t.Fatalf("read the file the diagnostic names: %v", err)
	}
	if d.Offset < 0 || d.Offset+d.Len > len(disk) {
		t.Fatalf("offset %d len %d does not index the %d-byte file on disk", d.Offset, d.Len, len(disk))
	}
	if got := string(disk[d.Offset : d.Offset+d.Len]); got != "useMountEffect" {
		t.Errorf("disk[offset:offset+len] = %q, want %q", got, "useMountEffect")
	}
}

func TestDiagnosticPositionWithoutTransformer(t *testing.T) {
	assertLocatesUseMountEffect(t, positionOfUnresolvedName(t, false, true))
}

func TestDiagnosticPositionSurvivesATransformerReprint(t *testing.T) {
	assertLocatesUseMountEffect(t, positionOfUnresolvedName(t, true, true))
}

// TestDiagnosticPositionSurvivesATransformerWithoutSourceMaps is the same bug
// on a project that does not emit source maps. The trace map is what puts a
// position back on disk, so the sidecar has to produce one for its own sake,
// not the emit's.
func TestDiagnosticPositionSurvivesATransformerWithoutSourceMaps(t *testing.T) {
	assertLocatesUseMountEffect(t, positionOfUnresolvedName(t, true, false))
}

// TestBuildDiagnosticPositionSurvivesATransformerReprint covers the BUILD path
// rather than the census path. The two reach compileProjectSourceFiles through
// different callers, each of which builds its own projectContext — so carrying
// the traces on one of them is not carrying them on both, and `rotor build`
// (the path that renders code frames, and the one users actually see) can be
// wrong while `rotor diagnostics` is right.
func TestBuildDiagnosticPositionSurvivesATransformerReprint(t *testing.T) {
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writePositionFixture(t, true, true)
	t.Cleanup(closeSidecarSessions)

	result, _, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err == nil {
		t.Fatal("build of a project with an unresolved name succeeded")
	}
	if result == nil {
		t.Fatal("failed build carries no BuildResult, so no diagnostic to locate")
	}
	for _, d := range result.Diagnostics {
		if d.Code == "TS2304" {
			assertLocatesUseMountEffect(t, d)
			return
		}
	}
	t.Fatalf("no TS2304 diagnostic in the build result: %+v", result.Diagnostics)
}
