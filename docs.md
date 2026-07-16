# rotor documentation

rotor is an all-in-one Roblox toolchain, written in Go. At its core is a native-speed rewrite of the [roblox-ts](https://roblox-ts.com) compiler — built on TypeScript's own native compiler — alongside a native Luau bundler, minifier, dev loop, and packer (`bundle`, `minify`, `dev`, `pack`).

rotor targets `rbxtsc` compatibility: **byte-identical Luau output**, the same `@rbxts/*` npm ecosystem, and the same CLI shape, at roughly **10x the speed** on the native TypeScript compiler.

- [Why](#why)
- [How rotor stays 1:1](#how-rotor-stays-11)
- [What works today](#what-works-today)
- [Commands](#commands)
- [Build options](#build-options)
- [Production readiness](#production-readiness)
- [Architecture](#architecture)
- [Roadmap](#roadmap)
- [Credits & licenses](#credits--licenses)

## Why

roblox-ts is a brilliant compiler with one structural problem: it runs on the JavaScript TypeScript compiler API. Every build boots Node, parses, binds, and typechecks your entire project in single-threaded JS. Watch-mode rebuilds, cold builds, startup — all of it is slow, and it gets slower as your game grows.

You can't fix that with a syntax transpiler (SWC/esbuild-style), because roblox-ts's emit is **type-directed**: `for...of` compiles differently for an `Array` vs a `Map` vs a string, `+` becomes `+` or `..` by operand type, truthiness guards depend on whether a type can be `0` or `""`, and the entire macro system resolves through the type checker. No types, no correct Luau.

The unlock is [**typescript-go**](https://github.com/microsoft/typescript-go) — Microsoft's official native port of the full TypeScript compiler (shipping as TypeScript 7), ~10x faster with parallel checking. It's the only native implementation of the *real* checker in existence. rotor ports roblox-ts's emit layer to Go on top of it.

## How rotor stays 1:1

Compatibility isn't a hope — it's enforced by construction:

- **Differential testing**: every emitted `.luau` file is byte-compared against `rbxtsc` 3.0.0's output — 43 committed fixture goldens run on every `go test`, and a real 95-file production game compiles 95/95 byte-identical.
- **Behavioral conformance**: roblox-ts's vendored runtime suite, compiled by rotor and executed under [Lune](https://github.com/lune-org/lune). The in-repo corpus and harnesses (`testdata/conformance`, `internal/conformance`) are fully enabled today: all 44 upstream golden fixtures are byte-for-byte green, the full vendored diagnostics corpus passes, the Lune suite currently reports `460 passed, 0 failed, 0 skipped`, and the real `randomness` acceptance compare is byte-for-byte green when pointed at a local checkout.
- **Faithful porting**: the reference sources are vendored in-repo (`reference/`), and ports are reviewed line-by-line against them — down to quirks like ECMAScript `Number::toString` formatting and temp-identifier collision naming.
- **Same runtime**: `RuntimeLib.lua` and `Promise.lua` are reused verbatim from roblox-ts — zero behavioral drift at runtime.

Your existing project — `tsconfig.json`, `default.project.json`, `node_modules/@rbxts/*`, transformer plugins like Flamework — is the compatibility target, unchanged.

## What works today

rotor **compiles multi-file TypeScript projects to byte-identical Luau** across the full language surface: imports with Rojo-aware require chains, JSX (`@rbxts/react`), classes and decorators, async/generators, try/catch, enums and namespaces, spread, functions, closures, destructuring, the full macro tables (`Array.map`, `string.format`, `Map.get`, ...), optional chaining, Map/Set/string/generator iteration, switch, `new` — verified continuously against real `rbxtsc` output (43/43 differential fixtures; **all 95 files of a real production game compile byte-identical**, zero divergent). It also **natively typechecks and watches real rbxts projects**.

Anything not yet ported fails loudly with a clear "not yet supported" diagnostic — rotor **never silently emits wrong output**. Everything that compiles is byte-identical to `rbxtsc` 3.0.0.

### rotor extensions (superset of rbxtsc)

These compile under rotor but not under rbxtsc; everything rbxtsc accepts is still byte-identical:

- **`$getModuleTree` on folders** — rbxtsc requires the specifier to resolve as a module, so pointing it at a folder only works if the folder has an `index.ts`. rotor resolves folder specifiers directly: relative ones (`"./systems"`) against the importing file, non-relative ones against `baseUrl`/`paths` (`"shared/systems"`) and then the project root (`"src/shared/systems"`). The usual server-import/isolation guards still apply.
- **`$env` compile-time environment macro** — a built-in replacement for the `rbxts-transform-env` plugin (no Node sidecar, no plugin install, no typings package). `$env("GAME_NAME")` inlines the variable's value as a Luau string literal (or `nil` when unset), `$env("GAME_NAME", "fallback")` inlines the value or the fallback, and `$env.GAME_NAME` / `$env["GAME_NAME"]` behave like the 1-arg call. Values resolve at compile time with priority **process environment > `.env.<NODE_ENV>` > `.env`** (files live next to your `tsconfig.json`; `NODE_ENV` itself resolves from the process env, then `.env`). The `.env` format is `KEY=VALUE` lines with `#` comments and optional single/double quotes; v1 inlines strings only. The type surface (`declare const $env: ...`) is injected automatically — no `.d.ts` needed — and names/fallbacks must be string literals so the value can be baked in (dynamic names get a clear diagnostic). For editors (which never see the injected declaration), rotor writes a single on-disk **`rotor.d.ts`** companion covering every macro (see below). If you migrate from `rbxts-transform-env`, drop the plugin from your tsconfig; its modern module-export form (`import { $env } from "rbxts-transform-env"`) still works through the plugin sidecar and never collides with the built-in global.
- **`$asset` compile-time asset macro** — the headline rotor 2.0 feature. `$asset("assets/logo.png")` inlines a Luau string `"rbxassetid://<id>"`, resolving the file's content hash to a Roblox asset id through the committed lockfile (**`rotor-lock.json`**). The single argument is a string literal (dynamic paths get a clear diagnostic); a project-relative path is resolved against the project root (under the optional `[assets] base` directory when configured, so `base = "assets/images"` lets you write `$asset("ui/icon.png")` for `assets/images/ui/icon.png`), and a path beginning with `./` or `../` against the importing file. **Cache hit (the common case) is fully offline and deterministic** — the build reads only the lockfile and parity is unaffected. On a genuine cache miss, if `ROBLOX_API_KEY` is set and `[assets].creator` is configured, rotor uploads the file via Open Cloud, records the new id in `rotor-lock.json` (persisted atomically after a successful build), and inlines it; **offline + miss** is a clear compile error pointing at `rotor asset sync`. Missing files, bad usage (a bare `$asset`), and non-literal paths all surface diagnostics rather than panics. As with `$env`, the type surface (`declare function $asset(path: string): string;`) is injected automatically, and the shared on-disk **`rotor.d.ts`** companion (see below) carries it for editors.
- **`$nameof(expr)` compile-time name macro** — inlines the *trailing* identifier or property name of an expression as a Luau string literal: `$nameof(player.Humanoid.Health)` → `"Health"`, `$nameof(foo)` → `"foo"`. The argument's source is read but never evaluated (it produces no runtime code), so it stays in sync with refactors/renames. An expression with no statically-knowable trailing name (an index, a call, a literal) gets a clear diagnostic. Type: `declare function $nameof(item: unknown): string;`
- **`$keys<T>()` compile-time keys macro** — inlines an array of `T`'s string keys as a Luau array literal, using the type checker (the `rbxts-transformer-keys` staple): `$keys<{ x: number; y: string }>()` → `{ "x", "y" }`. Keys come from the type's apparent/declared string properties in declaration order; number/symbol keys are skipped; a type with no enumerable string keys (e.g. `{}`) yields an empty array (a valid result, not an error). A missing type argument is a diagnostic. Type: `declare function $keys<T>(): string[];` (the generic is consumed at compile time; the macro fills the array).
- **`$file(path)` compile-time file macro** — inlines a project file's parsed contents as a Luau **value** at compile time. A `.json` file becomes a Luau table/array/scalar literal (int vs float precision preserved; a JSON `null` object member is dropped — a nil Luau field is absent — while a `null` array element becomes `nil`); any other text file (`.txt`, `.md`, …) becomes a Luau string literal of its raw contents. Path resolution mirrors `$asset`: project-relative, or `./`/`../` relative to the importing file. The path must be a string literal; a missing file or invalid JSON is a clear diagnostic. `$file` is a pure function of the file's bytes (parity-safe and cacheable) — editing the data file changes the output, and incremental rebuild handles it. Type: `declare function $file(path: string): any;`
- **`$git(field)` + `$buildTime()` build/VCS stamping macros** — `$git("sha")` inlines the short 7-character commit hash, `$git("branch")` the current branch (`""` in detached HEAD), `$git("tag")` the nearest tag pointing at HEAD (`""` if none), each as a string literal; `$git("dirty")` inlines a boolean for whether the working tree has uncommitted changes. `$buildTime()` inlines an ISO-8601 timestamp of the build. The git data is read natively from `.git` (HEAD → ref → sha, branch from HEAD, tags from `refs/tags` + `packed-refs`); the dirty check shells out to `git status --porcelain` and degrades to `false` when git or `.git` is absent. **Outside a git repo** every `$git` field is empty/`false` — never an error. **Determinism:** `$git` is *stable within a commit + working tree*, so its files rebuild to identical bytes; `$buildTime` is **intentionally non-deterministic** — it stamps the current time and *should* bust incremental caching for files that use it, so use it sparingly. Types: `declare function $git(field: "sha" | "branch" | "tag"): string;` `declare function $git(field: "dirty"): boolean;` `declare function $buildTime(): string;`

**Editor types — one `rotor.d.ts` for every macro.** All of rotor's macros (`$env`, `$asset`, `$nameof`, `$keys`, `$file`, `$git`, `$buildTime`) share a single on-disk **`rotor.d.ts`** companion. The compiler injects identical declarations in-memory, so this file exists purely for editors/IDEs (which never see the synthetic copies). `rotor init` scaffolds it and lists it in the tsconfig `include`; `rotor build` / `rotor check` / `rotor asset` write or refresh it whenever the project references any macro; the compiler skips its synthetic copies when the on-disk file is in the program, so the two never collide. If a macro red-squiggles in your editor, add `rotor.d.ts` to your tsconfig `include`. `rotor clean --types` removes it (and the legacy `rotor-env.d.ts` / `rotor-asset.d.ts` / `rotor-macros.d.ts` from projects scaffolded before the consolidation).

## Commands

```
rotor check [path] [-w]       typecheck the project (native, full strictness)
rotor build [options] [path]  compile the project to Luau
rotor doctor [path]           diagnose the setup: tsconfig, @rbxts packages,
                              Node.js + transformer plugins, Rojo wiring
rotor minify <file> [-o out] [--no-index-field]
                              minify a Luau file (strips comments + whitespace,
                              collapses t["x"] to t.x, keeps --! directives)
rotor bundle <entry> [-o out] [--minify]
                              inline a Luau require graph into one runnable file
rotor dev [path] [--no-serve] watch + incrementally compile, and serve to Studio
                              via `rojo serve` (the dev inner loop)
rotor pack [path] [--as luau|rbxmx|rbxm] [-o out] [--entry inst.path] [--rojo-tree]
                              package a Rojo project into one self-reconstructing
                              Luau script or a Roblox model file
rotor init [dir] [--template game|package|plain]
                              scaffold a new project (rbxts game, package library,
                              or plain Luau)
rotor sourcemap [path] [-o out.json]
                              emit a Rojo-compatible sourcemap.json for luau-lsp
rotor asset <sync|list> [path] [--dry-run]
                              upload assets via Open Cloud: lockfile + typed
                              assets.luau / assets.d.ts codegen (asphalt-style),
                              or the $asset macro companion in "macro" mode
rotor deploy <plan|apply> [path] -e <env> [--yes] [--allow-deletes]
                              declarative Open Cloud deployment with state +
                              plan/apply diffing (mantle-style); manages place
                              files + place settings, experience settings,
                              badges, game passes, icon assets, experience
                              icon + thumbnails, developer products, and
                              social links
```

`asset` and `deploy` are configured by a typed **`rotor.config.ts`** at the project root (evaluated natively — no Node needed; `rotor init` writes the skeleton plus `rotor-config.d.ts` for editor typing) and authenticate with an Open Cloud key in `ROBLOX_API_KEY`. See the [cloud toolchain spec](docs/superpowers/specs/2026-06-12-rotor-cloud-toolchain-design.md) for the full config shape.

**Asset delivery modes** (`[assets] mode`): a project picks one way assets reach Luau, both sharing the same scan/hash/upload pipeline and `rotor-lock.json` cache.

- **`"module"`** (default): `rotor asset sync` uploads the configured `paths` and regenerates the typed accessor module (`assets.luau` + `assets.d.ts`) from the lockfile — the asphalt-style 1.x behaviour. The `$asset` macro still works.
- **`"macro"`**: `rotor asset sync` uploads `paths` and maintains the lockfile + the `rotor.d.ts` editor companion (no `assets.luau`); the `$asset` macro is the consumption path, with build-time auto-upload filling any gaps. Image uploads (Open Cloud creates a **Decal**) are automatically unwrapped to the underlying **Image** asset id — the decal id doesn't render in image properties like `ImageLabel.Image`; the unwrap uses the asset delivery API, so the key needs the **legacy-assets** scope alongside Assets R/W. The macro itself works in either mode — `mode` only changes what `sync` emits.

- `path` is a project directory containing a `tsconfig.json` (defaults to the current directory).
- Your project needs `node_modules` installed (rotor reads the same `@rbxts` types).
- Exit codes: `0` = success, `1` = any failure (diagnostics, config, or usage) — matching upstream `rbxtsc`.
- Plugin-backed builds need Node.js at runtime for the transformer sidecar.

`rotor build` compiles every file in the project, writes the `.luau` outputs to your tsconfig's `outDir` exactly where `rbxtsc` would put them, runs the cleanup/copy pipeline, emits `.d.ts` files when `compilerOptions.declaration` is enabled, and copies `include/` (RuntimeLib.lua, Promise.lua — verbatim from roblox-ts). Try it on rotor's own test fixture project:

```powershell
bun install --cwd testdata/diff/project --no-save
rotor build testdata/diff/project
# out/01_literals.luau
# ...
# compiled 43 files in 189 ms
```

A standalone `.ts` file isn't compilable by itself — like `rbxtsc`, rotor needs the rbxts project around it (`package.json` with `@rbxts/compiler-types` + `@rbxts/types` installed, `tsconfig.json`, `default.project.json`). The fixture project above is a minimal working example of that setup.

## Build options

`rotor build` accepts the full rbxtsc flag surface (booleans accept `--flag`, `--flag=false`, `--no-flag`): `-p/--project`, `-w/--watch`, `--usePolling`, `--verbose`, `--noInclude`, `--logTruthyChanges`, `--writeOnlyChanged`, `--optimizedLoops`, `--type game|model|package`, `-i/--includePath`, `--rojo`, `--allowCommentDirectives`, `--luau`, plus rotor's own `--cpuprofile`. Run `rotor --help` for details.

Options may also be set under the top-level `"rbxts"` key of `tsconfig.json`; merge order: defaults < rbxts < command line.

**rotor DX extensions** (not in rbxtsc; safe to ignore for parity):

- **`--minify`** — pass every emitted `.luau`/`.lua` source through the Luau minifier (comment/whitespace stripping + `t["x"]` → `t.x`) before writing. Semantics-preserving and opt-in, so a default build stays byte-identical to rbxtsc; declaration and `include/` files are never minified.
- **Code-frame diagnostics** — TypeScript, transformer, and macro errors render as grouped code frames (source line + caret/underline, keyword highlighting, OSC 8 file links, an `✗ N errors in M files` footer). `--max-errors <n>` caps the rendered frames (default 50; `0` = all). In watch mode the screen clears before each rebuild (opt out with `--no-clear`), a `✗ N errors` banner persists on the idle line until the next green build, and `--bell` rings the terminal on a fail↔pass transition.
- **`--json`** — emit one machine-readable result object (version, ok, files, durationMs, diagnostics with `file`/`line`/`col`/`severity`/`message`) instead of styled output. Also available on `rotor check`.

## Production readiness

rotor is ready for production rbxts projects that want native-speed `check`, `check -w`, `build`, and `build -w`, including declaration emit, incremental rebuild selection, and transformer-plugin support through the bundled Node sidecar. Plugin-configured builds require Node.js on `PATH` so rotor can launch that sidecar.

Notes and current caveats (see the [roadmap](roadmap.md)):

- `build -w` reuses rotor's manifest-backed changed-file selection and runs a debounced, pruned polling watcher: `node_modules`, dot-directories, and the build-written `out`/`include` trees are never walked, editor write bursts ("save all") settle into one rebuild, edits made *during* a build are not lost, and editor junk files never trigger rebuilds. The poll adapts to the walk cost (100 ms floor), so idle watch CPU stays near zero even on big projects. Native FS events remain a possible future refinement.
- Declaration emit is available for declaration-enabled builds, but declaration-path alias rewriting still follows the current Phase 4 limitation called out in the roadmap.
- Transformer plugins run through the Node sidecar that ships **embedded in the rotor binary** (extracted on first plugin build). The worker uses your project's own `typescript` install — the same instance plugins `require` — and stays warm across builds and watch rebuilds. Validated against real `rbxts-transformer-flamework` and `rbxts-transform-env` packages.
- The conformance harnesses are in repo and green today. The external-project acceptance proof is environment-gated because it needs a local `randomness` checkout plus Rojo/Lune on the machine running it.

## Architecture

```
your-game/src/**/*.ts
        │
        ▼
┌─────────────────────────────┐
│  typescript-go  (vendored)  │   real TS parser + binder + checker,
│  parse · bind · typecheck   │   native, parallel  (tsgo/)
└──────────────┬──────────────┘
               │  typed AST + TypeChecker queries
               ▼
┌─────────────────────────────┐
│  rotor transformer          │   port of roblox-ts's TSTransformer
│  TS AST ──► Luau AST        │   (internal/transformer)
└──────────────┬──────────────┘
               ▼
┌─────────────────────────────┐
│  Luau AST + renderer        │   port of @roblox-ts/luau-ast
│  byte-exact Luau text       │   (internal/luau)
└──────────────┬──────────────┘
               ▼
        out/**/*.lua   (+ RuntimeLib.lua, verbatim from roblox-ts)
```

- `tsgo/` — generated mirror of [microsoft/typescript-go](https://github.com/microsoft/typescript-go) internals (its packages are `internal/`-only upstream; the mirror rewrites import paths). Regenerate with `go run ./tools/mirror`. **Never edit by hand.**
- `reference/` — pinned roblox-ts v3.0.0 + luau-ast 2.0.0 sources: the porting reference and differential-test oracle.
- `internal/luau`, `internal/luau/render` — the Luau AST and renderer.
- `internal/luau/lex`, `internal/luau/cst` — the Luau lexer and lossless CST/parser powering `minify`, `bundle`, and `pack`.
- `internal/version` — the single source of truth for rotor's release version.
- `cmd/rotor` — the CLI.

## Roadmap

| Phase | Scope | Status |
|:-----:|-------|:------:|
| **0** | Foundation — Go module, vendored typescript-go mirror, TypeChecker driven from Go | ✅ |
| **1** | Luau AST + renderer — full port of `@roblox-ts/luau-ast` (40 node kinds, temp-id solver, byte-exact formatting) | ✅ |
| **2** | Transformer core — `TransformState`, prereq statement stack, core expression/statement transforms, **differential harness vs rbxtsc** | ✅ |
| **2b** | Functions, arrows, destructuring, `for...of` (arrays), switch, loop closure semantics | ✅ |
| **3a** | Imports & module resolution (Rojo-aware requires, `TS.import`/`TS.getModule`, export-from), `new` + constructor macros, math-op macros | ✅ |
| **3b** | Macro tables (`Array`/`String`/`Set`/`Map`/`Promise` + call macros), optional chaining, full Map/Set/string/generator iteration, pnpm symlink + `baseUrl` resolution | ✅ |
| **3c** | JSX (`@rbxts/react`), classes, decorators, object/array/call spread + logical assignments, async/generators, try/catch flow rerouting, enums, namespaces | ✅ |
| **4** | Project layer — output pipeline, `.d.ts` emit, watch, plugin/concurrency integration | ✅ |
| **5** | Conformance — upstream behavioral suite under Lune, diagnostics corpus, acceptance closure | ✅ |
| | **v1.0** — drop-in `rbxtsc` replacement | ✅ |
| **v2** | Luau toolchain — lexer, lossless CST/parser, `minify`, `bundle`, `dev`, `pack` MVPs | ✅ |

The full roadmap with every phase and task lives in [`roadmap.md`](roadmap.md).

## Credits & licenses

rotor stands on two giants:

- [**roblox-ts**](https://github.com/roblox-ts/roblox-ts) (MIT) — the original compiler, whose emit semantics rotor faithfully ports. Vendored reference sources in `reference/` retain their MIT license.
- [**typescript-go**](https://github.com/microsoft/typescript-go) (Apache-2.0) — Microsoft's native TypeScript compiler. The vendored mirror in `tsgo/` retains its license and NOTICE; see `tsgo/MIRROR.md` for provenance and the statement of changes.

rotor itself is [MIT licensed](LICENSE).
