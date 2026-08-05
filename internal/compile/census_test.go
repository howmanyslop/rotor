package compile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"rotor/internal/cloud"
	"rotor/internal/transformer"
	"rotor/tsgo/ast"
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
	program, prepared, diags, err := prepareProjectProgramForCompile(absDir, program, []*ast.SourceFile{sourceFile})
	if err != nil {
		t.Fatalf("prepareProjectProgramForCompile: %v (diags: %v)", err, diags)
	}
	sourceFile = prepared[0]
	pctx, diags, err := newProjectContext(absDir, program, ProjectOptions{})
	if err != nil {
		t.Fatalf("newProjectContext: %v (diags: %v)", err, diags)
	}
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

func TestPrecheckGuardRecoversPanicIntoTypedError(t *testing.T) {
	// Given a precheck that panics — it runs inside a work-group goroutine,
	// where an unrecovered panic takes the whole process down
	const fileName = "/project/src/main.ts"

	// When it is run behind the guard
	result := runPrecheckGuarded(fileName, func() precheckedProjectSourceFile {
		panic("checker exploded")
	})

	// Then the panic is captured as a typed error naming the file
	if result.panicErr == nil {
		t.Fatal("precheck panic was not captured")
	}
	if result.panicErr.FileName != fileName {
		t.Errorf("FileName = %q, want %q", result.panicErr.FileName, fileName)
	}
	if result.panicErr.Value != "checker exploded" {
		t.Errorf("Value = %v, want the panic value", result.panicErr.Value)
	}
	if len(result.panicErr.Stack) == 0 {
		t.Error("Stack is empty")
	}
}

func TestPrecheckGuardPassesThroughNormalResults(t *testing.T) {
	// Given a precheck that returns normally
	want := precheckedProjectSourceFile{commentDiags: []string{"a directive diagnostic"}}

	// When it is run behind the guard
	result := runPrecheckGuarded("/project/src/main.ts", func() precheckedProjectSourceFile {
		return want
	})

	// Then the result is untouched
	if result.panicErr != nil {
		t.Fatalf("unexpected panic error: %v", result.panicErr)
	}
	if len(result.commentDiags) != 1 || result.commentDiags[0] != want.commentDiags[0] {
		t.Errorf("commentDiags = %v, want %v", result.commentDiags, want.commentDiags)
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
