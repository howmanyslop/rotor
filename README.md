<p align="center">
  <img src="media/logo.png" alt="rotor — an all in one roblox toolchain" width="480">
</p>

<p align="center"><em>TypeScript in, Roblox out — at native speed.</em></p>

<p align="center">
  <a href="https://github.com/uproot/rotor/releases/latest"><img src="https://img.shields.io/github/v/release/uproot/rotor" alt="latest release"></a>
  <a href="https://github.com/uproot/rotor/actions/workflows/ci.yml"><img src="https://github.com/uproot/rotor/actions/workflows/ci.yml/badge.svg" alt="ci"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
</p>

rotor is an all-in-one Roblox toolchain, written in Go. At its core is a native rewrite of the [roblox-ts](https://roblox-ts.com) compiler built on [typescript-go](https://github.com/microsoft/typescript-go) — a drop-in `rbxtsc` replacement with upstream-parity Luau output, plus a native Luau toolchain and Open Cloud asset + deployment pipelines, all in one binary.

```sh
bun add -d @rotor-rbx/rotor    # or: npm i -D @rotor-rbx/rotor — see Install for more ways
```

📖 [Documentation](docs.md) · 🤝 [Contributing](CONTRIBUTING.md) · 🗺️ [Roadmap](roadmap.md)

## Features

**Compile & check** — the rbxtsc replacement:

```sh
sloptor check ./my-game -w     # native full-strictness typecheck: 222 files in 161 ms
sloptor build ./my-game -w     # parity Luau, watch mode, incremental rebuilds
sloptor doctor                 # diagnose tsconfig, @rbxts packages, plugins, Rojo, cloud setup
```

Both `build` and `check` accept `--checkers <n>` to tune type-checker workers per project (default 4). Solution builds (`--build`) also accept `--builders <n>` for project concurrency (default 4). `sloptor build --build --builders 2 --checkers 4` is a typical tuned command.

Same `tsconfig.json`, same `@rbxts/*` packages, same transformer plugins (Flamework etc.), same CLI flags — plus built-in compile-time macros rbxtsc doesn't have (no plugins, no Node sidecar, fully typed):

| Macro | Inlines |
|-------|---------|
| `$env("KEY", "fallback")` · `$env.KEY` | env vars from `.env` / `.env.<NODE_ENV>` |
| `$asset("logo.png")` | a `rbxassetid://…` string (cached, auto-uploads on miss) |
| `$keys<T>()` | a type's string keys (checker-powered) |
| `$nameof(expr)` | the trailing identifier name as a string |
| `$file("data.json")` | a file's contents as a Luau value (JSON→table, text→string) |
| `$git("sha"\|"branch"\|"tag"\|"dirty")` · `$buildTime()` | build/VCS stamps |
| `$getModuleTree("shared/systems")` | a folder's module tree (no `index.ts` required) |

### Compatibility boundary

The verified `@isentinel/roblox-ts@4.0.11` fork archive in `roblox-ts.zip` is authoritative for fork-changed surfaces: varargs optimization, object-rest direct access, the `rbxts` option cascade, `.luau.map` source maps, the copy-files gate, the Rojo resolver cache, solution builds, and solution watch. Rotor keeps upstream parity for unaffected compiler behavior. Do not use the older `reference/roblox-ts` tree as the authority for those changed surfaces.

For the tsconfig extension schema, publish the Rotor copy once at the repository or project location you want editors to use:

```sh
sloptor schema --rbxts > rbxts-tsconfig.schema.json
```

Then point `tsconfig.json` at that file with its top-level `"$schema"` field. This is separate from `rotor.schema.json`, which describes `rotor.toml`.

**Luau toolchain** — works on any Rojo project, no rbxts required:

```sh
sloptor dev                    # watch + incremental compile + rojo serve to Studio
sloptor bundle entry.luau -o bundle.luau --minify   # inline a require graph, still runnable
sloptor minify file.luau       # strip comments/whitespace, keep --! directives
sloptor pack --as luau         # whole project -> one self-reconstructing script (or rbxm/rbxmx)
sloptor sourcemap -o sourcemap.json                 # Rojo-compatible, for luau-lsp
```

**Cloud** — assets and deployment from one typed config (see below):

```sh
sloptor asset sync             # upload new/changed assets, lockfile, typed codegen
sloptor deploy plan -e prod    # diff config vs live state (terraform-style, no network writes)
sloptor deploy apply -e prod   # publish places, settings, badges — only what drifted
```

**Scaffolding** — `sloptor init` runs an interactive wizard (template, Biome/oxlint, starter packages, asset/deploy config) or scripts cleanly with `--yes`/`--template`.

**Shell completion** — `sloptor completion bash|zsh|fish|powershell` writes a native completion script to stdout (e.g. `sloptor completion fish > ~/.config/fish/completions/sloptor.fish`); see `sloptor completion --help` for the one-line install per shell.

## Configuration — `rotor.toml`

One typed TOML config drives the cloud tools. `sloptor init` writes it with a `#:schema` directive that points at the schema **hosted in this repo** (served via raw GitHub), so taplo / Even Better TOML give validation + autocomplete with no per-project `rotor.schema.json` to generate or commit. Need a local copy for offline editing? `sloptor schema > rotor.schema.json`. (Upgrading from a 1.x `rotor.config.ts`? Run `sloptor migrate`.)

```toml
#:schema https://raw.githubusercontent.com/uproot/rotor/master/rotor.schema.json

[assets]
mode = "macro"                  # "module" (assets.luau) | "macro" ($asset transformer)
base = "assets"                 # optional: $asset("logo.png") -> assets/logo.png
paths = ["assets/**/*.png", "assets/**/*.ogg"]
[assets.creator]
type = "group"
id = 12345

[deploy.environments.prod]
universeId = 333
[deploy.environments.prod.places.start]
file = "build/game.rbxl"
placeId = 444
maxPlayers = 30
versionType = "published"
[deploy.environments.prod.experience]
name = "My Game"
playability = "public"
[deploy.environments.prod.gamepasses.vip]
name = "VIP"
price = 250
icon = "assets/vip.png"
[deploy.environments.prod.socials.discord]
title = "Join us"
url = "https://discord.gg/x"
type = "discord"
```

- **`sloptor asset sync`** scans the globs, uploads new/changed files via Open Cloud (SHA-256 lockfile `rotor-lock.json` — unchanged files never re-upload, updates keep asset ids stable). In `mode = "module"` it generates `assets.luau` + `assets.d.ts`; in `mode = "macro"` you reference assets inline with **`$asset("assets/logo.png")`**, which resolves from the cache and auto-uploads any miss when `ROBLOX_API_KEY` is set. An optional `[assets] base` directory is prepended to non-relative `$asset` paths, so files can live under e.g. `assets/images/` while references stay short. Image uploads are unwrapped to the underlying **Image** asset id (an upload creates a Decal wrapper whose id doesn't render in `ImageLabel.Image` and friends).
- **`sloptor deploy`** is infrastructure-as-code: it diffs the config against per-environment state (`.rotor/deploy/<env>.json`), shows a plan, and applies only the drift — place file publishing + place settings, experience settings, badges and game passes (icons upload automatically first, shared icons dedupe), experience icon + thumbnails, developer products, and social links. Deletes require `--allow-deletes`.
- Auth is an Open Cloud key in **`ROBLOX_API_KEY`** (scopes: Assets R/W, Legacy Assets manage — used to unwrap image uploads to their Image id, Universe Places W, Universe R/W). `sloptor doctor` checks your config and key setup.

Full config shape, every command flag, generated build-state file, environment variable, and all the macros: [docs.md](docs.md).

## Install

Grab a binary from [GitHub Releases](https://github.com/uproot/rotor/releases), or use a toolchain manager:

```sh
# mise
mise use -g github:uproot/rotor@2.0.0

# rokit
rokit add uproot/rotor@2.0.0
```

```toml
# aftman.toml
[tools]
rotor = "uproot/rotor@2.0.0"

# foreman.toml
[tools]
rotor = { github = "uproot/rotor", version = "2.0.0" }
```

### Install via npm / bun

For rbxts projects that already live in the JS ecosystem, install [`@rotor-rbx/rotor`](https://www.npmjs.com/package/@rotor-rbx/rotor) as a dev dependency — a postinstall step downloads the prebuilt binary for your platform:

```sh
bun add -d @rotor-rbx/rotor
npm i -D @rotor-rbx/rotor
pnpm add -D @rotor-rbx/rotor
yarn add -D @rotor-rbx/rotor
```

Installing straight from GitHub works too: `bun add -d github:uproot/rotor` (npm/pnpm/yarn equivalents likewise).

> **bun note:** bun skips postinstall scripts by default. Either add `"trustedDependencies": ["@rotor-rbx/rotor"]` to your project's `package.json` (then `bun install`), or do nothing — the `rotor` shim downloads the binary on first run. pnpm similarly asks you to approve build scripts (`pnpm approve-builds`), with the same first-run fallback.

Or build from source (Go 1.25+):

```sh
git clone https://github.com/uproot/rotor && cd rotor
go build ./cmd/rotor
```

## Benchmarks

Measured on real production rbxts games, with output byte-identical to `rbxtsc` 3.0.0 on the unaffected benchmark surfaces:

| Workload | rotor |
|----------|------:|
| Full strict typecheck — 222-file production game | **161 ms** |
| Full build — 95-file production game | **355 ms** |
| Incremental watch rebuild — same game | **180 ms** |

The JS toolchain spends longer than this booting Node. The ~10× speedup is structural: rotor runs Microsoft's native, parallel TypeScript compiler ([typescript-go](https://github.com/microsoft/typescript-go)) instead of the single-threaded JS one. You can tune the parallelism with `--checkers` and `--builders` (see [docs.md](docs.md#build-options)).

## Contributors

<a href="https://github.com/uproot"><img src="https://github.com/uproot.png" width="56" height="56" alt="uproot"></a>
<a href="https://github.com/Coyenn"><img src="https://github.com/Coyenn.png" width="56" height="56" alt="Coyenn"></a>

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE). rotor stands on [roblox-ts](https://github.com/roblox-ts/roblox-ts) (MIT) and [typescript-go](https://github.com/microsoft/typescript-go) (Apache-2.0) — see [credits](docs.md#credits--licenses).
