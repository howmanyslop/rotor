package compile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rotor/internal/includefiles"
	"rotor/internal/transformer"
)

// ----------------------------------------------------------------------------
// Runtime-lib emission, one test per ProjectType.
//
// Ground truth: src/_scratch_instanceof.ts —
//
//	declare class Foo {}
//	declare const inst: object;
//	const isFoo = inst instanceof Foo;
//	print(isFoo);
//
// compiled 2026-06-06 through testdata/diff/project with real rbxtsc 3.0.0
// (scratch cleaned up after), once per project type:
//   - `npx rbxtsc --type model` with the checked-in default.project.json,
//   - `npx rbxtsc --type package`,
//   - `npx rbxtsc` (type INFERRED as game) with the tree swapped to
//     {"$className":"DataModel","ReplicatedStorage":{"out":{"$path":"out"},
//     "include":{"$path":"include"},...}}.
//
// The testdata/runtimelib_* projects reproduce those setups self-contained
// (no @rbxts dependency: `print` is declared ambiently, which elides and
// changes no output bytes). `instanceof` is the one Phase-2b construct that
// sets UsesRuntimeLib without needing imports, so these are full end-to-end
// proofs of the runtime-lib require emission ahead of Task 4's TS.import.
// ----------------------------------------------------------------------------

func compileRuntimeLibProject(t *testing.T, name string) map[string]string {
	t.Helper()
	files, diags, err := CompileProject(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("CompileProject: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	return files
}

// Model: relative property chain via RojoResolver.relative — file rbxPath
// ["main"], RuntimeLib ["include","RuntimeLib"] (the project name is never in
// any rbxPath), so the chain is script.Parent.include.RuntimeLib. rbxtsc
// output verbatim.
func TestCompileProjectRuntimeLibModel(t *testing.T) {
	files := compileRuntimeLibProject(t, "runtimelib_model")
	want := "-- Compiled with roblox-ts v3.0.0\n" +
		"local TS = require(script.Parent.include.RuntimeLib)\n" +
		"local isFoo = TS.instanceof(inst, Foo)\n" +
		"print(isFoo)\n" +
		"return nil\n"
	if got := files["out/main.luau"]; got != want {
		t.Errorf("out/main.luau:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if len(files) != 1 {
		t.Errorf("produced files = %d, want 1 (%v)", len(files), keys(files))
	}
}

// Game: absolute GetService + WaitForChild chain over the runtimeLibRbxPath
// ["ReplicatedStorage","include","RuntimeLib"] — the one place WaitForChild
// is emitted literally. rbxtsc output verbatim (type inferred from the
// DataModel tree, also covering the inferProjectType Game branch).
func TestCompileProjectRuntimeLibGame(t *testing.T) {
	files := compileRuntimeLibProject(t, "runtimelib_game")
	want := "-- Compiled with roblox-ts v3.0.0\n" +
		"local TS = require(game:GetService(\"ReplicatedStorage\"):WaitForChild(\"include\"):WaitForChild(\"RuntimeLib\"))\n" +
		"local isFoo = TS.instanceof(inst, Foo)\n" +
		"print(isFoo)\n" +
		"return nil\n"
	if got := files["out/main.luau"]; got != want {
		t.Errorf("out/main.luau:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Package: `local TS = _G[script]`; the scoped package.json name selects
// ProjectType.Package (inferProjectType's first branch), runtimeLibRbxPath
// stays nil, and no Rojo config is required (synthetic resolver over outDir).
// rbxtsc output verbatim.
func TestCompileProjectRuntimeLibPackage(t *testing.T) {
	files := compileRuntimeLibProject(t, "runtimelib_package")
	want := "-- Compiled with roblox-ts v3.0.0\n" +
		"local TS = _G[script]\n" +
		"local isFoo = TS.instanceof(inst, Foo)\n" +
		"print(isFoo)\n" +
		"return nil\n"
	if got := files["out/main.luau"]; got != want {
		t.Errorf("out/main.luau:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// CompileProject over the differential fixture project must reproduce every
// golden byte-for-byte — the whole-project path emits exactly what the
// per-file path (and rbxtsc) does. Task 6 moves the diff harness onto this.
func TestCompileProjectFixtureParity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files, diags, err := CompileProject(filepath.Join(root, "testdata", "diff", "project"))
	if err != nil {
		t.Fatalf("CompileProject: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}

	goldens, err := filepath.Glob(filepath.Join(root, "testdata", "diff", "golden", "*.luau"))
	if err != nil || len(goldens) == 0 {
		t.Fatalf("no goldens found (%v)", err)
	}
	for _, goldenPath := range goldens {
		name := filepath.Base(goldenPath)
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := files["out/"+strings.TrimSuffix(name, ".luau")+".luau"]
		if !ok {
			t.Errorf("%s: missing from CompileProject output (%v)", name, keys(files))
			continue
		}
		if got != string(want) {
			t.Errorf("%s: differs from golden", name)
		}
	}
	if len(files) != len(goldens) {
		t.Errorf("produced %d files, want %d", len(files), len(goldens))
	}
}

// ----------------------------------------------------------------------------
// The four plain-text emit failures (compileFiles.ts L82-98). Message text
// verified against rbxtsc 3.0.0 (the first one reproduced verbatim on
// 2026-06-06 by deleting the fixture's default.project.json; the rest ported
// from the reference).
// ----------------------------------------------------------------------------

// writeProject lays down a minimal compilable project; rojoConfig "" means no
// default.project.json.
func writeProject(t *testing.T, pkgName, rojoConfig string) string {
	t.Helper()
	dir := t.TempDir()
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out"
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+pkgName+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if rojoConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "default.project.json"), []byte(rojoConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "globals.d.ts"), []byte(noLibGlobalStubs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCompileProjectObjectRestDestructuring(t *testing.T) {
	dir := writeProject(t, "@scope/object-rest-fixture", "")
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "Preserve",
		"moduleResolution": "Bundler",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out"
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "interface Props { readonly Change: number; readonly Other: string }\n" +
		"declare const props: Props;\n" +
		"const { Change: change, ...rest } = props;\n" +
		"print(change, rest.Other);\n"
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	files, diags, err := CompileProject(dir)
	if err != nil {
		t.Fatalf("CompileProject: %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}

	want := "-- Compiled with roblox-ts v3.0.0\n" +
		"local _binding = props\n" +
		"local change = _binding.Change\n" +
		"local _extracted = {\n\t[\"Change\"] = true,\n}\n" +
		"local _rest = {}\n" +
		"for _k, _v in _binding do\n\tif not _extracted[_k] then\n\t\t_rest[_k] = _v\n\tend\nend\n" +
		"local rest = _rest\n" +
		"print(change, rest.Other)\n" +
		"return nil\n"
	if got := files["out/main.luau"]; got != want {
		t.Errorf("out/main.luau:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestCompileProjectReferenceUsesReferencedOutDir(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"src", "test", "out", "include", "node_modules/@rbxts"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"package.json":           `{"name":"project-reference-fixture"}`,
		"include/RuntimeLib.lua": "return {}\n",
		"default.project.json":   `{"name":"fixture","tree":{"$className":"DataModel","ReplicatedStorage":{"include":{"$path":"include"},"lib":{"$path":"out"},"tests":{"$path":"out-test"}}}}`,
		"tsconfig.lib.json":      `{"compilerOptions":{"allowSyntheticDefaultImports":true,"composite":true,"declaration":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"},"include":["src"]}`,
		"tsconfig.test.json":     `{"compilerOptions":{"allowSyntheticDefaultImports":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"test","outDir":"out-test"},"references":[{"path":"./tsconfig.lib.json"}],"include":["test"]}`,
		"src/value.ts":           "export const value = 42;\n",
		"out/value.d.ts":         "export declare const value = 42;\n",
		"test/globals.d.ts":      noLibGlobalStubs,
		"test/main.ts":           "import { value } from \"../src/value\";\nprint(value);\n",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	outputs, diags, err := CompileProjectWithOptions(dir, ProjectOptions{TsConfigPath: filepath.Join(dir, "tsconfig.test.json")})
	if err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	got := outputs["out-test/main.luau"]
	if !strings.Contains(got, `TS.import(script, game:GetService("ReplicatedStorage"), "lib", "value").value`) {
		t.Fatalf("cross-project import did not use referenced outDir:\n%s", got)
	}
}

// noLibGlobalStubs declares the fundamental global types the checker resolves
// at initialization under noLib (@rbxts/compiler-types provides them in real
// projects; stubbed so temp projects stay self-contained).
const noLibGlobalStubs = "declare function print(...params: Array<unknown>): void;\n" +
	"interface Array<T> {}\ninterface Boolean {}\ninterface CallableFunction {}\n" +
	"interface Function {}\ninterface IArguments {}\ninterface NewableFunction {}\n" +
	"interface Number {}\ninterface Object {}\ninterface RegExp {}\ninterface String {}\n"

func TestCompileProjectValidationFailures(t *testing.T) {
	tests := []struct {
		name       string
		rojoConfig string
		want       string
	}{
		{
			"non-package without rojo config",
			"",
			"Non-package projects must have a Rojo project file!",
		},
		{
			"include folder not covered",
			`{"name":"x","tree":{"$path":"out"}}`,
			"Rojo project contained no data for include folder!",
		},
		{
			// Network containers only apply to Game projects (getContainer
			// checks isGame), hence the DataModel tree.
			"runtime lib in server container",
			`{"name":"x","tree":{"$className":"DataModel","ServerScriptService":{"out":{"$path":"out"},"include":{"$path":"include"}}}}`,
			"Runtime library cannot be in a server-only or client-only container!",
		},
		{
			// PluginDebugService is isolated but in neither network table
			// (StarterGui etc. would trip the network check first).
			"runtime lib in isolated container",
			`{"name":"x","tree":{"$className":"DataModel","PluginDebugService":{"include":{"$path":"include"}},"ReplicatedStorage":{"out":{"$path":"out"}}}}`,
			"Runtime library cannot be in an isolated container!",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeProject(t, "validation-fixture", tt.rojoConfig)
			_, diags, err := CompileProject(dir)
			if err == nil {
				t.Fatal("expected a hard error")
			}
			if len(diags) != 1 || diags[0] != tt.want {
				t.Errorf("diags = %v, want [%q]", diags, tt.want)
			}
		})
	}
}

// A file whose output path no Rojo $path covers raises noRojoData from
// createRuntimeLibImport (TransformState.ts:232-241) when the file needs the
// runtime lib. The project validation passes (include is covered) — the
// failure is per-file.
func TestCompileProjectNoRojoDataForFile(t *testing.T) {
	dir := writeProject(t, "norojodata-fixture",
		`{"name":"x","tree":{"$path":"out/sub","include":{"$path":"include"}}}`)
	src := "declare class Foo {}\ndeclare const inst: object;\nexport const isFoo = inst instanceof Foo;\n"
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, diags, err := CompileProject(dir)
	if err == nil {
		t.Fatal("expected a hard error")
	}
	want := "Could not find Rojo data. There is no $path in your Rojo config that covers " +
		filepath.Join("out", "main.luau")
	if len(diags) != 1 || diags[0] != want {
		t.Errorf("diags = %v, want [%q]", diags, want)
	}
}

// A file that does not parse must fail the compile. rbxtsc runs
// ts.getPreEmitDiagnostics per file — syntactic diagnostics included — and
// rejects this source with TS1128; tsgo's GetSemanticDiagnostics alone
// reports nothing for the stray `}`, so CompileProject must collect
// GetSyntacticDiagnostics too. The scoped package name makes this a Package
// project, which needs no Rojo config, so the compile reaches the per-file
// diagnostics pass.
func TestCompileProjectSyntacticErrorFails(t *testing.T) {
	dir := writeProject(t, "@syntax/error-fixture", "")
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export const x = 5;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, diags, err := CompileProject(dir)
	if err == nil {
		t.Fatal("expected a hard error for the parse error")
	}
	if files != nil {
		t.Errorf("files = %v, want nil", keys(files))
	}
	if len(diags) == 0 {
		t.Fatal("expected diagnostics for the parse error")
	}
	// Syntactic diagnostics come first, mirroring upstream's
	// getPreEmitDiagnostics ordering.
	if !strings.Contains(diags[0], "Declaration or statement expected") {
		t.Errorf("diags[0] = %q, want the TS1128 'Declaration or statement expected.' diagnostic (all: %v)", diags[0], diags)
	}
}

// TestCompileProjectMissingRootDirOutDir: a tsconfig that defines neither
// rootDir/rootDirs nor outDir must fail with upstream validateCompilerOptions'
// friendly ProjectError text (validateCompilerOptions.ts L89-95, L107-115) —
// not the getRootDirs internal panic.
func TestCompileProjectMissingRootDirOutDir(t *testing.T) {
	dir := t.TempDir()
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"]
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, diags, err := CompileProject(dir)
	if err == nil {
		t.Fatal("expected a hard error for missing rootDir/outDir")
	}
	if files != nil {
		t.Errorf("files = %v, want nil", keys(files))
	}
	want := "Invalid \"tsconfig.json\" configuration!\n" +
		"https://roblox-ts.com/docs/quick-start#project-folder-setup\n" +
		"- \"rootDir\" or \"rootDirs\" must be defined\n" +
		"- \"outDir\" must be defined\n"
	if len(diags) != 1 || diags[0] != want {
		t.Fatalf("diags = %#v, want exactly [%q]", diags, want)
	}
}

// ----------------------------------------------------------------------------
// Include emission (copyInclude.ts via CompileProjectWithOptions).
// ----------------------------------------------------------------------------

// requireIncludeFiles asserts every embedded runtime file exists under
// includeDir with the embedded bytes.
func requireIncludeFiles(t *testing.T, includeDir string) {
	t.Helper()
	for _, name := range includefiles.Names() {
		got, err := os.ReadFile(filepath.Join(includeDir, name))
		if err != nil {
			t.Fatalf("include emission: %v", err)
		}
		want, err := includefiles.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: emitted bytes differ from embedded runtime library", name)
		}
	}
}

// EmitIncludeFiles writes the runtime library to <projectDir>/include by
// default; plain CompileProject (zero options) stays pure and writes nothing.
func TestCompileProjectEmitIncludeFiles(t *testing.T) {
	dir := writeProject(t, "include-fixture",
		`{"name":"x","tree":{"$path":"out","include":{"$path":"include"}}}`)

	if _, diags, err := CompileProject(dir); err != nil {
		t.Fatalf("CompileProject: %v (diags: %v)", err, diags)
	}
	if _, err := os.Stat(filepath.Join(dir, "include")); !os.IsNotExist(err) {
		t.Fatalf("plain CompileProject must not create include/ (err=%v)", err)
	}

	if _, diags, err := CompileProjectWithOptions(dir, ProjectOptions{EmitIncludeFiles: true}); err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	requireIncludeFiles(t, filepath.Join(dir, "include"))
}

// Package projects never receive include files (copyInclude.ts L9-10: with no
// --type flag the gate reduces to !isPackage), even with EmitIncludeFiles set.
func TestCompileProjectEmitIncludeFilesSkipsPackage(t *testing.T) {
	dir := writeProject(t, "@include/package-fixture", "")
	if _, diags, err := CompileProjectWithOptions(dir, ProjectOptions{EmitIncludeFiles: true}); err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	if _, err := os.Stat(filepath.Join(dir, "include")); !os.IsNotExist(err) {
		t.Fatalf("package project must not get include/ (err=%v)", err)
	}
}

// --includePath flows into BOTH halves of the pipeline: the copy destination
// (copyInclude.ts L13) and the RuntimeLib.lua Rojo-path validation
// (compileFiles.ts L88-89). The fixture's Rojo tree maps "runtime", so the
// compile only succeeds because validation looked at the override — and the
// files must land there too.
func TestCompileProjectIncludePathOverride(t *testing.T) {
	dir := writeProject(t, "includepath-fixture",
		`{"name":"x","tree":{"$path":"out","include":{"$path":"runtime"}}}`)
	override := filepath.Join(dir, "runtime")

	opts := ProjectOptions{IncludePath: override, EmitIncludeFiles: true}
	if _, diags, err := CompileProjectWithOptions(dir, opts); err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	requireIncludeFiles(t, override)
	if _, err := os.Stat(filepath.Join(dir, "include")); !os.IsNotExist(err) {
		t.Fatalf("default include/ must stay absent when overridden (err=%v)", err)
	}

	// The default path no longer satisfies validation when the Rojo tree only
	// covers the override — proving newProjectContext consults the option.
	_, diags, err := CompileProjectWithOptions(dir, ProjectOptions{EmitIncludeFiles: false})
	if err == nil {
		t.Fatal("expected validation failure for uncovered default include path")
	}
	want := "Rojo project contained no data for include folder!"
	if len(diags) != 1 || diags[0] != want {
		t.Errorf("diags = %v, want [%q]", diags, want)
	}
}

// Upstream order (CLI/commands/build.ts L140-145): copyInclude runs after
// program creation but before compileFiles, so source-file type errors do not
// prevent the runtime library from landing.
func TestCompileProjectEmitIncludeFilesDespiteTypeErrors(t *testing.T) {
	dir := writeProject(t, "include-errors-fixture",
		`{"name":"x","tree":{"$path":"out","include":{"$path":"include"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("const n: number = \"nope\";\nexport {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := CompileProjectWithOptions(dir, ProjectOptions{EmitIncludeFiles: true}); err == nil {
		t.Fatal("expected the type error to fail the compile")
	}
	requireIncludeFiles(t, filepath.Join(dir, "include"))
}

// ----------------------------------------------------------------------------
// --type override (ProjectOptions.Type), upstream's
// `data.projectOptions.type ?? inferProjectType(...)` (compileFiles.ts L80 fed
// by CLI/commands/build.ts L98-101).
// ----------------------------------------------------------------------------

// TestCompileProjectTypeOverridePackageSkipsRojo: a non-package project with
// NO Rojo config normally fails the "Non-package projects must have a Rojo
// project file!" gate; --type package skips that whole path (no Rojo config,
// no runtime-lib validation) and compiles.
func TestCompileProjectTypeOverridePackageSkipsRojo(t *testing.T) {
	dir := writeProject(t, "typeoverride-fixture", "")

	// Sanity: inference picks Model (unscoped name, no Rojo config) and fails.
	if _, diags, err := CompileProject(dir); err == nil || len(diags) != 1 ||
		diags[0] != "Non-package projects must have a Rojo project file!" {
		t.Fatalf("inferred compile = (%v, %v), want the Rojo-config failure", diags, err)
	}

	files, diags, err := CompileProjectWithOptions(dir, ProjectOptions{Type: transformer.ProjectTypePackage})
	if err != nil {
		t.Fatalf("CompileProjectWithOptions(package): %v (diags: %v)", err, diags)
	}
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", diags)
	}
	if _, ok := files["out/main.luau"]; !ok {
		t.Errorf("out/main.luau missing (%v)", keys(files))
	}
}

// TestCompileProjectTypeOverrideGameModelRequireRojo: --type game/model on an
// INFERRED-package project (scoped name) re-enables the Rojo-config
// requirement — the override beats inference in both directions.
func TestCompileProjectTypeOverrideGameModelRequireRojo(t *testing.T) {
	for _, projectType := range []transformer.ProjectType{transformer.ProjectTypeGame, transformer.ProjectTypeModel} {
		t.Run(string(projectType), func(t *testing.T) {
			dir := writeProject(t, "@scope/typeoverride-fixture", "")

			// Sanity: inference picks Package (scoped name) and compiles.
			if _, diags, err := CompileProject(dir); err != nil {
				t.Fatalf("inferred package compile failed: %v (diags: %v)", err, diags)
			}

			_, diags, err := CompileProjectWithOptions(dir, ProjectOptions{Type: projectType})
			if err == nil {
				t.Fatal("expected the Rojo-config failure")
			}
			want := "Non-package projects must have a Rojo project file!"
			if len(diags) != 1 || diags[0] != want {
				t.Errorf("diags = %v, want [%q]", diags, want)
			}
		})
	}
}

// TestCompileProjectTypeOverridePackageEmission: --type package on the model
// fixture switches the runtime-lib require to the package `_G[script]` shape
// — the override reaches the transformer's ProjectType, not just the
// validation gates (mirrors the rbxtsc oracle run `npx rbxtsc --type package`
// documented atop this file).
func TestCompileProjectTypeOverridePackageEmission(t *testing.T) {
	files, diags, err := CompileProjectWithOptions(
		filepath.Join("testdata", "runtimelib_model"),
		ProjectOptions{Type: transformer.ProjectTypePackage},
	)
	if err != nil {
		t.Fatalf("CompileProjectWithOptions: %v (diags: %v)", err, diags)
	}
	want := "-- Compiled with roblox-ts v3.0.0\n" +
		"local TS = _G[script]\n" +
		"local isFoo = TS.instanceof(inst, Foo)\n" +
		"print(isFoo)\n" +
		"return nil\n"
	if got := files["out/main.luau"]; got != want {
		t.Errorf("out/main.luau:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewProjectProgramUsesMultipleCheckerGroups(t *testing.T) {
	dir := writeProject(t, "@scope/checkers-fixture", "")
	tsconfig := `{
	"compilerOptions": {
		"allowSyntheticDefaultImports": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"types": [],
		"typeRoots": ["node_modules/@rbxts"],
		"rootDir": "src",
		"outDir": "out",
		"checkers": 3
	},
	"include": ["src"]
}`
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.ts", "b.ts", "c.ts", "d.ts"} {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte("export {};\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, program, diags, err := newProjectProgram(dir, "")
	if err != nil {
		t.Fatalf("newProjectProgram: %v (diags: %v)", err, diags)
	}
	sourceFiles := projectSourceFiles(program)
	checkers := 0
	if program.Options().Checkers != nil {
		checkers = *program.Options().Checkers
	}
	groups := groupSourceFilesByChecker(context.Background(), program, sourceFiles)
	if len(groups) < 2 {
		t.Fatalf("checker groups = %d, want at least 2 (source files=%d, singleThreaded=%v, options.checkers=%d)", len(groups), len(sourceFiles), program.SingleThreaded(), checkers)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
