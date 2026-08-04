package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"rotor/internal/logservice"
)

// cmdDev runs the developer inner loop: it watches the project and incrementally
// compiles to Luau (like `rotor build -w`) while supervising a `rojo serve` child so
// Roblox Studio live-syncs the fresh output. One Ctrl-C tears down both. rotor does
// not speak the Rojo protocol itself — it launches the installed `rojo` CLI. Use
// --no-serve to watch and build without serving.
func cmdDev(args []string) int {
	noServe := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--no-serve" {
			noServe = true
			continue
		}
		rest = append(rest, a)
	}

	parsed, err := parseBuildArgs(rest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor dev: %v\n\n", err)
		usage(os.Stderr)
		return 1
	}
	if parsed.help {
		usage(os.Stdout)
		return 0
	}

	tsConfigPath, err := findTsConfigPath(parsed.project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sloptor dev: %v\n", err)
		return 1
	}
	opts := mergeProjectOptions(defaultProjectOptions, readRbxtsOptions(tsConfigPath), &parsed.opts)
	opts.watch = true
	logservice.Verbose = opts.verbose
	dir := filepath.Dir(tsConfigPath)

	out := newUI(os.Stdout)
	out.banner("dev  " + filepath.Base(dir))

	var rojoCmd *exec.Cmd
	if !noServe {
		rojoCmd = startRojoServe(dir, opts.rojo, out)
	}
	defer stopRojo(rojoCmd)

	// Catch Ctrl-C so we can tear the rojo child down instead of orphaning it.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)

	done := make(chan int, 1)
	go func() {
		done <- runBuildWatch(dir, tsConfigPath, opts, watchOptions{maxErrors: 50, clearScreen: true})
	}()

	select {
	case <-sigc:
		return 0
	case code := <-done:
		return code
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
