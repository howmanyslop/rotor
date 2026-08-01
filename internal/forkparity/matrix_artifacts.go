package forkparity

import (
	"regexp"
	"strings"
)

var (
	matrixRojoCacheArtifact = regexp.MustCompile(`(?i)[a-f0-9]{64}\.rojocache\.json$`)
	matrixVolatileArtifact  = regexp.MustCompile(`(?:\.luau\.map|rbxts\.copyfiles\.json)$`)
)

func matrixArtifactDigest(path string, contents []byte) (string, string) {
	path = matrixRojoCacheArtifact.ReplaceAllString(path, "<CACHE_KEY>.rojocache.json")
	if matrixArtifactVolatile(path) {
		return path, "<normalized>"
	}
	return path, digest(contents)
}

func matrixArtifactVolatile(path string) bool {
	return strings.HasSuffix(path, ".rojocache.json") ||
		matrixVolatileArtifact.MatchString(path)
}
