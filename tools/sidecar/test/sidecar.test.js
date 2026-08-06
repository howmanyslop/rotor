const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");
const test = require("node:test");
const ts = require("typescript");

const sidecar = require("../index.js");
const { createDeclarationTransformers } = require("../lib/declarations");

const projectDir = path.resolve(__dirname, "..", "testdata", "project");
const tsConfigPath = path.join(projectDir, "tsconfig.json");
const sourcePath = path.join(projectDir, "src", "example.ts");
const mainPath = path.resolve(__dirname, "..", "main.js");

// TypeScript reports file names with forward slashes on every platform, so
// comparisons against host paths must be slash-normalized to hold on Windows.
function toSlash(value) {
  return value.replace(/\\/g, "/");
}

function resolveOptions(configPath) {
  const parsed = ts.getParsedCommandLineOfConfigFile(configPath, {}, ts.sys);
  if (!parsed) {
    throw new Error("expected parsed tsconfig");
  }

  return parsed;
}

function createProgram() {
  const parsed = resolveOptions(tsConfigPath);

  return ts.createProgram({
    rootNames: parsed.fileNames,
    options: parsed.options,
    projectReferences: parsed.projectReferences,
  });
}

function describePluginConfigs(configs) {
  return configs.map((config) => ({
    type: config.type ?? "program",
    import: config.import ?? "default",
    prefix: config.prefix,
    after: Boolean(config.after),
    afterDeclarations: Boolean(config.afterDeclarations),
  }));
}

const PROJECT_PLUGIN_CONFIGS = [
  { type: "program", import: "default", prefix: "after", after: true, afterDeclarations: false },
  { type: "config", import: "configTransformer", prefix: "before", after: false, afterDeclarations: false },
  { type: "raw", import: "rawTransformer", prefix: "afterDeclarations", after: false, afterDeclarations: true },
];

function readProtocolLine(stream) {
  return new Promise((resolve, reject) => {
    let buffer = "";

    function cleanup() {
      stream.off("data", onData);
      stream.off("error", onError);
    }

    function onError(error) {
      cleanup();
      reject(error);
    }

    function onData(chunk) {
      buffer += chunk.toString();
      const newlineIndex = buffer.indexOf("\n");
      if (newlineIndex === -1) {
        return;
      }

      const line = buffer.slice(0, newlineIndex);
      cleanup();
      resolve(JSON.parse(line));
    }

    stream.on("data", onData);
    stream.on("error", onError);
  });
}

// The three tests below pin `extends` resolution to what `tsc --showConfig`
// reports: `plugins` is an array-valued compiler option, so a child that
// declares it REPLACES the parent's list rather than adding to it. rbxtsc
// concatenates the whole chain instead, which leaves a child no way to opt out
// of an inherited transform.
test("getPluginConfigs takes the child's plugins in place of the parent's", () => {
  const configs = sidecar.getPluginConfigs(resolveOptions(tsConfigPath).options);

  assert.deepEqual(describePluginConfigs(configs), PROJECT_PLUGIN_CONFIGS);
  assert.equal(
    configs.some((config) => config.prefix === "inherited"),
    false,
    "expected the parent's replaced plugin to be absent",
  );
});

test("getPluginConfigs honours a child that overrides plugins with an empty array", () => {
  const configs = sidecar.getPluginConfigs(resolveOptions(path.join(projectDir, "tsconfig.no-plugins.json")).options);

  assert.deepEqual(configs, []);
});

test("getPluginConfigs inherits the parent's plugins when the child declares none", () => {
  const configs = sidecar.getPluginConfigs(resolveOptions(path.join(projectDir, "tsconfig.inherit.json")).options);

  assert.deepEqual(describePluginConfigs(configs), PROJECT_PLUGIN_CONFIGS);
});

test("a project whose plugins are overridden to empty runs no transformers", () => {
  const configPath = path.join(projectDir, "tsconfig.no-plugins.json");
  const session = new sidecar.SidecarProjectSession(ts, projectDir, configPath);
  const response = session.handleRequest({
    protocol: 1,
    projectDir,
    tsConfigPath: configPath,
    compileFileNames: [sourcePath],
    changedFiles: [],
  });

  assert.deepEqual(response.diagnostics, []);
  assert.deepEqual(response.transformed, []);
});

test("createTransformerList instantiates checker and compilerOptions factories", () => {
  const program = createProgram();
  const sourceFile = program.getSourceFile(sourcePath);
  assert.ok(sourceFile, "expected source file");

  const { transforms, diagnostics } = sidecar.createTransformerList(ts, program, [
    {
      transform: "./plugins/prefix-string-named.js",
      import: "compilerOptionsTransformer",
      type: "compilerOptions",
      prefix: "options",
      after: true,
    },
    {
      transform: "./plugins/prefix-string-named.js",
      import: "checkerTransformer",
      type: "checker",
      prefix: "checker",
    },
  ], projectDir);

  assert.deepEqual(diagnostics, []);

  const result = sidecar.transformSourceFiles(ts, program, [sourceFile], transforms);
  assert.deepEqual(
    result.diagnostics,
    [],
  );
  assert.match(result.transformed[0].text, /options:checker:start/);
});

test("createTransformerList reports a non-function export as an error", () => {
  const program = createProgram();
  const { diagnostics } = sidecar.createTransformerList(ts, program, [
    {
      transform: "./plugins/prefix-string-named.js",
      import: "missingTransformer",
    },
  ], projectDir);

  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].category, "error");
  assert.equal(diagnostics[0].code, "transformer-not-found");
  assert.match(diagnostics[0].message, /Transformer `\.\/plugins\/prefix-string-named\.js` failed to load!/);
  assert.match(diagnostics[0].message, /factory not a function/);
  assert.match(diagnostics[0].message, /Suggestion: Did you forget to install the package, or to build it\?/);
});

test("transformSourceFiles omits source files whose transformers preserve identity", () => {
  const program = createProgram();
  const sourceFile = program.getSourceFile(sourcePath);
  assert.ok(sourceFile, "expected source file");

  const result = sidecar.transformSourceFiles(ts, program, [sourceFile], {
    before: [() => (file) => file],
    after: [],
    afterDeclarations: [],
  });

  assert.deepEqual(result.diagnostics, []);
  assert.deepEqual(result.transformed, []);
});

test("afterDeclarations emits only declaration output with builtin path rewrites", () => {
  const declarationProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-declaration-"));
  const sourceDir = path.join(declarationProjectDir, "src");
  fs.mkdirSync(sourceDir, { recursive: true });
  fs.writeFileSync(
    path.join(declarationProjectDir, "marker.js"),
    `const ts = require(${JSON.stringify(require.resolve("typescript"))});
module.exports = () => context => sourceFile => {
  const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
    context.factory.createVariableDeclaration("__DECLARATION_MARKER__", undefined, undefined, context.factory.createStringLiteral("after-declarations")),
  ], ts.NodeFlags.Const));
  return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
};
`,
  );
  fs.writeFileSync(
    path.join(declarationProjectDir, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        declaration: true,
        module: "CommonJS",
        moduleResolution: "Node",
        noLib: true,
        strict: true,
        target: "ESNext",
        baseUrl: ".",
        paths: { "@alias/*": ["src/*"] },
        rootDir: "src",
        outDir: "out",
        plugins: [{ transform: "./marker.js", afterDeclarations: true }],
      },
      include: ["src"],
    }),
  );
  const mainFile = path.join(sourceDir, "main.ts");
  fs.writeFileSync(mainFile, 'import type { Value } from "@alias/value";\nexport type Output = Value;\n');
  fs.writeFileSync(path.join(sourceDir, "value.ts"), "export interface Value { name: string; }\n");

  const session = new sidecar.SidecarProjectSession(ts, declarationProjectDir, path.join(declarationProjectDir, "tsconfig.json"));
  const response = session.handleRequest({
    protocol: 1,
    projectDir: declarationProjectDir,
    tsConfigPath: path.join(declarationProjectDir, "tsconfig.json"),
    compileFileNames: [mainFile],
    changedFiles: [],
  });

  assert.deepEqual(response.diagnostics, []);
  assert.deepEqual(response.transformed, []);
  assert.equal(response.declarations.length, 1);
  assert.match(response.declarations[0].text, /__DECLARATION_MARKER__/);
  assert.match(response.declarations[0].text, /from "\.\/value"/);
});

test("declaration path resolution reuses host probes within one declaration request", () => {
  const resolutionProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-resolution-cache-"));
  const sourceDir = path.join(resolutionProjectDir, "src");
  fs.mkdirSync(sourceDir, { recursive: true });
  fs.writeFileSync(
    path.join(resolutionProjectDir, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        declaration: true,
        module: "CommonJS",
        moduleResolution: "Node",
        noLib: true,
        strict: true,
        target: "ESNext",
        baseUrl: ".",
        paths: { "@alias/*": ["src/*"] },
        rootDir: "src",
        outDir: "out",
      },
      include: ["src"],
    }),
  );
  fs.writeFileSync(path.join(sourceDir, "one.ts"), 'import type { Value } from "@alias/value";\nexport type One = Value;\n');
  fs.writeFileSync(path.join(sourceDir, "two.ts"), 'import type { Value } from "@alias/value";\nexport type Two = Value;\n');
  fs.writeFileSync(path.join(sourceDir, "value.ts"), "export interface Value { name: string; }\n");

  const parsed = ts.getParsedCommandLineOfConfigFile(path.join(resolutionProjectDir, "tsconfig.json"), {}, ts.sys);
  assert.ok(parsed, "expected parsed tsconfig");
  const program = ts.createProgram({ rootNames: parsed.fileNames, options: parsed.options });
  const sourceFiles = ["one.ts", "two.ts"].map((fileName) => {
    const sourceFile = program.getSourceFile(path.join(sourceDir, fileName));
    assert.ok(sourceFile, `expected ${fileName}`);
    return sourceFile;
  });
  const probeCounts = [];
  const declarationTexts = [];

  for (const sourceFile of sourceFiles) {
    let probes = 0;
    const moduleResolutionHost = {
      ...ts.sys,
      fileExists(fileName) {
        probes += 1;
        return ts.sys.fileExists(fileName);
      },
    };
    const afterDeclarations = createDeclarationTransformers(ts, program, moduleResolutionHost);
    program.emit(sourceFile, (_fileName, text) => declarationTexts.push(text), undefined, true, { afterDeclarations });
    probeCounts.push(probes);
  }

  let requestProbes = 0;
  const requestHost = {
    ...ts.sys,
    fileExists(fileName) {
      requestProbes += 1;
      return ts.sys.fileExists(fileName);
    },
  };
  const requestDeclarations = [];
  const afterDeclarations = createDeclarationTransformers(ts, program, requestHost);
  for (const sourceFile of sourceFiles) {
    program.emit(sourceFile, (_fileName, text) => requestDeclarations.push(text), undefined, true, { afterDeclarations });
  }

  assert.ok(probeCounts.every((count) => count > 0), "separate declaration requests must probe the host");
  assert.ok(requestProbes < probeCounts[0] + probeCounts[1], `request probes = ${requestProbes}, separate probes = ${probeCounts}`);
  assert.deepEqual(requestDeclarations, declarationTexts);
  assert.deepEqual(requestDeclarations.map((text) => text.includes('from "./value"')), [true, true]);
});

test("declaration path resolution observes filesystem mutations on the next request", () => {
  const resolutionProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-resolution-boundary-"));
  const sourceDir = path.join(resolutionProjectDir, "src");
  const packagesDir = path.join(resolutionProjectDir, "packages");
  fs.mkdirSync(sourceDir, { recursive: true });
  fs.mkdirSync(path.join(packagesDir, "replace-package"), { recursive: true });
  fs.writeFileSync(
    path.join(resolutionProjectDir, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        declaration: true,
        module: "CommonJS",
        moduleResolution: "Node",
        noLib: true,
        strict: true,
        target: "ESNext",
        baseUrl: ".",
        paths: {
          "@fixture/new-package": ["packages/new-package"],
          "@fixture/replace-package": ["packages/replace-package"],
          "@fixture/deleted-target": ["src/deleted-target"],
        },
        rootDir: "src",
        outDir: "out",
      },
      include: ["src"],
    }),
  );
  const mainPath = path.join(sourceDir, "main.ts");
  fs.writeFileSync(
    mainPath,
    [
      'export type Generated = import("./generated").Generated;',
      'export type NewPackage = import("@fixture/new-package").NewPackage;',
      'export type Replaced = import("@fixture/replace-package").Replaced;',
      'export type Deleted = import("@fixture/deleted-target").Deleted;',
      "",
    ].join("\n"),
  );
  fs.writeFileSync(path.join(sourceDir, "deleted-target.ts"), "export interface Deleted { value: string; }\n");
  fs.writeFileSync(path.join(packagesDir, "replace-package", "package.json"), JSON.stringify({ types: "old.d.ts" }));
  fs.writeFileSync(path.join(packagesDir, "replace-package", "old.d.ts"), "export interface Replaced { version: \"old\"; }\n");

  const parsed = ts.getParsedCommandLineOfConfigFile(path.join(resolutionProjectDir, "tsconfig.json"), {}, ts.sys);
  assert.ok(parsed, "expected parsed tsconfig");
  const program = ts.createProgram({ rootNames: parsed.fileNames, options: parsed.options });
  const sourceFile = program.getSourceFile(mainPath);
  assert.ok(sourceFile, "expected main source file");
  const session = new sidecar.SidecarProjectSession(ts, resolutionProjectDir, path.join(resolutionProjectDir, "tsconfig.json"));

  function emitSessionRequest() {
    const response = session.handleRequest({
      protocol: 1,
      projectDir: resolutionProjectDir,
      tsConfigPath: path.join(resolutionProjectDir, "tsconfig.json"),
      compileFileNames: [mainPath],
      changedFiles: [],
    });
    assert.deepEqual(response.diagnostics, []);
    assert.equal(response.declarations.length, 1);
    return response.declarations.map((declaration) => declaration.text).join("");
  }

  function emitDeclarationForRequest() {
    const probes = [];
    const moduleResolutionHost = {
      ...ts.sys,
      fileExists(fileName) {
        const exists = ts.sys.fileExists(fileName);
        probes.push({ fileName, exists });
        return exists;
      },
    };
    const declarations = [];
    const afterDeclarations = createDeclarationTransformers(ts, program, moduleResolutionHost);
    program.emit(sourceFile, (_fileName, text) => declarations.push(text), undefined, true, { afterDeclarations });
    return { probes, text: declarations.join("") };
  }

  const firstSessionRequest = emitSessionRequest();
  const firstRequest = emitDeclarationForRequest();
  assert.match(firstSessionRequest, /import\("\.\/generated"\)/);
  assert.match(firstSessionRequest, /import\("@fixture\/new-package"\)/);
  assert.match(firstSessionRequest, /import\("\.\.\/packages\/replace-package\/old"\)/);
  assert.match(firstSessionRequest, /import\("\.\/deleted-target"\)/);
  assert.match(firstRequest.text, /import\("\.\/generated"\)/);
  assert.match(firstRequest.text, /import\("@fixture\/new-package"\)/);
  assert.match(firstRequest.text, /import\("\.\.\/packages\/replace-package\/old"\)/);
  assert.match(firstRequest.text, /import\("\.\/deleted-target"\)/);
  const generatedProbePath = toSlash(path.join(sourceDir, "generated.ts"));
  assert.ok(firstRequest.probes.some((probe) => toSlash(probe.fileName) === generatedProbePath && !probe.exists));

  fs.writeFileSync(path.join(sourceDir, "generated.ts"), "export interface Generated { value: string; }\n");
  fs.mkdirSync(path.join(packagesDir, "new-package"), { recursive: true });
  fs.writeFileSync(path.join(packagesDir, "new-package", "package.json"), JSON.stringify({ types: "entry.d.ts" }));
  fs.writeFileSync(path.join(packagesDir, "new-package", "entry.d.ts"), "export interface NewPackage { value: string; }\n");
  fs.writeFileSync(path.join(packagesDir, "replace-package", "package.json"), JSON.stringify({ types: "new.d.ts" }));
  fs.writeFileSync(path.join(packagesDir, "replace-package", "new.d.ts"), "export interface Replaced { version: \"new\"; }\n");
  fs.rmSync(path.join(sourceDir, "deleted-target.ts"));

  const secondSessionRequest = emitSessionRequest();
  const secondRequest = emitDeclarationForRequest();
  assert.match(secondSessionRequest, /import\("\.\/generated"\)/);
  assert.match(secondSessionRequest, /import\("\.\.\/packages\/new-package\/entry"\)/);
  assert.match(secondSessionRequest, /import\("\.\.\/packages\/replace-package\/new"\)/);
  assert.match(secondSessionRequest, /import\("@fixture\/deleted-target"\)/);
  assert.match(secondRequest.text, /import\("\.\/generated"\)/);
  assert.match(secondRequest.text, /import\("\.\.\/packages\/new-package\/entry"\)/);
  assert.match(secondRequest.text, /import\("\.\.\/packages\/replace-package\/new"\)/);
  assert.match(secondRequest.text, /import\("@fixture\/deleted-target"\)/);
  assert.ok(secondRequest.probes.some((probe) => toSlash(probe.fileName) === generatedProbePath && probe.exists));
});

test("transformSourceFiles repairs orphaned parents between transformers", () => {
  const program = createProgram();
  const sourceFile = program.getSourceFile(sourcePath);
  assert.ok(sourceFile, "expected source file");

  const createSyntheticLiteral = (context) => {
    const visit = (node) => {
      if (ts.isStringLiteral(node) && node.text === "start") {
        return ts.factory.createStringLiteral("synthetic");
      }
      return ts.visitEachChild(node, visit, context);
    };
    return (file) => ts.visitNode(file, visit);
  };
  const requireSyntheticParent = (context) => {
    const visit = (node) => {
      if (ts.isStringLiteral(node) && node.text === "synthetic") {
        assert.ok(node.parent, "synthetic node must have a parent before the next transformer");
        assert.ok(ts.isVariableDeclaration(node.parent));
      }
      return ts.visitEachChild(node, visit, context);
    };
    return (file) => ts.visitNode(file, visit);
  };

  const result = sidecar.transformSourceFiles(ts, program, [sourceFile], {
    before: [createSyntheticLiteral, requireSyntheticParent],
    after: [],
    afterDeclarations: [],
  });

  assert.deepEqual(result.diagnostics, []);
  assert.match(result.transformed[0].text, /synthetic/);
});

test("main.js runs before then after, excludes afterDeclarations, and reuses overlay updates", async () => {
  const child = spawn(process.execPath, [mainPath], {
    cwd: path.resolve(__dirname, ".."),
    stdio: ["pipe", "pipe", "pipe"],
  });

  const stderr = [];
  child.stderr.on("data", (chunk) => {
    stderr.push(chunk.toString());
  });

  try {
    const firstResponsePromise = readProtocolLine(child.stdout);
    child.stdin.write(`${JSON.stringify({
      protocol: 1,
      projectDir,
      tsConfigPath,
      compileFileNames: [sourcePath],
      changedFiles: [],
    })}\n`);

    const firstResponse = await firstResponsePromise;
    assert.deepEqual(firstResponse.diagnostics, []);
    assert.equal(firstResponse.transformed.length, 1);
    assert.match(firstResponse.transformed[0].text, /after:before:start/);

    const secondResponsePromise = readProtocolLine(child.stdout);
    child.stdin.write(`${JSON.stringify({
      protocol: 1,
      projectDir,
      tsConfigPath,
      compileFileNames: [sourcePath],
      changedFiles: [
        {
          fileName: sourcePath,
          text: 'export const phase = "memory";\n',
        },
      ],
    })}\n`);

    const secondResponse = await secondResponsePromise;
    assert.deepEqual(secondResponse.diagnostics, []);
    assert.equal(secondResponse.transformed.length, 1);
    assert.match(secondResponse.transformed[0].text, /after:before:memory/);
  } finally {
    child.stdin.end();
    await new Promise((resolve) => child.once("exit", resolve));
  }

  assert.deepEqual(stderr, []);
});

test("main.js honors shouldTransformSourceFile and omits skipped source files", () => {
  const hookProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-hook-"));
  const sourceDir = path.join(hookProjectDir, "src");
  fs.mkdirSync(sourceDir, { recursive: true });

  fs.writeFileSync(
    path.join(hookProjectDir, "selective-plugin.js"),
    `const ts = require(${JSON.stringify(require.resolve("typescript"))});

function prefix(prefix) {
  return (context) => {
    const visit = (node) => {
      if (ts.isStringLiteral(node)) {
        return ts.factory.createStringLiteral(prefix + ":" + node.text);
      }
      return ts.visitEachChild(node, visit, context);
    };
    return (sourceFile) => ts.visitNode(sourceFile, visit);
  };
}

module.exports = function () {
  return {
    before: prefix("before"),
    after: prefix("after"),
    afterDeclarations: prefix("afterDeclarations"),
  };
};

module.exports.shouldTransformSourceFile = function (sourceFile, program, config) {
  if (!program.getTypeChecker() || config.transform !== "./selective-plugin.js") {
    throw new Error("missing source-file hook inputs");
  }
  return sourceFile.fileName.endsWith("selected.ts");
};
`,
  );
  fs.writeFileSync(
    path.join(hookProjectDir, "no-hooks-plugin.js"),
    `module.exports = function () { return {}; };
module.exports.shouldTransformSourceFile = true;
`,
  );
  fs.writeFileSync(
    path.join(hookProjectDir, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        module: "CommonJS",
        moduleResolution: "Node",
        noLib: true,
        moduleDetection: "force",
        target: "ESNext",
        types: [],
        rootDir: "src",
        outDir: "out",
        plugins: [{ transform: "./selective-plugin.js" }, { transform: "./no-hooks-plugin.js" }],
      },
      include: ["src"],
    }),
  );
  const selectedPath = path.join(sourceDir, "selected.ts");
  const skippedPath = path.join(sourceDir, "skipped.ts");
  fs.writeFileSync(selectedPath, 'export const phase = "selected";\n');
  fs.writeFileSync(skippedPath, 'export const phase = "skipped";\n');

  const result = spawnSync(process.execPath, [mainPath], {
    input: `${JSON.stringify({
      protocol: 1,
      projectDir: hookProjectDir,
      tsConfigPath: path.join(hookProjectDir, "tsconfig.json"),
      compileFileNames: [selectedPath, skippedPath],
      changedFiles: [],
    })}\n`,
    encoding: "utf8",
    cwd: hookProjectDir,
  });

  assert.equal(result.status, 0, result.stderr);
  const response = JSON.parse(result.stdout.trim());
  assert.deepEqual(response.diagnostics, []);
  assert.equal(response.transformed.length, 1);
  assert.equal(toSlash(response.transformed[0].fileName), toSlash(selectedPath));
  assert.match(response.transformed[0].text, /after:before:selected/);
  assert.doesNotMatch(response.transformed[0].text, /afterDeclarations/);
});

test("main.js keeps plugin console.log off the protocol stream", () => {
  const noisyProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-stdout-"));
  fs.mkdirSync(path.join(noisyProjectDir, "src"), { recursive: true });
  fs.writeFileSync(
    path.join(noisyProjectDir, "noisy-plugin.js"),
    `module.exports = function (program, config) {
  console.log("plugin chatter on stdout");
  return (context) => (sourceFile) => sourceFile;
};\n`,
  );
  fs.writeFileSync(
    path.join(noisyProjectDir, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        module: "CommonJS",
        moduleResolution: "Node",
        noLib: true,
        moduleDetection: "force",
        target: "ESNext",
        types: [],
        rootDir: "src",
        outDir: "out",
        plugins: [{ transform: "./noisy-plugin.js" }],
      },
      include: ["src"],
    }),
  );
  const noisyMainFile = path.join(noisyProjectDir, "src", "main.ts");
  fs.writeFileSync(noisyMainFile, 'export const phase = "start";\n');

  const request = JSON.stringify({
    protocol: 1,
    tsConfigPath: path.join(noisyProjectDir, "tsconfig.json"),
    projectDir: noisyProjectDir,
    compileFileNames: [noisyMainFile],
    changedFiles: [],
  });

  const result = spawnSync(process.execPath, [mainPath], {
    input: `${request}\n`,
    encoding: "utf8",
    cwd: noisyProjectDir,
  });

  assert.equal(result.status, 0, result.stderr);
  const lines = result.stdout.split("\n").filter((line) => line.trim().length > 0);
  assert.equal(lines.length, 1, `stdout must carry exactly one JSON response, got:\n${result.stdout}`);
  const response = JSON.parse(lines[0]);
  assert.deepEqual(response.diagnostics, []);
  assert.match(result.stderr, /plugin chatter on stdout/);
});

test("resolveTypeScript prefers the project's typescript copy", () => {
  const stubProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-ts-"));
  const stubDir = path.join(stubProjectDir, "node_modules", "typescript");
  fs.mkdirSync(stubDir, { recursive: true });
  fs.writeFileSync(
    path.join(stubDir, "package.json"),
    JSON.stringify({ name: "typescript", version: "0.0.0-stub", main: "index.js" }),
  );
  fs.writeFileSync(path.join(stubDir, "index.js"), "module.exports = { __rotorStub: true };\n");

  const resolved = sidecar.resolveTypeScript(stubProjectDir);
  assert.equal(resolved.__rotorStub, true);
});

test("resolveTypeScript falls back to the sidecar's own typescript", () => {
  const emptyProjectDir = fs.mkdtempSync(path.join(os.tmpdir(), "rotor-sidecar-nots-"));
  const resolved = sidecar.resolveTypeScript(emptyProjectDir);
  assert.equal(typeof resolved.transformNodes, "function");
});
