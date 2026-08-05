package compile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
