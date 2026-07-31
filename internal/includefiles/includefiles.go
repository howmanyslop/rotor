// Package includefiles embeds the roblox-ts runtime library — the files
// upstream ships in its package's include/ directory (Shared/constants.ts L5:
// INCLUDE_PATH = <package root>/include) — so rotor.exe stays a standalone
// single binary.
//
// The .lua files in this directory are byte-for-byte copies of
// reference/roblox-ts/include/, vendored a second time here
// because go:embed cannot reference files above the package directory; the
// human-facing copy lives in the repo-root include/ directory per the design
// doc. TestEmbeddedMatchesVendored fails if the copies drift.
package includefiles

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
)

// files holds the runtime library. Upstream's include/ directory contains
// exactly RuntimeLib.lua and Promise.lua; the patterns are listed explicitly
// so an accidental extra file here is a compile error, not silent payload.
//
//go:embed RuntimeLib.lua Promise.lua
var files embed.FS

// Names returns the embedded file names in sorted order.
func Names() []string {
	entries, err := files.ReadDir(".")
	if err != nil {
		panic("includefiles: " + err.Error()) // embed.FS root always readable
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// Read returns the contents of one embedded runtime file.
func Read(name string) ([]byte, error) {
	return files.ReadFile(name)
}

// Copy ports Project/functions/copyInclude.ts L12-14's
// fs.copySync(INCLUDE_PATH, includePath, { dereference: true }): write every
// runtime file into includePath, creating the directory if needed and
// overwriting same-named files. Like fs.copySync, it merges into an existing
// directory — extra files the user put there are left alone. (dereference
// only matters for symlinks inside the source tree; the embedded FS has
// none.) The gating — --noInclude and package projects skip the copy
// (copyInclude.ts L7-11) — is the caller's responsibility.
func Copy(includePath string) error {
	if err := os.MkdirAll(includePath, 0o755); err != nil {
		return err
	}
	for _, name := range Names() {
		data, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(includePath, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
