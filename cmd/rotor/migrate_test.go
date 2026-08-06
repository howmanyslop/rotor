package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"rotor/internal/config"
)

// legacyConfigTS is a representative legacy rotor.config.ts covering assets +
// deploy with nested places, an experience, a gamepass, and a social link.
const legacyConfigTS = `import { defineConfig } from "@rotor-rbx/rotor";

export default defineConfig({
	assets: {
		paths: ["assets/**/*.png", "assets/**/*.ogg"],
		output: { luau: "src/shared/assets.luau", types: "src/shared/assets.d.ts" },
		creator: { type: "group", id: 12345 },
	},
	deploy: {
		environments: {
			prod: {
				universeId: 333,
				places: {
					start: { file: "build/game.rbxl", placeId: 444, name: "Start", maxPlayers: 30, versionType: "saved" },
				},
				experience: { name: "My Game", playability: "public" },
				gamepasses: { vip: { name: "VIP", price: 250, icon: "assets/vip.png" } },
				socials: { discord: { title: "Join", url: "https://discord.gg/x", type: "discord" } },
			},
		},
	},
});
`

func runMigrate(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = execute(append([]string{"migrate"}, args...), cliStreams{in: strings.NewReader(""), out: &out, err: &errBuf})
	return code, out.String(), errBuf.String()
}

func TestMigrateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "rotor.config.ts")
	if err := os.WriteFile(tsPath, []byte(legacyConfigTS), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture the legacy config BEFORE migrating — migrate renames the .ts
	// away, so this must happen first.
	want, err := config.LoadLegacyTS(dir)
	if err != nil {
		t.Fatalf("LoadLegacyTS (pre-migrate): %v", err)
	}

	code, out, errOut := runMigrate(t, []string{dir})
	if code != 0 {
		t.Fatalf("migrate exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}

	// rotor.toml written with the hosted #:schema directive on the first line.
	tomlData, err := os.ReadFile(filepath.Join(dir, "rotor.toml"))
	if err != nil {
		t.Fatalf("rotor.toml not written: %v", err)
	}
	if !strings.HasPrefix(string(tomlData), "#:schema https://") {
		t.Errorf("rotor.toml missing hosted #:schema directive:\n%s", tomlData)
	}

	// The legacy file is renamed to .bak (and no longer present).
	if fileExists(tsPath) {
		t.Error("rotor.config.ts should have been renamed away")
	}
	if !fileExists(tsPath + ".bak") {
		t.Error("rotor.config.ts.bak not created")
	}

	// Round-trip: the migrated rotor.toml loads to the same Config the legacy
	// path produced (ignoring the Warnings field, which is load-context).
	got, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load(rotor.toml): %v", err)
	}
	want.Warnings, got.Warnings = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
	if errs := got.Validate(); len(errs) != 0 {
		t.Errorf("migrated config does not validate: %v", errs)
	}
}

func TestMigrateNoLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := runMigrate(t, []string{dir})
	if code != 1 {
		t.Fatalf("migrate with no legacy config: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "no rotor.config.ts") {
		t.Fatalf("error should mention the missing legacy config:\n%s", errOut)
	}
}

func TestMigrateRefusesExistingToml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rotor.config.ts"), []byte(legacyConfigTS), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rotor.toml"), []byte("# existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --force: refuse, leave everything alone.
	code, _, errOut := runMigrate(t, []string{dir})
	if code != 1 {
		t.Fatalf("migrate over existing rotor.toml: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "already exists") {
		t.Fatalf("error should mention the existing rotor.toml:\n%s", errOut)
	}
	if got := mustReadFile(t, filepath.Join(dir, "rotor.toml")); got != "# existing\n" {
		t.Errorf("rotor.toml was overwritten without --force:\n%s", got)
	}

	// With --force: overwrite and migrate.
	code, _, errOut = runMigrate(t, []string{dir, "--force"})
	if code != 0 {
		t.Fatalf("migrate --force: exit %d\n%s", code, errOut)
	}
	if got := mustReadFile(t, filepath.Join(dir, "rotor.toml")); got == "# existing\n" {
		t.Error("rotor.toml should have been replaced with --force")
	}
}

func TestMigrateHelp(t *testing.T) {
	code, out, _ := runMigrate(t, []string{"-h"})
	if code != 0 {
		t.Fatalf("migrate -h: exit %d", code)
	}
	if !strings.Contains(out, "sloptor migrate") {
		t.Errorf("help missing usage:\n%s", out)
	}
}
