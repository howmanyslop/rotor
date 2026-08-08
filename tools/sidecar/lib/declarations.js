const path = require("node:path");

const typeReferencePackage = "types";
const typeReferenceScope = "@rbxts/";

// moduleResolutionHost is required rather than defaulted to ts.sys: a default
// would silently resolve against disk, which is the behavior the session's
// override map exists to replace.
function createDeclarationTransformers(ts, program, moduleResolutionHost) {
  return [transformTypeReferenceDirectives(ts), transformPaths(ts, program, moduleResolutionHost)];
}

function transformTypeReferenceDirectives(ts) {
  return () => (sourceFile) => {
    if (!ts.isSourceFile(sourceFile)) {
      return sourceFile;
    }

    for (const directive of sourceFile.typeReferenceDirectives) {
      if (directive.fileName === typeReferencePackage) {
        directive.fileName = typeReferenceScope + typeReferencePackage;
        directive.end += typeReferenceScope.length;
      }
    }
    return sourceFile;
  };
}

function transformPaths(ts, program, moduleResolutionHost) {
  const compilerOptions = program.getCompilerOptions();
  const currentDirectory = program.getCurrentDirectory();
  const canonicalName = (fileName) => {
    const actualPath = path.isAbsolute(fileName) ? fileName : path.join(currentDirectory, fileName);
    const resolved = path.normalize(path.resolve(actualPath));
    return ts.sys.useCaseSensitiveFileNames ? resolved : resolved.toLowerCase();
  };
  const moduleResolutionCache = ts.createModuleResolutionCache(currentDirectory, canonicalName, compilerOptions);
  const implicitExtensions = [".ts", ".d.ts"];
  if (compilerOptions.allowJs) {
    implicitExtensions.push(".js");
  }
  const allowsJSX = compilerOptions.jsx !== undefined && compilerOptions.jsx !== ts.JsxEmit.None;
  if (allowsJSX) {
    implicitExtensions.push(".tsx");
  }
  if (compilerOptions.allowJs && allowsJSX) {
    implicitExtensions.push(".jsx");
  }
  if (compilerOptions.resolveJsonModule) {
    implicitExtensions.push(".json");
  }

  return (context) => (sourceFile) => {
    if (ts.isBundle(sourceFile) || (!compilerOptions.baseUrl && !compilerOptions.paths)) {
      return sourceFile;
    }

    const fileName = sourceFile.fileName;
    const fileDirectory = ts.normalizePath(path.dirname(fileName));
    return ts.visitEachChild(sourceFile, visit, context);

    function update(original, moduleName, updatePath) {
      const { resolvedModule, failedLookupLocations } = ts.resolveModuleName(
        moduleName,
        fileName,
        compilerOptions,
        moduleResolutionHost,
        moduleResolutionCache,
      );
      let resolvedPath;
      if (!resolvedModule) {
        const possibleURL = failedLookupLocations?.[0];
        if (!isURL(possibleURL)) {
          return original;
        }
        resolvedPath = possibleURL;
      } else if (resolvedModule.isExternalLibraryImport) {
        return original;
      } else {
        let directory = fileDirectory;
        let moduleDirectory = path.dirname(resolvedModule.resolvedFileName);
        const rootDirectories = compilerOptions.rootDirs?.filter(path.isAbsolute);
        if (rootDirectories) {
          let fileRootDirectory = "";
          let moduleRootDirectory = "";
          for (const rootDirectory of rootDirectories) {
            if (path.relative(rootDirectory, fileName)?.[0] !== "." && rootDirectory.length > fileRootDirectory.length) {
              fileRootDirectory = rootDirectory;
            }
            if (path.relative(rootDirectory, resolvedModule.resolvedFileName)?.[0] !== "." && rootDirectory.length > moduleRootDirectory.length) {
              moduleRootDirectory = rootDirectory;
            }
          }
          if (fileRootDirectory && moduleRootDirectory) {
            directory = path.relative(fileRootDirectory, directory);
            moduleDirectory = path.relative(moduleRootDirectory, moduleDirectory);
          }
        }
        resolvedPath = ts.normalizePath(path.join(path.relative(directory, moduleDirectory), path.basename(resolvedModule.resolvedFileName)));
        if (resolvedModule.extension && implicitExtensions.includes(resolvedModule.extension)) {
          resolvedPath = resolvedPath.slice(0, -resolvedModule.extension.length);
        }
        if (!resolvedPath) {
          return original;
        }
        if (resolvedPath[0] !== ".") {
          resolvedPath = `./${resolvedPath}`;
        }
      }
      return updatePath(context.factory.createStringLiteral(resolvedPath));
    }

    function updateModuleSpecifier(node, moduleSpecifier, updateNode) {
      return update(node, moduleSpecifier.text, (newPath) => {
        ts.setSourceMapRange(newPath, ts.getSourceMapRange(node));
        ts.setTextRange(newPath, moduleSpecifier);
        return updateNode(newPath);
      });
    }

    function visit(node) {
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "require" &&
        node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0])) {
        return update(node, node.arguments[0].text, (newPath) => context.factory.updateCallExpression(node, node.expression, node.typeArguments, [newPath]));
      }
      if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword &&
        node.arguments.length === 1 && ts.isStringLiteral(node.arguments[0])) {
        return update(node, node.arguments[0].text, (newPath) => context.factory.updateCallExpression(node, node.expression, node.typeArguments, [newPath]));
      }
      if (ts.isExternalModuleReference(node) && ts.isStringLiteral(node.expression)) {
        return updateModuleSpecifier(node, node.expression, (newPath) => context.factory.updateExternalModuleReference(node, newPath));
      }
      if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
        return updateModuleSpecifier(node, node.moduleSpecifier, (newPath) => context.factory.updateImportDeclaration(node, node.modifiers, node.importClause, newPath, node.attributes));
      }
      if (ts.isExportDeclaration(node) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
        return updateModuleSpecifier(node, node.moduleSpecifier, (newPath) => context.factory.updateExportDeclaration(node, node.modifiers, node.isTypeOnly, node.exportClause, newPath, node.attributes));
      }
      if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument) && ts.isStringLiteral(node.argument.literal)) {
        return update(node, node.argument.literal.text, (newPath) => context.factory.updateImportTypeNode(
          node,
          context.factory.updateLiteralTypeNode(node.argument, newPath),
          node.attributes,
          node.qualifier,
          node.typeArguments,
          node.isTypeOf,
        ));
      }
      return ts.visitEachChild(node, visit, context);
    }
  };
}

function isURL(value) {
  try {
    const parsed = new URL(value);
    return Boolean(parsed.host || parsed.hostname);
  } catch {
    return false;
  }
}

module.exports = { createDeclarationTransformers };
