package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"rotor/internal/assets"
	"rotor/internal/compile"
	"rotor/internal/logservice"
	"rotor/internal/transformer"
)

// projectTypeChoices are the upstream --type choices (CLI/commands/build.ts
// L98-101: ProjectType.Game | Model | Package).
var projectTypeChoices = map[string]transformer.ProjectType{
	string(transformer.ProjectTypeGame):    transformer.ProjectTypeGame,
	string(transformer.ProjectTypeModel):   transformer.ProjectTypeModel,
	string(transformer.ProjectTypePackage): transformer.ProjectTypePackage,
}

// buildArgs is the parsed `rotor build` argv: the project path (the only
// option with a default — "DO NOT PROVIDE DEFAULTS BELOW HERE",
// CLI/commands/build.ts L62) plus a Partial<ProjectOptions> of exactly the
// flags the user passed.
type buildArgs struct {
	project             string
	opts                partialProjectOptions
	build               bool
	buildPath           string
	emitDeclarationOnly bool
	help                bool
	version             bool
	builders            *int
	checkers            *int
	jsonOut             bool   // rotor DX extension: emit a machine-readable result object
	cpuprofile          string // rotor DX extension: write a pprof CPU profile here
	maxErrors           int    // rotor DX extension: cap the number of rendered code frames (0 = unlimited; default 50)
	bell                bool   // rotor DX extension (watch): ring the bell on a fail<->pass flip
	clearScreen         bool   // rotor DX extension (watch): clear the screen before each rebuild (default true)
	minify              bool   // rotor DX extension: minify emitted Luau before writing
}

// parseBuildArgs parses the rbxtsc-compatible `build` flag surface
// (CLI/commands/build.ts L49-118). Booleans accept `--flag`, `--flag=bool`,
// and the yargs-17 negation `--no-flag`; strings accept `--flag value` and
// `--flag=value` (a missing value parses as "", like yargs — load-bearing
// for the `--rojo` empty-string fall-through quirk). `--usePolling` implies
// `--watch`: yargs errors when usePolling appears in argv without watch.
// As rotor sugar (kept from the earlier CLI), one positional argument is
// accepted as the project path.
func parseBuildArgs(args []string) (*buildArgs, error) {
	// yargs default: --project "."; maxErrors default 50; clear-on-rebuild on.
	res := &buildArgs{project: ".", maxErrors: 50, clearScreen: true}
	positional := ""
	projectSet := false

	boolTargets := func(name string) **bool {
		switch name {
		case "watch", "w":
			return &res.opts.watch
		case "usePolling":
			return &res.opts.usePolling
		case "verbose":
			return &res.opts.verbose
		case "noInclude":
			return &res.opts.noInclude
		case "logTruthyChanges":
			return &res.opts.logTruthyChanges
		case "writeOnlyChanged":
			return &res.opts.writeOnlyChanged
		case "writeTransformedFiles":
			return &res.opts.writeTransformedFiles
		case "optimizedLoops":
			return &res.opts.optimizedLoops
		case "allowCommentDirectives":
			return &res.opts.allowCommentDirectives
		case "luau":
			return &res.opts.luau
		}
		return nil
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-h", "--help":
			res.help = true
			return res, nil
		case "-v", "--version":
			res.version = true
			return res, nil
		}

		if !strings.HasPrefix(a, "-") {
			if positional != "" {
				return nil, fmt.Errorf("unexpected extra argument %q", a)
			}
			positional = a
			continue
		}

		// Split --name=value / alias normalization.
		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value, name = name[eq+1:], name[:eq]
			hasValue = true
		}

		// takeValue consumes the next argv entry as a string value; a
		// missing/flag-like next token yields "" (yargs string options).
		takeValue := func() string {
			if hasValue {
				return value
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				return args[i]
			}
			return ""
		}

		switch name {
		case "project", "p":
			res.project = takeValue()
			projectSet = true
			continue
		case "build", "b":
			res.build = true
			res.buildPath = takeValue()
			continue
		case "includePath", "i":
			v := takeValue()
			res.opts.includePath = &v
			continue
		case "rojo":
			v := takeValue()
			res.opts.rojo = &v
			continue
		case "type":
			v := takeValue()
			if _, ok := projectTypeChoices[v]; !ok {
				return nil, fmt.Errorf("invalid --type %q (choices: game, model, package)", v)
			}
			res.opts.typeName = &v
			continue
		case "cpuprofile":
			res.cpuprofile = takeValue()
			continue
		case "max-errors":
			v := takeValue()
			n := 0
			if v != "" {
				if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
					return nil, fmt.Errorf("invalid --max-errors value %q (must be a non-negative integer)", v)
				}
			}
			res.maxErrors = n
			continue
		case "builders", "checkers":
			v := value
			if !hasValue && i+1 < len(args) && isNumericFlagValue(args[i+1]) {
				i++
				v = args[i]
			}
			n, err := parsePositiveIntFlag(name, v)
			if err != nil {
				return nil, err
			}
			if name == "builders" {
				res.builders = n
			} else {
				res.checkers = n
			}
			continue
		case "json":
			// rotor DX extension (not in rbxtsc): a plain boolean flag that
			// swaps the styled UI for one machine-readable result object.
			res.jsonOut = true
			continue
		case "minify":
			// rotor DX extension: minify emitted Luau before writing.
			res.minify = true
			continue
		}

		// Boolean flags: --flag / --flag=bool / --no-flag.
		negated := false
		if rest, ok := strings.CutPrefix(name, "no-"); ok {
			name, negated = rest, true
		}
		if target := boolTargets(name); target != nil {
			b, err := resolveBool(negated, hasValue, value, name)
			if err != nil {
				return nil, err
			}
			*target = &b
			continue
		}
		if name == "emitDeclarationOnly" {
			b, err := resolveBool(negated, hasValue, value, name)
			if err != nil {
				return nil, err
			}
			res.emitDeclarationOnly = b
			continue
		}
		// rotor DX watch booleans (not part of the rbxtsc flag surface).
		switch name {
		case "bell":
			b, err := resolveBool(negated, hasValue, value, name)
			if err != nil {
				return nil, err
			}
			res.bell = b
			continue
		case "clear":
			b, err := resolveBool(negated, hasValue, value, name)
			if err != nil {
				return nil, err
			}
			res.clearScreen = b
			continue
		}

		return nil, fmt.Errorf("unknown flag %q", a)
	}

	if projectSet && positional != "" {
		return nil, fmt.Errorf("unexpected extra argument %q (project already set via --project)", positional)
	}
	if positional != "" {
		res.project = positional
	}
	if res.builders != nil && !res.build {
		return nil, errors.New("--builders requires --build")
	}

	// yargs `implies: "watch"` (build.ts L68-72): --usePolling present in
	// argv without --watch is a usage error.
	if res.opts.usePolling != nil && res.opts.watch == nil {
		return nil, errors.New("Implications failed:\n usePolling -> watch")
	}
	if res.emitDeclarationOnly && !res.build {
		return nil, errors.New("Implications failed:\n emitDeclarationOnly -> build")
	}
	if res.build && res.emitDeclarationOnly && res.opts.watch != nil && *res.opts.watch {
		return nil, errors.New("--build --watch is incompatible with --emitDeclarationOnly (no Luau emit to incrementally watch)")
	}

	return res, nil
}

// resolveBool resolves a yargs-style boolean flag: bare `--flag` is true,
// `--no-flag` is false, and `--flag=<bool>` takes the explicit value (an outer
// `no-` prefix inverts it). It rejects non-boolean `=value`s.
func resolveBool(negated, hasValue bool, value, name string) (bool, error) {
	b := !negated
	if hasValue {
		switch value {
		case "true", "1":
			b = true
		case "false", "0":
			b = false
		default:
			return false, fmt.Errorf("invalid boolean value %q for --%s", value, name)
		}
		if negated {
			b = !b
		}
	}
	return b, nil
}

// cmdBuild is the compile-to-disk command, porting the rbxtsc build handler
// (CLI/commands/build.ts L120-167): find the tsconfig (file path or upward
// search), merge ProjectOptions (defaults < tsconfig `rbxts` key < argv),
// set LogService verbosity, then compile and write outputs.
//
// Flag wiring status: --type/--noInclude/--includePath/--rojo/--luau/
// --logTruthyChanges/--optimizedLoops/--allowCommentDirectives/--verbose are
// live; --writeOnlyChanged now runs inside the compile package's output
// pipeline; --watch/
// --usePolling drive the polling watch loop;
// --writeTransformedFiles is parsed and ignored (rbxtsc plugin debug output —
// out of v1 scope).
//
// Exit-code policy: usage errors exit 1, matching upstream
// (`.fail(...)` sets exitCode 1, CLI/cli.ts L30-35 — rotor's earlier exit 2
// convention was a documented divergence, removed in Phase 4).
func cmdBuild(args []string) int {
	parsed, err := parseBuildArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotor build: %v\n\n", err)
		usage(os.Stderr)
		return 1
	}
	if parsed.help {
		usage(os.Stdout)
		return 0
	}
	if parsed.version {
		fmt.Println(version)
		return 0
	}

	if parsed.cpuprofile != "" {
		f, err := os.Create(parsed.cpuprofile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rotor build: cannot create cpu profile: %v\n", err)
			return 1
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "rotor build: cannot start cpu profile: %v\n", err)
			return 1
		}
		defer pprof.StopCPUProfile()
	}

	projectPath := parsed.project
	if parsed.buildPath != "" {
		projectPath = parsed.buildPath
	}
	tsConfigPath, err := findTsConfigPath(projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotor build: %v\n", err)
		return 1
	}

	// Merge order (build.ts L125-130): defaults < tsconfig `rbxts` key <
	// argv. Absent CLI booleans (nil) never clobber `rbxts` values.
	rbxtsOptions, err := readRbxtsOptionsChecked(tsConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rotor build: %v\n", err)
		return 1
	}
	opts := mergeProjectOptions(defaultProjectOptions, rbxtsOptions, &parsed.opts)
	opts.minify = parsed.minify // rotor extension: CLI-only, outside the rbxts merge
	opts.emitDeclarationOnly = parsed.emitDeclarationOnly

	// LogService.verbose = projectOptions.verbose === true (build.ts L132).
	logservice.Verbose = opts.verbose

	// Upstream projectPath = path.dirname(tsConfigPath)
	// (createProjectData.ts L13).
	dir := filepath.Dir(tsConfigPath)

	// --json: suppress all styled chrome and emit exactly one result object on
	// stdout. Watch mode has no terminal "end", so it is not JSON-encoded; a
	// one-shot build is what CI/editor integrations call with --json.
	if parsed.jsonOut && !opts.watch {
		return cmdBuildJSON(dir, tsConfigPath, opts)
	}

	out := newUI(os.Stdout)
	out.banner(filepath.Base(dir))

	if opts.writeTransformedFiles {
		out.warn("--writeTransformedFiles is not supported by rotor yet (rbxtsc transformer-plugin debug output; out of v1 scope) — ignoring")
	}
	if opts.watch {
		if parsed.build {
			reload := func() (projectOptions, error) {
				declared, err := readRbxtsOptionsChecked(tsConfigPath)
				if err != nil {
					return projectOptions{}, err
				}
				next := mergeProjectOptions(defaultProjectOptions, declared, &parsed.opts)
				next.minify = parsed.minify
				next.emitDeclarationOnly = parsed.emitDeclarationOnly
				return next, nil
			}
			return runBuildSolutionWatch(tsConfigPath, opts, reload, watchOptions{
				maxErrors: parsed.maxErrors, bell: parsed.bell, clearScreen: parsed.clearScreen,
			})
		}
		return runBuildWatch(dir, tsConfigPath, opts, watchOptions{
			maxErrors:   parsed.maxErrors,
			bell:        parsed.bell,
			clearScreen: parsed.clearScreen,
		})
	}

	if _, statErr := os.Stat(filepath.Join(dir, "package.json")); statErr == nil {
		if _, statErr := os.Stat(filepath.Join(dir, "node_modules")); statErr != nil {
			out.warn(fmt.Sprintf("%s has a package.json but no node_modules — type packages (e.g. @rbxts/*) cannot be resolved; install dependencies first", filepath.Base(dir)))
		}
	}

	var result *compile.BuildResult
	var diags []compile.DiagnosticInfo
	var elapsed time.Duration
	if parsed.build {
		result, diags, elapsed, err = runBuildSolutionOnce(tsConfigPath, opts)
	} else {
		result, diags, elapsed, err = runBuildOnce(dir, tsConfigPath, opts)
	}
	if err != nil {
		newUI(os.Stderr).buildFailure(err.Error(), diags, parsed.maxErrors)
		return 1
	}

	if result.WroteRotorTypes {
		out.noteLine(compile.RotorTypesFileName + "  (generated — editor types for rotor macros)")
	}
	if result.WroteLockfile {
		out.noteLine(assets.LockfileName + "  (updated — uploaded new $asset assets)")
	}
	copiedFiles := len(result.Outputs) - len(result.EmittedFiles)
	if copiedFiles < 0 {
		copiedFiles = 0
	}
	out.buildSuccess(len(result.Outputs), len(result.EmittedFiles), copiedFiles, elapsed)
	return 0
}

func runBuildOnce(dir, tsConfigPath string, opts projectOptions) (*compile.BuildResult, []compile.DiagnosticInfo, time.Duration, error) {
	transformer.HeaderComment = " Compiled with @isentinel/roblox-ts v4.0.11"

	start := time.Now()
	result, msgs, err := compile.BuildProjectWithOptions(dir, projectCompileOptions(tsConfigPath, opts))
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

func runBuildSolutionOnce(tsConfigPath string, opts projectOptions) (*compile.BuildResult, []compile.DiagnosticInfo, time.Duration, error) {
	transformer.HeaderComment = " Compiled with @isentinel/roblox-ts v4.0.11"
	start := time.Now()
	result, msgs, err := compile.BuildSolutionWithOptions(tsConfigPath, projectCompileOptions(tsConfigPath, opts))
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
	}
}

// jsonDiagnostic is one entry in the --json diagnostics array. file/line/col
// are populated from the structured DiagnosticInfo location when available;
// `rotor check --json` also fills these from its own structured AST diagnostics.
type jsonDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// jsonResult is the single object printed by `rotor build --json` /
// `rotor check --json`. The shape is stable for CI/editor integration.
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
// instead of the styled UI. Exit code is unchanged from the styled path: 1 on
// any build error, 0 otherwise.
func cmdBuildJSON(dir, tsConfigPath string, opts projectOptions) int {
	result, diags, elapsed, err := runBuildOnce(dir, tsConfigPath, opts)
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
			jd := jsonDiagnostic{Severity: sev, Message: d.Message}
			if d.FileName != "" {
				jd.File = relForDisplay(d.FileName)
				jd.Line, jd.Col = lineColOf(d.FileName, d.Offset)
			}
			res.Diagnostics = append(res.Diagnostics, jd)
		}
		if len(diags) == 0 {
			res.Diagnostics = append(res.Diagnostics, jsonDiagnostic{Severity: "error", Message: err.Error()})
		}
		writeJSONResult(os.Stdout, res)
		return 1
	}
	res.Files = len(result.Outputs)
	writeJSONResult(os.Stdout, res)
	return 0
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
