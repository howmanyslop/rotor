package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"rotor/internal/compile"
	"rotor/internal/config"
)

// newInitCommand scaffolds a new rotor project. The game template (default)
// is a full rbxts game (package.json, tsconfig.json, DataModel Rojo project,
// starter src/); package is an rbxts model/package project; plain is a
// Luau-only project for bundle/minify/pack users.
//
// When stdin and stdout are both terminals and neither --template nor --yes
// was passed, an interactive wizard collects the options; otherwise (CI,
// pipes, or explicit flags) the non-interactive default scaffold runs.
func newInitCommand(streams cliStreams) *cobra.Command {
	var template string
	var yes, configOnly bool
	cmd := &cobra.Command{
		Use:                   "init [dir] [--template game|package|plain]",
		Short:                 "scaffold a new project (rbxts game; package lib, or plain Luau)",
		Args:                  cobra.MaximumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			dir := "."
			if len(argv) > 0 {
				dir = argv[0]
			}
			return runInitCommand(streams, cmd, dir, template, yes, configOnly)
		},
	}
	cmd.Flags().SortFlags = false
	addStringFlag(cmd, &template, "template", "t", "", "<name>",
		"scaffold non-interactively from a template (game, package, or plain)")
	addBoolFlag(cmd, &yes, "yes", "y", false, "accept all defaults, no prompts")
	addBoolFlag(cmd, &configOnly, "config", "", false, "add only rotor config to an existing project")
	return cmd
}

func runInitCommand(streams cliStreams, cmd *cobra.Command, dir, template string, yes, configOnly bool) error {
	templateSet := template != ""
	if template == "" {
		template = "game"
	}
	if template != "game" && template != "package" && template != "plain" {
		return usageFailure("unknown template %q (want game, package, or plain)", template)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return runtimeFailure(err)
	}
	name := filepath.Base(abs)

	// Three-way decision (replaces the old "refuse existing project" guard):
	// adopt an existing project (config-only), no-op if already configured, or
	// fall through to greenfield scaffolding for an empty directory.
	existing := false
	for _, marker := range []string{"package.json", "tsconfig.json", "default.project.json"} {
		if fileExists(filepath.Join(dir, marker)) {
			existing = true
			break
		}
	}
	if configOnly || existing {
		if fileExists(filepath.Join(dir, config.ConfigFileName)) {
			u := newUI(streams.out)
			u.banner("init  " + name)
			fmt.Fprintln(u.w)
			u.events([]uiEvent{{
				Status: eventUnchanged,
				Target: "already configured",
				Detail: config.ConfigFileName + " exists",
			}})
			fmt.Fprintf(u.w, "    %s %s\n", u.s.Muted(u.s.Glyphs().Arrow), u.s.Info("sloptor doctor"))
			return nil
		}
		if writeAdoptFiles(streams.out, dir, detectTemplate(dir)) != 0 {
			return reportedFailure(fmt.Errorf("init failed"))
		}
		return nil
	}

	// Wizard gate: a real terminal on both ends and no overriding flags.
	inFile, inIsFile := streams.in.(*os.File)
	outFile, outIsFile := streams.out.(*os.File)
	if !templateSet && !yes && inIsFile && outIsFile && isTerminal(inFile) && isTerminal(outFile) {
		if runInitInteractive(dir, name, streams.in, streams.out) != 0 {
			return reportedFailure(fmt.Errorf("init failed"))
		}
		return nil
	}

	opts := initOptions{dir: dir, name: name, template: template}
	u := newUI(streams.out)
	u.banner("init  " + name)
	if writeInitFiles(streams.out, opts, scaffold(opts)) != 0 {
		return reportedFailure(fmt.Errorf("init failed"))
	}
	return nil
}

// isTerminal reports whether f is an interactive terminal (character device).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// detectTemplate guesses which rotor.toml skeleton fits an existing project:
//   - "plain"   — a default.project.json but no tsconfig.json (Luau-only)
//   - "package" — tsconfig has "declaration": true (a model/package project)
//   - "game"    — otherwise (the common rbxts game)
//
// It only chooses which commented skeleton to describe; it never reads or
// writes source files.
func detectTemplate(dir string) string {
	tsconfig := filepath.Join(dir, "tsconfig.json")
	hasTS := fileExists(tsconfig)
	if !hasTS && fileExists(filepath.Join(dir, "default.project.json")) {
		return "plain"
	}
	if hasTS {
		if data, err := os.ReadFile(tsconfig); err == nil && declarationEnabled(data) {
			return "package"
		}
	}
	return "game"
}

// declarationEnabled reports whether a tsconfig's compilerOptions.declaration is
// true. tsconfig.json is JSONC; a tolerant line scan avoids pulling in a full
// JSONC parser for this one boolean (a commented-out "declaration" stays false).
func declarationEnabled(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			continue
		}
		if strings.Contains(t, "\"declaration\"") && strings.Contains(t, "true") {
			return true
		}
	}
	return false
}

// adoptFiles returns just the rotor config files for adopt mode: rotor.toml
// (the commented skeleton) and the editor type declaration for rotor's macros.
// Source/project files are never part of adopt mode.
func adoptFiles() []initFile {
	return []initFile{
		{config.ConfigFileName, rotorTOML(nil, nil)},
		{compile.RotorTypesFileName, compile.RotorTypesFileText},
	}
}

// writeAdoptFiles writes adoptFiles() into an existing project, never
// overwriting: a pre-existing target is reported as "(exists, kept)". template
// is the detected skeleton, surfaced to the user as context (the skeleton is
// shared today, but the detection keeps the message honest and future-proofs
// per-template config).
func writeAdoptFiles(out io.Writer, dir, template string) int {
	u := newUI(out)
	u.banner("init  " + filepath.Base(mustAbs(dir)) + "  (adopt)")
	fmt.Fprintf(out, "  %s %s\n\n", u.s.Muted(u.s.Glyphs().Dot), u.s.Muted("detected an existing "+template+" project"))
	var events []uiEvent
	wrote := 0
	for _, f := range adoptFiles() {
		path := filepath.Join(dir, filepath.FromSlash(f.path))
		if fileExists(path) {
			events = append(events, uiEvent{Status: eventUnchanged, Target: f.path, Detail: "(exists, kept)"})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			newUI(os.Stderr).failLine(fmt.Sprintf("sloptor init: %v", err))
			return 1
		}
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			newUI(os.Stderr).failLine(fmt.Sprintf("sloptor init: cannot write %q: %v", path, err))
			return 1
		}
		events = append(events, uiEvent{Status: eventCreated, Target: f.path})
		wrote++
	}
	u.events(events)
	printAdoptNextSteps(u, wrote)
	return 0
}

// mustAbs resolves dir to an absolute path, falling back to dir on error (only
// used for the display name in the banner).
func mustAbs(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

func printAdoptNextSteps(u *ui, wrote int) {
	fmt.Fprintln(u.w)
	status, target, detail := eventFinished, "added sloptor config to an existing project", plural(wrote, "file")
	if wrote == 0 {
		status, target, detail = eventUnchanged, "already had sloptor config", "nothing to add"
	}
	u.events([]uiEvent{{Status: status, Target: target, Detail: detail}})
	fmt.Fprintln(u.w)
	fmt.Fprintf(u.w, "  %s\n", u.s.Bold("next steps"))
	fmt.Fprintf(u.w, "    %s %s\n", u.s.Muted(u.s.Glyphs().Arrow), u.s.Info("sloptor doctor"))
}

// initOptions is everything the scaffold needs to render a project. The
// non-interactive path fills only dir/name/template; the wizard fills the
// rest. scaffold() is the single source of truth for file contents either way.
type initOptions struct {
	dir      string
	name     string // project name → default.project.json name + npm name
	template string // game | package | plain
	linter   string // "" (none) | biome | oxlint — rbxts templates only
	packages []int  // indices into extraPackages — rbxts templates only
	assets   *assetsOptions
	deploy   *deployOptions
}

// assetsOptions is the wizard's asset-sync answers; nil means "keep the
// commented skeleton" in rotor.toml.
type assetsOptions struct {
	dir         string // asset directory, forward slashes, no trailing slash
	creatorType string // "user" or "group"
	creatorID   string // digits
}

// deployOptions is the wizard's deploy answers; nil means "keep the commented
// skeleton" in rotor.toml.
type deployOptions struct {
	env        string // environment name, e.g. "production"
	universeID string // digits
	placeID    string // digits
	placeFile  string // path to the built place file
}

// initFile is one scaffolded file: a forward-slash path relative to the
// project directory, and its content.
type initFile struct {
	path    string
	content string
}

// dep is one npm dependency pin.
type dep struct {
	name, version string
}

// extraPackage is a wizard-selectable extra dependency (step d).
type extraPackage struct {
	label string // menu label
	desc  string // muted menu description
	deps  []dep
	jsx   string // non-empty configures the tsconfig jsx options: "react" or "vide"
}

// Version pins for wizard extras. A scaffold cannot query the npm registry,
// so pins are loose ^majors only where the major has been stable for a long
// time (@rbxts/services 1.x, @rbxts/net v3, Biome 2.x, oxlint 1.x) and "*"
// where releases still move majors quickly (react/react-roblox track the
// upstream React port, charm and lapis are pre-1.0-ish). This is a scaffold:
// `npm install` / `bun install` resolves the real latest into the lockfile.
const (
	biomeVersion  = "^2.0.0"
	oxlintVersion = "^1.0.0"
)

var extraPackages = []extraPackage{
	{label: "@rbxts/services", desc: "typed service access", deps: []dep{{"@rbxts/services", "^1.0.0"}}},
	{label: "@rbxts/react + @rbxts/react-roblox", desc: "React UI (enables tsconfig jsx)", jsx: "react",
		deps: []dep{{"@rbxts/react", "*"}, {"@rbxts/react-roblox", "*"}}},
	{label: "@rbxts/vide", desc: "reactive UI (enables tsconfig jsx for Vide)", jsx: "vide",
		deps: []dep{{"@rbxts/vide", "*"}}},
	{label: "@rbxts/charm", desc: "atomic state management", deps: []dep{{"@rbxts/charm", "*"}}},
	{label: "@rbxts/net", desc: "typed networking (rbx-net)", deps: []dep{{"@rbxts/net", "^3.0.0"}}},
	{label: "@rbxts/lapis", desc: "DataStore abstraction", deps: []dep{{"@rbxts/lapis", "*"}}},
	{label: "@rbxts/remo", desc: "typed remote events/functions", deps: []dep{{"@rbxts/remo", "*"}}},
	{label: "@rbxts/ripple", desc: "spring/tween motion library", deps: []dep{{"@rbxts/ripple", "*"}}},
	{label: "@rbxts/pretty-react-hooks", desc: "hook utilities for @rbxts/react", deps: []dep{{"@rbxts/pretty-react-hooks", "*"}}},
}

// scaffold returns the complete file set for the chosen options, including
// the editor type declarations, linter config, and asset-dir placeholder, so
// the wizard summary can list every file before anything is written.
func scaffold(opts initOptions) []initFile {
	name := opts.name
	if opts.template == "plain" {
		return []initFile{
			{"default.project.json", fmt.Sprintf(`{
	"name": %s,
	"tree": {
		"$path": "src"
	}
}
`, jsonString(name))},
			{"src/init.luau", `--!strict
local hello = {}

function hello.makeHello(name: string): string
	return ("Hello from %s!"):format(name)
end

return hello
`},
			{"aftman.toml", `# Tool versions for this project, managed by Aftman:
#   https://github.com/LPGhatguy/aftman
#
# sloptor bundle / minify / pack / sourcemap work without any of these; install
# rojo if you want live sync into Studio or sloptor's rbxm/rbxmx pack formats.

[tools]
# rojo = "rojo-rbx/rojo@7.6.1"
`},
		}
	}

	// React wins if both a react and a vide package were selected — the user
	// can flip the factories by hand in that (unusual) mixed setup.
	jsx := ""
	for _, i := range opts.packages {
		if p := extraPackages[i].jsx; p != "" && (jsx == "" || p == "react") {
			jsx = p
		}
	}

	files := []initFile{
		{"package.json", packageJSON(opts)},
		{"tsconfig.json", tsconfigJSON(opts.template == "package", jsx)},
	}
	if opts.template == "package" {
		files = append(files,
			initFile{"default.project.json", fmt.Sprintf(`{
	"name": %s,
	"globIgnorePaths": ["**/package.json", "**/tsconfig.json"],
	"tree": {
		"$path": "out"
	}
}
`, jsonString(name))},
			initFile{"src/init.ts", tsHelloModule},
			initFile{".gitignore", "/node_modules\n/out\n"},
		)
	} else { // game
		files = append(files,
			initFile{"default.project.json", fmt.Sprintf(`{
	"name": %s,
	"globIgnorePaths": ["**/package.json", "**/tsconfig.json"],
	"tree": {
		"$className": "DataModel",
		"ReplicatedStorage": {
			"rbxts_include": {
				"$path": "include",
				"node_modules": {
					"$className": "Folder",
					"@rbxts": {
						"$path": "node_modules/@rbxts"
					}
				}
			},
			"TS": {
				"$path": "out/shared"
			}
		},
		"ServerScriptService": {
			"TS": {
				"$path": "out/server"
			}
		},
		"StarterPlayer": {
			"StarterPlayerScripts": {
				"TS": {
					"$path": "out/client"
				}
			}
		},
		"HttpService": {
			"$properties": {
				"HttpEnabled": true
			}
		},
		"SoundService": {
			"$properties": {
				"RespectFilteringEnabled": true
			}
		}
	}
}
`, jsonString(name))},
			initFile{"src/shared/module.ts", tsHelloModule},
			initFile{"src/server/main.server.ts", `import { makeHello } from "../shared/module";

print(makeHello("main.server.ts"));
`},
			initFile{"src/client/main.client.ts", `import { makeHello } from "../shared/module";

print(makeHello("main.client.ts"));
`},
			initFile{".gitignore", "/node_modules\n/out\n/include/*\n!/include/.gitkeep\n"},
			initFile{"include/.gitkeep", ""},
		)
	}

	files = append(files, initFile{config.ConfigFileName, rotorTOML(opts.assets, opts.deploy)})
	switch opts.linter {
	case "biome":
		files = append(files, initFile{"biome.json", biomeJSON})
	case "oxlint":
		files = append(files, initFile{".oxlintrc.json", oxlintrcJSON})
	}
	if opts.assets != nil {
		files = append(files, initFile{opts.assets.dir + "/.gitkeep", ""})
	}
	// Editor types for all of rotor's macros; the scaffolded tsconfig lists the
	// file under "include" so tsserver picks it up (the compiler skips its own
	// synthetic copies when this on-disk one is part of the program).
	files = append(files, initFile{compile.RotorTypesFileName, compile.RotorTypesFileText})
	return files
}

// writeInitFiles writes the scaffold to disk under opts.dir, printing one
// `Created` event row per file plus the final timed row and next-steps block.
// out is the wizard/banner stream (a buffer in tests); operational errors
// still go to stderr.
func writeInitFiles(out io.Writer, opts initOptions, files []initFile) int {
	u := newUI(out)
	start := time.Now()
	events := make([]uiEvent, 0, len(files)+1)
	for _, f := range files {
		path := filepath.Join(opts.dir, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			newUI(os.Stderr).failLine(fmt.Sprintf("sloptor init: %v", err))
			return 1
		}
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			newUI(os.Stderr).failLine(fmt.Sprintf("sloptor init: cannot write %q: %v", path, err))
			return 1
		}
		events = append(events, uiEvent{Status: eventCreated, Target: f.path})
	}
	events = append(events, uiEvent{
		Status:  eventFinished,
		Target:  fmt.Sprintf("scaffolded a %s project", opts.template),
		Detail:  fmt.Sprintf("%s · %s", opts.dir, plural(len(files), "file")),
		Elapsed: time.Since(start),
	})
	u.events(events)
	printInitNextSteps(u, opts, len(files))
	return 0
}

const tsHelloModule = `export function makeHello(name: string) {
	return ` + "`Hello from ${name}!`" + `;
}
`

// rotorTOML renders rotor.toml. Its first line is the taplo `#:schema`
// directive so editors validate + complete against rotor.schema.json. Sections
// the wizard configured are written for real; skipped (nil) sections keep a
// commented skeleton.
func rotorTOML(assets *assetsOptions, deploy *deployOptions) string {
	var b strings.Builder
	b.WriteString(config.SchemaDirective + "\n\n")
	b.WriteString("# sloptor project configuration — read by `sloptor asset sync` and\n")
	b.WriteString("# `sloptor deploy`. Uncomment the sections you need.\n")

	if assets != nil {
		b.WriteString("\n[assets]\n")
		b.WriteString("mode = \"module\"\n")
		fmt.Fprintf(&b, "paths = [%q, %q]\n", assets.dir+"/**/*.png", assets.dir+"/**/*.ogg")
		b.WriteString("\n[assets.output]\n")
		b.WriteString("luau = \"src/shared/assets.luau\"\n")
		b.WriteString("types = \"src/shared/assets.d.ts\"\n")
		b.WriteString("\n[assets.creator]\n")
		fmt.Fprintf(&b, "type = %q\n", assets.creatorType)
		fmt.Fprintf(&b, "id = %s\n", assets.creatorID)
	} else {
		b.WriteString(`
# [assets]
# mode = "module"                 # "module" (assets.luau) | "macro" ($asset transformer)
# paths = ["assets/**/*.png", "assets/**/*.ogg"]
#
# [assets.output]                 # only used in "module" mode
# luau = "src/shared/assets.luau"
# types = "src/shared/assets.d.ts"
#
# [assets.creator]
# type = "user"                   # "user" | "group"
# id = 0
`)
	}

	if deploy != nil {
		fmt.Fprintf(&b, "\n[deploy.environments.%s]\n", tomlKey(deploy.env))
		fmt.Fprintf(&b, "universeId = %s\n", deploy.universeID)
		fmt.Fprintf(&b, "\n[deploy.environments.%s.places.start]\n", tomlKey(deploy.env))
		fmt.Fprintf(&b, "file = %q\n", deploy.placeFile)
		fmt.Fprintf(&b, "placeId = %s\n", deploy.placeID)
	} else {
		b.WriteString(`
# [deploy.environments.dev]
# universeId = 0
# [deploy.environments.dev.places.start]
# file = "build/game.rbxl"
# placeId = 0
`)
	}
	return b.String()
}

// tomlKey renders a TOML table key segment: bare when it is a valid bare key
// (letters, digits, underscores, hyphens), quoted otherwise.
func tomlKey(name string) string {
	bare := name != ""
	for _, r := range name {
		ok := r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9')
		if !ok {
			bare = false
			break
		}
	}
	if bare {
		return name
	}
	return jsonString(name)
}

// biomeJSON is the rbxts-tuned Biome config (Biome 2.x schema): tab indent,
// double quotes, import organizing on, recommended lint rules minus
// noNonNullAssertion (rbxts code uses `!` heavily by design).
const biomeJSON = `{
	"$schema": "https://biomejs.dev/schemas/2.0.0/schema.json",
	"files": {
		"includes": ["src/**"]
	},
	"formatter": {
		"enabled": true,
		"indentStyle": "tab"
	},
	"javascript": {
		"formatter": {
			"quoteStyle": "double"
		}
	},
	"assist": {
		"actions": {
			"source": {
				"organizeImports": "on"
			}
		}
	},
	"linter": {
		"enabled": true,
		"rules": {
			"recommended": true,
			"style": {
				"noNonNullAssertion": "off"
			}
		}
	}
}
`

// oxlintrcJSON enables oxlint's recommended categories over src.
const oxlintrcJSON = `{
	"$schema": "./node_modules/oxlint/configuration_schema.json",
	"categories": {
		"correctness": "error",
		"suspicious": "warn"
	},
	"ignorePatterns": ["node_modules/**", "out/**", "include/**"]
}
`

// packageJSON renders the scaffolded package.json from the options: linter
// scripts/devDeps and wizard-selected dependencies included. Rendering goes
// through encoding/json so the output is valid JSON by construction.
func packageJSON(opts initOptions) string {
	pkg := struct {
		Name            string            `json:"name"`
		Version         string            `json:"version"`
		Private         bool              `json:"private,omitempty"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies,omitempty"`
		DevDependencies map[string]string `json:"devDependencies"`
	}{
		Name:    npmName(opts.name),
		Version: "0.1.0",
		Private: opts.template == "game",
		Scripts: map[string]string{
			"build": "sloptor build",
			"watch": "sloptor dev",
		},
		DevDependencies: map[string]string{
			// compiler-types only publishes prerelease-tagged versions
			// (X.Y.Z-types.N); a plain ^X.Y.Z range matches none of them,
			// so the range must carry the prerelease component.
			"@rbxts/compiler-types": "^3.0.0-types.0",
			"@rbxts/types":          "^1.0.800",
			"typescript":            "^5.5.0",
		},
	}
	switch opts.linter {
	case "biome":
		pkg.DevDependencies["@biomejs/biome"] = biomeVersion
		pkg.Scripts["lint"] = "biome check src"
		pkg.Scripts["format"] = "biome format --write src"
	case "oxlint":
		pkg.DevDependencies["oxlint"] = oxlintVersion
		pkg.Scripts["lint"] = "oxlint src"
	}
	if len(opts.packages) > 0 {
		pkg.Dependencies = map[string]string{}
		for _, i := range opts.packages {
			for _, d := range extraPackages[i].deps {
				pkg.Dependencies[d.name] = d.version
			}
		}
	}
	b, err := json.MarshalIndent(pkg, "", "\t")
	if err != nil {
		return "{}\n" // unreachable: the struct always marshals
	}
	return string(b) + "\n"
}

// tsconfigJSON renders the scaffolded tsconfig.json: the canonical rbxts shape
// (no baseUrl). The jsx options are commented out unless a JSX UI package was
// selected ("react" or "vide" factories); tsconfig.json is JSONC, so comment
// lines are valid for tooling.
func tsconfigJSON(declaration bool, jsx string) string {
	declarationLine := ""
	if declaration {
		declarationLine = "\t\t\"declaration\": true,\n"
	}
	jsxBlock := `		// jsx — uncomment for @rbxts/react (or use Vide.jsx factories for @rbxts/vide)
		// "jsx": "react",
		// "jsxFactory": "React.createElement",
		// "jsxFragmentFactory": "React.Fragment",
`
	switch jsx {
	case "react":
		jsxBlock = `		// jsx — configured for @rbxts/react
		"jsx": "react",
		"jsxFactory": "React.createElement",
		"jsxFragmentFactory": "React.Fragment",
`
	case "vide":
		jsxBlock = `		// jsx — configured for @rbxts/vide
		"jsx": "react",
		"jsxFactory": "Vide.jsx",
		"jsxFragmentFactory": "Vide.Fragment",
`
	}
	// NOTE: no "types" entry — "types": [] would disable the automatic
	// inclusion of every package under typeRoots, which is exactly how
	// @rbxts/types' globals (print, game, ...) get loaded.
	return fmt.Sprintf(`{
	"compilerOptions": {
		// required
		"allowSyntheticDefaultImports": true,
		"downlevelIteration": true,
		"module": "CommonJS",
		"moduleResolution": "Node",
		"noLib": true,
		"resolveJsonModule": true,
		"forceConsistentCasingInFileNames": true,
		"moduleDetection": "force",
		"strict": true,
		"target": "ESNext",
		"typeRoots": ["node_modules/@rbxts"],

%s
		// configurable
%s		"rootDir": "src",
		"outDir": "out"
	},
	"include": ["src", "rotor.d.ts"]
}
`, jsxBlock, declarationLine)
}

// npmName lowercases a directory name and replaces characters that are not
// valid in npm package names.
func npmName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "rotor-project"
	}
	return out
}

// jsonString renders s as a JSON string literal.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"project"`
	}
	return string(b)
}

// detectPackageManager picks the install command to suggest: bun when a bun
// lockfile already exists in the project dir or bun is on PATH, npm otherwise.
func detectPackageManager(dir string) string {
	if fileExists(filepath.Join(dir, "bun.lockb")) || fileExists(filepath.Join(dir, "bun.lock")) {
		return "bun"
	}
	if _, err := exec.LookPath("bun"); err == nil {
		return "bun"
	}
	return "npm"
}

func printInitNextSteps(u *ui, opts initOptions, n int) {
	fmt.Fprintln(u.w)
	fmt.Fprintf(u.w, "  %s\n", u.s.Bold("next steps"))
	step := func(cmd, why string) {
		if why == "" {
			fmt.Fprintf(u.w, "    %s %s\n", u.s.Muted(u.s.Glyphs().Arrow), u.s.Info(cmd))
			return
		}
		pad := strings.Repeat(" ", max(1, 38-len(cmd)))
		fmt.Fprintf(u.w, "    %s %s%s%s\n", u.s.Muted(u.s.Glyphs().Arrow), u.s.Info(cmd), pad, u.s.Muted(why))
	}
	if opts.dir != "." {
		step("cd "+opts.dir, "")
	}
	if opts.template == "plain" {
		step("sloptor pack --as luau -o bundle.luau", "package the project into one script")
		step("sloptor sourcemap -o sourcemap.json", "generate a sourcemap for luau-lsp")
		return
	}
	pm := detectPackageManager(opts.dir)
	if pm == "bun" {
		step("bun install", "(or npm install)")
	} else {
		step("npm install", "(or bun install / pnpm install)")
	}
	step("sloptor build", "compile to Luau")
	step("sloptor dev", "watch + serve to Studio")
	if opts.linter != "" {
		step(pm+" run lint", "lint the source")
	}
}
