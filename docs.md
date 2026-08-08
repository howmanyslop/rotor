# rotor documentation

rotor is an all-in-one Roblox toolchain, written in Go. At its core is a native-speed rewrite of the [roblox-ts](https://roblox-ts.com) compiler — built on TypeScript's own native compiler — alongside a native Luau bundler, minifier, dev loop, and packer (`bundle`, `minify`, `dev`, `pack`).

rotor targets `rbxtsc` compatibility: upstream-parity Luau output on unaffected surfaces, the same `@rbxts/*` npm ecosystem, and the same CLI shape, at roughly **10x the speed** on the native TypeScript compiler. For fork-changed surfaces, the verified `@isentinel/roblox-ts@4.0.11` archive in `roblox-ts.zip` is the authority.

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

The unlock is [**typescript-go**](https://github.com/microsoft/typescript-go) — Microsoft's official native port of the full TypeScript compiler (shipping as TypeScript 7), ~10x faster with parallel checking and configurable concurrency controls. It's the only native implementation of the *real* checker in existence. rotor ports roblox-ts's emit layer to Go on top of it.

## How rotor stays 1:1

Compatibility isn't a hope — it's enforced by construction:

- **Differential testing**: unaffected compiler surfaces remain byte-compared against `rbxtsc` 3.0.0 output. Fork-changed surfaces are compared against the verified `@isentinel/roblox-ts@4.0.11` archive, including solution builds, watch, source maps, copy-file gating, resolver caches, and the fork's optimized emit paths.
- **Behavioral conformance**: roblox-ts's vendored runtime suite, compiled by rotor and executed under [Lune](https://github.com/lune-org/lune). The in-repo corpus and harnesses (`testdata/conformance`, `internal/conformance`) cover both the upstream reference and the verified fork fixtures; fork-divergent goldens are kept fork-authoritative rather than presented as upstream matches.
- **Faithful porting**: the reference sources are vendored in-repo (`reference/`), and ports are reviewed line-by-line against them — down to quirks like ECMAScript `Number::toString` formatting and temp-identifier collision naming.
- **Same runtime**: `RuntimeLib.lua` and `Promise.lua` match the targeted roblox-ts compiler runtime.

Your existing project — `tsconfig.json`, `default.project.json`, `node_modules/@rbxts/*`, transformer plugins like Flamework — is the compatibility target, unchanged. The compatibility oracle depends on the surface: use the fork archive for the changed surfaces above and upstream parity elsewhere.

## What works today

rotor **compiles multi-file TypeScript projects with upstream or fork-authoritative parity** across the language surface: imports with Rojo-aware require chains, JSX (`@rbxts/react`), classes and decorators, async/generators, try/catch, enums and namespaces, spread, functions, closures, destructuring, the full macro tables (`Array.map`, `string.format`, `Map.get`, ...), optional chaining, Map/Set/string/generator iteration, switch, and `new`. It also **natively typechecks and watches real rbxts projects**.

Anything not yet ported fails loudly with a clear "not yet supported" diagnostic — rotor **never silently emits wrong output**. On unaffected surfaces, compiled output remains byte-identical to `rbxtsc` 3.0.0; fork-changed surfaces follow the verified fork behavior instead.

### rotor extensions (superset of rbxtsc)

These compile under rotor but not under rbxtsc. Unaffected code accepted by rbxtsc remains byte-identical; fork-changed surfaces follow the verified fork archive:

- **Loop labels (`outer: for (...) { break outer; continue outer; }`)** — rbxtsc rejects every labeled statement with `labels are not supported!`. rotor compiles them. Luau has no labels and no `goto`, so a label that is not already satisfied by a plain `break`/`continue` lowers to a `"none" | "break" | "continue"` string flag, with `if _outer == "break" then break end` style checks emitted after each enclosing loop to unwind outwards. Labels work on `while`, `do`/`while`, both `for` shapes, every `for-of` shape (including `$range`), and on `switch`; a label on a non-loop statement (a block, an `if`) is wrapped in `repeat ... until true` so it can be broken out of. The flag variable and its per-iteration reset are emitted only when actually needed, so an unused label costs nothing. **Not supported:** a label crossing a `try`/`catch` (the `TS.TRY_BREAK`/`TRY_CONTINUE` reroute carries no label) and a labeled non-loop statement that also contains an *unlabeled* `break`/`continue` aimed at an enclosing loop — both are clear diagnostics, never wrong output.
- **Named function expressions (`useEffect(function onName() { ... }, [name])`)** — rbxtsc rejects every one of them with `Function expression names are not supported!`. rotor compiles them, in **any** expression position: a call argument, an assignment right-hand side, a `const`/`let` initializer, an object-literal value, a parameter default, a ternary arm, a short-circuit operand, an IIFE callee. The name is lifted to a `local function <name>() ... end` declaration emitted immediately before the expression, and the expression becomes a reference to it — which is the only Luau form that carries a **debug name into a traceback**; a function expression assigned to anything, including a plain `local`, compiles to an unnamed closure. Recursion through the name works, because the name resolves to the lifted declaration rather than to the outer binding. A name that would collide with something already in scope is renamed (`letNamed` → `letNamed_1`), so lifting never shadows. A conditionally evaluated operand keeps its declaration *inside* the branch, so the closure is still built only when the expression is reached. **Not supported:** `async` and generator function expressions — their name binds to the `TS.async`/`TS.generator` wrapper rather than to the lifted closure, so a self-reference would call the wrong function; both keep the diagnostic. This also matters for **transformer plugins**: one that relocates a hook argument into a `memo = function onName() {}` assignment no longer breaks the build.
- **`$getModuleTree` on folders** — rbxtsc requires the specifier to resolve as a module, so pointing it at a folder only works if the folder has an `index.ts`. rotor resolves folder specifiers directly: relative ones (`"./systems"`) against the importing file, non-relative ones against `baseUrl`/`paths` (`"shared/systems"`) and then the project root (`"src/shared/systems"`). The usual server-import/isolation guards still apply.
- **`$env` compile-time environment macro** — a built-in replacement for the `rbxts-transform-env` plugin (no Node sidecar, no plugin install, no typings package). `$env("GAME_NAME")` inlines the variable's value as a Luau string literal (or `nil` when unset), `$env("GAME_NAME", "fallback")` inlines the value or the fallback, and `$env.GAME_NAME` / `$env["GAME_NAME"]` behave like the 1-arg call. Values resolve at compile time with priority **process environment > `.env.<NODE_ENV>` > `.env`** (files live next to your `tsconfig.json`; `NODE_ENV` itself resolves from the process env, then `.env`). The `.env` format is `KEY=VALUE` lines with `#` comments and optional single/double quotes; v1 inlines strings only. The type surface (`declare const $env: ...`) is injected automatically — no `.d.ts` needed — and names/fallbacks must be string literals so the value can be baked in (dynamic names get a clear diagnostic). For editors (which never see the injected declaration), rotor writes a single on-disk **`rotor.d.ts`** companion covering every macro (see below). If you migrate from `rbxts-transform-env`, drop the plugin from your tsconfig; its modern module-export form (`import { $env } from "rbxts-transform-env"`) still works through the plugin sidecar and never collides with the built-in global.
- **`$asset` compile-time asset macro** — the headline rotor 2.0 feature. `$asset("assets/logo.png")` inlines a Luau string `"rbxassetid://<id>"`, resolving the file's content hash to a Roblox asset id through the committed lockfile (**`rotor-lock.json`**). The single argument is a string literal (dynamic paths get a clear diagnostic); a project-relative path is resolved against the project root (under the optional `[assets] base` directory when configured, so `base = "assets/images"` lets you write `$asset("ui/icon.png")` for `assets/images/ui/icon.png`), and a path beginning with `./` or `../` against the importing file. **Cache hit (the common case) is fully offline and deterministic** — the build reads only the lockfile and parity is unaffected. On a genuine cache miss, if `ROBLOX_API_KEY` is set and `[assets].creator` is configured, rotor uploads the file via Open Cloud, records the new id in `rotor-lock.json` (persisted atomically after a successful build), and inlines it; **offline + miss** is a clear compile error pointing at `sloptor asset sync`. Missing files, bad usage (a bare `$asset`), and non-literal paths all surface diagnostics rather than panics. As with `$env`, the type surface (`declare function $asset(path: string): string;`) is injected automatically, and the shared on-disk **`rotor.d.ts`** companion (see below) carries it for editors.
- **`$nameof(expr)` compile-time name macro** — inlines the *trailing* identifier or property name of an expression as a Luau string literal: `$nameof(player.Humanoid.Health)` → `"Health"`, `$nameof(foo)` → `"foo"`. The argument's source is read but never evaluated (it produces no runtime code), so it stays in sync with refactors/renames. An expression with no statically-knowable trailing name (an index, a call, a literal) gets a clear diagnostic. Type: `declare function $nameof(item: unknown): string;`
- **`$keys<T>()` compile-time keys macro** — inlines an array of `T`'s string keys as a Luau array literal, using the type checker (the `rbxts-transformer-keys` staple): `$keys<{ x: number; y: string }>()` → `{ "x", "y" }`. Keys come from the type's apparent/declared string properties in declaration order; number/symbol keys are skipped; a type with no enumerable string keys (e.g. `{}`) yields an empty array (a valid result, not an error). A missing type argument is a diagnostic. Type: `declare function $keys<T>(): string[];` (the generic is consumed at compile time; the macro fills the array).
- **`$file(path)` compile-time file macro** — inlines a project file's parsed contents as a Luau **value** at compile time. A `.json` file becomes a Luau table/array/scalar literal (int vs float precision preserved; a JSON `null` object member is dropped — a nil Luau field is absent — while a `null` array element becomes `nil`); any other text file (`.txt`, `.md`, …) becomes a Luau string literal of its raw contents. Path resolution mirrors `$asset`: project-relative, or `./`/`../` relative to the importing file. The path must be a string literal; a missing file or invalid JSON is a clear diagnostic. `$file` is a pure function of the file's bytes (parity-safe and cacheable) — editing the data file changes the output, and incremental rebuild handles it. Type: `declare function $file(path: string): any;`
- **`$git(field)` + `$buildTime()` build/VCS stamping macros** — `$git("sha")` inlines the short 7-character commit hash, `$git("branch")` the current branch (`""` in detached HEAD), `$git("tag")` the nearest tag pointing at HEAD (`""` if none), each as a string literal; `$git("dirty")` inlines a boolean for whether the working tree has uncommitted changes. `$buildTime()` inlines an ISO-8601 timestamp of the build. The git data is read natively from `.git` (HEAD → ref → sha, branch from HEAD, tags from `refs/tags` + `packed-refs`); the dirty check shells out to `git status --porcelain` and degrades to `false` when git or `.git` is absent. **Outside a git repo** every `$git` field is empty/`false` — never an error. **Determinism:** `$git` is *stable within a commit + working tree*, so its files rebuild to identical bytes; `$buildTime` is **intentionally non-deterministic** — it stamps the current time and *should* bust incremental caching for files that use it, so use it sparingly. Types: `declare function $git(field: "sha" | "branch" | "tag"): string;` `declare function $git(field: "dirty"): boolean;` `declare function $buildTime(): string;`

**Editor types — one `rotor.d.ts` for every macro.** All of rotor's macros (`$env`, `$asset`, `$nameof`, `$keys`, `$file`, `$git`, `$buildTime`) share a single on-disk **`rotor.d.ts`** companion. The compiler injects identical declarations in-memory, so this file exists purely for editors/IDEs (which never see the synthetic copies). `sloptor init` scaffolds it and lists it in the tsconfig `include`; `sloptor build` / `sloptor check` / `sloptor asset` write or refresh it whenever the project references any macro; the compiler skips its synthetic copies when the on-disk file is in the program, so the two never collide. If a macro red-squiggles in your editor, add `rotor.d.ts` to your tsconfig `include`. `sloptor clean --types` removes it (and the legacy `rotor-env.d.ts` / `rotor-asset.d.ts` / `rotor-macros.d.ts` from projects scaffolded before the consolidation).

## Commands

```
sloptor check [path] [-w]       typecheck the project (native, full strictness)
sloptor build [options] [path]  compile the project to Luau
sloptor diagnostics [options] [path]
                              report EVERY file's outcome instead of stopping at
                              the first failure, optionally over in-memory source
                              overlays read as JSON on stdin; writes nothing.
                              --build [path] censuses a whole solution
sloptor doctor [path]           diagnose the setup: tsconfig, @rbxts packages,
                              Node.js + transformer plugins, Rojo wiring
sloptor minify <file> [-o out] [--no-index-field]
                              minify a Luau file (strips comments + whitespace,
                              collapses t["x"] to t.x, keeps --! directives)
sloptor bundle <entry> [-o out] [--minify]
                              inline a Luau require graph into one runnable file
sloptor dev [path] [--no-serve] watch + incrementally compile, and serve to Studio
                              via `rojo serve` (the dev inner loop)
sloptor pack [path] [--as luau|rbxmx|rbxm] [-o out] [--entry inst.path] [--rojo-tree]
                              package a Rojo project into one self-reconstructing
                              Luau script or a Roblox model file
sloptor init [dir] [--template game|package|plain]
                              scaffold a new project (rbxts game, package library,
                              or plain Luau)
sloptor sourcemap [path] [-o out.json]
                              emit a Rojo-compatible sourcemap.json for luau-lsp
sloptor asset <sync|list> [path] [--dry-run]
                              upload assets via Open Cloud: lockfile + typed
                              assets.luau / assets.d.ts codegen (asphalt-style),
                              or the $asset macro companion in "macro" mode
sloptor schema [--rbxts]         print the rotor.toml schema, or the separate
                              tsconfig.json "rbxts" schema with --rbxts
sloptor clean [path] [--types] [--dry-run]
                              remove build outputs and generated editor types
sloptor add [--dev] <pkg>...     add dependencies to package.json
sloptor migrate [path] [--force]
                              convert legacy rotor.config.ts to rotor.toml
sloptor deploy <plan|apply> [path] -e <env> [--yes] [--allow-deletes]
                              declarative Open Cloud deployment with state +
                              plan/apply diffing (mantle-style); manages place
                              files + place settings, experience settings,
                              badges, game passes, icon assets, experience
                              icon + thumbnails, developer products, and
                              social links
sloptor completion <bash|zsh|fish|powershell>
                              generate a shell completion script (writes to
                              stdout; see `sloptor completion --help` for the
                              one-line install per shell)
```

`asset` and `deploy` are configured by **`rotor.toml`** at the project root and authenticate with an Open Cloud key in `ROBLOX_API_KEY`. `sloptor init` writes the hosted `rotor.schema.json` directive; use `sloptor migrate` for a legacy `rotor.config.ts`. See the [cloud toolchain spec](docs/superpowers/specs/2026-06-12-rotor-cloud-toolchain-design.md) for the full config shape.

**Asset delivery modes** (`[assets] mode`): a project picks one way assets reach Luau, both sharing the same scan/hash/upload pipeline and `rotor-lock.json` cache.

- **`"module"`** (default): `sloptor asset sync` uploads the configured `paths` and regenerates the typed accessor module (`assets.luau` + `assets.d.ts`) from the lockfile — the asphalt-style 1.x behaviour. The `$asset` macro still works.
- **`"macro"`**: `sloptor asset sync` uploads `paths` and maintains the lockfile + the `rotor.d.ts` editor companion (no `assets.luau`); the `$asset` macro is the consumption path, with build-time auto-upload filling any gaps. Image uploads (Open Cloud creates a **Decal**) are automatically unwrapped to the underlying **Image** asset id — the decal id doesn't render in image properties like `ImageLabel.Image`; the unwrap uses the asset delivery API, so the key needs the **legacy-assets** scope alongside Assets R/W. The macro itself works in either mode — `mode` only changes what `sync` emits.

- `path` is a project directory containing a `tsconfig.json` (defaults to the current directory).
- Your project needs `node_modules` installed (rotor reads the same `@rbxts` types).
- Exit codes: `0` = success, `1` = any failure (diagnostics, config, or usage) — matching upstream `rbxtsc`. The one exception is `sloptor diagnostics`, which reports rather than gates: see below.
- Plugin-backed builds need Node.js at runtime for the transformer sidecar.

`sloptor build` compiles every file in the project, writes the `.luau` outputs to your tsconfig's `outDir` exactly where `rbxtsc` would put them, runs the cleanup/copy pipeline, emits `.d.ts` files when `compilerOptions.declaration` is enabled, and copies `include/` (RuntimeLib.lua, Promise.lua — verbatim from roblox-ts). Try it on sloptor's own test fixture project:

```powershell
bun install --cwd testdata/diff/project --no-save
sloptor build testdata/diff/project
# out/01_literals.luau
# ...
# compiled 43 files in 189 ms
```

### sloptor diagnostics

`sloptor build` is four sequential gates — program-option diagnostics, the
per-file precheck, the global checker diagnostics, the transform drain — and
each returns at the first failure. The precheck gate returns *before* the
transform stage is queued, so **one type error anywhere hides every transformer
diagnostic in the project**. `sloptor check` is complete but typecheck-only: it
never runs the transformer, so it surfaces no `noAny`-class diagnostic at all.

`sloptor diagnostics` runs every file and reports each one's outcome:

| Outcome | Meaning |
|---|---|
| `ok` | transformed with no diagnostics. Not a claim that the Luau is *correct* — rotor uses types for truthiness, coercion and loop lowering, so output for a type-broken file can be silently wrong |
| `typeError` | TypeScript rejected it. It is transformed anyway |
| `transformerDiagnostic` | rotor's transformer rejected it (or it uses comment directives) |
| `internalCompilerError` | a ported upstream assert fired; the panic value and stack are reported |

```powershell
sloptor diagnostics --project tsconfig.json --json
'{"overlays":{"C:/proj/src/main.ts":"export const x = 1;\n"}}' | sloptor diagnostics --json
```

- `--project <path>` selects the config; `--checkers <n>` sets checker count.
  `-b/--build [path]` censuses a whole solution of project references, with
  `--builders <n>` — the same flags, values and `--builders requires --build`
  rule `sloptor build` uses.
- `--json` extends the `sloptor build --json` result shape with a `transformed`
  count, an `overlayMatches` count and a `fileDiagnostics` array. Diagnostic
  positions are resolved against the text that was compiled, so they stay
  correct under overlays.
- Every diagnostic carries a `code`: `TS####` for a TypeScript one, the upstream
  factory name (`noAny`, `rotorLabeledStatementFlowControl`) for a transformer
  one. Classify on it rather than on the file's `outcome` — a file can carry
  both families at once, and `outcome` reports only the most severe. The key is
  omitted where there was never a code to carry, such as a run that failed as a
  whole rather than at a diagnostic.
- Note that `files` means something different here than it does for `build`:
  for `build` it counts files **written**, for `diagnostics` it counts files
  **censused** (nothing is written).
- It is **read-only**: no `outDir`, no `include/`, no `rotor.d.ts`, and `$asset`
  resolution runs offline so a census can never upload.
- The tsconfig `rbxts` key still sets the project's shape (`type`, `rojo`,
  `includePath`), but `allowCommentDirectives` is forced off. That does **not**
  stop `@ts-ignore` from hiding type errors — a directive suppresses them
  inside the checker, and no compiler flag rotor sets undoes that. What forcing
  it off does is add rotor's own "comment directives are not supported"
  diagnostic, so a file that leans on them is at least visible as
  `transformerDiagnostic` rather than passing as `ok`. The cost is a
  divergence: a project that legitimately sets `allowCommentDirectives: true`
  gets a diagnostic here that `sloptor build` does not report.
- **Exit code, unlike `build` and `check`: 0 whenever a census was produced**,
  even one full of diagnostics; 1 only when none could be. This command reports,
  it does not gate. Read `ok` and the per-file outcomes to judge the contents.

#### Overlays

Optional **overlays** arrive as JSON on **stdin** (`{"overlays":{"<absolute
path>":"<source>"}}`) and replace those files' text for the run only. argv
cannot carry a project's worth of source, which is why this is stdin.

- **stdin must be closed**, or the reader blocks. An interactive terminal is
  detected and treated as "no overlays"; a pipe left open is not.
- Overlays **replace** files, they cannot **add** them. The overlay filesystem
  overrides `FileExists` and `ReadFile` only, not directory enumeration, so a
  key naming a path the tsconfig `include` never walks to reaches nothing.
- A key that matches **no file in the program is an error**, not a silent
  no-op. Compare `overlayMatches` against the number you sent: a green census
  of the unmodified tree is exactly the failure this prevents. Keys may use
  either separator, and match case-insensitively where the filesystem does.
  Under `--build` the check is against the **union of every project**, since a
  solution's files are split between them; `overlayMatches` counts distinct
  overlays, not per-project matches.
- Unknown top-level fields in the request are rejected, so a typo'd wrapper key
  fails instead of parsing to an empty overlay set.
- Overlays work on projects with **transformer plugins**, so you can census
  your real build tsconfig rather than authoring a plugin-free copy of it. The
  text ships to the Node sidecar as a changed file, the worker's plugins run
  against it, and the program rotor rebuilds from the worker's output keeps
  your overlay on every file the worker was not asked to transform. Declaration
  emit through `baseUrl`/`paths` resolves module names against the same view.

#### Solutions (`--build`)

`sloptor diagnostics --build [path]` censuses every project the entry tsconfig
references, transitively, in dependency order.

- A project that cannot be censused at all does **not** stop the others. A
  solution *build* blocks the dependents of a failed project, because they
  consume its missing output; a census reads no project's output (TypeScript
  redirects a project reference to source), so blocking would only mean
  projects going unreported. The failure is reported against its own project
  and the run exits 1.
- `--json` adds a `projects` array: `projectDir`, `configPath`, `ok`, `files`,
  `transformed`, `overlayMatches`, and the diagnostics that belong to the
  project rather than to one of its files. Each `fileDiagnostics` entry gains a
  `project` key naming the same `configPath`, so the files stay in one flat
  array instead of being repeated per project.
- Every top-level number is a **solution-wide aggregate**: `files`,
  `transformed`, `overlayMatches` and `diagnostics` are the totals, and
  `fileDiagnostics` holds every project's files. A project's `diagnostics` are
  the attributed subset of the top-level array, not an addition to it;
  solution-level failures belong to no project and appear only at the top.
- **Without `--build` the output is byte-identical to a solution-unaware
  rotor**: `projects` and the per-file `project` are the only new keys and both
  are omitted.
- `allowCommentDirectives` is forced off for every project of the solution, not
  only the entry, so one referenced project's `rbxts` key cannot change how its
  files are classified.

A standalone `.ts` file isn't compilable by itself — like `rbxtsc`, rotor needs the rbxts project around it (`package.json` with `@rbxts/compiler-types` + `@rbxts/types` installed, `tsconfig.json`, `default.project.json`). The fixture project above is a minimal working example of that setup.

## Build options

`sloptor build` accepts the rbxtsc-compatible flag surface (booleans accept `--flag`, `--flag=false`, `--no-flag`): `-p/--project`, `-b/--build [path]`, `--builders <n>`, `--checkers <n>`, `--emitDeclarationOnly`, `-w/--watch`, `--usePolling`, `--verbose`, `--noInclude`, `--logTruthyChanges`, `--writeOnlyChanged`, `--writeTransformedFiles` (parsed and ignored), `--optimizedLoops`, `--type game|model|package`, `-i/--includePath`, `--rojo`, `--allowCommentDirectives`, and `--luau`. Sloptor adds `--cpuprofile`, `--trace-out`, `--blockprofile`, `--mutexprofile`, `--heapprofile`, `--timings`, `--minify`, `--max-errors`, `--json`, `--bell`, and `--no-clear`. `--emitDeclarationOnly` requires `--build`; `--build --watch` cannot be combined with declaration-only emit. The finite profiling and timing flags cannot be combined with `--watch`. Run `sloptor build --help` for the rendered descriptions.

Options may also be set under the top-level `"rbxts"` key of `tsconfig.json`; merge order: defaults < rbxts < command line.

**rotor DX extensions** (not in rbxtsc; safe to ignore for parity):

- **`--minify`** — pass every emitted `.luau`/`.lua` source through the Luau minifier (comment/whitespace stripping + `t["x"]` → `t.x`) before writing. It is opt-in; declaration and `include/` files are never minified.
- **Code-frame diagnostics** — TypeScript, transformer, and macro errors render as grouped code frames (source line + caret/underline, keyword highlighting, OSC 8 file links, an `✗ N errors in M files` footer). `--max-errors <n>` caps the rendered frames (default 50; `0` = all). In watch mode the screen clears before each rebuild (opt out with `--no-clear`), a `✗ N errors` banner persists on the idle line until the next green build, and `--bell` rings the terminal on a fail↔pass transition.
- **`--json`** — emit one machine-readable result object (version, ok, files, durationMs, diagnostics with `file`/`line`/`col`/`code`/`severity`/`message`) instead of styled output. `code` is `TS####` for a TypeScript diagnostic and the upstream factory name for a transformer one, and is omitted when the diagnostic has none. Also available on `sloptor check`.
- **One-shot diagnostics** — `--cpuprofile`, `--trace-out`, `--blockprofile`, `--mutexprofile`, `--heapprofile`, and `--timings` can be combined on one build. Rotor finalizes requested profiles even when the build fails, which keeps failed-build traces usable.

### Shell completion

`sloptor completion <bash|zsh|fish|powershell>` writes a native completion
script to stdout — no installation side effects, so redirection works
unchanged:

```sh
sloptor completion bash > /etc/bash_completion.d/sloptor
sloptor completion zsh > "${fpath[1]}/_sloptor"
sloptor completion fish > ~/.config/fish/completions/sloptor.fish
sloptor completion powershell | Out-String | Invoke-Expression
```

The scripts are generated from the live command tree, so they stay in sync
with the flags and subcommands above.

### Tsconfig schema publication

The `rotor.toml` schema and the tsconfig `rbxts` schema are separate documents. `sloptor schema` prints the hosted `rotor.toml` schema; `sloptor schema --rbxts` prints the eight-key tsconfig extension schema. Publish the latter where your project editors can read it:

```sh
sloptor schema --rbxts > rbxts-tsconfig.schema.json
```

Use `"$schema": "./rbxts-tsconfig.schema.json"` in `tsconfig.json`. The `rbxts` values merge through the `extends` chain from parent to child, path values resolve relative to the file that declares them, and command-line values win over tsconfig values. CLI-only values such as `watch`, `verbose`, and `usePolling` are warned about and ignored when placed under `rbxts`.

### Project references, source maps, plugins, and build state

`sloptor build --build` drains project references in dependency order. A coordinator tsconfig with only `references` is not compiled as a project of its own. `--emitDeclarationOnly` applies to the solution build and cannot be watched. `--build --watch` watches the solution's tsconfig extends chain and Rojo topology, invalidating dependent projects when a referenced project changes.

With `compilerOptions.sourceMap: true`, Rotor writes an adjacent `.luau.map` for each Luau output. The map's `sourcesContent` is the original pre-transformer TypeScript, and source-map files are not counted as emitted Luau files. Declaration maps are kept while their corresponding source exists.

Transformer plugins remain compatible with the fork's `compilerOptions.plugins` shape and run through Rotor's Node sidecar. `before` transformers run before `after` transformers; `afterDeclarations` runs only during declaration emit. A plugin's `shouldTransformSourceFile` hook can skip a file, and plugin loading failures are build diagnostics. Plugin-configured builds require Node.js and the project's own `typescript` package.

Successful builds may leave these generated or cached files. They are build state, not source files:

| File or directory | Purpose |
| --- | --- |
| `rbxts.copyfiles.json` | `outDir` copy-files gate manifest; lets an unchanged build skip cleanup and copy work |
| `rbxts.rojocache.json` | Fork-compatible Rojo cache name protected from cleanup; Rotor's active hashed cache is under `.rotor/cache/rojo/` |
| `.rotor/cache/rojo/` | Rotor's hashed Rojo resolver cache directory |
| `.luau.map` | Adjacent Luau source map when `sourceMap` is enabled |
| `*.rbxtsc.tsbuildinfo` | Rotor incremental build state; Rotor inserts `.rbxtsc` before `.tsbuildinfo` to avoid collisions |

### Concurrency controls

`--checkers <n>` sets the number of type-checker workers per project. The default is 4, matching TypeScript 7. It applies to `sloptor build`, `sloptor check`, and both watch modes. A CLI value overrides each project's `compilerOptions.checkers`; omitting the flag leaves the project's own config in place. If a project sets `compilerOptions.singleThreaded: true`, it still forces one effective checker regardless of the CLI.

`--builders <n>` sets the number of project-reference builders that run concurrently during solution builds (`--build`). The default is 4; `--builders 1` makes the solution build serial. It is only valid with `--build` — passing it without `--build` is a usage error (exit 1). `sloptor check` does not accept `--builders`.

Both flags accept positive integers only. Missing, zero, negative, or non-integer values are usage errors.

The two flags multiply: `--builders 4 --checkers 4` can run up to 16 checker workers at once, plus parsing, emit, and write work. CPU and memory use grow with the product. Find the right balance for the machine — CI runners with limited cores or memory may want smaller values.

**Result guarantees.** Varying `--builders` never changes build results: dependencies always build first, and output, diagnostics, and caching stay deterministic. TypeScript 7 notes that in rare cases varying `--checkers` can surface order-dependent results. Teams that need perfectly stable diagnostics across environments may want to pin `--checkers` to a fixed value.

**Real-world limits.** Even with high concurrency, some work stays serialized. Transformer-plugin sidecars run through Node and may bottleneck a project. Projects whose output or include directories overlap are serialized for safety. And the dependency graph itself bounds how many projects can build in parallel — leaf projects run first, and dependents wait.

These flags control checker and builder parallelism. They are separate from the write-worker environment variables below, which only tune disk-write parallelism.

The write-worker controls are:

- `RBXTSC_WRITE_CONCURRENCY` directly overrides output-write workers and takes precedence over Rotor's setting; values are capped at 256.
- `ROTOR_WRITE_WORKERS` is Rotor's own output-write override.
- Without either override, Rotor uses 8 Go output-write workers. Positive fractional overrides are rounded down; invalid values fall through to the next setting or this default.

`UV_THREADPOOL_SIZE` may still affect the Node sidecar's libuv thread pool, but it does not select Rotor's Go output-write worker count.

Rotor uses `GOGC=400` when `GOGC` is unset. When `GOMEMLIMIT` is unset, Rotor uses 75% of effective memory, clamped from 512 MiB to 16 GiB; Linux accounts for finite cgroup v1/v2 limits, while macOS and Windows use the host memory reported by the OS. Explicit environment values always win.

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
