# Fork-Parity Matrix Data

`divergence-ledger.json` is the machine-readable authority map for the zip
compatibility matrix. It contains no hand-authored compiler output. The frozen
fork artifacts remain in `internal/forkparity` and identify their archive digest
and extraction command. A full matrix run writes its normalized zero-drift
report to the Go test artifact directory.
