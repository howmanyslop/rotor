package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rotor/internal/bundle"
	"rotor/internal/diagframe"
	"rotor/internal/luau/cst"
	"rotor/internal/term"
)

// cmdBundle bundles a Luau require graph rooted at an entry file into one runnable
// file. Output goes to --output, or stdout when no output path is given. With
// --minify the bundle is also minified. --exclude <glob> (repeatable) leaves
// requires whose resolved path matches a glob verbatim (for runtime-provided
// modules); .json/.txt/.md requires are embedded as data modules; "@alias"
// requires resolve through the nearest .luaurc.
//
// Output discipline: without -o the bundle itself is the stdout stream, so no
// chrome is printed there; with -o the rotor banner + summary appear on stdout.
func cmdBundle(args []string) int {
	entry := ""
	output := ""
	minify := false
	var exclude []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			usage(os.Stdout)
			return 0
		case a == "--minify":
			minify = true
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "sloptor bundle: %s requires a path\n", a)
				return 1
			}
			i++
			output = args[i]
		case strings.HasPrefix(a, "--output="):
			output = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o="):
			output = strings.TrimPrefix(a, "-o=")
		case a == "--exclude":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "sloptor bundle: %s requires a glob\n", a)
				return 1
			}
			i++
			exclude = append(exclude, args[i])
		case strings.HasPrefix(a, "--exclude="):
			exclude = append(exclude, strings.TrimPrefix(a, "--exclude="))
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "sloptor bundle: unknown flag %q\n\n", a)
			usage(os.Stderr)
			return 1
		default:
			if entry != "" {
				fmt.Fprintf(os.Stderr, "sloptor bundle: unexpected extra argument %q\n\n", a)
				usage(os.Stderr)
				return 1
			}
			entry = a
		}
	}
	if entry == "" {
		fmt.Fprintln(os.Stderr, "sloptor bundle: an entry .luau/.lua file is required")
		usage(os.Stderr)
		return 1
	}

	errUI := newUI(os.Stderr)
	if output != "" {
		newUI(os.Stdout).banner("bundle  " + filepath.Base(entry))
	}

	start := time.Now()
	out, err := bundle.BundleWith(entry, bundle.Options{Exclude: exclude})
	if err != nil {
		var pe *bundle.ParseError
		if errors.As(err, &pe) {
			errUI.failLine("sloptor bundle: syntax error")
			color := term.ColorEnabled(os.Stderr)
			fmt.Fprint(os.Stderr, diagframe.RenderGroups(
				[]diagframe.Group{{Path: pe.Path, Source: pe.Source, Lang: diagframe.Luau,
					Spots: []diagframe.Spot{{Offset: pe.Diag.Pos.Offset, Len: 1, Severity: diagframe.Error, Message: pe.Diag.Message}}}},
				diagframe.Options{Color: color, Link: color}, 0))
			return 1
		}
		errUI.failLine(fmt.Sprintf("sloptor bundle: %v", err))
		return 1
	}
	// Display-only module tally: one `local function impl_<id>(` per bundled
	// module in the assembled output (counted before minification).
	modules := strings.Count(out, "local function impl_")

	rawSize := len(out)
	if minify {
		minified, diags := cst.Minify(out)
		if len(diags) != 0 {
			errUI.failLine(fmt.Sprintf("sloptor bundle: internal error minifying bundle: %s", diags[0].Message))
			return 1
		}
		out = minified
	}

	if output == "" {
		_, _ = os.Stdout.WriteString(out)
		return 0
	}
	if err := os.WriteFile(output, []byte(out), 0o644); err != nil {
		errUI.failLine(fmt.Sprintf("sloptor bundle: cannot write %q: %v", output, err))
		return 1
	}

	u := newUI(os.Stdout)
	u.okLine(fmt.Sprintf("bundled %s", plural(modules, "module")),
		fmt.Sprintf("in %d ms", time.Since(start).Milliseconds()))
	detail := fmt.Sprintf("%s  %s", output, formatBytes(len(out)))
	if minify {
		detail += fmt.Sprintf(" (minified, %s)", shrinkPercent(rawSize, len(out)))
	}
	u.noteLine(detail)
	fmt.Println()
	return 0
}
