package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/pack"
)

// newPackCommand packages a Rojo project into a distributable artifact: a
// self-reconstructing Luau script (--as luau, default), or a Roblox model
// file (--as rbxmx / --as rbxm, built via `rojo build`). The Luau form
// rebuilds the instance tree + a require polyfill at runtime, so it runs
// without Rojo.
//
// Output discipline: without -o the packed artifact IS the stdout stream
// (luau format only), so chrome is omitted; with -o the rotor banner + event
// rows appear on stdout.
func newPackCommand(streams cliStreams) *cobra.Command {
	var output, format, entry string
	rojoTree := false
	cmd := &cobra.Command{
		Use:                   "pack [path] [--as luau|rbxmx|rbxm] [-o out] [--entry inst.path] [--rojo-tree]",
		Short:                 "package a Rojo project into one self-reconstructing Luau script",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			project := ""
			if len(argv) > 0 {
				project = argv[0]
			}
			return runPackCommand(streams, project, output, format, entry, rojoTree)
		},
	}
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &rojoTree, "rojo-tree", "", false, "force building the tree via rojo")
	addStringFlag(cmd, &format, "as", "", "luau", "<format>", "output format: luau, rbxmx, or rbxm")
	addStringFlag(cmd, &output, "output", "o", "", "<file>", "write the packed artifact to this file")
	addStringFlag(cmd, &entry, "entry", "", "", "<path>", "instance path to enter (luau format only)")
	return cmd
}

func runPackCommand(streams cliStreams, project, output, format, entry string, rojoTree bool) error {
	var f pack.Format
	switch format {
	case "luau":
		f = pack.FormatLuau
	case "rbxmx":
		f = pack.FormatRbxmx
	case "rbxm":
		f = pack.FormatRbxm
	default:
		return usageFailure("unknown format %q (want luau, rbxmx, or rbxm)", format)
	}
	if entry != "" && f != pack.FormatLuau {
		return usageFailure("--entry only applies to --as luau")
	}
	if output == "" && f != pack.FormatLuau {
		return usageFailure("--as %s needs an output path (-o <file.%s>)", format, format)
	}

	if output != "" {
		newUI(streams.out).banner("pack  " + format)
	}

	start := time.Now()
	data, err := pack.Pack(pack.Options{Project: project, Format: f, Entry: entry, RojoTree: rojoTree})
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
