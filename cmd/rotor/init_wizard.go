package main

// The interactive `rotor init` wizard: line-based prompts (no raw mode, no
// cursor addressing) that assemble an initOptions consumed by the same
// scaffold() as the non-interactive path. The wizard runs only when stdin and
// stdout are both terminals and neither --template nor --yes was passed, so
// CI and pipes always get the scriptable path.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"rotor/internal/term"
)

// runInitInteractive drives the wizard against in/out (os.Stdin/os.Stdout in
// production, scripted buffers in tests) and writes the scaffold on confirm.
func runInitInteractive(dir, name string, in io.Reader, out io.Writer) int {
	opts, ok, err := runInitWizard(in, out, initOptions{dir: dir, name: name, template: "game"})
	if err != nil {
		newUI(os.Stderr).failLine(fmt.Sprintf("rotor init: %v", err))
		return 1
	}
	if !ok {
		u := newUI(out)
		fmt.Fprintln(u.w)
		u.noteLine("aborted, nothing written")
		return 0
	}
	fmt.Fprintln(out)
	return writeInitFiles(out, opts, scaffold(opts))
}

// runInitWizard walks the prompt steps and returns the assembled options and
// whether the user confirmed the summary. An io error (closed stdin) aborts.
func runInitWizard(in io.Reader, out io.Writer, defaults initOptions) (initOptions, bool, error) {
	p := newPrompter(in, out)
	u := newUI(out)
	opts := defaults

	u.banner("init")
	fmt.Fprintf(out, "  %s\n", p.s.Muted("press enter to accept the (default) shown; Ctrl+C to quit"))

	// a. project name → default.project.json name + package.json name.
	name, err := p.askDefault("project name", defaults.name)
	if err != nil {
		return opts, false, err
	}
	opts.name = name

	// b. template.
	templates := []string{"game", "package", "plain"}
	ti, err := p.askChoice("template", []choice{
		{"game", "full rbxts game (DataModel project)"},
		{"package", "rbxts model / npm package"},
		{"plain", "Luau-only (bundle · minify · pack)"},
	}, 0)
	if err != nil {
		return opts, false, err
	}
	opts.template = templates[ti]

	if opts.template != "plain" {
		// c. linter / formatter (rbxts templates only).
		linters := []string{"biome", "oxlint", ""}
		li, err := p.askChoice("linter / formatter", []choice{
			{"biome", "format + lint, tuned for rbxts"},
			{"oxlint", "fast lint only"},
			{"none", "skip"},
		}, 0)
		if err != nil {
			return opts, false, err
		}
		opts.linter = linters[li]

		// d. extra packages.
		pkgChoices := make([]choice, len(extraPackages))
		for i, ep := range extraPackages {
			pkgChoices[i] = choice{ep.label, ep.desc}
		}
		opts.packages, err = p.askMulti("extra packages", pkgChoices)
		if err != nil {
			return opts, false, err
		}

		// e. rotor cloud setup: asset sync + deploy environments.
		if yes, err := p.askYesNo("set up asset sync?", false); err != nil {
			return opts, false, err
		} else if yes {
			adir, err := p.askDefault("assets directory", "assets")
			if err != nil {
				return opts, false, err
			}
			ct, err := p.askChoice("creator", []choice{
				{"user", "assets owned by your account"},
				{"group", "assets owned by a group"},
			}, 0)
			if err != nil {
				return opts, false, err
			}
			cid, err := p.askDigits("creator id", "0")
			if err != nil {
				return opts, false, err
			}
			opts.assets = &assetsOptions{
				dir:         strings.Trim(strings.ReplaceAll(adir, "\\", "/"), "/ "),
				creatorType: []string{"user", "group"}[ct],
				creatorID:   cid,
			}
			if opts.assets.dir == "" {
				opts.assets.dir = "assets"
			}
		}
		if yes, err := p.askYesNo("set up deploy environments?", false); err != nil {
			return opts, false, err
		} else if yes {
			env, err := p.askDefault("environment name", "production")
			if err != nil {
				return opts, false, err
			}
			uid, err := p.askDigits("universe id", "0")
			if err != nil {
				return opts, false, err
			}
			pid, err := p.askDigits("place id", "0")
			if err != nil {
				return opts, false, err
			}
			file, err := p.askDefault("place file", "build/game.rbxl")
			if err != nil {
				return opts, false, err
			}
			opts.deploy = &deployOptions{env: env, universeID: uid, placeID: pid, placeFile: file}
		}
	}

	// f. summary + confirm.
	printWizardSummary(out, p.s, opts, scaffold(opts))
	ok, err := p.askYesNo("create?", true)
	if err != nil {
		return opts, false, err
	}
	return opts, ok, nil
}

// printWizardSummary renders the chosen options as an aligned block plus the
// full list of files that would be written.
func printWizardSummary(out io.Writer, s *term.Styler, opts initOptions, files []initFile) {
	rows := [][2]string{
		{"name", opts.name},
		{"template", opts.template},
	}
	if opts.template != "plain" {
		linter := opts.linter
		if linter == "" {
			linter = "none"
		}
		rows = append(rows, [2]string{"linter", linter})
		pkgs := "none"
		if len(opts.packages) > 0 {
			var names []string
			for _, i := range opts.packages {
				for _, d := range extraPackages[i].deps {
					names = append(names, d.name)
				}
			}
			pkgs = strings.Join(names, ", ")
		}
		rows = append(rows, [2]string{"packages", pkgs})
		if opts.assets != nil {
			rows = append(rows, [2]string{"assets",
				fmt.Sprintf("%s/ · creator %s %s", opts.assets.dir, opts.assets.creatorType, opts.assets.creatorID)})
		}
		if opts.deploy != nil {
			rows = append(rows, [2]string{"deploy",
				fmt.Sprintf("%s · universe %s · place %s (%s)", opts.deploy.env, opts.deploy.universeID, opts.deploy.placeID, opts.deploy.placeFile)})
		}
	}

	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}
	fmt.Fprintf(out, "\n  %s\n", s.Bold("summary"))
	for _, r := range rows {
		key := r[0] + strings.Repeat(" ", width-len(r[0]))
		fmt.Fprintf(out, "    %s  %s\n", s.Muted(key), r[1])
	}
	fmt.Fprintf(out, "\n  %s\n", s.Bold(plural(len(files), "file")+" to create"))
	for _, f := range files {
		fmt.Fprintf(out, "    %s %s\n", s.Green("+"), f.path)
	}
}

// --- prompt helpers ---

// choice is one entry of a numbered menu: a name and a muted description.
type choice struct {
	name, desc string
}

// prompter reads line-based answers from in and renders prompts to out in
// house style. All helpers re-prompt on invalid input with a muted hint and
// return an error only when the input stream ends or fails.
type prompter struct {
	sc *bufio.Scanner
	w  io.Writer
	s  *term.Styler
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{sc: bufio.NewScanner(in), w: out, s: term.For(out)}
}

// line reads the next input line, trimmed. io.EOF means the stream ended.
func (p *prompter) line() (string, error) {
	if !p.sc.Scan() {
		if err := p.sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("input closed before the wizard finished")
	}
	return strings.TrimSpace(p.sc.Text()), nil
}

// hint prints a muted re-prompt hint under an invalid answer.
func (p *prompter) hint(msg string) {
	fmt.Fprintf(p.w, "    %s %s\n", p.s.Muted(p.s.Glyphs().Arrow), p.s.Muted(msg))
}

// ask prompts for free text with no default.
func (p *prompter) ask(label string) (string, error) {
	fmt.Fprintf(p.w, "\n  %s%s ", p.s.Bold(label), p.s.Muted(":"))
	return p.line()
}

// askDefault prompts for free text, `label (default):`, empty answer = default.
func (p *prompter) askDefault(label, def string) (string, error) {
	fmt.Fprintf(p.w, "\n  %s %s ", p.s.Bold(label), p.s.Muted("("+def+"):"))
	v, err := p.line()
	if err != nil {
		return "", err
	}
	if v == "" {
		return def, nil
	}
	return v, nil
}

// askDigits prompts like askDefault but accepts only a decimal number.
func (p *prompter) askDigits(label, def string) (string, error) {
	for {
		v, err := p.askDefault(label, def)
		if err != nil {
			return "", err
		}
		if isDigits(v) {
			return v, nil
		}
		p.hint("enter a number (digits only)")
	}
}

// askChoice renders a numbered menu and returns the chosen index.
func (p *prompter) askChoice(label string, options []choice, def int) (int, error) {
	fmt.Fprintf(p.w, "\n  %s\n", p.s.Bold(label))
	for i, o := range options {
		desc := o.desc
		if i == def {
			if desc != "" {
				desc += " "
			}
			desc += "(default)"
		}
		fmt.Fprintf(p.w, "    %s %-9s %s\n", p.s.Accent(fmt.Sprintf("%d.", i+1)), o.name, p.s.Muted(desc))
	}
	for {
		fmt.Fprintf(p.w, "  choose %s ", p.s.Muted(fmt.Sprintf("[%d]:", def+1)))
		v, err := p.line()
		if err != nil {
			return 0, err
		}
		if v == "" {
			return def, nil
		}
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 1 || n > len(options) {
			p.hint(fmt.Sprintf("enter a number from 1 to %d", len(options)))
			continue
		}
		return n - 1, nil
	}
}

// askMulti renders a numbered menu and returns the selected indices, parsed
// from a comma-separated answer like "1,3". Empty answer selects nothing.
func (p *prompter) askMulti(label string, options []choice) ([]int, error) {
	fmt.Fprintf(p.w, "\n  %s %s\n", p.s.Bold(label), p.s.Muted("(comma-separated, e.g. 1,3 — empty for none)"))
	for i, o := range options {
		fmt.Fprintf(p.w, "    %s %-36s %s\n", p.s.Accent(fmt.Sprintf("%d.", i+1)), o.name, p.s.Muted(o.desc))
	}
	for {
		fmt.Fprintf(p.w, "  select %s ", p.s.Muted("[none]:"))
		v, err := p.line()
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, nil
		}
		var picked []int
		valid := true
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, convErr := strconv.Atoi(part)
			if convErr != nil || n < 1 || n > len(options) {
				valid = false
				break
			}
			if !slices.Contains(picked, n-1) {
				picked = append(picked, n-1)
			}
		}
		if !valid {
			p.hint(fmt.Sprintf("enter numbers from 1 to %d, separated by commas", len(options)))
			continue
		}
		slices.Sort(picked)
		return picked, nil
	}
}

// askYesNo prompts `label [Y/n]` (or [y/N]) and returns the answer.
func (p *prompter) askYesNo(label string, def bool) (bool, error) {
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	for {
		fmt.Fprintf(p.w, "\n  %s %s ", p.s.Bold(label), p.s.Muted(suffix))
		v, err := p.line()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(v) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		p.hint("answer y or n")
	}
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
