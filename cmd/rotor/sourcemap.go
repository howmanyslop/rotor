package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/sourcemap"
)

// newSourcemapCommand emits a Rojo-compatible sourcemap.json for the project —
// the format `rojo sourcemap --include-non-scripts` produces, which luau-lsp
// consumes. The tree is built natively (no rojo) for plain script trees;
// projects using features outside that subset fall back to `rojo sourcemap`
// when rojo is on PATH. File paths are project-relative with forward slashes.
// Output goes to --output, or to stdout when no output path is given.
//
// Output discipline: without -o the sourcemap JSON IS the stdout stream
// (piped into luau-lsp and tests), so no chrome touches stdout; with -o the
// rotor banner + event rows appear on stdout.
func newSourcemapCommand(streams cliStreams) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:                   "sourcemap [path] [-o out.json]",
		Short:                 "emit a Rojo-compatible sourcemap.json for luau-lsp",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			project := ""
			if len(argv) > 0 {
				project = argv[0]
			}
			return runSourcemapCommand(streams, project, output)
		},
	}
	cmd.Flags().SortFlags = false
	addStringFlag(cmd, &output, "output", "o", "", "<file>",
		"write the sourcemap to this file instead of stdout")
	return cmd
}

func runSourcemapCommand(streams cliStreams, project, output string) error {
	if output != "" {
		newUI(streams.out).banner("sourcemap")
	}

	start := time.Now()
	data, err := sourcemap.Generate(project)
	if err != nil {
		return runtimeFailure(err)
	}
	if output == "" {
		_, _ = streams.out.Write(data)
		return nil
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return runtimeFailure(fmt.Errorf("cannot write %q: %w", output, err))
	}

	newUI(streams.out).events([]uiEvent{
		{Status: eventWrote, Target: output, Detail: formatBytes(len(data))},
		{Status: eventFinished, Elapsed: time.Since(start)},
	})
	fmt.Fprintln(streams.out)
	return nil
}
