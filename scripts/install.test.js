#!/usr/bin/env node
// Manual test for the npm packaging (scripts/install.js + bin/rotor.js).
// Not wired into `go test` — run directly:
//
//   node scripts/install.test.js
//
// Verifies, without needing a published GitHub release:
//   1. install() downloads the platform asset from ROTOR_INSTALL_BASE_URL,
//      writes bin/sloptor-<os>-<arch>(.exe), and makes it executable (unix).
//   2. install() skips the download when the binary already exists (>1MB).
//   3. install() fails with a clear error naming the URL on HTTP 404.
//   4. bin/rotor.js spawns the binary, forwarding args, stdout, and exit code.
//   5. bin/rotor.js auto-downloads a missing binary before spawning (the
//      bun-without-trustedDependencies path). Needs cross-process loopback;
//      probed first and skipped with a warning where blocked (some sandboxes).
//   6. `npm pack --dry-run` ships only the intended files.
//
// The fake "sloptor binary" served over HTTP is a copy of the current Node
// executable: it is >1MB and runnable on every platform, so the shim's
// spawn/arg-forwarding/exit-code path can be tested for real.
"use strict";

const assert = require("assert");
const fs = require("fs");
const http = require("http");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");

const repoRoot = path.join(__dirname, "..");
const pkg = require(path.join(repoRoot, "package.json"));

const PLATFORM_MAP = { win32: "windows", linux: "linux", darwin: "darwin" };
const ARCH_MAP = { x64: "amd64", arm64: "arm64" };
const osName = PLATFORM_MAP[process.platform];
const archName = ARCH_MAP[process.arch];
const ext = osName === "windows" ? ".exe" : "";
const assetName = `sloptor-v${pkg.version}-${osName}-${archName}-bin${ext}`;
const localBinName = `sloptor-${osName}-${archName}${ext}`;

let passed = 0;
function ok(name) {
  passed++;
  console.log(`  ok ${passed} - ${name}`);
}

function listen(server) {
  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () =>
      resolve(`http://127.0.0.1:${server.address().port}`)
    );
  });
}

function runNode(args, opts) {
  return spawnSync(process.execPath, args, { encoding: "utf8", ...opts });
}

// Some sandboxes (including certain CI/agent harnesses) silently drop loopback
// connections between sibling processes. Detect that so the one test that
// needs a child process to reach our in-test HTTP server can be skipped with a
// warning instead of hanging or failing spuriously.
function childCanReachLoopback(url) {
  const probe =
    "require('http').get(process.env.U,r=>{r.resume();r.on('end',()=>process.exit(0))})" +
    ".on('error',()=>process.exit(1));setTimeout(()=>process.exit(1),4000);";
  const r = runNode(["-e", probe], {
    env: { ...process.env, U: url },
    timeout: 8000,
  });
  return r.status === 0;
}

async function main() {
  // -- stage a copy of the package layout in a temp dir, so downloads never
  //    land in the real repo's bin/ ------------------------------------------
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "sloptor-npm-test-"));
  fs.mkdirSync(path.join(tmp, "bin"));
  fs.mkdirSync(path.join(tmp, "scripts"));
  fs.copyFileSync(path.join(repoRoot, "package.json"), path.join(tmp, "package.json"));
  fs.copyFileSync(path.join(repoRoot, "bin", "rotor.js"), path.join(tmp, "bin", "rotor.js"));
  fs.copyFileSync(
    path.join(repoRoot, "scripts", "install.js"),
    path.join(tmp, "scripts", "install.js")
  );

  // Drive the staged copy in-process — the very module bin/rotor.js requires.
  const installer = require(path.join(tmp, "scripts", "install.js"));

  const fakeBinary = fs.readFileSync(process.execPath); // >1MB, runnable
  const requests = [];
  const server = http.createServer((req, res) => {
    requests.push(req.url);
    res.writeHead(200, { "content-type": "application/octet-stream" });
    res.end(fakeBinary);
  });
  const baseUrl = await listen(server);

  const notFound = http.createServer((req, res) => {
    res.writeHead(404);
    res.end("Not Found");
  });
  const notFoundUrl = await listen(notFound);

  const installedBin = path.join(tmp, "bin", localBinName);
  const env = { ...process.env, ROTOR_INSTALL_BASE_URL: baseUrl };
  const savedBase = process.env.ROTOR_INSTALL_BASE_URL;

  try {
    process.env.ROTOR_INSTALL_BASE_URL = baseUrl;

    // 1. install() downloads the asset to bin/sloptor-<os>-<arch>(.exe)
    assert.strictEqual(installer.binaryPath(), installedBin, "binaryPath mismatch");
    assert.strictEqual(installer.assetUrl(), `${baseUrl}/${assetName}`, "assetUrl mismatch");
    let dest = await installer.install({ quiet: true });
    assert.strictEqual(dest, installedBin);
    assert.deepStrictEqual(requests, [`/${assetName}`], "requested wrong asset path");
    assert.strictEqual(fs.statSync(installedBin).size, fakeBinary.length);
    if (process.platform !== "win32") {
      assert.strictEqual(fs.statSync(installedBin).mode & 0o777, 0o755, "not chmod 755");
    }
    assert.ok(!fs.existsSync(installedBin + ".download"), "temp download file left behind");
    ok(`install() downloads /${assetName} -> bin/${localBinName}`);

    // 2. re-run skips the download (binary exists, >1MB)
    await installer.install({ quiet: true });
    assert.strictEqual(requests.length, 1, "second install() should not re-download");
    ok("install() skips download when binary already present");

    // 3. 404 produces a clear error that names the URL
    process.env.ROTOR_INSTALL_BASE_URL = notFoundUrl;
    let threw = null;
    try {
      await installer.install({ force: true, quiet: true });
    } catch (err) {
      threw = err;
    }
    assert.ok(threw, "install() should reject on 404");
    assert.match(threw.message, /404/);
    assert.ok(
      threw.message.includes(`${notFoundUrl}/${assetName}`),
      `404 error should contain the URL, got: ${threw.message}`
    );
    assert.match(threw.message, /releases/, "404 error should point at GitHub releases");
    assert.strictEqual(
      fs.statSync(installedBin).size,
      fakeBinary.length,
      "failed re-download must not clobber the existing binary"
    );
    process.env.ROTOR_INSTALL_BASE_URL = baseUrl;
    ok("install() fails on 404 with the asset URL in the message");

    // 4. shim spawns the existing binary, forwarding args + exit code.
    //    The fake binary is node, so `-e ...` proves args reach it verbatim.
    let r = runNode(
      [
        path.join(tmp, "bin", "rotor.js"),
        "-e",
        "console.log('fake-rotor ran'); process.exit(7)",
      ],
      { cwd: tmp, env }
    );
    assert.match(r.stdout, /fake-rotor ran/, `stdout not forwarded: ${r.stderr}`);
    assert.strictEqual(r.status, 7, "exit code not forwarded");
    ok("bin/rotor.js spawns binary, forwards args, stdout, and exit code 7");

    // 5. shim auto-downloads when the binary is missing (bun-without-
    //    trustedDependencies path), then runs it — needs the child process to
    //    reach our loopback server, which some sandboxes block.
    if (childCanReachLoopback(`${baseUrl}/probe`)) {
      requests.length = 0;
      fs.rmSync(installedBin);
      r = runNode(
        [path.join(tmp, "bin", "rotor.js"), "-e", "process.exit(3)"],
        { cwd: tmp, env }
      );
      assert.deepStrictEqual(requests, [`/${assetName}`], "shim should have re-downloaded");
      assert.strictEqual(r.status, 3, `shim auto-download run failed: ${r.stderr}`);
      assert.match(r.stderr, /downloading/, "expected download notice on stderr");
      ok("bin/rotor.js auto-downloads missing binary before spawning");
    } else {
      console.log(
        "  SKIP - shim auto-download E2E: this environment blocks cross-process" +
          " loopback connections (run on a normal machine to exercise it)"
      );
    }

    // 6. npm pack ships only the intended files (run against the real repo)
    r = spawnSync("npm pack --dry-run --json", {
      cwd: repoRoot,
      encoding: "utf8",
      shell: true,
    });
    assert.strictEqual(r.status, 0, `npm pack failed: ${r.stderr}`);
    const files = JSON.parse(r.stdout)[0].files.map((f) => f.path.replace(/\\/g, "/"));
    const allowed = new Set([
      "package.json",
      "README.md",
      "LICENSE",
      "bin/rotor.js",
      "scripts/install.js",
    ]);
    const unexpected = files.filter((f) => !allowed.has(f));
    assert.deepStrictEqual(unexpected, [], `npm pack includes extra files: ${unexpected}`);
    assert.ok(files.includes("bin/rotor.js"), "npm pack missing bin/rotor.js");
    assert.ok(files.includes("scripts/install.js"), "npm pack missing scripts/install.js");
    ok(`npm pack --dry-run ships only: ${files.sort().join(", ")}`);

    console.log(`\nall ${passed} checks passed`);
  } finally {
    if (savedBase === undefined) delete process.env.ROTOR_INSTALL_BASE_URL;
    else process.env.ROTOR_INSTALL_BASE_URL = savedBase;
    server.close();
    notFound.close();
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error(`FAIL after ${passed} passing checks:`);
  console.error(err && err.stack ? err.stack : err);
  process.exit(1);
});
