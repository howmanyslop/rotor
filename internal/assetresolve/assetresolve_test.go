package assetresolve

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"rotor/internal/assets"
	"rotor/internal/cloud"
	"rotor/internal/transformer"
)

// fakeCloud is a minimal assets.Cloud that records create calls and returns a
// deterministic id from PollOperation — no network.
type fakeCloud struct {
	createCalls int
	nextID      int64
	lastCreator cloud.Creator
	pending     map[string]int64
}

func newFakeCloud(firstID int64) *fakeCloud {
	return &fakeCloud{nextID: firstID, pending: map[string]int64{}}
}

func (f *fakeCloud) CreateAsset(_ context.Context, req cloud.CreateAssetRequest, fileName string, file io.Reader) (string, error) {
	if _, err := io.ReadAll(file); err != nil {
		return "", err
	}
	f.createCalls++
	f.lastCreator = req.CreationContext.Creator
	op := "operations/create-" + fileName
	f.pending[op] = f.nextID
	f.nextID++
	return op, nil
}

func (f *fakeCloud) UpdateAssetContent(_ context.Context, assetID int64, fileName string, file io.Reader) (string, error) {
	if _, err := io.ReadAll(file); err != nil {
		return "", err
	}
	op := "operations/update-" + fileName
	f.pending[op] = assetID
	return op, nil
}

func (f *fakeCloud) PollOperation(_ context.Context, path string, into any) error {
	id, ok := f.pending[path]
	if !ok {
		return errors.New("fake: unknown operation " + path)
	}
	if a, ok := into.(*cloud.Asset); ok && a != nil {
		a.AssetID = id
	}
	return nil
}

// ResolveImageID mimics the decal→image unwrap with a recognizable offset so
// tests can assert the unwrapped id (not the decal id) was recorded.
func (f *fakeCloud) ResolveImageID(_ context.Context, decalID int64) (int64, error) {
	return decalID + 500000, nil
}

func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A cache hit (lockfile entry whose hash matches) returns the id offline and
// never touches the (nil) client, never marks dirty.
func TestResolveCacheHitOffline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "assets/logo.png", "logo-bytes")
	hash, err := assets.HashFile(filepath.Join(dir, "assets", "logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	lock := assets.NewLockfile()
	lock.Assets["assets/logo.png"] = assets.LockEntry{Hash: hash, AssetID: 42}

	r := New(Options{ProjectDir: dir, Lockfile: lock}) // no client

	id, err := r.Resolve("", "assets/logo.png")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id != "42" {
		t.Errorf("id = %q, want %q", id, "42")
	}
	if r.Dirty() {
		t.Error("a cache hit must not mark the resolver dirty")
	}
}

// The upload-on-miss path: a file with no lockfile entry, with a client +
// creator, uploads exactly once, records a new lock entry keyed by content
// hash, marks the resolver dirty, and dedups repeat references.
func TestResolveUploadsOnMissAndRecordsEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "assets/new.png", "brand-new-bytes")
	hash, err := assets.HashFile(filepath.Join(dir, "assets", "new.png"))
	if err != nil {
		t.Fatal(err)
	}

	fc := newFakeCloud(7000)
	creator := cloud.Creator{GroupID: 555}
	r := New(Options{ProjectDir: dir, Client: fc, Creator: creator})

	id, err := r.Resolve("", "assets/new.png")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 507000 = the fake's decal id (7000) unwrapped to its Image id.
	if id != "507000" {
		t.Errorf("id = %q, want %q", id, "507000")
	}
	if !r.Dirty() {
		t.Error("an upload-on-miss must mark the resolver dirty")
	}

	// Repeat reference: in-memory cache → no second upload.
	if _, err := r.Resolve("", "assets/new.png"); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if fc.createCalls != 1 {
		t.Errorf("createCalls = %d, want 1 (same asset must upload once)", fc.createCalls)
	}
	if fc.lastCreator != creator {
		t.Errorf("upload creator = %+v, want %+v", fc.lastCreator, creator)
	}

	// The new entry is recorded in the (in-memory) lockfile keyed by hash.
	entry, ok := r.Lockfile().Assets["assets/new.png"]
	if !ok {
		t.Fatal("upload did not record a lockfile entry")
	}
	if entry.Hash != hash || entry.AssetID != 507000 {
		t.Errorf("entry = %+v, want hash=%s id=507000", entry, hash)
	}
	if got := r.Entries(); len(got) != 1 || got[0] != "assets/new.png" {
		t.Errorf("Entries() = %v, want [assets/new.png]", got)
	}

	// And the lockfile persists to disk via Save (the pipeline's step).
	if err := r.Lockfile().Save(dir); err != nil {
		t.Fatal(err)
	}
	onDisk, err := assets.LoadLockfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Assets["assets/new.png"] != entry {
		t.Errorf("on-disk entry = %+v, want %+v", onDisk.Assets["assets/new.png"], entry)
	}
}

// A miss with no client surfaces transformer.ErrAssetNotCached (the offline
// diagnostic the transformer maps to rotorAssetNotCached).
func TestResolveMissOfflineIsNotCached(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "assets/uncached.png", "bytes")

	r := New(Options{ProjectDir: dir}) // no client/creator

	_, err := r.Resolve("", "assets/uncached.png")
	if !errors.Is(err, transformer.ErrAssetNotCached) {
		t.Fatalf("err = %v, want ErrAssetNotCached", err)
	}
}

// A missing file surfaces transformer.ErrAssetFileNotFound.
func TestResolveMissingFile(t *testing.T) {
	dir := t.TempDir()
	r := New(Options{ProjectDir: dir})

	_, err := r.Resolve("", "assets/does-not-exist.png")
	if !errors.Is(err, transformer.ErrAssetFileNotFound) {
		t.Fatalf("err = %v, want ErrAssetFileNotFound", err)
	}
}

// A "./"-relative path resolves against the importing file's directory, not
// the project root.
func TestResolveRelativeToImporter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/ui/icon.png", "icon-bytes")
	hash, err := assets.HashFile(filepath.Join(dir, "src", "ui", "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	lock := assets.NewLockfile()
	lock.Assets["src/ui/icon.png"] = assets.LockEntry{Hash: hash, AssetID: 99}

	r := New(Options{ProjectDir: dir, Lockfile: lock})

	importer := filepath.ToSlash(filepath.Join(dir, "src", "ui", "panel.ts"))
	id, err := r.Resolve(importer, "./icon.png")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id != "99" {
		t.Errorf("id = %q, want 99", id)
	}
}

// [assets].base prefixes non-relative macro paths (project root + base),
// while ./-relative references and lockfile keys are unaffected.
func TestResolveWithBase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "assets/images/ui/icon.png", "icon-bytes")
	hash, err := assets.HashFile(filepath.Join(dir, "assets", "images", "ui", "icon.png"))
	if err != nil {
		t.Fatal(err)
	}

	lock := assets.NewLockfile()
	lock.Assets["assets/images/ui/icon.png"] = assets.LockEntry{Hash: hash, AssetID: 4242}
	r := New(Options{ProjectDir: dir, Base: "assets/images", Lockfile: lock})

	// The macro path omits the base but hits the lockfile's project-relative key.
	id, err := r.Resolve("", "ui/icon.png")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id != "4242" {
		t.Errorf("id = %q, want %q", id, "4242")
	}
	if r.Dirty() {
		t.Error("a cache hit must not mark the resolver dirty")
	}

	// ./-relative references bypass the base entirely.
	importer := filepath.ToSlash(filepath.Join(dir, "src", "app.ts"))
	if _, err := r.Resolve(importer, "./missing.png"); err == nil {
		t.Error("a ./-relative miss must not resolve under base")
	}

	// New uploads under base still record project-relative lockfile keys.
	writeFile(t, dir, "assets/images/ui/new.png", "new-bytes")
	fc := newFakeCloud(9000)
	r2 := New(Options{ProjectDir: dir, Base: "assets/images", Client: fc, Creator: cloud.Creator{UserID: 1}})
	if _, err := r2.Resolve("", "ui/new.png"); err != nil {
		t.Fatalf("Resolve upload: %v", err)
	}
	if _, ok := r2.Lockfile().Assets["assets/images/ui/new.png"]; !ok {
		t.Errorf("lockfile keys must stay project-relative; got %v", r2.Entries())
	}
}
