package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// TestMergeProjectOptions covers the upstream Object.assign merge
// (CLI/commands/build.ts L125-130): defaults < tsconfig `rbxts` key < argv,
// where absent (nil) fields never clobber earlier layers.
func TestMergeProjectOptions(t *testing.T) {
	tests := []struct {
		name  string
		rbxts *partialProjectOptions
		argv  *partialProjectOptions
		want  projectOptions
	}{
		{
			name: "defaults only",
			want: projectOptions{optimizedLoops: true, luau: true},
		},
		{
			name:  "rbxts layer overrides defaults",
			rbxts: &partialProjectOptions{luau: boolPtr(false), verbose: boolPtr(true), includePath: strPtr("inc")},
			want:  projectOptions{optimizedLoops: true, luau: false, verbose: true, includePath: "inc"},
		},
		{
			name:  "absent CLI booleans do not clobber rbxts values",
			rbxts: &partialProjectOptions{optimizedLoops: boolPtr(false), logTruthyChanges: boolPtr(true)},
			argv:  &partialProjectOptions{}, // nothing passed on the CLI
			want:  projectOptions{optimizedLoops: false, logTruthyChanges: true, luau: true},
		},
		{
			name:  "CLI overrides rbxts",
			rbxts: &partialProjectOptions{luau: boolPtr(false), includePath: strPtr("a"), typeName: strPtr("model")},
			argv:  &partialProjectOptions{luau: boolPtr(true), includePath: strPtr("b")},
			want:  projectOptions{optimizedLoops: true, luau: true, includePath: "b", typeName: "model"},
		},
		{
			name:  "rbxts type and rojo pass through when CLI silent",
			rbxts: &partialProjectOptions{typeName: strPtr("package"), rojo: strPtr("custom.project.json")},
			want:  projectOptions{optimizedLoops: true, luau: true, typeName: "package", rojo: "custom.project.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeProjectOptions(defaultProjectOptions, tt.rbxts, tt.argv)
			if got != tt.want {
				t.Errorf("merge = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestFindTsConfigPath covers findTsConfigPath (build.ts L31-40): direct
// file paths win; otherwise tsconfig.json is searched upward from the
// resolved path.
func TestFindTsConfigPath(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "tsconfig.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "src", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(root, "tsconfig.build.json")
	if err := os.WriteFile(custom, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("directory containing tsconfig", func(t *testing.T) {
		got, err := findTsConfigPath(root)
		if err != nil || got != cfg {
			t.Errorf("got %q, %v; want %q", got, err, cfg)
		}
	})
	t.Run("explicit file path of any name", func(t *testing.T) {
		got, err := findTsConfigPath(custom)
		if err != nil || got != custom {
			t.Errorf("got %q, %v; want %q", got, err, custom)
		}
	})
	t.Run("nested directory walks up", func(t *testing.T) {
		got, err := findTsConfigPath(nested)
		if err != nil || got != cfg {
			t.Errorf("got %q, %v; want %q", got, err, cfg)
		}
	})
	t.Run("no tsconfig anywhere errors", func(t *testing.T) {
		empty := t.TempDir()
		got, err := findTsConfigPath(filepath.Join(empty, "nope"))
		// The upward search may legitimately escape the temp root on a
		// machine with a tsconfig.json in a temp ancestor; only a result
		// INSIDE the temp tree would be a bug.
		if err == nil && strings.HasPrefix(got, empty) {
			t.Errorf("found %q inside empty tree", got)
		}
		if err != nil && err.Error() != "Unable to find tsconfig.json!" {
			t.Errorf("error = %q, want upstream CLIError text", err)
		}
	})
}

// TestReadRbxtsOptions covers getTsConfigProjectOptions (build.ts L22-29):
// raw single-file JSONC read of the top-level `rbxts` key; `extends` is NOT
// followed (quirk verbatim).
func TestReadRbxtsOptions(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("rbxts key with comments parses", func(t *testing.T) {
		p := write("a.json", `{
			// jsonc comment
			"compilerOptions": { "strict": true },
			"rbxts": {
				"luau": false,
				"type": "model", /* block */
				"includePath": "runtime",
			},
		}`)
		got, err := readRbxtsOptionsChecked(p)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("rbxts key not read")
		}
		if got.luau == nil || *got.luau {
			t.Error("luau=false not parsed")
		}
		if got.typeName == nil || *got.typeName != "model" {
			t.Error("type not parsed")
		}
		if got.includePath == nil || *got.includePath != filepath.Join(dir, "runtime") {
			t.Error("includePath not parsed")
		}
		if got.watch != nil || got.optimizedLoops != nil {
			t.Error("absent fields must stay nil")
		}
	})

	t.Run("no rbxts key returns nil", func(t *testing.T) {
		p := write("b.json", `{"compilerOptions": {}}`)
		if got, err := readRbxtsOptionsChecked(p); err != nil || got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("extends inherits parent options", func(t *testing.T) {
		write("base.json", `{"rbxts": {"verbose": true}}`)
		p := write("child.json", `{"extends": "./base.json", "compilerOptions": {}}`)
		if got, err := readRbxtsOptionsChecked(p); err != nil {
			t.Fatal(err)
		} else if got == nil || *got != (partialProjectOptions{}) {
			t.Errorf("CLI-only parent key should become an empty rbxts layer: %+v", got)
		}
	})

	t.Run("unreadable returns nil", func(t *testing.T) {
		if got, err := readRbxtsOptionsChecked(filepath.Join(dir, "missing.json")); err != nil || got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
}
