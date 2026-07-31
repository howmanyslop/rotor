package transformer

import (
	"path/filepath"
	"slices"
	"strings"

	"rotor/internal/luau"
	"rotor/internal/rojo"
	"rotor/tsgo/ast"
	"rotor/tsgo/core"
	"rotor/tsgo/tspath"
	"rotor/tsgo/vfs/osvfs"
)

// nodeModules ports Shared/constants.ts NODE_MODULES.
const nodeModules = "node_modules"

// getSourceFileFromModuleSpecifier ports
// util/getSourceFileFromModuleSpecifier.ts (L4-33): the program SourceFile a
// module specifier resolves to, or nil.
func getSourceFileFromModuleSpecifier(s *State, moduleSpecifier *ast.Node) *ast.SourceFile {
	symbol := s.Checker.GetSymbolAtLocation(moduleSpecifier)
	if symbol == nil {
		symbol = s.Checker.ResolveExternalModuleName(moduleSpecifier)
	}
	if symbol != nil {
		declaration := symbol.ValueDeclaration

		if declaration != nil && ast.IsModuleDeclaration(declaration) && ast.IsStringLiteralLike(declaration.Name()) {
			// Ambient module declaration: chase the REAL file through the
			// program's resolution cache.
			sourceFile := ast.GetSourceFileOfNode(moduleSpecifier)
			mode := s.Program.GetModeForUsageLocation(sourceFile, declaration.Name())
			resolvedModule := s.Program.GetResolvedModule(sourceFile, declaration.Name().Text(), mode)
			if resolvedModule.IsResolved() {
				return s.Program.GetSourceFile(resolvedModule.ResolvedFileName)
			}
		}

		if declaration != nil && ast.IsSourceFile(declaration) {
			return declaration.AsSourceFile()
		}
	}

	// Fallback for $getModuleTree when the module is not referenced by any
	// regular import (upstream L25-32): a raw resolveModuleName against the
	// program options. Upstream passes no resolution mode (undefined); tsgo's
	// equivalent is ResolutionModeNone (resolve per the compiler options).
	if ast.IsStringLiteralLike(moduleSpecifier) && s.Program != nil {
		sourceFile := ast.GetSourceFileOfNode(moduleSpecifier)
		result := s.Program.ResolveModuleName(moduleSpecifier.Text(), sourceFile.FileName(), core.ResolutionModeNone)
		if result != nil && result.IsResolved() {
			return s.Program.GetSourceFile(result.ResolvedFileName)
		}
	}

	return nil
}

// isInsideNodeModules reimplements strada's ts.isInsideNodeModules (no public
// tsgo ast helper, digest §9.2): any path component equals "node_modules".
func isInsideNodeModules(filePath string) bool {
	for _, part := range strings.FieldsFunc(filepath.ToSlash(filePath), func(r rune) bool { return r == '/' }) {
		if part == nodeModules {
			return true
		}
	}
	return false
}

// relToProject renders a path relative to the project dir for noRojoData
// diagnostics (upstream path.relative(state.data.projectPath, ...)).
func relToProject(s *State, target string) string {
	rel, err := filepath.Rel(s.Rojo.ProjectPath, target)
	if err != nil {
		return target
	}
	return rel
}

// getAbsoluteImport ports createImportExpression.ts getAbsoluteImport
// (L15-24): [game:GetService("<p0>"), "p1", "p2", ...].
func getAbsoluteImport(moduleRbxPath rojo.RbxPath) []luau.Expression {
	serviceName := moduleRbxPath[0] // upstream assert(serviceName)
	pathExpressions := []luau.Expression{createGetService(serviceName)}
	for _, part := range moduleRbxPath[1:] {
		pathExpressions = append(pathExpressions, luau.Str(part))
	}
	return pathExpressions
}

// getRelativeImport ports createImportExpression.ts getRelativeImport
// (L26-47): leading RbxPathParent segments fold into ONE
// `script.Parent.Parent...` root expression; the remaining names follow as
// string arguments.
func getRelativeImport(sourceRbxPath, moduleRbxPath rojo.RbxPath) []luau.Expression {
	relativePath := rojo.Relative(sourceRbxPath, moduleRbxPath)

	var parents []string
	i := 0
	for i < len(relativePath) && relativePath[i].Parent {
		parents = append(parents, parentField)
		i++
	}

	pathExpressions := []luau.Expression{propertyAccessExpressionChain(luau.GlobalID("script"), parents)}
	for ; i < len(relativePath); i++ {
		// upstream assert(typeof pathPart === "string"): Relative never
		// yields a Parent after a name segment.
		pathExpressions = append(pathExpressions, luau.Str(relativePath[i].Name))
	}
	return pathExpressions
}

// validateModule ports createImportExpression.ts validateModule (L49-59): the
// scope's node_modules directory must path-equal one of the configured
// typeRoots.
func validateModule(s *State, scope string) bool {
	scopedModules := filepath.Clean(filepath.Join(s.Rojo.NodeModulesPath, scope))
	for _, typeRoot := range s.Rojo.TypeRoots {
		if scopedModules == filepath.Clean(filepath.FromSlash(typeRoot)) {
			return true
		}
	}
	return false
}

// findRelativeRbxPath ports createImportExpression.ts findRelativeRbxPath
// (L61-68): first pkg resolver that covers the path wins.
func findRelativeRbxPath(moduleOutPath string, pkgRojoResolvers []*rojo.RojoResolver) (rojo.RbxPath, bool) {
	for _, pkgRojoResolver := range pkgRojoResolvers {
		if relativeRbxPath, ok := pkgRojoResolver.GetRbxPathFromFilePath(moduleOutPath); ok {
			return relativeRbxPath, true
		}
	}
	return nil, false
}

func getPathsWithScope(resolver *rojo.RojoResolver, moduleScope string) []string {
	results := make(map[string]struct{})
	for _, partition := range resolver.GetPartitions() {
		normalized := filepath.Clean(partition.FsPath)
		if strings.HasSuffix(normalized, moduleScope) && !strings.Contains(normalized, nodeModules) {
			results[normalized] = struct{}{}
		}
	}
	paths := make([]string, 0, len(results))
	for path := range results {
		paths = append(paths, path)
	}
	return paths
}

func resolveSymlinkedModulePath(nodeModulesPath, moduleOutPath string, useCaseSensitiveFileNames bool) (string, bool) {
	rel, err := filepath.Rel(nodeModulesPath, moduleOutPath)
	if err != nil {
		return "", false
	}
	segments := strings.Split(rel, string(filepath.Separator))
	if len(segments) == 0 {
		return "", false
	}
	packageSegments := 1
	if strings.HasPrefix(segments[0], "@") {
		packageSegments = 2
	}
	if len(segments) < packageSegments {
		return "", false
	}
	packagePath := filepath.Join(nodeModulesPath, filepath.Join(segments[:packageSegments]...))
	realPackagePath := filepath.FromSlash(osvfs.FS().Realpath(filepath.ToSlash(packagePath)))
	if rojo.CanonicalFileName(realPackagePath, useCaseSensitiveFileNames) == rojo.CanonicalFileName(packagePath, useCaseSensitiveFileNames) {
		return "", false
	}
	return filepath.Join(realPackagePath, filepath.Join(segments[packageSegments:]...)), true
}

// getNodeModulesImportParts ports createImportExpression.ts
// getNodeModulesImportParts (L70-134).
func getNodeModulesImportParts(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node, moduleOutPath string) []luau.Expression {
	rel, err := filepath.Rel(s.Rojo.NodeModulesPath, moduleOutPath)
	if err != nil || rel == "" {
		panic("transformer: getNodeModulesImportParts: no module scope") // upstream assert(moduleScope)
	}
	moduleScope := strings.Split(rel, string(filepath.Separator))[0]

	if !strings.HasPrefix(moduleScope, "@") {
		s.Diags.Add(DiagNoUnscopedModule(moduleSpecifier))
		return []luau.Expression{luau.NewNone()}
	}

	if !validateModule(s, moduleScope) {
		s.Diags.Add(DiagNoInvalidModule(moduleSpecifier))
		return []luau.Expression{luau.NewNone()}
	}

	if s.ProjectType == ProjectTypePackage {
		relativeRbxPath, ok := findRelativeRbxPath(moduleOutPath, s.Rojo.PkgRojoResolvers)
		if !ok {
			s.Diags.Add(DiagNoRojoData(moduleSpecifier, relToProject(s, moduleOutPath), true))
			return []luau.Expression{luau.NewNone()}
		}

		moduleName := relativeRbxPath[0] // upstream assert(moduleName)

		return []luau.Expression{
			propertyAccessExpressionChain(
				luau.NewCall(s.RuntimeLib(moduleSpecifier.Parent, "getModule"), luau.NewList[luau.Expression](
					luau.GlobalID("script"),
					luau.Str(moduleScope),
					luau.Str(moduleName),
				)),
				relativeRbxPath[1:],
			),
		}
	}

	moduleRbxPath, ok := s.Rojo.Resolver.GetRbxPathFromFilePath(moduleOutPath)
	if !ok {
		if resolved, exists := resolveSymlinkedModulePath(s.Rojo.NodeModulesPath, moduleOutPath, s.Rojo.UseCaseSensitiveFileNames); exists {
			moduleRbxPath, ok = s.Rojo.Resolver.GetRbxPathFromFilePath(resolved)
		}
	}
	if !ok {
		packageRelativePath := strings.Split(rel, string(filepath.Separator))[1:]
		for _, scopePath := range getPathsWithScope(s.Rojo.Resolver, moduleScope) {
			candidate := filepath.Join(scopePath, filepath.Join(packageRelativePath...))
			if resolvedPath, exists := s.Rojo.Resolver.GetRbxPathFromFilePath(candidate); exists {
				moduleRbxPath = resolvedPath
				ok = true
				break
			}
		}
		if !ok {
			s.Diags.Add(DiagNoRojoData(moduleSpecifier, relToProject(s, moduleOutPath), true))
			return []luau.Expression{luau.NewNone()}
		}
	}

	indexOfScope := slices.Index(moduleRbxPath, moduleScope)
	if indexOfScope <= 0 || moduleRbxPath[indexOfScope-1] != nodeModules {
		s.Diags.Add(DiagNoPackageImportWithoutScope(moduleSpecifier, relToProject(s, moduleOutPath), moduleRbxPath))
		return []luau.Expression{luau.NewNone()}
	}

	return getProjectImportParts(s, sourceFile, moduleSpecifier, moduleOutPath, moduleRbxPath)
}

// getProjectImportParts ports createImportExpression.ts getProjectImportParts
// (L136-182).
func getProjectImportParts(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node, moduleOutPath string, moduleRbxPath rojo.RbxPath) []luau.Expression {
	moduleRbxType := s.Rojo.Resolver.GetRbxTypeFromFilePath(moduleOutPath)
	if moduleRbxType == rojo.RbxTypeScript || moduleRbxType == rojo.RbxTypeLocalScript {
		s.Diags.Add(DiagNoNonModuleImport(moduleSpecifier))
		return []luau.Expression{luau.NewNone()}
	}

	sourceOutPath := s.Rojo.PathTranslator.GetOutputPath(sourceFile.FileName())
	sourceRbxPath, ok := s.Rojo.Resolver.GetRbxPathFromFilePath(sourceOutPath)
	if !ok {
		s.Diags.Add(DiagNoRojoData(sourceFile.AsNode(), relToProject(s, sourceOutPath), false))
		return []luau.Expression{luau.NewNone()}
	}

	if s.ProjectType == ProjectTypeGame {
		// In the case of `import("")`, don't do the network type check as the
		// call may be guarded by runtime RunService checks (upstream comment).
		if !ast.IsImportCall(moduleSpecifier.Parent) &&
			s.Rojo.Resolver.GetNetworkType(moduleRbxPath) == rojo.NetworkTypeServer &&
			s.Rojo.Resolver.GetNetworkType(sourceRbxPath) != rojo.NetworkTypeServer {
			s.Diags.Add(DiagNoServerImport(moduleSpecifier))
			return []luau.Expression{luau.NewNone()}
		}

		fileRelation := s.Rojo.Resolver.GetFileRelation(sourceRbxPath, moduleRbxPath)
		switch fileRelation {
		case rojo.FileRelationOutToOut, rojo.FileRelationInToOut:
			return getAbsoluteImport(moduleRbxPath)
		case rojo.FileRelationInToIn:
			return getRelativeImport(sourceRbxPath, moduleRbxPath)
		default:
			s.Diags.Add(DiagNoIsolatedImport(moduleSpecifier))
			return []luau.Expression{luau.NewNone()}
		}
	}

	return getRelativeImport(sourceRbxPath, moduleRbxPath)
}

// getImportParts ports createImportExpression.ts getImportParts (L184-210):
// resolve the specifier to a file, translate to its output location, and pick
// the node_modules or project pipeline. Every error path adds its diagnostic
// and returns [none] (the compile bails on the diagnostic before rendering).
func getImportParts(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node) []luau.Expression {
	return getImportPartsImpl(s, sourceFile, moduleSpecifier, false)
}

// getModuleTreeImportParts is getImportParts with the rotor folder extension
// enabled — only $getModuleTree opts in; regular imports stay upstream-exact.
func getModuleTreeImportParts(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node) []luau.Expression {
	return getImportPartsImpl(s, sourceFile, moduleSpecifier, true)
}

func getImportPartsImpl(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node, allowFolder bool) []luau.Expression {
	// rotor guard (no upstream counterpart): every path below dereferences
	// s.Rojo; a State without SetRojoContext (transformer-level unit tests)
	// must get a diagnostic, not a nil-pointer panic.
	if s.Rojo == nil {
		s.Diags.Add(DiagRotorNoProjectContext(moduleSpecifier))
		return []luau.Expression{luau.NewNone()}
	}

	moduleFile := getSourceFileFromModuleSpecifier(s, moduleSpecifier)
	if moduleFile == nil {
		if allowFolder {
			if parts, ok := getFolderImportParts(s, sourceFile, moduleSpecifier); ok {
				return parts
			}
		}
		s.Diags.Add(DiagNoModuleSpecifierFile(moduleSpecifier))
		return []luau.Expression{luau.NewNone()}
	}

	// createImportExpression.ts:191: `const virtualPath =
	// state.guessVirtualPath(moduleFile.fileName) || moduleFile.fileName;` —
	// everything below (node_modules detection, the path mapping lookup, and
	// the output-path translation) operates on the VIRTUAL path.
	virtualPath := moduleFile.FileName()
	if guessed := s.GuessVirtualPath(virtualPath); guessed != "" {
		virtualPath = guessed
	}

	if isInsideNodeModules(virtualPath) {
		mappedPath := virtualPath
		key := rojo.CanonicalFileName(virtualPath, s.Rojo.UseCaseSensitiveFileNames)
		if mapped, ok := s.Rojo.NodeModulesPathMapping[key]; ok {
			mappedPath = mapped
		}
		moduleOutPath := s.Rojo.PathTranslator.GetImportPath(mappedPath, true /* isNodeModule */)
		return getNodeModulesImportParts(s, sourceFile, moduleSpecifier, moduleOutPath)
	}

	moduleOutPath, ok := s.Rojo.ImportPathMap[rojo.CanonicalFileName(virtualPath, s.Rojo.UseCaseSensitiveFileNames)]
	if !ok {
		moduleOutPath = s.Rojo.PathTranslator.GetImportPath(virtualPath, false)
	}
	moduleRbxPath, ok := s.Rojo.Resolver.GetRbxPathFromFilePath(moduleOutPath)
	if !ok {
		s.Diags.Add(DiagNoRojoData(moduleSpecifier, relToProject(s, moduleOutPath), false))
		return []luau.Expression{luau.NewNone()}
	}
	return getProjectImportParts(s, sourceFile, moduleSpecifier, moduleOutPath, moduleRbxPath)
}

// getFolderImportParts is a rotor EXTENSION (no upstream counterpart):
// $getModuleTree on a FOLDER. Upstream requires the specifier to resolve as
// a module — a folder needs an index.ts — and declared folder support out of
// scope. rotor resolves the specifier to a source DIRECTORY and emits its
// instance path instead. Candidate bases, in order:
//  1. relative specifiers — the importing file's directory (TS-faithful)
//  2. non-relative — tsconfig `paths` substitutions (covers the sanitizer's
//     baseUrl rewrite, so baseUrl-relative folder specifiers work)
//  3. non-relative — the project directory ("src/shared/systems" style)
//
// Only $getModuleTree reaches this (allowFolder); a regular import of a
// folder without index.ts still errors exactly like upstream. The first
// candidate that is an existing directory with Rojo data wins; the emitted
// parts go through getProjectImportParts, so Script/server-import/isolation
// guards apply exactly as they would for a file in that folder.
func getFolderImportParts(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node) ([]luau.Expression, bool) {
	if !ast.IsStringLiteralLike(moduleSpecifier) || s.Program == nil {
		return nil, false
	}
	spec := moduleSpecifier.Text()
	fs := s.Program.Host().FS()

	var candidates []string
	if spec == "." || spec == ".." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		candidates = append(candidates, tspath.ResolvePath(tspath.GetDirectoryPath(sourceFile.FileName()), spec))
	} else {
		options := s.Program.Options()
		pathsBase := options.GetPathsBasePath(s.Program.GetCurrentDirectory())
		for pattern, substitutions := range options.Paths.Entries() {
			starText, ok := matchPathsPattern(pattern, spec)
			if !ok {
				continue
			}
			for _, substitution := range substitutions {
				candidates = append(candidates, tspath.ResolvePath(pathsBase, strings.Replace(substitution, "*", starText, 1)))
			}
		}
		candidates = append(candidates, tspath.ResolvePath(s.Program.GetCurrentDirectory(), spec))
	}

	for _, dir := range candidates {
		if !fs.DirectoryExists(dir) {
			continue
		}
		moduleOutPath := s.Rojo.PathTranslator.GetOutputPath(dir)
		moduleRbxPath, ok := s.Rojo.Resolver.GetRbxPathFromFilePath(moduleOutPath)
		if !ok {
			continue
		}
		return getProjectImportParts(s, sourceFile, moduleSpecifier, moduleOutPath, moduleRbxPath), true
	}
	return nil, false
}

// matchPathsPattern matches a tsconfig `paths` pattern (at most one `*`)
// against a specifier, returning the text the star matched.
func matchPathsPattern(pattern, candidate string) (string, bool) {
	star := strings.Index(pattern, "*")
	if star < 0 {
		return "", pattern == candidate
	}
	prefix, suffix := pattern[:star], pattern[star+1:]
	if len(candidate) >= len(prefix)+len(suffix) && strings.HasPrefix(candidate, prefix) && strings.HasSuffix(candidate, suffix) {
		return candidate[len(prefix) : len(candidate)-len(suffix)], true
	}
	return "", false
}

// createImportExpression ports createImportExpression.ts (L212-220):
// `TS.import(script, <root expr>, "<name>"...)`. The state.TS call sets
// UsesRuntimeLib; WaitForChild semantics live inside RuntimeLib's TS.import
// at runtime — the emitted AST carries plain strings.
func createImportExpression(s *State, sourceFile *ast.SourceFile, moduleSpecifier *ast.Node) luau.IndexableExpression {
	parts := getImportParts(s, sourceFile, moduleSpecifier)
	args := luau.NewList[luau.Expression](luau.GlobalID("script"))
	for _, part := range parts {
		args.Push(part)
	}
	return luau.NewCall(s.RuntimeLib(moduleSpecifier.Parent, "import"), args)
}

// transformImportExpression ports
// nodes/expressions/transformImportExpression.ts (L8-30) — dynamic
// `import("x")`:
// `TS.Promise.new(function(resolve) resolve(TS.import(script, ...)) end)`.
func transformImportExpression(s *State, node *ast.Node) luau.Expression {
	var moduleSpecifier *ast.Node
	if arguments := node.Arguments(); len(arguments) > 0 {
		moduleSpecifier = arguments[0]
	}

	if moduleSpecifier == nil || !ast.IsStringLiteral(moduleSpecifier) {
		s.Diags.Add(DiagNoNonStringModuleSpecifier(node))
		return luau.NewNone()
	}

	importExpression := createImportExpression(s, ast.GetSourceFileOfNode(node), moduleSpecifier)
	resolveID := luau.ID("resolve")

	return luau.NewCall(
		luau.NewPropertyAccess(s.RuntimeLib(node, "Promise"), "new"),
		luau.NewList[luau.Expression](luau.NewFunctionExpression(
			luau.NewList[luau.AnyIdentifier](resolveID),
			false,
			luau.NewList[luau.Statement](luau.NewCallStatement(
				luau.NewCall(resolveID, luau.NewList[luau.Expression](importExpression)),
			)),
		)),
	)
}
