package compile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"rotor/internal/logservice"
)

// RbxtsOptions is the validated partial option set declared by a tsconfig.
// Pointer fields retain the distinction between an absent field and false.
type RbxtsOptions struct {
	AllowCommentDirectives *bool
	IncludePath            *string
	LogTruthyChanges       *bool
	Luau                   *bool
	NoInclude              *bool
	OptimizedLoops         *bool
	Rojo                   *string
	Type                   *string
}

// ReadRbxtsOptions ports getTsConfigProjectOptions: parent `extends` blocks
// merge first, the child overrides them, and paths resolve at the declaration.
func ReadRbxtsOptions(tsConfigPath string) (*RbxtsOptions, error) {
	return readRbxtsOptions(tsConfigPath, make(map[string]struct{}))
}

func readRbxtsOptions(tsConfigPath string, visited map[string]struct{}) (*RbxtsOptions, error) {
	normalized, err := filepath.Abs(tsConfigPath)
	if err != nil {
		return nil, fmt.Errorf("resolve tsconfig %q: %w", tsConfigPath, err)
	}
	normalized = filepath.Clean(normalized)
	if _, ok := visited[normalized]; ok {
		return nil, nil
	}
	visited[normalized] = struct{}{}

	data, err := os.ReadFile(normalized)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tsconfig %q: %w", normalized, err)
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(stripJSONC(string(data))), &config); err != nil {
		return nil, fmt.Errorf("Failed to parse tsconfig at %s: %w", normalized, err)
	}

	var inherited *RbxtsOptions
	if extends, ok := config["extends"].(string); ok {
		parent, err := resolveExtendsPath(filepath.Dir(normalized), extends)
		if err != nil {
			return nil, err
		}
		inherited, err = readRbxtsOptions(parent, visited)
		if err != nil {
			return nil, err
		}
	}

	raw, present := config["rbxts"]
	if !present {
		return inherited, nil
	}
	object, ok := raw.(map[string]any)
	if !ok || raw == nil {
		return nil, fmt.Errorf("tsconfig \"rbxts\" field at %s must be an object", normalized)
	}
	current, err := parseRbxtsOptions(object, normalized)
	if err != nil {
		return nil, err
	}
	return mergeRbxtsOptions(inherited, current), nil
}

func resolveExtendsPath(dir, extends string) (string, error) {
	if filepath.IsAbs(extends) || isRelativeExtendsPath(extends) {
		path := extends
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, filepath.FromSlash(path))
		}
		if resolved, ok := resolveConfigFile(path); ok {
			return resolved, nil
		}
		return path, nil
	}

	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		path := filepath.Join(current, "node_modules", filepath.FromSlash(extends))
		if resolved, ok := resolveConfigFile(path); ok {
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}

	return "", fmt.Errorf("Failed to resolve tsconfig extends %q from %s", extends, dir)
}

func isRelativeExtendsPath(path string) bool {
	return path == "." || path == ".." ||
		len(path) >= 2 && (path[:2] == "./" || path[:2] == `.\`) ||
		len(path) >= 3 && (path[:3] == "../" || path[:3] == `..\`)
}

func resolveConfigFile(path string) (string, bool) {
	for _, candidate := range []string{path, path + ".json"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return filepath.Clean(candidate), true
		}
	}

	packagePath := filepath.Join(path, "package.json")
	data, err := os.ReadFile(packagePath)
	if err != nil {
		return "", false
	}
	var pkg struct {
		Main string `json:"main"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Main == "" {
		return "", false
	}
	return resolveConfigFile(filepath.Join(path, filepath.FromSlash(pkg.Main)))
}

func parseRbxtsOptions(raw map[string]any, configPath string) (*RbxtsOptions, error) {
	for key := range raw {
		if !rbxtsOptionKeys[key] {
			logservice.Warn(fmt.Sprintf("Unknown \"rbxts\" option \"%s\" in %s (ignored)", key, configPath))
		}
	}

	result := &RbxtsOptions{}
	var err error
	if result.AllowCommentDirectives, err = rbxtsBoolean(raw, "allowCommentDirectives", configPath); err != nil {
		return nil, err
	}
	if result.LogTruthyChanges, err = rbxtsBoolean(raw, "logTruthyChanges", configPath); err != nil {
		return nil, err
	}
	if result.Luau, err = rbxtsBoolean(raw, "luau", configPath); err != nil {
		return nil, err
	}
	if result.NoInclude, err = rbxtsBoolean(raw, "noInclude", configPath); err != nil {
		return nil, err
	}
	if result.OptimizedLoops, err = rbxtsBoolean(raw, "optimizedLoops", configPath); err != nil {
		return nil, err
	}

	if result.IncludePath, err = rbxtsPath(raw, "includePath", configPath); err != nil {
		return nil, err
	}
	if result.Rojo, err = rbxtsPath(raw, "rojo", configPath); err != nil {
		return nil, err
	}
	if value, present := raw["type"]; present {
		text, ok := value.(string)
		if !ok {
			return nil, rbxtsTypeError(configPath, "type", `"game", "model" or "package"`, value)
		}
		if text != "game" && text != "model" && text != "package" {
			return nil, fmt.Errorf("Invalid \"rbxts\" config in %s: type must be \"game\", \"model\" or \"package\" (was %q)", configPath, text)
		}
		result.Type = &text
	}
	return result, nil
}

var rbxtsOptionKeys = map[string]bool{
	"allowCommentDirectives": true,
	"includePath":            true,
	"logTruthyChanges":       true,
	"luau":                   true,
	"noInclude":              true,
	"optimizedLoops":         true,
	"rojo":                   true,
	"type":                   true,
}

func rbxtsBoolean(raw map[string]any, key, configPath string) (*bool, error) {
	value, present := raw[key]
	if !present {
		return nil, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return nil, rbxtsTypeError(configPath, key, "a boolean", value)
	}
	return &boolean, nil
}

func rbxtsPath(raw map[string]any, key, configPath string) (*string, error) {
	value, present := raw[key]
	if !present {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, rbxtsTypeError(configPath, key, "a string", value)
	}
	if text != "" && !filepath.IsAbs(text) {
		text = filepath.Join(filepath.Dir(configPath), filepath.FromSlash(text))
	}
	return &text, nil
}

func rbxtsTypeError(configPath, key, expected string, value any) error {
	return fmt.Errorf("Invalid \"rbxts\" config in %s: %s must be %s (was %s)", configPath, key, expected, rbxtsValueDescription(value))
}

func rbxtsValueDescription(value any) string {
	switch value := value.(type) {
	case string:
		return fmt.Sprintf("%q", value)
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("a %T", value)
	}
}

func mergeRbxtsOptions(base, overlay *RbxtsOptions) *RbxtsOptions {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	result := *base
	if overlay.AllowCommentDirectives != nil {
		result.AllowCommentDirectives = overlay.AllowCommentDirectives
	}
	if overlay.IncludePath != nil {
		result.IncludePath = overlay.IncludePath
	}
	if overlay.LogTruthyChanges != nil {
		result.LogTruthyChanges = overlay.LogTruthyChanges
	}
	if overlay.Luau != nil {
		result.Luau = overlay.Luau
	}
	if overlay.NoInclude != nil {
		result.NoInclude = overlay.NoInclude
	}
	if overlay.OptimizedLoops != nil {
		result.OptimizedLoops = overlay.OptimizedLoops
	}
	if overlay.Rojo != nil {
		result.Rojo = overlay.Rojo
	}
	if overlay.Type != nil {
		result.Type = overlay.Type
	}
	return &result
}
