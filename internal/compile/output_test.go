package compile

import (
	"encoding/json"
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
		filepath.Join(outDir, "stale.luau"):               "-- stale\n",
		filepath.Join(outDir, "nested", "old.json"):       "{}\n",
		filepath.Join(outDir, ".git", "keep"):             "keep\n",
		filepath.Join(outDir, "cache.rbxtsc.tsbuildinfo"): "{}\n",
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
	if _, err := os.Stat(filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")); err != nil {
		t.Fatalf("build info missing: %v", err)
	}
	if len(result.EmittedFiles) != 1 || filepath.Base(result.EmittedFiles[0]) != "main.luau" {
		t.Fatalf("EmittedFiles = %v, want only compiled main.luau", result.EmittedFiles)
	}
}

func TestSourceMapEmission(t *testing.T) {
	t.Run("enabled emits a V3 map and removes it with its source", func(t *testing.T) {
		dir := writeProject(t, "@scope/source-map-fixture", "")
		source := "export const answer = 42;\n"
		if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}

		tsconfigPath := filepath.Join(dir, "tsconfig.json")
		tsconfig, err := os.ReadFile(tsconfigPath)
		if err != nil {
			t.Fatal(err)
		}
		tsconfigWithSourceMap := strings.Replace(string(tsconfig), `"outDir": "out"`, `"outDir": "out", "sourceMap": true`, 1)
		if err := os.WriteFile(tsconfigPath, []byte(tsconfigWithSourceMap), 0o644); err != nil {
			t.Fatal(err)
		}

		result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
		if err != nil {
			t.Fatalf("build: %v (diags: %v)", err, diags)
		}

		mapPath := filepath.Join(dir, "out", "main.luau.map")
		mapBytes, err := os.ReadFile(mapPath)
		if err != nil {
			t.Fatalf("read emitted source map: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(mapBytes, &fields); err != nil {
			t.Fatalf("unmarshal source map fields: %v", err)
		}
		if len(fields) != 5 {
			t.Fatalf("source map fields = %v, want exactly version, file, sources, sourcesContent, mappings", fields)
		}
		for _, field := range []string{"version", "file", "sources", "sourcesContent", "mappings"} {
			if _, ok := fields[field]; !ok {
				t.Errorf("source map missing %q field", field)
			}
		}

		var sourceMap struct {
			Version        int      `json:"version"`
			File           string   `json:"file"`
			Sources        []string `json:"sources"`
			SourcesContent []string `json:"sourcesContent"`
			Mappings       string   `json:"mappings"`
		}
		if err := json.Unmarshal(mapBytes, &sourceMap); err != nil {
			t.Fatalf("unmarshal source map: %v", err)
		}
		wantSource := filepath.ToSlash(filepath.Join(dir, "src", "main.ts"))
		if sourceMap.Version != 3 {
			t.Errorf("version = %d, want 3", sourceMap.Version)
		}
		if sourceMap.File != strings.TrimSuffix(wantSource, ".ts")+".luau" {
			t.Errorf("file = %q, want %q", sourceMap.File, strings.TrimSuffix(wantSource, ".ts")+".luau")
		}
		if len(sourceMap.Sources) != 1 || sourceMap.Sources[0] != wantSource {
			t.Errorf("sources = %v, want [%s]", sourceMap.Sources, wantSource)
		}
		if len(sourceMap.SourcesContent) != 1 || sourceMap.SourcesContent[0] != source {
			t.Errorf("sourcesContent = %v, want original source", sourceMap.SourcesContent)
		}
		if sourceMap.Mappings == "" {
			t.Error("mappings is empty")
		}
		for _, emitted := range result.EmittedFiles {
			if strings.HasSuffix(emitted, ".map") {
				t.Errorf("EmittedFiles includes source map %q", emitted)
			}
		}

		srcDir := filepath.Join(dir, "src")
		srcDirInfo, err := os.Stat(srcDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(srcDir, "main.ts")); err != nil {
			t.Fatal(err)
		}
		// Restore the pre-deletion directory mtime: on a fast machine both
		// builds land inside one millisecond anyway, so the copy-files gate
		// must not depend on the deletion being visible in mtimes.
		if err := os.Chtimes(srcDir, srcDirInfo.ModTime(), srcDirInfo.ModTime()); err != nil {
			t.Fatal(err)
		}
		if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
			t.Fatalf("build after source removal: %v (diags: %v)", err, diags)
		}
		if _, err := os.Stat(mapPath); !os.IsNotExist(err) {
			t.Fatalf("orphaned source map err = %v, want not-exist", err)
		}
	})

	t.Run("disabled does not emit a map", func(t *testing.T) {
		dir := writeProject(t, "@scope/source-map-disabled-fixture", "")
		if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
			t.Fatalf("build: %v (diags: %v)", err, diags)
		}
		if _, err := os.Stat(filepath.Join(dir, "out", "main.luau.map")); !os.IsNotExist(err) {
			t.Fatalf("source map with sourceMap disabled err = %v, want not-exist", err)
		}
	})
}

func TestRbxtscBuildInfoPath(t *testing.T) {
	dir := writeProject(t, "@scope/rbxtsc-build-info-fixture", "")
	enableIncrementalBuilds(t, dir)

	if _, _, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("BuildProjectWithOptions: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")); err != nil {
		t.Fatalf("suffixed build info missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "cache.tsbuildinfo")); !os.IsNotExist(err) {
		t.Fatalf("unsuffixed build info err = %v, want not-exist", err)
	}
}

func TestBuildInfoFailureRollback(t *testing.T) {
	dir := writeProject(t, "@scope/build-info-rollback-fixture", "")
	enableIncrementalBuilds(t, dir)
	if _, _, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("first build: %v", err)
	}

	buildInfoPath := filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")
	prior, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatalf("read prior build info: %v", err)
	}
	outputPath := filepath.Join(dir, "out", "main.luau")
	if err := os.Remove(outputPath); err != nil {
		t.Fatalf("remove output: %v", err)
	}
	if err := os.Mkdir(outputPath, 0o755); err != nil {
		t.Fatalf("make output directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const value = 2;\n"), 0o644); err != nil {
		t.Fatalf("change source: %v", err)
	}

	if _, _, err := BuildProjectWithOptions(dir, ProjectOptions{}); err == nil {
		t.Fatal("expected emit failure")
	}
	after, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatalf("read rolled-back build info: %v", err)
	}
	if string(after) != string(prior) {
		t.Fatalf("build info changed after failed emit: got %q, want %q", after, prior)
	}
}

func TestBuildInfoFailureAfterOutputPersistenceRollback(t *testing.T) {
	dir := writeProject(t, "@scope/build-info-persistence-rollback-fixture", "")
	enableIncrementalBuilds(t, dir)
	if _, _, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("first build: %v", err)
	}

	buildInfoPath := filepath.Join(dir, "out", "cache.rbxtsc.tsbuildinfo")
	prior, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "src", "main.ts"),
		[]byte("export const value = 1;\nexport const name = $nameof(value);\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, RotorTypesFileName), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := BuildProjectWithOptions(dir, ProjectOptions{}); err == nil {
		t.Fatal("expected rotor types persistence failure")
	}
	after, err := os.ReadFile(buildInfoPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(prior) {
		t.Fatal("build info changed after post-output persistence failure")
	}
}

func TestDuplicateOutputGuard(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "out", "same.luau")
	second := filepath.Join(dir, "out", ".", "same.luau")
	if err := rejectDuplicateOutputPaths([]string{first, second}); err == nil {
		t.Fatal("expected duplicate output error")
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
		t.Fatalf("output directory err = %v, want not-exist", err)
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

func TestBuildProjectHashMatchRecreatesMissingOutput(t *testing.T) {
	dir := writeProject(t, "@scope/missing-output-fixture", "")
	if _, diags, err := BuildProjectWithOptions(dir, ProjectOptions{}); err != nil {
		t.Fatalf("first build: %v (diags: %v)", err, diags)
	}
	outputPath := filepath.Join(dir, "out", "main.luau")
	if err := os.Remove(outputPath); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{})
	if err != nil {
		t.Fatalf("second build: %v (diags: %v)", err, diags)
	}
	if len(result.EmittedFiles) != 1 || result.EmittedFiles[0] != outputPath {
		t.Fatalf("emitted files = %v, want %s", result.EmittedFiles, outputPath)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("recreated output: %v", err)
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
	timings := NewBuildTimings()
	warm, diags, err := BuildProjectWithOptions(dir, ProjectOptions{Timings: timings})
	if err != nil {
		t.Fatalf("warm build: %v (diags: %v)", err, diags)
	}
	if len(warm.EmittedFiles) != 0 || timings.Counts.ActualWrites != 0 || timings.Counts.HashSkips < 2 {
		t.Fatalf("warm emitted=%v counts=%+v", warm.EmittedFiles, timings.Counts)
	}
}

func TestBuildProjectDeclarationOnlyEmitsDeclarations(t *testing.T) {
	dir := writeProject(t, "@scope/declaration-only-fixture", "")
	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	tsconfig, err := os.ReadFile(tsconfigPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(tsconfig), `"outDir": "out"`, `"outDir": "out", "declaration": true`, 1)
	if err := os.WriteFile(tsconfigPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	result, diags, err := BuildProjectWithOptions(dir, ProjectOptions{EmitDeclarationOnly: true})
	if err != nil {
		t.Fatalf("BuildProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if len(result.EmittedFiles) != 1 || filepath.Base(result.EmittedFiles[0]) != "main.d.ts" {
		t.Fatalf("EmittedFiles = %v, want main.d.ts", result.EmittedFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.d.ts")); err != nil {
		t.Fatalf("declaration output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "main.luau")); !os.IsNotExist(err) {
		t.Fatalf("Luau output stat error = %v, want not exists", err)
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

	t.Run("preserves map while source exists", func(t *testing.T) {
		if isOutputFileOrphaned(pt, mapPath) {
			t.Fatal("expected .d.ts.map to be preserved when its source exists")
		}
	})
	t.Run("removes map when source is gone", func(t *testing.T) {
		if err := os.Remove(sourcePath); err != nil {
			t.Fatal(err)
		}
		if !isOutputFileOrphaned(pt, mapPath) {
			t.Fatal("expected .d.ts.map to be orphaned when its source was deleted")
		}
	})
}
