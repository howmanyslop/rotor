package compile

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
)

type packageExportKind uint8

const (
	packageExportInvalid packageExportKind = iota
	packageExportNull
	packageExportString
	packageExportObject
	packageExportArray
)

type packageExportValue struct {
	kind   packageExportKind
	string string
	object []packageExportEntry
	array  []packageExportValue
}

type packageExportEntry struct {
	key   string
	value packageExportValue
}

type resolvedExportTargets struct {
	types         string
	hasTypes      bool
	runtime       string
	hasRuntime    bool
	customTargets []string
}

func hasPackageExports(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func exportMappings(pkgDir string, pkg packageManifest, customConditions []string) []packageExportMapping {
	if !hasPackageExports(pkg.Exports) {
		return nil
	}
	exports, err := parsePackageExport(pkg.Exports)
	if err != nil {
		return nil
	}
	customs := make(map[string]struct{}, len(customConditions))
	for _, condition := range customConditions {
		customs[condition] = struct{}{}
	}

	switch exports.kind {
	case packageExportString, packageExportArray:
		return resolveExportSubpath(pkgDir, pkg, ".", exports, customs)
	case packageExportObject:
		isSubpathMap := len(exports.object) > 0
		for _, entry := range exports.object {
			if !strings.HasPrefix(entry.key, ".") {
				isSubpathMap = false
				break
			}
		}
		if !isSubpathMap {
			return resolveExportSubpath(pkgDir, pkg, ".", exports, customs)
		}
		mappings := []packageExportMapping{}
		for _, entry := range exports.object {
			if !strings.Contains(entry.key, "*") {
				mappings = append(mappings, resolveExportSubpath(pkgDir, pkg, entry.key, entry.value, customs)...)
			}
		}
		return mappings
	default:
		return nil
	}
}

func resolveExportSubpath(pkgDir string, pkg packageManifest, subpath string, value packageExportValue, customs map[string]struct{}) []packageExportMapping {
	if value.kind == packageExportNull {
		return nil
	}
	targets := resolveExportTargets(value, customs)
	if !targets.hasRuntime {
		return nil
	}
	if !targets.hasTypes {
		if subpath == "." {
			targets.types = pkg.Types
			if targets.types == "" {
				targets.types = pkg.Typings
			}
			targets.hasTypes = targets.types != ""
		}
		if !targets.hasTypes {
			targets.types = adjacentDTS(targets.runtime)
		}
	}

	runtimePath := resolveAgainst(pkgDir, targets.runtime)
	mappings := []packageExportMapping{{
		typesPath:   resolveAgainst(pkgDir, targets.types),
		runtimePath: runtimePath,
		subpath:     subpath,
	}}
	for _, customTarget := range targets.customTargets {
		customPath := resolveAgainst(pkgDir, customTarget)
		if customPath != mappings[0].typesPath {
			mappings = append(mappings, packageExportMapping{
				typesPath:   customPath,
				runtimePath: runtimePath,
				subpath:     subpath,
				custom:      true,
			})
		}
	}
	return mappings
}

func resolveExportTargets(value packageExportValue, customs map[string]struct{}) resolvedExportTargets {
	switch value.kind {
	case packageExportString:
		return resolvedExportTargets{runtime: value.string, hasRuntime: true, customTargets: []string{}}
	case packageExportObject:
		return resolveExportConditionObject(value.object, customs)
	case packageExportArray:
		result := resolvedExportTargets{customTargets: []string{}}
		for _, entry := range value.array {
			switch entry.kind {
			case packageExportNull:
				continue
			case packageExportString:
				result.runtime = entry.string
				result.hasRuntime = true
				return result
			case packageExportObject:
				mergeExportTargets(&result, resolveExportConditionObject(entry.object, customs))
				if result.hasRuntime {
					return result
				}
			}
		}
		return result
	default:
		return resolvedExportTargets{customTargets: []string{}}
	}
}

func resolveExportConditionObject(entries []packageExportEntry, customs map[string]struct{}) resolvedExportTargets {
	result := resolvedExportTargets{customTargets: []string{}}
	for _, entry := range entries {
		switch {
		case entry.key == "types" && entry.value.kind == packageExportString && !result.hasTypes:
			result.types, result.hasTypes = entry.value.string, true
		case isCustomCondition(entry.key, customs) && entry.value.kind == packageExportString:
			result.customTargets = append(result.customTargets, entry.value.string)
		case isRuntimeCondition(entry.key):
			if entry.value.kind == packageExportString && !result.hasRuntime {
				result.runtime, result.hasRuntime = entry.value.string, true
			} else if entry.value.kind == packageExportObject {
				mergeExportTargets(&result, resolveExportConditionObject(entry.value.object, customs))
			}
		}
	}
	return result
}

func mergeExportTargets(result *resolvedExportTargets, inner resolvedExportTargets) {
	if !result.hasTypes && inner.hasTypes {
		result.types, result.hasTypes = inner.types, true
	}
	if !result.hasRuntime && inner.hasRuntime {
		result.runtime, result.hasRuntime = inner.runtime, true
	}
	result.customTargets = append(result.customTargets, inner.customTargets...)
}

func isCustomCondition(condition string, customs map[string]struct{}) bool {
	_, ok := customs[condition]
	return ok
}

func isRuntimeCondition(condition string) bool {
	switch condition {
	case "node", "require", "import", "default":
		return true
	default:
		return false
	}
}

func adjacentDTS(filePath string) string {
	return strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".d.ts"
}

func parsePackageExport(raw json.RawMessage) (packageExportValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	value, err := decodePackageExport(decoder)
	if err != nil {
		return packageExportValue{}, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return packageExportValue{}, err
	}
	return value, nil
}

func decodePackageExport(decoder *json.Decoder) (packageExportValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return packageExportValue{}, err
	}
	switch value := token.(type) {
	case nil:
		return packageExportValue{kind: packageExportNull}, nil
	case string:
		return packageExportValue{kind: packageExportString, string: value}, nil
	case json.Delim:
		switch value {
		case '{':
			entries := []packageExportEntry{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return packageExportValue{}, err
				}
				entryValue, err := decodePackageExport(decoder)
				if err != nil {
					return packageExportValue{}, err
				}
				entries = append(entries, packageExportEntry{key: keyToken.(string), value: entryValue})
			}
			if _, err := decoder.Token(); err != nil {
				return packageExportValue{}, err
			}
			return packageExportValue{kind: packageExportObject, object: entries}, nil
		case '[':
			entries := []packageExportValue{}
			for decoder.More() {
				entryValue, err := decodePackageExport(decoder)
				if err != nil {
					return packageExportValue{}, err
				}
				entries = append(entries, entryValue)
			}
			if _, err := decoder.Token(); err != nil {
				return packageExportValue{}, err
			}
			return packageExportValue{kind: packageExportArray, array: entries}, nil
		}
	}
	return packageExportValue{kind: packageExportInvalid}, nil
}
