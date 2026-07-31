package transformer

import (
	"regexp"
	"strings"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// ---------------------------------------------------------------------------
// Import declarations — statements/transformImportDeclaration.ts,
// transformImportEqualsDeclaration.ts (digest §1)
// ---------------------------------------------------------------------------

// nonWordRegex ports util/cleanModuleName.ts `\W` (ASCII semantics match Go's).
var nonWordRegex = regexp.MustCompile(`\W`)

// cleanModuleName ports util/cleanModuleName.ts: non-word chars -> `_`.
func cleanModuleName(name string) string {
	return nonWordRegex.ReplaceAllString(name, "_")
}

// lazyImportExp ports Shared/classes/Lazy.ts as used by
// transformImportDeclaration: the TS.import call is only BUILT if some
// binding survives elision — a fully-elided import emits NOTHING, not even
// the require (digest §1.1). set() overrides the memo (the >1-uses temp id).
type lazyImportExp struct {
	factory func() luau.IndexableExpression
	value   luau.IndexableExpression
}

func (l *lazyImportExp) get() luau.IndexableExpression {
	if l.value == nil {
		l.value = l.factory()
	}
	return l.value
}

func (l *lazyImportExp) set(value luau.IndexableExpression) {
	l.value = value
}

// isReferencedAliasValue is the combined elision predicate applied to the
// default name and each named element (transformImportDeclaration.ts L16-21,
// L27-32, L70-71, L103-105): the emit resolver's referenced mark AND the
// skipped alias being a value (excluding type-only and const-enum targets).
// declaration is the node the resolver is asked about (ImportClause or
// ImportSpecifier); name is the bound identifier.
func isReferencedAliasValue(s *State, declaration *ast.Node, name *ast.Node) bool {
	symbol := getOriginalSymbolOfNode(s, name)
	if s.EmitResolver().IsReferencedAliasDeclarationUnsafe(declaration) && (symbol == nil || isSymbolOfValue(symbol)) {
		return true
	}
	if symbol != nil && symbol.Flags&ast.SymbolFlagsConstEnum != 0 {
		return false
	}
	return isImportedNameReferencedAsValue(s, name)
}

func isImportedNameReferencedAsValue(s *State, name *ast.Node) bool {
	alias := s.Checker.GetSymbolAtLocation(name)
	if alias == nil {
		return false
	}
	target := checker.SkipAlias(alias, s.Checker)
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsIdentifier(node) {
			if node == name || node.Text() != name.Text() || ast.IsPartOfTypeNode(node) || ast.IsPartOfTypeQuery(node) {
				return false
			}
			reference := s.Checker.GetSymbolAtLocation(node)
			if reference == alias || reference != nil && checker.SkipAlias(reference, s.Checker) == target {
				return true
			}
			if node.Parent != nil && ast.IsShorthandPropertyAssignment(node.Parent) {
				reference = s.Checker.GetShorthandAssignmentValueSymbol(node.Parent)
				if reference == alias || reference != nil && checker.SkipAlias(reference, s.Checker) == target {
					return true
				}
			}
			return true
		}
		return node.ForEachChild(visit)
	}
	return s.SourceFile.AsNode().ForEachChild(visit)
}

// countImportExpUses ports transformImportDeclaration.ts countImportExpUses
// (L13-37): how many bindings will actually read importExp. Namespace imports
// count unconditionally — `import * as ns` always binds if the clause
// survived the type-only check.
func countImportExpUses(s *State, importClause *ast.Node) int {
	clause := importClause.AsImportClause()
	uses := 0

	if clause.Name() != nil && isReferencedAliasValue(s, importClause, clause.Name()) {
		uses++
	}

	if clause.NamedBindings != nil {
		if ast.IsNamespaceImport(clause.NamedBindings) {
			uses++
		} else {
			for _, element := range clause.NamedBindings.AsNamedImports().Elements.Nodes {
				if isReferencedAliasValue(s, element, element.Name()) {
					uses++
				}
			}
		}
	}

	return uses
}

// transformImportDeclaration ports
// statements/transformImportDeclaration.ts (L39-131).
func transformImportDeclaration(s *State, node *ast.Node) *luau.List[luau.Statement] {
	decl := node.AsImportDeclaration()

	// no emit for type only
	importClause := decl.ImportClause
	if importClause != nil && importClause.IsTypeOnly() {
		return luau.NewList[luau.Statement]()
	}

	statements := luau.NewList[luau.Statement]()

	if !ast.IsStringLiteral(decl.ModuleSpecifier) {
		panic("transformer: transformImportDeclaration: module specifier is not a string literal") // upstream assert
	}
	importExp := &lazyImportExp{factory: func() luau.IndexableExpression {
		return createImportExpression(s, ast.GetSourceFileOfNode(node), decl.ModuleSpecifier)
	}}

	if importClause != nil {
		clause := importClause.AsImportClause()

		// Detect if we need to push to a new var or not (L53-65): with
		// uses > 1, hoist the call into `local <temp> = TS.import(...)`,
		// named after the cleaned last specifier segment.
		uses := countImportExpUses(s, importClause)
		if uses > 1 {
			moduleName := strings.Split(decl.ModuleSpecifier.Text(), "/")
			id := luau.TempID(cleanModuleName(moduleName[len(moduleName)-1]))
			statements.Push(luau.NewVariableDeclaration(id, importExp.get()))
			importExp.set(id)
		}

		// Default import logic (L68-88): `.default` when the target module
		// has a real `default` export, else the whole module table
		// (allowSyntheticDefaultImports interop over `export =` modules).
		if importClauseName := clause.Name(); importClauseName != nil && isReferencedAliasValue(s, importClause, importClauseName) {
			moduleFile := getSourceFileFromModuleSpecifier(s, decl.ModuleSpecifier)
			var moduleSymbol *ast.Symbol
			if moduleFile != nil {
				moduleSymbol = s.Checker.GetSymbolAtLocation(moduleFile.AsNode())
			}
			hasDefaultExport := false
			if moduleSymbol != nil {
				for _, export := range s.GetModuleExports(moduleSymbol) {
					if export.Name == "default" {
						hasDefaultExport = true
						break
					}
				}
			}
			if hasDefaultExport {
				statements.PushList(s.CaptureStatements(func() {
					transformVariable(s, importClauseName, luau.NewPropertyAccess(importExp.get(), "default"))
				}))
			} else {
				statements.PushList(s.CaptureStatements(func() {
					transformVariable(s, importClauseName, importExp.get())
				}))
			}
		}

		if namedBindings := clause.NamedBindings; namedBindings != nil {
			if ast.IsNamespaceImport(namedBindings) {
				// Namespace import logic (L93-99): whole module table, no
				// per-member binding, never elided.
				statements.PushList(s.CaptureStatements(func() {
					transformVariable(s, namedBindings.Name(), importExp.get())
				}))
			} else {
				// Named elements import logic (L102-118): alias
				// `import { greet as g }` reads property `greet`
				// (propertyName ?? name), binds local `g`.
				for _, element := range namedBindings.AsNamedImports().Elements.Nodes {
					if isReferencedAliasValue(s, element, element.Name()) {
						spec := element.AsImportSpecifier()
						propertyName := spec.PropertyName
						if propertyName == nil {
							propertyName = element.Name()
						}
						statements.PushList(s.CaptureStatements(func() {
							transformVariable(s, element.Name(), luau.NewPropertyAccess(importExp.get(), propertyName.Text()))
						}))
					}
				}
			}
		}
	}

	// Ensure we emit something (L122-128): `import "./x"` (no clause) emits a
	// bare TS.import CallStatement; under verbatimModuleSyntax a fully-elided
	// clause still emits the call (side effects preserved). The CallExpression
	// guard skips importExp already set() to a temp id (upstream comment: not
	// reachable in practice — uses==0 whenever statements is empty).
	if importClause == nil || (s.Program.Options().VerbatimModuleSyntax.IsTrue() && statements.IsEmpty()) {
		if expression, ok := importExp.get().(*luau.CallExpression); ok {
			statements.Push(luau.NewCallStatement(expression))
		}
	}

	return statements
}

// transformImportEqualsDeclaration ports
// statements/transformImportEqualsDeclaration.ts (L10-44).
func transformImportEqualsDeclaration(s *State, node *ast.Node) *luau.List[luau.Statement] {
	decl := node.AsImportEqualsDeclaration()
	moduleReference := decl.ModuleReference

	if ast.IsExternalModuleReference(moduleReference) {
		// `import x = require("./y")`: EAGER createImportExpression (no
		// Lazy); whole module table, no `.default` unwrapping ever.
		expression := moduleReference.AsExternalModuleReference().Expression
		if !ast.IsStringLiteral(expression) {
			panic("transformer: transformImportEqualsDeclaration: module reference is not a string literal") // upstream assert
		}
		importExp := createImportExpression(s, ast.GetSourceFileOfNode(node), expression)

		statements := luau.NewList[luau.Statement]()

		aliasSymbol := s.Checker.GetSymbolAtLocation(decl.Name())
		if aliasSymbol == nil {
			panic("transformer: transformImportEqualsDeclaration: no alias symbol") // upstream assert
		}
		if isSymbolOfValue(checker.SkipAlias(aliasSymbol, s.Checker)) || isImportedNameReferencedAsValue(s, decl.Name()) {
			statements.PushList(s.CaptureStatements(func() {
				transformVariable(s, decl.Name(), importExp)
			}))
		}

		// ensure we emit something
		if s.Program.Options().VerbatimModuleSyntax.IsTrue() && statements.IsEmpty() {
			if call, ok := importExp.(*luau.CallExpression); ok {
				statements.Push(luau.NewCallStatement(call))
			}
		}

		return statements
	}

	// Identifier | QualifiedName (issue #1895): plain aliasing of a namespace
	// path, no import machinery.
	return s.CaptureStatements(func() {
		transformVariable(s, decl.Name(), transformEntityName(s, moduleReference))
	})
}

// transformEntityName ports nodes/transformEntityName.ts.
func transformEntityName(s *State, node *ast.Node) luau.Expression {
	if ast.IsIdentifier(node) {
		ValidateIdentifier(s, node)
		return TransformIdentifier(s, node)
	}
	return transformQualifiedName(s, node)
}

func transformQualifiedName(s *State, node *ast.Node) *luau.PropertyAccessExpression {
	qualified := node.AsQualifiedName()
	return luau.NewPropertyAccess(convertToIndexableExpression(transformEntityName(s, qualified.Left)), qualified.Right.Text())
}

// ---------------------------------------------------------------------------
// Export-from declarations — statements/transformExportDeclaration.ts
// (digest §2.1)
// ---------------------------------------------------------------------------

// isExportSpecifierValue ports transformExportDeclaration.ts (L9-24): a
// specifier survives when it is not type-only AND either the emit resolver
// marked it referenced OR (fallback) the skipped alias is a value — the
// fallback keeps re-exports of values even when "unreferenced" locally.
func isExportSpecifierValue(s *State, element *ast.Node) bool {
	if element.AsExportSpecifier().IsTypeOnly {
		return false
	}

	if s.EmitResolver().IsReferencedAliasDeclarationUnsafe(element) {
		return true
	}

	aliasSymbol := s.Checker.GetSymbolAtLocation(element.Name())
	if aliasSymbol != nil && isSymbolOfValue(checker.SkipAlias(aliasSymbol, s.Checker)) {
		return true
	}

	return false
}

// countExportFromUses ports transformExportDeclaration.ts countImportExpUses
// (L26-38; renamed — Go has no per-file scopes and imports.go already defines
// the import-clause counter): NamedExports count their surviving value
// specifiers; `export * as ns` and bare `export *` always use importExp once.
func countExportFromUses(s *State, exportClause *ast.Node) int {
	if exportClause != nil && ast.IsNamedExports(exportClause) {
		uses := 0
		for _, element := range exportClause.AsNamedExports().Elements.Nodes {
			if isExportSpecifierValue(s, element) {
				uses++
			}
		}
		return uses
	}
	return 1
}

// transformExportFrom ports transformExportDeclaration.ts transformExportFrom
// (L40-123). All assignments land at STATEMENT POSITION (digest §2.4: the
// star/named-from interleave never reaches handleExports' exportPairs).
func transformExportFrom(s *State, node *ast.Node) *luau.List[luau.Statement] {
	decl := node.AsExportDeclaration()
	if decl.ModuleSpecifier == nil || !ast.IsStringLiteral(decl.ModuleSpecifier) {
		panic("transformer: transformExportFrom: module specifier is not a string literal") // upstream assert
	}

	statements := luau.NewList[luau.Statement]()
	var importExp luau.IndexableExpression

	exportClause := decl.ExportClause

	// Detect if we need to push to a new var or not (L49-62): uses == 1
	// inlines the TS.import at the single use site; uses > 1 hoists it into
	// `local <temp> = TS.import(...)` named after the cleaned last specifier
	// segment (same shape as imports).
	uses := countExportFromUses(s, exportClause)
	if uses == 1 {
		importExp = createImportExpression(s, ast.GetSourceFileOfNode(node), decl.ModuleSpecifier)
	} else if uses > 1 {
		moduleName := strings.Split(decl.ModuleSpecifier.Text(), "/")
		id := luau.TempID(cleanModuleName(moduleName[len(moduleName)-1]))
		statements.Push(luau.NewVariableDeclaration(
			id,
			createImportExpression(s, ast.GetSourceFileOfNode(node), decl.ModuleSpecifier),
		))
		importExp = id
	}

	if importExp == nil {
		return statements
	}

	moduleID := s.GetModuleIDFromNode(node)
	if exportClause != nil {
		if ast.IsNamedExports(exportClause) {
			// export { a, b, c } from "./module";
			// per surviving specifier: exports.<name> = <importExp>.<propertyName ?? name>
			for _, element := range exportClause.AsNamedExports().Elements.Nodes {
				if isExportSpecifierValue(s, element) {
					propertyName := element.AsExportSpecifier().PropertyName
					if propertyName == nil {
						propertyName = element.Name()
					}
					statements.Push(luau.NewAssignment(
						luau.NewPropertyAccess(moduleID, element.Name().Text()),
						"=",
						luau.NewPropertyAccess(importExp, propertyName.Text()),
					))
				}
			}
		} else {
			// export * as foo from "./module";
			statements.Push(luau.NewAssignment(
				luau.NewPropertyAccess(moduleID, exportClause.Name().Text()),
				"=",
				importExp,
			))
		}
	} else {
		// export * from "./module";
		keyID := luau.TempID("k")
		valueID := luau.TempID("v")
		body := luau.NewList[luau.Statement]()
		body.Push(luau.NewAssignment(
			luau.NewComputedIndex(moduleID, keyID),
			"=",
			valueID,
		))
		statements.Push(luau.NewFor(
			luau.NewList[luau.AnyIdentifier](keyID, valueID),
			// importExp may be `nil` in .d.ts files, so default to `{}`
			// boolean `or` is safe, because importExp can only be a table if not `nil`
			luau.NewBinary(importExp, "or", luau.NewMap(luau.NewList[*luau.MapField]())),
			body,
		))
	}

	s.HasExportFrom = true

	return statements
}

// transformExportDeclaration ports
// statements/transformExportDeclaration.ts (L125-133): type-only and plain
// `export { x }` (no module specifier) emit nothing at the statement —
// handleExports collects plain exports via the exports alias map; export-from
// goes through transformExportFrom.
func transformExportDeclaration(s *State, node *ast.Node) *luau.List[luau.Statement] {
	decl := node.AsExportDeclaration()
	if decl.IsTypeOnly {
		return luau.NewList[luau.Statement]()
	}
	if decl.ModuleSpecifier != nil {
		return transformExportFrom(s, node)
	}
	return luau.NewList[luau.Statement]()
}

// ---------------------------------------------------------------------------
// Export assignment — statements/transformExportAssignment.ts (digest §2.2)
// ---------------------------------------------------------------------------

// transformExportEquals ports transformExportAssignment.ts (L10-27): sets
// HasExportEquals (handleExports then skips the export collection loop and
// only appends the conditional `return exports`). When the `export =` is the
// FINAL statement of the file the value returns directly; otherwise it lands
// in `local exports = <expr>` and handleExports appends `return exports`.
func transformExportEquals(s *State, node *ast.Node) *luau.List[luau.Statement] {
	s.HasExportEquals = true

	sourceFile := ast.GetSourceFileOfNode(node)
	stmts := sourceFile.Statements.Nodes
	finalStatement := stmts[len(stmts)-1]
	result := luau.NewList[luau.Statement]()
	if finalStatement == node {
		result.Push(luau.NewReturn(TransformExpression(s, node.AsExportAssignment().Expression)))
	} else {
		result.Push(luau.NewVariableDeclaration(
			s.GetModuleIDFromNode(node),
			TransformExpression(s, node.AsExportAssignment().Expression),
		))
	}
	return result
}

// transformExportDefault ports transformExportAssignment.ts (L29-43):
// `export default <expr>` binds `local default = <expr>` (literal identifier
// `default`); getExportPair collects it into the exports shape like any other
// export. Named function/class default exports never reach here — they are
// FunctionDeclaration/ClassDeclaration with the ExportDefault modifier.
func transformExportDefault(s *State, node *ast.Node) *luau.List[luau.Statement] {
	statements := luau.NewList[luau.Statement]()

	expression, prereqs := s.Capture(func() luau.Expression {
		return TransformExpression(s, node.AsExportAssignment().Expression)
	})
	statements.PushList(prereqs)
	statements.Push(luau.NewVariableDeclaration(luau.ID("default"), expression))

	return statements
}

// transformExportAssignment ports
// statements/transformExportAssignment.ts (L45-60).
func transformExportAssignment(s *State, node *ast.Node) *luau.List[luau.Statement] {
	expression := node.AsExportAssignment().Expression

	symbol := s.Checker.GetSymbolAtLocation(expression)
	if symbol != nil && IsSymbolMutable(s, symbol) {
		s.Diags.Add(DiagNoExportAssignmentLet(node))
	}

	// type-only `export =` emits nothing
	if symbol != nil && !isSymbolOfValue(checker.SkipAlias(symbol, s.Checker)) {
		return luau.NewList[luau.Statement]()
	}

	if node.AsExportAssignment().IsExportEquals {
		return transformExportEquals(s, node)
	}
	return transformExportDefault(s, node)
}
