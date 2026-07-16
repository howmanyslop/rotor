package assets

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"rotor/internal/cloud"
)

// fakeCloud implements Cloud in-memory. Files whose names appear in failPoll
// fail at the operation-poll step (like a moderation rejection); names in
// failUpload fail the HTTP call itself.
type fakeCloud struct {
	nextID     int64
	failPoll   map[string]bool // fileName -> poll returns an API error
	failUpload map[string]bool // fileName -> create/update call errors

	pending     map[string]int64 // operation path -> asset id to return
	createdReqs []cloud.CreateAssetRequest
	createdName []string
	updatedIDs  []int64
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{nextID: 1000, pending: map[string]int64{}, failPoll: map[string]bool{}, failUpload: map[string]bool{}}
}

func (f *fakeCloud) CreateAsset(_ context.Context, req cloud.CreateAssetRequest, fileName string, file io.Reader) (string, error) {
	if f.failUpload[fileName] {
		return "", fmt.Errorf("fake: upload of %s refused", fileName)
	}
	if _, err := io.ReadAll(file); err != nil {
		return "", err
	}
	f.nextID++
	op := fmt.Sprintf("operations/create-%s", fileName)
	if f.failPoll[fileName] {
		f.pending[op] = -1
	} else {
		f.pending[op] = f.nextID
	}
	f.createdReqs = append(f.createdReqs, req)
	f.createdName = append(f.createdName, fileName)
	return op, nil
}

func (f *fakeCloud) UpdateAssetContent(_ context.Context, assetID int64, fileName string, file io.Reader) (string, error) {
	if f.failUpload[fileName] {
		return "", fmt.Errorf("fake: upload of %s refused", fileName)
	}
	if _, err := io.ReadAll(file); err != nil {
		return "", err
	}
	op := fmt.Sprintf("operations/update-%s", fileName)
	if f.failPoll[fileName] {
		f.pending[op] = -1
	} else {
		f.pending[op] = assetID
	}
	f.updatedIDs = append(f.updatedIDs, assetID)
	return op, nil
}

func (f *fakeCloud) PollOperation(_ context.Context, path string, into any) error {
	id, ok := f.pending[path]
	if !ok {
		return fmt.Errorf("fake: unknown operation %q", path)
	}
	if id == -1 {
		return &cloud.APIError{Code: "Moderated", Message: "content was moderated"}
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

func writeProjectFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCreatesUpdatesAndCollectsFailures(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, dir, "assets/a.png", "new file a")
	writeProjectFile(t, dir, "assets/bad.png", "fails moderation")
	writeProjectFile(t, dir, "assets/b.ogg", "changed audio")
	writeProjectFile(t, dir, "assets/c.png", "changed but fails")

	lock := NewLockfile()
	lock.Assets["assets/b.ogg"] = LockEntry{Hash: "sha256:old-b", AssetID: 50}
	lock.Assets["assets/c.png"] = LockEntry{Hash: "sha256:old-c", AssetID: 70}
	if err := lock.Save(dir); err != nil {
		t.Fatal(err)
	}

	scan, err := Scan(dir, []string{"assets/**"})
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(scan, lock)
	if plan.Count(ActionCreate) != 2 || plan.Count(ActionUpdate) != 2 {
		t.Fatalf("plan: %d creates, %d updates", plan.Count(ActionCreate), plan.Count(ActionUpdate))
	}

	fc := newFakeCloud()
	fc.failPoll["bad.png"] = true // moderation failure mid-batch
	fc.failUpload["c.png"] = true // transport failure on an update
	creator := cloud.Creator{GroupID: 777}

	var calls []string
	res, err := Sync(context.Background(), fc, dir, plan, lock, SyncOptions{
		Creator: creator,
		OnFile: func(item PlanItem, assetID int64, err error) {
			status := "ok"
			if err != nil {
				status = "err"
			}
			calls = append(calls, fmt.Sprintf("%s:%s", item.File.Path, status))
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Created != 1 || res.Updated != 1 || len(res.Errors) != 2 {
		t.Fatalf("result: %+v", res)
	}
	if len(calls) != 4 {
		t.Fatalf("OnFile calls: %v", calls)
	}

	// a.png created with a fresh id and the right request shape.
	entryA := lock.Assets["assets/a.png"]
	if entryA.AssetID < 1000 || entryA.Hash == "" {
		t.Fatalf("a.png entry: %+v", entryA)
	}
	if len(fc.createdReqs) == 0 || fc.createdReqs[0].AssetType != "Decal" ||
		fc.createdReqs[0].CreationContext.Creator.GroupID != 777 {
		t.Fatalf("create request: %+v", fc.createdReqs)
	}

	// b.ogg updated in place: same id, new hash.
	entryB := lock.Assets["assets/b.ogg"]
	if entryB.AssetID != 50 {
		t.Fatalf("update must keep the asset id, got %+v", entryB)
	}
	if entryB.Hash == "sha256:old-b" {
		t.Fatal("update must record the new hash")
	}
	if len(fc.updatedIDs) != 1 || fc.updatedIDs[0] != 50 {
		t.Fatalf("updated ids: %v", fc.updatedIDs)
	}

	// bad.png failed moderation: no lock entry.
	if _, ok := lock.Assets["assets/bad.png"]; ok {
		t.Fatal("failed create must not write a lock entry")
	}
	// c.png failed update: old entry preserved.
	if lock.Assets["assets/c.png"] != (LockEntry{Hash: "sha256:old-c", AssetID: 70}) {
		t.Fatalf("failed update must keep the old entry, got %+v", lock.Assets["assets/c.png"])
	}

	// The lockfile on disk reflects the successes (written after each one).
	onDisk, err := LoadLockfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Assets["assets/a.png"] != entryA || onDisk.Assets["assets/b.ogg"] != entryB {
		t.Fatalf("lockfile on disk out of date: %+v", onDisk.Assets)
	}
	if _, ok := onDisk.Assets["assets/bad.png"]; ok {
		t.Fatal("failed file leaked into the on-disk lockfile")
	}
}

func TestSyncSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeProjectFile(t, dir, "assets/same.png", "same content")
	scan, err := Scan(dir, []string{"assets/*.png"})
	if err != nil {
		t.Fatal(err)
	}
	lock := NewLockfile()
	lock.Assets["assets/same.png"] = LockEntry{Hash: scan.Files[0].Hash, AssetID: 9}

	fc := newFakeCloud()
	res, err := Sync(context.Background(), fc, dir, BuildPlan(scan, lock), lock, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 0 || res.Updated != 0 || len(res.Errors) != 0 {
		t.Fatalf("result: %+v", res)
	}
	if len(fc.createdName) != 0 || len(fc.updatedIDs) != 0 {
		t.Fatal("unchanged file must not touch the network")
	}
}
