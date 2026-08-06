package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/compile"
)

// diagnosticsArgs is the parsed `sloptor diagnostics` argv, assembled by
// newDiagnosticsCommand from Cobra flags.
type diagnosticsArgs struct {
	project   string
	build     bool
	buildPath string
	jsonOut   bool
	builders  *int
	checkers  *int
}

// newDiagnosticsCommand is the census counterpart of `sloptor check`: it
// reports what happened to EVERY file of a project instead of stopping at the
// first failure, optionally over in-memory source overrides.
func newDiagnosticsCommand(streams cliStreams) *cobra.Command {
	var args diagnosticsArgs
	cmd := &cobra.Command{
		Use:                   "diagnostics [options] [path]",
		Short:                 "report EVERY file's outcome instead of stopping at the first failure",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runDiagnosticsCommand(streams, &args, cmd, argv)
		},
	}
	flags := cmd.Flags()
	flags.SortFlags = false
	addStringFlag(cmd, &args.project, "project", "p", "", "<path>",
		"project path (default \".\"): a tsconfig file, a directory containing one, or any path to search upward from")
	addStringFlag(cmd, &args.buildPath, "build", "b", "", "[path]",
		"census a solution of project references (optionally select a tsconfig path)")
	cmd.Flags().VarP(newPositiveIntValue(&args.builders), "builders", "",
		"number of projects to build concurrently (default 4; only with --build)")
	setFlagPlaceholder(cmd, "builders", "<n>")
	cmd.Flags().VarP(newPositiveIntValue(&args.checkers), "checkers", "",
		"number of checkers per project (default 4; build and check)")
	setFlagPlaceholder(cmd, "checkers", "<n>")
	addBoolFlag(cmd, &args.jsonOut, "json", "", false,
		"emit one machine-readable result object instead of styled output")
	return cmd
}

// runDiagnosticsCommand loads the overlay request from stdin, resolves the
// config like build does, censuses, and renders. It is deliberately read-only
// and deliberately does not gate: exit 1 only when no census could be
// produced at all.
func runDiagnosticsCommand(streams cliStreams, args *diagnosticsArgs, cmd *cobra.Command, argv []string) error {
	f := cmd.Flags()
	if f.Changed("project") && args.project == "" {
		return usageFailure("flag \"--project\" needs a value")
	}
	if args.project == "" {
		args.project = "."
	}
	if len(argv) > 0 {
		if f.Changed("project") {
			return usageFailure("unexpected extra argument %q (project already set via --project)", argv[0])
		}
		args.project = argv[0]
	}
	if f.Changed("build") {
		args.build = true
		args.buildPath, _ = f.GetString("build")
	}
	if args.builders != nil && !args.build {
		return usageFailure("--builders requires --build")
	}

	request, err := readDiagnosticsRequest(streams.in)
	if err != nil {
		return runtimeFailure(err)
	}

	// cmdBuild's resolution order: --build's optional path wins over --project.
	projectPath := args.project
	if args.buildPath != "" {
		projectPath = args.buildPath
	}
	tsConfigPath, err := findTsConfigPath(projectPath)
	if err != nil {
		return runtimeFailure(err)
	}
	dir := filepath.Dir(tsConfigPath)

	// The tsconfig `rbxts` key still decides the project's SHAPE — type, Rojo
	// project, include path — without which some projects cannot be censused
	// at all. allowCommentDirectives is the one option deliberately not
	// honored.
	//
	// Note what that does and does not buy. It does NOT stop @ts-ignore from
	// suppressing type errors: the directive is honored inside the checker and
	// no flag rotor sets undoes that. What it does is add rotor's own
	// "comment directives are not supported" diagnostic, so a file leaning on
	// them shows up as transformerDiagnostic instead of passing as `ok`. The
	// cost is a divergence from `sloptor build` for a project that legitimately
	// sets allowCommentDirectives: true.
	rbxtsOptions, err := readRbxtsOptionsChecked(tsConfigPath)
	if err != nil {
		return runtimeFailure(err)
	}
	merged := mergeProjectOptions(defaultProjectOptions, rbxtsOptions)
	merged.allowCommentDirectives = false

	opts := projectCompileOptions(tsConfigPath, merged)
	opts.Checkers = args.checkers
	opts.Builders = args.builders
	opts.Overlays = request.Overlays

	start := time.Now()
	projects, overlayMatches, censusErr := runDiagnosticsCensus(dir, tsConfigPath, opts, args.build)
	elapsed := time.Since(start)

	if args.jsonOut {
		writeDiagnosticsJSON(streams.out, projects, overlayMatches, censusErr, elapsed, args.build)
	} else {
		writeDiagnosticsText(streams.out, streams.err, projects, censusErr, elapsed, args.build)
	}
	if censusErr != nil {
		return reportedFailure(censusErr)
	}
	return nil
}

// diagnosticsRequest is the JSON object read from stdin. Overlays replace the
// on-disk text of the listed files for this run only, keyed by absolute path.
// argv cannot carry a project's worth of source, which is why this is stdin
// and not a flag.
type diagnosticsRequest struct {
	Overlays map[string]string `json:"overlays"`
}

// jsonInternalError is the structured form of a transformer panic. A consumer
// classifies on the file's outcome and reads these for the detail; it never
// has to match a message prefix.
type jsonInternalError struct {
	Message string `json:"message"`
	Stack   string `json:"stack"`
}

// jsonFileDiagnostics is one file's census entry. Diagnostics reuses the
// `sloptor build --json` / `sloptor check --json` jsonDiagnostic shape.
type jsonFileDiagnostics struct {
	File string `json:"file"`
	// Project is the config path of the project that compiled this file — the
	// join key into the top-level `projects` array. Present only under --build,
	// where a flat file list would otherwise not say which project a file came
	// from; a single-project run has one answer and omits it.
	Project       string             `json:"project,omitempty"`
	Outcome       string             `json:"outcome"`
	Transformed   bool               `json:"transformed"`
	Diagnostics   []jsonDiagnostic   `json:"diagnostics"`
	InternalError *jsonInternalError `json:"internalError,omitempty"`
}

// jsonProjectDiagnostics is one project's entry in a --build census: its
// attribution, its own totals, and the diagnostics that belong to the project
// rather than to one of its files.
//
// Its Diagnostics are the attributed subset of the top-level diagnostics array,
// not an addition to it — every one of them also appears there. Solution-level
// failures (an unreadable reference graph, an overlay no project matched)
// belong to no project and appear only at the top level.
type jsonProjectDiagnostics struct {
	ProjectDir     string           `json:"projectDir"`
	ConfigPath     string           `json:"configPath"`
	OK             bool             `json:"ok"`
	Files          int              `json:"files"`
	Transformed    int              `json:"transformed"`
	OverlayMatches int              `json:"overlayMatches"`
	Diagnostics    []jsonDiagnostic `json:"diagnostics"`
}

// jsonDiagnosticsResult extends the stable jsonResult shape rather than
// replacing it: version/ok/files/durationMs/diagnostics keep their existing
// meaning (diagnostics holds what belongs to a project rather than to one
// file), and the census adds the per-file array plus the transformed count.
//
// Every top-level number is a SOLUTION-WIDE aggregate: files, transformed and
// diagnostics are the totals across every project, and fileDiagnostics is every
// project's files in dependency order. A single-project run therefore emits
// byte-identical output to the version of this command that had no solution
// support — `projects` and the per-file `project` are the only new keys, and
// both are omitted without --build.
type jsonDiagnosticsResult struct {
	jsonResult
	Transformed int `json:"transformed"`
	// OverlayMatches counts the stdin overlays that named a file some program
	// actually holds. An overlay that matches nothing fails the run, so a
	// consumer can assert this equals the number it sent. Under --build it
	// counts distinct overlays, not per-project matches: a referenced project's
	// sources are in its dependents' programs too, so one overlay can match
	// several projects.
	OverlayMatches int `json:"overlayMatches"`
	// Projects carries per-project detail and is present only under --build.
	// The files themselves stay in the single flat fileDiagnostics array, each
	// tagged with its project, rather than being repeated here.
	Projects        []jsonProjectDiagnostics `json:"projects,omitempty"`
	FileDiagnostics []jsonFileDiagnostics    `json:"fileDiagnostics"`
}

// runDiagnosticsCensus censuses one project, or — under --build — every project
// so the renderers below have one shape to handle and the single-project output
// is what it always was.
//
// The returned overlay-match count is solution-wide: distinct overlays matched,
// not the sum of the projects' counts.
func runDiagnosticsCensus(dir, tsConfigPath string, opts compile.ProjectOptions, build bool) ([]*compile.ProjectDiagnostics, int, error) {
	if build {
		solution, err := compile.CompileSolutionDiagnostics(tsConfigPath, opts)
		return solution.Projects, solution.OverlayMatches, err
	}
	census, err := compile.CompileProjectDiagnostics(dir, opts)
	return []*compile.ProjectDiagnostics{census}, census.OverlayMatches, err
}

// readDiagnosticsRequest reads the overlay request from r. An empty stream —
// and an interactive terminal, which would otherwise block forever — means no
// overlays.
func readDiagnosticsRequest(r io.Reader) (diagnosticsRequest, error) {
	var request diagnosticsRequest
	if f, ok := r.(*os.File); ok {
		info, err := f.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return request, nil
		}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return request, fmt.Errorf("read overlay request from stdin: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return request, nil
	}
	// Unknown fields are rejected: a typo'd wrapper key would otherwise parse
	// to an empty overlay set and census the unmodified tree, reporting green
	// on source the caller never asked about.
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		return diagnosticsRequest{}, fmt.Errorf("parse overlay request from stdin: %w", err)
	}
	return request, nil
}

func writeDiagnosticsJSON(w io.Writer, projects []*compile.ProjectDiagnostics, overlayMatches int, censusErr error, elapsed time.Duration, build bool) {
	res := jsonDiagnosticsResult{
		jsonResult: jsonResult{
			Version:     version,
			OK:          censusErr == nil,
			DurationMs:  elapsed.Milliseconds(),
			Diagnostics: []jsonDiagnostic{},
		},
		OverlayMatches:  overlayMatches,
		FileDiagnostics: []jsonFileDiagnostics{},
	}
	if build {
		res.Projects = []jsonProjectDiagnostics{}
	}

	for _, census := range projects {
		project := jsonProjectDiagnostics{
			ProjectDir:     relForDisplay(filepath.FromSlash(census.ProjectDir)),
			ConfigPath:     relForDisplay(filepath.FromSlash(census.ConfigPath)),
			OK:             len(census.Diagnostics) == 0,
			Files:          len(census.Files),
			Transformed:    census.Transformed,
			OverlayMatches: census.OverlayMatches,
			Diagnostics:    []jsonDiagnostic{},
		}
		res.Files += len(census.Files)
		res.Transformed += census.Transformed
		for _, d := range census.Diagnostics {
			jd := diagnosticsJSONDiagnostic(d)
			res.Diagnostics = append(res.Diagnostics, jd)
			project.Diagnostics = append(project.Diagnostics, jd)
		}
		// Only a --build run has more than one answer to "which project?", and
		// only there is the key worth its bytes on every file of the census.
		attribution := ""
		if build {
			attribution = project.ConfigPath
		}
		for _, file := range census.Files {
			entry := jsonFileDiagnostics{
				File:        relForDisplay(file.FileName),
				Project:     attribution,
				Outcome:     string(file.Outcome),
				Transformed: file.Transformed,
				Diagnostics: []jsonDiagnostic{},
			}
			for _, d := range file.Diagnostics {
				entry.Diagnostics = append(entry.Diagnostics, diagnosticsJSONDiagnostic(d))
			}
			if file.InternalError != nil {
				entry.InternalError = &jsonInternalError{
					Message: file.InternalError.Error(),
					Stack:   string(file.InternalError.Stack),
				}
			}
			if file.Outcome != compile.FileOutcomeOK {
				res.OK = false
				project.OK = false
			}
			res.FileDiagnostics = append(res.FileDiagnostics, entry)
		}
		if build {
			res.Projects = append(res.Projects, project)
		}
	}

	// A failure with nothing attributed to any project — a reference graph that
	// could not be read, an overlay no project matched — would otherwise leave
	// the reason for a non-zero exit nowhere in the output.
	if censusErr != nil && len(res.Diagnostics) == 0 {
		res.Diagnostics = append(res.Diagnostics, jsonDiagnostic{Severity: "error", Message: censusErr.Error()})
	}
	if len(res.Diagnostics) > 0 {
		res.OK = false
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(res)
}

func diagnosticsJSONDiagnostic(d compile.DiagnosticInfo) jsonDiagnostic {
	severity := "error"
	if d.Warning {
		severity = "warning"
	}
	jd := jsonDiagnostic{Code: d.Code, Severity: severity, Message: d.Message}
	if d.FileName != "" {
		jd.File = relForDisplay(d.FileName)
		// Positions come from the compile, not from re-reading the file: under
		// overlays the text on disk is not the text that was compiled.
		jd.Line, jd.Col = d.Line, d.Col
	}
	return jd
}

// writeDiagnosticsText renders the census as text on w: one aligned event row
// per non-ok file (status word, relative file, one-line detail), project-level
// diagnostics, and a final timed census row. A failure to produce a census at
// all is not census output, so it goes to errw with the same
// "sloptor diagnostics: " prefix every other failure of this command carries —
// never into the stdout stream a consumer is parsing.
func writeDiagnosticsText(w, errw io.Writer, projects []*compile.ProjectDiagnostics, censusErr error, elapsed time.Duration, build bool) {
	if censusErr != nil && !build {
		fmt.Fprintf(errw, "sloptor diagnostics: census failed: %v\n", censusErr)
		for _, census := range projects {
			for _, d := range census.Diagnostics {
				fmt.Fprintf(errw, "  %s\n", oneLine(d.Message))
			}
		}
		return
	}

	u := newUI(w)
	counts := map[compile.FileOutcome]int{}
	files, transformed := 0, 0
	var events []uiEvent
	for _, census := range projects {
		// A solution's files come from several projects, so each one heads its
		// own section; a single project has nothing to disambiguate.
		if build {
			fmt.Fprintf(w, "%s\n", u.s.Muted(relForDisplay(filepath.FromSlash(census.ConfigPath))))
		}
		files += len(census.Files)
		transformed += census.Transformed
		for _, file := range census.Files {
			counts[file.Outcome]++
			if file.Outcome == compile.FileOutcomeOK {
				continue
			}
			var details []string
			if len(file.Diagnostics) > 0 {
				for _, d := range file.Diagnostics {
					details = append(details, oneLine(d.Message))
				}
			}
			if file.InternalError != nil {
				details = append(details, oneLine(file.InternalError.Error()))
			}
			detail := string(file.Outcome)
			if len(details) > 0 {
				detail += " — " + strings.Join(details, " · ")
			}
			events = append(events, uiEvent{
				Status: eventFailed,
				Target: relForDisplay(file.FileName),
				Detail: detail,
			})
		}
		for _, d := range census.Diagnostics {
			events = append(events, uiEvent{Status: eventFailed, Target: "(project)", Detail: oneLine(d.Message)})
		}
	}
	events = append(events, uiEvent{
		Status: eventFinished,
		Detail: fmt.Sprintf("%d files, %d transformed in %d ms — ok %d, typeError %d, transformerDiagnostic %d, internalCompilerError %d",
			files, transformed, elapsed.Milliseconds(),
			counts[compile.FileOutcomeOK], counts[compile.FileOutcomeTypeError],
			counts[compile.FileOutcomeTransformerDiagnostic], counts[compile.FileOutcomeInternalCompilerError]),
	})
	u.events(events)

	// Under --build the projects that DID census are worth printing, so the
	// failure follows them instead of replacing them. It still goes to errw:
	// what was censused is the output, and this is why it is incomplete.
	if censusErr != nil {
		fmt.Fprintf(errw, "sloptor diagnostics: census failed: %v\n", censusErr)
	}
}

// oneLine flattens the embedded newlines sloptor diagnostics carry (message plus
// "Suggestion: ...") so one diagnostic stays one line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", " ")
}
