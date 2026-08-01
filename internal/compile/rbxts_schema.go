package compile

const RbxtsTsConfigSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "https://raw.githubusercontent.com/roblox-ts/roblox-ts/main/schemas/rbxts-tsconfig.schema.json",
  "title": "roblox-ts tsconfig.json",
  "description": "JSON schema for roblox-ts-specific options in tsconfig.json. Reference via \"$schema\" at the top of your tsconfig.json to get IDE autocomplete and validation for both standard TypeScript options and the \"rbxts\" field.",
  "type": "object",
  "allOf": [{"$ref": "https://json.schemastore.org/tsconfig"}],
  "properties": {
    "rbxts": {
      "type": "object",
      "description": "roblox-ts compiler options. CLI flags override these at runtime.",
      "properties": {
        "allowCommentDirectives": {"type": "boolean"},
        "includePath": {"type": "string", "description": "Path to where the runtime library files should be stored. Resolved relative to this tsconfig.json."},
        "logTruthyChanges": {"type": "boolean"},
        "luau": {"type": "boolean", "description": "Emit files with .luau extension."},
        "noInclude": {"type": "boolean", "description": "Do not copy include files."},
        "optimizedLoops": {"type": "boolean"},
        "rojo": {"type": "string", "description": "Path to the Rojo configuration file. Resolved relative to this tsconfig.json. If omitted, roblox-ts auto-detects a *.project.json in the project root."},
        "type": {"enum": ["game", "model", "package"], "description": "Override project type."}
      },
      "additionalProperties": false
    }
  },
  "additionalProperties": true
}`
