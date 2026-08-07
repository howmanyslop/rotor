#!/usr/bin/env node
// Downloads the prebuilt sloptor binary for the current platform from GitHub
// Releases. Runs as the package's postinstall hook; also required as a module
// by bin/rotor.js for a first-run download when the postinstall was skipped
// (e.g. bun without trustedDependencies).
//
// Plain Node, no dependencies. Requires Node 18+ (global fetch) or Bun.
//
// Env:
//   ROTOR_INSTALL_BASE_URL  override the download base URL (testing/proxies);
//                           defaults to the GitHub release for pkg.version.
"use strict";

const fs = require("fs");
const path = require("path");

const pkg = require(path.join(__dirname, "..", "package.json"));

const PLATFORM_MAP = { win32: "windows", linux: "linux", darwin: "darwin" };
const ARCH_MAP = { x64: "amd64", arm64: "arm64" };

// Anything smaller than this is not a real sloptor binary (real ones are >10MB).
const MIN_BINARY_SIZE = 1024 * 1024;

function targetInfo() {
  const os = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!os || !arch) {
    throw new Error(
      `sloptor: unsupported platform ${process.platform}-${process.arch} ` +
        `(supported: windows/linux/darwin on amd64/arm64). ` +
        `Build from source instead: go build ./cmd/rotor`
    );
  }
  return { os, arch, ext: os === "windows" ? ".exe" : "" };
}

// Where the platform binary lives inside the installed package.
function binaryPath() {
  const { os, arch, ext } = targetInfo();
  return path.join(__dirname, "..", "bin", `sloptor-${os}-${arch}${ext}`);
}

function assetUrl() {
  const { os, arch, ext } = targetInfo();
  const base = (
    process.env.ROTOR_INSTALL_BASE_URL ||
    `https://github.com/howmanysmall/rotor/releases/download/v${pkg.version}`
  ).replace(/\/+$/, "");
  return `${base}/sloptor-v${pkg.version}-${os}-${arch}-bin${ext}`;
}

async function install({ force = false, quiet = false } = {}) {
  const dest = binaryPath();

  if (!force) {
    try {
      if (fs.statSync(dest).size > MIN_BINARY_SIZE) return dest; // already installed
    } catch {
      /* not downloaded yet */
    }
  }

  const url = assetUrl();
  if (!quiet) console.error(`sloptor: downloading ${url}`);

  let res;
  try {
    res = await fetch(url, { redirect: "follow" });
  } catch (err) {
    throw new Error(
      `sloptor: download failed for ${url}\n  ${err && err.message ? err.message : err}`
    );
  }

  if (res.status === 404) {
    throw new Error(
      `sloptor: binary not found (HTTP 404):\n  ${url}\n` +
        `The v${pkg.version} release may not be published yet, or the asset for ` +
        `this platform is missing.\n` +
        `Grab a binary from https://github.com/howmanysmall/rotor/releases or build ` +
        `from source: go build ./cmd/rotor`
    );
  }
  if (!res.ok) {
    throw new Error(`sloptor: download failed (HTTP ${res.status}) for ${url}`);
  }

  const buf = Buffer.from(await res.arrayBuffer());

  // Write to a temp name then rename so a torn download never looks installed.
  const tmp = dest + ".download";
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(tmp, buf);
  if (process.platform !== "win32") fs.chmodSync(tmp, 0o755);
  fs.renameSync(tmp, dest);

  if (!quiet) {
    console.error(
      `sloptor: installed ${path.basename(dest)} (${(buf.length / 1048576).toFixed(1)} MB)`
    );
  }
  return dest;
}

module.exports = { install, binaryPath, assetUrl, MIN_BINARY_SIZE };

if (require.main === module) {
  install().catch((err) => {
    console.error(err && err.message ? err.message : err);
    process.exit(1);
  });
}
