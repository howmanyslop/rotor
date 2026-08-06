#!/usr/bin/env node
// npm/bun shim for the sloptor CLI. Resolves the platform binary downloaded by
// scripts/install.js (postinstall); if it is missing — e.g. bun skipped the
// postinstall because "rotor" is not in trustedDependencies — downloads it on
// first run, then execs it with the original args.
"use strict";

const fs = require("fs");
const { spawnSync } = require("child_process");
const { install, binaryPath } = require("../scripts/install.js");

async function main() {
  let bin = binaryPath();
  if (!fs.existsSync(bin)) {
    bin = await install();
  }

  const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.signal) {
    // Re-raise the child's fatal signal so callers see the same termination.
    process.kill(process.pid, result.signal);
    return;
  }
  process.exit(result.status === null ? 1 : result.status);
}

main().catch((err) => {
  console.error(err && err.message ? err.message : err);
  process.exit(1);
});
