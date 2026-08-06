package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/diagframe"
	"rotor/internal/luau/cst"
	"rotor/internal/term"
)

// newMinifyCommand minifies a single Luau file: it strips comments (except
// leading `--!` directives) and superfluous whitespace, preserving program
// semantics. Output goes to --output, or to stdout when no output path is
// given.
//
// Output discipline: when the artifact goes to stdout (no -o), NO chrome is
// written to stdout — errors go to stderr and the pipe stays clean. With -o,
// the rotor banner + event rows are printed to stdout like build/check.
func newMinifyCommand(streams cliStreams) *cobra.Command {
	var output string
	indexToField := true // rotor DX: collapse t["foo"] -> t.foo (opt out with --no-index-field)
	cmd := &cobra.Command{
		Use:                   "minify <file> [-o out]",
		Short:                 "minify a Luau file (keeps --! directives)",
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runMinifyCommand(streams, argv[0], output, indexToField)
		},
	}
	cmd.Flags().SortFlags = false
	addStringFlag(cmd, &output, "output", "o", "", "<file>",
		"write the minified output to this file instead of stdout")
	addBoolFlag(cmd, &indexToField, "index-field", "", true,
		"collapse t[\"x\"] into t.x (--no-index-field disables)")
	return cmd
}

func runMinifyCommand(streams cliStreams, input, output string, indexToField bool) error {
	if output != "" {
		newUI(streams.out).banner("minify  " + filepath.Base(input))
	}

	start := time.Now()
	src, err := os.ReadFile(input)
	if err != nil {
		return runtimeFailure(fmt.Errorf("cannot read %q: %w", input, err))
	}

	minified, diags := cst.MinifyWith(string(src), cst.MinifyOptions{ConvertIndexToField: indexToField})
	if len(diags) != 0 {
		u := newUI(streams.err)
		u.events([]uiEvent{{
			Status: eventFailed,
			Target: input,
			Detail: plural(len(diags), "syntax error"),
		}})
		spots := make([]diagframe.Spot, len(diags))
		for i, d := range diags {
			spots[i] = diagframe.Spot{Offset: d.Pos.Offset, Len: 1, Severity: diagframe.Error, Message: d.Message}
		}
		color := term.ColorEnabled(streams.err)
		fmt.Fprint(streams.err, diagframe.RenderGroups(
			[]diagframe.Group{{Path: input, Source: string(src), Lang: diagframe.Luau, Spots: spots}},
			diagframe.Options{Color: color, Link: color},
			0,
		))
		return reportedFailure(errors.New("minify failed"))
	}

	if output == "" {
		_, _ = io.WriteString(streams.out, minified)
		return nil
	}
	if err := os.WriteFile(output, []byte(minified), 0o644); err != nil {
		return runtimeFailure(fmt.Errorf("cannot write %q: %w", output, err))
	}

	newUI(streams.out).events([]uiEvent{
		{
			Status: eventWrote,
			Target: output,
			Detail: fmt.Sprintf("%s → %s (%s)", formatBytes(len(src)), formatBytes(len(minified)), shrinkPercent(len(src), len(minified))),
		},
		{Status: eventFinished, Elapsed: time.Since(start)},
	})
	fmt.Fprintln(streams.out)
	return nil
}

// shrinkPercent renders the size delta of a minify/bundle pass ("43% smaller").
func shrinkPercent(before, after int) string {
	if before <= 0 || after >= before {
		return "no smaller"
	}
	return fmt.Sprintf("%d%% smaller", (before-after)*100/before)
}
