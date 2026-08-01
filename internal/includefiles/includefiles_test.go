package includefiles

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// wantNames is the exact compiler runtime payload.
var wantNames = []string{"Promise.lua", "RuntimeLib.lua"}

func TestNames(t *testing.T) {
	got := Names()
	if len(got) != len(wantNames) {
		t.Fatalf("Names() = %v, want %v", got, wantNames)
	}
	for i := range wantNames {
		if got[i] != wantNames[i] {
			t.Fatalf("Names() = %v, want %v", got, wantNames)
		}
	}
}

// TestEmbeddedMatchesVendored guards the drift hazard of the triple-vendoring
// (go:embed cannot reach the repo-root include/ directory, so this package
// carries a second copy; the upstream authority is the third, vendored under
// reference/): every embedded file must match BOTH the repo-root include/
// file and reference/roblox-ts/include/ byte-for-byte, and include/ must
// contain no .lua file the embed is missing. Pinning against the reference
// copy prevents the two rotor copies drifting together undetected.
func TestEmbeddedMatchesVendored(t *testing.T) {
	vendoredDir := filepath.Join("..", "..", "include")
	referenceDir := filepath.Join("..", "..", "reference", "roblox-ts", "include")

	for _, name := range Names() {
		embedded, err := Read(name)
		if err != nil {
			t.Fatalf("Read(%q): %v", name, err)
		}
		vendored, err := os.ReadFile(filepath.Join(vendoredDir, name))
		if err != nil {
			t.Fatalf("reading vendored %s: %v", name, err)
		}
		if !bytes.Equal(embedded, vendored) {
			t.Errorf("%s: embedded copy differs from include/%s — the vendored copies must stay byte-identical", name, name)
		}
		upstream, err := os.ReadFile(filepath.Join(referenceDir, name))
		if err != nil {
			t.Fatalf("reading reference %s: %v", name, err)
		}
		if !bytes.Equal(embedded, upstream) {
			t.Errorf("%s: embedded copy differs from reference/roblox-ts/include/%s — the runtime files must stay verbatim upstream", name, name)
		}
	}

	luaFiles, err := filepath.Glob(filepath.Join(vendoredDir, "*.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if len(luaFiles) != len(wantNames) {
		t.Errorf("include/ has %d .lua files, embed has %d — vendor both copies together", len(luaFiles), len(wantNames))
	}
}

func TestRuntimeLibAllowsSameRuntimeReload(t *testing.T) {
	runtimeLib, err := Read("RuntimeLib.lua")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(runtimeLib, []byte("TS.__runtimeScript = script")) {
		t.Fatal("RuntimeLib.lua does not identify reloads of the same runtime")
	}
	if !bytes.Contains(runtimeLib, []byte("registeredBy.__runtimeScript ~= script")) {
		t.Fatal("RuntimeLib.lua rejects modules registered by a reload of the same runtime")
	}
}

func TestIncludeFilesMatchFork(t *testing.T) {
	archive, err := zip.OpenReader(filepath.Join("..", "..", "roblox-ts.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()

	for _, name := range Names() {
		var forkFile *zip.File
		for _, file := range archive.File {
			if file.Name == "roblox-ts/include/"+name {
				forkFile = file
				break
			}
		}
		if forkFile == nil {
			t.Fatalf("fork archive is missing %s", name)
		}

		reader, err := forkFile.Open()
		if err != nil {
			t.Fatal(err)
		}
		forkBytes, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}

		embedded, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(embedded, forkBytes) {
			t.Errorf("%s differs from the fork archive", name)
		}
	}
}

// TestCopy exercises the fs.copySync port (copyInclude.ts L12-14): files land
// in a fresh directory, an existing directory is merged into (stale runtime
// files overwritten, unrelated files untouched).
func TestCopy(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "include")

	// Fresh directory: created, fully populated.
	if err := Copy(dest); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	for _, name := range wantNames {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("after Copy, reading %s: %v", name, err)
		}
		want, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: copied bytes differ from embedded bytes", name)
		}
	}

	// Existing directory: a stale RuntimeLib.lua is overwritten, an unrelated
	// user file survives (fs.copySync merges, never clears the destination).
	if err := os.WriteFile(filepath.Join(dest, "RuntimeLib.lua"), []byte("-- stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "user.lua"), []byte("-- mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Copy(dest); err != nil {
		t.Fatalf("Copy over existing dir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "RuntimeLib.lua"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := Read("RuntimeLib.lua")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("RuntimeLib.lua: stale file was not overwritten")
	}
	user, err := os.ReadFile(filepath.Join(dest, "user.lua"))
	if err != nil || string(user) != "-- mine\n" {
		t.Errorf("user.lua was disturbed (err=%v, content=%q)", err, user)
	}
}
