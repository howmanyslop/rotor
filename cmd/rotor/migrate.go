package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"rotor/internal/config"
)

// cmdMigrate is `rotor migrate [path] [--force]`: it converts a legacy
// rotor.config.ts (or rotor.config.js) into rotor.toml.
//
// It loads the old config through the retained goja/esbuild path (the only
// remaining user of that pipeline), serializes it to rotor.toml with a leading
// `#:schema ./rotor.schema.json` directive, writes rotor.schema.json, and
// renames the old config (and any rotor-config.d.ts) to a .bak sidecar.
//
// It refuses to overwrite an existing rotor.toml unless --force is passed.
func cmdMigrate(args []string) int {
	return migrateMain(args, os.Stdout, os.Stderr)
}

func migrateMain(args []string, stdout, stderr io.Writer) int {
	dir := ""
	force := false
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help":
			migrateUsage(stdout)
			return 0
		case a == "--force" || a == "-f":
			force = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "sloptor migrate: unknown flag %q\n\n", a)
			migrateUsage(stderr)
			return 1
		default:
			if dir != "" {
				fmt.Fprintf(stderr, "sloptor migrate: unexpected extra argument %q\n\n", a)
				migrateUsage(stderr)
				return 1
			}
			dir = a
		}
	}
	if dir == "" {
		dir = "."
	}

	u := newUI(stdout)
	errUI := newUI(stderr)
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
		errUI.failLine(fmt.Sprintf("sloptor migrate: no rotor.config.ts (or rotor.config.js) found in %s", dir))
		fmt.Fprintln(stderr, "    migrate converts an existing TypeScript config to rotor.toml;")
		fmt.Fprintln(stderr, "    there is nothing to migrate here. Use `sloptor init` to start fresh.")
		return 1
	}

	tomlPath := filepath.Join(dir, config.ConfigFileName)
	if fileExists(tomlPath) && !force {
		errUI.failLine(fmt.Sprintf("sloptor migrate: %s already exists", config.ConfigFileName))
		fmt.Fprintln(stderr, "    refusing to overwrite it; re-run with --force to replace it.")
		return 1
	}

	cfg, err := config.LoadLegacyTS(dir)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			errUI.failLine(fmt.Sprintf("sloptor migrate: no legacy config found in %s", dir))
			return 1
		}
		errUI.failLine(fmt.Sprintf("sloptor migrate: could not load %s: %v", filepath.Base(legacyPath), err))
		return 1
	}
	for _, w := range cfg.Warnings {
		errUI.warn("sloptor migrate: " + w)
	}

	body, err := config.MarshalTOML(cfg)
	if err != nil {
		errUI.failLine(fmt.Sprintf("sloptor migrate: could not serialize config to TOML: %v", err))
		return 1
	}
	out := config.SchemaDirective + "\n\n" + body
	if err := os.WriteFile(tomlPath, []byte(out), 0o644); err != nil {
		errUI.failLine(fmt.Sprintf("sloptor migrate: writing %s: %v", config.ConfigFileName, err))
		return 1
	}
	u.okLine("wrote "+config.ConfigFileName, "")

	// Rename the legacy files to .bak so they stop being picked up but are not
	// lost. A .bak that already exists is overwritten (idempotent re-runs).
	if err := backup(legacyPath); err != nil {
		errUI.warn(fmt.Sprintf("could not rename %s: %v", filepath.Base(legacyPath), err))
	} else {
		u.noteLine(filepath.Base(legacyPath) + " → " + filepath.Base(legacyPath) + ".bak")
	}
	dtsPath := filepath.Join(dir, "rotor-config.d.ts")
	if fileExists(dtsPath) {
		if err := backup(dtsPath); err != nil {
			errUI.warn(fmt.Sprintf("could not rename %s: %v", filepath.Base(dtsPath), err))
		} else {
			u.noteLine("rotor-config.d.ts → rotor-config.d.ts.bak")
		}
	}

	fmt.Fprintln(stdout)
	u.okLine("migration complete", "review "+config.ConfigFileName+" and commit it")
	fmt.Fprintln(stdout)
	return 0
}

// backup renames path to path + ".bak", replacing any existing .bak.
func backup(path string) error {
	bak := path + ".bak"
	_ = os.Remove(bak)
	return os.Rename(path, bak)
}

func migrateUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: sloptor migrate [path] [--force]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  path       project directory containing rotor.config.ts (default \".\")")
	fmt.Fprintln(w, "  -f, --force  overwrite an existing rotor.toml")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Converts a legacy rotor.config.ts to rotor.toml (+ rotor.schema.json) and")
	fmt.Fprintln(w, "renames the old config to rotor.config.ts.bak.")
}
