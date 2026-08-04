package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rotor/internal/compile"
	"rotor/tsgo/ast"
	"rotor/tsgo/bundled"
	"rotor/tsgo/compiler"
	"rotor/tsgo/diagnostics"
	"rotor/tsgo/diagnosticwriter"
	"rotor/tsgo/scanner"
	"rotor/tsgo/tsoptions"
	"rotor/tsgo/tspath"
	"rotor/tsgo/vfs/cachedvfs"
	"rotor/tsgo/vfs/osvfs"
)

type checkArgs struct {
	project  string
	watch    bool
	jsonOut  bool
	checkers *int
	help     bool
}

func parseCheckArgs(args []string) (*checkArgs, error) {
	res := &checkArgs{project: "."}
	positional := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-w", "--watch":
			res.watch = true
			continue
		case "--json":
			res.jsonOut = true
			continue
		case "-h", "--help":
			res.help = true
			return res, nil
		}

		if !strings.HasPrefix(a, "-") {
			if positional {
				return nil, fmt.Errorf("unexpected extra argument %q", a)
			}
			res.project = a
			positional = true
			continue
		}

		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value, name = name[eq+1:], name[:eq]
			hasValue = true
		}
		if name == "checkers" {
			if !hasValue && i+1 < len(args) && isNumericFlagValue(args[i+1]) {
				i++
				value = args[i]
			}
			n, err := parsePositiveIntFlag(name, value)
			if err != nil {
				return nil, err
			}
			res.checkers = n
			continue
		}
		return nil, fmt.Errorf("unknown flag %q", a)
	}
	return res, nil
}

func cmdCheck(args []string) int {
	parsed, err := parseCheckArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor check: %v\n\n", err)
		usage(os.Stderr)
		return 1 // usage errors exit 1 (rbxtsc parity; see main.go)
	}
	if parsed.help {
		usage(os.Stdout)
		return 0
	}

	dir, err := filepath.Abs(parsed.project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor check: cannot resolve path %q: %v\n", parsed.project, err)
		return 1
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "sloptor check: %s is not a directory\n", dir)
		return 1
	}
	if _, statErr := os.Stat(filepath.Join(dir, "tsconfig.json")); statErr != nil {
		fmt.Fprintf(os.Stderr, "sloptor check: no tsconfig.json found in %s\n", dir)
		return 1
	}

	// rbxts-style projects resolve all of their types (including the
	// runtime/global definitions) out of node_modules; missing packages
	// produce a wall of confusing diagnostics, so warn up front.
	if _, statErr := os.Stat(filepath.Join(dir, "package.json")); statErr == nil {
		if _, statErr := os.Stat(filepath.Join(dir, "node_modules")); statErr != nil {
			fmt.Fprintf(os.Stderr,
				"sloptor check: warning: %s has a package.json but no node_modules — type packages (e.g. @rbxts/*) cannot be resolved; install dependencies first\n",
				dir)
		}
	}

	// --json: suppress styled chrome and emit exactly one result object on
	// stdout (watch has no terminal "end", so it stays styled).
	if parsed.jsonOut && !parsed.watch {
		return cmdCheckJSON(dir, parsed.checkers)
	}

	newUI(os.Stdout).banner(filepath.Base(dir))

	if parsed.watch {
		return runWatch(dir, os.Stdout, parsed.checkers)
	}
	res := runCheck(dir, os.Stdout, parsed.checkers)
	if res.errorCount > 0 {
		return 1
	}
	return 0
}

// cmdCheckJSON runs a one-shot check and prints a single jsonResult object
// (shared with `rotor build --json`) built from the structured diagnostics.
// Exit code is unchanged: 1 when any error diagnostic is present, else 0.
func cmdCheckJSON(dir string, checkers *int) int {
	res := runCheckCollect(dir, checkers)
	out := jsonResult{
		Version:     version,
		OK:          res.errorCount == 0,
		Files:       res.fileCount,
		DurationMs:  res.elapsed.Milliseconds(),
		Diagnostics: res.jsonDiags,
	}
	writeJSONResult(os.Stdout, out)
	if res.errorCount > 0 {
		return 1
	}
	return 0
}

type checkResult struct {
	fileCount  int
	errorCount int
	elapsed    time.Duration
	watchFiles []string

	// jsonDiags is the structured diagnostics list for `rotor check --json`,
	// populated only by runCheckCollect (nil on the styled path).
	jsonDiags []jsonDiagnostic
}

// checkCore is the shared diagnostics-building result: the sorted AST
// diagnostics plus the formatting options and metadata both the styled and
// JSON renderers need. It builds the program once; callers choose how to emit.
type checkCore struct {
	diags      []*ast.Diagnostic
	formatOpts *diagnosticwriter.FormattingOptions
	fileCount  int
	elapsed    time.Duration
	watchFiles []string
}

func newCheckProgram(dir, configPath string, checkers *int) (*compiler.Program, *tsoptions.ParsedCommandLine, []*ast.Diagnostic) {
	slashDir := filepath.ToSlash(dir)
	fs := cachedvfs.From(compile.SanitizeFS(bundled.WrapFS(osvfs.FS())))
	host := compiler.NewCompilerHost(slashDir, fs, bundled.LibPath(), nil, nil)
	parsed, configDiags := tsoptions.GetParsedCommandLineOfConfigFile(configPath, nil, nil, host, nil)
	if parsed == nil {
		return nil, nil, configDiags
	}
	compile.ApplyCheckerOverride(parsed.CompilerOptions(), checkers)
	return compiler.NewProgram(compiler.ProgramOptions{
		Host:   host,
		Config: parsed,
	}), parsed, configDiags
}

// runCheckCore builds a fresh program for the project in dir and returns its
// (sorted, deduplicated) diagnostics without rendering anything — the common
// core of the styled runCheck and the JSON runCheckCollect, so both observe
// identical diagnostics. The rotor-env.d.ts refresh still happens here (silent).
func runCheckCore(dir string, checkers *int) checkCore {
	start := time.Now()
	slashDir := filepath.ToSlash(dir)

	// SanitizeFS rewrites rbxts-required tsconfig options that tsgo (TS7)
	// rejects (downlevelIteration, baseUrl, moduleResolution=node10) so
	// standard roblox-ts projects check cleanly — same wrapping as
	// compile.CompileFile.
	// Cache filesystem metadata (Stat/FileExists/Realpath) for the lifetime of
	// this check: module resolution re-stats overlapping node_modules paths
	// once per file, and a check never mutates its source tree. Same wrapper
	// tsgo's project host uses; see compile.newProjectProgram.
	formatOpts := &diagnosticwriter.FormattingOptions{
		NewLine: "\n",
		ComparePathsOptions: tspath.ComparePathsOptions{
			CurrentDirectory:          slashDir,
			UseCaseSensitiveFileNames: osvfs.FS().UseCaseSensitiveFileNames(),
		},
	}

	configPath := slashDir + "/tsconfig.json"
	program, parsed, configDiags := newCheckProgram(dir, configPath, checkers)
	if parsed == nil {
		// Unreadable/unparsable config: report what we have and stop.
		return checkCore{
			diags:      configDiags,
			formatOpts: formatOpts,
			elapsed:    time.Since(start),
			watchFiles: []string{configPath},
		}
	}
	formatOpts.Locale = parsed.Locale()

	// Same collection order as tsgo's own CLI: config parse, syntactic,
	// program (options), global, then semantic diagnostics. Semantic checking
	// is scoped to the project's own files, mirroring rbxtsc — upstream calls
	// getPreEmitDiagnostics(program, sourceFile) per compiled file
	// (compileFiles.ts:156), so node_modules type packages are resolved
	// against, never themselves checked. (The fixture @rbxts/types, for one,
	// has internal errors that tsc only avoids via skipLibCheck.)
	ctx := context.Background()
	semanticProjectFilesOnly := func(ctx context.Context, _ *ast.SourceFile) []*ast.Diagnostic {
		var out []*ast.Diagnostic
		for _, name := range parsed.FileNames() {
			if sf := program.GetSourceFile(name); sf != nil {
				out = append(out, program.GetSemanticDiagnostics(ctx, sf)...)
			}
		}
		return out
	}
	diags := compiler.GetDiagnosticsOfAnyProgram(ctx, program, nil, false,
		program.GetBindDiagnostics, semanticProjectFilesOnly)
	diags = compiler.SortAndDeduplicateDiagnostics(diags)

	// rotor extension: refresh the consolidated rotor.d.ts editor companion when
	// the project references any macro (silent — check's stdout is byte-stable).
	refreshRotorTypesForCheck(dir, parsed.FileNames(), program)

	return checkCore{
		diags:      diags,
		formatOpts: formatOpts,
		fileCount:  len(program.GetSourceFiles()),
		elapsed:    time.Since(start),
		watchFiles: append([]string{configPath}, parsed.FileNames()...),
	}
}

// runCheck builds a fresh program for the project in dir, prints all
// diagnostics plus a summary line, and reports the file list to watch.
func runCheck(dir string, out io.Writer, checkers *int) checkResult {
	core := runCheckCore(dir, checkers)
	writeDiagnostics(out, core.diags, core.formatOpts)
	res := checkResult{
		fileCount:  core.fileCount,
		errorCount: countErrors(core.diags),
		elapsed:    core.elapsed,
		watchFiles: core.watchFiles,
	}
	printSummary(out, res)
	return res
}

// runCheckCollect is runCheck's JSON sibling: it builds the same program and
// diagnostics but renders nothing, returning a checkResult whose jsonDiags
// carries the structured (file, line, col, severity, message) entries.
func runCheckCollect(dir string, checkers *int) checkResult {
	core := runCheckCore(dir, checkers)
	return checkResult{
		fileCount:  core.fileCount,
		errorCount: countErrors(core.diags),
		elapsed:    core.elapsed,
		watchFiles: core.watchFiles,
		jsonDiags:  jsonDiagnostics(core.diags, core.formatOpts),
	}
}

// jsonDiagnostics converts AST diagnostics into the --json wire shape, mirroring
// diagnosticwriter's location math (1-based line/col, project-relative file).
func jsonDiagnostics(diags []*ast.Diagnostic, formatOpts *diagnosticwriter.FormattingOptions) []jsonDiagnostic {
	out := make([]jsonDiagnostic, 0, len(diags))
	for _, d := range diags {
		jd := jsonDiagnostic{Severity: severityName(d), Message: d.String()}
		if file := d.File(); file != nil {
			line, character := scanner.GetECMALineAndUTF16CharacterOfPosition(file, d.Pos())
			jd.Line = line + 1
			jd.Col = int(character) + 1
			jd.File = tspath.ConvertToRelativePath(file.FileName(), formatOpts.ComparePathsOptions)
		}
		out = append(out, jd)
	}
	return out
}

// severityName maps a diagnostic category to the --json severity string
// ("error" | "warning"; suggestions/messages collapse to "warning").
func severityName(d *ast.Diagnostic) string {
	if d.Category() == diagnostics.CategoryError {
		return "error"
	}
	return "warning"
}

// refreshRotorTypesForCheck (re)writes the consolidated rotor.d.ts editor
// companion when any non-declaration project file references one of rotor's
// macros ($env / $asset / $nameof / $keys / $file / $git / $buildTime). The
// on-disk file is written only when missing or stale. Failures only warn on
// stderr — they never affect the check result or its stdout shape.
func refreshRotorTypesForCheck(dir string, fileNames []string, program *compiler.Program) {
	for _, name := range fileNames {
		if strings.HasSuffix(name, ".d.ts") {
			continue
		}
		sf := program.GetSourceFile(name)
		if sf == nil {
			continue
		}
		text := sf.Text()
		if !strings.Contains(text, "$env") && !strings.Contains(text, "$asset") && !compile.SourceUsesMacros(text) {
			continue
		}
		if _, err := compile.WriteRotorTypes(dir); err != nil {
			fmt.Fprintf(os.Stderr, "sloptor check: warning: cannot write %s: %v\n", compile.RotorTypesFileName, err)
		}
		return
	}
}

func printSummary(out io.Writer, res checkResult) {
	newUI(out).checkSummary(res.fileCount, res.errorCount, res.elapsed)
}

func writeDiagnostics(out io.Writer, diags []*ast.Diagnostic, formatOpts *diagnosticwriter.FormattingOptions) {
	if len(diags) == 0 {
		return
	}
	wrapped := diagnosticwriter.FromASTDiagnostics(diags)
	if useColor(out) {
		diagnosticwriter.FormatDiagnosticsWithColorAndContext(out, wrapped, formatOpts)
		fmt.Fprint(out, formatOpts.NewLine)
	} else {
		diagnosticwriter.WriteFormatDiagnostics(out, wrapped, formatOpts)
	}
	fmt.Fprint(out, formatOpts.NewLine)
}

func countErrors(diags []*ast.Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.Category() == diagnostics.CategoryError {
			n++
		}
	}
	return n
}

func useColor(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
