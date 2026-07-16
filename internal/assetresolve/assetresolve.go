// Package assetresolve implements the build-time resolver behind rotor's
// $asset compile-time macro (internal/transformer/assetmacro.go). It maps an
// asset path referenced by `$asset("path")` to a Roblox asset id, backed by
// the committed lockfile (rotor-lock.json) so a cache-hit build is fully
// offline and deterministic, with optional auto-upload via Open Cloud on a
// genuine cache miss when a client is configured.
//
// Separation of concerns: the transformer imports only the AssetResolver
// interface (transformer.AssetResolver) and calls Resolve. This package owns
// all filesystem/cloud/lockfile knowledge, so the transformer never imports
// internal/cloud, internal/assets, or os. Resolve may upload (network) on a
// miss, but it never PERSISTS the lockfile — the compile pipeline flushes
// rotor-lock.json after a successful build (see Dirty/Entries), keeping all
// disk writes out of the transform pass.
package assetresolve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"rotor/internal/assets"
	"rotor/internal/cloud"
	"rotor/internal/transformer"
)

// Resolver resolves $asset paths to asset ids for one compile pass. It is
// safe for concurrent use (CompileProject transforms files in parallel).
type Resolver struct {
	projectDir string        // absolute project directory (lockfile root)
	base       string        // project-relative prefix for non-relative paths ("" = project root)
	creator    cloud.Creator // owner for uploaded assets; zero when unset
	client     assets.Cloud  // nil when offline / no ROBLOX_API_KEY
	hasCreator bool          // a usable creator was configured

	mu sync.Mutex
	// lock holds the loaded lockfile entries (keyed by project-relative
	// forward-slash path). New uploads are recorded here and into added.
	lock *assets.Lockfile
	// byPath memoizes resolved ids by ABSOLUTE file path within this pass, so
	// the same file referenced N times uploads at most once.
	byPath map[string]string
	// added records lockfile keys created this pass (the resolver is "dirty"
	// when non-empty), so the pipeline knows to flush rotor-lock.json.
	added map[string]struct{}
}

var _ transformer.AssetResolver = (*Resolver)(nil)

// Options configures New.
type Options struct {
	// ProjectDir is the project root; the lockfile and project-relative paths
	// are resolved against it.
	ProjectDir string
	// Base is a project-relative directory ([assets].base) prepended to
	// non-relative $asset paths. `./`-relative references and lockfile keys
	// are unaffected. Empty means paths resolve from the project root.
	Base string
	// Lockfile is the loaded rotor-lock.json (use assets.LoadLockfile). May be
	// nil — an empty lockfile is used.
	Lockfile *assets.Lockfile
	// Client is the Open Cloud client for cache-miss uploads, or nil to stay
	// offline (a miss then errors with transformer.ErrAssetNotCached).
	Client assets.Cloud
	// Creator owns newly uploaded assets. When its id is zero (or Client is
	// nil) the resolver never uploads.
	Creator cloud.Creator
}

// New constructs a Resolver. A nil Client (or a zero-id Creator) disables
// auto-upload: cache misses then surface transformer.ErrAssetNotCached.
func New(opts Options) *Resolver {
	lock := opts.Lockfile
	if lock == nil {
		lock = assets.NewLockfile()
	}
	dir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		dir = opts.ProjectDir
	}
	return &Resolver{
		projectDir: filepath.ToSlash(dir),
		base:       strings.Trim(filepath.ToSlash(opts.Base), "/"),
		creator:    opts.Creator,
		client:     opts.Client,
		hasCreator: opts.Creator.UserID != 0 || opts.Creator.GroupID != 0,
		lock:       lock,
		byPath:     map[string]string{},
		added:      map[string]struct{}{},
	}
}

// Resolve maps path (as written in `$asset("path")`) to a Roblox asset id.
//
//   - A path beginning with "./" or "../" is resolved relative to importerPath
//     (the source file containing the call); otherwise it is resolved from the
//     project root, under [assets].base when one is configured.
//   - Missing file -> transformer.ErrAssetFileNotFound.
//   - The content hash is looked up in the in-memory cache, then in the
//     lockfile (hit -> reuse id, fully offline).
//   - On a miss with a client + creator -> upload, record a new lock entry,
//     mark dirty, return the id.
//   - On a miss without a client/creator -> transformer.ErrAssetNotCached.
func (r *Resolver) Resolve(importerPath, path string) (string, error) {
	absPath, err := r.resolvePath(importerPath, path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("%w: %s", transformer.ErrAssetFileNotFound, path)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if id, ok := r.byPath[absPath]; ok {
		return id, nil
	}

	hash, err := assets.HashFile(absPath)
	if err != nil {
		return "", err
	}

	relKey := r.lockfileKey(absPath)

	// Lockfile hit: an entry for this path whose hash matches reuses the id.
	if entry, ok := r.lock.Assets[relKey]; ok && entry.Hash == hash {
		id := assetIDString(entry.AssetID)
		r.byPath[absPath] = id
		return id, nil
	}

	// Cache miss. Upload when a client and creator are configured; otherwise
	// the id cannot be produced offline.
	if r.client == nil || !r.hasCreator {
		return "", fmt.Errorf("%w: %s", transformer.ErrAssetNotCached, path)
	}

	assetID, err := assets.UploadFile(context.Background(), r.client, absPath, r.creator)
	if err != nil {
		return "", err
	}
	r.lock.Assets[relKey] = assets.LockEntry{Hash: hash, AssetID: assetID}
	r.added[relKey] = struct{}{}
	id := assetIDString(assetID)
	r.byPath[absPath] = id
	return id, nil
}

// resolvePath turns the macro argument into an absolute filesystem path.
func (r *Resolver) resolvePath(importerPath, path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("%w: (empty path)", transformer.ErrAssetFileNotFound)
	}
	slash := filepath.ToSlash(p)
	var abs string
	if strings.HasPrefix(slash, "./") || strings.HasPrefix(slash, "../") {
		base := r.projectDir
		if importerPath != "" {
			base = filepath.ToSlash(filepath.Dir(filepath.FromSlash(importerPath)))
		}
		abs = filepath.Join(filepath.FromSlash(base), filepath.FromSlash(slash))
	} else {
		abs = filepath.Join(filepath.FromSlash(r.projectDir), filepath.FromSlash(r.base), filepath.FromSlash(slash))
	}
	return abs, nil
}

// lockfileKey returns the project-relative forward-slash key for absPath
// (matching the keys assets.Scan/Sync write). Files outside the project keep
// their absolute slash path as a stable key.
func (r *Resolver) lockfileKey(absPath string) string {
	rel, err := filepath.Rel(filepath.FromSlash(r.projectDir), absPath)
	if err != nil {
		return filepath.ToSlash(absPath)
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return filepath.ToSlash(absPath)
	}
	return rel
}

// Dirty reports whether Resolve added any new lockfile entries this pass. The
// pipeline persists rotor-lock.json (Lockfile().Save) only when true.
func (r *Resolver) Dirty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.added) > 0
}

// Lockfile returns the (possibly updated) lockfile so the pipeline can persist
// it after a successful build.
func (r *Resolver) Lockfile() *assets.Lockfile {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lock
}

// Entries returns the project-relative keys of the entries added this pass,
// for logging/reporting.
func (r *Resolver) Entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.added))
	for k := range r.added {
		out = append(out, k)
	}
	return out
}

// assetIDString renders a numeric asset id as the bare string the macro wraps
// (`rbxassetid://` + this).
func assetIDString(id int64) string {
	return fmt.Sprintf("%d", id)
}
