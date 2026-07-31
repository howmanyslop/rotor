package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rotor/internal/rojo"
)

func TestBuildProjectOutputPipeline(t *testing.T) {
	dir := writeProject(t, "@scope/output-fixture", "")
	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfigBytes, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	tsconfig := strings.Replace(string(tsconfigBytes), `"outDir": "out"`, `"outDir": "out",
		"incremental": true,
		"tsBuildInfoFile": "out/cache.tsbuildinfo"`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "src", "data.json"), []byte("{\"ok\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "script.luau"), []byte("print(\"copied\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "types.d.ts"), []byte("declare const TAG: string;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(outDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{
		filepath.Join(outDir, "stale.luau"):         "-- stale\n",
		filepath.Join(outDir, "nested", "old.json"): "{}\n",
		filepath.Join(outDir, ".git", "keep"):       "keep\n",
		filepath.Join(outDir, "cache.tsbuildinfo"):  "{}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if result == nil {
		t.Fatal("nil result")
	}

	for _, rel := range []string{"out/main.luau", "out/data.json", "out/script.luau"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s missing after build: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "types.d.ts")); !os.IsNotExist(err) {
		t.Fatalf("out/types.d.ts err = %v, want not-exist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "stale.luau")); !os.IsNotExist(err) {
		t.Fatalf("stale output err = %v, want removed", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "nested", "old.json")); !os.IsNotExist(err) {
		t.Fatalf("nested orphan err = %v, want removed", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", ".git", "keep")); err != nil {
		t.Fatalf(".git sentinel missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "cache.tsbuildinfo")); err != nil {
		t.Fatalf("build info missing: %v", err)
	}
	if len(result.EmittedFiles) != 1 || filepath.Base(result.EmittedFiles[0]) != "main.luau" {
		t.Fatalf("EmittedFiles = %v, want only compiled main.luau", result.EmittedFiles)
	}
}

func TestBuildProjectWriteOnlyChangedSkipsUnchangedOutputs(t *testing.T) {
	dir := writeProject(t, "@scope/write-only-fixture", "")
	if err := os.WriteFile(filepath.Join(dir, "src", "data.json"), []byte("{\"same\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := ProjectOptions{WriteOnlyChanged: true}
	result, diags, err := BuildProjectWithOptions(dir, opts)
	if err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("first build diagnostics: %v", diags)
	}
	if len(result.EmittedFiles) != 1 {
		t.Fatalf("first build EmittedFiles = %v, want 1 compiled file", result.EmittedFiles)
	}

	old := time.Unix(100, 0)
	mainOut := filepath.Join(dir, "out", "main.luau")
	jsonOut := filepath.Join(dir, "out", "data.json")
	for _, path := range []string{mainOut, jsonOut} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	result, diags, err = BuildProjectWithOptions(dir, opts)
	if err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("second build diagnostics: %v", diags)
	}
	if len(result.EmittedFiles) != 0 {
		t.Fatalf("second build EmittedFiles = %v, want none", result.EmittedFiles)
	}
	for _, path := range []string{mainOut, jsonOut} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Fatalf("%s modtime = %v, want preserved %v", path, info.ModTime(), old)
		}
	}
}

func TestBuildProjectLuaExtension(t *testing.T) {
	dir := writeProject(t, "@scope/lua-ext-fixture", "")

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{LuaExtension: true})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.lua")); err != nil {
		t.Fatalf("out/main.lua missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.luau")); !os.IsNotExist(err) {
		t.Fatalf("out/main.luau err = %v, want not-exist", err)
	}
}

func TestBuildProjectEmitsDeclarationsForPackage(t *testing.T) {
	dir := writeProject(t, "@scope/declaration-fixture", "")

	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfigBytes, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	tsconfig := strings.Replace(string(tsconfigBytes), `"outDir": "out"`, `"outDir": "out",
		"declaration": true,
		"types": ["types"]`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "@rbxts", "types"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "@rbxts", "types", "package.json"), []byte("{\"name\":\"@rbxts/types\",\"types\":\"index.d.ts\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "@rbxts", "types", "index.d.ts"), []byte("interface TypesBox {\n\tmarker: string;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := "export interface Box {\n\tvalue: TypesBox;\n}\nexport const value = undefined as unknown as TypesBox;\n"
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if result == nil {
		t.Fatal("nil result")
	}

	declPath := filepath.Join(dir, "out", "main.d.ts")
	declBytes, err := os.ReadFile(declPath)
	if err != nil {
		t.Fatalf("read declaration output: %v", err)
	}
	declText := string(declBytes)
	if !strings.Contains(declText, "export interface Box") || !strings.Contains(declText, "export declare const value: TypesBox;") {
		t.Fatalf("unexpected declaration output:\n%s", declText)
	}

	if len(result.EmittedFiles) != 2 {
		t.Fatalf("EmittedFiles = %v, want compiled file + declaration", result.EmittedFiles)
	}
}

func TestBuildResultCarriesStructuredDiagnostics(t *testing.T) {
	res, msgs, err := BuildProjectWithOptions("testdata/env_diag_model", ProjectOptions{})
	if err == nil {
		t.Fatal("expected a diagnostic error")
	}
	if res == nil || len(res.Diagnostics) == 0 {
		t.Fatalf("BuildResult.Diagnostics empty (res=%v)", res)
	}
	if len(msgs) != len(res.Diagnostics) {
		t.Errorf("msgs (%d) and structured diags (%d) length mismatch", len(msgs), len(res.Diagnostics))
	}
	var located bool
	for _, d := range res.Diagnostics {
		if d.FileName != "" && d.Len > 0 {
			located = true
		}
	}
	if !located {
		t.Errorf("no structured diagnostic carried a location: %+v", res.Diagnostics)
	}
}

func TestRewriteDeclarationTypeReferences(t *testing.T) {
	input := "/// <reference types=\"types\" />\n/// <reference types=\"other\" />\n"
	got := rewriteDeclarationTypeReferences(input)
	if !strings.Contains(got, "/// <reference types=\"@rbxts/types\" />") {
		t.Fatalf("got %q, want rewritten @rbxts/types reference", got)
	}
	if !strings.Contains(got, "/// <reference types=\"other\" />") {
		t.Fatalf("got %q, want unrelated reference preserved", got)
	}
	if strings.Contains(got, "/// <reference types=\"types\" />") {
		t.Fatalf("got %q, want raw types reference removed", got)
	}
}

func TestIsOutputFileOrphanedDTSMap(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(srcDir, "foo.ts")
	if err := os.WriteFile(sourcePath, []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(outDir, "foo.d.ts.map")
	if err := os.WriteFile(mapPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	pt := rojo.NewPathTranslator(srcDir, outDir, "", true, true)

	t.Run("env unset keeps legacy deletion", func(t *testing.T) {
		t.Setenv("ROTOR_PRESERVE_DTS_MAPS", "")
		if !isOutputFileOrphaned(pt, mapPath) {
			t.Fatal("expected .d.ts.map to be orphaned with env unset")
		}
	})
	t.Run("env set preserves map while source exists", func(t *testing.T) {
		t.Setenv("ROTOR_PRESERVE_DTS_MAPS", "1")
		if isOutputFileOrphaned(pt, mapPath) {
			t.Fatal("expected .d.ts.map to be preserved when its source exists")
		}
	})
	t.Run("env set still removes map when source is gone", func(t *testing.T) {
		t.Setenv("ROTOR_PRESERVE_DTS_MAPS", "1")
		if err := os.Remove(sourcePath); err != nil {
			t.Fatal(err)
		}
		if !isOutputFileOrphaned(pt, mapPath) {
			t.Fatal("expected .d.ts.map to be orphaned when its source was deleted")
		}
	})
}
