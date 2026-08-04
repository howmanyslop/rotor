package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"rotor/internal/pack"
)

// cmdPack packages a Rojo project into a distributable artifact: a self-
// reconstructing Luau script (--as luau, default), or a Roblox model file
// (--as rbxmx / --as rbxm, built via `rojo build`). The Luau form rebuilds the
// instance tree + a require polyfill at runtime, so it runs without Rojo.
func cmdPack(args []string) int {
	project := ""
	output := ""
	format := "luau"
	entry := ""
	rojoTree := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			usage(os.Stdout)
			return 0
		case a == "--rojo-tree":
			rojoTree = true
		case a == "--as":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "sloptor pack: --as requires a format (luau|rbxmx|rbxm)")
				return 1
			}
			i++
			format = args[i]
		case strings.HasPrefix(a, "--as="):
			format = strings.TrimPrefix(a, "--as=")
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "sloptor pack: %s requires a path\n", a)
				return 1
			}
			i++
			output = args[i]
		case strings.HasPrefix(a, "--output="):
			output = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o="):
			output = strings.TrimPrefix(a, "-o=")
		case a == "--entry":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "sloptor pack: --entry requires an instance path")
				return 1
			}
			i++
			entry = args[i]
		case strings.HasPrefix(a, "--entry="):
			entry = strings.TrimPrefix(a, "--entry=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "sloptor pack: unknown flag %q\n\n", a)
			usage(os.Stderr)
			return 1
		default:
			if project != "" {
				fmt.Fprintf(os.Stderr, "sloptor pack: unexpected extra argument %q\n\n", a)
				usage(os.Stderr)
				return 1
			}
			project = a
		}
	}

	var f pack.Format
	switch format {
	case "luau":
		f = pack.FormatLuau
	case "rbxmx":
		f = pack.FormatRbxmx
	case "rbxm":
		f = pack.FormatRbxm
	default:
		fmt.Fprintf(os.Stderr, "sloptor pack: unknown format %q (want luau, rbxmx, or rbxm)\n", format)
		return 1
	}
	if entry != "" && f != pack.FormatLuau {
		fmt.Fprintln(os.Stderr, "sloptor pack: --entry only applies to --as luau")
		return 1
	}
	if output == "" && f != pack.FormatLuau {
		fmt.Fprintf(os.Stderr, "sloptor pack: --as %s needs an output path (-o <file.%s>)\n", format, format)
		return 1
	}

	// Output discipline: without -o the packed artifact IS the stdout stream
	// (luau format only), so chrome is omitted; with -o the rotor banner +
	// summary appear on stdout.
	errUI := newUI(os.Stderr)
	if output != "" {
		newUI(os.Stdout).banner("pack  " + format)
	}

	start := time.Now()
	data, err := pack.Pack(pack.Options{Project: project, Format: f, Entry: entry, RojoTree: rojoTree})
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor pack: %v", err))
		return 1
	}

	if output == "" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		errUI.failLine(fmt.Sprintf("sloptor pack: cannot write %q: %v", output, err))
		return 1
	}

	u := newUI(os.Stdout)
	u.okLine("packed project as "+format, fmt.Sprintf("in %d ms", time.Since(start).Milliseconds()))
	u.noteLine(fmt.Sprintf("%s  %s", output, formatBytes(len(data))))
	fmt.Println()
	return 0
}
