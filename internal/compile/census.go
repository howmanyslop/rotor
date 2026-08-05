package compile

import (
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
}

// censusCollector accumulates a census while compileProjectSourceFiles runs.
// ProjectOptions carries it out of band, the way rojoCache and
// pendingSolutionPersists are carried.
type censusCollector struct {
	files       []FileDiagnostics
	diagnostics []DiagnosticInfo
}

func (c *censusCollector) addProjectDiagnostics(diags []DiagnosticInfo) {
	c.diagnostics = append(c.diagnostics, diags...)
}

// record classifies one file from its precheck and transform results.
func (c *censusCollector) record(sourceFile *ast.SourceFile, precheck precheckedProjectSourceFile, result compiledProjectSourceFile, transformed bool) {
	entry := FileDiagnostics{
		FileName:    sourceFile.FileName(),
		Outcome:     FileOutcomeOK,
		Transformed: transformed,
	}

	if len(precheck.tsDiags) > 0 {
		entry.Outcome = FileOutcomeTypeError
		entry.Diagnostics = append(entry.Diagnostics, tsDiagnosticInfos(precheck.tsDiags)...)
	}
	if len(precheck.commentDiags) > 0 {
		entry.Outcome = FileOutcomeTransformerDiagnostic
		entry.Diagnostics = append(entry.Diagnostics, stringDiagnostics(precheck.commentDiags)...)
	}
	if len(result.diags) > 0 {
		entry.Outcome = FileOutcomeTransformerDiagnostic
		entry.Diagnostics = append(entry.Diagnostics, result.diags...)
	}
	switch {
	case precheck.panicErr != nil:
		entry.Outcome = FileOutcomeInternalCompilerError
		entry.InternalError = precheck.panicErr
	case result.err != nil:
		if ice, ok := result.err.(*InternalCompilerError); ok {
			entry.Outcome = FileOutcomeInternalCompilerError
			entry.InternalError = ice
			break
		}
		// Anything else the transform stage failed with (a macro registration
		// failure, for one) is rotor rejecting the file, same class as a
		// transformer diagnostic.
		if entry.Outcome != FileOutcomeTransformerDiagnostic {
			entry.Outcome = FileOutcomeTransformerDiagnostic
		}
		if len(result.diags) == 0 {
			entry.Diagnostics = append(entry.Diagnostics, DiagnosticInfo{Message: result.err.Error(), FileName: entry.FileName})
		}
	}

	c.files = append(c.files, entry)
}

// CompileProjectDiagnostics compiles every file of the project rooted at
// projectDir and reports what happened to each one, instead of stopping at the
// first failure. It routes through the write-free compileProjectProgram, so it
// produces no outDir, no include folder, and no rotor.d.ts — nothing is
// written to disk.
//
// The returned *ProjectDiagnostics is never nil. A non-nil error means the
// project could not be set up far enough to census it (an unreadable tsconfig,
// a missing Rojo project); the diagnostics collected before that point are
// still in the result.
func CompileProjectDiagnostics(projectDir string, opts ProjectOptions) (*ProjectDiagnostics, error) {
	collector := &censusCollector{}
	opts.Census = true
	opts.census = collector
	// Never emit the include folder: the census must not touch disk.
	opts.EmitIncludeFiles = false

	census := &ProjectDiagnostics{ProjectDir: projectDir, ConfigPath: opts.TsConfigPath}

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
