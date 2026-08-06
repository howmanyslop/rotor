package main

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cmd* adapters route the legacy test entrypoints through the public Cobra
// surface: every invocation is `run(["<command>", ...args...])`, so the whole
// suite exercises the real command tree (flag parsing, legacy --no-*
// normalization, stream wiring, exit codes) with no production wrappers.
func cmdBuild(args []string) int       { return run(append([]string{"build"}, args...)) }
func cmdCheck(args []string) int       { return run(append([]string{"check"}, args...)) }
func cmdDiagnostics(args []string) int { return run(append([]string{"diagnostics"}, args...)) }
func cmdDoctor(args []string) int      { return run(append([]string{"doctor"}, args...)) }
func cmdMinify(args []string) int      { return run(append([]string{"minify"}, args...)) }
func cmdBundle(args []string) int      { return run(append([]string{"bundle"}, args...)) }
func cmdDev(args []string) int         { return run(append([]string{"dev"}, args...)) }
func cmdPack(args []string) int        { return run(append([]string{"pack"}, args...)) }
func cmdInit(args []string) int        { return run(append([]string{"init"}, args...)) }
func cmdMigrate(args []string) int     { return run(append([]string{"migrate"}, args...)) }
func cmdSchema(args []string) int      { return run(append([]string{"schema"}, args...)) }
func cmdSourcemap(args []string) int   { return run(append([]string{"sourcemap"}, args...)) }
func cmdAsset(args []string) int       { return run(append([]string{"asset"}, args...)) }
func cmdDeploy(args []string) int      { return run(append([]string{"deploy"}, args...)) }
func cmdClean(args []string) int       { return run(append([]string{"clean"}, args...)) }
func cmdAdd(args []string) int         { return run(append([]string{"add"}, args...)) }

// testStreams are the discard streams used when only parsing matters.
func testStreams() cliStreams {
	return cliStreams{in: strings.NewReader(""), out: io.Discard, err: io.Discard}
}

// parseCommandFlags runs Cobra's flag parsing (including legacy --no-*
// normalization) against a bare command and returns the positional args.
func parseCommandFlags(t *testing.T, cmd *cobra.Command, args []string) []string {
	t.Helper()
	normalized := normalizeLegacyArgs(cmd, args)
	cmd.SetArgs(normalized)
	if err := cmd.ParseFlags(normalized); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cmd.Flags().Args()
}

// parseBuildArgsForTest parses build argv through the real Cobra flag set and
// returns the resulting buildArgs without running the command or its
// implication checks (those are exercised behaviorally through run()).
func parseBuildArgsForTest(t *testing.T, args []string) *buildArgs {
	t.Helper()
	flags := &buildFlags{maxErrors: 50, clear: true}
	cmd := &cobra.Command{Use: "build"}
	registerBuildFlags(cmd, flags)
	argv := parseCommandFlags(t, cmd, args)
	ba := buildArgs{project: ".", maxErrors: 50, clearScreen: true}
	if err := collectBuildArgs(cmd.Flags(), argv, flags, &ba); err != nil {
		t.Fatalf("collectBuildArgs(%v): %v", args, err)
	}
	return &ba
}

// parseCheckArgsForTest parses check argv through the real Cobra flag set.
func parseCheckArgsForTest(t *testing.T, args []string) *checkArgs {
	t.Helper()
	var ca checkArgs
	cmd := &cobra.Command{Use: "check"}
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &ca.watch, "watch", "w", false, "")
	addBoolFlag(cmd, &ca.jsonOut, "json", "", false, "")
	cmd.Flags().VarP(newPositiveIntValue(&ca.checkers), "checkers", "", "")
	argv := parseCommandFlags(t, cmd, args)
	ca.project = "."
	if len(argv) > 0 {
		ca.project = argv[0]
	}
	return &ca
}

// parseDiagnosticsArgsForTest parses diagnostics argv through the real Cobra
// flag set.
func parseDiagnosticsArgsForTest(t *testing.T, args []string) *diagnosticsArgs {
	t.Helper()
	var da diagnosticsArgs
	cmd := &cobra.Command{Use: "diagnostics"}
	cmd.Flags().SortFlags = false
	addStringFlag(cmd, &da.project, "project", "p", "", "", "")
	addStringFlag(cmd, &da.buildPath, "build", "b", "", "", "")
	cmd.Flags().VarP(newPositiveIntValue(&da.builders), "builders", "", "")
	cmd.Flags().VarP(newPositiveIntValue(&da.checkers), "checkers", "", "")
	addBoolFlag(cmd, &da.jsonOut, "json", "", false, "")
	argv := parseCommandFlags(t, cmd, args)
	if !cmd.Flags().Changed("project") {
		da.project = "."
	}
	if len(argv) > 0 {
		da.project = argv[0]
	}
	if cmd.Flags().Changed("build") {
		da.build = true
		da.buildPath, _ = cmd.Flags().GetString("build")
	}
	return &da
}
