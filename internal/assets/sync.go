package assets

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"rotor/internal/cloud"
)

// SyncOptions configures Sync.
type SyncOptions struct {
	// Creator owns newly created assets. Exactly one of UserID/GroupID set.
	Creator cloud.Creator
	// OnFile, when non-nil, is called after each attempted upload — with the
	// final asset id on success, or err set on failure. Used by the CLI for
	// per-file progress lines.
	OnFile func(item PlanItem, assetID int64, err error)
}

// FileError is one failed upload within an otherwise-continuing batch.
type FileError struct {
	Path string
	Err  error
}

func (e FileError) Error() string { return e.Path + ": " + e.Err.Error() }

// SyncResult summarizes an executed plan.
type SyncResult struct {
	Created int
	Updated int
	Errors  []FileError
}

// Sync executes the plan against the cloud client: creates upload new assets,
// updates replace content on the existing asset id, unchanged items are
// skipped. The lockfile is updated and written to disk after EACH successful
// upload, so an aborted run resumes from where it stopped. Per-file failures
// (moderation, transient API errors) are collected in the result and do not
// abort the batch; a failed file keeps its previous lock entry. The returned
// error is reserved for fatal problems (a lockfile that cannot be written).
func Sync(ctx context.Context, client Cloud, projectDir string, plan *Plan, lock *Lockfile, opts SyncOptions) (*SyncResult, error) {
	res := &SyncResult{}
	for _, item := range plan.Items {
		if item.Action == ActionUnchanged {
			continue
		}
		assetID, err := uploadOne(ctx, client, projectDir, item, opts.Creator)
		if err != nil {
			res.Errors = append(res.Errors, FileError{Path: item.File.Path, Err: err})
			if opts.OnFile != nil {
				opts.OnFile(item, 0, err)
			}
			continue
		}
		lock.Assets[item.File.Path] = LockEntry{Hash: item.File.Hash, AssetID: assetID}
		if err := lock.Save(projectDir); err != nil {
			return res, err
		}
		if item.Action == ActionCreate {
			res.Created++
		} else {
			res.Updated++
		}
		if opts.OnFile != nil {
			opts.OnFile(item, assetID, nil)
		}
	}
	return res, nil
}

// UploadFile creates a new asset from absPath (a Decal or Audio, classified
// by extension), polls the upload operation to completion, and returns the
// final asset id. It is the single-file create path the $asset resolver uses
// on a cache miss; it shares CreateAsset + PollOperation with Sync. Files with
// an unknown extension are rejected.
func UploadFile(ctx context.Context, client Cloud, absPath string, creator cloud.Creator) (int64, error) {
	assetType, ok := classifyExt(path.Ext(absPath))
	if !ok {
		return 0, fmt.Errorf("assets: %s has an unsupported extension for upload", path.Base(filepath.ToSlash(absPath)))
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return 0, err
	}
	fileName := path.Base(filepath.ToSlash(absPath))
	req := cloud.CreateAssetRequest{
		AssetType:       string(assetType),
		DisplayName:     stem(fileName),
		Description:     "uploaded by rotor $asset",
		CreationContext: cloud.CreationContext{Creator: creator},
	}
	opPath, err := client.CreateAsset(ctx, req, fileName, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	var asset cloud.Asset
	if err := client.PollOperation(ctx, opPath, &asset); err != nil {
		return 0, err
	}
	if asset.AssetID == 0 {
		return 0, fmt.Errorf("assets: upload operation finished without an asset id")
	}
	if assetType == TypeDecal {
		return client.ResolveImageID(ctx, asset.AssetID)
	}
	return asset.AssetID, nil
}

// uploadOne performs a single create or update, polling the long-running
// operation to completion, and returns the final asset id.
func uploadOne(ctx context.Context, client Cloud, projectDir string, item PlanItem, creator cloud.Creator) (int64, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(item.File.Path)))
	if err != nil {
		return 0, err
	}
	fileName := path.Base(item.File.Path)

	var opPath string
	switch item.Action {
	case ActionCreate:
		req := cloud.CreateAssetRequest{
			AssetType:       string(item.File.Type),
			DisplayName:     stem(fileName),
			Description:     "uploaded by rotor asset sync",
			CreationContext: cloud.CreationContext{Creator: creator},
		}
		opPath, err = client.CreateAsset(ctx, req, fileName, bytes.NewReader(data))
	case ActionUpdate:
		opPath, err = client.UpdateAssetContent(ctx, item.AssetID, fileName, bytes.NewReader(data))
	default:
		return 0, fmt.Errorf("assets: cannot upload %s action", item.Action)
	}
	if err != nil {
		return 0, err
	}

	var asset cloud.Asset
	if err := client.PollOperation(ctx, opPath, &asset); err != nil {
		return 0, err
	}
	id := asset.AssetID
	if id == 0 {
		id = item.AssetID // content updates may omit the id in the response
	}
	if id == 0 {
		return 0, fmt.Errorf("assets: upload operation finished without an asset id")
	}
	// Image uploads create a Decal wrapper whose id does not render in image
	// properties; store the underlying Image id instead.
	if item.Action == ActionCreate && item.File.Type == TypeDecal {
		return client.ResolveImageID(ctx, id)
	}
	return id, nil
}
