package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"rotor/internal/assets"
	"rotor/internal/compile"
	"rotor/internal/logservice"
	"rotor/internal/transformer"
	"rotor/tsgo/vfs/osvfs"
)

// buildArgs is the parsed `sloptor build` argv: the project path (the only
// option with a default — "DO NOT PROVIDE DEFAULTS BELOW HERE",
// CLI/commands/build.ts L62) plus a Partial<ProjectOptions> of exactly the
// flags the user passed. It is assembled by newBuildCommand from Cobra's
// parsed flags (only Changed booleans enter the partial), and consumed by the
// runner, profile wiring, and the watch option reload.
type buildArgs struct {
	project             string
	opts                partialProjectOptions
	build               bool
	buildPath           string
	emitDeclarationOnly bool
	builders            *int
	checkers            *int
	jsonOut             bool   // rotor DX extension: emit a machine-readable result object
	cpuprofile          string // rotor DX extension: write a pprof CPU profile here
	traceOut            string
	blockprofile        string
	mutexprofile        string
	heapprofile         string
	timings             string
	maxErrors           int  // rotor DX extension: cap the number of rendered code frames (0 = unlimited; default 50)
	bell                bool // rotor DX extension (watch): ring the bell on a fail<->pass flip
	clearScreen         bool // rotor DX extension (watch): clear the screen before each rebuild (default true)
	minify              bool // rotor DX extension: minify emitted Luau before writing
}

// buildFlags is the parsed flag destination shared by `sloptor build` and
// `sloptor dev`: every registered flag binds here, and collectBuildArgs turns
// the Changed subset into a buildArgs (the yargs Object.assign semantics —
// absent CLI booleans never clobber the tsconfig `rbxts` layer).
type buildFlags struct {
	project      string
	buildPath    string
	includePath  string
	rojo         string
	typeName     string
	cpuprofile   string
	traceOut     string
	blockprofile string
	mutexprofile string
	heapprofile  string
	timings      string
	maxErrors    int

	emitDeclarationOnly    bool
	watch                  bool
	usePolling             bool
	verbose                bool
	noInclude              bool
	logTruthyChanges       bool
	writeOnlyChanged       bool
	writeTransformedFiles  bool
	optimizedLoops         bool
	allowCommentDirectives bool
	luau                   bool
	minify                 bool
	jsonOut                bool
	bell                   bool
	clear                  bool
	version                bool
	builders               *int
	checkers               *int
}

// registerBuildFlags registers the full rbxtsc-compatible build surface in
// documented order. `dev` registers the same surface (it forwards the build
// options it accepts) plus its own --serve flag.
func registerBuildFlags(cmd *cobra.Command, flags *buildFlags) {
	f := cmd.Flags()
	f.SortFlags = false
	addStringFlag(cmd, &flags.project, "project", "p", ".", "<path>",
		"project path (default \".\"): a tsconfig file, a directory containing one, or any path to search upward from")
	addStringFlag(cmd, &flags.buildPath, "build", "b", "", "[path]",
		"build project references (optionally select a tsconfig path)")
	f.VarP(newPositiveIntValue(&flags.builders), "builders", "",
		"number of projects to build concurrently (default 4; only with --build)")
	setFlagPlaceholder(cmd, "builders", "<n>")
	f.VarP(newPositiveIntValue(&flags.checkers), "checkers", "",
		"number of checkers per project (default 4; build and check)")
	setFlagPlaceholder(cmd, "checkers", "<n>")
	addBoolFlag(cmd, &flags.emitDeclarationOnly, "emitDeclarationOnly", "", false,
		"only emit declaration files for a solution build (requires --build)")
	addBoolFlag(cmd, &flags.watch, "watch", "w", false, "enable watch mode")
	addBoolFlag(cmd, &flags.usePolling, "usePolling", "", false,
		"use polling for watch mode (requires --watch)")
	addBoolFlag(cmd, &flags.verbose, "verbose", "", false, "enable verbose logs")
	addBoolFlag(cmd, &flags.noInclude, "noInclude", "", false, "do not copy include files")
	addBoolFlag(cmd, &flags.logTruthyChanges, "logTruthyChanges", "", false,
		"logs changes to truthiness evaluation from Lua truthiness rules")
	addBoolFlag(cmd, &flags.writeOnlyChanged, "writeOnlyChanged", "", false,
		"skip rewriting output files whose contents are unchanged")
	addBoolFlag(cmd, &flags.writeTransformedFiles, "writeTransformedFiles", "", false,
		"not supported by sloptor (parsed and ignored)")
	addBoolFlag(cmd, &flags.optimizedLoops, "optimizedLoops", "", true,
		"numeric-for loop optimization (default true)")
	f.VarP(newEnumValue(&flags.typeName, "game", "model", "package"), "type", "",
		"override project type (choices: game, model, package)")
	setFlagPlaceholder(cmd, "type", "<kind>")
	addStringFlag(cmd, &flags.includePath, "includePath", "i", "", "<dir>",
		"folder to copy runtime files to (default <project>/include)")
	addStringFlag(cmd, &flags.rojo, "rojo", "", "", "<path>",
		"manually select Rojo project file")
	addBoolFlag(cmd, &flags.allowCommentDirectives, "allowCommentDirectives", "", false,
		"allow @ts-ignore et al.")
	addBoolFlag(cmd, &flags.luau, "luau", "", true,
		"emit files with .luau extension (default true; --luau=false emits .lua)")
	addStringFlag(cmd, &flags.cpuprofile, "cpuprofile", "", "", "<path>",
		"write a pprof CPU profile of the build (diagnostics)")
	addStringFlag(cmd, &flags.traceOut, "trace-out", "", "", "<path>",
		"write a Go execution trace of the build (diagnostics)")
	addStringFlag(cmd, &flags.blockprofile, "blockprofile", "", "", "<path>",
		"write a blocking profile sampled at 1 ms (diagnostics)")
	addStringFlag(cmd, &flags.mutexprofile, "mutexprofile", "", "", "<path>",
		"write a mutex contention profile at fraction 5 (diagnostics)")
	addStringFlag(cmd, &flags.heapprofile, "heapprofile", "", "", "<path>",
		"write a heap profile after the build (diagnostics)")
	addStringFlag(cmd, &flags.timings, "timings", "", "", "<path>",
		"write aggregate one-shot build timings as JSON (not with --watch)")
	addBoolFlag(cmd, &flags.minify, "minify", "", false,
		"minify emitted Luau (strip comments/whitespace, t[\"x\"] -> t.x)")
	f.VarP(newNonNegativeIntValue(&flags.maxErrors), "max-errors", "",
		"cap the rendered code frames on failure (default 50; 0 = all)")
	setFlagPlaceholder(cmd, "max-errors", "<n>")
	addBoolFlag(cmd, &flags.jsonOut, "json", "", false,
		"emit one machine-readable result object instead of styled output")
	addBoolFlag(cmd, &flags.bell, "bell", "", false,
		"ring the terminal bell on a watch fail<->pass transition")
	addBoolFlag(cmd, &flags.clear, "clear", "", true,
		"clear the screen before each rebuild (--no-clear keeps scroll history)")
	addBoolFlag(cmd, &flags.version, "version", "v", false, "print sloptor's version")
}

// collectBuildArgs assembles a buildArgs from the parsed flag set: the
// positional-or---project path, the Changed-only partial options, and the
// rotor DX fields. It never inspects argv values beyond the single positional
// (Cobra owns parsing); absent flags stay nil so the tsconfig layer wins.
func collectBuildArgs(f *pflag.FlagSet, argv []string, flags *buildFlags, ba *buildArgs) error {
	ba.project = flags.project
	if len(argv) > 0 {
		if f.Changed("project") {
			return usageFailure("unexpected extra argument %q (project already set via --project)", argv[0])
		}
		ba.project = argv[0]
	}
	if f.Changed("build") {
		ba.build = true
		ba.buildPath, _ = f.GetString("build")
	}
	if f.Changed("emitDeclarationOnly") {
		ba.emitDeclarationOnly, _ = f.GetBool("emitDeclarationOnly")
	}
	for name, dst := range map[string]**bool{
		"watch": &ba.opts.watch, "usePolling": &ba.opts.usePolling,
		"verbose": &ba.opts.verbose, "noInclude": &ba.opts.noInclude,
		"logTruthyChanges": &ba.opts.logTruthyChanges, "writeOnlyChanged": &ba.opts.writeOnlyChanged,
		"writeTransformedFiles": &ba.opts.writeTransformedFiles, "optimizedLoops": &ba.opts.optimizedLoops,
		"allowCommentDirectives": &ba.opts.allowCommentDirectives, "luau": &ba.opts.luau,
	} {
		if f.Changed(name) {
			v, _ := f.GetBool(name)
			*dst = &v
		}
	}
	for name, dst := range map[string]**string{
		"includePath": &ba.opts.includePath,
		"rojo":        &ba.opts.rojo,
		"type":        &ba.opts.typeName,
	} {
		if f.Changed(name) {
			v, _ := f.GetString(name)
			*dst = &v
		}
	}
	ba.builders = flags.builders
	ba.checkers = flags.checkers
	if f.Changed("clear") {
		ba.clearScreen, _ = f.GetBool("clear")
	}
	ba.bell, _ = f.GetBool("bell")
	ba.jsonOut, _ = f.GetBool("json")
	ba.minify, _ = f.GetBool("minify")
	ba.maxErrors = flags.maxErrors
	ba.cpuprofile, _ = f.GetString("cpuprofile")
	ba.traceOut, _ = f.GetString("trace-out")
	ba.blockprofile, _ = f.GetString("blockprofile")
	ba.mutexprofile, _ = f.GetString("mutexprofile")
	ba.heapprofile, _ = f.GetString("heapprofile")
	ba.timings, _ = f.GetString("timings")
	return nil
}

// newBuildCommand is the compile-to-disk command, porting the rbxtsc build
// handler (CLI/commands/build.ts L120-167) onto the Cobra flag surface: find
// the tsconfig (file path or upward search), merge ProjectOptions (defaults <
// tsconfig `rbxts` key < argv), set LogService verbosity, then compile and
// write outputs.
//
// The rbxtsc flag surface is registered exactly as documented: booleans keep
// their yargs forms (`--flag`, `--flag=<bool>`, and `--no-flag`, the last
// normalized by execute before parsing), `--build`/`--rojo` keep their
// present-but-empty fall-through semantics, and the numeric/type validators
// reproduce the yargs errors. Option implications run in runBuildCommand, in
// argv order.
func newBuildCommand(streams cliStreams) *cobra.Command {
	flags := &buildFlags{maxErrors: 50, clear: true}
	cmd := &cobra.Command{
		Use:                   "build [options] [path]",
		Short:                 "compile the project to Luau (tsconfig outDir + include/)",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runBuildCommand(streams, cmd, flags, argv)
		},
	}
	registerBuildFlags(cmd, flags)
	return cmd
}

// runBuildCommand is the post-parse build pipeline. It owns option
// implication checks (usage failures), the profile lifecycle, and the styled
// / JSON output split; failures flow to execute as commandFailures.
func runBuildCommand(streams cliStreams, cmd *cobra.Command, flags *buildFlags, argv []string) error {
	if flags.version {
		fmt.Fprintln(streams.out, version)
		return nil
	}
	f := cmd.Flags()
	ba := buildArgs{project: ".", maxErrors: 50, clearScreen: true}
	if err := collectBuildArgs(f, argv, flags, &ba); err != nil {
		return err
	}

	for name, path := range map[string]string{
		"--cpuprofile": ba.cpuprofile, "--trace-out": ba.traceOut,
		"--blockprofile": ba.blockprofile, "--mutexprofile": ba.mutexprofile,
		"--heapprofile": ba.heapprofile, "--timings": ba.timings,
	} {
		if f.Changed(strings.TrimPrefix(name, "--")) && path == "" {
			return usageFailure("%s requires a path", name)
		}
	}

	// Option implications (build.ts): the same failures yargs raised at parse
	// time, in the same order, with the same text.
	if ba.builders != nil && !ba.build {
		return usageFailure("--builders requires --build")
	}
	if ba.opts.usePolling != nil && ba.opts.watch == nil {
		return usageFailure("Implications failed:\n usePolling -> watch")
	}
	if ba.emitDeclarationOnly && !ba.build {
		return usageFailure("Implications failed:\n emitDeclarationOnly -> build")
	}
	if ba.build && ba.emitDeclarationOnly && ba.opts.watch != nil && *ba.opts.watch {
		return usageFailure("--build --watch is incompatible with --emitDeclarationOnly (no Luau emit to incrementally watch)")
	}
	if ba.opts.watch != nil && *ba.opts.watch {
		for flag, path := range map[string]string{
			"--cpuprofile": ba.cpuprofile, "--trace-out": ba.traceOut,
			"--blockprofile": ba.blockprofile, "--mutexprofile": ba.mutexprofile,
			"--heapprofile": ba.heapprofile, "--timings": ba.timings,
		} {
			if path != "" {
				return usageFailure("%s cannot be used with --watch", flag)
			}
		}
	}

	if err := validateBuildDiagnosticPaths(&ba); err != nil {
		return runtimeFailure(err)
	}
	profiles, err := startBuildProfiles(&ba)
	if err != nil {
		return runtimeFailure(err)
	}
	buildErr := runBuildBody(streams, &ba)
	if perr := profiles.stop(); perr != nil {
		if buildErr == nil {
			buildErr = runtimeFailure(fmt.Errorf("finalize profiles: %w", perr))
		}
	}
	return buildErr
}

// runBuildBody is the old cmdBuild body: config resolution, merge, and the
// styled or --json output. It runs with profiles already started; the caller
// finalizes them.
func runBuildBody(streams cliStreams, parsed *buildArgs) error {
	projectPath := parsed.project
	if parsed.buildPath != "" {
		projectPath = parsed.buildPath
	}
	tsConfigPath, err := findTsConfigPath(projectPath)
	if err != nil {
		return runtimeFailure(err)
	}

	// Merge order (build.ts L125-130): defaults < tsconfig `rbxts` key <
	// argv. Absent CLI booleans (nil) never clobber `rbxts` values.
	rbxtsOptions, err := readRbxtsOptionsChecked(tsConfigPath)
	if err != nil {
		return runtimeFailure(err)
	}
	opts := mergeProjectOptions(defaultProjectOptions, rbxtsOptions, &parsed.opts)
	opts.minify = parsed.minify // rotor extension: CLI-only, outside the rbxts merge
	opts.emitDeclarationOnly = parsed.emitDeclarationOnly
	opts.builders = parsed.builders
	opts.checkers = parsed.checkers
	if parsed.timings != "" && opts.watch {
		return usageFailure("--timings cannot be used with --watch")
	}
	if parsed.timings != "" {
		if err := prepareBuildTimingsPath(parsed.timings); err != nil {
			return runtimeFailure(fmt.Errorf("cannot prepare timings output: %w", err))
		}
	}

	// LogService.verbose = projectOptions.verbose === true (build.ts L132).
	logservice.Verbose = opts.verbose

	// Upstream projectPath = path.dirname(tsConfigPath)
	// (createProjectData.ts L13).
	dir := filepath.Dir(tsConfigPath)

	// --json: suppress all styled chrome and emit exactly one result object on
	// stdout. Watch mode has no terminal "end", so it is not JSON-encoded; a
	// one-shot build is what CI/editor integrations call with --json.
	if parsed.jsonOut && !opts.watch {
		if code := cmdBuildJSON(streams.out, streams.err, dir, tsConfigPath, opts, parsed.build, parsed.timings); code != 0 {
			return reportedFailure(errors.New("build failed"))
		}
		return nil
	}

	out := newUI(streams.out)
	out.banner(filepath.Base(dir))

	if opts.writeTransformedFiles {
		out.warn("--writeTransformedFiles is not supported by sloptor yet (rbxtsc transformer-plugin debug output; out of v1 scope) — ignoring")
	}
	if opts.watch {
		if parsed.build {
			reload := newBuildOptionsReload(tsConfigPath, parsed)
			runBuildSolutionWatch(tsConfigPath, opts, reload, watchOptions{
				maxErrors: parsed.maxErrors, bell: parsed.bell, clearScreen: parsed.clearScreen,
			})
		} else {
			runBuildWatch(dir, tsConfigPath, opts, watchOptions{
				maxErrors:   parsed.maxErrors,
				bell:        parsed.bell,
				clearScreen: parsed.clearScreen,
			})
		}
		return nil // unreachable in practice: watch loops until Ctrl+C
	}

	if _, statErr := os.Stat(filepath.Join(dir, "package.json")); statErr == nil {
		if _, statErr := os.Stat(filepath.Join(dir, "node_modules")); statErr != nil {
			out.warn(fmt.Sprintf("%s has a package.json but no node_modules — type packages (e.g. @rbxts/*) cannot be resolved; install dependencies first", filepath.Base(dir)))
		}
	}

	var result *compile.BuildResult
	var diags []compile.DiagnosticInfo
	var elapsed time.Duration
	var timings *compile.BuildTimings
	if parsed.timings != "" {
		timings = compile.NewBuildTimings()
	}
	if parsed.build {
		result, diags, elapsed, err = runBuildSolutionOnce(tsConfigPath, opts, timings)
	} else {
		result, diags, elapsed, err = runBuildOnceWithTimings(dir, tsConfigPath, opts, timings)
	}
	if timings != nil {
		timings.SetOK(err == nil)
		if writeErr := writeBuildTimings(parsed.timings, timings); writeErr != nil {
			return runtimeFailure(fmt.Errorf("write timings: %w", writeErr))
		}
	}
	if err != nil {
		newUI(streams.err).buildFailure(err.Error(), diags, parsed.maxErrors)
		return reportedFailure(err)
	}

	if result.WroteRotorTypes {
		out.noteLine(compile.RotorTypesFileName + "  (generated — editor types for sloptor macros)")
	}
	if result.WroteLockfile {
		out.noteLine(assets.LockfileName + "  (updated — uploaded new $asset assets)")
	}
	copiedFiles := len(result.Outputs) - len(result.EmittedFiles)
	if copiedFiles < 0 {
		copiedFiles = 0
	}
	out.buildSuccess(len(result.Outputs), len(result.EmittedFiles), copiedFiles, elapsed)
	return nil
}

func validateBuildDiagnosticPaths(args *buildArgs) error {
	outputs := []struct {
		name string
		path string
	}{
		{"--cpuprofile", args.cpuprofile},
		{"--trace-out", args.traceOut},
		{"--blockprofile", args.blockprofile},
		{"--mutexprofile", args.mutexprofile},
		{"--heapprofile", args.heapprofile},
		{"--timings", args.timings},
	}
	type resolvedOutput struct {
		name string
		path string
		info os.FileInfo
	}
	seen := make([]resolvedOutput, 0, len(outputs))
	for _, output := range outputs {
		if output.path == "" {
			continue
		}
		path, info, err := resolveDiagnosticOutputPath(output.path)
		if err != nil {
			return fmt.Errorf("resolve %s output path: %w", output.name, err)
		}
		if !osvfs.FS().UseCaseSensitiveFileNames() {
			path = strings.ToLower(path)
		}
		for _, previous := range seen {
			if previous.path == path || previous.info != nil && info != nil && os.SameFile(previous.info, info) {
				return fmt.Errorf("%s and %s cannot write to the same path", previous.name, output.name)
			}
		}
		seen = append(seen, resolvedOutput{name: output.name, path: path, info: info})
	}
	return nil
}

func resolveDiagnosticOutputPath(path string) (string, os.FileInfo, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	path = filepath.Clean(path)
	for range 255 {
		info, err := os.Stat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			return resolved, info, err
		}
		if !os.IsNotExist(err) {
			return "", nil, err
		}
		linkInfo, linkErr := os.Lstat(path)
		if linkErr == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", nil, err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			path = filepath.Clean(target)
			continue
		}
		if linkErr != nil && !os.IsNotExist(linkErr) {
			return "", nil, linkErr
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
			path = filepath.Join(parent, filepath.Base(path))
		}
		return path, nil, nil
	}
	return "", nil, errors.New("too many symbolic links in diagnostic output path")
}

func newBuildOptionsReload(tsConfigPath string, parsed *buildArgs) func() (projectOptions, error) {
	return func() (projectOptions, error) {
		declared, err := readRbxtsOptionsChecked(tsConfigPath)
		if err != nil {
			return projectOptions{}, err
		}
		next := mergeProjectOptions(defaultProjectOptions, declared, &parsed.opts)
		next.minify = parsed.minify
		next.emitDeclarationOnly = parsed.emitDeclarationOnly
		next.builders = parsed.builders
		next.checkers = parsed.checkers
		return next, nil
	}
}

func runBuildOnce(dir, tsConfigPath string, opts projectOptions) (*compile.BuildResult, []compile.DiagnosticInfo, time.Duration, error) {
	return runBuildOnceWithTimings(dir, tsConfigPath, opts, nil)
}

func runBuildOnceWithTimings(dir, tsConfigPath string, opts projectOptions, timings *compile.BuildTimings) (*compile.BuildResult, []compile.DiagnosticInfo, time.Duration, error) {
	transformer.HeaderComment = " Compiled with @isentinel/roblox-ts v4.0.11"

	start := time.Now()
	compileOptions := projectCompileOptions(tsConfigPath, opts)
	compileOptions.Timings = timings
	result, msgs, err := compile.BuildProjectWithOptions(dir, compileOptions)
	var diags []compile.DiagnosticInfo
	if result != nil {
		diags = result.Diagnostics
	}
	if len(diags) == 0 && len(msgs) > 0 { // config/validation errors have no source span
		for _, m := range msgs {
			diags = append(diags, compile.DiagnosticInfo{Message: m})
		}
	}
	return result, diags, time.Since(start), err
}

func runBuildSolutionOnce(tsConfigPath string, opts projectOptions, timings *compile.BuildTimings) (*compile.BuildResult, []compile.DiagnosticInfo, time.Duration, error) {
	transformer.HeaderComment = " Compiled with @isentinel/roblox-ts v4.0.11"
	start := time.Now()
	compileOptions := projectCompileOptions(tsConfigPath, opts)
	compileOptions.Timings = timings
	result, msgs, err := compile.BuildSolutionWithOptions(tsConfigPath, compileOptions)
	var diags []compile.DiagnosticInfo
	if result != nil {
		diags = result.Diagnostics
	}
	if len(diags) == 0 && len(msgs) > 0 {
		for _, message := range msgs {
			diags = append(diags, compile.DiagnosticInfo{Message: message})
		}
	}
	return result, diags, time.Since(start), err
}

func projectCompileOptions(tsConfigPath string, opts projectOptions) compile.ProjectOptions {
	return compile.ProjectOptions{
		TsConfigPath:           tsConfigPath,
		IncludePath:            opts.includePath,
		EmitIncludeFiles:       !opts.noInclude,
		Type:                   transformer.ProjectType(opts.typeName),
		RojoConfigPath:         opts.rojo,
		LogTruthyChanges:       opts.logTruthyChanges,
		AllowCommentDirectives: opts.allowCommentDirectives,
		NoOptimizedLoops:       !opts.optimizedLoops,
		LuaExtension:           !opts.luau,
		WriteOnlyChanged:       opts.writeOnlyChanged,
		MinifyOutput:           opts.minify,
		EmitDeclarationOnly:    opts.emitDeclarationOnly,
		Builders:               opts.builders,
		Checkers:               opts.checkers,
	}
}

// jsonDiagnostic is one entry in the --json diagnostics array. file/line/col
// are populated from the structured DiagnosticInfo location when available;
// `sloptor check --json` also fills these from its own structured AST diagnostics.
type jsonDiagnostic struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	// Code is the diagnostic's stable identity: "TS####" for a TypeScript
	// diagnostic, the upstream factory name ("noAny") for a transformer one.
	// Without it the message is the only thing telling the two families apart,
	// and a `sloptor diagnostics` file can carry both at once — so a consumer
	// grouping or routing by class had no key but prose. Omitted when the
	// diagnostic has no code (a bare run failure), so entries that had none
	// keep the shape they had.
	Code     string `json:"code,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// jsonResult is the single object printed by `sloptor build --json` /
// `sloptor check --json`. The shape is stable for CI/editor integration.
type jsonResult struct {
	Version     string           `json:"version"`
	OK          bool             `json:"ok"`
	Files       int              `json:"files"`
	DurationMs  int64            `json:"durationMs"`
	Diagnostics []jsonDiagnostic `json:"diagnostics"`
}

// writeJSONResult prints exactly one jsonResult object (with a trailing
// newline) to w. A nil Diagnostics slice is normalized to [] so consumers
// always see an array.
func writeJSONResult(w io.Writer, res jsonResult) {
	if res.Diagnostics == nil {
		res.Diagnostics = []jsonDiagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(res)
}

// cmdBuildJSON runs a one-shot build and prints a single jsonResult object
// to out instead of the styled UI. Exit code is unchanged from the styled
// path: 1 on any build error, 0 otherwise.
func cmdBuildJSON(out, errOut io.Writer, dir, tsConfigPath string, opts projectOptions, solution bool, timingPath string) int {
	var result *compile.BuildResult
	var diags []compile.DiagnosticInfo
	var elapsed time.Duration
	var err error
	var timings *compile.BuildTimings
	if timingPath != "" {
		timings = compile.NewBuildTimings()
	}
	if solution {
		result, diags, elapsed, err = runBuildSolutionOnce(tsConfigPath, opts, timings)
	} else {
		result, diags, elapsed, err = runBuildOnceWithTimings(dir, tsConfigPath, opts, timings)
	}
	if timings != nil {
		timings.SetOK(err == nil)
		if writeErr := writeBuildTimings(timingPath, timings); writeErr != nil {
			fmt.Fprintf(errOut, "write timings: %v\n", writeErr)
			return 1
		}
	}
	res := jsonResult{
		Version:    version,
		OK:         err == nil,
		DurationMs: elapsed.Milliseconds(),
	}
	if err != nil {
		for _, d := range diags {
			sev := "error"
			if d.Warning {
				sev = "warning"
			}
			jd := jsonDiagnostic{Code: d.Code, Severity: sev, Message: d.Message}
			if d.FileName != "" {
				jd.File = relForDisplay(d.FileName)
				jd.Line, jd.Col = lineColOf(d.FileName, d.Offset)
			}
			res.Diagnostics = append(res.Diagnostics, jd)
		}
		if len(diags) == 0 {
			res.Diagnostics = append(res.Diagnostics, jsonDiagnostic{Severity: "error", Message: err.Error()})
		}
		writeJSONResult(out, res)
		return 1
	}
	res.Files = len(result.Outputs)
	writeJSONResult(out, res)
	return 0
}

func prepareBuildTimingsPath(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create timings directory %q: %w", dir, err)
	}
	file, err := os.CreateTemp(dir, ".rotor-timings-*")
	if err != nil {
		return fmt.Errorf("create timings destination in %q: %w", dir, err)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return fmt.Errorf("close timings destination %q: %w", name, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove timings destination %q: %w", name, err)
	}
	return nil
}

func writeBuildTimings(path string, timings *compile.BuildTimings) (err error) {
	data, err := json.Marshal(timings)
	if err != nil {
		return fmt.Errorf("encode timings: %w", err)
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".rotor-timings-*")
	if err != nil {
		return fmt.Errorf("create temporary timings file: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary timings file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary timings file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary timings file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace timings output %q: %w", path, err)
	}
	return nil
}

// lineColOf computes 1-based line/col for a byte offset in a file (0,0 if unreadable).
func lineColOf(fileName string, offset int) (int, int) {
	data, err := os.ReadFile(fileName)
	if err != nil || offset < 0 || offset > len(data) {
		return 0, 0
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
