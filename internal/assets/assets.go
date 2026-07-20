// Package assets implements `rotor asset sync`: an asphalt-style asset
// uploader driven by rotor.config.ts. It scans the project's asset globs,
// content-hashes each file, uploads new/changed files through the Open Cloud
// assets API, records results in a committed lockfile (rotor-lock.json), and
// generates typed accessor modules (assets.luau + assets.d.ts) from the
// lockfile.
//
// The lockfile is the source of truth: unchanged hashes never re-upload, and
// it is rewritten after every successful upload so an aborted run resumes
// where it left off. Per-file failures (e.g. audio moderation) are collected
// and reported; they never abort the rest of the batch, and the failed file
// keeps its previous lock entry.
package assets

import (
	"context"
	"io"
	"path"
	"strings"

	"rotor/internal/cloud"
)

// Cloud is the slice of the Open Cloud client that asset sync needs.
// *cloud.Client satisfies it; tests substitute a fake so no network is
// touched.
type Cloud interface {
	CreateAsset(ctx context.Context, req cloud.CreateAssetRequest, fileName string, file io.Reader) (operationPath string, err error)
	UpdateAssetContent(ctx context.Context, assetID int64, fileName string, file io.Reader) (operationPath string, err error)
	PollOperation(ctx context.Context, path string, into any) error
	ResolveImageID(ctx context.Context, decalID int64) (int64, error)
}

var _ Cloud = (*cloud.Client)(nil)

// AssetType is the Open Cloud assetType for an upload.
type AssetType string

const (
	TypeDecal AssetType = "Decal"
	TypeAudio AssetType = "Audio"
)

// classifyExt maps a lowercase file extension to its asset type. Unknown
// extensions are reported by Scan and skipped.
func classifyExt(ext string) (AssetType, bool) {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".tga", ".bmp":
		return TypeDecal, true
	case ".ogg", ".mp3":
		return TypeAudio, true
	}
	return "", false
}

// File is one scanned asset file.
type File struct {
	Path string    // project-relative, forward slashes (lockfile key)
	Abs  string    // absolute filesystem path
	Type AssetType // Decal or Audio
	Hash string    // "sha256:<hex>" of the file content
}

// stem returns the file name without its extension — the basis for generated
// identifiers and upload display names.
func stem(fileName string) string {
	base := path.Base(fileName)
	return strings.TrimSuffix(base, path.Ext(base))
}
