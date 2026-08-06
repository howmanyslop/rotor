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

	"rotor/internal/compile"
)

type diagnosticsArgs struct {
	project   string
	build     bool
	buildPath string
	jsonOut   bool
	builders  *int
	checkers  *int
	help      bool
}

func parseDiagnosticsArgs(args []string) (*diagnosticsArgs, error) {
	res := &diagnosticsArgs{project: "."}
	positional := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
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

		// One or two leading dashes name a flag; "---project" does not.
		if strings.HasPrefix(a, "---") {
			return nil, fmt.Errorf("unknown flag %q", a)
		}

		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value, name = name[eq+1:], name[:eq]
			hasValue = true
		}
		switch name {
		case "p", "project":
			// build.go's takeValue semantics: a flag-like next token is NOT a
			// value. Consuming it unconditionally made `--project --json` set
			// the project to "--json", walk up to the cwd, and census a
			// different project with no error and no JSON.
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				value = args[i]
			}
			if value == "" {
				return nil, fmt.Errorf("flag %q needs a value", a)
			}
			res.project = value
			continue
		case "build", "b":
			// build.go's takeValue semantics verbatim: the path is optional and
			// a flag-like next token is not one, so `--build --json` censuses
			// the solution rooted at --project rather than at "--json".
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				value = args[i]
			}
			res.build = true
			res.buildPath = value
			continue
		case "builders", "checkers":
			if !hasValue && i+1 < len(args) && isNumericFlagValue(args[i+1]) {
				i++
				value = args[i]
			}
			n, err := parsePositiveIntFlag(name, value)
			if err != nil {
				return nil, err
			}
			if name == "builders" {
				res.builders = n
			} else {
				res.checkers = n
			}
			continue
		}
		return nil, fmt.Errorf("unknown flag %q", a)
	}
	if res.builders != nil && !res.build {
		// build.go rejects the same combination: there is nothing to run in
		// parallel without a solution.
		return nil, errors.New("--builders requires --build")
	}
	return res, nil
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
// `rotor build --json` / `rotor check --json` jsonDiagnostic shape.
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

// cmdDiagnostics reports what happened to EVERY file of a project instead of
// stopping at the first failure, optionally over in-memory source overrides.
// It is the census counterpart of `rotor check`: check is typecheck-only and
// never runs the transformer, while build stops at the first gate that trips.
//
// It is strictly read-only. It routes through compile.CompileProjectDiagnostics
// (the write-free path), never emits the include folder, and — unlike
// `rotor check` — never refreshes rotor.d.ts.
//
// Overlays arrive as JSON on stdin, which must be closed (or a terminal) or
// the read blocks. They REPLACE the text of files already in the program;
// they cannot add one, and a key naming no file in the program fails the run.
// Projects with transformer plugins cannot be overlaid at all — the plugin
// sidecar reads source from disk.
//
// Exit code, deliberately unlike build and check: 0 whenever a census was
// produced, even one full of diagnostics, and 1 only when no census could be
// produced at all. This command reports; it does not gate. Callers read `ok`
// and the per-file outcomes to decide what to do about the contents.
func cmdDiagnostics(args []string) int {
	parsed, err := parseDiagnosticsArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor diagnostics: %v\n\n", err)
		usage(os.Stderr)
		return 1 // usage errors exit 1 (rbxtsc parity; see main.go)
	}
	if parsed.help {
		usage(os.Stdout)
		return 0
	}

	request, err := readDiagnosticsRequest(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor diagnostics: %v\n", err)
		return 1
	}

	// cmdBuild's resolution order: --build's optional path wins over --project.
	projectPath := parsed.project
	if parsed.buildPath != "" {
		projectPath = parsed.buildPath
	}
	tsConfigPath, err := findTsConfigPath(projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor diagnostics: %v\n", err)
		return 1
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
	// cost is a divergence from `rotor build` for a project that legitimately
	// sets allowCommentDirectives: true.
	rbxtsOptions, err := readRbxtsOptionsChecked(tsConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor diagnostics: %v\n", err)
		return 1
	}
	merged := mergeProjectOptions(defaultProjectOptions, rbxtsOptions)
	merged.allowCommentDirectives = false

	opts := projectCompileOptions(tsConfigPath, merged)
	opts.Checkers = parsed.checkers
	opts.Builders = parsed.builders
	opts.Overlays = request.Overlays

	start := time.Now()
	projects, overlayMatches, censusErr := runDiagnosticsCensus(dir, tsConfigPath, opts, parsed.build)
	elapsed := time.Since(start)

	if parsed.jsonOut {
		writeDiagnosticsJSON(os.Stdout, projects, overlayMatches, censusErr, elapsed, parsed.build)
	} else {
		writeDiagnosticsText(os.Stdout, os.Stderr, projects, censusErr, elapsed, parsed.build)
	}
	if censusErr != nil {
		return 1
	}
	return 0
}

// runDiagnosticsCensus censuses one project, or — under --build — every project
// the entry tsconfig references. Both arms return the same per-project slice,
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
	jd := jsonDiagnostic{Severity: severity, Message: d.Message}
	if d.FileName != "" {
		jd.File = relForDisplay(d.FileName)
		// Positions come from the compile, not from re-reading the file: under
		// overlays the text on disk is not the text that was compiled.
		jd.Line, jd.Col = d.Line, d.Col
	}
	return jd
}

// writeDiagnosticsText renders the census as text on w. A failure to produce
// one at all is not census output, so it goes to errw with the same
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

	counts := map[compile.FileOutcome]int{}
	files, transformed := 0, 0
	for _, census := range projects {
		// A solution's files come from several projects, so each one heads its
		// own section; a single project has nothing to disambiguate.
		if build {
			fmt.Fprintf(w, "%s\n", relForDisplay(filepath.FromSlash(census.ConfigPath)))
		}
		files += len(census.Files)
		transformed += census.Transformed
		for _, file := range census.Files {
			counts[file.Outcome]++
			if file.Outcome == compile.FileOutcomeOK {
				continue
			}
			fmt.Fprintf(w, "%-22s %s\n", file.Outcome, relForDisplay(file.FileName))
			for _, d := range file.Diagnostics {
				fmt.Fprintf(w, "    %s\n", oneLine(d.Message))
			}
			if file.InternalError != nil {
				fmt.Fprintf(w, "    %s\n", oneLine(file.InternalError.Error()))
			}
		}
		for _, d := range census.Diagnostics {
			fmt.Fprintf(w, "%-22s %s\n", "project", oneLine(d.Message))
		}
	}
	fmt.Fprintf(w, "\n%d files, %d transformed in %d ms — ok %d, typeError %d, transformerDiagnostic %d, internalCompilerError %d\n",
		files, transformed, elapsed.Milliseconds(),
		counts[compile.FileOutcomeOK], counts[compile.FileOutcomeTypeError],
		counts[compile.FileOutcomeTransformerDiagnostic], counts[compile.FileOutcomeInternalCompilerError])

	// Under --build the projects that DID census are worth printing, so the
	// failure follows them instead of replacing them. It still goes to errw:
	// what was censused is the output, and this is why it is incomplete.
	if censusErr != nil {
		fmt.Fprintf(errw, "sloptor diagnostics: census failed: %v\n", censusErr)
	}
}

// oneLine flattens the embedded newlines rotor diagnostics carry (message plus
// "Suggestion: ...") so one diagnostic stays one line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", " ")
}
