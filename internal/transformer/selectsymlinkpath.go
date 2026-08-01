package transformer

import (
	"path/filepath"
	"strings"
)

func selectSymlinkPath(symlinkPaths []string, nodeModulesPath string) string {
	if nodeModulesPath != "" {
		for _, symlinkPath := range symlinkPaths {
			relative, err := filepath.Rel(nodeModulesPath, symlinkPath)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return symlinkPath
			}
		}
	}
	if len(symlinkPaths) > 0 {
		return symlinkPaths[0]
	}
	return ""
}
