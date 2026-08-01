package compile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"rotor/internal/rojo"
	"rotor/tsgo/vfs/osvfs"
)

type packageManifest struct {
	Main    string          `json:"main"`
	Types   string          `json:"types"`
	Typings string          `json:"typings"`
	Exports json.RawMessage `json:"exports"`
}

type packageExportMapping struct {
	typesPath   string
	runtimePath string
	subpath     string
	custom      bool
}

func resolvePackageExports(pkgDir string, customConditions []string) (typesPath string, runtimePath string, err error) {
	pkg, err := readPackageManifest(pkgDir)
	if err != nil {
		return "", "", err
	}
	for _, mapping := range exportMappings(pkgDir, pkg, customConditions) {
		if mapping.subpath == "." && !mapping.custom {
			return mapping.typesPath, mapping.runtimePath, nil
		}
	}
	if hasPackageExports(pkg.Exports) {
		return "", "", nil
	}
	typesPath, runtimePath = legacyPackagePaths(pkgDir, pkg)
	return typesPath, runtimePath, nil
}

func readPackageManifest(pkgDir string) (packageManifest, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return packageManifest{}, err
	}
	var pkg packageManifest
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageManifest{}, err
	}
	return pkg, nil
}

func packageMappings(pkgDir string, pkg packageManifest, customConditions []string) []packageExportMapping {
	mappings := exportMappings(pkgDir, pkg, customConditions)
	if len(mappings) == 0 {
		typesPath, runtimePath := legacyPackagePaths(pkgDir, pkg)
		if runtimePath == "" {
			return nil
		}
		return []packageExportMapping{{typesPath: typesPath, runtimePath: runtimePath, subpath: "."}}
	}

	legacyTypes := pkg.Types
	if legacyTypes == "" {
		legacyTypes = pkg.Typings
	}
	if legacyTypes == "" || pkg.Main == "" {
		return mappings
	}
	legacyTypes = resolveAgainst(pkgDir, legacyTypes)
	for _, mapping := range mappings {
		if mapping.typesPath == legacyTypes {
			return mappings
		}
	}
	return append(mappings, packageExportMapping{
		typesPath:   legacyTypes,
		runtimePath: resolveAgainst(pkgDir, pkg.Main),
		subpath:     ".",
		custom:      true,
	})
}

func legacyPackagePaths(pkgDir string, pkg packageManifest) (typesPath, runtimePath string) {
	typesPath = pkg.Types
	if typesPath == "" {
		typesPath = pkg.Typings
	}
	if typesPath == "" {
		typesPath = "index.d.ts"
	}
	if pkg.Main == "" {
		return resolveAgainst(pkgDir, typesPath), ""
	}
	return resolveAgainst(pkgDir, typesPath), resolveAgainst(pkgDir, pkg.Main)
}

func createNodeModulesPathMapping(typeRoots []string, useCaseSensitiveFileNames bool, customConditions []string) map[string]string {
	mapping := make(map[string]string)
	for _, typeRoot := range typeRoots {
		scopePath := filepath.FromSlash(typeRoot)
		entries, err := os.ReadDir(scopePath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			pkgDir := filepath.Join(scopePath, entry.Name())
			pkg, err := readPackageManifest(pkgDir)
			if err != nil {
				continue
			}
			virtualMappings := packageMappings(pkgDir, pkg, customConditions)
			addPackageMappings(mapping, pkgDir, virtualMappings, useCaseSensitiveFileNames)

			realPkgDir := packageRealPath(pkgDir)
			if rojo.CanonicalFileName(realPkgDir, useCaseSensitiveFileNames) == rojo.CanonicalFileName(pkgDir, useCaseSensitiveFileNames) {
				continue
			}
			realMappings := packageMappings(realPkgDir, pkg, customConditions)
			addRealPathAliases(mapping, pkgDir, realMappings, virtualMappings, useCaseSensitiveFileNames)
		}
	}
	return mapping
}

func addPackageMappings(mapping map[string]string, pkgDir string, mappings []packageExportMapping, useCaseSensitiveFileNames bool) {
	for _, entry := range mappings {
		key := rojo.CanonicalFileName(entry.typesPath, useCaseSensitiveFileNames)
		if entry.custom {
			if _, exists := mapping[key]; exists {
				continue
			}
		}
		mapping[key] = remapToRojoTree(pkgDir, entry.runtimePath)
	}
}

func addRealPathAliases(mapping map[string]string, virtualPkgDir string, realMappings, virtualMappings []packageExportMapping, useCaseSensitiveFileNames bool) {
	for index, realMapping := range realMappings {
		if index >= len(virtualMappings) {
			return
		}
		key := rojo.CanonicalFileName(realMapping.typesPath, useCaseSensitiveFileNames)
		if _, exists := mapping[key]; !exists {
			mapping[key] = remapToRojoTree(virtualPkgDir, virtualMappings[index].runtimePath)
		}
	}
}

func remapToRojoTree(pkgDir, runtimePath string) string {
	projectPath, _ := rojo.FindRojoConfigFilePath(pkgDir)
	if projectPath == "" {
		return runtimePath
	}
	_, tree, err := rojo.ParseProjectFile(projectPath)
	if err != nil || tree.Path == nil {
		return runtimePath
	}

	var project struct {
		Tree struct {
			Path json.RawMessage `json:"$path"`
		} `json:"tree"`
	}
	data, err := os.ReadFile(projectPath)
	if err != nil || json.Unmarshal(data, &project) != nil {
		return runtimePath
	}
	var rojoTreePath string
	if json.Unmarshal(project.Tree.Path, &rojoTreePath) != nil {
		return runtimePath
	}

	rojoDir := resolveAgainst(pkgDir, rojoTreePath)
	if rel, err := filepath.Rel(rojoDir, filepath.Clean(runtimePath)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return runtimePath
	}
	relRuntime, err := filepath.Rel(pkgDir, runtimePath)
	if err != nil {
		return runtimePath
	}
	segments := strings.Split(relRuntime, string(filepath.Separator))
	segments[0] = filepath.FromSlash(rojoTreePath)
	return filepath.Join(pkgDir, filepath.Join(segments...))
}

func packageRealPath(pkgDir string) string {
	return filepath.FromSlash(osvfs.FS().Realpath(filepath.ToSlash(pkgDir)))
}
