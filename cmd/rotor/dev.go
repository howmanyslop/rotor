package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	"rotor/internal/logservice"
)

// newDevCommand is the developer inner loop: it watches the project and
// incrementally compiles to Luau (like `sloptor build -w`) while supervising a
// `rojo serve` child so Roblox Studio live-syncs the fresh output. One Ctrl-C
// tears down both. rotor does not speak the Rojo protocol itself — it
// launches the installed `rojo` CLI. Use --no-serve to watch and build
// without serving. The build option surface is registered as-is (dev forwards
// the flags it accepts); watch mode is forced after parsing.
func newDevCommand(streams cliStreams) *cobra.Command {
	flags := &buildFlags{maxErrors: 50, clear: true}
	serve := true
	cmd := &cobra.Command{
		Use:                   "dev [path] [--no-serve]",
		Short:                 "watch + incrementally compile, serve to Studio via `rojo serve`",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runDevCommand(streams, cmd, flags, serve, argv)
		},
	}
	registerBuildFlags(cmd, flags)
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &serve, "serve", "", true,
		"serve to Studio via `rojo serve` (--no-serve disables)")
	return cmd
}

// runDevCommand forces watch mode, launches rojo serve unless disabled, and
// blocks until Ctrl-C or the watch loop exits. The build-side implication
// checks run as they did for `sloptor dev` (which always parses the build
// surface); the finite-diagnostics watch conflicts do not apply because dev
// never started profiles.
func runDevCommand(streams cliStreams, cmd *cobra.Command, flags *buildFlags, serve bool, argv []string) error {
	ba := buildArgs{project: ".", maxErrors: 50, clearScreen: true}
	if err := collectBuildArgs(cmd.Flags(), argv, flags, &ba); err != nil {
		return err
	}
	if ba.builders != nil && !ba.build {
		return usageFailure("--builders requires --build")
	}
	if ba.opts.usePolling != nil && ba.opts.watch == nil {
		return usageFailure("Implications failed:\n usePolling -> watch")
	}
	if ba.emitDeclarationOnly && !ba.build {
		return usageFailure("Implications failed:\n emitDeclarationOnly -> build")
	}

	tsConfigPath, err := findTsConfigPath(ba.project)
	if err != nil {
		return runtimeFailure(err)
	}
	opts := mergeProjectOptions(defaultProjectOptions, readRbxtsOptions(tsConfigPath), &ba.opts)
	opts.watch = true
	logservice.Verbose = opts.verbose
	dir := filepath.Dir(tsConfigPath)

	out := newUI(streams.out)
	out.banner("dev  " + filepath.Base(dir))

	var rojoCmd *exec.Cmd
	if serve {
		rojoCmd = startRojoServe(dir, opts.rojo, out)
	}
	defer stopRojo(rojoCmd)

	// Catch Ctrl-C so we can tear the rojo child down instead of orphaning it.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)

	done := make(chan int, 1)
	go func() {
		done <- runBuildWatch(dir, tsConfigPath, opts, watchOptions{maxErrors: ba.maxErrors, clearScreen: ba.clearScreen})
	}()

	select {
	case <-sigc:
		return nil
	case code := <-done:
		if code != 0 {
			return reportedFailure(errors.New("dev loop failed"))
		}
		return nil
	}
}

// startRojoServe launches `rojo serve <project>` for live Studio sync, or returns
// nil (with a hint) if rojo or a project file is unavailable — dev still watches and
// builds in that case.
func startRojoServe(dir string, rojoFlag string, out *ui) *exec.Cmd {
	rojoBin, err := exec.LookPath("rojo")
	if err != nil {
		out.warn("rojo not on PATH — dev will watch and build, but not serve to Studio (install rojo, or pass --no-serve to silence)")
		return nil
	}
	project := resolveRojoProject(dir, rojoFlag)
	if project == "" {
		out.warn("no *.project.json found — dev will watch and build, but not serve (add default.project.json, or pass --no-serve)")
		return nil
	}
	cmd := exec.Command(rojoBin, "serve", project)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		out.warn("failed to start rojo serve: " + err.Error())
		return nil
	}
	out.okLine("serving "+filepath.Base(project), "via rojo serve")
	return cmd
}

// resolveRojoProject picks the Rojo project file: an explicit --rojo flag, else
// default.project.json, else the first *.project.json in the project directory.
func resolveRojoProject(dir string, rojoFlag string) string {
	if rojoFlag != "" {
		return rojoFlag
	}
	if def := filepath.Join(dir, "default.project.json"); isRegularFile(def) {
		return def
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.project.json")); len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func stopRojo(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}
