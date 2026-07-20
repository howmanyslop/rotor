package config

// SchemaFileName is the JSON Schema file name. A single copy is committed at the
// rotor repository root and served from SchemaURL; projects no longer carry a
// per-project one.
const SchemaFileName = "rotor.schema.json"

// SchemaURL is the canonical, hosted location of the rotor.toml JSON Schema,
// served from the rotor repository via raw GitHub. rotor.toml's `#:schema`
// directive points here so taplo / Even Better TOML fetch the schema directly —
// there is no per-project rotor.schema.json to generate, refresh, or check in.
// `rotor schema` prints this same schema for publishing the committed file or
// keeping a local/offline copy.
const SchemaURL = "https://raw.githubusercontent.com/uproot/rotor/master/" + SchemaFileName

// SchemaDirective is the taplo schema comment prepended to rotor.toml so editors
// resolve the hosted schema.
const SchemaDirective = "#:schema " + SchemaURL

// Schema is the hand-maintained JSON Schema (draft-07) describing rotor.toml.
// It mirrors the Config structs in config.go 1:1 — the TOML analogue of the
// old TypeScript TypeDeclarations — including every property, the enums
// (creator.type, playability, socials.type, place versionType, assets.mode)
// and human descriptions. It is the single source of truth: the committed
// rotor.schema.json (served from SchemaURL) is generated from it by
// `rotor schema`, and a test asserts the two never drift.
//
// It must itself be valid JSON; a test asserts json.Unmarshal succeeds and the
// top-level assets + deploy properties exist.
const Schema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "https://raw.githubusercontent.com/uproot/rotor/master/rotor.schema.json",
  "title": "rotor.toml",
  "description": "Configuration for rotor asset sync and rotor deploy.",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "assets": {
      "type": "object",
      "description": "Configures rotor asset sync and the $asset macro.",
      "additionalProperties": false,
      "properties": {
        "mode": {
          "type": "string",
          "description": "How assets reach Luau: \"module\" generates assets.luau + assets.d.ts (default); \"macro\" uses the $asset transformer + rotor-asset.d.ts.",
          "enum": ["module", "macro"]
        },
        "base": {
          "type": "string",
          "description": "Project-relative directory prepended to non-relative $asset paths, e.g. \"assets/images\" lets you write $asset(\"ui/icon.png\") for assets/images/ui/icon.png. Does not affect paths globs, ./-relative references, or lockfile keys."
        },
        "paths": {
          "type": "array",
          "description": "Glob patterns of asset files relative to the project root.",
          "items": { "type": "string" }
        },
        "output": {
          "type": "object",
          "description": "Where generated asset modules are written (module mode only).",
          "additionalProperties": false,
          "properties": {
            "luau": {
              "type": "string",
              "description": "Output path of the generated Luau module, e.g. \"src/shared/assets.luau\"."
            },
            "types": {
              "type": "string",
              "description": "Output path of the matching .d.ts, e.g. \"src/shared/assets.d.ts\"."
            }
          }
        },
        "creator": {
          "type": "object",
          "description": "The Roblox creator that owns uploaded assets.",
          "additionalProperties": false,
          "properties": {
            "type": {
              "type": "string",
              "description": "Whether the owning creator is a user or a group.",
              "enum": ["user", "group"]
            },
            "id": {
              "type": "integer",
              "description": "The user or group id that owns uploaded assets."
            }
          }
        }
      }
    },
    "deploy": {
      "type": "object",
      "description": "Configures rotor deploy.",
      "additionalProperties": false,
      "properties": {
        "environments": {
          "type": "object",
          "description": "Named deploy targets (e.g. \"dev\", \"prod\").",
          "additionalProperties": {
            "type": "object",
            "description": "One named deploy target.",
            "additionalProperties": false,
            "properties": {
              "universeId": {
                "type": "integer",
                "description": "The universe id this environment publishes to."
              },
              "places": {
                "type": "object",
                "description": "Places to publish in this environment, keyed by a logical name.",
                "additionalProperties": {
                  "type": "object",
                  "description": "Publishes a built place file to a place id (and optionally manages place metadata).",
                  "additionalProperties": false,
                  "properties": {
                    "file": {
                      "type": "string",
                      "description": "Path to the built place file (.rbxl / .rbxlx)."
                    },
                    "placeId": {
                      "type": "integer",
                      "description": "The place id to publish to."
                    },
                    "name": {
                      "type": "string",
                      "description": "Place display name."
                    },
                    "description": {
                      "type": "string",
                      "description": "Place description."
                    },
                    "maxPlayers": {
                      "type": "integer",
                      "description": "Maximum players per server.",
                      "minimum": 0
                    },
                    "versionType": {
                      "type": "string",
                      "description": "Whether to save or publish the place version (default \"published\").",
                      "enum": ["saved", "published"]
                    }
                  }
                }
              },
              "experience": {
                "type": "object",
                "description": "Universe-level experience settings.",
                "additionalProperties": false,
                "properties": {
                  "name": {
                    "type": "string",
                    "description": "Experience display name."
                  },
                  "description": {
                    "type": "string",
                    "description": "Experience description."
                  },
                  "playability": {
                    "type": "string",
                    "description": "Who can play the experience.",
                    "enum": ["public", "private", "friends"]
                  },
                  "privateServers": {
                    "type": "object",
                    "description": "Paid private servers; omit price for free private servers.",
                    "additionalProperties": false,
                    "properties": {
                      "price": {
                        "type": "integer",
                        "description": "Private server price in Robux (0 = free).",
                        "minimum": 0
                      }
                    }
                  }
                }
              },
              "payments": {
                "type": "string",
                "description": "Payments configuration tag for the environment."
              },
              "badges": {
                "type": "object",
                "description": "Badges to create or update, keyed by a logical name.",
                "additionalProperties": {
                  "type": "object",
                  "description": "A badge to create or update.",
                  "additionalProperties": false,
                  "properties": {
                    "name": { "type": "string", "description": "Badge name." },
                    "description": { "type": "string", "description": "Badge description." },
                    "icon": { "type": "string", "description": "Badge icon image path." }
                  }
                }
              },
              "gamepasses": {
                "type": "object",
                "description": "Game passes to create or update, keyed by a logical name.",
                "additionalProperties": {
                  "type": "object",
                  "description": "A game pass to create or update; omit price to leave it off sale.",
                  "additionalProperties": false,
                  "properties": {
                    "name": { "type": "string", "description": "Game pass name." },
                    "description": { "type": "string", "description": "Game pass description." },
                    "price": {
                      "type": "integer",
                      "description": "Game pass price in Robux; omit to keep it off sale.",
                      "minimum": 0
                    },
                    "icon": { "type": "string", "description": "Game pass icon image path." }
                  }
                }
              },
              "icon": {
                "type": "string",
                "description": "Experience icon image path."
              },
              "thumbnails": {
                "type": "array",
                "description": "Ordered thumbnail image paths (max 10).",
                "items": { "type": "string" },
                "maxItems": 10
              },
              "products": {
                "type": "object",
                "description": "Developer products to create or update, keyed by a logical name.",
                "additionalProperties": {
                  "type": "object",
                  "description": "A developer product to create or update.",
                  "additionalProperties": false,
                  "properties": {
                    "name": { "type": "string", "description": "Product name." },
                    "description": { "type": "string", "description": "Product description." },
                    "price": {
                      "type": "integer",
                      "description": "Product price in Robux.",
                      "minimum": 0
                    }
                  }
                }
              },
              "socials": {
                "type": "object",
                "description": "Universe social links, keyed by a logical name.",
                "additionalProperties": {
                  "type": "object",
                  "description": "A universe social link.",
                  "additionalProperties": false,
                  "properties": {
                    "title": { "type": "string", "description": "Social link title." },
                    "url": { "type": "string", "description": "Social link URL." },
                    "type": {
                      "type": "string",
                      "description": "Social platform.",
                      "enum": ["facebook", "twitter", "youtube", "twitch", "discord", "github", "guilded"]
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
`
