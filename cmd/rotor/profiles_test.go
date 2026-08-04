package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProfilesStopReportsFinalizationErrors(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "closed.prof"))
	if err != nil {
		t.Fatal(err)
	}
	profiles := &buildProfiles{files: []*os.File{file}, blockFile: file}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := profiles.stop(); err == nil {
		t.Fatal("stop returned nil after profile destination was closed")
	}
}
