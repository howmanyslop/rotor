# Transformer Sidecar Protocol

Rotor runs transformer plugins (`compilerOptions.plugins`) through a Node
worker that mirrors upstream roblox-ts plugin semantics. The worker's source
of truth is `tools/sidecar/`, and it is **embedded in the rotor binary**
(`tools/sidecar/embed.go` + `internal/compile/sidecar_install.go`): released
binaries extract it to `<user-cache>/rotor/sidecar-<content-hash>/` on first
plugin build, so no repo checkout is required. `ROTOR_SIDECAR_PATH` overrides
the worker location (repo development and tests point it at `tools/sidecar`).

Projects without plugins never spawn Node.

## TypeScript resolution

The worker resolves the `typescript` package **from the project's
`node_modules` first**, falling back to the worker's own directory. This
matters for correctness, not just convenience: plugins `require("typescript")`
themselves, and factory nodes only compose with `transformNodes` when both
sides share one module instance — upstream roblox-ts guarantees this by
construction (its own pinned 5.5.3 is the hoisted copy plugins see).
roblox-ts projects pin `typescript@~5.5.3`; if a project has no typescript
install, the worker reports a `typescript-not-found` diagnostic.

## Setup (repo development)

```bash
cd tools/sidecar
bun install --no-save
```

The package pins `typescript@5.5.3` to match the upstream `roblox-ts` plugin
runtime; this copy is only the fallback for synthetic test fixtures that have
no `node_modules` of their own.

## Invocation

```bash
node tools/sidecar/main.js
```

The process reads newline-delimited JSON requests from `stdin` and writes one
newline-delimited JSON response per request to `stdout`.

**stdout is reserved for protocol responses.** `main.js` captures the real
stdout writer and reroutes every other stdout write (plugin `console.log`,
e.g. Flamework's logging) to stderr. Rotor streams the worker's stderr lines
to the compiler log as they arrive and keeps a tail for error reporting.

## Warm sessions

Rotor keeps **one worker per `(projectDir, tsConfigPath)` for the life of the
rotor process**, including across `rotor build -w` rebuilds — the JS program
stays warm, mirroring upstream's persistent `transformerWatcher`. The worker
exits when rotor's pipes close.

Edits are communicated via `changedFiles`: rotor stat-diffs the project's
`.ts`/`.tsx` files against the session's last-seen stamps and ships new text
for anything that changed, which bumps the worker's LanguageService script
versions (upstream `updateFile` semantics). A request on a fresh worker sends
no changed files — the worker reads from disk. If a worker dies mid-request,
rotor respawns it once and retries.

**Caller source overlays** (`ProjectOptions.Overlays`, which `rotor
diagnostics` fills from its stdin request) ride the same field, under
different rules. An overlay exists nowhere on disk, so no stat describes it:
each one ships on every round trip, fresh worker or not. An overlay the next
round trip drops is undone by resending the file's disk text, because the
worker's override map outlives the request that filled it.

Known limitation: a warm worker's *plugin-visible* view of an edited ambient
`.d.ts` can be stale until the watch session restarts (stamps cover the
`.ts`/`.tsx` compile surface). Rotor's own typecheck and emit always read
fresh state.

Inside the worker, one in-memory project session per
`(projectDir, tsConfigPath)` holds the overlay map and reuses the TypeScript
`LanguageService` program across requests.

## Protocol v1

Each request must be a single JSON object with `protocol: 1`.

```json
{
  "protocol": 1,
  "tsConfigPath": "C:/abs/project/tsconfig.json",
  "projectDir": "C:/abs/project",
  "compileFileNames": [
    "C:/abs/project/src/example.ts"
  ],
  "changedFiles": [
    {
      "fileName": "C:/abs/project/src/example.ts",
      "text": "export const phase = \"memory\";\n"
    }
  ]
}
```

Response shape:

```json
{
  "diagnostics": [
    {
      "category": "error",
      "code": "invalid-request",
      "message": "protocol must equal 1"
    }
  ],
  "transformed": [
    {
      "fileName": "C:/abs/project/src/example.ts",
      "text": "export const phase = \"afterDeclarations:before:after:start\";\n"
    }
  ]
}
```

`diagnostics` may contain:

- TypeScript config/program diagnostics converted to `{ category, code, file, start, length, message }`
- transformer resolution warnings using code `transformer-not-found`
- a `typescript-not-found` error when the project has no resolvable `typescript` package
- request validation errors using code `invalid-request`
- internal worker failures using code `sidecar-internal`

## Semantics Mirrored From Upstream

The worker mirrors the upstream `roblox-ts` transformer behavior in these areas:

- `getPluginConfigs` only accepts plugin objects with string `transform` fields.
- transformer modules resolve relative to `projectDir`.
- factory invocation follows upstream `type` handling for `program`, `config`, `checker`, `raw`, and `compilerOptions`.
- transformed files run through a single `typescript.transformNodes(...)` pass.
- transformer flatten order intentionally stays `after`, then `before`, then `afterDeclarations`.
- transformed `SourceFile`s are reprinted with `typescript.createPrinter().printFile(...)`.

## Deliberate Divergence: `plugins` And `extends`

Upstream `roblox-ts` walks the `extends` chain itself and **concatenates** the
`compilerOptions.plugins` of every config in it (`Project/transformers/
getPluginConfigs.ts`). A child that sets `"plugins": []`, or that lists a
different transform, still runs everything its parents declared — there is no
way to opt out of an inherited transformer.

Rotor takes the **resolved** value instead: `getPluginConfigs` reads
`plugins` off the compiler options the config parse already produced, so the
rule is TypeScript's own. `plugins` is an array-valued compiler option, and
`extends` replaces array-valued options rather than merging them:

| child `compilerOptions.plugins` | resolved list | rbxtsc |
| --- | --- | --- |
| absent | the parent's | the parent's |
| `[]` | empty | the parent's |
| `[{ "transform": "x" }]` | `x` only | `x` + the parent's |

`tsc --showConfig` on the child reports exactly the middle column; run it to
confirm what a project resolves to. Note that this rule is what makes a
test-only config (`tsconfig.spec.json` extending a package's `tsconfig.json`)
able to compile without its package's transform.

Two consequences for projects migrating from rbxtsc:

- A config that declares its own `plugins` no longer inherits a shared base's
  transformers. List them alongside its own if it needs both.
- `internal/compile` gates the Node worker on the same rule, so a project that
  resolves to no plugins never spawns one.

## Verification

JS worker suite (also run in CI):

```bash
cd tools/sidecar
node --test test/*.test.js
```

Real-package integration (Flamework + rbxts-transform-env), exercising the
full production path — embedded extraction, project typescript, warm session:

```bash
cd testdata/transformers/project && bun install --no-save && cd ../../..
go test ./internal/compile -run TestTransformersFixtureFlameworkAndEnv -count=1
```

The JS suite covers plugin discovery through `extends`, named/default factory
loading, `checker`/`compilerOptions` factory instantiation, the
`after -> before -> afterDeclarations` execution quirk, stdio protocol
handling with warm overlay updates, per-project typescript resolution, and
the stdout-protection rule.
