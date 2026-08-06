const { createPluginNotFoundDiagnostic } = require("./diagnostics");

// getPluginConfigs reads the transformer list off the RESOLVED compiler
// options — the value `tsc --showConfig` reports — rather than walking the
// `extends` chain itself. `plugins` is an array-valued compiler option, so
// TypeScript's own resolution has already applied the rule that a child which
// declares `plugins` replaces the parent's list instead of adding to it. A
// child can therefore drop an inherited transform with `"plugins": []`.
//
// rbxtsc concatenates every config in the chain (Project/transformers/
// getPluginConfigs.ts), which leaves no way to opt out; rotor diverges here
// deliberately. See "Deliberate Divergence" in docs/sidecar-protocol.md.
function getPluginConfigs(options) {
  const plugins = options?.plugins;
  if (!Array.isArray(plugins)) {
    return [];
  }

  return plugins.filter((pluginConfig) => pluginConfig && typeof pluginConfig.transform === "string");
}

function getTransformerFromFactory(ts, factory, config, program) {
  const { after, afterDeclarations, type, ...manualConfig } = config;
  let transformer;

  switch (type) {
    case undefined:
    case "program":
      transformer = factory(program, manualConfig, { ts });
      break;
    case "checker":
      transformer = factory(program.getTypeChecker(), manualConfig);
      break;
    case "compilerOptions":
      transformer = factory(program.getCompilerOptions(), manualConfig);
      break;
    case "config":
      transformer = factory(manualConfig);
      break;
    case "raw":
      transformer = (context) => factory(context, program, manualConfig);
      break;
    default:
      return undefined;
  }

  if (typeof transformer === "function") {
    if (after) {
      return { after: transformer };
    }
    if (afterDeclarations) {
      return { afterDeclarations: transformer };
    }
    return { before: transformer };
  }

  return transformer;
}

function wrapWithShouldTransform(ts, transformer, shouldTransformSourceFile, program, config) {
  if (typeof shouldTransformSourceFile !== "function") {
    return transformer;
  }

  return (context) => {
    const transform = transformer(context);
    return (sourceFile) => {
      if (!ts.isSourceFile(sourceFile) || shouldTransformSourceFile(sourceFile, program, config)) {
        return transform(sourceFile);
      }
      return sourceFile;
    };
  };
}

function createTransformerList(ts, program, configs, baseDir) {
  const transforms = {
    before: [],
    after: [],
    afterDeclarations: [],
  };
  const diagnostics = [];

  for (const config of configs) {
    if (!config.transform) {
      continue;
    }

    try {
      const modulePath = require.resolve(config.transform, { paths: [baseDir] });
      const requiredModule = require(modulePath);
      const factoryModule = typeof requiredModule === "function"
        ? Object.assign({ default: requiredModule }, requiredModule)
        : requiredModule;
      const factory = factoryModule[config.import ?? "default"];

      if (typeof factory !== "function") {
        throw new Error("factory not a function");
      }

      const transformer = getTransformerFromFactory(ts, factory, config, program);
      if (!transformer) {
        continue;
      }
      const shouldTransformSourceFile = factoryModule.shouldTransformSourceFile;

      if (transformer.afterDeclarations) {
        transforms.afterDeclarations.push(
          wrapWithShouldTransform(ts, transformer.afterDeclarations, shouldTransformSourceFile, program, config),
        );
      }
      if (transformer.after) {
        transforms.after.push(wrapWithShouldTransform(ts, transformer.after, shouldTransformSourceFile, program, config));
      }
      if (transformer.before) {
        transforms.before.push(wrapWithShouldTransform(ts, transformer.before, shouldTransformSourceFile, program, config));
      }
    } catch (error) {
      diagnostics.push(createPluginNotFoundDiagnostic(config.transform, error));
    }
  }

  return { transforms, diagnostics };
}

function flattenIntoTransformers(transforms) {
  return [
    ...transforms.before,
    ...transforms.after,
  ];
}

function fixOrphanedParents(ts, root) {
  const walk = (parent) => {
    ts.forEachChild(parent, (child) => {
      if (!child.parent || !child.parent.getSourceFile?.()) {
        child.parent = parent;
      }
      walk(child);
    });
  };
  walk(root);
}

function wrapTransformersWithParentFix(ts, transformers) {
  const lastIndex = transformers.length - 1;
  return transformers.map((factory, index) => {
    if (index === lastIndex) {
      return factory;
    }
    return (context) => {
      const transform = factory(context);
      return (sourceFile) => {
        const result = transform(sourceFile);
        if (result !== sourceFile && ts.isSourceFile(result)) {
          fixOrphanedParents(ts, result);
        }
        return result;
      };
    };
  });
}

module.exports = {
  createTransformerList,
  flattenIntoTransformers,
  getPluginConfigs,
  wrapTransformersWithParentFix,
};
