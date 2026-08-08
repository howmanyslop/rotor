package compile

import (
	"fmt"
	"sort"
	"strings"

	"rotor/tsgo/bundled"
	"rotor/tsgo/compiler"
	"rotor/tsgo/vfs"
	"rotor/tsgo/vfs/cachedvfs"
	"rotor/tsgo/vfs/osvfs"
	"rotor/tsgo/vfs/wrapvfs"
)

// normalizeOverlays rekeys a caller-supplied overlay map into the form
// newOverlayFS looks up: slash-normalized, and lowercased on case-insensitive
// filesystems. Callers pass ordinary absolute paths and stay unaware of it.
func normalizeOverlays(overlays map[string]string) map[string]string {
	caseSensitive := osvfs.FS().UseCaseSensitiveFileNames()
	out := make(map[string]string, len(overlays))
	for path, text := range overlays {
		out[normalizeOverlayPath(path, caseSensitive)] = text
	}
	return out
}

// matchOverlaysToProgram counts the overlay keys that name a source file the
// program actually holds, and rejects the run outright if any names nothing.
//
// Silence is the failure mode this exists to prevent. An overlay FS only
// overrides FileExists and ReadFile, so a key the program never asks about —
// a relative path, a typo, a file outside the tsconfig include set — changes
// nothing and yields a clean report on the UNMODIFIED tree. A consumer
// comparing a before and an after cannot tell that apart from a real pass.
func matchOverlaysToProgram(program *compiler.Program, overlays map[string]string) (int, error) {
	matched, unmatched := overlayKeysInProgram(program, overlays)
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		return 0, fmt.Errorf("compile: overlay matches no file in the program: %s", strings.Join(unmatched, ", "))
	}
	return len(matched), nil
}

// matchSolutionOverlaysToProgram is matchOverlaysToProgram for one project of a
// solution census. A solution's files are split between its projects, so "every
// overlay must match" is only answerable against the union of all of them: this
// records what one project matched, and the caller's solutionOverlayMatches
// checks the union once, after every project has run.
func matchSolutionOverlaysToProgram(program *compiler.Program, overlays map[string]string, tracker *solutionOverlayMatches) (int, error) {
	matched, _ := overlayKeysInProgram(program, overlays)
	tracker.record(matched)
	return len(matched), nil
}

// overlayKeysInProgram splits the overlay keys into those naming a source file
// the program holds (as a normalized set, so a caller can union them across
// projects) and those naming nothing (in their original spelling, for the error
// message).
func overlayKeysInProgram(program *compiler.Program, overlays map[string]string) (matched map[string]struct{}, unmatched []string) {
	caseSensitive := osvfs.FS().UseCaseSensitiveFileNames()
	inProgram := make(map[string]struct{}, len(program.SourceFiles()))
	for _, sourceFile := range program.SourceFiles() {
		inProgram[normalizeOverlayPath(sourceFile.FileName(), caseSensitive)] = struct{}{}
	}

	matched = make(map[string]struct{}, len(overlays))
	for path := range overlays {
		normalized := normalizeOverlayPath(path, caseSensitive)
		if _, ok := inProgram[normalized]; !ok {
			unmatched = append(unmatched, path)
			continue
		}
		matched[normalized] = struct{}{}
	}
	return matched, unmatched
}

func newOverlayFS(rawBase vfs.FS, configPath string, overlays map[string]string) vfs.FS {
	baseFS := cachedvfs.From(SanitizeFSWithConfigPath(bundled.WrapFS(rawBase), configPath))
	caseSensitive := baseFS.UseCaseSensitiveFileNames()
	return wrapvfs.Wrap(baseFS, wrapvfs.Replacements{
		FileExists: func(path string) bool {
			if _, ok := overlays[normalizeOverlayPath(path, caseSensitive)]; ok {
				return true
			}
			return baseFS.FileExists(path)
		},
		ReadFile: func(path string) (string, bool) {
			if text, ok := overlays[normalizeOverlayPath(path, caseSensitive)]; ok {
				return text, true
			}
			return baseFS.ReadFile(path)
		},
	})
}
