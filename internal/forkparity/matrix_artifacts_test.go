package forkparity

import (
	"strings"
	"testing"
)

func TestMatrixArtifactDigest(t *testing.T) {
	// Given
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{
			name:     "Rojo cache",
			path:     "game/.rotor/cache/rojo/" + strings.Repeat("a", 64) + ".rojocache.json",
			wantPath: "game/.rotor/cache/rojo/<CACHE_KEY>.rojocache.json",
		},
		{name: "copy files cache", path: "out/rbxts.copyfiles.json", wantPath: "out/rbxts.copyfiles.json"},
		{name: "source map", path: "out/init.luau.map", wantPath: "out/init.luau.map"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			gotPath, gotDigest := matrixArtifactDigest(tt.path, []byte("nondeterministic bytes"))

			// Then
			if gotPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotDigest != "<normalized>" {
				t.Fatalf("digest = %q", gotDigest)
			}
		})
	}
}
