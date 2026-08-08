const path = require("node:path");
const {
  createInternalDiagnostic,
  createProtocolDiagnostic,
  createRequestDiagnostic,
  toProtocolDiagnostic,
} = require("./diagnostics");
const {
  createTransformerList,
  flattenIntoTransformers,
  getPluginConfigs,
  wrapTransformersWithParentFix,
} = require("./plugins");
const { createDeclarationTransformers } = require("./declarations");

function createParseHost(session, ts) {
  return {
    useCaseSensitiveFileNames: ts.sys.useCaseSensitiveFileNames,
    readDirectory: ts.sys.readDirectory,
    fileExists: (fileName) => session.fileExists(fileName),
    readFile: (fileName) => session.readFile(fileName),
    getCurrentDirectory: () => session.projectDir,
    onUnRecoverableConfigFileDiagnostic: () => undefined,
  };
}

function createServiceHost(session, ts) {
  return {
    getCompilationSettings: () => session.parsed.options,
    getCurrentDirectory: () => session.projectDir,
    getDefaultLibFileName: (options) => ts.getDefaultLibFilePath(options),
    getDirectories: ts.sys.getDirectories,
    directoryExists: ts.sys.directoryExists,
    fileExists: (fileName) => session.fileExists(fileName),
    getProjectVersion: () => String(session.projectVersion),
    getProjectReferences: () => session.parsed.projectReferences,
    getScriptFileNames: () => session.getScriptFileNames(),
    getScriptSnapshot: (fileName) => {
      const text = session.readFile(fileName);
      if (text === undefined) {
        return undefined;
      }

      return ts.ScriptSnapshot.fromString(text);
    },
    getScriptVersion: (fileName) => String(session.getScriptVersion(fileName)),
    readDirectory: ts.sys.readDirectory,
    readFile: (fileName) => session.readFile(fileName),
    realpath: ts.sys.realpath ? (fileName) => ts.sys.realpath(fileName) : undefined,
    useCaseSensitiveFileNames: () => ts.sys.useCaseSensitiveFileNames,
  };
}

function normalizePath(fileName) {
  return path.normalize(path.resolve(fileName));
}

class SidecarProjectSession {
  constructor(ts, projectDir, tsConfigPath) {
    this.ts = ts;
    this.projectDir = normalizePath(projectDir);
    this.tsConfigPath = normalizePath(tsConfigPath);
    this.documentRegistry = ts.createDocumentRegistry(ts.sys.useCaseSensitiveFileNames);
    this.overrides = new Map();
    this.actualPaths = new Map();
    this.versions = new Map();
    this.projectVersion = 0;
    this.configSignature = "";
    this.parsed = undefined;
    this.service = undefined;
  }

  canonicalize(fileName) {
    const resolved = normalizePath(path.isAbsolute(fileName) ? fileName : path.join(this.projectDir, fileName));
    return this.ts.sys.useCaseSensitiveFileNames ? resolved : resolved.toLowerCase();
  }

  rememberFile(fileName) {
    const actualPath = normalizePath(path.isAbsolute(fileName) ? fileName : path.join(this.projectDir, fileName));
    const canonical = this.canonicalize(actualPath);
    if (!this.actualPaths.has(canonical)) {
      this.actualPaths.set(canonical, actualPath);
      this.versions.set(canonical, 0);
      this.projectVersion += 1;
    }

    return this.actualPaths.get(canonical);
  }

  fileExists(fileName) {
    const canonical = this.canonicalize(fileName);
    if (this.overrides.has(canonical)) {
      return true;
    }

    const actualPath = this.actualPaths.get(canonical) ?? normalizePath(fileName);
    return this.ts.sys.fileExists(actualPath);
  }

  readFile(fileName) {
    const canonical = this.canonicalize(fileName);
    if (this.overrides.has(canonical)) {
      return this.overrides.get(canonical);
    }

    const actualPath = this.actualPaths.get(canonical) ?? normalizePath(fileName);
    return this.ts.sys.readFile(actualPath);
  }

  updateFile(fileName, text) {
    const actualPath = this.rememberFile(fileName);
    const canonical = this.canonicalize(actualPath);
    if (this.overrides.get(canonical) === text) {
      return actualPath;
    }
    this.overrides.set(canonical, text);
    this.versions.set(canonical, (this.versions.get(canonical) ?? 0) + 1);
    this.projectVersion += 1;
    return actualPath;
  }

  getScriptFileNames() {
    const fileNames = [];
    for (const [canonical, actualPath] of this.actualPaths.entries()) {
      if (this.overrides.has(canonical) || this.ts.sys.fileExists(actualPath)) {
        fileNames.push(actualPath);
      }
    }
    return fileNames;
  }

  getScriptVersion(fileName) {
    const canonical = this.canonicalize(fileName);
    return this.versions.get(canonical) ?? 0;
  }

  refreshParsedConfig() {
    const parsed = this.ts.getParsedCommandLineOfConfigFile(
      this.tsConfigPath,
      {},
      createParseHost(this, this.ts),
    );
    if (!parsed) {
      return {
        diagnostics: [createProtocolDiagnostic("error", "config-parse", `Failed to parse ${this.tsConfigPath}`)],
      };
    }

    for (const fileName of parsed.fileNames) {
      this.rememberFile(fileName);
    }

    const configSignature = JSON.stringify({
      fileNames: [...parsed.fileNames].sort(),
      options: parsed.options,
      projectReferences: (parsed.projectReferences ?? []).map((reference) => reference.path),
    });

    if (!this.parsed || this.configSignature !== configSignature) {
      this.parsed = parsed;
      this.configSignature = configSignature;
      this.service = this.ts.createLanguageService(createServiceHost(this, this.ts), this.documentRegistry);
      this.projectVersion += 1;
    }

    return {
      diagnostics: parsed.errors.map((diagnostic) => toProtocolDiagnostic(this.ts, diagnostic)),
      parsed,
    };
  }

  getSourceFile(program, fileName) {
    const actualPath = this.rememberFile(fileName);
    const direct = program.getSourceFile(actualPath);
    if (direct) {
      return direct;
    }

    const canonical = this.canonicalize(actualPath);
    return program.getSourceFiles().find((sourceFile) => this.canonicalize(sourceFile.fileName) === canonical);
  }

  transformSourceFiles(program, sourceFiles, transforms) {
    const transformerList = wrapTransformersWithParentFix(this.ts, flattenIntoTransformers(transforms));
    const printer = this.ts.createPrinter({ removeComments: program.getCompilerOptions().removeComments });

    if (transformerList.length === 0) {
      return {
        diagnostics: [],
        transformed: [],
      };
    }

    if (typeof this.ts.transformNodes !== "function") {
      return {
        diagnostics: [createProtocolDiagnostic("error", "transform-nodes-missing", "typescript.transformNodes is unavailable")],
        transformed: [],
      };
    }

    const result = this.ts.transformNodes(
      undefined,
      undefined,
      this.ts.factory,
      program.getCompilerOptions(),
      sourceFiles,
      transformerList,
      false,
    );

    try {
      return {
        diagnostics: (result.diagnostics ?? []).map((diagnostic) => toProtocolDiagnostic(this.ts, diagnostic)),
        transformed: result.transformed
          .filter((node) => this.ts.isSourceFile(node) && !sourceFiles.includes(node))
          .map((sourceFile) => ({
            fileName: sourceFile.fileName,
            ...printSourceFileWithTraceMap(this.ts, printer, sourceFile),
          })),
      };
    } finally {
      if (typeof result.dispose === "function") {
        result.dispose();
      }
    }
  }

  emitDeclarationFiles(program, sourceFiles, transforms) {
    if (!program.getCompilerOptions().declaration || sourceFiles.length === 0) {
      return { diagnostics: [], declarations: [] };
    }

    const declarations = [];
    const afterDeclarations = [
      ...wrapTransformersWithParentFix(this.ts, transforms.afterDeclarations),
      // The service host is the module-resolution host too: every member
      // ts.resolveModuleName asks for is on it, answered from this session's
      // overrides. ts.sys would answer from disk, so an overlay that adds an
      // import would resolve against a tree that does not have it.
      ...createDeclarationTransformers(this.ts, program, createServiceHost(this, this.ts)),
    ];
    const customTransformers = { afterDeclarations };
    const diagnostics = [];
    for (const sourceFile of sourceFiles) {
      const result = program.emit(
        sourceFile,
        (fileName, text) => declarations.push({ fileName, text }),
        undefined,
        true,
        customTransformers,
      );
      diagnostics.push(...(result.diagnostics ?? []).map((diagnostic) => toProtocolDiagnostic(this.ts, diagnostic)));
    }
    return { diagnostics, declarations };
  }

  handleRequest(request) {
    try {
      for (const changedFile of request.changedFiles) {
        this.updateFile(changedFile.fileName, changedFile.text);
      }

      const parsedState = this.refreshParsedConfig();
      if (!this.parsed) {
        return { diagnostics: parsedState.diagnostics, transformed: [] };
      }

      const program = this.service.getProgram();
      if (!program) {
        return {
          diagnostics: [...parsedState.diagnostics, createProtocolDiagnostic("error", "program-missing", "Language service did not return a program")],
          transformed: [],
        };
      }

      const pluginConfigs = getPluginConfigs(this.parsed.options);
      const { transforms, diagnostics: pluginDiagnostics } = createTransformerList(this.ts, program, pluginConfigs, this.projectDir);

      const diagnostics = [...parsedState.diagnostics, ...pluginDiagnostics];
      const sourceFiles = [];
      for (const fileName of request.compileFileNames) {
        const sourceFile = this.getSourceFile(program, fileName);
        if (!sourceFile) {
          diagnostics.push(createProtocolDiagnostic("error", "source-file-missing", `Source file not found in program: ${fileName}`));
          continue;
        }
        sourceFiles.push(sourceFile);
      }

      if (sourceFiles.length === 0) {
        return { diagnostics, transformed: [] };
      }

      const transformResult = this.transformSourceFiles(program, sourceFiles, transforms);
      const declarationResult = this.emitDeclarationFiles(program, sourceFiles, transforms);
      return {
        diagnostics: [...diagnostics, ...transformResult.diagnostics, ...declarationResult.diagnostics],
        transformed: transformResult.transformed,
        declarations: declarationResult.declarations,
      };
    } catch (error) {
      return {
        diagnostics: [createInternalDiagnostic(error)],
        transformed: [],
        declarations: [],
      };
    }
  }
}

// The trace map is not an emit artifact — rotor needs it to put every position
// this reprint produces (diagnostics included) back into the coordinates of the
// file on disk. So it is generated whether or not the project emits source
// maps; a project that turned sourceMap off would otherwise report every
// diagnostic at an offset into text nobody can see.
function printSourceFileWithTraceMap(ts, printer, sourceFile) {
  const writer = ts.createTextWriter("\n");
  const generator = ts.createSourceMapGenerator(
    {
      getCurrentDirectory: () => "",
      getCanonicalFileName: (fileName) => fileName,
    },
    sourceFile.fileName,
    "",
    "",
    {},
  );
  printer.writeFile(sourceFile, writer, generator);
  return { text: writer.getText(), traceMap: JSON.stringify(generator.toJSON()) };
}

function validateRequest(request) {
  if (!request || typeof request !== "object") {
    return createRequestDiagnostic("request must be a JSON object");
  }
  if (request.protocol !== 1) {
    return createRequestDiagnostic("protocol must equal 1");
  }
  if (typeof request.projectDir !== "string" || request.projectDir.length === 0) {
    return createRequestDiagnostic("projectDir must be a non-empty string");
  }
  if (typeof request.tsConfigPath !== "string" || request.tsConfigPath.length === 0) {
    return createRequestDiagnostic("tsConfigPath must be a non-empty string");
  }
  if (!Array.isArray(request.compileFileNames) || !request.compileFileNames.every((fileName) => typeof fileName === "string")) {
    return createRequestDiagnostic("compileFileNames must be an array of strings");
  }
  if (!Array.isArray(request.changedFiles)) {
    return createRequestDiagnostic("changedFiles must be an array");
  }
  for (const changedFile of request.changedFiles) {
    if (!changedFile || typeof changedFile.fileName !== "string" || typeof changedFile.text !== "string") {
      return createRequestDiagnostic("each changedFiles item must include string fileName and text");
    }
  }
  return undefined;
}

class SidecarServer {
  constructor(tsOrLoader) {
    this.loadTypeScript = typeof tsOrLoader === "function" ? tsOrLoader : () => tsOrLoader;
    this.session = undefined;
    this.sessionKey = "";
  }

  handleRequest(request) {
    const validationError = validateRequest(request);
    if (validationError) {
      return { diagnostics: [validationError], transformed: [] };
    }

    const sessionKey = `${normalizePath(request.projectDir)}\u0000${normalizePath(request.tsConfigPath)}`;
    if (!this.session || this.sessionKey !== sessionKey) {
      let ts;
      try {
        ts = this.loadTypeScript(request.projectDir);
      } catch (error) {
        return {
          diagnostics: [
            createProtocolDiagnostic(
              "error",
              "typescript-not-found",
              `Could not resolve the \`typescript\` package from ${request.projectDir}.\n` +
                `Transformer plugins require typescript in the project's node_modules (roblox-ts projects pin ~5.5.3).\n` +
                `More info: ${error instanceof Error ? error.message : String(error)}`,
            ),
          ],
          transformed: [],
        };
      }
      this.session = new SidecarProjectSession(ts, request.projectDir, request.tsConfigPath);
      this.sessionKey = sessionKey;
    }

    return this.session.handleRequest(request);
  }
}

module.exports = {
  SidecarProjectSession,
  SidecarServer,
};
