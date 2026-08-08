// Package compile wires program creation, transformation, and rendering into
// the project-aware entry points (the Go analog of upstream
// Project/functions/compileFiles.ts): CompileProject for whole projects and
// CompileFile as the single-file fast path the per-fixture tests use.
package compile

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"

	"rotor/internal/luau/render"
	"rotor/internal/transformer"
	"rotor/tsgo/ast"
	"rotor/tsgo/compiler"
	"rotor/tsgo/scanner"
)

// DiagnosticInfo carries the structured form of a compile diagnostic. Every
// diagnostic carries a stable Code: the upstream-style factory name for a rotor
// transformer diagnostic (for example "noAny"), and "TS####" for a TypeScript
// one (see TypeScriptDiagnosticCode). Code is empty only where there was never
// one to carry — a project-setup failure reported as a bare message.
// FileName, Offset, and Len locate the diagnostic span within the source file
// for code-frame rendering; FileName is empty when no source location is
// available, and Len is 0 when no usable span was found.
type DiagnosticInfo struct {
	Code    string
	Message string
	Warning bool

	FileName string // empty when the diagnostic has no source location
	Offset   int    // byte offset of the span start into the file's source
	Len      int    // span length in bytes; 0 means "no usable span"

	// Line and Col are the 1-based position of Offset, resolved against the
	// source text the compile actually used. Both are 0 when there is no
	// location. Resolving them here rather than re-reading the file is what
	// makes positions correct under ProjectOptions.Overlays, where the text on
	// disk is not the text that was compiled.
	Line int
	Col  int
}

// CompileFile compiles projectDir/relPath to Luau source text. It returns the
// rendered text, any diagnostics as strings (TypeScript config/option/
// semantic diagnostics, project-validation failures, or transformer
// diagnostics), and a hard error. When diagnostics are returned the text is
// empty — mirroring upstream compileFiles.ts, which bails before transforming
// on pre-emit errors and before rendering on transformer errors.
//
// CompileFile deliberately keeps a single-file fast path instead of wrapping
// CompileProject: it builds the same Program and project context (Rojo
// resolver, ProjectType, runtimeLibRbxPath validation) but transforms only
// the requested file, so per-fixture tests stay isolated and fast. The diff
// harness migrates to CompileProject (Phase 3a Task 6).
func CompileFile(projectDir, relPath string) (string, []string, error) {
	text, diags, err := CompileFileDetailed(projectDir, relPath)
	return text, diagnosticInfoMessages(diags), err
}

// CompileFileDetailed is CompileFile's structured sibling. It preserves
// transformer diagnostic codes so higher-level conformance tests can assert
// exact upstream diagnostic IDs instead of scraping message text.
func CompileFileDetailed(projectDir, relPath string) (string, []DiagnosticInfo, error) {
	return CompileFileDetailedWithOptions(projectDir, relPath, ProjectOptions{})
}

// CompileFileWithOptions is CompileFile with ProjectOptions plumbed through
// the project-layer setup.
func CompileFileWithOptions(projectDir, relPath string, opts ProjectOptions) (string, []string, error) {
	text, diags, err := CompileFileDetailedWithOptions(projectDir, relPath, opts)
	return text, diagnosticInfoMessages(diags), err
}

// CompileFileDetailedWithOptions is the options-aware single-file fast path.
func CompileFileDetailedWithOptions(projectDir, relPath string, opts ProjectOptions) (string, []DiagnosticInfo, error) {
	dir, program, diags, err := newProjectProgramWithOptions(projectDir, opts.TsConfigPath, opts)
	if err != nil {
		return "", stringDiagnostics(diags), err
	}

	filePath := dir + "/" + filepath.ToSlash(relPath)
	sourceFile := program.GetSourceFile(filePath)
	if sourceFile == nil {
		return "", nil, fmt.Errorf("compile: source file not in program: %s", filePath)
	}
	program, preparedFiles, traces, diags, err := prepareProjectProgramForCompile(dir, program, []*ast.SourceFile{sourceFile}, opts.Overlays)
	if err != nil {
		return "", stringDiagnostics(diags), err
	}
	sourceFile = preparedFiles[0]

	pctx, diags, err := newProjectContext(dir, program, opts)
	if err != nil {
		return "", stringDiagnostics(diags), err
	}
	pctx.sourceTraces = traces
	ctx := context.Background()

	// Program-level option diagnostics (e.g. removed-option checks) plus this
	// file's pre-emit diagnostics (syntactic + semantic + checker globals); any
	// of them fails the compile before transforming, mirroring upstream's
	// pre-emit bail (compileFiles.ts:151-158).
	tsDiags := program.GetProgramDiagnostics()
	tsDiags = append(tsDiags, preEmitDiagnostics(ctx, program, sourceFile)...)
	if len(tsDiags) > 0 {
		return "", tsDiagnosticInfos(tsDiags, pctx.sourceTraces), errors.New("compile: TypeScript diagnostics")
	}
	if !opts.AllowCommentDirectives {
		if diags := commentDirectiveDiagnostics(sourceFile); len(diags) > 0 {
			return "", stringDiagnostics(diags), errors.New("compile: comment directive diagnostics")
		}
	}

	chk, release := program.GetTypeCheckerForFile(ctx, sourceFile)
	defer release()

	state := transformer.NewState(program, chk, sourceFile, transformer.NewDiagService(), transformer.NewMultiState())
	// Macro registration audit (digest §6): upstream's MacroManager
	// constructor throws ProjectError before any emit when a registration
	// name fails to resolve; rotor fails the compile here with the same
	// texts (sentinel-gated — see MacroManager.Missing).
	if missing := state.Macros().Missing(); len(missing) > 0 {
		return "", stringDiagnostics(missing), errors.New("compile: macro registration failure")
	}
	state.SetRojoContext(pctx.rojoContext, pctx.projectType)
	state.Env = pctx.env
	state.Assets = pctx.assets
	state.Files = pctx.files
	state.Stamps = pctx.stamps
	return transformAndRenderDetailed(state)
}

// transformAndRender runs the transformer and renderer behind a recover
// boundary: the transformer panics on internal invariant violations (ported
// upstream asserts — missing symbols, prereq-stack misuse), and a user's
// source must surface as an error, never crash the process.
func transformAndRender(state *transformer.State) (text string, diags []string, err error) {
	text, infos, err := transformAndRenderDetailed(state)
	return text, diagnosticInfoMessages(infos), err
}

func transformAndRenderDetailed(state *transformer.State) (text string, diags []DiagnosticInfo, err error) {
	text, _, diags, err = transformAndRenderSourceMapDetailed(state, nil)
	return text, diags, err
}

// InternalCompilerError is the recovered form of a transformer panic: the
// ported upstream asserts panic on internal invariant violations, and a user's
// source must surface as an error rather than crash the process. It carries
// the offending file, the panic value, and the stack captured at recover time
// so a consumer can attribute the failure to a panic site without matching on
// the message text.
//
// Error() is byte-identical to the untyped fmt.Errorf it replaced, so nothing
// that renders compile errors moves.
type InternalCompilerError struct {
	// FileName is the source file being transformed, empty when the panic
	// happened outside a file's transform.
	FileName string
	// Value is the value passed to panic().
	Value any
	// Stack is debug.Stack() captured inside the recovering deferred function.
	Stack []byte
}

func (e *InternalCompilerError) Error() string {
	return fmt.Sprintf("internal compiler error: %v", e.Value)
}

// newInternalCompilerError captures the current goroutine's stack. Call it
// only from inside a recover()ing deferred function.
func newInternalCompilerError(fileName string, value any) *InternalCompilerError {
	return &InternalCompilerError{FileName: fileName, Value: value, Stack: debug.Stack()}
}

func transformAndRenderSourceMapDetailed(state *transformer.State, sourceFile *ast.SourceFile) (text, sourceMap string, diags []DiagnosticInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			text = ""
			sourceMap = ""
			diags = nil
			err = newInternalCompilerError(stateFileName(state), r)
		}
	}()

	luauAST := transformer.TransformSourceFile(state)

	hasErrors := state.Diags.HasErrors()
	for _, d := range state.Diags.Flush() {
		diags = append(diags, infoFromNodeDiag(d))
	}
	if hasErrors {
		// Upstream bails before rendering when the transformer reported
		// errors (compileFiles.ts:176-178).
		return "", "", diags, nil
	}

	text = render.RenderAST(luauAST)
	if sourceFile != nil {
		sourceMap = state.RenderSourceMap(luauAST, sourceFile)
	}
	return text, sourceMap, diags, nil
}

// preEmitDiagnostics ports the per-file half of ts.getPreEmitDiagnostics
// (TypeScript program.ts), which rbxtsc runs for every compiled file
// (compileFiles.ts:156). Upstream concatenates config-parse, options,
// syntactic, global, and semantic diagnostics, then sorts and dedupes;
// rbxtsc fails the file when any are present. Config-parse failures already
// aborted in newProjectProgram and options diagnostics are program-level
// (GetProgramDiagnostics, checked once by each caller), so this collects the
// rest: syntactic first (upstream order), then semantic, then the checker's
// global diagnostics — tsgo accumulates globals only as checkers run, so they
// are queried after the semantic pass, exactly as tsgo's own CLI does
// (compiler.GetDiagnosticsOfAnyProgram re-queries globals after checking).
// Upstream sorts the combined list anyway, and any non-empty result aborts
// the compile, so the global/semantic order swap is unobservable.
func preEmitDiagnostics(ctx context.Context, program *compiler.Program, sourceFile *ast.SourceFile) []*ast.Diagnostic {
	tsDiags := preEmitProjectFileDiagnostics(ctx, program, sourceFile)
	tsDiags = append(tsDiags, program.GetGlobalDiagnostics(ctx)...)
	return tsDiags
}

func preEmitProjectFileDiagnostics(ctx context.Context, program *compiler.Program, sourceFile *ast.SourceFile) []*ast.Diagnostic {
	tsDiags := program.GetSyntacticDiagnostics(ctx, sourceFile)
	tsDiags = append(tsDiags, program.GetSemanticDiagnostics(ctx, sourceFile)...)
	return tsDiags
}

// stateFileName names the file a transform State is working on, tolerating a
// nil State/SourceFile so the recover path can never panic itself.
func stateFileName(state *transformer.State) string {
	if state == nil || state.SourceFile == nil {
		return ""
	}
	return state.SourceFile.FileName()
}

func diagnosticStrings(diags []*ast.Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.String()
	}
	return out
}

func diagnosticInfoMessages(diags []DiagnosticInfo) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Message
	}
	return out
}

func stringDiagnostics(diags []string) []DiagnosticInfo {
	out := make([]DiagnosticInfo, len(diags))
	for i, msg := range diags {
		out[i] = DiagnosticInfo{Message: msg}
	}
	return out
}

func tsDiagnosticInfos(diags []*ast.Diagnostic, traces diagnosticTraces) []DiagnosticInfo {
	out := make([]DiagnosticInfo, len(diags))
	for i, d := range diags {
		out[i] = infoFromTSDiag(d, traces)
	}
	return out
}

// infoFromNodeDiag builds a located DiagnosticInfo from a transformer
// diagnostic. Code/Message/Warning are copied verbatim (byte-identical to the
// previous behaviour); location is resolved from the node when present.
func infoFromNodeDiag(d transformer.Diagnostic) DiagnosticInfo {
	info := DiagnosticInfo{Code: d.Code, Message: d.Message, Warning: d.Warning}
	if d.Node != nil {
		if sf := ast.GetSourceFileOfNode(d.Node); sf != nil {
			start := scanner.GetTokenPosOfNode(d.Node, sf, false)
			info.FileName = sf.FileName()
			info.Offset = start
			info.Line, info.Col = lineColIn(sf.Text(), start)
			if end := d.Node.End(); end > start {
				info.Len = end - start
			}
		}
	}
	return info
}

// TypeScriptDiagnosticCode renders a TypeScript diagnostic number the way every
// consumer of DiagnosticInfo.Code sees it. `sloptor check` builds its --json
// entries straight from ast.Diagnostic and never goes through DiagnosticInfo,
// so it needs this too — and a second `"TS" + itoa` there is a second place for
// the format to drift.
func TypeScriptDiagnosticCode(code int32) string {
	return "TS" + strconv.FormatInt(int64(code), 10)
}

func infoFromTSDiag(d *ast.Diagnostic, traces diagnosticTraces) DiagnosticInfo {
	info := DiagnosticInfo{Code: TypeScriptDiagnosticCode(d.Code()), Message: d.String()}
	if f := d.File(); f != nil {
		info.FileName = f.FileName()
		info.Offset = d.Pos()
		info.Len = d.Len()
		info.Line, info.Col = lineColIn(f.Text(), d.Pos())
		info = traces.remap(info, f.Text())
	}
	return info
}

// lineColIn resolves a byte offset into a 1-based line and column against the
// source text the compile actually used — which is the only text the offset is
// an index into.
//
// It does NOT always agree with the CLI's disk-reading lineColOf (build.go),
// and where they differ this one is right. lineColOf re-reads raw disk bytes,
// while a source file's Text() has had its BOM stripped and any UTF-16 content
// decoded to UTF-8. A UTF-8 BOM shifts every column on the first line by three;
// a UTF-16LE file makes the disk reader's answer meaningless. Under
// ProjectOptions.Overlays the disk bytes are not the compiled text at all.
// lineColOf's divergence is a pre-existing bug left alone here: moving it would
// move `rotor build --json` output.
func lineColIn(source string, offset int) (int, int) {
	if offset < 0 || offset > len(source) {
		return 0, 0
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if source[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func commentDirectiveDiagnostics(sourceFile *ast.SourceFile) []string {
	count := len(sourceFile.CommentDirectives)
	if ast.GetPragmaFromSourceFile(sourceFile, "ts-nocheck") != nil {
		count++
	}
	if count == 0 {
		return nil
	}
	msg := transformer.DiagNoCommentDirectives(sourceFile.AsNode()).Message
	diags := make([]string, count)
	for i := range diags {
		diags[i] = msg
	}
	return diags
}
