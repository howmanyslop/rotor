# Conformance corpus (Phase 5)

A second differential corpus: roblox-ts's **own upstream test sources**, compiled
by the real `rbxtsc` 3.0.0 into committed goldens that rotor's output is
byte-compared against (`internal/conformance`). Fork-divergent fixtures use
fork-authoritative Rotor goldens instead: `tests/array.spec.luau`,
`tests/class.spec.luau`, `tests/destructure.spec.luau`,
`tests/exportLet.spec.luau`, `tests/function.spec.luau`,
`tests/loop.spec.luau`, and `tests/object.spec.luau`. Phase 5 now has three
harnesses:

- `TestDiagnosticsCorpus` compiles the vendored `excluded/diagnostics/*` files
  one by one and asserts rotor's diagnostic IDs.
- `TestConformance` compiles enabled golden fixtures one by one through temp
  projects rooted under `project/`, so unsupported nodes in unrelated upstream
  specs do not block the fixtures that already match.
- `TestBehavioralSuite` and `TestRandomnessAcceptance` are environment-gated
  runners for the upstream runtime suite and the real `randomness` project.

## Provenance

Sources copied verbatim from the vendored upstream checkout at
`reference/roblox-ts/tests/src` — roblox-ts **v3.0.0**, commit
`d1d5486094ac1d1821b7eb03ce0de0890d30b82e` (see `reference/VERSIONS.md`).

Upstream `tests/src` contains **131 files**, all accounted for:

| Destination | Files | What |
|---|---|---|
| `project/src/` | 45 | `main.server.ts`, `services.d.ts`, `helpers/**` (5), `tests/**` (38 spec files, ~5.8k LOC of feature coverage) |
| `excluded/diagnostics/` | 86 | the entire upstream `diagnostics/` directory (85 `.ts`/`.tsx` + its `README.md`) |
| **Goldens** | **44** | every `project/src` file except `services.d.ts` (declaration file, emits nothing) |

## Exclusions

- `excluded/diagnostics/**` (85 sources): these files **intentionally fail
  compilation** — upstream's test driver (`src/CLI/test.ts`) compiles each one
  individually and asserts that the expected diagnostic is produced. They can
  never be part of a clean-compile golden corpus. They are kept here (not
  dropped) so a future diagnostics-conformance harness can use them.

No other file failed: all 44 emitting sources compile clean under the setup
below, on the first full run.

## Fixture project recipe (`project/`)

- `package.json` — exact pins, reconciled between `testdata/diff/project` and
  upstream `tests/package.json`:
  - `roblox-ts@3.0.0`, `typescript@5.5.3`, `@rbxts/compiler-types@3.0.0-types.0`,
    `@rbxts/types@^1.0.800` (same as the diff project),
  - plus the upstream test deps as npm pins: `@rbxts/roact@1.4.4-ts.0`,
    `@rbxts/services@1.5.3`, `@rbxts/testez@0.4.2-ts.0` (upstream declares the
    first via `github:roblox-ts/rbx-roact`; its lockfile resolved the same
    1.4.4-ts.0, which also exists on npm).
  - Upstream's lockfile had *stale* `@rbxts/compiler-types@2.2.0-types.0` and
    `@rbxts/types@1.0.751`; we use the 3.0.0-matching compiler-types and a
    newer types floor, identical to the diff corpus. Goldens were generated
    with `@rbxts/types@1.0.925` installed; `project/package-lock.json` is
    committed (like the diff project's) so installs are reproducible.
- `tsconfig.json` — upstream `tests/tsconfig.json` verbatim (minus comments)
  plus `"include": ["src"]`. Differences from the diff project's tsconfig:
  `jsxFactory` is `Roact.jsx` (not `React.createElement`), `baseUrl: "src"` is
  set, and `incremental`/`tsBuildInfoFile` are omitted.
- `default.project.json` — **model**-type, same shape as the diff project's.
  Upstream uses a full DataModel (game) project that places
  `helpers/rojo/isolated.ts` in StarterGui; under a model project nothing is
  isolated, and all files (including that one) still compile clean. The
  diagnostics harness keeps this model project for the portable fixtures, but
  stages the two Rojo-topology diagnostics in a temporary upstream-shaped
  DataModel project so their original isolation / missing-$path expectations
  are still exercised.
- `rbxtsc` is invoked with `--allowCommentDirectives` because the upstream
  sources use `@ts-ignore`/`@ts-expect-error`; upstream's own test driver sets
  `allowCommentDirectives: true` for the same reason. Rotor now honors the
  same option in its project/build path, so this corpus stays aligned with the
  upstream driver behavior.

## Regenerating goldens

```
powershell -File tools/oracle/conformance-oracle.ps1
```

(Requires Node plus Bun or npm — mise-managed; the script invokes
`node .\node_modules\roblox-ts\out\CLI\cli.js` directly so Bun/npm installs behave the same. It
prefers `bun install --no-save` on first run, falls back to npm, cleans `project/out/`, compiles, and mirrors
`out/**/*.luau` into `golden/` preserving subdirectories.)

The upstream oracle must not overwrite the seven fork-divergent fixtures listed
above. Regenerate those only from current fork-correct Rotor output after
`TestFullForkParity` confirms the archived fork contract.

## Enabling fixtures

Add the golden-relative slash path (e.g. `"tests/array.spec.luau"`) to
`EnabledFixtures` in `internal/conformance/manifest.go`. Then
`go test ./internal/conformance/ -run TestConformance -count=1 -v` compiles
each enabled fixture through a temp project rooted under `project/`, preserving
the shared `node_modules` tree while limiting the compile to the selected
source plus shared helpers.

As of June 7, 2026, **44 / 44** committed goldens are enabled. `DisabledFixtures`
is empty and the vendored conformance corpus is fully closed: byte-diff,
diagnostics, runtime, and real-project acceptance all have green harnesses.

## Diagnostics corpus

Run:

```powershell
go test ./internal/conformance/ -run TestDiagnosticsCorpus -count=1
```

The harness installs `project/node_modules` on first use if needed, then
overlays each vendored diagnostics fixture under `project/src/__diagnostics`
and compiles it through the real conformance project config. The two
Rojo-topology fixtures (`noRojoData.ts`, `noIsolatedImport.ts`) are staged in
temporary projects that copy the upstream `tests/default.project.json`, so the
full vendored diagnostics corpus now runs without skips.

## Runtime suite

Run:

```powershell
go test ./internal/conformance/ -run TestBehavioralSuite -count=1 -v
```

Requires `rojo` plus `lune`. Detection order is:

1. `ROTOR_ROJO_PATH` / `ROTOR_LUNE_PATH`
2. the executable on `PATH`

The test skips with an actionable message if either tool is missing. When
available it stages a temporary **runtime subset project** containing:

- the current enabled conformance specs,
- shared helpers and entrypoints,
- an upstream-shaped DataModel Rojo config for `ServerScriptService.tests`.

This keeps the runtime run aligned with the upstream TestEZ corpus while still
staging the temporary project topology that Lune needs.

When the tools are available it:

1. builds a staged conformance subset project with `rotor`,
2. runs `rojo build` to produce a place file,
3. executes the upstream `reference/roblox-ts/tests/runTestsWithLune.lua`.

With `rojo` + `lune` available, the staged suite now executes the full vendored
runtime corpus green (`460 passed, 0 failed, 0 skipped` on June 7, 2026).

## randomness acceptance

Run:

```powershell
go test ./internal/conformance/ -run TestRandomnessAcceptance -count=1 -v
```

Set `ROTOR_RANDOMNESS_PATH` to the local project root before running. The test
accepts either the project root or a direct path to its `tsconfig.json`. It
skips with an explicit setup message when unset and otherwise:

1. copies the target project twice,
2. runs Rotor's full build pipeline over one copy,
3. runs the local `rbxtsc` install over the other copy,
4. compares the normalized `out/` and `include/` trees byte-for-byte.

This keeps the harness environment-gated while making it a real build/output
acceptance proof rather than a compile-only smoke test. With a local
`randomness` checkout available, the staged compare is green byte-for-byte on
both `out/` and `include/`.
