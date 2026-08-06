package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/bundle"
	"rotor/internal/diagframe"
	"rotor/internal/luau/cst"
	"rotor/internal/term"
)

// newBundleCommand bundles a Luau require graph rooted at an entry file into
// one runnable file. Output goes to --output, or stdout when no output path
// is given. With --minify the bundle is also minified. --exclude <glob>
// (repeatable) leaves requires whose resolved path matches a glob verbatim
// (for runtime-provided modules); .json/.txt/.md requires are embedded as
// data modules; "@alias" requires resolve through the nearest .luaurc.
//
// Output discipline: without -o the bundle itself is the stdout stream, so no
// chrome is printed there; with -o the rotor banner + event rows appear on
// stdout.
func newBundleCommand(streams cliStreams) *cobra.Command {
	var output string
	minify := false
	var exclude []string
	cmd := &cobra.Command{
		Use:                   "bundle <entry> [-o out] [--minify]",
		Short:                 "inline a Luau require graph into one runnable file",
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runBundleCommand(streams, argv[0], output, minify, exclude)
		},
	}
	cmd.Flags().SortFlags = false
	addStringFlag(cmd, &output, "output", "o", "", "<file>",
		"write the bundle to this file instead of stdout")
	addBoolFlag(cmd, &minify, "minify", "", false, "minify the bundled output")
	cmd.Flags().StringSliceVar(&exclude, "exclude", nil,
		"leave requires whose resolved path matches a glob verbatim (repeatable)")
	setFlagPlaceholder(cmd, "exclude", "<glob>")
	return cmd
}

func runBundleCommand(streams cliStreams, entry, output string, minify bool, exclude []string) error {
	if output != "" {
		newUI(streams.out).banner("bundle  " + filepath.Base(entry))
	}

	start := time.Now()
	out, err := bundle.BundleWith(entry, bundle.Options{Exclude: exclude})
	if err != nil {
		var pe *bundle.ParseError
		if errors.As(err, &pe) {
			u := newUI(streams.err)
			u.events([]uiEvent{{Status: eventFailed, Target: entry, Detail: "syntax error"}})
			color := term.ColorEnabled(streams.err)
			fmt.Fprint(streams.err, diagframe.RenderGroups(
				[]diagframe.Group{{Path: pe.Path, Source: pe.Source, Lang: diagframe.Luau,
					Spots: []diagframe.Spot{{Offset: pe.Diag.Pos.Offset, Len: 1, Severity: diagframe.Error, Message: pe.Diag.Message}}}},
				diagframe.Options{Color: color, Link: color}, 0))
			return reportedFailure(err)
		}
		return runtimeFailure(err)
	}
	// Display-only module tally: one `local function impl_<id>(` per bundled
	// module in the assembled output (counted before minification).
	modules := strings.Count(out, "local function impl_")

	rawSize := len(out)
	if minify {
		minified, diags := cst.Minify(out)
		if len(diags) != 0 {
			return runtimeFailure(fmt.Errorf("internal error minifying bundle: %s", diags[0].Message))
		}
		out = minified
	}

	if output == "" {
		_, _ = io.WriteString(streams.out, out)
		return nil
	}
	if err := os.WriteFile(output, []byte(out), 0o644); err != nil {
		return runtimeFailure(fmt.Errorf("cannot write %q: %w", output, err))
	}

	detail := fmt.Sprintf("%d modules · %s", modules, formatBytes(len(out)))
	if minify {
		detail += fmt.Sprintf(" (minified, %s)", shrinkPercent(rawSize, len(out)))
	}
	newUI(streams.out).events([]uiEvent{
		{Status: eventWrote, Target: output, Detail: detail},
		{Status: eventFinished, Elapsed: time.Since(start)},
	})
	fmt.Fprintln(streams.out)
	return nil
}
