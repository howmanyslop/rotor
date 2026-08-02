package forkparity

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	committedZipDigest  = "a0f097baefaa02ae411f18ea19a150c0076bfd2adbc6645dcef02e2c162cea5a"
	expectedPackageName = "@isentinel/roblox-ts"
	expectedPackageVer  = "4.0.11"
)

func FindZip(repoRoot string) (string, error) {
	zipPath := filepath.Join(repoRoot, "roblox-ts.zip")
	info, err := os.Stat(zipPath)
	if err != nil {
		return "", fmt.Errorf("stat roblox-ts.zip: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("roblox-ts.zip is a directory: %s", zipPath)
	}
	return zipPath, nil
}

func VerifyAndExtract(zipPath string) (string, func(), error) {
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		return "", nil, fmt.Errorf("read roblox-ts.zip: %w", err)
	}
	sum := sha256.Sum256(zipBytes)
	if got := hex.EncodeToString(sum[:]); got != committedZipDigest {
		return "", nil, fmt.Errorf("verify roblox-ts.zip digest: got %s, want %s", got, committedZipDigest)
	}

	extractDir, cleanup, err := extractZipBytes(zipBytes)
	if err != nil {
		return "", nil, err
	}
	if err := validatePackageIdentity(extractDir); err != nil {
		cleanup()
		return "", nil, err
	}
	return extractDir, cleanup, nil
}

func ReadPackageJSON(extractDir string) (name string, version string, err error) {
	data, err := os.ReadFile(filepath.Join(extractDir, "roblox-ts", "package.json"))
	if err != nil {
		return "", "", fmt.Errorf("read package.json: %w", err)
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", "", fmt.Errorf("parse package.json: %w", err)
	}
	return pkg.Name, pkg.Version, nil
}

func extractZipBytes(zipBytes []byte) (string, func(), error) {
	extractDir, err := os.MkdirTemp("", "forkparity-archive-*")
	if err != nil {
		return "", nil, fmt.Errorf("create extract dir: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(extractDir)
	}

	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("open zip: %w", err)
	}

	for _, file := range reader.File {
		if err := extractZipEntry(extractDir, file); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	return extractDir, cleanup, nil
}

func extractZipEntry(extractDir string, file *zip.File) error {
	name := file.Name
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\\`) {
		return fmt.Errorf("reject zip entry %q: absolute paths are not allowed", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("reject zip entry %q: parent traversal is not allowed", name)
		}
	}
	if file.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("reject zip entry %q: symlinks are not allowed", name)
	}

	targetPath := filepath.Join(extractDir, filepath.FromSlash(name))
	rel, err := filepath.Rel(extractDir, targetPath)
	if err != nil {
		return fmt.Errorf("resolve zip entry %q: %w", name, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("reject zip entry %q: escapes extraction directory", name)
	}

	if file.FileInfo().IsDir() {
		if err := os.MkdirAll(targetPath, file.Mode().Perm()); err != nil {
			return fmt.Errorf("create directory %q: %w", name, err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create parent for %q: %w", name, err)
	}

	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create file %q: %w", name, err)
	}
	defer out.Close()

	in, err := file.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %q: %w", name, err)
	}
	defer in.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy zip entry %q: %w", name, err)
	}
	return nil
}

func validatePackageIdentity(extractDir string) error {
	name, version, err := ReadPackageJSON(extractDir)
	if err != nil {
		return err
	}
	if name != expectedPackageName || version != expectedPackageVer {
		return fmt.Errorf("unexpected package identity %s@%s, want %s@%s", name, version, expectedPackageName, expectedPackageVer)
	}
	return nil
}
