package compile

import (
	"rotor/tsgo/bundled"
	"rotor/tsgo/vfs"
	"rotor/tsgo/vfs/cachedvfs"
	"rotor/tsgo/vfs/wrapvfs"
)

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
