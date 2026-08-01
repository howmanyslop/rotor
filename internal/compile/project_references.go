package compile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func readProjectReferencePaths(tsConfigPath string) ([]string, bool, error) {
	data, err := os.ReadFile(tsConfigPath)
	if err != nil {
		return nil, false, err
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(stripJSONC(string(data))), &config); err != nil {
		return nil, false, fmt.Errorf("Failed to parse tsconfig at %s: %w", tsConfigPath, err)
	}
	references, _ := config["references"].([]any)
	paths := make([]string, 0, len(references))
	for _, reference := range references {
		entry, ok := reference.(map[string]any)
		if !ok {
			continue
		}
		path, ok := entry["path"].(string)
		if !ok {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(tsConfigPath), filepath.FromSlash(path))
		}
		if filepath.Ext(path) != ".json" {
			path = filepath.Join(path, "tsconfig.json")
		}
		paths = append(paths, filepath.Clean(path))
	}
	files, hasFiles := config["files"].([]any)
	_, hasInclude := config["include"]
	return paths, len(paths) > 0 && hasFiles && len(files) == 0 && !hasInclude, nil
}
