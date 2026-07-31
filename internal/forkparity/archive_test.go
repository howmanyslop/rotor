package forkparity

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestArchiveIdentity(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	zipPath, err := FindZip(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "roblox-ts.zip"); zipPath != want {
		t.Fatalf("FindZip() = %q, want %q", zipPath, want)
	}

	extractDir, cleanup, err := VerifyAndExtract(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	name, version, err := ReadPackageJSON(extractDir)
	if err != nil {
		t.Fatal(err)
	}
	if name != expectedPackageName {
		t.Fatalf("package name = %q, want %q", name, expectedPackageName)
	}
	if version != expectedPackageVer {
		t.Fatalf("package version = %q, want %q", version, expectedPackageVer)
	}
}

func TestArchiveRejectsTraversal(t *testing.T) {
	t.Parallel()

	zipPath := writeZip(t,
		zipTestEntry{name: "roblox-ts/", mode: os.ModeDir | 0o755},
		zipTestEntry{name: "roblox-ts/package.json", mode: 0o644, data: `{"name":"@isentinel/roblox-ts","version":"4.0.11"}`},
		zipTestEntry{name: "roblox-ts/../evil.txt", mode: 0o644, data: "evil"},
	)

	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := extractZipBytes(zipBytes); err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("extractZipBytes() error = %v, want parent traversal rejection", err)
	}
}

func TestArchiveRejectsSymlink(t *testing.T) {
	t.Parallel()

	zipPath := writeZip(t,
		zipTestEntry{name: "roblox-ts/", mode: os.ModeDir | 0o755},
		zipTestEntry{name: "roblox-ts/package.json", mode: 0o644, data: `{"name":"@isentinel/roblox-ts","version":"4.0.11"}`},
		zipTestEntry{name: "roblox-ts/link", mode: os.ModeSymlink | 0o777, data: "../../outside"},
	)

	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := extractZipBytes(zipBytes); err == nil || !strings.Contains(err.Error(), "symlinks are not allowed") {
		t.Fatalf("extractZipBytes() error = %v, want symlink rejection", err)
	}
}

func TestArchiveRejectsWrongDigest(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	zipPath, err := FindZip(root)
	if err != nil {
		t.Fatal(err)
	}

	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes[0] ^= 0xFF

	mutatedPath := filepath.Join(t.TempDir(), "roblox-ts.zip")
	if err := os.WriteFile(mutatedPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := VerifyAndExtract(mutatedPath); err == nil || !strings.Contains(err.Error(), "verify roblox-ts.zip digest") {
		t.Fatalf("VerifyAndExtract() error = %v, want digest rejection", err)
	}
}

func TestArchiveRejectsWrongPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pkgName string
		version string
	}{
		{name: "wrong name", pkgName: "not-roblox-ts", version: expectedPackageVer},
		{name: "wrong version", pkgName: expectedPackageName, version: "4.0.10"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			pkgDir := filepath.Join(root, "roblox-ts")
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}{Name: tt.pkgName, Version: tt.version})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), payload, 0o644); err != nil {
				t.Fatal(err)
			}

			if err := validatePackageIdentity(root); err == nil || !strings.Contains(err.Error(), "unexpected package identity") {
				t.Fatalf("validatePackageIdentity() error = %v, want package identity rejection", err)
			}
		})
	}
}

type zipTestEntry struct {
	name string
	mode os.FileMode
	data string
}

func writeZip(t *testing.T, entries ...zipTestEntry) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fixture.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		hdr := &zip.FileHeader{Name: entry.name}
		hdr.SetMode(entry.mode)
		if entry.mode&os.ModeSymlink != 0 {
			hdr.Method = zip.Store
		}
		w, err := writer.CreateHeader(hdr)
		if err != nil {
			_ = writer.Close()
			_ = file.Close()
			t.Fatal(err)
		}
		if entry.mode.IsDir() {
			continue
		}
		if _, err := w.Write([]byte(entry.data)); err != nil {
			_ = writer.Close()
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
