package transformer

import (
	"sort"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// ExportShape identifies which of the four per-file export emission shapes
// handleExports produces (transformSourceFile.ts:132-188).
type ExportShape int

const (
	// ExportShapeNone: no exports at all — nothing emitted here (a bare
	// `return nil` may still be appended for ModuleScripts).
	ExportShapeNone ExportShape = iota
	// ExportShapeExportEquals: `export = x` — transformExportAssignment
	// already created `local exports = <value>`; append `return exports`
	// unless the file's last TS statement is itself the `export =`.
	ExportShapeExportEquals
	// ExportShapeExportsTable: any `export ... from` or any mutable
	// (`export let`) export — unshift `local exports = {}` to the top of
	// the file, assign each immutable pair, then `return exports`.
	ExportShapeExportsTable
	// ExportShapeReturnMap: only immutable exports — a single
	// `return { key = id, ... }` at the bottom of the file.
	ExportShapeReturnMap
)

// ChooseExportShape is the pure shape-selection rule of handleExports.
// Precedence: export-equals wins outright (the export collection loop is
// skipped entirely, so immutable pairs never exist); otherwise any
// export-from or mutable export forces the exports-table shape for the WHOLE
// file (immutable exports join the table as assignments); otherwise immutable
// exports return a map literal; otherwise nothing.
func ChooseExportShape(hasExportEquals, hasExportFrom, hasMutableExports, hasImmutableExports bool) ExportShape {
	switch {
	case hasExportEquals:
		return ExportShapeExportEquals
	case hasExportFrom || hasMutableExports:
		return ExportShapeExportsTable
	case hasImmutableExports:
		return ExportShapeReturnMap
	default:
		return ExportShapeNone
	}
}

type exportPair struct {
	name   string
	id     luau.AnyIdentifier
	symbol *ast.Symbol
}

// handleExports ports transformSourceFile.ts handleExports (lines 94-189):
// adds export information to the end (and, for the exports-table shape, the
// top) of the statement tree.
func handleExports(s *State, sourceFile *ast.SourceFile, symbol *ast.Symbol, statements *luau.List[luau.Statement]) {
	ignoredExportSymbols := getIgnoredExportSymbols(s, sourceFile)

	hasMutableExports := false
	var exportPairs []exportPair
	if !s.HasExportEquals {
		for _, exportSymbol := range s.GetModuleExports(symbol) {
			if ignoredExportSymbols[exportSymbol] {
				continue
			}
			// ignore prototype exports
			if exportSymbol.Flags&ast.SymbolFlagsPrototype != 0 {
				continue
			}
			// export { default as x } from "./module";
			if isExportSymbolFromExportFrom(exportSymbol) {
				continue
			}
			originalSymbol := checker.SkipAlias(exportSymbol, s.Checker)
			// only export values
			if !isSymbolOfValue(originalSymbol) {
				continue
			}
			// export let — reads/writes already compile to exports.x in
			// transformIdentifier; presence forces the exports-table shape
			if IsSymbolMutable(s, originalSymbol) {
				hasMutableExports = true
				continue
			}
			// ignore exports in the form of `export declare const x: T;`
			if isExportSymbolOnlyFromDeclare(exportSymbol) {
				continue
			}
			name, id := getExportPair(s, exportSymbol)
			exportPairs = append(exportPairs, exportPair{name: name, id: id, symbol: exportSymbol})
		}
	}

	switch ChooseExportShape(s.HasExportEquals, s.HasExportFrom, hasMutableExports, len(exportPairs) > 0) {
	case ExportShapeExportEquals:
		// local exports variable is created in transformExportAssignment
		stmts := sourceFile.Statements.Nodes
		finalStatement := stmts[len(stmts)-1]
		if !(ast.IsExportAssignment(finalStatement) && finalStatement.AsExportAssignment().IsExportEquals) {
			statements.Push(luau.NewReturn(luau.GlobalID("exports")))
		}

	case ExportShapeExportsTable:
		// `local exports = {}` at the top of the statement list — this runs
		// before the header is prepended, so it lands right after the header
		// (and `local TS` line, when present) in the final output.
		statements.Unshift(luau.NewVariableDeclaration(
			luau.GlobalID("exports"),
			luau.NewMap(luau.NewList[*luau.MapField]()),
		))
		for _, pair := range exportPairs {
			assignment := luau.NewAssignment(
				luau.GlobalProperty("exports", pair.name),
				"=",
				pair.id,
			)
			if anchor := getExportSyntaxAnchor(pair.symbol, sourceFile); anchor != nil {
				s.setSourcePosition(assignment, s.getOriginalSourcePosition(anchor, false))
				s.setSourceEndPosition(assignment, s.getOriginalSourcePosition(anchor, true))
			}
			statements.Push(assignment)
		}
		statements.Push(luau.NewReturn(luau.GlobalID("exports")))

	case ExportShapeReturnMap:
		fields := luau.NewList[*luau.MapField]()
		for _, pair := range exportPairs {
			fields.Push(luau.NewMapField(luau.Str(pair.name), pair.id))
		}
		statements.Push(luau.NewReturn(luau.NewMap(fields)))
	}
}

// getExportPair ports transformSourceFile.ts getExportPair: returns
// [exportName, luauId]. `export { a as b }` specifiers -> ("b", <a>);
// otherwise (symbol.name, luau.id(name)) where a default-exported named
// function/class uses its declared name (`export default function foo` ->
// ("default", foo)).
func getExportPair(s *State, exportSymbol *ast.Symbol) (string, luau.AnyIdentifier) {
	var declaration *ast.Node
	if len(exportSymbol.Declarations) > 0 {
		declaration = exportSymbol.Declarations[0]
	}
	if declaration != nil && ast.IsExportSpecifier(declaration) {
		nameNode := declaration.PropertyName()
		if nameNode == nil {
			nameNode = declaration.Name()
		}
		return declaration.Name().Text(), TransformIdentifierDefined(s, nameNode)
	}
	name := exportSymbol.Name
	if name == "default" && declaration != nil &&
		(ast.IsFunctionDeclaration(declaration) || ast.IsClassDeclaration(declaration)) &&
		declaration.Name() != nil {
		name = declaration.Name().Text()
	}
	return exportSymbol.Name, luau.ID(name)
}

// isExportSymbolFromExportFrom ports the same-named upstream helper: true if
// any declaration is an export specifier whose ExportDeclaration has a module
// specifier (`export { x } from "..."` — already assigned where transformed).
func isExportSymbolFromExportFrom(exportSymbol *ast.Symbol) bool {
	for _, declaration := range exportSymbol.Declarations {
		if ast.IsExportSpecifier(declaration) {
			if exportDec := declaration.Parent.Parent; exportDec != nil &&
				ast.IsExportDeclaration(exportDec) &&
				exportDec.AsExportDeclaration().ModuleSpecifier != nil {
				return true
			}
		}
	}
	return false
}

// getIgnoredExportSymbols ports the upstream helper: symbols to skip —
// everything re-exported by `export * from "./m"` (the module's own exports)
// and the namespace id of `export * as ns from "./m"`.
func getIgnoredExportSymbols(s *State, sourceFile *ast.SourceFile) map[*ast.Symbol]bool {
	ignored := map[*ast.Symbol]bool{}
	for _, statement := range sourceFile.Statements.Nodes {
		if !ast.IsExportDeclaration(statement) {
			continue
		}
		exportDec := statement.AsExportDeclaration()
		if exportDec.ModuleSpecifier == nil {
			continue
		}
		if exportDec.ExportClause == nil {
			// export * from "./module";
			if moduleSymbol := getOriginalSymbolOfNode(s, exportDec.ModuleSpecifier); moduleSymbol != nil {
				for _, v := range s.GetModuleExports(moduleSymbol) {
					ignored[v] = true
				}
			}
		} else if ast.IsNamespaceExport(exportDec.ExportClause) {
			// export * as id from "./module";
			if idSymbol := s.Checker.GetSymbolAtLocation(exportDec.ExportClause.Name()); idSymbol != nil {
				ignored[idSymbol] = true
			}
		}
	}
	return ignored
}

// isExportSymbolOnlyFromDeclare ports the upstream helper: true iff EVERY
// declaration's ancestor statement has a `declare` modifier — so
// `export declare const x` is skipped, but `declare const x; export { x };`
// is not (mimics TypeScript behavior).
func isExportSymbolOnlyFromDeclare(exportSymbol *ast.Symbol) bool {
	if len(exportSymbol.Declarations) == 0 {
		return false
	}
	for _, declaration := range exportSymbol.Declarations {
		statement := ast.FindAncestor(declaration, ast.IsStatement)
		if statement == nil || !ast.HasSyntacticModifier(statement, ast.ModifierFlagsAmbient) {
			return false
		}
	}
	return true
}

// getOriginalSymbolOfNode ports util/getOriginalSymbolOfNode.ts.
func getOriginalSymbolOfNode(s *State, node *ast.Node) *ast.Symbol {
	symbol := s.Checker.GetSymbolAtLocation(node)
	if symbol != nil {
		return checker.SkipAlias(symbol, s.Checker)
	}
	return nil
}

// isSymbolOfValue ports util/isSymbolOfValue.ts: the symbol represents
// something with a runtime value (excludes types and const enums).
func isSymbolOfValue(symbol *ast.Symbol) bool {
	return symbol.Flags&ast.SymbolFlagsValue != 0 && symbol.Flags&ast.SymbolFlagsConstEnum == 0
}

// IsSymbolMutable ports util/isSymbolMutable.ts, cached in
// Multi.IsDefinedAsLetCache: true if the value declaration is a parameter or
// its enclosing VariableDeclarationList has NodeFlags.Let.
func IsSymbolMutable(s *State, idSymbol *ast.Symbol) bool {
	if cached, ok := s.Multi.IsDefinedAsLetCache[idSymbol]; ok {
		return cached
	}
	result := false
	if decl := idSymbol.ValueDeclaration; decl != nil {
		if decl.Kind == ast.KindParameter {
			result = true
		} else if varDecList := ast.FindAncestor(decl, ast.IsVariableDeclarationList); varDecList != nil {
			result = varDecList.Flags&ast.NodeFlagsLet != 0
		}
	}
	s.Multi.IsDefinedAsLetCache[idSymbol] = result
	return result
}

// GetModuleExports ports TransformState.getModuleExports with the
// multi-transform-state cache.
//
// DIVERGENCE: tsgo's Checker.GetExportsOfModule materializes a Go map
// (ast.SymbolTable) whose iteration order is nondeterministic, while
// upstream's JS Map preserves binder insertion order, which fixes the order
// of emitted export assignments/fields. rotor reconstructs insertion order:
// the binder binds a container's FunctionDeclaration statements before all
// other statements (binder.ts bindEachFunctionsFirst; same in tsgo
// binder.go:1776), so function symbols enter the exports table first — in
// source order — followed by everything else in source order. Verified
// against rbxtsc 3.0.0: `export const A = 6; export function z() {}` returns
// `{ z = z, A = A }`, function first. Sort key: (file, functions-first pass,
// offset); declaration-less symbols sort last by name.
func (s *State) GetModuleExports(moduleSymbol *ast.Symbol) []*ast.Symbol {
	if cached, ok := s.Multi.GetModuleExportsCache[moduleSymbol]; ok {
		return cached
	}
	exports := s.Checker.GetExportsOfModule(moduleSymbol)
	sort.SliceStable(exports, func(i, j int) bool {
		fi, gi, pi, oki := exportSortKey(exports[i])
		fj, gj, pj, okj := exportSortKey(exports[j])
		if oki != okj {
			return oki // symbols with declarations first
		}
		if !oki {
			return exports[i].Name < exports[j].Name
		}
		if fi != fj {
			return fi < fj
		}
		if gi != gj {
			return gi < gj // binder pass: function declarations first
		}
		if pi != pj {
			return pi < pj
		}
		// Final tiebreaker: keep the order deterministic even when two
		// symbols share a declaration position (map iteration order would
		// otherwise leak through).
		return exports[i].Name < exports[j].Name
	})
	s.Multi.GetModuleExportsCache[moduleSymbol] = exports
	return exports
}

// GetModuleExportsAliasMap ports TransformState.getModuleExportsAliasMap with
// the multi-transform-state cache: original (alias-skipped) export symbol ->
// the name it is exported under (`export { a as b }` -> "b").
func (s *State) GetModuleExportsAliasMap(moduleSymbol *ast.Symbol) map[*ast.Symbol]string {
	if cached, ok := s.Multi.GetModuleExportsAliasMapCache[moduleSymbol]; ok {
		return cached
	}
	aliasMap := map[*ast.Symbol]string{}
	for _, exportSymbol := range s.GetModuleExports(moduleSymbol) {
		originalSymbol := checker.SkipAlias(exportSymbol, s.Checker)
		var declaration *ast.Node
		if len(exportSymbol.Declarations) > 0 {
			declaration = exportSymbol.Declarations[0]
		}
		if declaration != nil && ast.IsExportSpecifier(declaration) {
			aliasMap[originalSymbol] = declaration.Name().Text()
		} else {
			aliasMap[originalSymbol] = exportSymbol.Name
		}
	}
	s.Multi.GetModuleExportsAliasMapCache[moduleSymbol] = aliasMap
	return aliasMap
}

// exportSortKey returns the binder-insertion-order key for an export symbol:
// pass 0 with the first FunctionDeclaration's position when the symbol has
// one (bound in the binder's functions-first pass), otherwise pass 1 with the
// first-bound declaration's position. tsgo appends Symbol.Declarations in
// binding order, so Declarations[0] is the insertion-defining declaration.
//
// NOTE: the "any FunctionDeclaration → pass 0" rule is per container —
// bindEachStatementFunctionsFirst runs for ModuleBlocks too (tsgo
// binder.go:1740), and every declaration of a module symbol's exports lives
// in that same container, so the key reconstructs the per-container binder
// order for namespaces as well (oracle-verified 2026-06-07: a namespace's
// exported functions sort before its consts/sub-namespaces, source order
// within each pass; same for the file-level return map).
func exportSortKey(symbol *ast.Symbol) (file string, pass int, pos int, ok bool) {
	var decl *ast.Node
	for _, d := range symbol.Declarations {
		if ast.IsFunctionDeclaration(d) {
			decl = d
			break
		}
	}
	if decl == nil {
		pass = 1
		if len(symbol.Declarations) > 0 {
			decl = symbol.Declarations[0]
		} else {
			decl = symbol.ValueDeclaration
		}
	}
	if decl == nil {
		return "", 0, 0, false
	}
	// RESOLVED(phase-3a, digest §2.4): the feared cross-file interleave never
	// reaches exportPairs — star-exported symbols are dropped by
	// getIgnoredExportSymbols and `export { x } from` symbols by
	// isExportSymbolFromExportFrom, so their assignments live at STATEMENT
	// position in transformExportFrom. Trailing pairs only ever contain the
	// file's OWN exports, which this single-file sort key already orders
	// correctly (rbxtsc ground truth: star loop + named-from assignment
	// in statement order, `exports.own` trailing).
	if sf := ast.GetSourceFileOfNode(decl); sf != nil {
		file = sf.FileName()
	}
	return file, pass, decl.Pos(), true
}
