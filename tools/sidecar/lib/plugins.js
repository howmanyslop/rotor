const path = require("node:path");
const { createPluginNotFoundDiagnostic } = require("./diagnostics");

function getPluginConfigs(ts, tsConfigPath) {
  const configFile = ts.readConfigFile(tsConfigPath, ts.sys.readFile);
  if (configFile.error) {
    throw new Error(ts.flattenDiagnosticMessageText(configFile.error.messageText, "\n"));
  }

  const pluginConfigs = [];
  const config = configFile.config ?? {};
  const plugins = config.compilerOptions?.plugins;
  if (Array.isArray(plugins)) {
    for (const pluginConfig of plugins) {
      if (pluginConfig && typeof pluginConfig.transform === "string") {
        pluginConfigs.push(pluginConfig);
      }
    }
  }

  if (typeof config.extends === "string") {
    const extendedPath = require.resolve(config.extends, {
      paths: [path.dirname(tsConfigPath)],
    });
    pluginConfigs.push(...getPluginConfigs(ts, extendedPath));
  }

  return pluginConfigs;
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
      const factoryModule = typeof requiredModule === "function" ? { default: requiredModule } : requiredModule;
      const factory = factoryModule[config.import ?? "default"];

      if (typeof factory !== "function") {
        throw new Error("factory not a function");
      }

      const transformer = getTransformerFromFactory(ts, factory, config, program);
      if (!transformer) {
        continue;
      }

      if (transformer.afterDeclarations) {
        transforms.afterDeclarations.push(transformer.afterDeclarations);
      }
      if (transformer.after) {
        transforms.after.push(transformer.after);
      }
      if (transformer.before) {
        transforms.before.push(transformer.before);
      }
    } catch (error) {
      diagnostics.push(createPluginNotFoundDiagnostic(config.transform, error));
    }
  }

  return { transforms, diagnostics };
}

function flattenIntoTransformers(transforms) {
  return [
    ...transforms.after,
    ...transforms.before,
    ...transforms.afterDeclarations,
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
