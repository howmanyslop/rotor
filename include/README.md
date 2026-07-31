# include/ — roblox-ts runtime library

`RuntimeLib.lua` and `Promise.lua` are copied byte-for-byte from
`reference/roblox-ts/include/`. This fork carries the runtime behavior from
`@isentinel/roblox-ts` 4.0.11, including same-runtime module reload support.
Update all three vendored copies together.

These are the files `rotor build` writes into a project's include folder
(default `<projectDir>/include`, overridable with `--includePath`, skipped
with `--noInclude`), porting upstream `Project/functions/copyInclude.ts`.

The same bytes are embedded in the rotor binary via `internal/includefiles`
(go:embed cannot reach above its package directory, so that package carries a
second copy); `internal/includefiles/includefiles_test.go` fails if the two
copies ever drift.
