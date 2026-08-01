package transformer

import (
	"sort"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// This file ports TSTransformer/classes/MacroManager.ts and
// TSTransformer/macros/types.ts: ONE component owning all macro
// identification, shared per compilation pass via MultiState (upstream
// constructs the MacroManager once per Program and threads it through
// TransformServices).
//
// Phase 3b Task 5 state of the tables: ALL upstream macros are implemented —
// CONSTRUCTOR_MACROS (constructormacros.go), IDENTIFIER_MACROS and
// CALL_MACROS (callmacros.go), PROPERTY_CALL_MACROS (propertycallmacros.go:
// math classes; stringmacros.go: String/ArrayLike; arraymacros.go/
// arraymacros2.go: ReadonlyArray/Array; collectionmacros.go: ReadonlySet/
// Set/ReadonlyMap/Map/Promise). The lookup methods therefore match upstream
// exactly: table-only, plus getPropertyCallMacro's macro-only-class assert
// (`Macro X.y() is not implemented!`) for compiler-types methods on
// MACRO_ONLY_CLASSES that lack a registered macro — the forward-compat guard
// for a compiler-types package newer than the compiler. The Phase 2
// "any compiler-types symbol is a macro" detection fallback is GONE.
//
// Registration divergence: upstream throws ProjectError at construction when
// a registration name cannot be resolved ("You may need to update your
// @rbxts/compiler-types!"); rotor skips the entry so checker-light test
// projects keep working (same divergence as the AmbientSymbol nil-return
// pattern) — BUT records every skip with the exact upstream ProjectError
// text (see Missing). CompileProject/CompileFile fail hard when Missing() is
// non-empty, restoring upstream's ProjectError-before-any-emit contract for
// real projects: a failed ResolveName must never silently regress a macro to
// a plain method call (the damage-numbers.ts bug class: `v.add(w)` -> wrong
// `v:add(w)` output with no diagnostic). The sentinel gating (compiler-types
// present iff LuaTuple resolves; @rbxts/types present iff CFrame resolves)
// keeps checker-light unit-test projects, which lack the packages entirely,
// from failing the audit.

// ---------------------------------------------------------------------------
// Macro signatures — macros/types.ts
// ---------------------------------------------------------------------------

// IdentifierMacro ports macros/types.ts IdentifierMacro.
type IdentifierMacro func(s *State, node *ast.Node) luau.Expression

// ConstructorMacro ports macros/types.ts ConstructorMacro (node is the
// ts.NewExpression).
type ConstructorMacro func(s *State, node *ast.Node) luau.Expression

// CallMacro ports macros/types.ts CallMacro.
type CallMacro func(s *State, node *ast.Node, expression luau.Expression, args []luau.Expression) luau.Expression

// PropertyCallMacro ports macros/types.ts PropertyCallMacro.
type PropertyCallMacro func(s *State, node *ast.Node, expression luau.Expression, args []luau.Expression) luau.Expression

// IdentifierMacroEntry / CallMacroEntry / PropertyCallMacroEntry pair a macro
// with its upstream registration name (used in diagnostics). Macro == nil
// means the macro exists upstream but rotor has not implemented it yet —
// as of Phase 3b Task 5 every table entry is implemented, so callers' nil
// branches are defensive dead code.
type IdentifierMacroEntry struct {
	Name  string
	Macro IdentifierMacro
}

type CallMacroEntry struct {
	Name  string
	Macro CallMacro
}

type PropertyCallMacroEntry struct {
	Name  string
	Macro PropertyCallMacro
}

// ---------------------------------------------------------------------------
// Registration tables
// ---------------------------------------------------------------------------

// identifierMacroTable and callMacroTable live in callmacros.go.

// constructorMacroTable ports macros/constructorMacros.ts CONSTRUCTOR_MACROS
// (all implemented — constructormacros.go). Populated in init() because the
// macro funcs transitively reference State helpers that reach back to the
// MacroManager (Go initialization-cycle rule).
var constructorMacroTable map[string]ConstructorMacro

func init() {
	constructorMacroTable = map[string]ConstructorMacro{
		"ArrayConstructor": arrayConstructorMacro,
		"SetConstructor":   setConstructorMacro,
		"MapConstructor":   mapConstructorMacro,
		"WeakSetConstructor": func(s *State, node *ast.Node) luau.Expression {
			return wrapWeak(s, node, setConstructorMacro)
		},
		"WeakMapConstructor": func(s *State, node *ast.Node) luau.Expression {
			return wrapWeak(s, node, mapConstructorMacro)
		},
		"ReadonlyMapConstructor": mapConstructorMacro,
		"ReadonlySetConstructor": setConstructorMacro,
	}
}

// symbolNames ports MacroManager.ts SYMBOL_NAMES (the registry values, in
// upstream declaration order). Upstream resolves every entry eagerly in the
// constructor and throws on a miss; rotor resolves them eagerly too (misses
// recorded by the audit) and Symbol() reads the memoized results.
var symbolNames = []string{
	"globalThis",

	"ArrayConstructor",
	"SetConstructor",
	"MapConstructor",
	"WeakSetConstructor",
	"WeakMapConstructor",
	"ReadonlyMapConstructor",
	"ReadonlySetConstructor",

	"Array",
	"Generator",
	"IterableFunction",
	"LuaTuple",
	"Map",
	"Object",
	"ReadonlyArray",
	"ReadonlyMap",
	"ReadonlySet",
	"ReadVoxelsArray",
	"Set",
	"SharedTable",
	"String",
	"TemplateStringsArray",
	"WeakMap",
	"WeakSet",

	"Iterable",

	"$range",
	"$tuple",
}

// typesNotice ports MacroManager.ts TYPES_NOTICE, appended verbatim to every
// registration-failure ProjectError text.
const typesNotice = "\nYou may need to update your @rbxts/compiler-types!"

// rbxTypesClasses lists the PROPERTY_CALL_MACROS classes declared by
// @rbxts/types (include/macro_math.d.ts) rather than @rbxts/compiler-types.
// Their audit entries gate on the @rbxts/types sentinel (CFrame); everything
// else gates on the compiler-types sentinel (LuaTuple). Upstream throws
// unconditionally; the partition is rotor's test-friendly refinement — a
// project genuinely missing the packages already dies earlier in
// noLib/global resolution.
var rbxTypesClasses = map[string]bool{
	"CFrame":       true,
	"UDim":         true,
	"UDim2":        true,
	"Vector2":      true,
	"Vector2int16": true,
	"Vector3":      true,
	"Vector3int16": true,
	"Number":       true,
}

// NominalLuaTupleName ports Shared/constants.ts NOMINAL_LUA_TUPLE_NAME.
const NominalLuaTupleName = "_nominal_LuaTuple"

// macroOnlyClasses ports MacroManager.ts MACRO_ONLY_CLASSES (L53-63): the
// classes that exist ONLY as compiler macros — they have no runtime object,
// so a method on them WITHOUT a registered macro can never emit a real
// method call (see GetPropertyCallMacro's assert).
var macroOnlyClasses = map[string]bool{
	"ReadonlyArray": true,
	"Array":         true,
	"ReadonlyMap":   true,
	"WeakMap":       true,
	"Map":           true,
	"ReadonlySet":   true,
	"WeakSet":       true,
	"Set":           true,
	"String":        true,
}

// ---------------------------------------------------------------------------
// MacroManager
// ---------------------------------------------------------------------------

// MacroManager ports classes/MacroManager.ts: symbol -> macro lookup maps
// built once per compilation pass from the checker's ambient declarations,
// plus the SYMBOL_NAMES ambient-symbol registry (resolved eagerly like
// upstream; see Symbol for the lazy tail).
type MacroManager struct {
	chk *checker.Checker // nil in checker-free mechanics tests

	// symbols ports the SYMBOL_NAMES map: the upstream names are resolved
	// eagerly by the constructor; other names memoize lazily via Symbol
	// (including misses — presence in the map marks "resolved").
	symbols map[string]*ast.Symbol

	identifierMacros   map[*ast.Symbol]*IdentifierMacroEntry
	callMacros         map[*ast.Symbol]*CallMacroEntry
	constructorMacros  map[*ast.Symbol]ConstructorMacro
	propertyCallMacros map[*ast.Symbol]*PropertyCallMacroEntry

	// luaTupleNominal caches the `_nominal_LuaTuple` property symbol resolved
	// from the ambient LuaTuple<T> alias (see LuaTupleNominalSymbol).
	luaTupleNominal         *ast.Symbol
	luaTupleNominalResolved bool

	// Registration audit (digest §6): every registration that upstream's
	// constructor would have thrown ProjectError for is recorded with the
	// exact upstream message, partitioned by the package that declares the
	// failed name. Missing() gates each bucket on its package sentinel.
	missingCompilerTypes []string // gated on compilerTypesPresent
	missingRbxTypes      []string // gated on rbxTypesPresent
	compilerTypesPresent bool     // LuaTuple resolves (declared only by compiler-types)
	rbxTypesPresent      bool     // CFrame resolves (declared only by @rbxts/types)
}

// NewMacroManager builds the symbol->macro maps, mirroring the upstream
// constructor: each registration name is resolved through
// typeChecker.resolveName against the ambient declarations; constructor
// macros are keyed by the construct-signature symbol of the GLOBAL interface
// (so user-defined shadowing types won't collide). Unresolvable names are
// skipped (see the header note on the upstream-throw divergence).
func NewMacroManager(chk *checker.Checker) *MacroManager {
	m := &MacroManager{
		chk:                chk,
		symbols:            make(map[string]*ast.Symbol),
		identifierMacros:   make(map[*ast.Symbol]*IdentifierMacroEntry),
		callMacros:         make(map[*ast.Symbol]*CallMacroEntry),
		constructorMacros:  make(map[*ast.Symbol]ConstructorMacro),
		propertyCallMacros: make(map[*ast.Symbol]*PropertyCallMacroEntry),
	}
	if chk == nil {
		return m
	}

	// Audit gating sentinels (digest §6): LuaTuple's declaration lives only
	// in @rbxts/compiler-types; CFrame's only in @rbxts/types.
	m.compilerTypesPresent = chk.ResolveName("LuaTuple", nil, ast.SymbolFlagsAll, false) != nil
	m.rbxTypesPresent = chk.ResolveName("CFrame", nil, ast.SymbolFlagsInterface, false) != nil

	for name, macro := range identifierMacroTable {
		if symbol := chk.ResolveName(name, nil, ast.SymbolFlagsVariable, false); symbol != nil {
			m.identifierMacros[symbol] = &IdentifierMacroEntry{Name: name, Macro: macro}
		} else {
			// getGlobalSymbolByNameOrThrow (MacroManager.ts L78).
			m.recordMissing(name, "MacroManager could not find symbol for "+name+typesNotice)
		}
	}

	for name, macro := range callMacroTable {
		if symbol := chk.ResolveName(name, nil, ast.SymbolFlagsFunction, false); symbol != nil {
			m.callMacros[symbol] = &CallMacroEntry{Name: name, Macro: macro}
		} else {
			m.recordMissing(name, "MacroManager could not find symbol for "+name+typesNotice)
		}
	}

	for name, macro := range constructorMacroTable {
		symbol := chk.ResolveName(name, nil, ast.SymbolFlagsInterface, false)
		if symbol == nil {
			m.recordMissing(name, "MacroManager could not find symbol for "+name+typesNotice)
			continue
		}
		// getFirstDeclarationOrThrow(symbol, ts.isInterfaceDeclaration) +
		// getConstructorSymbol(interfaceDec): FIRST interface declaration only.
		var interfaceDec *ast.Node
		for _, declaration := range symbol.Declarations {
			if ast.IsInterfaceDeclaration(declaration) {
				interfaceDec = declaration
				break
			}
		}
		if interfaceDec == nil {
			// getFirstDeclarationOrThrow throws ProjectError("") — the empty
			// message is upstream's, verbatim (MacroManager.ts L70).
			m.recordMissing(name, "")
		} else if constructSymbol := interfaceConstructSymbol(interfaceDec); constructSymbol != nil {
			m.constructorMacros[constructSymbol] = macro
		} else {
			// getConstructorSymbol throw (MacroManager.ts L88).
			m.recordMissing(name, "MacroManager could not find constructor for "+name+typesNotice)
		}
	}

	// PROPERTY_CALL_MACROS registration (MacroManager.ts L119-144): resolve
	// the global interface, collect its method-signature member symbols across
	// ALL interface declarations (the math interfaces merge @rbxts/types
	// macro_math.d.ts into roblox.d.ts / compiler-types core.d.ts), then key
	// each macro by its method symbol. Upstream keys by
	// `getType(typeChecker, member).symbol` — the symbol of the method's
	// function type — which is exactly what GetFirstDefinedSymbol yields for
	// `a.b` at the call sites. All upstream rows are registered: math classes,
	// String/ArrayLike, ReadonlyArray/Array, ReadonlySet/Set/ReadonlyMap/Map,
	// Promise.
	for className, methods := range propertyCallMacroTable {
		symbol := chk.ResolveName(className, nil, ast.SymbolFlagsInterface, false)
		if symbol == nil {
			m.recordMissing(className, "MacroManager could not find symbol for "+className+typesNotice)
			continue
		}

		methodMap := make(map[string]*ast.Symbol)
		for _, declaration := range symbol.Declarations {
			if !ast.IsInterfaceDeclaration(declaration) {
				continue
			}
			for _, member := range declaration.AsInterfaceDeclaration().Members.Nodes {
				if ast.IsMethodSignatureDeclaration(member) && member.Name() != nil && ast.IsIdentifier(member.Name()) {
					// upstream getType: typeChecker.getTypeAtLocation(skipUpwards(member)).symbol
					if methodSymbol := chk.GetTypeAtLocation(SkipUpwards(member)).Symbol(); methodSymbol != nil {
						methodMap[member.Name().Text()] = methodSymbol
					}
				}
			}
		}

		for methodName, macro := range methods {
			// upstream throws ProjectError when the method is missing
			// (MacroManager.ts L138-141); rotor skips and records for the
			// audit (same checker-light-project divergence as the other
			// tables, made loud again by Missing()).
			if methodSymbol := methodMap[methodName]; methodSymbol != nil {
				m.propertyCallMacros[methodSymbol] = &PropertyCallMacroEntry{Name: className + "." + methodName, Macro: macro}
			} else {
				m.recordMissing(className, "MacroManager could not find method for "+className+"."+methodName+typesNotice)
			}
		}
	}

	// SYMBOL_NAMES registration (MacroManager.ts L146-153): upstream resolves
	// eagerly and throws on the first miss; rotor resolves eagerly into the
	// memo map Symbol() reads (misses memoized as nil, preserving the lazy
	// nil-return contract for callers) and records each miss for the audit.
	for _, name := range symbolNames {
		symbol := chk.ResolveName(name, nil, ast.SymbolFlagsAll, false)
		m.symbols[name] = symbol
		if symbol == nil {
			m.recordMissing(name, "MacroManager could not find symbol for "+name+typesNotice)
		}
	}

	return m
}

// recordMissing files a registration failure under the audit bucket of the
// package that declares className/name (see rbxTypesClasses).
func (m *MacroManager) recordMissing(name, message string) {
	if rbxTypesClasses[name] {
		m.missingRbxTypes = append(m.missingRbxTypes, message)
	} else {
		m.missingCompilerTypes = append(m.missingCompilerTypes, message)
	}
}

// Missing returns the upstream ProjectError texts for every registration
// that failed while the package declaring it is present (compiler-types
// present iff LuaTuple resolves; @rbxts/types present iff CFrame resolves),
// sorted for determinism (registration iterates Go maps). nil when the audit
// passes — including for checker-light projects without the types packages,
// where upstream's unconditional throw would be test-hostile. Upstream
// throws the FIRST failure at construction (ProjectError before any emit);
// rotor collects them all and CompileProject/CompileFile fail hard with the
// full list.
func (m *MacroManager) Missing() []string {
	var out []string
	if m.compilerTypesPresent {
		out = append(out, m.missingCompilerTypes...)
	}
	if m.rbxTypesPresent {
		out = append(out, m.missingRbxTypes...)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// interfaceConstructSymbol ports MacroManager.ts getConstructorSymbol: the
// symbol of the first construct-signature member of the interface
// declaration, or nil (upstream throws ProjectError).
func interfaceConstructSymbol(interfaceDec *ast.Node) *ast.Symbol {
	for _, member := range interfaceDec.AsInterfaceDeclaration().Members.Nodes {
		if ast.IsConstructSignatureDeclaration(member) {
			return member.Symbol()
		}
	}
	return nil
}

// Symbol ports MacroManager.getSymbolOrThrow:
// `typeChecker.resolveName(symbolName, undefined, ts.SymbolFlags.All, false)`,
// memoized (including misses). The upstream SYMBOL_NAMES set is resolved
// eagerly by NewMacroManager (with audit recording); other names resolve
// lazily here. Returns nil instead of throwing for projects without
// @rbxts/compiler-types (callers nil-guard; Missing() makes real projects
// fail loudly instead).
func (m *MacroManager) Symbol(name string) *ast.Symbol {
	if symbol, ok := m.symbols[name]; ok {
		return symbol
	}
	var symbol *ast.Symbol
	if m.chk != nil {
		symbol = m.chk.ResolveName(name, nil, ast.SymbolFlagsAll, false)
	}
	m.symbols[name] = symbol
	return symbol
}

// LuaTupleNominalSymbol resolves the `_nominal_LuaTuple` property symbol from
// the ambient LuaTuple<T> type alias, memoized. Ports the MacroManager
// constructor tail: find the LuaTuple TypeAliasDeclaration, take the type at
// that location, and grab its NOMINAL_LUA_TUPLE_NAME property. nil when the
// project has no @rbxts/compiler-types.
func (m *MacroManager) LuaTupleNominalSymbol() *ast.Symbol {
	if m.luaTupleNominalResolved {
		return m.luaTupleNominal
	}
	m.luaTupleNominalResolved = true
	if luaTupleSymbol := m.Symbol("LuaTuple"); luaTupleSymbol != nil {
		for _, declaration := range luaTupleSymbol.Declarations {
			if ast.IsTypeAliasDeclaration(declaration) {
				t := m.chk.GetTypeAtLocation(declaration)
				m.luaTupleNominal = m.chk.GetPropertyOfType(t, NominalLuaTupleName)
				break
			}
		}
	}
	return m.luaTupleNominal
}

// IsMacroOnlyClass ports macroManager.isMacroOnlyClass: the symbol must be
// THE registered global symbol of that name (`symbols.get(symbol.name) ===
// symbol` — not a same-named user type) AND the name must be in
// MACRO_ONLY_CLASSES.
func (m *MacroManager) IsMacroOnlyClass(symbol *ast.Symbol) bool {
	return m.symbols[symbol.Name] == symbol && macroOnlyClasses[symbol.Name]
}

// GetIdentifierMacro ports macroManager.getIdentifierMacro: table-only.
// The misuse guards around the hook (noConstructorMacroWithoutNew,
// noMacroExtends, noIndexWithoutCall) live in TransformIdentifier, exactly
// as upstream (transformIdentifier.ts L132-159).
func (m *MacroManager) GetIdentifierMacro(symbol *ast.Symbol) *IdentifierMacroEntry {
	return m.identifierMacros[symbol]
}

// GetCallMacro ports macroManager.getCallMacro: table-only.
func (m *MacroManager) GetCallMacro(symbol *ast.Symbol) *CallMacroEntry {
	return m.callMacros[symbol]
}

// IsTypeCheckCallMacro stands in for `macro === CALL_MACROS.typeIs ||
// macro === CALL_MACROS.typeOf` (isValidMethodIndexWithoutCall.ts L24-29).
func (m *MacroManager) IsTypeCheckCallMacro(symbol *ast.Symbol) bool {
	entry := m.GetCallMacro(symbol)
	return entry != nil && (entry.Name == "typeIs" || entry.Name == "typeOf")
}

// GetConstructorMacro ports macroManager.getConstructorMacro: table-only, no
// fallback — non-macro construct signatures take transformNewExpression's
// `X.new(args)` fallback, exactly as upstream.
func (m *MacroManager) GetConstructorMacro(symbol *ast.Symbol) ConstructorMacro {
	return m.constructorMacros[symbol]
}

// GetPropertyCallMacro ports macroManager.getPropertyCallMacro (L190-201)
// EXACTLY: table lookup, plus upstream's assert — a method symbol whose
// parent is THE registered global symbol of a macro-only class but which has
// no registered macro means the @rbxts/compiler-types package declares a
// method this compiler version does not implement:
// `assert(false, "Macro X.y() is not implemented!")`. Rotor panics with the
// same text; the CompileFile/CompileProject recover boundary surfaces it as
// an internal-compiler-error, never silent wrong output. Methods of
// NON-macro-only compiler-types classes (e.g. Generator.next, Promise.cancel)
// return nil here and emit as plain method calls, exactly as upstream.
func (m *MacroManager) GetPropertyCallMacro(symbol *ast.Symbol) *PropertyCallMacroEntry {
	entry := m.propertyCallMacros[symbol]
	if entry == nil &&
		symbol.Parent != nil &&
		m.symbols[symbol.Parent.Name] == symbol.Parent &&
		m.IsMacroOnlyClass(symbol.Parent) {
		panic("Macro " + symbol.Parent.Name + "." + symbol.Name + "() is not implemented!") // upstream assert(false, ...)
	}
	return entry
}
