package compile

import (
	"os"
	"path/filepath"
	"testing"

	"rotor/internal/rojo"
)

func TestResolvePackageExports(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		wantTypes   string
		wantRuntime string
	}{
		{
			name:        "uses types and the first runtime condition",
			manifest:    `{"exports":{".":{"types":"./dist/index.d.ts","import":"./dist/import.luau","require":"./dist/require.luau","default":"./dist/default.luau"}}}`,
			wantTypes:   "dist/index.d.ts",
			wantRuntime: "dist/import.luau",
		},
		{
			name:        "uses an adjacent declaration when exports omits types",
			manifest:    `{"exports":"./dist/init.luau"}`,
			wantTypes:   "dist/init.d.ts",
			wantRuntime: "dist/init.luau",
		},
		{
			name:        "falls back to legacy types before typings and main",
			manifest:    `{"types":"./types/index.d.ts","typings":"./typings/index.d.ts","main":"./out/init.lua"}`,
			wantTypes:   "types/index.d.ts",
			wantRuntime: "out/init.lua",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkgDir := writePackageManifest(t, t.TempDir(), "pkg", tt.manifest)

			typesPath, runtimePath, err := resolvePackageExports(pkgDir, nil)

			if err != nil {
				t.Fatalf("resolvePackageExports: %v", err)
			}
			if want := filepath.Join(pkgDir, filepath.FromSlash(tt.wantTypes)); typesPath != want {
				t.Errorf("types path = %q, want %q", typesPath, want)
			}
			if want := filepath.Join(pkgDir, filepath.FromSlash(tt.wantRuntime)); runtimePath != want {
				t.Errorf("runtime path = %q, want %q", runtimePath, want)
			}
		})
	}
}

func TestPackageExportsMapping(t *testing.T) {
	t.Run("maps subpaths and custom condition targets", func(t *testing.T) {
		scopePath := filepath.Join(t.TempDir(), "@rbxts")
		pkgDir := writePackageManifest(t, scopePath, "pkg", `{
			"exports": {
				".": {
					"types": "./dist/index.d.ts",
					"source": "./src/index.ts",
					"import": "./dist/init.luau"
				},
				"./utils": {
					"types": "./dist/utils.d.ts",
					"require": "./dist/utils.luau"
				},
				"./features/*": {
					"types": "./dist/features/*.d.ts",
					"import": "./dist/features/*.luau"
				}
			}
		}`)

		mapping := createNodeModulesPathMapping([]string{scopePath}, true, []string{"source"})

		assertPackageMapping(t, mapping, filepath.Join(pkgDir, "dist", "index.d.ts"), filepath.Join(pkgDir, "dist", "init.luau"))
		assertPackageMapping(t, mapping, filepath.Join(pkgDir, "src", "index.ts"), filepath.Join(pkgDir, "dist", "init.luau"))
		assertPackageMapping(t, mapping, filepath.Join(pkgDir, "dist", "utils.d.ts"), filepath.Join(pkgDir, "dist", "utils.luau"))
		if _, exists := mapping[rojo.CanonicalFileName(filepath.Join(pkgDir, "dist", "features", "*.d.ts"), true)]; exists {
			t.Error("wildcard export subpath should not create a mapping")
		}
	})

	t.Run("remaps runtime through a package rojo path", func(t *testing.T) {
		scopePath := filepath.Join(t.TempDir(), "@rbxts")
		pkgDir := writePackageManifest(t, scopePath, "pkg", `{
			"exports": {
				".": {"types":"./dist/index.d.ts","import":"./dist/init.luau"}
			}
		}`)
		writePackageFile(t, pkgDir, "default.project.json", `{"name":"pkg","tree":{"$path":"out"}}`)

		mapping := createNodeModulesPathMapping([]string{scopePath}, true, nil)

		assertPackageMapping(t, mapping, filepath.Join(pkgDir, "dist", "index.d.ts"), filepath.Join(pkgDir, "out", "init.luau"))
	})

	t.Run("adds a real path key for a symlinked package", func(t *testing.T) {
		dir := t.TempDir()
		realPkgDir := writePackageManifest(t, filepath.Join(dir, "store"), "pkg", `{
			"exports": {
				".": {"types":"./dist/index.d.ts","import":"./dist/init.luau"}
			}
		}`)
		scopePath := filepath.Join(dir, "node_modules", "@rbxts")
		virtualPkgDir := filepath.Join(scopePath, "pkg")
		if err := os.MkdirAll(scopePath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPkgDir, virtualPkgDir); err != nil {
			t.Skipf("cannot create package symlink: %v", err)
		}

		mapping := createNodeModulesPathMapping([]string{scopePath}, true, nil)

		assertPackageMapping(t, mapping, filepath.Join(virtualPkgDir, "dist", "index.d.ts"), filepath.Join(virtualPkgDir, "dist", "init.luau"))
		assertPackageMapping(t, mapping, filepath.Join(packageRealPath(realPkgDir), "dist", "index.d.ts"), filepath.Join(virtualPkgDir, "dist", "init.luau"))
	})
}

func writePackageManifest(t *testing.T, scopePath, name, manifest string) string {
	t.Helper()
	pkgDir := filepath.Join(scopePath, name)
	writePackageFile(t, pkgDir, "package.json", manifest)
	return pkgDir
}

func writePackageFile(t *testing.T, pkgDir, name, contents string) {
	t.Helper()
	path := filepath.Join(pkgDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertPackageMapping(t *testing.T, mapping map[string]string, typesPath, runtimePath string) {
	t.Helper()
	key := rojo.CanonicalFileName(typesPath, true)
	if got := mapping[key]; got != runtimePath {
		t.Errorf("mapping[%q] = %q, want %q", key, got, runtimePath)
	}
}
