package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes args against the real root with the given environment and
// returns the exit code plus the rendered stdout/stderr.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := execute(args, cliStreams{in: strings.NewReader(""), out: &out, err: &errOut})
	return code, out.String(), errOut.String()
}

// TestRootHelpRendersCargoStyleLayout pins the custom help renderer: a
// sloptor identity line, grouped command sections, an aligned Environment
// table, and no stock Cobra blocks.
func TestRootHelpRendersCargoStyleLayout(t *testing.T) {
	code, out, errOut := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("--help exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{
		"sloptor — an all-in-one Roblox toolchain",
		"Usage:",
		"Options:",
		"Compile & check", "Project & deps", "Cloud", "Other",
		"build", "check", "completion", "version",
		"Environment:",
		"RBXTSC_WRITE_CONCURRENCY",
		"Exit codes:",
		"0  success",
		"1  any failure",
		"exception:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("root help missing %q:\n%s", want, out)
		}
	}
	// Stock Cobra template artifacts must never appear.
	for _, banned := range []string{"Available Commands", "Flags:", "Use \"", "help for"} {
		if strings.Contains(out, banned) {
			t.Errorf("root help contains stock Cobra text %q:\n%s", banned, out)
		}
	}
	// Groups render in defined order with their commands: the first command
	// under Compile & check is build (marquee command first), and the Cloud
	// group lists asset before deploy.
	compile := out[strings.Index(out, "Compile & check"):]
	compile = compile[:strings.Index(compile, "Project & deps")]
	lines := strings.Split(compile, "\n")
	var firstCmd string
	for _, line := range lines {
		if strings.HasPrefix(line, "    ") { // command rows, not the group heading
			firstCmd, _, _ = strings.Cut(strings.TrimSpace(line), " ")
			break
		}
	}
	if firstCmd != "build" {
		t.Errorf("first command under Compile & check = %q, want build:\n%s", firstCmd, compile)
	}
	cloud := out[strings.Index(out, "Cloud"):]
	cloud = cloud[:strings.Index(cloud, "Options:")]
	if strings.Index(cloud, "asset") > strings.Index(cloud, "deploy") {
		t.Errorf("Cloud group does not list asset before deploy:\n%s", cloud)
	}
}

// TestBuildHelpShowsOptionsOrder pins the documented flag registration order
// (registration order, not alphabetic).
func TestBuildHelpShowsOptionsOrder(t *testing.T) {
	code, out, errOut := runCLI(t, "build", "--help")
	if code != 0 {
		t.Fatalf("build --help exit = %d, stderr: %s", code, errOut)
	}
	options := out[strings.Index(out, "Options:"):]
	if idx := strings.Index(options, "--project"); idx < 0 {
		t.Errorf("build help missing --project:\n%s", options)
	}
	if idx := strings.Index(options, "--watch"); idx < 0 {
		t.Errorf("build help missing --watch:\n%s", options)
	}
	// The build surface is registered in the documented order: project before
	// watch, watch before the DX flags.
	if strings.Index(options, "--project") > strings.Index(options, "--watch") {
		t.Errorf("build help renders --project after --watch:\n%s", options)
	}
	if strings.Index(options, "--watch") > strings.Index(options, "--max-errors") {
		t.Errorf("build help renders --watch after --max-errors:\n%s", options)
	}
}

// TestInvalidCommandRendersCompactError pins the usage-error contract: one
// red error: line, a dim help hint, exit 1, and never a full help dump.
func TestInvalidCommandRendersCompactError(t *testing.T) {
	code, out, errOut := runCLI(t, "nope")
	if code != 1 {
		t.Fatalf("unknown command exit = %d, want 1", code)
	}
	if !strings.HasPrefix(errOut, "error: ") {
		t.Errorf("stderr = %q, want a red error: prefix", errOut)
	}
	if !strings.Contains(errOut, "Run 'sloptor --help' for usage.") {
		t.Errorf("stderr = %q, want the dim usage hint", errOut)
	}
	if out != "" {
		t.Errorf("unknown command wrote to stdout: %q", out)
	}
}

// TestSubcommandUsageErrorNamesTheCommand pins the resolved-command hint for
// a subcommand parse failure.
func TestSubcommandUsageErrorNamesTheCommand(t *testing.T) {
	_, _, errOut := runCLI(t, "build", "--bogus")
	if !strings.Contains(errOut, "Run 'sloptor build --help' for usage.") {
		t.Errorf("stderr = %q, want the sloptor build hint", errOut)
	}
}

// TestVersionSurfaces covers the root -v/--version flag and the version
// subcommand, all printing the bare version string.
func TestVersionSurfaces(t *testing.T) {
	old := version
	version = "9.9.9-test"
	t.Cleanup(func() { version = old })
	for _, args := range [][]string{{"--version"}, {"-v"}, {"version"}} {
		code, out, errOut := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v exit = %d, stderr: %s", args, code, errOut)
		}
		if strings.TrimSpace(out) != "9.9.9-test" {
			t.Errorf("%v output = %q, want the bare version", args, out)
		}
	}
}

// TestBuildVersionFlag pins `sloptor build -v` printing the bare version.
func TestBuildVersionFlag(t *testing.T) {
	old := version
	version = "9.9.9-test"
	t.Cleanup(func() { version = old })
	code, out, errOut := runCLI(t, "build", "-v")
	if code != 0 {
		t.Fatalf("build -v exit = %d, stderr: %s", code, errOut)
	}
	if strings.TrimSpace(out) != "9.9.9-test" {
		t.Errorf("build -v output = %q, want the bare version", out)
	}
}

// TestCompletionGeneratesNativeScripts pins the completion contract: each
// shell's script names sloptor, exposes the current subcommands, writes no
// banner, and is pure script on stdout (no ANSI).
func TestCompletionGeneratesNativeScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			code, out, errOut := runCLI(t, "completion", shell)
			if code != 0 {
				t.Fatalf("completion %s exit = %d, stderr: %s", shell, code, errOut)
			}
			if !strings.Contains(out, "sloptor") {
				t.Errorf("%s script does not name sloptor", shell)
			}
			// Cobra's scripts resolve commands at runtime through the
			// __complete protocol rather than embedding a static list.
			if !strings.Contains(out, "__complete") {
				t.Errorf("%s script does not wire the __complete protocol", shell)
			}
			if strings.Contains(out, "\x1b[") {
				t.Errorf("%s script contains ANSI escapes", shell)
			}
			if strings.HasPrefix(out, "\n  sloptor v") {
				t.Errorf("%s script starts with a UI banner:\n%s", shell, out[:min(200, len(out))])
			}
			if errOut != "" {
				t.Errorf("completion %s wrote to stderr: %q", shell, errOut)
			}
		})
	}
}

// TestCompletionInvalidShell pins the argument validation for completion.
func TestCompletionInvalidShell(t *testing.T) {
	code, _, errOut := runCLI(t, "completion", "tcsh")
	if code != 1 {
		t.Fatalf("completion tcsh exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "error:") {
		t.Errorf("stderr = %q, want an error line", errOut)
	}
}

// TestCompletionHelpShowsInstallExamples pins that completion's own help is
// rendered by the shared renderer (green Examples heading, cyan commands).
func TestCompletionHelpShowsInstallExamples(t *testing.T) {
	code, out, errOut := runCLI(t, "completion", "--help")
	if code != 0 {
		t.Fatalf("completion --help exit = %d, stderr: %s", code, errOut)
	}
	for _, want := range []string{"Examples:", "sloptor completion bash", "sloptor completion zsh", "sloptor completion fish", "sloptor completion powershell"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion help missing %q:\n%s", want, out)
		}
	}
}

// TestNoColorHelpAndEvents pins NO_COLOR suppression across the rendered
// surface: help and event rows must contain no ANSI escapes.
func TestNoColorHelpAndEvents(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	code, helpOut, errOut := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("--help exit = %d, stderr: %s", code, errOut)
	}
	if strings.Contains(helpOut, "\x1b[") {
		t.Errorf("NO_COLOR root help contains ANSI escapes:\n%q", helpOut)
	}
	code, errOut2, _ := runCLI(t, "nope")
	if code != 1 {
		t.Fatalf("nope exit = %d, want 1", code)
	}
	if strings.Contains(errOut2, "\x1b[") {
		t.Errorf("NO_COLOR error line contains ANSI escapes: %q", errOut2)
	}
}

// TestLegacyNegatedBooleans pins the yargs --no-<flag> normalization end to
// end through the root: --no-luau is accepted and behaves like --luau=false
// (emitting .lua instead of .luau).
func TestLegacyNegatedBooleans(t *testing.T) {
	dir := writeBuildableProject(t, "")
	_, _, code := captureBuildOutput(t, []string{"--no-luau", dir})
	if code != 0 {
		t.Fatalf("build --no-luau exit = %d, want 0", code)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lua") {
			found = true
		}
	}
	if !found {
		t.Error("--no-luau did not emit .lua outputs")
	}
}
