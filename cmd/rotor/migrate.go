package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/config"
)

// newMigrateCommand is `sloptor migrate [path] [--force]`: it converts a legacy
// rotor.config.ts (or rotor.config.js) into rotor.toml.
//
// It loads the old config through the retained goja/esbuild path (the only
// remaining user of that pipeline), serializes it to rotor.toml with a leading
// `#:schema ./rotor.schema.json` directive, writes rotor.schema.json, and
// renames the old config (and any rotor-config.d.ts) to a .bak sidecar.
//
// It refuses to overwrite an existing rotor.toml unless --force is passed.
func newMigrateCommand(streams cliStreams) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:                   "migrate [path] [--force]",
		Short:                 "convert a legacy rotor.config.ts to rotor.toml",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			dir := "."
			if len(argv) > 0 {
				dir = argv[0]
			}
			return runMigrateCommand(streams, dir, force)
		},
	}
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &force, "force", "f", false, "overwrite an existing rotor.toml")
	return cmd
}

func runMigrateCommand(streams cliStreams, dir string, force bool) error {
	start := time.Now()
	u := newUI(streams.out)
	u.banner("migrate")

	// Find the legacy config so we can name it in messages and rename it later.
	legacyPath := ""
	for _, name := range []string{"rotor.config.ts", "rotor.config.js"} {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			legacyPath = candidate
			break
		}
	}
	if legacyPath == "" {
		return runtimeFailure(fmt.Errorf(
			"no rotor.config.ts (or rotor.config.js) found in %s\n    migrate converts an existing TypeScript config to rotor.toml;\n    there is nothing to migrate here. Use `sloptor init` to start fresh.", dir))
	}

	tomlPath := filepath.Join(dir, config.ConfigFileName)
	if fileExists(tomlPath) && !force {
		return runtimeFailure(fmt.Errorf(
			"%s already exists\n    refusing to overwrite it; re-run with --force to replace it.", config.ConfigFileName))
	}

	cfg, err := config.LoadLegacyTS(dir)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return runtimeFailure(fmt.Errorf("no legacy config found in %s", dir))
		}
		return runtimeFailure(fmt.Errorf("could not load %s: %w", filepath.Base(legacyPath), err))
	}
	for _, w := range cfg.Warnings {
		newUI(streams.err).warn(w)
	}

	body, err := config.MarshalTOML(cfg)
	if err != nil {
		return runtimeFailure(fmt.Errorf("could not serialize config to TOML: %w", err))
	}
	out := config.SchemaDirective + "\n\n" + body
	if err := os.WriteFile(tomlPath, []byte(out), 0o644); err != nil {
		return runtimeFailure(fmt.Errorf("writing %s: %w", config.ConfigFileName, err))
	}

	events := []uiEvent{{Status: eventWrote, Target: config.ConfigFileName}}

	// Rename the legacy files to .bak so they stop being picked up but are not
	// lost. A .bak that already exists is overwritten (idempotent re-runs).
	if err := backup(legacyPath); err != nil {
		newUI(streams.err).warn(fmt.Sprintf("could not rename %s: %v", filepath.Base(legacyPath), err))
	} else {
		events = append(events, uiEvent{
			Status: eventWrote,
			Target: filepath.Base(legacyPath) + ".bak",
			Detail: filepath.Base(legacyPath) + " → " + filepath.Base(legacyPath) + ".bak",
		})
	}
	dtsPath := filepath.Join(dir, "rotor-config.d.ts")
	if fileExists(dtsPath) {
		if err := backup(dtsPath); err != nil {
			newUI(streams.err).warn(fmt.Sprintf("could not rename %s: %v", filepath.Base(dtsPath), err))
		} else {
			events = append(events, uiEvent{Status: eventWrote, Target: "rotor-config.d.ts.bak", Detail: "rotor-config.d.ts → rotor-config.d.ts.bak"})
		}
	}

	events = append(events, uiEvent{
		Status:  eventFinished,
		Target:  "migration complete",
		Detail:  "review " + config.ConfigFileName + " and commit it",
		Elapsed: time.Since(start),
	})
	u.events(events)
	fmt.Fprintln(streams.out)
	return nil
}

// backup renames path to path + ".bak", replacing any existing .bak.
func backup(path string) error {
	bak := path + ".bak"
	_ = os.Remove(bak)
	return os.Rename(path, bak)
}
