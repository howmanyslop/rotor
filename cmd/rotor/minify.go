package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rotor/internal/diagframe"
	"rotor/internal/luau/cst"
	"rotor/internal/term"
)

// cmdMinify minifies a single Luau file: it strips comments (except leading `--!`
// directives) and superfluous whitespace, preserving program semantics. Output goes
// to --output, or to stdout when no output path is given.
//
// Output discipline: when the artifact goes to stdout (no -o), NO chrome is
// written to stdout — errors go to stderr and the pipe stays clean. With -o,
// the rotor banner + summary are printed to stdout like build/check.
func cmdMinify(args []string) int {
	input := ""
	output := ""
	indexToField := true // rotor DX: collapse t["foo"] -> t.foo (opt out with --no-index-field)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			usage(os.Stdout)
			return 0
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "sloptor minify: %s requires a path\n", a)
				return 1
			}
			i++
			output = args[i]
		case strings.HasPrefix(a, "--output="):
			output = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o="):
			output = strings.TrimPrefix(a, "-o=")
		case a == "--no-index-field":
			indexToField = false
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "sloptor minify: unknown flag %q\n\n", a)
			usage(os.Stderr)
			return 1
		default:
			if input != "" {
				fmt.Fprintf(os.Stderr, "sloptor minify: unexpected extra argument %q\n\n", a)
				usage(os.Stderr)
				return 1
			}
			input = a
		}
	}
	if input == "" {
		fmt.Fprintln(os.Stderr, "sloptor minify: an input .luau/.lua file is required")
		usage(os.Stderr)
		return 1
	}

	errUI := newUI(os.Stderr)
	if output != "" {
		newUI(os.Stdout).banner("minify  " + filepath.Base(input))
	}

	start := time.Now()
	src, err := os.ReadFile(input)
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor minify: cannot read %q: %v", input, err))
		return 1
	}

	minified, diags := cst.MinifyWith(string(src), cst.MinifyOptions{ConvertIndexToField: indexToField})
	if len(diags) != 0 {
		errUI.failLine(fmt.Sprintf("sloptor minify: %s has %s", input, plural(len(diags), "syntax error")))
		spots := make([]diagframe.Spot, len(diags))
		for i, d := range diags {
			spots[i] = diagframe.Spot{Offset: d.Pos.Offset, Len: 1, Severity: diagframe.Error, Message: d.Message}
		}
		color := term.ColorEnabled(os.Stderr)
		fmt.Fprint(os.Stderr, diagframe.RenderGroups(
			[]diagframe.Group{{Path: input, Source: string(src), Lang: diagframe.Luau, Spots: spots}},
			diagframe.Options{Color: color, Link: color},
			0,
		))
		return 1
	}

	if output == "" {
		_, _ = os.Stdout.WriteString(minified)
		return 0
	}
	if err := os.WriteFile(output, []byte(minified), 0o644); err != nil {
		errUI.failLine(fmt.Sprintf("sloptor minify: cannot write %q: %v", output, err))
		return 1
	}

	out := newUI(os.Stdout)
	out.okLine("minified "+filepath.Base(input), fmt.Sprintf("in %d ms", time.Since(start).Milliseconds()))
	out.noteLine(fmt.Sprintf("%s  %s %s %s (%s)", output,
		formatBytes(len(src)), out.s.Glyphs().Arrow, formatBytes(len(minified)), shrinkPercent(len(src), len(minified))))
	fmt.Println()
	return 0
}

// shrinkPercent renders the size delta of a minify/bundle pass ("43% smaller").
func shrinkPercent(before, after int) string {
	if before <= 0 || after >= before {
		return "no smaller"
	}
	return fmt.Sprintf("%d%% smaller", (before-after)*100/before)
}
