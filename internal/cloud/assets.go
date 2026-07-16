package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Assets API (https://create.roblox.com/docs/cloud/reference — assets v1).
const (
	assetCreatePath    = "/assets/v1/assets"
	assetUpdatePathFmt = "/assets/v1/assets/%d"
)

// CreateAssetRequest is the `request` JSON part of the multipart upload.
// Open Cloud serializes int64 ids as JSON strings (proto3 mapping), hence
// the `,string` tags on Creator.
type CreateAssetRequest struct {
	AssetType       string          `json:"assetType"` // "Decal", "Audio", "Model", ...
	DisplayName     string          `json:"displayName"`
	Description     string          `json:"description,omitempty"`
	CreationContext CreationContext `json:"creationContext"`
}

// CreationContext names who owns the new asset. Exactly one of
// Creator.UserID / Creator.GroupID should be set.
type CreationContext struct {
	Creator       Creator `json:"creator"`
	ExpectedPrice int64   `json:"expectedPrice,omitempty"`
}

type Creator struct {
	UserID  int64 `json:"userId,omitempty,string"`
	GroupID int64 `json:"groupId,omitempty,string"`
}

// Asset is the operation `response` for a finished asset upload — pass
// &Asset{} to PollOperation after CreateAsset/UpdateAssetContent.
type Asset struct {
	AssetID     int64  `json:"assetId,string"`
	AssetType   string `json:"assetType"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Path        string `json:"path"`
	RevisionID  string `json:"revisionId"`
}

// CreateAsset uploads a new asset (POST /assets/v1/assets) as
// multipart/form-data with a `request` JSON part and a `fileContent` part,
// returning the long-running operation path to feed PollOperation.
//
// The file is buffered in memory so the transport can retry on 429/5xx;
// rotor's assets are icons/decals/audio, small enough for that to be fine.
func (c *Client) CreateAsset(ctx context.Context, req CreateAssetRequest, fileName string, file io.Reader) (operationPath string, err error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	body, contentType, err := assetMultipart(reqJSON, fileName, file)
	if err != nil {
		return "", err
	}
	var op operation
	if err := c.do(ctx, "POST", assetCreatePath, nil, contentType, body, &op); err != nil {
		return "", err
	}
	return op.Path, nil
}

// updateAssetRequest is the minimal `request` part for a content-only
// update; the asset id is repeated in the body per the API reference.
type updateAssetRequest struct {
	AssetID int64 `json:"assetId,string"`
}

// UpdateAssetContent replaces an existing asset's content (PATCH
// /assets/v1/assets/{assetId}), returning the operation path. Same
// multipart shape and in-memory buffering as CreateAsset.
func (c *Client) UpdateAssetContent(ctx context.Context, assetID int64, fileName string, file io.Reader) (operationPath string, err error) {
	reqJSON, err := json.Marshal(updateAssetRequest{AssetID: assetID})
	if err != nil {
		return "", err
	}
	body, contentType, err := assetMultipart(reqJSON, fileName, file)
	if err != nil {
		return "", err
	}
	var op operation
	if err := c.do(ctx, "PATCH", fmt.Sprintf(assetUpdatePathFmt, assetID), nil, contentType, body, &op); err != nil {
		return "", err
	}
	return op.Path, nil
}

// assetMultipart assembles the two-part body the assets API expects:
// `request` (JSON) and `fileContent` (the file bytes, with a Content-Type
// guessed from the extension — the API rejects parts without one).
func assetMultipart(reqJSON []byte, fileName string, file io.Reader) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	reqHeader := textproto.MIMEHeader{}
	reqHeader.Set("Content-Disposition", `form-data; name="request"`)
	reqHeader.Set("Content-Type", "application/json")
	part, err := w.CreatePart(reqHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(reqJSON); err != nil {
		return nil, "", err
	}

	fileHeader := textproto.MIMEHeader{}
	fileHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="fileContent"; filename=%q`, fileName))
	fileHeader.Set("Content-Type", fileContentType(fileName))
	part, err = w.CreatePart(fileHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// assetDeliveryPathFmt locates an asset's content through the asset delivery
// API (same apis.roblox.com host; the key needs the legacy-assets scope).
const assetDeliveryPathFmt = "/asset-delivery-api/v1/assetId/%d"

// decalImageIDRe extracts the underlying Image id from a Decal's content XML
// (`<url>http://www.roblox.com/asset/?id=NNN</url>`).
var decalImageIDRe = regexp.MustCompile(`\?id=(\d+)`)

// ResolveImageID resolves the Image asset id wrapped by a Decal. Uploading an
// image through the assets API creates a Decal, but `rbxassetid://<decal id>`
// does not render in image properties (ImageLabel.Image, MeshPart textures,
// ...) — those need the underlying Image asset the decal points at. The decal
// content (an XML pointer fetched via the asset delivery API) names that id.
//
// The delivery lookup requires the API key to carry the legacy-assets scope
// in addition to assets read/write; a 403 here almost always means that scope
// is missing.
func (c *Client) ResolveImageID(ctx context.Context, decalID int64) (int64, error) {
	var loc struct {
		Location string `json:"location"`
	}
	if err := c.do(ctx, "GET", fmt.Sprintf(assetDeliveryPathFmt, decalID), nil, "", nil, &loc); err != nil {
		if apiErr, ok := err.(*APIError); ok && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
			return 0, fmt.Errorf("cloud: resolving decal %d to its image: %w (the API key likely lacks the legacy-assets scope — add it at https://create.roblox.com/dashboard/credentials)", decalID, err)
		}
		return 0, fmt.Errorf("cloud: resolving decal %d to its image: %w", decalID, err)
	}
	if loc.Location == "" {
		return 0, fmt.Errorf("cloud: asset delivery returned no location for decal %d", decalID)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", loc.Location, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("cloud: fetching decal %d content: HTTP %d", decalID, resp.StatusCode)
	}
	m := decalImageIDRe.FindSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("cloud: decal %d content has no image asset reference", decalID)
	}
	id, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cloud: decal %d content has an unparseable image id: %w", decalID, err)
	}
	return id, nil
}

// fileContentType maps an upload's extension to a MIME type, defaulting to
// octet-stream. TGA is special-cased: it's an accepted decal source but
// absent from the stdlib MIME table on most platforms.
func fileContentType(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".tga" {
		return "image/tga"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}
