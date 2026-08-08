package compile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"rotor/internal/assetresolve"
	"rotor/internal/cloud"
	"rotor/internal/transformer"
	"rotor/tsgo/ast"
	"rotor/tsgo/vfs/osvfs"
)

// writeCensusProject lays down a package-type project (no Rojo file needed)
// whose src/ holds one file per entry of files, plus the noLib global stubs.
func writeCensusProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := writeProject(t, "@scope/census-fixture", "")
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCompileProjectOverlaysOverrideFileText(t *testing.T) {
	// Given a project whose on-disk main.ts declares nothing
	dir := writeCensusProject(t, map[string]string{"main.ts": "export const fromDisk = 1;\n"})
	mainPath := filepath.Join(dir, "src", "main.ts")

	// When the file text is overridden in memory
	outputs, diags, err := CompileProjectWithOptions(dir, ProjectOptions{
		Overlays: map[string]string{mainPath: "export const fromOverlay = 2;\n"},
	})

	// Then the compiled Luau reflects the overlay, not the disk
	if err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	text, ok := outputs["out/main.luau"]
	if !ok {
		t.Fatalf("out/main.luau missing; outputs: %v", keys(outputs))
	}
	if !strings.Contains(text, "fromOverlay") {
		t.Errorf("compiled output did not use the overlay text:\n%s", text)
	}
	if strings.Contains(text, "fromDisk") {
		t.Errorf("compiled output still used the on-disk text:\n%s", text)
	}
}

func TestCompileProjectOverlaysLeaveDiskUntouched(t *testing.T) {
	// Given a project with a known on-disk file
	const onDisk = "export const fromDisk = 1;\n"
	dir := writeCensusProject(t, map[string]string{"main.ts": onDisk})
	mainPath := filepath.Join(dir, "src", "main.ts")

	// When it is compiled through an overlay
	if _, diags, err := CompileProjectWithOptions(dir, ProjectOptions{
		Overlays: map[string]string{mainPath: "export const fromOverlay = 2;\n"},
	}); err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}

	// Then the file on disk is unchanged
	got, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != onDisk {
		t.Errorf("overlay wrote through to disk: %q", string(got))
	}
}

// transformStateForFile builds the transform-stage State for one project file
// without going through the pre-emit gates, so a deliberately type-broken file
// can be pushed into the transformer the way census mode pushes it.
func transformStateForFile(t *testing.T, dir, relPath string) *transformer.State {
	t.Helper()
	absDir, program, diags, err := newProjectProgram(dir, "")
	if err != nil {
		t.Fatalf("newProjectProgram: %v (diags: %v)", err, diags)
	}
	filePath := absDir + "/" + filepath.ToSlash(relPath)
	sourceFile := program.GetSourceFile(filePath)
	if sourceFile == nil {
		t.Fatalf("source file not in program: %s", filePath)
	}
	program, prepared, traces, diags, err := prepareProjectProgramForCompile(absDir, program, []*ast.SourceFile{sourceFile}, nil)
	if err != nil {
		t.Fatalf("prepareProjectProgramForCompile: %v (diags: %v)", err, diags)
	}
	sourceFile = prepared[0]
	pctx, diags, err := newProjectContext(absDir, program, ProjectOptions{})
	if err != nil {
		t.Fatalf("newProjectContext: %v (diags: %v)", err, diags)
	}
	pctx.sourceTraces = traces
	chk, release := program.GetTypeCheckerForFile(context.Background(), sourceFile)
	t.Cleanup(release)

	state := transformer.NewState(program, chk, sourceFile, transformer.NewDiagService(), transformer.NewMultiState())
	state.SetRojoContext(pctx.rojoContext, pctx.projectType)
	state.Env = pctx.env
	state.Assets = pctx.assets
	state.Files = pctx.files
	state.Stamps = pctx.stamps
	return state
}

func TestTransformPanicYieldsTypedInternalCompilerError(t *testing.T) {
	// Given a file whose identifier resolves to no symbol — the transformer
	// assert at internal/transformer/identifier.go panics on it
	dir := writeCensusProject(t, map[string]string{"main.ts": "export const x = neverDeclared;\n"})
	state := transformStateForFile(t, dir, "src/main.ts")

	// When it is transformed
	_, _, err := transformAndRenderDetailed(state)

	// Then the error is typed and carries the file and the panic stack
	if err == nil {
		t.Fatal("transform of a symbol-less identifier did not fail")
	}
	var ice *InternalCompilerError
	if !errors.As(err, &ice) {
		t.Fatalf("error %T (%v) is not an *InternalCompilerError", err, err)
	}
	if !strings.HasSuffix(filepath.ToSlash(ice.FileName), "src/main.ts") {
		t.Errorf("FileName = %q, want it to name src/main.ts", ice.FileName)
	}
	if ice.Value == nil {
		t.Error("Value is nil, want the recovered panic value")
	}
	if !strings.Contains(string(ice.Stack), "rotor/internal/transformer") {
		t.Errorf("Stack does not name a transformer frame:\n%s", ice.Stack)
	}
	// The rendered message must stay byte-identical to the untyped error it
	// replaces, so existing output and goldens do not move.
	if want := "internal compiler error: transformer: identifier has no symbol"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// censusFiles is the fixture population shared by the census tests: one clean
// file, one only TypeScript objects to, one only the transformer objects to,
// and one that makes the transformer panic (an identifier with no symbol — the
// sole panic class the emission-census measurements found).
var censusFiles = map[string]string{
	"clean.ts":     "export const clean = 1;\n",
	"typebad.ts":   "export const s: string = 5;\n",
	"noany.ts":     "declare const loose: any;\nexport const taken = loose.field;\n",
	"panicking.ts": "export const x = neverDeclared;\n",
}

func censusByFile(census *ProjectDiagnostics) map[string]FileDiagnostics {
	byName := make(map[string]FileDiagnostics, len(census.Files))
	for _, file := range census.Files {
		byName[filepath.Base(filepath.FromSlash(file.FileName))] = file
	}
	return byName
}

func censusFileNames(byName map[string]FileDiagnostics) []string {
	out := make([]string, 0, len(byName))
	for name := range byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestCompileProjectStopsBeforeTransformOnTypeErrors(t *testing.T) {
	// Given a project with one type-broken file and one file the transformer
	// rejects
	dir := writeCensusProject(t, map[string]string{
		"typebad.ts": censusFiles["typebad.ts"],
		"noany.ts":   censusFiles["noany.ts"],
	})

	// When it is compiled without census mode
	outputs, diags, err := CompileProjectWithOptions(dir, ProjectOptions{})

	// Then the type error alone aborts it: the transform stage never runs, so
	// the transformer's own diagnostic is never observed
	if err == nil {
		t.Fatal("compile of a type-broken project succeeded")
	}
	if len(outputs) != 0 {
		t.Errorf("outputs = %v, want none", keys(outputs))
	}
	for _, d := range diags {
		if strings.Contains(d, "not supported") {
			t.Fatalf("transformer diagnostic leaked out of a non-census compile: %q", d)
		}
	}
}

func TestCompileProjectDiagnosticsTransformsPastTypeErrors(t *testing.T) {
	// Given the same project
	dir := writeCensusProject(t, map[string]string{
		"typebad.ts": censusFiles["typebad.ts"],
		"noany.ts":   censusFiles["noany.ts"],
	})

	// When it is compiled through the census entry point
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then the type-broken file is classified and still reaches the transform,
	// and the transformer's diagnostic on the other file is reported too
	byName := censusByFile(census)
	typebad, ok := byName["typebad.ts"]
	if !ok {
		t.Fatalf("typebad.ts missing from the census; got %v", censusFileNames(byName))
	}
	if typebad.Outcome != FileOutcomeTypeError {
		t.Errorf("typebad.ts outcome = %q, want %q", typebad.Outcome, FileOutcomeTypeError)
	}
	if !typebad.Transformed {
		t.Error("typebad.ts did not reach the transform stage in census mode")
	}
	noany, ok := byName["noany.ts"]
	if !ok {
		t.Fatalf("noany.ts missing from the census; got %v", censusFileNames(byName))
	}
	if noany.Outcome != FileOutcomeTransformerDiagnostic {
		t.Errorf("noany.ts outcome = %q (diags %+v), want %q", noany.Outcome, noany.Diagnostics, FileOutcomeTransformerDiagnostic)
	}
}

func TestCompileProjectDiagnosticsCodesNameTheDiagnosticClass(t *testing.T) {
	// Given one file TypeScript rejects and one the transformer rejects
	dir := writeCensusProject(t, map[string]string{
		"typebad.ts": censusFiles["typebad.ts"],
		"noany.ts":   censusFiles["noany.ts"],
	})

	// When they are censused
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then every diagnostic carries a code naming its class. A file's Outcome
	// reports only the most severe of its failures, so it cannot tell a
	// consumer which family an individual diagnostic belongs to.
	byName := censusByFile(census)
	for _, tc := range []struct {
		name     string
		wantCode string
	}{
		{"typebad.ts", "TS2322"},
		{"noany.ts", "noAny"},
	} {
		file, ok := byName[tc.name]
		if !ok || len(file.Diagnostics) == 0 {
			t.Errorf("%s carries no diagnostics: %+v", tc.name, file)
			continue
		}
		if got := file.Diagnostics[0].Code; got != tc.wantCode {
			t.Errorf("%s code = %q, want %q (message %q)", tc.name, got, tc.wantCode, file.Diagnostics[0].Message)
		}
	}
}

func TestTypeScriptDiagnosticCode(t *testing.T) {
	// Given the diagnostic numbers a checker reports
	// When they are rendered for DiagnosticInfo.Code and for `check --json`
	// Then both read as the upstream "TS####" form, from one implementation, so
	// the two paths cannot drift apart
	for _, tc := range []struct {
		code int32
		want string
	}{
		{2322, "TS2322"},
		{0, "TS0"},
	} {
		if got := TypeScriptDiagnosticCode(tc.code); got != tc.want {
			t.Errorf("TypeScriptDiagnosticCode(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestCompileProjectDiagnosticsReportsEveryFailingFile(t *testing.T) {
	// Given a project where files fail in every way the census classifies
	dir := writeCensusProject(t, censusFiles)

	// When the census runs
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then every file has an entry, with the outcome it earned
	byName := censusByFile(census)
	want := map[string]FileOutcome{
		"clean.ts":     FileOutcomeOK,
		"main.ts":      FileOutcomeOK,
		"typebad.ts":   FileOutcomeTypeError,
		"noany.ts":     FileOutcomeTransformerDiagnostic,
		"panicking.ts": FileOutcomeInternalCompilerError,
	}
	for name, wantOutcome := range want {
		file, ok := byName[name]
		if !ok {
			t.Errorf("%s missing from the census; got %v", name, censusFileNames(byName))
			continue
		}
		if file.Outcome != wantOutcome {
			t.Errorf("%s outcome = %q (diags %+v), want %q", name, file.Outcome, file.Diagnostics, wantOutcome)
		}
	}
	if len(census.Files) != len(want) {
		t.Errorf("census covered %d files, want %d: %v", len(census.Files), len(want), censusFileNames(byName))
	}
}

func TestCompileProjectDiagnosticsPanickingFileDoesNotStopTheRest(t *testing.T) {
	// Given a project whose first file by name makes the transformer panic
	dir := writeCensusProject(t, map[string]string{
		"a_panicking.ts": censusFiles["panicking.ts"],
		"b_clean.ts":     "export const afterThePanic = 1;\n",
		"c_clean.ts":     "export const alsoAfter = 2;\n",
	})

	// When the census runs
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then the files after it were still transformed
	byName := censusByFile(census)
	panicking := byName["a_panicking.ts"]
	if panicking.Outcome != FileOutcomeInternalCompilerError {
		t.Fatalf("a_panicking.ts outcome = %q, want %q", panicking.Outcome, FileOutcomeInternalCompilerError)
	}
	if panicking.InternalError == nil {
		t.Error("a_panicking.ts carries no InternalError")
	} else if !strings.Contains(string(panicking.InternalError.Stack), "rotor/internal/transformer") {
		t.Errorf("InternalError.Stack does not name a transformer frame:\n%s", panicking.InternalError.Stack)
	}
	for _, name := range []string{"b_clean.ts", "c_clean.ts"} {
		file, ok := byName[name]
		if !ok {
			t.Errorf("%s missing from the census; got %v", name, censusFileNames(byName))
			continue
		}
		if file.Outcome != FileOutcomeOK || !file.Transformed {
			t.Errorf("%s outcome = %q transformed = %t, want ok/true", name, file.Outcome, file.Transformed)
		}
	}
}

func TestCompileProjectDiagnosticsCountsTransformedFiles(t *testing.T) {
	// Given a project with one file the transformer cannot get through
	dir := writeCensusProject(t, map[string]string{
		"clean.ts":     censusFiles["clean.ts"],
		"panicking.ts": censusFiles["panicking.ts"],
	})

	// When the census runs
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then the transformed count excludes it — a silently shrinking census is
	// the one failure a consumer cannot detect any other way
	if census.Transformed != len(census.Files)-1 {
		t.Errorf("Transformed = %d over %d files, want %d", census.Transformed, len(census.Files), len(census.Files)-1)
	}
}

func TestCompileProjectDiagnosticsOverlayIntroducesTheFailure(t *testing.T) {
	// Given a project that is clean on disk
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	cleanPath := filepath.Join(dir, "src", "clean.ts")

	// When the census runs over an overlay that breaks one file
	census, err := CompileProjectDiagnostics(dir, ProjectOptions{
		Overlays: map[string]string{cleanPath: censusFiles["noany.ts"]},
	})
	if err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then the census reports the overlay's failure, not the disk's success
	byName := censusByFile(census)
	if got := byName["clean.ts"].Outcome; got != FileOutcomeTransformerDiagnostic {
		t.Errorf("clean.ts outcome = %q, want %q (diags %+v)", got, FileOutcomeTransformerDiagnostic, byName["clean.ts"].Diagnostics)
	}
}

func TestCompileProjectOverlayNamingNoFileIsAnError(t *testing.T) {
	// Given a project and an overlay for a file that is not in it — a typo, or
	// a path outside the include set
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	missing := filepath.Join(dir, "src", "typo.ts")

	// When a census is asked for
	_, err := CompileProjectDiagnostics(dir, ProjectOptions{
		Overlays: map[string]string{missing: censusFiles["noany.ts"]},
	})

	// Then it refuses rather than censusing the unmodified tree and calling it
	// green
	if err == nil {
		t.Fatal("an overlay matching no file in the program was accepted")
	}
	if !strings.Contains(err.Error(), "typo.ts") {
		t.Errorf("error = %v, want it to name the unmatched overlay", err)
	}
}

func TestCompileProjectRelativeOverlayKeyIsAnError(t *testing.T) {
	// Given an overlay keyed relatively — the program holds absolute paths, so
	// this can never match
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})

	// When a census is asked for
	_, err := CompileProjectDiagnostics(dir, ProjectOptions{
		Overlays: map[string]string{"src/clean.ts": censusFiles["noany.ts"]},
	})

	// Then it refuses
	if err == nil {
		t.Fatal("a relative overlay key was accepted")
	}
}

func TestCompileProjectOverlayKeysMatchAcrossSeparatorAndCase(t *testing.T) {
	// Given overlay keys written the ways a consumer will actually write them
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	slashed := filepath.ToSlash(filepath.Join(dir, "src", "clean.ts"))
	backslashed := filepath.FromSlash(slashed)

	keys := map[string]string{"slash-separated": slashed, "backslash-separated": backslashed}
	if !osvfs.FS().UseCaseSensitiveFileNames() {
		keys["upper-cased"] = strings.ToUpper(slashed)
	}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			// When the census runs over it
			census, err := CompileProjectDiagnostics(dir, ProjectOptions{
				Overlays: map[string]string{key: censusFiles["noany.ts"]},
			})

			// Then it matched, and is counted
			if err != nil {
				t.Fatalf("CompileProjectDiagnostics: %v", err)
			}
			if census.OverlayMatches != 1 {
				t.Errorf("OverlayMatches = %d, want 1", census.OverlayMatches)
			}
			if got := censusByFile(census)["clean.ts"].Outcome; got != FileOutcomeTransformerDiagnostic {
				t.Errorf("clean.ts outcome = %q, want %q — the overlay did not apply", got, FileOutcomeTransformerDiagnostic)
			}
		})
	}
}

// addTransformerPlugin rewrites the fixture tsconfig to declare a transformer
// plugin, which is all projectUsesTransformerPlugins reads.
func addTransformerPlugin(t *testing.T, dir string) {
	t.Helper()
	configPath := filepath.Join(dir, "tsconfig.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(data), `"compilerOptions": {`,
		`"compilerOptions": {`+"\n\t\t"+`"plugins": [{ "transform": "rbxts-transformer-fixture" }],`, 1)
	if patched == string(data) {
		t.Fatal("tsconfig fixture shape changed; plugin injection did not apply")
	}
	if err := os.WriteFile(configPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompileProjectOverlaysWithTransformerPluginsAreAccepted(t *testing.T) {
	// Given a project with a transformer plugin, and an overlay for a file it
	// holds
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	addTransformerPlugin(t, dir)
	cleanPath := filepath.Join(dir, "src", "clean.ts")

	// When the overlays are matched against the program
	_, program, _, err := newProjectProgramWithOptions(dir, "", ProjectOptions{})
	if err != nil {
		t.Fatalf("newProjectProgramWithOptions: %v", err)
	}
	matched, err := matchProgramOverlays(program, ProjectOptions{
		Overlays: map[string]string{cleanPath: censusFiles["noany.ts"]},
	})

	// Then the plugin does not disqualify them. The worker holds the overlay
	// text, so the census reports on what the caller sent.
	if err != nil {
		t.Fatalf("overlays on a transformer-plugin project were refused: %v", err)
	}
	if matched != 1 {
		t.Errorf("matched = %d, want 1", matched)
	}
}

func TestCompileProjectTransformerPluginsWithoutOverlaysAreUntouched(t *testing.T) {
	// Given the same project with no overlays — the guard must not fire on the
	// stock path, which has its own (working) sidecar handling
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	addTransformerPlugin(t, dir)

	// When the program is built without overlays (stopping short of the sidecar
	// itself, which needs Node)
	if _, _, diags, err := newProjectProgramWithOptions(dir, "", ProjectOptions{}); err != nil {
		// Then the overlay refusal did not fire
		t.Fatalf("overlay-free run failed: %v (diags: %v)", err, diags)
	}
}

func TestCompileProjectDiagnosticsWritesNothingToDisk(t *testing.T) {
	// Given a project and a snapshot of its tree
	dir := writeCensusProject(t, censusFiles)
	before := treeSnapshot(t, dir)

	// When the census runs
	if _, err := CompileProjectDiagnostics(dir, ProjectOptions{}); err != nil {
		t.Fatalf("CompileProjectDiagnostics: %v", err)
	}

	// Then nothing on disk moved — no outDir, no include folder, no rotor.d.ts
	after := treeSnapshot(t, dir)
	if len(after) != len(before) {
		t.Fatalf("tree changed: %d entries before, %d after\nbefore: %v\nafter:  %v", len(before), len(after), before, after)
	}
	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("%s changed on disk", path)
		}
	}
}

// treeSnapshot maps every path under dir to a digest of its contents.
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = "dir"
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// censusCompileOutputs runs the census gates over a project and hands back the
// outputs map compileProjectSourceFiles built — the one
// CompileProjectDiagnostics discards. Nothing else can see it, and it is the
// map a caller who turned census mode on for a *build* would have written to
// disk.
func censusCompileOutputs(t *testing.T, dir string) map[string]string {
	t.Helper()
	opts := ProjectOptions{census: &censusCollector{}}
	absDir, program, diags, err := newProjectProgramWithOptions(dir, "", opts)
	if err != nil {
		t.Fatalf("newProjectProgramWithOptions: %v (diags: %v)", err, diags)
	}
	sourceFiles := projectSourceFiles(program)
	program, sourceFiles, prepTraces, prepDiags, err := prepareProjectProgramForCompile(absDir, program, sourceFiles, nil)
	if err != nil {
		t.Fatalf("prepareProjectProgramForCompile: %v (diags: %v)", err, prepDiags)
	}
	pctx, ctxDiags, err := newProjectContext(absDir, program, opts)
	if err != nil {
		t.Fatalf("newProjectContext: %v (diags: %v)", err, ctxDiags)
	}
	pctx.sourceTraces = prepTraces
	outputs, _, _, err := compileProjectSourceFiles(absDir, program, pctx, sourceFiles, opts)
	if err != nil {
		t.Fatalf("compileProjectSourceFiles: %v", err)
	}
	return outputs
}

func TestCensusNeverEmitsAnOutputForAFileThatDidNotTransform(t *testing.T) {
	// Given a project with a file the transformer panics on, alongside a clean
	// one — the panicking file leaves a zero-value result, whose relOut is ""
	dir := writeCensusProject(t, map[string]string{
		"clean.ts":     censusFiles["clean.ts"],
		"panicking.ts": censusFiles["panicking.ts"],
	})

	// When the census gates run
	outputs := censusCompileOutputs(t, dir)

	// Then no entry was keyed on the empty output path
	if text, ok := outputs[""]; ok {
		t.Errorf(`outputs[""] = %q; a file that never transformed must not reach the map`, text)
	}
	if _, ok := outputs["out/clean.luau"]; !ok {
		t.Errorf("out/clean.luau missing; outputs: %v", keys(outputs))
	}
}

func TestCensusNeverEmitsAnOutputForATypeBrokenFile(t *testing.T) {
	// Given a file TypeScript rejects but the transformer gets through — the
	// transformer uses types for truthiness, coercion and loop lowering, so
	// its Luau for a type-broken file can be silently wrong
	dir := writeCensusProject(t, map[string]string{
		"clean.ts":   censusFiles["clean.ts"],
		"typebad.ts": censusFiles["typebad.ts"],
	})

	// When the census gates run
	outputs := censusCompileOutputs(t, dir)

	// Then its output is not in the map
	if text, ok := outputs["out/typebad.luau"]; ok {
		t.Errorf("out/typebad.luau = %q; type-broken output must not reach the map", text)
	}
	if _, ok := outputs["out/clean.luau"]; !ok {
		t.Errorf("out/clean.luau missing; outputs: %v", keys(outputs))
	}
}

func TestCensusCollectorAccumulatesProjectDiagnostics(t *testing.T) {
	// Given a collector
	collector := &censusCollector{}

	// When project-level diagnostics arrive from more than one gate — gate 1's
	// program options and gate 3's global checker diagnostics both land here
	collector.addProjectDiagnostics([]DiagnosticInfo{{Message: "gate 1"}})
	collector.addProjectDiagnostics(nil)
	collector.addProjectDiagnostics([]DiagnosticInfo{{Message: "gate 3a"}, {Message: "gate 3b"}})

	// Then all of them are kept, in order
	want := []string{"gate 1", "gate 3a", "gate 3b"}
	if len(collector.diagnostics) != len(want) {
		t.Fatalf("diagnostics = %+v, want %d entries", collector.diagnostics, len(want))
	}
	for i, message := range want {
		if collector.diagnostics[i].Message != message {
			t.Errorf("diagnostics[%d] = %q, want %q", i, collector.diagnostics[i].Message, message)
		}
	}
}

// assetResolverClientIsNil reads the resolver's unexported cloud client. It is
// the only observable difference between an offline and an online resolver, and
// the whole point of the census wiring.
func assetResolverClientIsNil(resolver *assetresolve.Resolver) bool {
	return reflect.ValueOf(resolver).Elem().FieldByName("client").IsNil()
}

func TestProjectContextGivesTheCensusAnOfflineAssetResolver(t *testing.T) {
	// Given an Open Cloud key and a configured creator — the setup where a
	// cache-missing $asset would upload
	t.Setenv("ROBLOX_API_KEY", "not-a-real-key")
	dir := writeCensusProject(t, map[string]string{"clean.ts": censusFiles["clean.ts"]})
	if err := os.WriteFile(filepath.Join(dir, "rotor.toml"),
		[]byte("[assets.creator]\ntype = \"user\"\nid = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	absDir, program, diags, err := newProjectProgram(dir, "")
	if err != nil {
		t.Fatalf("newProjectProgram: %v (diags: %v)", err, diags)
	}

	// When a project context is built for a census and for a build
	censusCtx, diags, err := newProjectContext(absDir, program, ProjectOptions{census: &censusCollector{}})
	if err != nil {
		t.Fatalf("newProjectContext (census): %v (diags: %v)", err, diags)
	}
	buildCtx, diags, err := newProjectContext(absDir, program, ProjectOptions{})
	if err != nil {
		t.Fatalf("newProjectContext (build): %v (diags: %v)", err, diags)
	}

	// Then only the build one can reach the cloud. The lockfile persist lives
	// only on the Build path, so a census upload would be forgotten and
	// repeated on every run.
	if !assetResolverClientIsNil(censusCtx.assets) {
		t.Error("the census project context got a cloud client")
	}
	if assetResolverClientIsNil(buildCtx.assets) {
		t.Error("the build project context lost its cloud client")
	}
}

func TestCensusAssetResolverNeverUploads(t *testing.T) {
	// Given an Open Cloud key in the environment and a configured creator —
	// the setup where a cache-missing $asset would upload
	t.Setenv("ROBLOX_API_KEY", "not-a-real-key")
	creator := cloud.Creator{UserID: 1}

	// When the asset cloud client is built for a census run
	censusClient := assetCloudClient(creator, true)

	// Then it is absent, so a census can never upload. The lockfile persist
	// lives only on the Build path, so an upload here would be forgotten and
	// repeated on the next run.
	if censusClient != nil {
		t.Error("census asset resolver got a cloud client")
	}
	// And an ordinary build still gets one.
	if assetCloudClient(creator, false) == nil {
		t.Error("non-census asset resolver lost its cloud client")
	}
}
