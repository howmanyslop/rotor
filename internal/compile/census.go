package compile

import (
	"errors"
	"path/filepath"

	"rotor/tsgo/ast"
)

// FileOutcome classifies how one source file fared in a census compile.
//
// The stock pipeline is four sequential gates: program-option diagnostics, the
// per-file precheck, the global checker diagnostics, and the transform drain.
// Each returns at the first failure, and the precheck gate returns before the
// transform work group is queued at all — so one type error anywhere hides
// every transformer diagnostic in the project. Census mode turns those gates
// into classifications: every file is run, and every file gets an outcome.
type FileOutcome string

const (
	// FileOutcomeOK means the file transformed with no diagnostics of any
	// kind. It does NOT mean the emitted Luau is correct: the transformer uses
	// types for truthiness, coercion and loop lowering, so output for a
	// type-broken file can be silently wrong.
	FileOutcomeOK FileOutcome = "ok"
	// FileOutcomeTypeError means TypeScript rejected the file. In census mode
	// the file is transformed anyway, so Transformed may still be true.
	FileOutcomeTypeError FileOutcome = "typeError"
	// FileOutcomeTransformerDiagnostic means rotor's transformer reported a
	// diagnostic (or the file uses comment directives, which rotor refuses the
	// same way).
	FileOutcomeTransformerDiagnostic FileOutcome = "transformerDiagnostic"
	// FileOutcomeInternalCompilerError means a ported upstream assert fired.
	// InternalError carries the panic value and stack.
	FileOutcomeInternalCompilerError FileOutcome = "internalCompilerError"
)

// FileDiagnostics is one source file's census entry.
type FileDiagnostics struct {
	// FileName is the file's absolute path, slash-separated (as the program
	// holds it).
	FileName string
	// Outcome is the file's classification. When a file fails several ways at
	// once the most severe wins: internalCompilerError, then
	// transformerDiagnostic, then typeError, then ok.
	Outcome FileOutcome
	// Transformed reports whether the file reached and completed the transform
	// stage. A census that silently shrinks is the failure mode this exists to
	// make visible.
	Transformed bool
	// Diagnostics holds every diagnostic attributed to this file. TypeScript
	// diagnostics carry a "TS####" Code; transformer diagnostics carry the
	// upstream factory name (for example "noAny").
	Diagnostics []DiagnosticInfo
	// InternalError is set only for FileOutcomeInternalCompilerError.
	InternalError *InternalCompilerError
}

// ProjectDiagnostics is the census for one project.
//
// Solution builds, project references and per-project attribution are
// deliberately out of scope here; a solution-level entry point returning a
// slice of these adds them without reshaping this type.
type ProjectDiagnostics struct {
	// ProjectDir is the directory the project was compiled from.
	ProjectDir string
	// ConfigPath is the tsconfig the project was compiled with.
	ConfigPath string
	// Files is one entry per compiled source file, in program order.
	Files []FileDiagnostics
	// Diagnostics holds what could not be attributed to a single file:
	// program-option diagnostics, global checker diagnostics, and project
	// setup failures.
	Diagnostics []DiagnosticInfo
	// Transformed counts the entries of Files whose Transformed is true.
	Transformed int
	// OverlayMatches counts the ProjectOptions.Overlays keys that named a file
	// the program actually holds. A key that matches nothing fails the run, so
	// a consumer can assert this equals the number of overlays it sent and know
	// its edit was really compiled.
	OverlayMatches int
}

// censusCollector accumulates a census while compileProjectSourceFiles runs.
// ProjectOptions carries it out of band, the way rojoCache and
// pendingSolutionPersists are carried.
type censusCollector struct {
	files          []FileDiagnostics
	diagnostics    []DiagnosticInfo
	overlayMatches int
	// traces is the pass's transformer traces, set once compileProjectSourceFiles
	// knows them; record() needs them to put a file's positions back into the
	// coordinates of the file on disk.
	traces diagnosticTraces
}

func (c *censusCollector) addProjectDiagnostics(diags []DiagnosticInfo) {
	c.diagnostics = append(c.diagnostics, diags...)
}

// record classifies one file from its precheck and transform results.
func (c *censusCollector) record(sourceFile *ast.SourceFile, precheck precheckedProjectSourceFile, result compiledProjectSourceFile) {
	entry := FileDiagnostics{
		FileName:    sourceFile.FileName(),
		Outcome:     FileOutcomeOK,
		Transformed: result.transformed,
	}

	if len(precheck.tsDiags) > 0 {
		entry.Outcome = FileOutcomeTypeError
		entry.Diagnostics = append(entry.Diagnostics, tsDiagnosticInfos(precheck.tsDiags, c.traces)...)
	}
	if len(precheck.commentDiags) > 0 {
		entry.Outcome = FileOutcomeTransformerDiagnostic
		entry.Diagnostics = append(entry.Diagnostics,
			c.traces.remapAll(commentDirectiveInfos(sourceFile, precheck.commentDiags), sourceFile.Text())...)
	}
	if len(result.diags) > 0 {
		entry.Outcome = FileOutcomeTransformerDiagnostic
		entry.Diagnostics = append(entry.Diagnostics, result.diags...)
	}
	if result.err != nil {
		var ice *InternalCompilerError
		if errors.As(result.err, &ice) {
			entry.Outcome = FileOutcomeInternalCompilerError
			entry.InternalError = ice
		} else {
			// Anything else the transform stage failed with (a macro
			// registration failure, for one) is rotor rejecting the file, same
			// class as a transformer diagnostic.
			entry.Outcome = FileOutcomeTransformerDiagnostic
			if len(result.diags) == 0 {
				entry.Diagnostics = append(entry.Diagnostics, DiagnosticInfo{Message: result.err.Error(), FileName: entry.FileName})
			}
		}
	}

	c.files = append(c.files, entry)
}

// absSlashPath normalizes a path the way the program holds file names, leaving
// an empty path empty.
func absSlashPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(abs)
}

// commentDirectiveInfos re-attaches the file and position to the comment
// directive messages the precheck produced as bare strings. The stock path
// renders these as plain strings and has nowhere to put a location, but a
// census entry already knows its file — serializing it as `"file": ""` at
// line 0 would be throwing away what the caller needs. The messages are
// unchanged. The ts-nocheck pragma has no directive range, so the message
// commentDirectiveDiagnostics appends for it carries the file only.
func commentDirectiveInfos(sourceFile *ast.SourceFile, messages []string) []DiagnosticInfo {
	fileName := sourceFile.FileName()
	infos := make([]DiagnosticInfo, 0, len(messages))
	for i, message := range messages {
		info := DiagnosticInfo{Message: message, FileName: fileName}
		if i < len(sourceFile.CommentDirectives) {
			loc := sourceFile.CommentDirectives[i].Loc
			info.Offset, info.Len = loc.Pos(), loc.Len()
			info.Line, info.Col = lineColIn(sourceFile.Text(), info.Offset)
		}
		infos = append(infos, info)
	}
	return infos
}

// CompileProjectDiagnostics compiles every file of the project rooted at
// projectDir and reports what happened to each one, instead of stopping at the
// first failure. It routes through the write-free compileProjectProgram, so it
// produces no outDir, no include folder, and no rotor.d.ts — nothing is
// written to disk.
//
// The returned *ProjectDiagnostics is never nil. A non-nil error means the
// project could not be set up far enough to census it (an unreadable tsconfig,
// a missing Rojo project, an overlay that matched nothing); the diagnostics
// collected before that point are still in the result.
func CompileProjectDiagnostics(projectDir string, opts ProjectOptions) (*ProjectDiagnostics, error) {
	collector := &censusCollector{}
	opts.census = collector

	// Both are overwritten below with what the program resolved. They are
	// seeded in the same absolute, slash-separated form here so that a project
	// which fails before it has a program still attributes its diagnostics the
	// way every other project in a solution census does.
	census := &ProjectDiagnostics{
		ProjectDir: absSlashPath(projectDir),
		ConfigPath: absSlashPath(opts.TsConfigPath),
	}

	dir, program, diags, err := newProjectProgramWithOptions(projectDir, opts.TsConfigPath, opts)
	if err != nil {
		census.Diagnostics = append(census.Diagnostics, stringDiagnostics(diags)...)
		return census, err
	}
	census.ProjectDir = dir
	if configFilePath := program.Options().ConfigFilePath; configFilePath != "" {
		census.ConfigPath = filepath.ToSlash(configFilePath)
	}

	_, infos, err := compileProjectProgram(dir, program, opts)

	census.Files = collector.files
	census.OverlayMatches = collector.overlayMatches
	census.Diagnostics = append(census.Diagnostics, collector.diagnostics...)
	for _, file := range census.Files {
		if file.Transformed {
			census.Transformed++
		}
	}
	if err != nil {
		// Setup failures (project context, program preparation) never reach
		// the per-file stage, so their diagnostics only exist here.
		if len(collector.files) == 0 {
			census.Diagnostics = append(census.Diagnostics, infos...)
		}
		return census, err
	}
	return census, nil
}
