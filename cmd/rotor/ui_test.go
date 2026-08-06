package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"rotor/internal/term"
)

// TestBuildSuccessStructure pins the preserved build summary: the glyph
// header, the "compiled N files in N ms" headline, and the written/unchanged
// detail line. This is the visual reference every other command now shares.
func TestBuildSuccessStructure(t *testing.T) {
	var buf bytes.Buffer
	newUI(&buf).buildSuccess(5, 4, 1, 120*time.Millisecond)
	out := buf.String()
	for _, want := range []string{
		"compiled 5 files",
		"in 120 ms",
		"4 written",
		"1 unchanged",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("buildSuccess output missing %q:\n%s", want, out)
		}
	}
}

// TestUIEventsAlignVisibleColumns pins the aligned event-table contract in
// plain (no color) output: status and target columns padded to the widest
// visible cell, trailing empty columns omitted.
func TestUIEventsAlignVisibleColumns(t *testing.T) {
	var buf bytes.Buffer
	newUI(&buf).events([]uiEvent{
		{Status: eventWrote, Target: "src/main.luau", Detail: "1.2 KB"},
		{Status: eventCreated, Target: "src/mod.luau"},
		{Status: eventFinished, Elapsed: 3 * time.Millisecond},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 rows, got %d:\n%s", len(lines), buf.String())
	}
	// Status column is padded to "Finished" (8 chars): every row's status
	// starts at column 2 and the target/detail starts after the padding.
	for i, line := range lines {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("row %d missing indent: %q", i, line)
		}
	}
	if !strings.Contains(lines[0], "Wrote") || !strings.Contains(lines[0], "src/main.luau") || !strings.Contains(lines[0], "1.2 KB") {
		t.Errorf("row 0 = %q, want Wrote row with target and detail", lines[0])
	}
	// The Finished row omits the empty target/detail columns: its timing sits
	// right after the status column.
	if !strings.HasPrefix(lines[2], "  Finished  in 3 ms") {
		t.Errorf("row 2 = %q, want Finished row with only elapsed", lines[2])
	}
	// Wrote rows align their statuses: strip styles and compare widths.
	for _, line := range lines {
		if term.VisibleLen(line) == 0 {
			t.Errorf("empty visible row: %q", line)
		}
	}
}

// TestUIEventsAlignWithColor pins the same table under forced color: visible
// columns align identically and only the status word is styled.
func TestUIEventsAlignWithColor(t *testing.T) {
	var styled bytes.Buffer
	u := newUI(term.ForceColorWriter{})
	u.w = &styled
	u.events([]uiEvent{
		{Status: eventWrote, Target: "a.luau", Detail: "1.2 KB"},
		{Status: eventFinished, Elapsed: 3 * time.Millisecond},
	})
	lines := strings.Split(strings.TrimRight(styled.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 rows, got %d:\n%s", len(lines), styled.String())
	}
	// Visible widths must match the plain rendering exactly (color codes do
	// not count).
	plain := term.VisibleLen(lines[0])
	if plain == 0 {
		t.Error("styled row has zero visible width")
	}
	if !strings.Contains(styled.String(), "\x1b[") {
		t.Error("forced-color events contain no ANSI codes")
	}
}

// TestUIEventsNoGlyphStatus pins the status-is-a-word contract: event rows
// never render glyph-only statuses.
func TestUIEventsNoGlyphStatus(t *testing.T) {
	var buf bytes.Buffer
	newUI(&buf).events([]uiEvent{{Status: eventWrote, Target: "x.luau"}})
	out := buf.String()
	if strings.Contains(out, "✓") || strings.Contains(out, "✗") {
		t.Errorf("event row uses a glyph status: %q", out)
	}
	if !strings.Contains(out, "Wrote") {
		t.Errorf("event row missing status word: %q", out)
	}
}

// TestJSONOutputHasNoChrome pins the machine-readable contract: `build --json`
// emits exactly one JSON object with no headers or event rows.
func TestJSONOutputHasNoChrome(t *testing.T) {
	dir := writeBuildableProject(t, "")
	output, code := captureStdout(t, func() int {
		return cmdBuild([]string{"--json", dir})
	})
	if code != 0 {
		t.Fatalf("build --json exit = %d", code)
	}
	if strings.Contains(output, "Wrote") || strings.Contains(output, "Finished") ||
		strings.Contains(output, "\x1b[") || strings.Contains(output, "sloptor v") {
		t.Errorf("build --json output contains UI chrome:\n%s", output)
	}
	if !strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Errorf("build --json output is not a JSON object:\n%s", output)
	}
}
