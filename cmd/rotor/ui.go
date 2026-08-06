package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rotor/internal/compile"
	"rotor/internal/diagframe"
	"rotor/internal/term"
)

// ui renders rotor's human-facing CLI chrome (headers, summaries, the watch
// banner) with color when the target is a terminal. It is intentionally
// separate from internal/logservice, which owns the upstream-compatible stdout
// channel (compiler warnings and --verbose benchmark lines) and stays
// byte-stable for differential tests.
type ui struct {
	w io.Writer
	s *term.Styler
}

func newUI(w io.Writer) *ui {
	return &ui{w: w, s: term.For(w)}
}

// banner prints the compact product header shown at the top of a command.
func (u *ui) banner(sub string) {
	g := u.s.Glyphs()
	head := u.s.Accent(u.s.Bold("sloptor")) + " " + u.s.Muted("v"+version)
	if sub != "" {
		head += "  " + u.s.Muted(g.Dot) + "  " + u.s.Info(sub)
	}
	fmt.Fprintf(u.w, "\n  %s\n\n", head)
}

// buildSuccess prints the post-build result block.
func (u *ui) buildSuccess(total, written, unchanged int, elapsed time.Duration) {
	g := u.s.Glyphs()
	ms := elapsed.Milliseconds()

	headline := fmt.Sprintf("%s  %s %s",
		u.s.SuccessBold(g.Check),
		u.s.Bold(fmt.Sprintf("compiled %s", plural(total, "file"))),
		u.s.Muted(fmt.Sprintf("in %d ms", ms)),
	)

	// Detail line: written/unchanged · throughput. Each part carries its own
	// style, so the separators (joinDot) are the only muted chrome.
	parts := []string{u.s.Bold(itoa(written)) + u.s.Muted(" written")}
	if unchanged > 0 {
		parts = append(parts, u.s.Muted(fmt.Sprintf("%d unchanged", unchanged)))
	}
	if ms > 0 {
		rate := int(float64(total) / (float64(ms) / 1000.0))
		parts = append(parts, u.s.Muted(fmt.Sprintf("%d files/s", rate)))
	}

	fmt.Fprintf(u.w, "  %s\n", headline)
	fmt.Fprintf(u.w, "    %s\n\n", joinDot(u.s, parts))
}

// buildFailure prints the failure headline, then a code frame per file.
// maxFrames caps the number of rendered frames (0 = unlimited).
func (u *ui) buildFailure(headline string, diags []compile.DiagnosticInfo, maxFrames int) {
	g := u.s.Glyphs()
	fmt.Fprintf(u.w, "  %s  %s\n", u.s.ErrorBold(g.Cross), u.s.Bold(headline))
	if len(diags) == 0 {
		fmt.Fprintln(u.w)
		return
	}
	fmt.Fprintln(u.w)
	fmt.Fprint(u.w, renderDiagFrames(u.w, diags, maxFrames))
	fmt.Fprintln(u.w)
}

// renderDiagFrames groups located diagnostics by file, reads each file's source,
// and renders frames; diagnostics without a usable location fall back to plain
// indented message lines.
func renderDiagFrames(w io.Writer, diags []compile.DiagnosticInfo, maxFrames int) string {
	color := term.ColorEnabled(w)
	byFile := map[string][]diagframe.Spot{}
	var order []string
	var loose []string
	for _, d := range diags {
		sev := diagframe.Error
		if d.Warning {
			sev = diagframe.Warning
		}
		if d.FileName == "" || d.Len == 0 {
			loose = append(loose, d.Message)
			continue
		}
		primary, help := splitHelp(d.Message)
		if _, ok := byFile[d.FileName]; !ok {
			order = append(order, d.FileName)
		}
		byFile[d.FileName] = append(byFile[d.FileName], diagframe.Spot{
			Offset: d.Offset, Len: d.Len, Severity: sev, Code: d.Code, Message: primary, Help: help,
		})
	}
	var groups []diagframe.Group
	for _, fn := range order {
		src, _ := os.ReadFile(fn)
		groups = append(groups, diagframe.Group{
			Path: relForDisplay(fn), Source: string(src), Lang: diagframe.TypeScript, Spots: byFile[fn],
		})
	}
	out := diagframe.RenderGroups(groups, diagframe.Options{Color: color, Link: color}, maxFrames)
	for _, l := range loose {
		out += "    " + l + "\n"
	}
	return out
}

// splitHelp separates a diagnostic message into its primary line(s) and any
// trailing "Suggestion:" / "More information:" lines (rendered as help:).
func splitHelp(message string) (primary string, help []string) {
	parts := strings.Split(message, "\n")
	primary = parts[0]
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "Suggestion: ") || strings.HasPrefix(p, "More information: ") {
			help = append(help, p)
		} else {
			primary += "\n" + p
		}
	}
	return primary, help
}

// relForDisplay shows a path relative to the working directory when possible.
func relForDisplay(p string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return p
}

// warn prints a single warning line in rotor chrome (distinct from the
// upstream-format logservice.Warn used for compiler warnings).
func (u *ui) warn(msg string) {
	g := u.s.Glyphs()
	fmt.Fprintf(u.w, "  %s  %s\n", u.s.WarnBold(g.Warn), msg)
}

// checkSummary prints the `sloptor check` result line.
func (u *ui) checkSummary(files int, errs int, elapsed time.Duration) {
	g := u.s.Glyphs()
	ms := elapsed.Milliseconds()
	if errs == 0 {
		fmt.Fprintf(u.w, "\n  %s  %s %s\n\n",
			u.s.SuccessBold(g.Check),
			u.s.Bold(fmt.Sprintf("checked %s", plural(files, "file"))),
			u.s.Muted(fmt.Sprintf("in %d ms · no errors", ms)))
		return
	}
	fmt.Fprintf(u.w, "\n  %s  %s %s\n\n",
		u.s.ErrorBold(g.Cross),
		u.s.Bold(fmt.Sprintf("checked %s", plural(files, "file"))),
		u.s.Muted(fmt.Sprintf("in %d ms · ", ms))+u.s.Error(plural(errs, "error")))
}

// watchBanner prints the "watching N files" idle line.
func (u *ui) watchBanner(n int, stats *watchStats) {
	g := u.s.Glyphs()
	parts := []string{u.s.Muted(fmt.Sprintf("watching %s for changes", plural(n, "file")))}
	parts = append(parts, u.watchStatParts(stats)...)
	parts = append(parts, u.s.Muted("Ctrl+C to exit"))
	fmt.Fprintf(u.w, "\n  %s  %s\n", u.s.Info(g.Watch), joinDot(u.s, parts))
}

// watchIdle prints the post-build "watching for changes" line for build watch,
// where the watched set is the whole project tree rather than a fixed count.
// After a failed build it shows a persistent ✗ error banner so the failure
// state stays visible after the code frames have scrolled off.
func (u *ui) watchIdle(stats *watchStats) {
	g := u.s.Glyphs()
	icon := u.s.Info(g.Watch)
	var parts []string
	if stats != nil && stats.lastFailed {
		icon = u.s.ErrorBold(g.Cross)
		parts = append(parts, u.s.Error(plural(stats.lastErrCount, "error"))+u.s.Muted(" — watching for changes"))
	} else {
		parts = append(parts, u.s.Muted("watching for changes"))
	}
	parts = append(parts, u.watchStatParts(stats)...)
	parts = append(parts, u.s.Muted("Ctrl+C to exit"))
	fmt.Fprintf(u.w, "  %s  %s\n", icon, joinDot(u.s, parts))
}

// watchStatParts renders the rebuild counter and the build-time sparkline for
// the idle lines: `rebuild #4 · 142 ms ▂▁▇▂`. Empty until the first rebuild.
func (u *ui) watchStatParts(stats *watchStats) []string {
	if stats == nil || stats.builds <= 1 {
		return nil
	}
	parts := []string{u.s.Muted(fmt.Sprintf("rebuild #%d", stats.builds-1))}
	if n := len(stats.history); n > 0 {
		last := u.s.Muted(fmt.Sprintf("%d ms", stats.history[n-1].Milliseconds()))
		// The sparkline is terminal chrome only — in piped/redirected output
		// the ASCII ramp reads as noise, so it stays interactive-only.
		if n > 1 && u.s.Color() {
			values := make([]float64, n)
			for i, d := range stats.history {
				values[i] = float64(d.Milliseconds())
			}
			last += " " + u.s.Info(u.s.Spark(values))
		}
		parts = append(parts, last)
	}
	return parts
}

// watchChanges prints the rule + change-detected line on a rebuild, listing
// the settled batch of changed files (up to three basenames).
func (u *ui) watchChanges(paths []string) {
	names := make([]string, 0, 3)
	for i, p := range paths {
		if i == 3 {
			break
		}
		names = append(names, filepath.Base(p))
	}
	label := strings.Join(names, ", ")
	if extra := len(paths) - len(names); extra > 0 {
		label += fmt.Sprintf(", +%d more", extra)
	}
	fmt.Fprintf(u.w, "\n%s\n", u.s.Rule(64))
	fmt.Fprintf(u.w, "  %s %s %s\n\n",
		u.s.Muted(clock()),
		u.s.Info(plural(len(paths), "file")+" changed"),
		u.s.Muted(u.s.Glyphs().Dot+" "+label))
}

// events renders one aligned row per uiEvent: `  <status>  <target>  <detail>
// glyph — carries the signal: green for work done, yellow Skipped, gray
// Unchanged/Planned, red Failed.
type eventStatus int

const (
	eventCreated eventStatus = iota
	eventUpdated
	eventWrote
	eventRemoved
	eventChecked
	eventFinished
	eventSkipped
	eventUnchanged
	eventPlanned
	eventFailed
)

func (s eventStatus) String() string {
	switch s {
	case eventCreated:
		return "Created"
	case eventUpdated:
		return "Updated"
	case eventWrote:
		return "Wrote"
	case eventRemoved:
		return "Removed"
	case eventChecked:
		return "Checked"
	case eventFinished:
		return "Finished"
	case eventSkipped:
		return "Skipped"
	case eventUnchanged:
		return "Unchanged"
	case eventPlanned:
		return "Planned"
	case eventFailed:
		return "Failed"
	}
	return ""
}

// uiEvent is one row of a completed batch: a status word, a target (a
// slash-normalized working-directory-relative file path, or a logical
// resource name), an optional dim detail, and an optional measured duration.
type uiEvent struct {
	Status  eventStatus
	Target  string
	Detail  string
	Elapsed time.Duration
}

// events renders one aligned row per uiEvent: `  <status>  <target>  <detail>
// <elapsed>`. The status and target columns are padded to the widest visible
// cell across the batch; empty trailing columns are omitted rather than
// placeholder-padded, so a target-less Finished row sits tight against its
// timing.
func (u *ui) events(events []uiEvent) {
	if len(events) == 0 {
		return
	}
	statusW, targetW := 0, 0
	for _, e := range events {
		if w := term.VisibleLen(e.Status.String()); w > statusW {
			statusW = w
		}
		if w := term.VisibleLen(e.Target); w > targetW {
			targetW = w
		}
	}
	for _, e := range events {
		cells := []string{padVisible(u.statusWord(e.Status), statusW)}
		if e.Target != "" {
			cells = append(cells, padVisible(relForDisplay(e.Target), targetW))
		}
		if e.Detail != "" {
			cells = append(cells, u.s.Muted(e.Detail))
		}
		if e.Elapsed > 0 {
			cells = append(cells, u.s.Muted(fmt.Sprintf("in %d ms", e.Elapsed.Milliseconds())))
		}
		fmt.Fprintf(u.w, "  %s\n", strings.Join(cells, "  "))
	}
}

// statusWord renders the colored status word: green for completed work, yellow
// Skipped, gray Unchanged/Planned, red Failed.
func (u *ui) statusWord(s eventStatus) string {
	switch s {
	case eventSkipped:
		return u.s.WarnBold(s.String())
	case eventUnchanged, eventPlanned:
		return u.s.Muted(s.String())
	case eventFailed:
		return u.s.ErrorBold(s.String())
	default:
		return u.s.SuccessBold(s.String())
	}
}

// okLine prints a generic success row: ✓ + bold headline + muted detail.
// The shared shape for every command's "work done" summary line.
func (u *ui) okLine(headline, detail string) {
	g := u.s.Glyphs()
	line := fmt.Sprintf("  %s  %s", u.s.SuccessBold(g.Check), u.s.Bold(headline))
	if detail != "" {
		line += "  " + u.s.Muted(detail)
	}
	fmt.Fprintln(u.w, line)
}

// failLine prints a generic failure row: ✗ + bold message. The shared shape
// for every command's operational-error rendering (callers still exit 1).
func (u *ui) failLine(msg string) {
	g := u.s.Glyphs()
	fmt.Fprintf(u.w, "  %s  %s\n", u.s.ErrorBold(g.Cross), u.s.Bold(msg))
}

// noteLine prints a muted arrow row for secondary facts (generated files,
// artifact paths).
func (u *ui) noteLine(msg string) {
	g := u.s.Glyphs()
	fmt.Fprintf(u.w, "    %s %s\n", u.s.Muted(g.Arrow), u.s.Muted(msg))
}

// --- small formatting helpers ---

// formatBytes renders a byte count compactly (B / KB / MB, one decimal).
func formatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func clock() string { return time.Now().Format("15:04:05") }

// joinDot joins parts with a muted middot separator.
func joinDot(s *term.Styler, parts []string) string {
	return strings.Join(parts, " "+s.Glyphs().Dot+" ")
}
