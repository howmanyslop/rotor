package compile

import (
	"path/filepath"
	"strings"

	"rotor/internal/rojo"
	"rotor/tsgo/compiler"
	"rotor/tsgo/vfs/osvfs"
)

func populateCrossProjectImportPathMap(graph *SolutionGraph) map[string]string {
	result := map[string]string{}
	for _, project := range graph.Projects {
		_, program, _, err := newProjectProgram(filepath.Dir(project.ConfigPath), project.ConfigPath)
		if err != nil {
			continue
		}
		rootDirs := projectRootDirs(program)
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
	return result
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
