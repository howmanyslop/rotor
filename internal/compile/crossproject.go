package compile

import (
	"path/filepath"
	"strings"

	"rotor/internal/rojo"
	"rotor/tsgo/compiler"
	"rotor/tsgo/vfs/osvfs"
)

type solutionWriteMetadata struct {
	writeRoots           map[string][]string
	waitOnlyDependencies map[string][]string
}

func populateCrossProjectMetadata(graph *SolutionGraph) (map[string]string, solutionWriteMetadata) {
	result := map[string]string{}
	metadata := solutionWriteMetadata{
		writeRoots:           make(map[string][]string, len(graph.Projects)),
		waitOnlyDependencies: map[string][]string{},
	}
	for _, project := range graph.Projects {
		metadata.writeRoots[project.ConfigPath] = []string{}
		_, program, _, err := newProjectProgram(filepath.Dir(project.ConfigPath), project.ConfigPath)
		if err != nil {
			continue
		}
		rootDirs := projectRootDirs(program)
		if program.Options().OutDir != "" {
			pathTranslator := createPathTranslator(program, !project.Options.LuaExtension)
			metadata.writeRoots[project.ConfigPath] = appendCanonicalWriteRoot(
				metadata.writeRoots[project.ConfigPath],
				pathTranslator.OutDir,
			)
		}
		if project.Options.EmitIncludeFiles {
			if includePath, includeErr := resolveIncludePath(filepath.Dir(project.ConfigPath), project.Options.IncludePath); includeErr == nil {
				metadata.writeRoots[project.ConfigPath] = appendCanonicalWriteRoot(
					metadata.writeRoots[project.ConfigPath],
					includePath,
				)
			}
		}
		if len(rootDirs) == 0 || program.Options().OutDir == "" {
			continue
		}
		translator := rojo.NewPathTranslator(
			findAncestorDir(rootDirs),
			filepath.FromSlash(program.Options().OutDir),
			"",
			program.Options().Declaration.IsTrue(),
			!project.Options.LuaExtension,
		)
		for _, sourceFile := range program.SourceFiles() {
			fileName := sourceFile.FileName()
			if !isProjectTypeScriptFile(fileName) || !pathWithinAnyRoot(fileName, rootDirs) {
				continue
			}
			importPath := translator.GetImportPath(fileName, false)
			canonical := rojo.CanonicalFileName(fileName, osvfs.FS().UseCaseSensitiveFileNames())
			if _, exists := result[canonical]; exists {
				continue
			}
			result[canonical] = importPath
			if program.Options().Declaration.IsTrue() && !sourceFile.IsDeclarationFile {
				declarationPath := translator.GetOutputDeclarationPath(fileName)
				declarationCanonical := rojo.CanonicalFileName(declarationPath, osvfs.FS().UseCaseSensitiveFileNames())
				if _, exists := result[declarationCanonical]; !exists {
					result[declarationCanonical] = importPath
				}
			}
		}
	}
	metadata.waitOnlyDependencies = deriveWaitOnlyDependencies(graph.Projects, metadata.writeRoots)
	return result, metadata
}

func appendCanonicalWriteRoot(roots []string, path string) []string {
	canonical := canonicalWriteRoot(path)
	for _, root := range roots {
		if root == canonical {
			return roots
		}
	}
	return append(roots, canonical)
}

func canonicalWriteRoot(path string) string {
	path = filepath.Clean(filepath.FromSlash(path))
	if absolute, err := filepath.Abs(path); err == nil {
		path = filepath.Clean(absolute)
	}
	missing := []string{}
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		missing = append(missing, filepath.Base(path))
		path = parent
	}
}

func deriveWaitOnlyDependencies(projects []SolutionProject, writeRoots map[string][]string) map[string][]string {
	dependencies := map[string][]string{}
	for laterIndex := 1; laterIndex < len(projects); laterIndex++ {
		later := projects[laterIndex]
		for earlierIndex := 0; earlierIndex < laterIndex; earlierIndex++ {
			earlier := projects[earlierIndex]
			if writeRootsOverlap(writeRoots[earlier.ConfigPath], writeRoots[later.ConfigPath]) {
				dependencies[later.ConfigPath] = append(dependencies[later.ConfigPath], earlier.ConfigPath)
			}
		}
	}
	return dependencies
}

func writeRootsOverlap(left, right []string) bool {
	for _, leftRoot := range left {
		for _, rightRoot := range right {
			if writeRootContains(leftRoot, rightRoot) || writeRootContains(rightRoot, leftRoot) {
				return true
			}
		}
	}
	return false
}

func writeRootContains(root, path string) bool {
	if !osvfs.FS().UseCaseSensitiveFileNames() {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func projectRootDirs(program *compiler.Program) []string {
	options := program.Options()
	if options.RootDir != "" {
		return []string{options.RootDir}
	}
	return options.RootDirs
}

func isProjectTypeScriptFile(fileName string) bool {
	return strings.HasSuffix(fileName, ".ts") || strings.HasSuffix(fileName, ".tsx")
}

func pathWithinAnyRoot(fileName string, rootDirs []string) bool {
	for _, rootDir := range rootDirs {
		rel, err := filepath.Rel(filepath.FromSlash(rootDir), filepath.FromSlash(fileName))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
