package compile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// applierShapedPositionPlugin covers the three node shapes a React-compiler-style
// transform produces that hoistingPositionPlugin above never reaches. All three
// happen at once, inside one function body:
//
//   - bookkeeping statements are spliced ABOVE the code they memoize, so the
//     authored statement below them moves between the file on disk and the
//     reprint the checker sees;
//   - a node the transform SYNTHESIZES is handed the authored range it stands
//     for (ts.setSourceMapRange, exactly as roblox-ts' own transformPaths does),
//     so it has to resolve to authored text that is nowhere near where it was
//     printed;
//   - the identifiers the transform invents for its own bookkeeping are given
//     no range at all, because no authored text corresponds to them. Those can
//     only resolve coarsely — to the enclosing spliced statement's authored
//     range — and the thing that must NOT happen is that they drift onto some
//     unrelated line.
//
// Each of the three unresolved names below exercises exactly one of those.
const applierShapedPositionPlugin = `const ts = require("typescript");

function findDeclaration(statements, name) {
	return statements.find(
		(statement) =>
			ts.isVariableStatement(statement) &&
			statement.declarationList.declarations.length === 1 &&
			ts.isIdentifier(statement.declarationList.declarations[0].name) &&
			statement.declarationList.declarations[0].name.text === name,
	);
}

function memoCacheSlot() {
	return ts.factory.createElementAccessExpression(
		ts.factory.createIdentifier("memoCache"),
		ts.factory.createNumericLiteral("0"),
	);
}

module.exports = function () {
	return (context) => (sourceFile) => {
		const enclosing = sourceFile.statements.find(
			(statement) =>
				ts.isFunctionDeclaration(statement) &&
				statement.name !== undefined &&
				statement.name.text === "enclosingFunction",
		);
		if (enclosing === undefined || enclosing.body === undefined) {
			throw new Error("fixture no longer declares enclosingFunction");
		}

		const dependency = findDeclaration(enclosing.body.statements, "dependency");
		const target = findDeclaration(enclosing.body.statements, "memoized");
		if (dependency === undefined || target === undefined) {
			throw new Error("fixture no longer has the statements the plugin splices around");
		}

		// No authored counterpart: the allocator is bookkeeping this transform
		// invented. The STATEMENT carries the authored range of what it
		// memoizes; the identifier inside it carries none.
		const cacheAllocation = ts.factory.createVariableStatement(
			undefined,
			ts.factory.createVariableDeclarationList(
				[
					ts.factory.createVariableDeclaration(
						"memoCache",
						undefined,
						undefined,
						ts.factory.createCallExpression(
							ts.factory.createIdentifier("bookkeepingMissingAlloc"),
							undefined,
							[ts.factory.createNumericLiteral("2")],
						),
					),
				],
				ts.NodeFlags.Const,
			),
		);
		ts.setSourceMapRange(cacheAllocation, ts.getSourceMapRange(target));

		// Synthesized, but standing in for an authored identifier — so it is
		// handed that identifier's range.
		const guardOperand = ts.factory.createIdentifier("synthesizedMissingGuard");
		ts.setSourceMapRange(
			guardOperand,
			ts.getSourceMapRange(dependency.declarationList.declarations[0].name),
		);

		const guard = ts.factory.createIfStatement(
			ts.factory.createBinaryExpression(
				memoCacheSlot(),
				ts.SyntaxKind.ExclamationEqualsEqualsToken,
				guardOperand,
			),
			ts.factory.createBlock(
				[
					ts.factory.createExpressionStatement(
						ts.factory.createAssignment(memoCacheSlot(), ts.factory.createNumericLiteral("1")),
					),
				],
				true,
			),
		);

		const body = [];
		for (const statement of enclosing.body.statements) {
			if (statement === target) {
				body.push(cacheAllocation, guard);
			}
			body.push(statement);
		}

		return ts.factory.updateSourceFile(
			sourceFile,
			sourceFile.statements.map((statement) =>
				statement === enclosing
					? ts.factory.updateFunctionDeclaration(
							enclosing,
							enclosing.modifiers,
							enclosing.asteriskToken,
							enclosing.name,
							enclosing.typeParameters,
							enclosing.parameters,
							enclosing.type,
							ts.factory.updateBlock(enclosing.body, body),
						)
					: statement,
			),
		);
	};
};
`

// applierPositionFixtureSource is padded the same way positionFixtureSource is,
// so a reprint (which collapses the padding and turns tabs into spaces) moves
// every authored position. The three unresolved names it has to produce are
// named for the shape they stand for.
const applierPositionFixtureSource = "export const SEED = 1;\n" + // 1
	"\n" + // 2
	"// The imports that would declare the missing names below are deliberately absent.\n" + // 3
	"\n" + // 4
	"/**\n" + // 5
	" * Doc comment padding.\n" + // 6
	" *\n" + // 7
	" * @returns Nothing.\n" + // 8
	" */\n" + // 9
	"export function enclosingFunction(): void {\n" + // 10
	"\tconst dependency = SEED;\n" + // 11  `dependency` at 11:8
	"\tconst memoized = authoredMissingCall(dependency);\n" + // 12  statement at 12:2, call at 12:19
	"\n" + // 13
	"\tprint(memoized);\n" + // 14
	"}\n" // 15

const applierPositionFixtureTSConfig = `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"sourceMap": true,
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"plugins": [{ "transform": "./plugins/applier.js" }]
	},
	"include": ["src"]
}`

func writeApplierPositionFixture(t *testing.T) string {
	t.Helper()
	dir := writeProject(t, "@scope/applier-pos-fixture", "")
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(applierPositionFixtureTSConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "applier.js"), []byte(applierShapedPositionPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(applierPositionFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// unresolvedName pulls the identifier out of "Cannot find name 'x'." so the
// three diagnostics the fixture produces can be told apart. Matching on the
// name rather than on ordering keeps the test from depending on the order the
// checker happens to report them in.
func unresolvedName(message string) string {
	open := strings.Index(message, "'")
	if open < 0 {
		return ""
	}
	close := strings.Index(message[open+1:], "'")
	if close < 0 {
		return ""
	}
	return message[open+1 : open+1+close]
}

func unresolvedNamePositions(t *testing.T) map[string]DiagnosticInfo {
	t.Helper()
	setRepoSidecarPath(t)
	closeSidecarSessions()
	dir := writeApplierPositionFixture(t)
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

	byName := map[string]DiagnosticInfo{}
	for _, file := range census.Files {
		for _, d := range file.Diagnostics {
			t.Logf("%s: %s at offset %d len %d -> %d:%d — %s", filepath.Base(file.FileName), d.Code, d.Offset, d.Len, d.Line, d.Col, d.Message)
			if d.Code == "TS2304" {
				byName[unresolvedName(d.Message)] = d
			}
		}
	}
	return byName
}

// assertAuthoredPosition pins the contract assertLocatesUseMountEffect pins,
// for a diagnostic whose span length is the SYNTHESIZED token's rather than the
// authored one's: the offset still has to index the file on disk, and the text
// it lands on has to be the authored construct the position claims.
func assertAuthoredPosition(t *testing.T, d DiagnosticInfo, line, col int, authored string) {
	t.Helper()
	if d.Line != line || d.Col != col {
		t.Errorf("line/col = %d/%d, want %d/%d (%q in main.ts on disk)", d.Line, d.Col, line, col, authored)
	}
	disk, err := os.ReadFile(filepath.FromSlash(d.FileName))
	if err != nil {
		t.Fatalf("read the file the diagnostic names: %v", err)
	}
	if d.Offset < 0 || d.Offset+len(authored) > len(disk) {
		t.Fatalf("offset %d does not index the %d-byte file on disk", d.Offset, len(disk))
	}
	if got := string(disk[d.Offset : d.Offset+len(authored)]); got != authored {
		t.Errorf("disk at offset %d = %q, want %q", d.Offset, got, authored)
	}
}

// TestDiagnosticPositionSurvivesAnApplierShapedTransform is the round trip for
// the node shapes a React-compiler-style transform actually emits. The hoisting
// plugin above only ever reports on an authored node that the transform left
// alone; nothing there says what happens to a diagnostic raised ON something
// the transform built.
func TestDiagnosticPositionSurvivesAnApplierShapedTransform(t *testing.T) {
	byName := unresolvedNamePositions(t)

	for _, name := range []string{"authoredMissingCall", "synthesizedMissingGuard", "bookkeepingMissingAlloc"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("no TS2304 for %s; the plugin did not splice what the test assumes (got %v)", name, byName)
		}
	}

	// Authored, and pushed forward by the statements spliced above it.
	t.Run("authored node pushed forward by the splice", func(t *testing.T) {
		d := byName["authoredMissingCall"]
		assertAuthoredPosition(t, d, 12, 19, "authoredMissingCall")
		if want := len("authoredMissingCall"); d.Len != want {
			t.Errorf("len = %d, want %d", d.Len, want)
		}
	})

	// Synthesized, carrying the authored range of the identifier it stands in
	// for. It is printed inside the spliced guard, two statements above and on
	// a different line from where it has to resolve — so landing on
	// `dependency` cannot be an accident of the position not moving.
	t.Run("synthesized node carrying an attached authored range", func(t *testing.T) {
		assertAuthoredPosition(t, byName["synthesizedMissingGuard"], 11, 8, "dependency")
	})

	// No authored counterpart at all. It resolves coarsely, to the start of the
	// authored statement its enclosing spliced statement stands for — not to
	// wherever the reprint happened to put it.
	t.Run("generated node with no authored counterpart", func(t *testing.T) {
		assertAuthoredPosition(t, byName["bookkeepingMissingAlloc"], 12, 2, "const memoized")
	})
}

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
