package compile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rotor/tsgo/bundled"
	"rotor/tsgo/compiler"
	"rotor/tsgo/tsoptions"
	"rotor/tsgo/vfs/osvfs"
)

// validateConfigText writes tsconfig (plus extraFiles, slash-relative) into a
// temp project, parses it exactly like newProjectProgram (sanitized FS), and
// returns validateCompilerOptions' message plus the project dir (for building
// expected path-bearing messages).
func validateConfigText(t *testing.T, tsconfig string, extraFiles map[string]string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	// include:["src"] must match at least one file or config parse fails.
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, contents := range extraFiles {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	slashDir := filepath.ToSlash(dir)
	fs := SanitizeFS(bundled.WrapFS(osvfs.FS()))
	host := compiler.NewCompilerHost(slashDir, fs, bundled.LibPath(), nil, nil)
	parsed, configDiags := tsoptions.GetParsedCommandLineOfConfigFile(slashDir+"/tsconfig.json", nil, nil, host, nil)
	if len(configDiags) > 0 {
		t.Fatalf("config parse diagnostics: %v", diagnosticStrings(configDiags))
	}
	raw := readRawEnforcedOptions(filepath.Join(dir, "tsconfig.json"))
	return validateCompilerOptions(parsed.CompilerOptions(), slashDir, raw), dir
}

// validConfig builds the canonical valid config text with the given lines
// removed and/or replaced (key -> full replacement line, "" to drop).
func validConfig(overrides map[string]string) string {
	lines := []struct{ key, line string }{
		{"allowSyntheticDefaultImports", `		"allowSyntheticDefaultImports": true,`},
		{"downlevelIteration", `		"downlevelIteration": true,`},
		{"module", `		"module": "commonjs",`},
		{"moduleResolution", `		"moduleResolution": "Node",`},
		{"noLib", `		"noLib": true,`},
		{"moduleDetection", `		"moduleDetection": "force",`},
		{"strict", `		"strict": true,`},
		{"target", `		"target": "ESNext",`},
		{"typeRoots", `		"typeRoots": ["node_modules/@rbxts"],`},
		{"types", ""},
		{"importsNotUsedAsValues", ""},
		{"rootDir", `		"rootDir": "src",`},
		{"outDir", `		"outDir": "out",`},
	}
	var sb strings.Builder
	sb.WriteString("{\n\t\"compilerOptions\": {\n")
	first := true
	for _, l := range lines {
		line := l.line
		if replacement, ok := overrides[l.key]; ok {
			line = replacement
		}
		if line == "" {
			continue
		}
		if !first {
			sb.WriteString("\n")
		}
		first = false
		sb.WriteString(line)
	}
	out := strings.TrimSuffix(sb.String(), ",")
	return out + "\n\t},\n\t\"include\": [\"src\"]\n}\n"
}

const validateHeader = "Invalid \"tsconfig.json\" configuration!\n" +
	"https://roblox-ts.com/docs/quick-start#project-folder-setup\n"

// TestValidateCompilerOptions pins every enforced check of
// validateCompilerOptions.ts against its exact upstream message. Cases whose
// message embeds the project path use {dir} as a placeholder for the native
// project dir.
func TestValidateCompilerOptions(t *testing.T) {
	rbxtsModules := func(dir string) string {
		return filepath.Join(filepath.FromSlash(dir), "node_modules", "@rbxts")
	}

	tests := []struct {
		name      string
		overrides map[string]string
		extra     map[string]string
		want      []string // "- " lines, in order; nil = valid
	}{
		{name: "canonical valid config", overrides: nil, want: nil},
		{
			name: "preserve bundler config",
			overrides: map[string]string{
				"module":           `		"module": "preserve",`,
				"moduleResolution": `		"moduleResolution": "bundler",`,
			},
			want: nil,
		},
		{
			// L45-47: the target check is commented out upstream.
			name:      "target not enforced",
			overrides: map[string]string{"target": `		"target": "ES2015",`},
			want:      nil,
		},
		{
			name:      "noLib missing",
			overrides: map[string]string{"noLib": ""},
			want:      []string{`"noLib" must be true`},
		},
		{
			name:      "noLib false",
			overrides: map[string]string{"noLib": `		"noLib": false,`},
			want:      []string{`"noLib" must be true`},
		},
		{
			name:      "strict missing",
			overrides: map[string]string{"strict": ""},
			want:      []string{`"strict" must be true`},
		},
		{
			name:      "module esnext",
			overrides: map[string]string{"module": `		"module": "esnext",`},
			want:      []string{`"module" must be commonjs`},
		},
		{
			name:      "moduleDetection missing",
			overrides: map[string]string{"moduleDetection": ""},
			want:      []string{`"moduleDetection" must be "force"`},
		},
		{
			// The sanitizer rewrites "Node" to "bundler" for tsgo, so this
			// check must read the RAW config: an actual "bundler" errors...
			name:      "moduleResolution bundler",
			overrides: map[string]string{"moduleResolution": `		"moduleResolution": "bundler",`},
			want:      []string{`"moduleResolution" must be "Node"`},
		},
		{
			name:      "moduleResolution missing",
			overrides: map[string]string{"moduleResolution": ""},
			want:      []string{`"moduleResolution" must be "Node"`},
		},
		{
			// ...while the case-insensitive node10 spelling stays valid.
			name:      "moduleResolution Node10 spelling",
			overrides: map[string]string{"moduleResolution": `		"moduleResolution": "Node10",`},
			want:      nil,
		},
		{
			name:      "allowSyntheticDefaultImports missing",
			overrides: map[string]string{"allowSyntheticDefaultImports": ""},
			want:      []string{`"allowSyntheticDefaultImports" must be true`},
		},
		{
			name:      "typeRoots missing",
			overrides: map[string]string{"typeRoots": ""},
			want:      []string{`"typeRoots" must contain {rbxts}`},
		},
		{
			name:      "typeRoots without the rbxts scope",
			overrides: map[string]string{"typeRoots": `		"typeRoots": ["node_modules/@types"],`},
			want:      []string{`"typeRoots" must contain {rbxts}`},
		},
		{
			name:      "typeRoots extra entries ok",
			overrides: map[string]string{"typeRoots": `		"typeRoots": ["node_modules/@types", "node_modules/@rbxts"],`},
			want:      nil,
		},
		{
			// The sanitizer injects `"types": ["*"]` when absent; validation
			// must see the user's (absent) types — covered by the canonical
			// case above. Present-but-unresolvable entries error...
			name:      "types entry not found",
			overrides: map[string]string{"types": `		"types": ["services"],`},
			want:      []string{"\"types\" services were not found. Make sure the path is relative to `typeRoots`"},
		},
		{
			// ...directory entries resolve...
			name:      "types entry as directory",
			overrides: map[string]string{"types": `		"types": ["services"],`},
			extra:     map[string]string{"node_modules/@rbxts/services/index.d.ts": "export {};\n"},
			want:      nil,
		},
		{
			// ...and so do bare .d.ts entries (the DTS_EXT retry).
			name:      "types entry as dts file",
			overrides: map[string]string{"types": `		"types": ["globals"],`},
			extra:     map[string]string{"node_modules/@rbxts/globals.d.ts": "declare const g: number;\n"},
			want:      nil,
		},
		{
			name: "rootDir missing",
			// rootDirs may stand in for rootDir; both absent errors.
			overrides: map[string]string{"rootDir": ""},
			want:      []string{`"rootDir" or "rootDirs" must be defined`},
		},
		{
			name:      "rootDirs stands in for rootDir",
			overrides: map[string]string{"rootDir": `		"rootDirs": ["src"],`},
			want:      nil,
		},
		{
			name:      "outDir missing",
			overrides: map[string]string{"outDir": ""},
			want:      []string{`"outDir" must be defined`},
		},
		{
			// tsgo doesn't declare importsNotUsedAsValues (the sanitizer
			// strips it so parse succeeds); the raw value drives upstream's
			// deprecation message: non-preserve suggests false...
			name:      "importsNotUsedAsValues error",
			overrides: map[string]string{"importsNotUsedAsValues": `		"importsNotUsedAsValues": "error",`},
			want:      []string{`"importsNotUsedAsValues" is no longer supported, use "verbatimModuleSyntax": false instead`},
		},
		{
			// ...preserve suggests true.
			name:      "importsNotUsedAsValues preserve",
			overrides: map[string]string{"importsNotUsedAsValues": `		"importsNotUsedAsValues": "preserve",`},
			want:      []string{`"importsNotUsedAsValues" is no longer supported, use "verbatimModuleSyntax": true instead`},
		},
		{
			// Everything at once, pinning upstream's error order (L37-104).
			name: "all violations in upstream order",
			overrides: map[string]string{
				"noLib":                        "",
				"strict":                       "",
				"module":                       "",
				"moduleDetection":              "",
				"moduleResolution":             "",
				"allowSyntheticDefaultImports": "",
				"typeRoots":                    "",
				"types":                        `		"types": ["services"],`,
				"rootDir":                      "",
				"outDir":                       "",
				"importsNotUsedAsValues":       `		"importsNotUsedAsValues": "remove",`,
			},
			want: []string{
				`"noLib" must be true`,
				`"strict" must be true`,
				`"module" must be commonjs`,
				`"moduleDetection" must be "force"`,
				`"moduleResolution" must be "Node"`,
				`"allowSyntheticDefaultImports" must be true`,
				`"typeRoots" must contain {rbxts}`,
				"\"types\" services were not found. Make sure the path is relative to `typeRoots`",
				`"rootDir" or "rootDirs" must be defined`,
				`"outDir" must be defined`,
				`"importsNotUsedAsValues" is no longer supported, use "verbatimModuleSyntax": false instead`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dir := validateConfigText(t, validConfig(tt.overrides), tt.extra)
			if tt.want == nil {
				if got != "" {
					t.Fatalf("validateCompilerOptions = %q, want clean", got)
				}
				return
			}
			want := validateHeader
			for _, e := range tt.want {
				want += "- " + strings.ReplaceAll(e, "{rbxts}", rbxtsModules(dir)) + "\n"
			}
			if got != want {
				t.Errorf("validateCompilerOptions =\n%q\nwant:\n%q", got, want)
			}
		})
	}
}

func TestValidateCompilerOptions_accepts_preserve_bundler_from_extended_config(t *testing.T) {
	got, _ := validateConfigText(
		t,
		`{"extends":"./tsconfig.base.json"}`,
		map[string]string{
			"tsconfig.base.json": validConfig(map[string]string{
				"module":           `		"module": "preserve",`,
				"moduleResolution": `		"moduleResolution": "bundler",`,
			}),
		},
	)

	if got != "" {
		t.Fatalf("validateCompilerOptions = %q, want clean", got)
	}
}

// TestValidateCompilerOptionsFixtureConfig: the differential fixture project's
// checked-in tsconfig.json — the canonical valid rbxts config, which rbxtsc
// 3.0.0 itself compiles — must validate clean.
func TestValidateCompilerOptionsFixtureConfig(t *testing.T) {
	dir := fixtureProjectDir(t)
	slashDir := filepath.ToSlash(dir)
	fs := SanitizeFS(bundled.WrapFS(osvfs.FS()))
	host := compiler.NewCompilerHost(slashDir, fs, bundled.LibPath(), nil, nil)
	parsed, configDiags := tsoptions.GetParsedCommandLineOfConfigFile(slashDir+"/tsconfig.json", nil, nil, host, nil)
	if len(configDiags) > 0 {
		t.Fatalf("config parse diagnostics: %v", diagnosticStrings(configDiags))
	}
	raw := readRawEnforcedOptions(filepath.Join(dir, "tsconfig.json"))
	if got := validateCompilerOptions(parsed.CompilerOptions(), slashDir, raw); got != "" {
		t.Fatalf("fixture config rejected:\n%s", got)
	}
}
