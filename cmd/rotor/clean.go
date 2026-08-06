package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/compile"
)

// newCleanCommand removes a project's build outputs — the tsconfig outDir and
// the runtime-library include folder — and, with --types, the generated editor
// type companion in the project root (rotor.d.ts, plus the legacy
// rotor-env.d.ts / rotor-asset.d.ts / rotor-macros.d.ts / rotor-config.d.ts
// when present). It never touches source: only the resolved output/include
// directories and the named generated files are removed.
//
// Targets are resolved exactly the way `sloptor build` resolves them: the
// tsconfig is found with findTsConfigPath, the outDir read from that config
// (default "out"), and the include dir from the merged ProjectOptions
// (default "<project>/include"). With --dry-run nothing is deleted — every
// target is listed as a Planned row instead.
func newCleanCommand(streams cliStreams) *cobra.Command {
	var types, dryRun bool
	cmd := &cobra.Command{
		Use:                   "clean [path] [--types] [--dry-run]",
		Short:                 "remove build outputs; --types also removes generated editor types",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			path := "."
			if len(argv) > 0 {
				path = argv[0]
			}
			return runCleanCommand(streams, path, types, dryRun)
		},
	}
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &types, "types", "", false,
		"also remove generated editor types (rotor.d.ts and legacy companions)")
	addBoolFlag(cmd, &dryRun, "dry-run", "n", false, "list what would be removed without deleting anything")
	return cmd
}

func runCleanCommand(streams cliStreams, path string, types, dryRun bool) error {
	// Resolve the tsconfig the same way build does (file, dir, or upward
	// search). Without one there is nothing to clean deterministically.
	tsConfigPath, err := findTsConfigPath(path)
	if err != nil {
		return runtimeFailure(err)
	}
	dir := filepath.Dir(tsConfigPath)

	out := newUI(streams.out)
	out.banner("clean  " + filepath.Base(dir))
	start := time.Now()

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

	var events []uiEvent
	failed := false
	for _, target := range targets {
		n, present, err := cleanTarget(target, dryRun)
		if err != nil {
			events = append(events, uiEvent{
				Status: eventFailed,
				Target: relDisplay(dir, target),
				Detail: fmt.Sprintf("cannot remove: %v", err),
			})
			failed = true
			continue
		}
		if !present {
			continue
		}
		verb := "removed"
		if dryRun {
			verb = "would remove"
		}
		status := eventRemoved
		if dryRun {
			status = eventPlanned
		}
		events = append(events, uiEvent{Status: status, Target: relDisplay(dir, target), Detail: plural(n, "file") + " " + verb})
	}

	if len(events) == 0 {
		events = append(events, uiEvent{Status: eventUnchanged, Target: "nothing to clean"})
	}
	events = append(events, uiEvent{Status: eventFinished, Elapsed: time.Since(start)})
	out.events(events)
	fmt.Fprintln(streams.out)

	if failed {
		return reportedFailure(errors.New("clean failed"))
	}
	return nil
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
