package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rotor/internal/compile"
)

// cmdClean removes a project's build outputs — the tsconfig outDir and the
// runtime-library include folder — and, with --types, the generated editor
// type companion in the project root (rotor.d.ts, plus the legacy
// rotor-env.d.ts / rotor-asset.d.ts / rotor-macros.d.ts / rotor-config.d.ts
// when present). It never touches source: only the resolved output/include
// directories and the named generated files are removed.
//
// Targets are resolved exactly the way `rotor build` resolves them: the
// tsconfig is found with findTsConfigPath, the outDir read from that config
// (default "out"), and the include dir from the merged ProjectOptions
// (default "<project>/include"). With --dry-run nothing is deleted — every
// target is listed instead.
func cmdClean(args []string) int {
	path := ""
	types := false
	dryRun := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			usage(os.Stdout)
			return 0
		case "--types":
			types = true
		case "--dry-run", "-n":
			dryRun = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "sloptor clean: unknown flag %q\n\n", a)
				usage(os.Stderr)
				return 1
			}
			if path != "" {
				fmt.Fprintf(os.Stderr, "sloptor clean: unexpected extra argument %q\n\n", a)
				usage(os.Stderr)
				return 1
			}
			path = a
		}
	}
	if path == "" {
		path = "."
	}

	// Resolve the tsconfig the same way build does (file, dir, or upward
	// search). Without one there is nothing to clean deterministically.
	tsConfigPath, err := findTsConfigPath(path)
	if err != nil {
		newUI(os.Stderr).failLine(fmt.Sprintf("sloptor clean: %v", err))
		return 1
	}
	dir := filepath.Dir(tsConfigPath)

	out := newUI(os.Stdout)
	out.banner("clean  " + filepath.Base(dir))

	// outDir from the tsconfig (raw single-file read, same JSONC strip the
	// rbxts-key reader uses); include dir from the merged ProjectOptions, via
	// the same helper the build watcher uses to prune the include tree.
	outDir := resolveOutDir(dir, tsConfigPath)
	opts := mergeProjectOptions(defaultProjectOptions, readRbxtsOptions(tsConfigPath), nil)
	includeDir := watchIncludeDir(dir, opts)

	targets := []string{outDir}
	if includeDir != "" {
		targets = append(targets, includeDir)
	}
	if types {
		// Generated editor companions in the project root: the consolidated
		// rotor.d.ts plus the legacy per-macro names (and rotor-config.d.ts),
		// removed only when present.
		for _, name := range []string{compile.RotorTypesFileName, compile.EnvDeclFileName, compile.AssetDeclFileName, compile.MacroDeclFileName, "rotor-config.d.ts"} {
			targets = append(targets, filepath.Join(dir, name))
		}
	}

	removed := 0
	failed := false
	for _, target := range targets {
		n, present, err := cleanTarget(target, dryRun)
		if err != nil {
			out.failLine(fmt.Sprintf("sloptor clean: cannot remove %s: %v", relDisplay(dir, target), err))
			failed = true
			continue
		}
		if !present {
			continue
		}
		removed++
		verb := "removed"
		if dryRun {
			verb = "would remove"
		}
		out.okLine(fmt.Sprintf("%s %s", verb, relDisplay(dir, target)), plural(n, "file"))
	}

	if failed {
		fmt.Fprintln(os.Stdout)
		return 1
	}
	if removed == 0 {
		out.noteLine("nothing to clean")
	}
	fmt.Fprintln(os.Stdout)
	return 0
}

// cleanTarget removes one file-or-directory target, returning the number of
// regular files it contained (or 1 for a single file) and whether it existed.
// With dryRun set it only counts, leaving the target on disk.
func cleanTarget(target string, dryRun bool) (count int, present bool, err error) {
	info, statErr := os.Stat(target)
	if statErr != nil {
		return 0, false, nil // absent → nothing to do (not an error)
	}
	count = countFiles(target, info)
	if dryRun {
		return count, true, nil
	}
	if err := os.RemoveAll(target); err != nil {
		return 0, true, err
	}
	return count, true, nil
}

// countFiles counts the regular files under target (1 when target is itself a
// file). Used only for the "(N files)" report line.
func countFiles(target string, info os.FileInfo) int {
	if !info.IsDir() {
		return 1
	}
	n := 0
	_ = filepath.WalkDir(target, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// resolveOutDir reads the tsconfig outDir (default "out"), resolved against the
// tsconfig directory — the same place program.Options().OutDir lands for a
// build. It is a raw single-file read (no `extends` following), mirroring
// readRbxtsOptions; StripJSONC lets the JSONC tsconfig parse.
func resolveOutDir(dir, tsConfigPath string) string {
	outDir := "out"
	if data, err := os.ReadFile(tsConfigPath); err == nil {
		var root struct {
			CompilerOptions struct {
				OutDir string `json:"outDir"`
			} `json:"compilerOptions"`
		}
		if json.Unmarshal([]byte(compile.StripJSONC(string(data))), &root) == nil &&
			root.CompilerOptions.OutDir != "" {
			outDir = root.CompilerOptions.OutDir
		}
	}
	if filepath.IsAbs(outDir) {
		return filepath.Clean(outDir)
	}
	return filepath.Join(dir, filepath.FromSlash(outDir))
}

// relDisplay renders target relative to the project dir for output, with a
// trailing slash for directories, falling back to the absolute path.
func relDisplay(dir, target string) string {
	rel, err := filepath.Rel(dir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = target
	}
	rel = filepath.ToSlash(rel)
	if info, err := os.Stat(target); err == nil && info.IsDir() && !strings.HasSuffix(rel, "/") {
		rel += "/"
	}
	return rel
}
