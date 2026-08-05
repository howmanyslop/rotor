package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rotor/internal/compile"
)

// `rotor diagnostics` reports what happened to EVERY file of a project instead
// of stopping at the first failure, optionally over in-memory source
// overrides. It is the census counterpart of `rotor check`: check is
// typecheck-only and never runs the transformer, while build stops at the
// first gate that trips.
//
// It is strictly read-only. It routes through compile.CompileProjectDiagnostics
// (the write-free path), never emits the include folder, and — unlike
// `rotor check` — never refreshes rotor.d.ts.
//
// Exit code, deliberately unlike build and check: 0 whenever a census was
// produced, even one full of diagnostics, and 1 only when no census could be
// produced at all. This command reports; it does not gate. Callers read `ok`
// and the per-file outcomes to decide what to do about the contents.
type diagnosticsArgs struct {
	project  string
	jsonOut  bool
	checkers *int
	help     bool
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

		name := strings.TrimLeft(a, "-")
		value, hasValue := "", false
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value, name = name[eq+1:], name[:eq]
			hasValue = true
		}
		switch name {
		case "p", "project":
			if !hasValue {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("flag %q needs a value", a)
				}
				i++
				value = args[i]
			}
			if value == "" {
				return nil, fmt.Errorf("flag %q needs a value", a)
			}
			res.project = value
			continue
		case "checkers":
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
	File          string             `json:"file"`
	Outcome       string             `json:"outcome"`
	Transformed   bool               `json:"transformed"`
	Diagnostics   []jsonDiagnostic   `json:"diagnostics"`
	InternalError *jsonInternalError `json:"internalError,omitempty"`
}

// jsonDiagnosticsResult extends the stable jsonResult shape rather than
// replacing it: version/ok/files/durationMs/diagnostics keep their existing
// meaning (diagnostics holds what belongs to the project rather than to one
// file), and the census adds the per-file array plus the transformed count.
type jsonDiagnosticsResult struct {
	jsonResult
	Transformed     int                   `json:"transformed"`
	FileDiagnostics []jsonFileDiagnostics `json:"fileDiagnostics"`
}

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

	tsConfigPath, err := findTsConfigPath(parsed.project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor diagnostics: %v\n", err)
		return 1
	}
	dir := filepath.Dir(tsConfigPath)

	opts := compile.ProjectOptions{
		TsConfigPath: tsConfigPath,
		Checkers:     parsed.checkers,
		Overlays:     request.Overlays,
	}

	start := time.Now()
	census, censusErr := compile.CompileProjectDiagnostics(dir, opts)
	elapsed := time.Since(start)

	if parsed.jsonOut {
		writeDiagnosticsJSON(os.Stdout, census, censusErr, elapsed)
	} else {
		writeDiagnosticsText(os.Stdout, census, censusErr, elapsed)
	}
	if censusErr != nil {
		return 1
	}
	return 0
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
	if err := json.Unmarshal(data, &request); err != nil {
		return request, fmt.Errorf("parse overlay request from stdin: %w", err)
	}
	return request, nil
}

func writeDiagnosticsJSON(w io.Writer, census *compile.ProjectDiagnostics, censusErr error, elapsed time.Duration) {
	res := jsonDiagnosticsResult{
		jsonResult: jsonResult{
			Version:     version,
			OK:          censusErr == nil,
			Files:       len(census.Files),
			DurationMs:  elapsed.Milliseconds(),
			Diagnostics: []jsonDiagnostic{},
		},
		Transformed:     census.Transformed,
		FileDiagnostics: []jsonFileDiagnostics{},
	}
	for _, d := range census.Diagnostics {
		res.Diagnostics = append(res.Diagnostics, diagnosticsJSONDiagnostic(d))
	}
	if censusErr != nil && len(census.Diagnostics) == 0 {
		res.Diagnostics = append(res.Diagnostics, jsonDiagnostic{Severity: "error", Message: censusErr.Error()})
	}
	for _, file := range census.Files {
		entry := jsonFileDiagnostics{
			File:        relForDisplay(file.FileName),
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
		}
		res.FileDiagnostics = append(res.FileDiagnostics, entry)
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

func writeDiagnosticsText(w io.Writer, census *compile.ProjectDiagnostics, censusErr error, elapsed time.Duration) {
	if censusErr != nil {
		fmt.Fprintf(w, "census failed: %v\n", censusErr)
		for _, d := range census.Diagnostics {
			fmt.Fprintf(w, "  %s\n", oneLine(d.Message))
		}
		return
	}
	counts := map[compile.FileOutcome]int{}
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
	fmt.Fprintf(w, "\n%d files, %d transformed in %d ms — ok %d, typeError %d, transformerDiagnostic %d, internalCompilerError %d\n",
		len(census.Files), census.Transformed, elapsed.Milliseconds(),
		counts[compile.FileOutcomeOK], counts[compile.FileOutcomeTypeError],
		counts[compile.FileOutcomeTransformerDiagnostic], counts[compile.FileOutcomeInternalCompilerError])
}

// oneLine flattens the embedded newlines rotor diagnostics carry (message plus
// "Suggestion: ...") so one diagnostic stays one line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", " ")
}
