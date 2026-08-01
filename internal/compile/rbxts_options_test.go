package compile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rotor/internal/logservice"
)

func TestRbxtsOptionsCascade(t *testing.T) {
	// Given
	root := t.TempDir()
	baseDir := filepath.Join(root, "base")
	childDir := filepath.Join(root, "child")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(baseDir, "base.json")
	childPath := filepath.Join(childDir, "tsconfig.json")
	if err := os.WriteFile(basePath, []byte(`{"rbxts":{"type":"package","luau":true,"noInclude":true,"rojo":"./base.project.json"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childPath, []byte(`{"extends":"../base/base.json","rbxts":{"type":"game","luau":false,"includePath":"./runtime","rojo":"./child.project.json"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// When
	opts, err := ReadRbxtsOptions(childPath)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if opts == nil {
		t.Fatal("ReadRbxtsOptions returned nil")
	}
	if opts.Type == nil || *opts.Type != "game" {
		t.Errorf("type = %v, want game", opts.Type)
	}
	if opts.Luau == nil || *opts.Luau {
		t.Errorf("luau = %v, want false", opts.Luau)
	}
	if opts.NoInclude == nil || !*opts.NoInclude {
		t.Errorf("noInclude = %v, want true from parent", opts.NoInclude)
	}
	if opts.IncludePath == nil || *opts.IncludePath != filepath.Join(childDir, "runtime") {
		t.Errorf("includePath = %v, want child-relative absolute path", opts.IncludePath)
	}
	if opts.Rojo == nil || *opts.Rojo != filepath.Join(childDir, "child.project.json") {
		t.Errorf("rojo = %v, want child-relative absolute path", opts.Rojo)
	}

	moduleBase := filepath.Join(root, "node_modules", "@scope", "base", "tsconfig.json")
	if err := os.MkdirAll(filepath.Dir(moduleBase), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moduleBase, []byte(`{"rbxts":{"rojo":"./base.project.json"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	moduleChild := filepath.Join(childDir, "module-tsconfig.json")
	if err := os.WriteFile(moduleChild, []byte(`{"extends":"@scope/base/tsconfig.json"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	moduleOpts, err := ReadRbxtsOptions(moduleChild)
	if err != nil {
		t.Fatal(err)
	}
	if moduleOpts == nil || moduleOpts.Rojo == nil || *moduleOpts.Rojo != filepath.Join(filepath.Dir(moduleBase), "base.project.json") {
		t.Errorf("module extends rojo = %+v, want base-relative path", moduleOpts)
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(RbxtsTsConfigSchema), &schema); err != nil {
		t.Fatalf("rbxts schema is invalid JSON: %v", err)
	}
	if schema["$id"] != "https://raw.githubusercontent.com/uproot/rotor/master/rbxts-tsconfig.schema.json" {
		t.Errorf("schema $id = %v", schema["$id"])
	}
	rbxts := schema["properties"].(map[string]any)["rbxts"].(map[string]any)
	if rbxts["description"] != "roblox-ts compiler options. CLI flags override these at runtime." {
		t.Errorf("rbxts schema description = %v", rbxts["description"])
	}
	if rbxts["additionalProperties"] != false {
		t.Errorf("schema additionalProperties = %v, want false", rbxts["additionalProperties"])
	}
}

func TestCommittedRbxtsSchemaMatchesConstant(t *testing.T) {
	data, err := os.ReadFile("../../rbxts-tsconfig.schema.json")
	if err != nil {
		t.Fatalf("committed rbxts schema missing - run `rotor schema --rbxts > rbxts-tsconfig.schema.json`: %v", err)
	}
	if string(data) != RbxtsTsConfigSchema {
		t.Errorf("../../rbxts-tsconfig.schema.json is out of sync with RbxtsTsConfigSchema")
	}
}

func TestRbxtsOptionsValidation(t *testing.T) {
	// Given
	dir := t.TempDir()
	previousOutput := logservice.Output
	var warnings bytes.Buffer
	logservice.Output = &warnings
	t.Cleanup(func() { logservice.Output = previousOutput })

	writeConfig := func(name, contents string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// When / Then
	unknown := writeConfig("unknown.json", `{"rbxts":{"rojo":"a.project.json","watch":true,"unknownOpt":true}}`)
	opts, err := ReadRbxtsOptions(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if opts == nil || opts.Rojo == nil {
		t.Fatal("known rbxts option was not preserved")
	}
	if got := warnings.String(); !strings.Contains(got, `Unknown "rbxts" option "watch" in `+unknown+` (ignored)`) || !strings.Contains(got, `Unknown "rbxts" option "unknownOpt" in `+unknown+` (ignored)`) {
		t.Errorf("warning = %q", got)
	}

	badType := writeConfig("bad-type.json", `{"rbxts":{"type":"library"}}`)
	if _, err := ReadRbxtsOptions(badType); err == nil || err.Error() != `Invalid "rbxts" config in `+badType+`: type must be "game", "model" or "package" (was "library")` {
		t.Errorf("bad type error = %v", err)
	}

	badRojo := writeConfig("bad-rojo.json", `{"rbxts":{"rojo":42}}`)
	if _, err := ReadRbxtsOptions(badRojo); err == nil || err.Error() != `Invalid "rbxts" config in `+badRojo+`: rojo must be a string (was a number)` {
		t.Errorf("bad rojo error = %v", err)
	}
}

func TestPerProjectRbxtsOptions(t *testing.T) {
	// Given
	dir := t.TempDir()
	config := filepath.Join(dir, "tsconfig.json")
	if err := os.WriteFile(config, []byte(`{"rbxts":{"type":"package","rojo":"./package.project.json","noInclude":true,"luau":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := ProjectOptions{Type: "game", RojoConfigPath: "entry.project.json", EmitIncludeFiles: true, LuaExtension: false}

	// When
	got, err := ProjectOptionsForReferencedConfig(entry, config, false)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "package" || got.RojoConfigPath != filepath.Join(dir, "package.project.json") {
		t.Errorf("referenced options = %+v", got)
	}
	if got.EmitIncludeFiles || !got.LuaExtension {
		t.Errorf("referenced options did not apply noInclude/luau: %+v", got)
	}

	noOwnConfig := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(noOwnConfig, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = ProjectOptionsForReferencedConfig(entry, noOwnConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "" || got.RojoConfigPath != "" {
		t.Errorf("referenced defaults should infer type and Rojo config: %+v", got)
	}

	childDir := filepath.Join(dir, "child")
	if err := os.MkdirAll(filepath.Join(childDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseConfig := `{"compilerOptions":{"allowSyntheticDefaultImports":true,"module":"CommonJS","moduleResolution":"Node","noLib":true,"moduleDetection":"force","strict":true,"target":"ESNext","types":[],"typeRoots":["node_modules/@rbxts"],"rootDir":"src","outDir":"out"}}`
	if err := os.WriteFile(filepath.Join(childDir, "base.json"), []byte(baseConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	childConfig := `{"extends":"./base.json","include":["src"],"rbxts":{"type":"package","luau":false}}`
	if err := os.WriteFile(filepath.Join(childDir, "tsconfig.json"), []byte(childConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "package.json"), []byte(`{"name":"@scope/child"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "src", "globals.d.ts"), []byte(noLibGlobalStubs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "src", "main.ts"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootConfig := filepath.Join(dir, "solution.json")
	if err := os.WriteFile(rootConfig, []byte(`{"files":[],"references":[{"path":"./child"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, messages, err := BuildSolutionWithOptions(rootConfig, ProjectOptions{}); err != nil {
		t.Fatalf("BuildSolutionWithOptions: %v (%v)", err, messages)
	}
	if _, err := os.Stat(filepath.Join(childDir, "out", "main.lua")); err != nil {
		t.Errorf("per-project luau option was not applied: %v", err)
	}
}
