package forkparity

import (
	"encoding/json"
	"regexp"
	"strings"
)

var matrixRojoCacheArtifact = regexp.MustCompile(`(?i)[a-f0-9]{64}\.rojocache\.json$`)

func matrixNormalizeArtifacts(artifacts map[string][]byte, tempRoot string) map[string][]byte {
	normalized := make(map[string][]byte, len(artifacts))
	for path, contents := range artifacts {
		normalized[path] = matrixNormalizeArtifactContents(path, contents, tempRoot)
	}
	return normalized
}

func matrixArtifactDigest(path string, contents []byte) (string, string) {
	path = matrixRojoCacheArtifact.ReplaceAllString(path, "<CACHE_KEY>.rojocache.json")
	return path, digest(contents)
}

func matrixNormalizeArtifactContents(path string, contents []byte, tempRoot string) []byte {
	switch {
	case strings.HasSuffix(path, ".luau.map"):
		return matrixNormalizeSourceMap(contents, tempRoot)
	case strings.HasSuffix(path, "rbxts.copyfiles.json"):
		return matrixNormalizeCopyFilesManifest(contents, tempRoot)
	case strings.HasSuffix(path, ".rojocache.json"), strings.HasSuffix(path, ".rbxtsc.tsbuildinfo"):
		return matrixNormalizeCacheArtifact(contents, tempRoot)
	default:
		return contents
	}
}

func matrixNormalizeSourceMap(contents []byte, tempRoot string) []byte {
	var sourceMap map[string]any
	if json.Unmarshal(contents, &sourceMap) != nil {
		return contents
	}
	sources, ok := sourceMap["sources"].([]any)
	if !ok {
		return contents
	}
	for index, source := range sources {
		if value, ok := source.(string); ok {
			sources[index] = normalizePath(value, tempRoot)
		}
	}
	return matrixMarshalNormalizedJSON(contents, sourceMap)
}

func matrixNormalizeCopyFilesManifest(contents []byte, tempRoot string) []byte {
	var manifest map[string]any
	if json.Unmarshal(contents, &manifest) != nil {
		return contents
	}
	manifest["key"] = "<ROOT_KEY>"
	matrixNormalizeCopyEntries(manifest["files"], tempRoot, 2)
	matrixNormalizeCopyEntries(manifest["dirs"], tempRoot, 1)
	return matrixMarshalNormalizedJSON(contents, manifest)
}

func matrixNormalizeCopyEntries(value any, tempRoot string, timestampOffset int) {
	entries, ok := value.([]any)
	if !ok {
		return
	}
	for _, entry := range entries {
		fields, ok := entry.([]any)
		if !ok {
			continue
		}
		for index, field := range fields {
			if index >= timestampOffset {
				fields[index] = "<TIMESTAMP>"
				continue
			}
			if value, ok := field.(string); ok {
				fields[index] = normalizePath(value, tempRoot)
			}
		}
	}
}

func matrixNormalizeCacheArtifact(contents []byte, tempRoot string) []byte {
	var artifact any
	if json.Unmarshal(contents, &artifact) != nil {
		return contents
	}
	normalized := matrixNormalizeCacheValue(artifact, tempRoot, "")
	return matrixMarshalNormalizedJSON(contents, normalized)
}

func matrixNormalizeCacheValue(value any, tempRoot, fieldName string) any {
	switch value := value.(type) {
	case string:
		if fieldName == "key" || fieldName == "salt" {
			return "<ROOT_" + strings.ToUpper(fieldName) + ">"
		}
		return normalizePath(value, tempRoot)
	case []any:
		for index, item := range value {
			value[index] = matrixNormalizeCacheValue(item, tempRoot, fieldName)
		}
		return value
	case map[string]any:
		normalized := make(map[string]any, len(value))
		for key, item := range value {
			if matrixTimestampField(key) {
				normalized[normalizePath(key, tempRoot)] = "<TIMESTAMP>"
				continue
			}
			normalized[normalizePath(key, tempRoot)] = matrixNormalizeCacheValue(item, tempRoot, key)
		}
		return normalized
	default:
		return value
	}
}

func matrixTimestampField(name string) bool {
	name = strings.ToLower(name)
	return name == "mtime" || strings.HasSuffix(name, "mtimeunixnano") || strings.HasSuffix(name, "mtimems")
}

func matrixMarshalNormalizedJSON(original []byte, value any) []byte {
	normalized, err := json.Marshal(value)
	if err != nil {
		return original
	}
	return normalized
}
