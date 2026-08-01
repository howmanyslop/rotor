package rojo

import (
	"path/filepath"
	"reflect"
	"testing"
)

// j joins path segments with the platform separator, mirroring how upstream's
// Node path module would build the expected strings on the host OS.
func j(parts ...string) string {
	return filepath.Join(parts...)
}

func newTestTranslator(useLuau bool) *PathTranslator {
	return NewPathTranslator(j("proj", "src"), j("proj", "out"), "", false, useLuau)
}

func TestGetOutputPath(t *testing.T) {
	pt := newTestTranslator(true)
	tests := []struct {
		name string
		in   string
		want string
	}{
		// .ts -> .luau, rebased rootDir -> outDir (PathTranslator.ts L64-80).
		{"ts file", j("proj", "src", "foo.ts"), j("proj", "out", "foo.luau")},
		{"tsx file", j("proj", "src", "foo.tsx"), j("proj", "out", "foo.luau")},
		{"nested ts", j("proj", "src", "a", "b.ts"), j("proj", "out", "a", "b.luau")},
		// index -> init rename.
		{"index", j("proj", "src", "index.ts"), j("proj", "out", "init.luau")},
		{"nested index", j("proj", "src", "a", "index.ts"), j("proj", "out", "a", "init.luau")},
		// sub-extensions are preserved (exts form a stack; only .ts(x) pops).
		{"server subext", j("proj", "src", "main.server.ts"), j("proj", "out", "main.server.luau")},
		{"spec subext", j("proj", "src", "foo.spec.ts"), j("proj", "out", "foo.spec.luau")},
		// .d.ts is NOT renamed (extsPeek(1) === D_EXT check) — only rebased.
		{"d.ts skipped", j("proj", "src", "foo.d.ts"), j("proj", "out", "foo.d.ts")},
		{"d.tsx skipped", j("proj", "src", "foo.d.tsx"), j("proj", "out", "foo.d.tsx")},
		// Non-ts files only rebase.
		{"json passthrough", j("proj", "src", "data.json"), j("proj", "out", "data.json")},
		{"luau passthrough", j("proj", "src", "raw.luau"), j("proj", "out", "raw.luau")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pt.GetOutputPath(tt.in); got != tt.want {
				t.Errorf("GetOutputPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewPathTranslatorAddsRbxtscBuildInfoInfix(t *testing.T) {
	path := j("proj", "out", "tsconfig.tsbuildinfo")
	pt := NewPathTranslator(j("proj", "src"), j("proj", "out"), path, false, true)
	if want := j("proj", "out", "tsconfig.rbxtsc.tsbuildinfo"); pt.BuildInfoOutputPath != want {
		t.Fatalf("BuildInfoOutputPath = %q, want %q", pt.BuildInfoOutputPath, want)
	}
	pt = NewPathTranslator(j("proj", "src"), j("proj", "out"), pt.BuildInfoOutputPath, false, true)
	if want := j("proj", "out", "tsconfig.rbxtsc.tsbuildinfo"); pt.BuildInfoOutputPath != want {
		t.Fatalf("idempotent BuildInfoOutputPath = %q, want %q", pt.BuildInfoOutputPath, want)
	}
}

func TestGetOutputPathLuaExtension(t *testing.T) {
	// useLuauExtension=false -> .lua (PathTranslator.ts L50-52).
	pt := newTestTranslator(false)
	got := pt.GetOutputPath(j("proj", "src", "foo.ts"))
	want := j("proj", "out", "foo.lua")
	if got != want {
		t.Errorf("GetOutputPath = %q, want %q", got, want)
	}
}

func TestGetImportPath(t *testing.T) {
	pt := newTestTranslator(true)
	tests := []struct {
		name string
		in   string
		want string
	}{
		// Same as output path for plain .ts.
		{"ts file", j("proj", "src", "foo.ts"), j("proj", "out", "foo.luau")},
		{"index", j("proj", "src", "index.ts"), j("proj", "out", "init.luau")},
		// Unlike getOutputPath, .d is popped beneath .ts(x) (PathTranslator.ts L202-206).
		{"d.ts maps", j("proj", "src", "foo.d.ts"), j("proj", "out", "foo.luau")},
		{"index.d.ts maps", j("proj", "src", "index.d.ts"), j("proj", "out", "init.luau")},
		// Other sub-extensions are preserved.
		{"spec.d.ts", j("proj", "src", "foo.spec.d.ts"), j("proj", "out", "foo.spec.luau")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pt.GetImportPath(tt.in, false); got != tt.want {
				t.Errorf("GetImportPath(%q, false) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestGetImportPathNodeModule(t *testing.T) {
	// isNodeModule=true: result stays SIBLING of the input (no rootDir->outDir
	// rebase) — node_modules d.ts files were already remapped to the shipped
	// .lua location (PathTranslator.ts L216-218). Use a rootDir that CONTAINS
	// node_modules so a buggy rebase would visibly move the path into outDir.
	pt := NewPathTranslator(j("proj"), j("proj", "out"), "", false, true)
	in := j("proj", "node_modules", "@rbxts", "pkg", "src", "index.d.ts")
	want := j("proj", "node_modules", "@rbxts", "pkg", "src", "init.luau")
	if got := pt.GetImportPath(in, true); got != want {
		t.Errorf("GetImportPath(%q, true) = %q, want %q", in, got, want)
	}
	// The same path WITH rebasing lands inside outDir (isNodeModule=false).
	rebased := j("proj", "out", "node_modules", "@rbxts", "pkg", "src", "init.luau")
	if got := pt.GetImportPath(in, false); got != rebased {
		t.Errorf("GetImportPath(%q, false) = %q, want %q", in, got, rebased)
	}
	// Non-ts input joins unchanged.
	in2 := j("proj", "node_modules", "@rbxts", "pkg", "main.lua")
	if got := pt.GetImportPath(in2, true); got != in2 {
		t.Errorf("GetImportPath(%q, true) = %q, want unchanged", in2, got)
	}
}

func TestGetOutputDeclarationPath(t *testing.T) {
	pt := newTestTranslator(true)
	tests := []struct {
		in   string
		want string
	}{
		{j("proj", "src", "foo.ts"), j("proj", "out", "foo.d.ts")},
		{j("proj", "src", "foo.tsx"), j("proj", "out", "foo.d.ts")},
		// Existing .d.ts only rebases.
		{j("proj", "src", "foo.d.ts"), j("proj", "out", "foo.d.ts")},
	}
	for _, tt := range tests {
		if got := pt.GetOutputDeclarationPath(tt.in); got != tt.want {
			t.Errorf("GetOutputDeclarationPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetOutputTransformedPath(t *testing.T) {
	pt := newTestTranslator(true)
	tests := []struct {
		in   string
		want string
	}{
		// .transformed inserted before the final ext (PathTranslator.ts L104-118).
		{j("proj", "src", "foo.ts"), j("proj", "out", "foo.transformed.ts")},
		// .d.ts: inserted before .d.
		{j("proj", "src", "foo.d.ts"), j("proj", "out", "foo.transformed.d.ts")},
	}
	for _, tt := range tests {
		if got := pt.GetOutputTransformedPath(tt.in); got != tt.want {
			t.Errorf("GetOutputTransformedPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGetInputPaths(t *testing.T) {
	pt := newTestTranslator(true)

	// foo.luau -> foo.ts, foo.tsx, foo.luau (identity last).
	got := pt.GetInputPaths(j("proj", "out", "foo.luau"))
	want := []string{
		j("proj", "src", "foo.ts"),
		j("proj", "src", "foo.tsx"),
		j("proj", "src", "foo.luau"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetInputPaths(foo.luau) = %v, want %v", got, want)
	}

	// init.luau additionally maps back to index.ts(x).
	got = pt.GetInputPaths(j("proj", "out", "init.luau"))
	want = []string{
		j("proj", "src", "init.ts"),
		j("proj", "src", "init.tsx"),
		j("proj", "src", "index.ts"),
		j("proj", "src", "index.tsx"),
		j("proj", "src", "init.luau"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetInputPaths(init.luau) = %v, want %v", got, want)
	}

	// index.luau cannot come from a .ts file: identity only.
	got = pt.GetInputPaths(j("proj", "out", "index.luau"))
	want = []string{j("proj", "src", "index.luau")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetInputPaths(index.luau) = %v, want %v", got, want)
	}

	// .d.ts reverse mapping only when declaration=true.
	dpt := NewPathTranslator(j("proj", "src"), j("proj", "out"), "", true, true)
	got = dpt.GetInputPaths(j("proj", "out", "foo.d.ts"))
	want = []string{
		j("proj", "src", "foo.ts"),
		j("proj", "src", "foo.tsx"),
		j("proj", "src", "foo.d.ts"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetInputPaths(foo.d.ts, declaration) = %v, want %v", got, want)
	}
}
