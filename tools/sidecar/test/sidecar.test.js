const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");
const test = require("node:test");
const ts = require("typescript");

const sidecar = require("../index.js");

const projectDir = path.resolve(__dirname, "..", "testdata", "project");
const tsConfigPath = path.join(projectDir, "tsconfig.json");
const sourcePath = path.join(projectDir, "src", "example.ts");
const mainPath = path.resolve(__dirname, "..", "main.js");

function createProgram() {
  const parsed = ts.getParsedCommandLineOfConfigFile(tsConfigPath, {}, ts.sys);
  if (!parsed) {
    throw new Error("expected parsed tsconfig");
  }

  return ts.createProgram({
    rootNames: parsed.fileNames,
    options: parsed.options,
    projectReferences: parsed.projectReferences,
  });
}

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

test("getPluginConfigs keeps child plugins before parent extends plugins", () => {
  const configs = sidecar.getPluginConfigs(ts, tsConfigPath);

  assert.equal(configs.length, 3);
  assert.deepEqual(
    configs.map((config) => ({
      type: config.type ?? "program",
      import: config.import ?? "default",
      prefix: config.prefix,
      after: Boolean(config.after),
      afterDeclarations: Boolean(config.afterDeclarations),
    })),
    [
      { type: "program", import: "default", prefix: "after", after: true, afterDeclarations: false },
      { type: "config", import: "configTransformer", prefix: "before", after: false, afterDeclarations: false },
      { type: "raw", import: "rawTransformer", prefix: "afterDeclarations", after: false, afterDeclarations: true },
    ],
  );
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
  assert.equal(response.transformed[0].fileName, selectedPath);
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
