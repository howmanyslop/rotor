package forkparity

import (
	"strings"
	"testing"
)

func TestMatrixArtifactDigest(t *testing.T) {
	// Given
	path := "game/.rotor/cache/rojo/" + strings.Repeat("a", 64) + ".rojocache.json"
	contents := matrixNormalizeArtifactContents(path, []byte(`{"key":"/tmp/stage/game.project.json","mtimeManifest":[{"mtimeUnixNano":123,"path":"/tmp/stage/game.project.json"}],"value":"preserved"}`), "/tmp/stage")

	// When
	gotPath, gotDigest := matrixArtifactDigest(path, contents)

	// Then
	if gotPath != "game/.rotor/cache/rojo/<CACHE_KEY>.rojocache.json" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotDigest == "<normalized>" {
		t.Fatal("semantic artifact digest was masked")
	}
}

func TestMatrixArtifactNormalizationIgnoresEnvironment(t *testing.T) {
	// Given
	first := matrixNormalizeArtifactContents("out/main.luau.map", []byte(`{"version":3,"sources":["/tmp/one/src/main.ts"],"sourcesContent":["export const path = '\\\\value';"],"mappings":"AAAA"}`), "/tmp/one")
	second := matrixNormalizeArtifactContents("out/main.luau.map", []byte(`{"version":3,"sources":["C:\\tmp\\two\\src\\main.ts"],"sourcesContent":["export const path = '\\\\value';"],"mappings":"AAAA"}`), `C:\tmp\two`)

	// When
	_, firstDigest := matrixArtifactDigest("out/main.luau.map", first)
	_, secondDigest := matrixArtifactDigest("out/main.luau.map", second)

	// Then
	if firstDigest != secondDigest {
		t.Fatalf("environment-normalized digests differ: %s != %s", firstDigest, secondDigest)
	}
}

func TestMatrixArtifactDriftDetectsSemanticBytes(t *testing.T) {
	// Given
	sourceMapPath := "out/main.luau.map"
	copyFilesPath := "out/rbxts.copyfiles.json"
	rojoCachePath := "game/.rotor/cache/rojo/cache.rojocache.json"
	cleanSourceMap := matrixNormalizeArtifactContents(sourceMapPath, []byte(`{"sources":["/tmp/stage/src/main.ts"],"mappings":"AAAA"}`), "/tmp/stage")
	driftedSourceMap := matrixNormalizeArtifactContents(sourceMapPath, []byte(`{"sources":["/tmp/stage/src/main.ts"],"mappings":"AAAB"}`), "/tmp/stage")
	cleanCopyFiles := matrixNormalizeArtifactContents(copyFilesPath, []byte(`{"key":"/tmp/stage/out","files":[["/tmp/stage/src/main.ts","/tmp/stage/out/main.luau",1,2]],"version":2}`), "/tmp/stage")
	driftedCopyFiles := matrixNormalizeArtifactContents(copyFilesPath, []byte(`{"key":"/tmp/stage/out","files":[["/tmp/stage/src/other.ts","/tmp/stage/out/main.luau",3,4]],"version":2}`), "/tmp/stage")
	cleanRojoCache := matrixNormalizeArtifactContents(rojoCachePath, []byte(`{"key":"/tmp/stage/game.project.json","mtimeManifest":[{"mtimeUnixNano":1,"path":"/tmp/stage/src/main.ts"}],"state":{"rbxPaths":{"Main":"ReplicatedStorage.Main"}}}`), "/tmp/stage")
	driftedRojoCache := matrixNormalizeArtifactContents(rojoCachePath, []byte(`{"key":"/tmp/stage/game.project.json","mtimeManifest":[{"mtimeUnixNano":2,"path":"/tmp/stage/src/other.ts"}],"state":{"rbxPaths":{"Main":"ReplicatedStorage.Main"}}}`), "/tmp/stage")

	// When
	_, cleanMapDigest := matrixArtifactDigest(sourceMapPath, cleanSourceMap)
	_, driftedMapDigest := matrixArtifactDigest(sourceMapPath, driftedSourceMap)
	_, cleanCacheDigest := matrixArtifactDigest(copyFilesPath, cleanCopyFiles)
	_, driftedCacheDigest := matrixArtifactDigest(copyFilesPath, driftedCopyFiles)
	_, cleanRojoDigest := matrixArtifactDigest(rojoCachePath, cleanRojoCache)
	_, driftedRojoDigest := matrixArtifactDigest(rojoCachePath, driftedRojoCache)

	// Then
	if cleanMapDigest == driftedMapDigest {
		t.Fatal("source-map mapping drift was masked")
	}
	if cleanCacheDigest == driftedCacheDigest {
		t.Fatal("copy-files cache drift was masked")
	}
	if cleanRojoDigest == driftedRojoDigest {
		t.Fatal("Rojo cache drift was masked")
	}
}
