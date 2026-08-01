package forkparity

// ProjectFixture is a self-contained project used to compare Rotor with the archived fork.
type ProjectFixture struct {
	Name             string
	Description      string
	Category         string
	Files            map[string]string
	ExpectedExitCode int
	ExpectedStdout   string
	ExpectedStderr   string
}

// FixtureManifest records the archived fork and commands used to capture project fixtures.
type FixtureManifest struct {
	ZipDigest         string
	ExtractionCommand string
	Invocations       []FixtureInvocation
}

// FixtureInvocation describes one archived-fork CLI invocation for a fixture.
type FixtureInvocation struct {
	Name             string
	FixtureName      string
	WorkingDirectory string
	Arguments        []string
}

const (
	fixturePackageJSON = `{
  "name": "@forkparity/fixture",
  "version": "0.0.0",
  "private": true
}
`
	standardTSConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "module": "Preserve",
    "moduleDetection": "force",
    "moduleResolution": "Bundler",
    "noLib": true,
    "outDir": "out",
    "rootDir": "src",
    "strict": true,
    "target": "ESNext",
    "types": ["compiler-types"],
    "typeRoots": ["node_modules/@rbxts"]
  },
  "include": ["src"]
}
`
	compositeTSConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "composite": true,
    "declaration": true,
    "module": "Preserve",
    "moduleDetection": "force",
    "moduleResolution": "Bundler",
    "noLib": true,
    "outDir": "out",
    "rootDir": "src",
    "sourceMap": true,
    "strict": true,
    "target": "ESNext",
    "types": ["compiler-types"],
    "typeRoots": ["node_modules/@rbxts"]
  },
  "include": ["src"]
}
`
	basicSource = `export function greet(name: string): string {
  return "hello, " + name;
}
`
	buildRootTSConfig = `{
  "files": [],
  "references": [{ "path": "./shared" }, { "path": "./game" }]
}
`
	buildSharedTSConfig = `{
  "extends": "../tsconfig.base.json",
  "compilerOptions": { "rootDir": "src", "outDir": "out" },
  "include": ["src"]
}
`
	buildGameTSConfig = `{
  "extends": "../tsconfig.base.json",
  "compilerOptions": { "rootDir": "src", "outDir": "out" },
  "include": ["src"],
  "references": [{ "path": "../shared" }]
}
`
	buildBaseTSConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "composite": true,
    "declaration": true,
    "module": "Preserve",
    "moduleDetection": "force",
    "moduleResolution": "Bundler",
    "noLib": true,
    "sourceMap": true,
    "strict": true,
    "target": "ESNext",
    "types": ["compiler-types"],
    "typeRoots": ["node_modules/@rbxts"]
  }
}
`
	transformerDeclarationConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "composite": true,
    "declaration": true,
    "module": "Preserve",
    "moduleDetection": "force",
    "moduleResolution": "Bundler",
    "noLib": true,
    "outDir": "out",
    "plugins": [{ "transform": "./declaration-marker-transformer.js", "afterDeclarations": true }],
    "rootDir": "src",
    "strict": true,
    "target": "ESNext",
    "types": ["compiler-types"],
    "typeRoots": ["node_modules/@rbxts"]
  },
  "include": ["src"]
}
`
	transformerOrderingConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "module": "Preserve",
    "moduleDetection": "force",
    "moduleResolution": "Bundler",
    "noLib": true,
    "outDir": "out",
    "plugins": [
      { "transform": "./order-transformer.js", "label": "before" },
      { "transform": "./order-transformer.js", "label": "after", "after": true }
    ],
    "rootDir": "src",
    "strict": true,
    "target": "ESNext",
    "types": ["compiler-types"],
    "typeRoots": ["node_modules/@rbxts"]
  },
  "include": ["src"]
}
`
	transformerSourceMapConfig = `{
  "compilerOptions": {
    "allowSyntheticDefaultImports": true,
    "composite": true,
    "declaration": true,
    "module": "Preserve",
    "moduleDetection": "force",
    "moduleResolution": "Bundler",
    "noLib": true,
    "outDir": "out",
    "plugins": [{ "transform": "./insert-statement-transformer.js" }],
    "rootDir": "src",
    "sourceMap": true,
    "strict": true,
    "target": "ESNext",
    "types": ["compiler-types"],
    "typeRoots": ["node_modules/@rbxts"]
  },
  "include": ["src"]
}
`
	afterDeclarationsTransformer = `"use strict";
module.exports = function () {
  return function (context) {
    return function (sourceFile) {
      const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
        context.factory.createVariableDeclaration("__DECLARATION_MARKER__", undefined, undefined, context.factory.createStringLiteral("after-declarations"))
      ], 1));
      return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
    };
  };
};
`
	orderingTransformer = `"use strict";
module.exports = function (_, config) {
  return function (context) {
    return function (sourceFile) {
      const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
        context.factory.createVariableDeclaration("__ORDER_" + config.label.toUpperCase() + "__", undefined, undefined, context.factory.createStringLiteral(config.label))
      ], 1));
      return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
    };
  };
};
`
	sourceMapTransformer = `"use strict";
module.exports = function () {
  return function (context) {
    return function (sourceFile) {
      const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
        context.factory.createVariableDeclaration("__INJECTED__", undefined, undefined, context.factory.createStringLiteral("transformer-was-here"))
      ], 1));
      return context.factory.updateSourceFile(sourceFile, [marker].concat(sourceFile.statements));
    };
  };
};
`
)

// AllProjectFixtures returns the fork-authoritative project fixture catalog in stable order.
func AllProjectFixtures() []ProjectFixture {
	return []ProjectFixture{
		{
			Name: "build-basic", Description: "Single-project compilation through the build command", Category: "build-mode", ExpectedExitCode: 0,
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": standardTSConfig, "src/main.ts": basicSource},
		},
		{
			Name: "build-declarations", Description: "Build-mode declaration emission", Category: "build-mode", ExpectedExitCode: 0,
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": compositeTSConfig, "src/index.ts": basicSource},
		},
		{
			Name: "build-no-change", Description: "Composite rebuild that emits no changed files", Category: "build-mode", ExpectedExitCode: 0,
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": compositeTSConfig, "src/index.ts": basicSource, "src/helper.ts": "export const helper = 1;\n"},
		},
		{
			Name: "cross-project-dts", Description: "Referenced library imports through generated and hand-authored declarations", Category: "cross-project", ExpectedExitCode: 0,
			Files: map[string]string{
				"package.json": fixturePackageJSON, "tsconfig.json": `{"files":[],"references":[{"path":"./lib"},{"path":"./game"}]}`,
				"lib/package.json": fixturePackageJSON, "lib/tsconfig.json": `{"extends":"../tsconfig.base.json","compilerOptions":{"rootDir":"src","outDir":"out"},"include":["src"]}`, "tsconfig.base.json": buildBaseTSConfig, "lib/src/regular.ts": "export const regular = (): string => \"regular\";\n", "lib/src/handauth/index.d.ts": "export declare function handauth(): string;\n", "lib/src/handauth/init.luau": "return { handauth = function(): string return \"handauth\" end }\n",
				"game/package.json": fixturePackageJSON, "game/tsconfig.json": `{"extends":"../lib/tsconfig.json","compilerOptions":{"rootDir":"src","outDir":"out"},"include":["src"],"references":[{"path":"../lib"}],"rbxts":{"rojo":"./game.project.json","type":"game"}}`, "game/game.project.json": `{"name":"cross-project","tree":{"$className":"DataModel","ReplicatedStorage":{"include":{"$path":"include"},"shared":{"$path":"../lib/out"},"game":{"$path":"out"}}}}`, "game/include/RuntimeLib.lua": "return {}\n", "game/src/index.ts": "import { handauth } from \"../../lib/src/handauth\";\nimport { regular } from \"../../lib/src/regular\";\nexport const run = () => regular() + handauth();\n",
			},
		},
		{
			Name: "per-project-rojo", Description: "Referenced project uses its rbxts.rojo override instead of discovery", Category: "resolver", ExpectedExitCode: 0,
			Files: map[string]string{
				"package.json": fixturePackageJSON, "tsconfig.json": `{"files":[],"references":[{"path":"./custom-pkg"}]}`, "custom-pkg/package.json": fixturePackageJSON,
				"custom-pkg/tsconfig.json": `{"extends":"../tsconfig.base.json","compilerOptions":{"rootDir":"src","outDir":"out"},"include":["src"],"rbxts":{"rojo":"./custom.project.json"}}`, "tsconfig.base.json": buildBaseTSConfig,
				"custom-pkg/custom.project.json": `{"name":"custom-pkg","tree":{"$path":"out"}}`, "custom-pkg/default.project.json": `{"name":"wrong-project","tree":{"$path":"does-not-exist"}}`, "custom-pkg/src/index.ts": basicSource,
			},
		},
		{
			Name: "transformer-declarations", Description: "afterDeclarations plugins affect declarations without leaking into Luau", Category: "plugin", ExpectedExitCode: 0,
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": transformerDeclarationConfig, "declaration-marker-transformer.js": afterDeclarationsTransformer, "src/index.ts": basicSource},
		},
		{
			Name: "transformer-ordering", Description: "Before transformers run before after transformers", Category: "plugin", ExpectedExitCode: 0,
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": transformerOrderingConfig, "order-transformer.js": orderingTransformer, "src/index.ts": basicSource},
		},
		{
			Name: "transformer-sourcemap", Description: "Source maps retain original positions after synthetic transformer output", Category: "source-map", ExpectedExitCode: 0,
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": transformerSourceMapConfig, "insert-statement-transformer.js": sourceMapTransformer, "src/index.ts": "export function greet(name: string): string {\n  return \"hello, \" + name;\n}\n\nexport function add(left: number, right: number): number {\n  return left + right;\n}\n"},
		},
		{
			Name: "diagnostics", Description: "Type errors produce a non-zero exit code and TypeScript diagnostic", Category: "diagnostics", ExpectedExitCode: 1, ExpectedStdout: "error TS2322: Type 'string' is not assignable to type 'number'.",
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": standardTSConfig, "src/main.ts": "const answer: number = \"not-a-number\";\nexport { answer };\n"},
		},
		{
			Name: "duplicate-output", Description: "Index and init sources cannot emit to the same Luau output path", Category: "output", ExpectedExitCode: 1, ExpectedStderr: "duplicate output path detected",
			Files: map[string]string{"package.json": fixturePackageJSON, "tsconfig.json": standardTSConfig, "src/index.ts": "export const first = 1;\n", "src/init.ts": "export const second = 2;\n"},
		},
	}
}

// ProjectFixtureManifest returns the capture recipe for the archived fork fixtures.
func ProjectFixtureManifest() FixtureManifest {
	return FixtureManifest{
		ZipDigest:         committedZipDigest,
		ExtractionCommand: "FindZip(repoRoot) then VerifyAndExtract(zipPath)",
		Invocations: []FixtureInvocation{
			{Name: "build", FixtureName: "build-basic", WorkingDirectory: ".", Arguments: []string{"build", "--project", "."}},
			{Name: "build", FixtureName: "build-declarations", WorkingDirectory: ".", Arguments: []string{"build", "--project", ".", "--build"}},
			{Name: "initial-build", FixtureName: "build-no-change", WorkingDirectory: ".", Arguments: []string{"build", "--project", ".", "--build"}},
			{Name: "no-change-rebuild", FixtureName: "build-no-change", WorkingDirectory: ".", Arguments: []string{"build", "--project", ".", "--build"}},
			{Name: "build", FixtureName: "cross-project-dts", WorkingDirectory: ".", Arguments: []string{"build", "--project", ".", "--build"}},
			{Name: "build", FixtureName: "per-project-rojo", WorkingDirectory: ".", Arguments: []string{"build", "--project", ".", "--build"}},
			{Name: "build", FixtureName: "transformer-declarations", WorkingDirectory: ".", Arguments: []string{"build", "--project", "."}},
			{Name: "build", FixtureName: "transformer-ordering", WorkingDirectory: ".", Arguments: []string{"build", "--project", "."}},
			{Name: "build", FixtureName: "transformer-sourcemap", WorkingDirectory: ".", Arguments: []string{"build", "--project", "."}},
			{Name: "build", FixtureName: "diagnostics", WorkingDirectory: ".", Arguments: []string{"build", "--project", "."}},
			{Name: "build", FixtureName: "duplicate-output", WorkingDirectory: ".", Arguments: []string{"build", "--project", "."}},
		},
	}
}
