# Windows Build Benchmark Harness

`windows-build-performance.ps1` measures prebuilt baseline and candidate Rotor
binaries against two isolated copies of one canonical fixture. It is evidence
generation for a controlled Windows host, not a CI wall-clock benchmark.

Run the self-test on any platform:

```powershell
pwsh -NoProfile -File tools/bench/windows-build-performance.ps1 -ValidateOnly
```

The self-test verifies alternating AB/BA order, the cold-cleanup allowlist,
fixture immutability, unchanged environment capture, manifest writing, and the
`perfcompare` invocation path. It deliberately writes a two-cold-pair manifest
and requires the evaluator to reject it with `cold pairs = 2, want at least 10`.

Run a scored benchmark only on a controlled Windows host with prebuilt binaries:

```powershell
pwsh -NoProfile -File tools/bench/windows-build-performance.ps1 `
  -BaselineExe C:\rotor-bench\baseline\rotor.exe `
  -CandidateExe C:\rotor-bench\candidate\rotor.exe `
  -Fixture C:\rotor-bench\fixture `
  -ColdPairs 10 `
  -NoChangeRuns 20 `
  -SidecarTimeout 300s `
  -EvidenceDir C:\rotor-bench\evidence
```

The evidence directory must be outside the Rotor checkout and fixture. The
script rejects dirty Git worktrees, snapshots the fixture tree, creates and
cleans only its own `work-*` copies, and verifies the source and fixture stayed
unchanged. It preserves `node_modules`, `.rotor`, OS cache state, Defender and
indexer settings, `UV_THREADPOOL_SIZE`, and all other inherited environment
values. The manifest records deterministic hashes of environment values, not
credential values. Cold cleanup is limited to `out`, `out-test`, `out-tsc`, and eligible
`*.rbxtsc.tsbuildinfo` files outside `node_modules`, `.rotor`, and `.git`.

Each scored command is a supplied executable running `build --project
<work-copy> --json`; it never uses `go run`. The candidate `--timings` command
is a separate, non-scored diagnostic. `go run ./tools/perfcompare` is used only
after the manifest is written. A nonzero evaluator result leaves all evidence
in place and makes the harness fail, including for intentionally insufficient
sample counts.

## Windows Stub Procedures

Keep fixture and Rotor worktrees clean, then use prebuilt stub executables with

| Scenario | Stub behavior | Expected result |
| --- | --- | --- |
| Nonzero exit | Candidate writes a valid JSON result and exits nonzero | Evaluator reports `exit_code_mismatch` |
| Digest mismatch | Candidate writes different diagnostics or output | Evaluator reports the corresponding digest mismatch |
| Intentional slowdown | Candidate sleeps before valid output | Evaluator reports a performance ratio failure |
| Missing executable | Pass a nonexistent candidate path | Harness fails preflight before copying the fixture |
| Fixture mutation | Stub attempts to modify the canonical fixture path | Harness detects the changed fixture digest and fails |

Use a short timeout for the hanging-stub procedure. The harness terminates an
overdue process tree per invocation, removes only its `work-*` directory in a
`finally` block, and preserves the evidence already captured.
