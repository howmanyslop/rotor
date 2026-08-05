package compile

import (
	"rotor/tsgo/bundled"
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
