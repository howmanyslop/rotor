package compile

import (
	"testing"
)

// ----------------------------------------------------------------------------
// Import declarations + module-to-Luau resolution (Phase 3a Task 4).
//
// Ground truth: every `want` below is VERBATIM rbxtsc 3.0.0 output, captured
// 2026-06-06 by compiling the same _scratch_* sources through
// testdata/diff/project (which has the real toolchain in node_modules) and
// a fake @rbxts/dummy package (main: out/init.lua, types: out/index.d.ts):
//   - Model:   `rbxtsc --type model` with the checked-in default.project.json
//   - Game:    `rbxtsc` (type inferred) with the tree swapped to a DataModel
//   - Package: `rbxtsc --type package`
// Scratch sources and the fake package were deleted afterwards; the
// testdata/imports_{model,game,package} fixtures reproduce the setups
// self-contained (print declared ambiently in globals.d.ts, which changes no
// output bytes).
// ----------------------------------------------------------------------------

const importsHeaderModel = "-- Compiled with roblox-ts v3.0.0\n" +
	"local TS = require(script.Parent.include.RuntimeLib)\n"

// TestCompileProjectImportsModel covers the Model-project import matrix; the
// _scratch_main/_scratch_util pair is the digest §5.3 verbatim ground truth.
func TestCompileProjectImportsModel(t *testing.T) {
	files := compileRuntimeLibProject(t, "imports_model")

	wants := map[string]string{
		// digest §5.3: uses=3 => temp named after the cleaned specifier
		// segment; clause-order bindings; real `default` export => .default.
		"out/_scratch_main.luau": importsHeaderModel +
			"local __scratch_util = TS.import(script, script.Parent, \"_scratch_util\")\n" +
			"local greeter = __scratch_util.default\n" +
			"local VALUE = __scratch_util.VALUE\n" +
			"local g = __scratch_util.greet\n" +
			"local x = VALUE + greeter()\n" +
			"print(g(\"world\"), x)\n" +
			"return nil\n",
		// digest §5.3: the imported module itself.
		"out/_scratch_util.luau": "-- Compiled with roblox-ts v3.0.0\n" +
			"local VALUE = 123\n" +
			"local function greet(name)\n" +
			"\treturn `Hello, {name}`\n" +
			"end\n" +
			"local function default()\n" +
			"\treturn VALUE\n" +
			"end\n" +
			"return {\n" +
			"\tgreet = greet,\n" +
			"\tdefault = default,\n" +
			"\tVALUE = VALUE,\n" +
			"}\n",
		// named import used once: TS.import inlined at the single use site.
		"out/_scratch_once.luau": importsHeaderModel +
			"local g = TS.import(script, script.Parent, \"_scratch_util\").greet\n" +
			"print(g(\"once\"))\n" +
			"return nil\n",
		// named imports used twice: hoisted temp.
		"out/_scratch_twice.luau": importsHeaderModel +
			"local __scratch_util = TS.import(script, script.Parent, \"_scratch_util\")\n" +
			"local VALUE = __scratch_util.VALUE\n" +
			"local greet = __scratch_util.greet\n" +
			"print(greet(\"x\"), VALUE)\n" +
			"return nil\n",
		// default import, single use, module WITH a real `default` export.
		"out/_scratch_default_once.luau": importsHeaderModel +
			"local greeter = TS.import(script, script.Parent, \"_scratch_util\").default\n" +
			"print(greeter())\n" +
			"return nil\n",
		// namespace import: whole module table, never elided.
		"out/_scratch_ns.luau": importsHeaderModel +
			"local util = TS.import(script, script.Parent, \"_scratch_util\")\n" +
			"print(util.VALUE)\n" +
			"return nil\n",
		// type-only import: the import line is ABSENT (early-out, no TS line
		// either since nothing used the runtime lib).
		"out/_scratch_typeonly.luau": "-- Compiled with roblox-ts v3.0.0\n" +
			"local n = 1\n" +
			"print(n)\n" +
			"return nil\n",
		// THE elision proof: a VALUE import referenced only in a type
		// position — IsReferencedAliasDeclaration is false on the checker
		// that semantically checked the file, so the whole import (and the
		// runtime lib) vanish.
		"out/_scratch_elide.luau": "-- Compiled with roblox-ts v3.0.0\n" +
			"local s = \"types\"\n" +
			"print(s)\n" +
			"return nil\n",
		// side-effect import: bare TS.import CallStatement.
		"out/_scratch_sideeffect.luau": importsHeaderModel +
			"TS.import(script, script.Parent, \"_scratch_util\")\n" +
			"return nil\n",
		// import-equals (external module reference): whole module table,
		// no .default unwrapping ever.
		"out/_scratch_eq.luau": importsHeaderModel +
			"local util = TS.import(script, script.Parent, \"_scratch_util\")\n" +
			"print(util.VALUE)\n" +
			"return nil\n",
		"out/_scratch_eqchain.luau": importsHeaderModel +
			"local VfxForge = TS.import(script, script.Parent, \"_scratch_eqchain_index\")\n" +
			"VfxForge.emit(\"effect\")\n" +
			"return nil\n",
		// import-equals (entity name): plain aliasing, no import machinery.
		"out/_scratch_eqns.luau": "-- Compiled with roblox-ts v3.0.0\n" +
			"local A = Ambient\n" +
			"print(A.val)\n" +
			"return nil\n",
		// synthetic default (allowSyntheticDefaultImports over an `export =`
		// d.ts module): the default binding receives the WHOLE module table.
		"out/_scratch_synthmain.luau": importsHeaderModel +
			"local synth = TS.import(script, script.Parent, \"_scratch_synth\")\n" +
			"print(synth())\n" +
			"return nil\n",
		// dynamic import(): TS.Promise.new(function(resolve) ... end).
		"out/_scratch_dynamic.luau": importsHeaderModel +
			"local p = TS.Promise.new(function(resolve)\n" +
			"\tresolve(TS.import(script, script.Parent, \"_scratch_util\"))\n" +
			"end)\n" +
			"print(p)\n" +
			"return nil\n",
		// node_modules import in a Model project: nodeModulesPathMapping
		// (index.d.ts -> out/init.lua), scope-after-node_modules rbxPath
		// check, then the regular relative pipeline.
		"out/_scratch_pkg.luau": importsHeaderModel +
			"local dummy = TS.import(script, script.Parent, \"node_modules\", \"@rbxts\", \"dummy\", \"out\").dummy\n" +
			"print(dummy())\n" +
			"return nil\n",
		// nested source file: leading Parent segments fold into one
		// script.Parent.Parent root expression.
		"out/_scratch_sub/inner.luau": "-- Compiled with roblox-ts v3.0.0\n" +
			"local TS = require(script.Parent.Parent.include.RuntimeLib)\n" +
			"local VALUE = TS.import(script, script.Parent.Parent, \"_scratch_util\").VALUE\n" +
			"print(VALUE)\n" +
			"return nil\n",
		// the type-provider module itself (type-only export elides).
		"out/_scratch_types.luau": "-- Compiled with roblox-ts v3.0.0\n" +
			"local tag = \"types\"\n" +
			"return {\n" +
			"\ttag = tag,\n" +
			"}\n",
	}

	for name, want := range wants {
		if got, ok := files[name]; !ok {
			t.Errorf("%s: missing from CompileProject output (%v)", name, keys(files))
		} else if got != want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
	if len(files) != len(wants) {
		t.Errorf("produced %d files, want %d (%v)", len(files), len(wants), keys(files))
	}
}

// TestCompileProjectImportsGame covers the Game-project paths: OutToOut/
// InToOut file relations resolve to ABSOLUTE game:GetService chains.
func TestCompileProjectImportsGame(t *testing.T) {
	files := compileRuntimeLibProject(t, "imports_game")

	header := "-- Compiled with roblox-ts v3.0.0\n" +
		"local TS = require(game:GetService(\"ReplicatedStorage\"):WaitForChild(\"include\"):WaitForChild(\"RuntimeLib\"))\n"
	wants := map[string]string{
		"out/_scratch_once.luau": header +
			"local g = TS.import(script, game:GetService(\"ReplicatedStorage\"), \"out\", \"_scratch_util\").greet\n" +
			"print(g(\"once\"))\n" +
			"return nil\n",
		"out/_scratch_pkg.luau": header +
			"local dummy = TS.import(script, game:GetService(\"ReplicatedStorage\"), \"node_modules\", \"@rbxts\", \"dummy\", \"out\").dummy\n" +
			"print(dummy())\n" +
			"return nil\n",
		// dynamic import skips the server-import network check (RunService
		// guards may protect it at runtime) but still resolves absolutely.
		"out/_scratch_dynamic.luau": header +
			"local p = TS.Promise.new(function(resolve)\n" +
			"\tresolve(TS.import(script, game:GetService(\"ReplicatedStorage\"), \"out\", \"_scratch_util\"))\n" +
			"end)\n" +
			"print(p)\n" +
			"return nil\n",
		"out/_scratch_sub/inner.luau": header +
			"local VALUE = TS.import(script, game:GetService(\"ReplicatedStorage\"), \"out\", \"_scratch_util\").VALUE\n" +
			"print(VALUE)\n" +
			"return nil\n",
	}
	for name, want := range wants {
		if got, ok := files[name]; !ok {
			t.Errorf("%s: missing from CompileProject output (%v)", name, keys(files))
		} else if got != want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
}

// TestCompileProjectImportsPackage covers the Package-project paths:
// node_modules imports resolve through the per-typeRoot synthetic resolvers
// into TS.getModule chains; project-relative imports stay relative; the
// runtime lib is _G[script].
func TestCompileProjectImportsPackage(t *testing.T) {
	files := compileRuntimeLibProject(t, "imports_package")

	header := "-- Compiled with roblox-ts v3.0.0\n" +
		"local TS = _G[script]\n"
	wants := map[string]string{
		"out/_scratch_pkg.luau": header +
			"local dummy = TS.import(script, TS.getModule(script, \"@rbxts\", \"dummy\").out).dummy\n" +
			"print(dummy())\n" +
			"return nil\n",
		"out/_scratch_once.luau": header +
			"local g = TS.import(script, script.Parent, \"_scratch_util\").greet\n" +
			"print(g(\"once\"))\n" +
			"return nil\n",
	}
	for name, want := range wants {
		if got, ok := files[name]; !ok {
			t.Errorf("%s: missing from CompileProject output (%v)", name, keys(files))
		} else if got != want {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", name, got, want)
		}
	}
}
