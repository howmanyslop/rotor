package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"rotor/internal/term"
)

// cliStreams carries the three process streams through the command tree.
// Production passes os.Stdin/os.Stdout/os.Stderr; tests inject buffers, which
// also pins the NO_COLOR-sensitive plain rendering.
type cliStreams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

// commandFailure is the single error type command RunE bodies return. It is
// not an error message by itself: `cause` is the text, `usage` selects the
// dim "Run .sloptor <command> --help' for usage." hint, `render` preserves rich
// diagnostic output (buildFailure code frames, Luau frames), and `reported`
// marks failures the command's own diagnostic writer already rendered so
// execute does not print a duplicate generic error line.
type commandFailure struct {
	cause    error
	usage    bool
	reported bool
	render   func(cliStreams)
}

func (f *commandFailure) Error() string {
	if f.cause != nil {
		return f.cause.Error()
	}
	return "command failed"
}

// usageFailure is a parse/argument-level failure: red `error:` line plus the
// dim help hint.
func usageFailure(format string, args ...any) *commandFailure {
	return &commandFailure{cause: fmt.Errorf(format, args...), usage: true}
}

// runtimeFailure is an operational failure: red `error:` line, no help hint.
func runtimeFailure(err error) *commandFailure {
	return &commandFailure{cause: err}
}

// reportedFailure marks a failure whose diagnostics were already rendered by
// the command (ui.buildFailure, census writers); execute prints nothing extra.
func reportedFailure(err error) *commandFailure {
	return &commandFailure{cause: err, reported: true}
}

// run is the process entry: apply runtime policy, then execute against the
// real process streams. main stays `os.Exit(run(os.Args[1:]))`.
func run(args []string) int {
	applyRuntimePolicy()
	return execute(args, cliStreams{in: os.Stdin, out: os.Stdout, err: os.Stderr})
}

// execute is the single exit-code owner. Every failure — Cobra parse errors,
// Args validators, and commandFailure values from RunE — maps to exit 1,
// matching the documented rbxtsc-compatible policy. Success is 0.
func execute(args []string, streams cliStreams) int {
	root := newRootCommand(streams)
	resolved := resolveCommandChain(root, args)
	args = normalizeLegacyArgs(resolved, args)
	root.SetArgs(args)
	root.SetIn(streams.in)
	root.SetOut(streams.out)
	root.SetErr(streams.err)
	if err := root.Execute(); err != nil {
		var cf *commandFailure
		if errors.As(err, &cf) {
			if cf.render != nil {
				cf.render(streams)
			}
			if cf.reported {
				return 1
			}
			renderError(streams, resolved.CommandPath(), cf.cause, cf.usage)
			return 1
		}
		// Cobra's own failures (unknown flag/command, bad argument counts,
		// validator errors) are usage errors: red error + help hint.
		renderError(streams, resolved.CommandPath(), err, true)
		return 1
	}
	return 0
}

// renderError prints the compact Cargo/Bun-style failure: one red `error:`
// prefix with the unstyled message, and — for usage failures only — a dim
// hint naming the resolved command's help.
func renderError(streams cliStreams, commandPath string, cause error, usage bool) {
	s := term.For(streams.err)
	fmt.Fprintf(streams.err, "%s %s\n", s.Error("error:"), cause)
	if usage {
		hint := "sloptor --help"
		if commandPath != "" && commandPath != "rotor" {
			hint = commandPath + " --help"
		}
		fmt.Fprintf(streams.err, "%s\n", s.Muted("Run '"+hint+"' for usage."))
	}
}

// newRootCommand builds the Cobra tree. Children are registered in the
// documented order; each flag set keeps registration order (SortFlags=false)
// so help renders the surface the way it is documented, not alphabetically.
func newRootCommand(streams cliStreams) *cobra.Command {
	root := &cobra.Command{
		Use:                   "sloptor <command> [flags]",
		Short:                 "an all-in-one Roblox toolchain (rbxtsc-parity compiler, Luau tools, assets, deploy)",
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if v, _ := cmd.Flags().GetBool("version"); v {
				fmt.Fprintln(cmd.OutOrStdout(), version)
				return nil
			}
			return usageFailure("no command given")
		},
	}
	root.SetHelpFunc(helpRenderer)
	root.SetUsageFunc(usageErrorRenderer)
	root.Flags().SortFlags = false
	root.Flags().BoolP("version", "v", false, "print sloptor's version")
	// rotor/notes is a presence marker: the root help renders its structured
	// Environment / Exit-codes sections (renderRootNotes) instead of a raw dump.
	root.Annotations = map[string]string{"rotor/notes": "root"}

	root.AddCommand(
		newAddCommand(streams),
		newAssetCommand(streams),
		newBuildCommand(streams),
		newBundleCommand(streams),
		newCheckCommand(streams),
		newCleanCommand(streams),
		newDeployCommand(streams),
		newDevCommand(streams),
		newDiagnosticsCommand(streams),
		newDoctorCommand(streams),
		newInitCommand(streams),
		newMigrateCommand(streams),
		newMinifyCommand(streams),
		newPackCommand(streams),
		newSchemaCommand(streams),
		newSourcemapCommand(streams),
		newCompletionCommand(),
		newVersionCommand(),
	)
	return root
}

// newVersionCommand is the explicit `sloptor version` subcommand: the same bare
// version string the root -v/--version flag prints.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print sloptor's version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}

// --- legacy argv normalization ---

// resolveCommandChain walks args to the deepest command they name, so the
// legacy normalizer and error hints can target the right flag set. Only
// asset/deploy have children, and their subcommand is always the first
// non-flag token, so the walk is unambiguous; tokens after `--` are never
// commands.
func resolveCommandChain(root *cobra.Command, args []string) *cobra.Command {
	cmd := root
	start := 0
	for {
		child, next := findSubcommandToken(cmd, args, start)
		if child == nil {
			return cmd
		}
		cmd = child
		start = next
	}
}

func findSubcommandToken(cmd *cobra.Command, args []string, start int) (*cobra.Command, int) {
	for i := start; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return nil, 0
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		for _, child := range cmd.Commands() {
			if child.Name() == a {
				return child, i + 1
			}
		}
		return nil, 0
	}
	return nil, 0
}

// normalizeLegacyArgs rewrites the yargs-17 spellings the rest of the surface
// documents into pflag-native `--<flag>=<value>` tokens, leaving everything
// else (paths, positionals, `--`, unknown `--no-*` flags) untouched so Cobra
// rejects them with its own errors. It runs before Cobra parses, against the
// flags of the resolved command chain.
//
// Three families are normalized:
//   - `--no-<boolean>` negation → `--<boolean>=false` (or the inverted value),
//     which pflag cannot express;
//   - the bare optional-value forms of --build/--rojo/--max-errors → the
//     present-and-empty form (`--build=`), since pflag's `NoOptDefVal` cannot
//     be an empty string;
//   - bare `--project`/`--includePath`/`--type`/profile-path flags followed by
//     a flag-like token (or nothing) → `--<flag>=`. pflag consumes the next
//     token unconditionally as a string value, which would silently swallow a
//     following flag (e.g. `--project --json` setting the project to
//     "--json"); the yargs parsers this surface ports rejected that.
func normalizeLegacyArgs(cmd *cobra.Command, args []string) []string {
	bools := map[string]bool{}
	optional := map[string]string{}
	guarded := map[string]bool{}
	// takeValueSemantics names the string flags whose yargs parsers refused to
	// consume a flag-like next token as a value (they returned "" instead).
	// pflag consumes the next token unconditionally, so these need the
	// explicit `--flag=` rewrite; every other string flag (output paths,
	// templates, ...) keeps pflag's consume-next behavior, which matches its
	// old parser.
	takeValueSemantics := []string{
		"project", "includePath", "type",
		"cpuprofile", "trace-out", "blockprofile", "mutexprofile", "heapprofile", "timings",
	}
	for c := cmd; c != nil; c = c.Parent() {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Value.Type() == "bool" {
				bools[f.Name] = true
			}
		})
		for _, name := range []string{"build", "rojo", "max-errors"} {
			if c.Flags().Lookup(name) != nil {
				if name == "max-errors" {
					optional[name] = "0" // bare --max-errors meant 0 (unlimited)
				} else {
					optional[name] = "" // bare --build / --rojo meant present-and-empty
				}
			}
		}
		for _, name := range takeValueSemantics {
			if c.Flags().Lookup(name) != nil {
				guarded[name] = true
			}
		}
	}
	out := make([]string, 0, len(args))
	for i := range args {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			out = append(out, a)
			continue
		}
		if strings.HasPrefix(a, "--") {
			name := a[2:]
			value, hasValue := "", false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				value, name = name[eq+1:], name[:eq]
				hasValue = true
			}
			if rest, ok := strings.CutPrefix(name, "no-"); ok && bools[rest] {
				if hasValue {
					out = append(out, "--"+rest+"="+invertBool(value))
				} else {
					out = append(out, "--"+rest+"=false")
				}
				continue
			}
			next := ""
			if i+1 < len(args) {
				next = args[i+1]
			}
			if !hasValue && (next == "" || strings.HasPrefix(next, "-")) {
				if def, ok := optional[name]; ok {
					out = append(out, "--"+name+"="+def)
					continue
				}
				if guarded[name] {
					out = append(out, "--"+name+"=")
					continue
				}
			}
			out = append(out, a)
			continue
		}
		// Short flags: only the bare -b form of --build needs help; pflag
		// consumes a following value itself, and `-b=<v>` is left alone.
		if a == "-b" {
			next := ""
			if i+1 < len(args) {
				next = args[i+1]
			}
			if next == "" || strings.HasPrefix(next, "-") {
				out = append(out, "--build=")
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// invertBool flips an explicit boolean value for `--no-flag=<v>` negation;
// anything unrecognized passes through so pflag reports the invalid value.
func invertBool(v string) string {
	switch v {
	case "true", "1":
		return "false"
	case "false", "0":
		return "true"
	}
	return v
}

// --- help rendering ---

// helpRenderer is the Cargo/Bun-style help writer installed on every command:
// a concise identity line, green section headings (Usage / Commands / Options
// / Examples, plus the root's Environment and Exit codes notes), cyan
// command/flag syntax aligned by visible width, unstyled descriptions. No
// stock Cobra template output is ever emitted.
func helpRenderer(cmd *cobra.Command, _ []string) {
	w := cmd.OutOrStdout()
	s := term.For(w)
	var b strings.Builder

	name := cmd.Name()
	if name == "" {
		name = "rotor"
	}
	fmt.Fprintf(&b, "  %s %s\n\n", s.Info(s.Bold(name)), "— "+cmd.Short)

	fmt.Fprintf(&b, "  %s %s\n\n", s.Green("Usage:"), s.Cyan(cmd.UseLine()))

	if children := visibleCommands(cmd); len(children) > 0 {
		if cmd.Parent() == nil {
			renderRootCommandGroups(&b, s, children)
		} else {
			renderCommandList(&b, s, children)
		}
	}

	// Cobra auto-adds the -h/--help flag with the stock "help for <cmd>"
	// description; rewrite it to the house wording so no stock text leaks
	// into the rendered surface.
	if f := cmd.Flags().Lookup("help"); f != nil && f.Usage == "help for "+name {
		f.Usage = "show this help"
	}

	if flags := visibleFlags(cmd); len(flags) > 0 {
		fmt.Fprintf(&b, "  %s\n", s.Green("Options:"))
		width := 0
		lefts := make([]string, len(flags))
		for i, f := range flags {
			lefts[i] = flagSyntax(s, cmd, f)
			if w := term.VisibleLen(lefts[i]); w > width {
				width = w
			}
		}
		for i, f := range flags {
			fmt.Fprintf(&b, "    %s  %s\n", padVisible(lefts[i], width), f.Usage)
		}
		b.WriteString("\n")
	}

	if cmd.Example != "" {
		fmt.Fprintf(&b, "  %s\n", s.Green("Examples:"))
		for _, line := range strings.Split(strings.TrimRight(cmd.Example, "\n"), "\n") {
			command, rest, _ := strings.Cut(line, " ")
			command = s.Cyan(command)
			if rest != "" {
				command += " " + rest
			}
			fmt.Fprintf(&b, "    %s\n", command)
		}
		b.WriteString("\n")
	}

	if cmd.Annotations["rotor/notes"] != "" {
		renderRootNotes(&b, s)
	}

	_, _ = io.WriteString(w, b.String())
}

// rootCommandGroups lists the root help's command sections in display order.
// Each group names its commands in display order (marquee commands first, not
// alphabetical); registered commands not claimed by any group fall through to
// the last group so nothing ever vanishes from help.
var rootCommandGroups = []struct {
	heading string
	note    string // dim trailing note on the heading, e.g. "needs ROBLOX_API_KEY"
	names   []string
}{
	{"Compile & check", "", []string{"build", "check", "diagnostics", "dev", "bundle", "pack", "minify"}},
	{"Project & deps", "", []string{"init", "add", "doctor", "migrate", "clean", "sourcemap", "schema"}},
	{"Cloud", "needs ROBLOX_API_KEY", []string{"asset", "deploy"}},
	{"Other", "", []string{"completion", "version"}},
}

// renderRootCommandGroups renders the root help's command list as headed
// sections (Compile & check / Project & deps / Cloud / Other) instead of one
// flat list, sharing a single name column so every group aligns identically.
func renderRootCommandGroups(b *strings.Builder, s *term.Styler, children []*cobra.Command) {
	byName := make(map[string]*cobra.Command, len(children))
	width := 0
	for _, child := range children {
		byName[child.Name()] = child
		if w := term.VisibleLen(child.Name()); w > width {
			width = w
		}
	}
	seen := make(map[string]bool, len(children))
	for gi, group := range rootCommandGroups {
		var rows []*cobra.Command
		for _, name := range group.names {
			if child, ok := byName[name]; ok {
				rows = append(rows, child)
				seen[name] = true
			}
		}
		if gi == len(rootCommandGroups)-1 {
			for _, child := range children {
				if !seen[child.Name()] {
					rows = append(rows, child)
					seen[child.Name()] = true
				}
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(b, "  %s", s.Bold(group.heading))
		if group.note != "" {
			fmt.Fprintf(b, " %s", s.Muted("("+group.note+")"))
		}
		b.WriteString("\n")
		for _, child := range rows {
			fmt.Fprintf(b, "    %s  %s\n", padVisible(s.Cyan(child.Name()), width), child.Short)
		}
		b.WriteString("\n")
	}
}

// renderCommandList renders a non-root command's subcommands as one flat,
// aligned list under a Commands: heading.
func renderCommandList(b *strings.Builder, s *term.Styler, children []*cobra.Command) {
	fmt.Fprintf(b, "  %s\n", s.Green("Commands:"))
	width := 0
	for _, child := range children {
		if w := term.VisibleLen(child.Name()); w > width {
			width = w
		}
	}
	for _, child := range children {
		fmt.Fprintf(b, "    %s  %s\n", padVisible(s.Cyan(child.Name()), width), child.Short)
	}
	b.WriteString("\n")
}

// helpEnvRows is the root help Environment table: variable name, description,
// and an optional dim default annotation ("" = none). Rendered by
// renderRootNotes as three aligned columns.
var helpEnvRows = []struct{ name, desc, def string }{
	{"GOGC", "Go GC target percentage", "default: 400"},
	{"GOMEMLIMIT", "Go memory limit", "default: 75% of effective memory"},
	{"RBXTSC_WRITE_CONCURRENCY", "output-write worker override", "max: 256"},
	{"ROTOR_WRITE_WORKERS", "rotor-specific output-write worker override", ""},
	{"UV_THREADPOOL_SIZE", "Node sidecar libuv pool size (not Go output writers)", ""},
}

// renderRootNotes renders the root help's trailing sections: an aligned
// Environment table (defaults dim), a hierarchical Exit codes block, the Color
// policy line, and the pointer to per-command help.
func renderRootNotes(b *strings.Builder, s *term.Styler) {
	fmt.Fprintf(b, "  %s\n", s.Green("Environment:"))
	nameWidth, descWidth := 0, 0
	for _, row := range helpEnvRows {
		if w := term.VisibleLen(row.name); w > nameWidth {
			nameWidth = w
		}
		if w := term.VisibleLen(row.desc); w > descWidth {
			descWidth = w
		}
	}
	for _, row := range helpEnvRows {
		desc := row.desc
		if row.def != "" {
			desc = padVisible(row.desc, descWidth)
		}
		fmt.Fprintf(b, "    %s  %s", padVisible(row.name, nameWidth), desc)
		if row.def != "" {
			fmt.Fprintf(b, "  %s", s.Muted(row.def))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	fmt.Fprintf(b, "  %s\n", s.Green("Exit codes:"))
	fmt.Fprintf(b, "  %s  %s\n", s.SuccessBold("0"), "success")
	fmt.Fprintf(b, "  %s  %s\n", s.ErrorBold("1"), "any failure (compile, config, usage — rbxtsc parity)")
	fmt.Fprintf(b, "    %s  %s\n", s.Muted("exception:"), "`diagnostics` exits 0 whenever a census was produced.")
	b.WriteString("\n")

	fmt.Fprintf(b, "  %s\n", s.Muted("Color: auto-detected for terminals; NO_COLOR disables, FORCE_COLOR forces."))
	fmt.Fprintf(b, "  %s\n", s.Muted("Run 'sloptor <command> --help' for details."))
	b.WriteString("\n")
}

// visibleCommands lists a command's subcommands for help, hiding Cobra's
// auto-added help command (its role is served by -h/--help) and any hidden
// commands.
func visibleCommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, child := range cmd.Commands() {
		if child.Hidden || child.Name() == "help" {
			continue
		}
		out = append(out, child)
	}
	return out
}

// visibleFlags lists the command's own flags in registration order, hiding
// Cobra's auto help flag is NOT hidden: -h/--help belongs in Options.
func visibleFlags(cmd *cobra.Command) []*pflag.Flag {
	var out []*pflag.Flag
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		out = append(out, f)
	})
	return out
}

// flagSyntax renders the cyan left column of an Options row: shorthands and
// long name, plus the registration placeholder when the flag takes a value.
func flagSyntax(s *term.Styler, cmd *cobra.Command, f *pflag.Flag) string {
	var parts []string
	if f.Shorthand != "" {
		parts = append(parts, "-"+f.Shorthand)
	}
	parts = append(parts, "--"+f.Name)
	left := strings.Join(parts, ", ")
	if ph := flagPlaceholder(cmd, f.Name); ph != "" {
		left += " " + ph
	}
	return s.Cyan(left)
}

func padVisible(text string, width int) string {
	if n := term.VisibleLen(text); n < width {
		return text + strings.Repeat(" ", width-n)
	}
	return text
}

// setFlagPlaceholder records a flag's value placeholder ("<path>", "<n>", ...)
// for the help renderer. Registered alongside the flag by the add* helpers.
func setFlagPlaceholder(cmd *cobra.Command, name, placeholder string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations["rotor/flag-"+name] = placeholder
}

func flagPlaceholder(cmd *cobra.Command, name string) string {
	if cmd.Annotations == nil {
		return ""
	}
	return cmd.Annotations["rotor/flag-"+name]
}

// usageErrorRenderer prevents Cobra's stock usage dump from ever appearing:
// usage is fully owned by execute's compact error line + hint.
func usageErrorRenderer(*cobra.Command) error {
	return nil
}

// --- flag registration helpers ---

func addStringFlag(cmd *cobra.Command, dst *string, name, shorthand, def, placeholder, usage string) {
	cmd.Flags().StringVarP(dst, name, shorthand, def, usage)
	setFlagPlaceholder(cmd, name, placeholder)
}

func addBoolFlag(cmd *cobra.Command, dst *bool, name, shorthand string, def bool, usage string) {
	cmd.Flags().BoolVarP(dst, name, shorthand, def, usage)
}

// positiveIntValue validates --builders/--checkers: positive integers only,
// mirroring the yargs number validation the hand parser enforced.
type positiveIntValue struct {
	dst **int
}

func newPositiveIntValue(dst **int) pflag.Value { return &positiveIntValue{dst: dst} }

func (v *positiveIntValue) String() string {
	if v.dst == nil || *v.dst == nil {
		return ""
	}
	return strconv.Itoa(**v.dst)
}

func (v *positiveIntValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return errors.New("must be a positive integer")
	}
	*v.dst = &n
	return nil
}

func (v *positiveIntValue) Type() string { return "int" }

// nonNegativeIntValue validates --max-errors: a non-negative integer (0 =
// unlimited), default 50.
type nonNegativeIntValue struct {
	dst *int
}

func newNonNegativeIntValue(dst *int) pflag.Value { return &nonNegativeIntValue{dst: dst} }

func (v *nonNegativeIntValue) String() string {
	if v.dst == nil {
		return ""
	}
	return strconv.Itoa(*v.dst)
}

func (v *nonNegativeIntValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return errors.New("must be a non-negative integer")
	}
	*v.dst = n
	return nil
}

func (v *nonNegativeIntValue) Type() string { return "int" }

// enumValue validates --type against the game|model|package choices.
type enumValue struct {
	dst     *string
	choices map[string]bool
}

func newEnumValue(dst *string, choices ...string) pflag.Value {
	set := make(map[string]bool, len(choices))
	for _, c := range choices {
		set[c] = true
	}
	return &enumValue{dst: dst, choices: set}
}

func (v *enumValue) String() string {
	if v.dst == nil {
		return ""
	}
	return *v.dst
}

func (v *enumValue) Set(s string) error {
	if !v.choices[s] {
		return fmt.Errorf("invalid --type %q (choices: game, model, package)", s)
	}
	*v.dst = s
	return nil
}

func (v *enumValue) Type() string { return "string" }
