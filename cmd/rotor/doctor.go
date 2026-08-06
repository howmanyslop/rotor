package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/compile"
	"rotor/internal/config"
)

// newDoctorCommand diagnoses the environment and project setup that `rotor
// build` depends on: tsconfig discovery, installed @rbxts packages, Node.js
// and the transformer sidecar when plugins are configured, and Rojo wiring.
// Rows are ok/warn/fail with actionable hints, rendered as aligned event
// rows; only hard failures exit 1.
func newDoctorCommand(streams cliStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "doctor [path]",
		Short:                 "diagnose the project setup (tsconfig, @rbxts, plugins, Rojo)",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			path := "."
			if len(argv) > 0 {
				path = argv[0]
			}
			return runDoctorCommand(streams, path)
		},
	}
	cmd.Flags().SortFlags = false
	return cmd
}

func runDoctorCommand(streams cliStreams, path string) error {
	start := time.Now()
	u := newUI(streams.out)
	checks, projectName := runDoctor(path)
	u.banner("doctor" + projectName)

	fails, warns := 0, 0
	events := make([]uiEvent, 0, len(checks)+1)
	for _, c := range checks {
		status := eventChecked
		switch c.status {
		case doctorInfo:
			status = eventUnchanged
		case doctorWarn:
			status = eventSkipped
			warns++
		case doctorFail:
			status = eventFailed
			fails++
		}
		detail := c.detail
		if c.hint != "" && c.status >= doctorWarn {
			detail += " — " + c.hint
		}
		events = append(events, uiEvent{Status: status, Target: c.label, Detail: detail})
	}

	switch {
	case fails > 0:
		events = append(events, uiEvent{
			Status:  eventFailed,
			Detail:  fmt.Sprintf("%s · (%d checks, %d warnings)", plural(fails, "problem")+" found", len(checks), warns),
			Elapsed: time.Since(start),
		})
	case warns > 0:
		events = append(events, uiEvent{
			Status:  eventSkipped,
			Detail:  fmt.Sprintf("ready, with %s · (%d checks)", plural(warns, "warning"), len(checks)),
			Elapsed: time.Since(start),
		})
	default:
		events = append(events, uiEvent{
			Status:  eventFinished,
			Detail:  fmt.Sprintf("everything looks good · (%d checks)", len(checks)),
			Elapsed: time.Since(start),
		})
	}
	u.events(events)
	fmt.Fprintln(streams.out)

	if fails > 0 {
		return reportedFailure(errors.New("doctor found problems"))
	}
	return nil
}

type doctorStatus int

const (
	doctorOK doctorStatus = iota
	doctorInfo
	doctorWarn
	doctorFail
)

type doctorCheck struct {
	status doctorStatus
	label  string
	detail string // muted context shown after the label
	hint   string // indented remedy line, shown for warn/fail
}

// runDoctor evaluates every check for the project at path. It returns the
// rows plus a " · <project>" banner suffix once the project dir is known.
func runDoctor(path string) ([]doctorCheck, string) {
	var checks []doctorCheck

	tsConfigPath, err := findTsConfigPath(path)
	if err != nil {
		checks = append(checks, doctorCheck{
			status: doctorFail,
			label:  "tsconfig.json",
			detail: "not found",
			hint:   "run from a roblox-ts project, or pass a path: sloptor doctor <project>",
		})
		return checks, ""
	}
	dir := filepath.Dir(tsConfigPath)
	checks = append(checks, doctorCheck{status: doctorOK, label: "tsconfig.json", detail: tsConfigPath})

	nodeModules := filepath.Join(dir, "node_modules")
	hasPackageJSON := fileExists(filepath.Join(dir, "package.json"))
	hasNodeModules := dirExists(nodeModules)
	switch {
	case !hasPackageJSON:
		checks = append(checks, doctorCheck{
			status: doctorWarn,
			label:  "package.json",
			detail: "not found next to tsconfig.json",
			hint:   "roblox-ts projects resolve @rbxts/* types from npm packages",
		})
	case !hasNodeModules:
		checks = append(checks, doctorCheck{
			status: doctorFail,
			label:  "node_modules",
			detail: "missing",
			hint:   "install dependencies first (npm install / bun install / pnpm install)",
		})
	default:
		checks = append(checks, doctorCheck{status: doctorOK, label: "node_modules", detail: "installed"})
	}

	if hasNodeModules {
		checks = append(checks, packageCheck(nodeModules, "@rbxts/compiler-types", doctorFail,
			"npm install -D @rbxts/compiler-types"))
		checks = append(checks, packageCheck(nodeModules, "@rbxts/types", doctorFail,
			"npm install -D @rbxts/types"))
	}

	transforms := tsconfigTransformerPlugins(tsConfigPath)
	nodeStatus := doctorInfo
	nodeHint := ""
	if len(transforms) > 0 {
		// Transformer plugins hard-require Node (the sidecar host).
		nodeStatus = doctorFail
		nodeHint = "transformer plugins are configured; install Node.js (https://nodejs.org)"
	}
	nodeVersion, nodeOK := toolVersion("node", "--version")
	if nodeOK {
		checks = append(checks, doctorCheck{status: doctorOK, label: "Node.js", detail: nodeVersion})
	} else {
		checks = append(checks, doctorCheck{status: nodeStatus, label: "Node.js", detail: "not on PATH", hint: nodeHint})
	}

	if len(transforms) > 0 {
		if hasNodeModules {
			checks = append(checks, packageCheck(nodeModules, "typescript", doctorFail,
				"transformer plugins resolve the project's own typescript package; npm install -D typescript"))
		}
		for _, name := range transforms {
			if dirExists(filepath.Join(nodeModules, filepath.FromSlash(name))) {
				checks = append(checks, doctorCheck{status: doctorOK, label: "transformer " + name, detail: "installed"})
			} else {
				// Builds log transformer-not-found as a warning and continue,
				// so doctor matches that severity.
				checks = append(checks, doctorCheck{
					status: doctorWarn,
					label:  "transformer " + name,
					detail: "not found in node_modules",
					hint:   "npm install -D " + name,
				})
			}
		}
		if sidecarDir, err := compile.ResolveSidecarDir(); err == nil {
			checks = append(checks, doctorCheck{status: doctorOK, label: "transformer sidecar", detail: sidecarDir})
		} else {
			checks = append(checks, doctorCheck{
				status: doctorFail,
				label:  "transformer sidecar",
				detail: err.Error(),
				hint:   "the embedded worker could not be extracted to the user cache dir",
			})
		}
	}

	if projects, _ := filepath.Glob(filepath.Join(dir, "*.project.json")); len(projects) > 0 {
		checks = append(checks, doctorCheck{status: doctorOK, label: "Rojo project", detail: filepath.Base(projects[0])})
	} else {
		checks = append(checks, doctorCheck{
			status: doctorWarn,
			label:  "Rojo project",
			detail: "no *.project.json found",
			hint:   "game projects need one for require-path resolution (default.project.json)",
		})
	}
	if rojoVersion, ok := toolVersion("rojo", "--version"); ok {
		checks = append(checks, doctorCheck{status: doctorOK, label: "rojo CLI", detail: rojoVersion})
	} else {
		checks = append(checks, doctorCheck{status: doctorInfo, label: "rojo CLI", detail: "not on PATH (only needed to sync/serve, not to compile)"})
	}

	checks = append(checks, cloudChecks(dir)...)

	return checks, "  " + filepath.Base(dir)
}

// cloudChecks evaluates the cloud tooling section: rotor.toml (loaded via
// config.Load and validated when present), its companion rotor.schema.json,
// and ROBLOX_API_KEY presence. Only presence is reported — the key value is
// never printed. Without a config the section degrades to muted info rows
// (cloud features are optional), matching the rojo CLI row's style.
func cloudChecks(dir string) []doctorCheck {
	var checks []doctorCheck

	hasConfig := true
	cfg, err := config.Load(dir)
	switch {
	case errors.Is(err, config.ErrNotFound):
		hasConfig = false
		checks = append(checks, doctorCheck{
			status: doctorWarn,
			label:  config.ConfigFileName,
			detail: "not found",
			hint:   "run `sloptor init` to add sloptor config (needed for sloptor asset / sloptor deploy)",
		})
	case err != nil:
		checks = append(checks, doctorCheck{
			status: doctorFail,
			label:  config.ConfigFileName,
			detail: err.Error(),
			hint:   "sloptor asset / sloptor deploy cannot run until the config loads",
		})
	default:
		validateErrs := cfg.Validate()
		if len(validateErrs) == 0 {
			checks = append(checks, doctorCheck{status: doctorOK, label: config.ConfigFileName, detail: "valid"})
		}
		for _, verr := range validateErrs {
			checks = append(checks, doctorCheck{
				status: doctorFail,
				label:  config.ConfigFileName,
				detail: verr.Error(),
				hint:   "sloptor asset / sloptor deploy cannot run until the config is valid",
			})
		}
		for _, warning := range cfg.Warnings {
			checks = append(checks, doctorCheck{status: doctorWarn, label: config.ConfigFileName, detail: warning})
		}
	}

	switch {
	case os.Getenv("ROBLOX_API_KEY") != "":
		checks = append(checks, doctorCheck{status: doctorOK, label: "ROBLOX_API_KEY", detail: "set"})
	case hasConfig:
		// A config is present, so cloud commands are in use; an unset key
		// will stop them.
		checks = append(checks, doctorCheck{
			status: doctorWarn,
			label:  "ROBLOX_API_KEY",
			detail: "not set",
			hint:   "set ROBLOX_API_KEY to use sloptor asset / sloptor deploy",
		})
	default:
		checks = append(checks, doctorCheck{
			status: doctorInfo,
			label:  "ROBLOX_API_KEY",
			detail: "not set (only needed for sloptor asset / sloptor deploy)",
		})
	}
	return checks
}

// packageCheck reports an installed package's version, or missStatus + hint
// when it cannot be resolved.
func packageCheck(nodeModules, pkg string, missStatus doctorStatus, hint string) doctorCheck {
	if version, ok := readPackageVersion(nodeModules, pkg); ok {
		return doctorCheck{status: doctorOK, label: pkg, detail: "v" + version}
	}
	return doctorCheck{status: missStatus, label: pkg, detail: "not installed", hint: hint}
}

// readPackageVersion reads node_modules/<pkg>/package.json's version field.
func readPackageVersion(nodeModules, pkg string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(nodeModules, filepath.FromSlash(pkg), "package.json"))
	if err != nil {
		return "", false
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version == "" {
		return "", false
	}
	return manifest.Version, true
}

// tsconfigTransformerPlugins lists compilerOptions.plugins[].transform names
// declared by this tsconfig file (raw single-file read). A config that declares
// `plugins` resolves to exactly this list, because `extends` replaces the
// option rather than merging it; one that declares none inherits its parent's,
// which this does not follow, so doctor can under-report there.
func tsconfigTransformerPlugins(tsConfigPath string) []string {
	data, err := os.ReadFile(tsConfigPath)
	if err != nil {
		return nil
	}
	var root struct {
		CompilerOptions struct {
			Plugins []struct {
				Transform string `json:"transform"`
			} `json:"plugins"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal([]byte(compile.StripJSONC(string(data))), &root) != nil {
		return nil
	}
	var transforms []string
	for _, p := range root.CompilerOptions.Plugins {
		if p.Transform != "" {
			transforms = append(transforms, p.Transform)
		}
	}
	return transforms
}

// toolVersion runs `<tool> <arg>` with a short timeout and returns the first
// line of output (e.g. "v22.10.0", "Rojo 7.4.4").
func toolVersion(tool, arg string) (string, bool) {
	if _, err := exec.LookPath(tool); err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, arg).Output()
	if err != nil {
		return "", false
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(version), version != ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
