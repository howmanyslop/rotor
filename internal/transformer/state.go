package transformer

import (
	"reflect"
	"strings"

	"rotor/internal/dotenv"
	"rotor/internal/luau"
	"rotor/internal/rojo"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
	"rotor/tsgo/compiler"
	"rotor/tsgo/scanner"
	"rotor/tsgo/tspath"
)

// ProjectType ports Shared/constants.ts ProjectType.
type ProjectType string

const (
	ProjectTypeGame    ProjectType = "game"
	ProjectTypeModel   ProjectType = "model"
	ProjectTypePackage ProjectType = "package"
)

// MultiState ports classes/MultiTransformState.ts: caches that live for one
// whole compilation step, shared across every file of that pass (recreated on
// each watch rebuild). Pure cache container, no methods.
type MultiState struct {
	IsMethodCache                        map[*ast.Symbol]bool
	IsDefinedAsLetCache                  map[*ast.Symbol]bool
	IsReportedByNoAnyCache               map[*ast.Symbol]bool
	IsReportedByMultipleDefinitionsCache map[*ast.Symbol]bool
	GetModuleExportsCache                map[*ast.Symbol][]*ast.Symbol
	GetModuleExportsAliasMapCache        map[*ast.Symbol]map[*ast.Symbol]string

	// macroManager is the upstream TransformServices analog: ONE MacroManager
	// per compilation pass, shared across every file's State (upstream
	// constructs it next to the MultiTransformState in compileFiles.ts and
	// threads it through `services`). Built by NewState from the first
	// checker-bearing State; see State.Macros.
	macroManager *MacroManager
}

// RojoContext carries the project-level Rojo data the reference TransformState
// receives as constructor arguments (compileFiles.ts:160-174): the resolver
// over the project's Rojo config, the input->output PathTranslator, the
// RuntimeLib rbxPath computed once per compile (compileFiles.ts:86-98), and
// the project path that noRojoData renders file paths relative to. Computed
// once per CompileProject pass and shared by every file's State.
type RojoContext struct {
	Resolver       *rojo.RojoResolver
	PathTranslator *rojo.PathTranslator
	// RuntimeLibRbxPath is nil for Package projects (upstream undefined),
	// selecting the `_G[script]` runtime-lib access form.
	RuntimeLibRbxPath rojo.RbxPath
	// ProjectPath is upstream data.projectPath (the tsconfig.json directory).
	ProjectPath string

	// Import-resolution members (createImportExpression pipeline, digest §3):

	// PkgRojoResolvers holds one RojoResolver.synthetic per typeRoot
	// (compileFiles.ts:77) — Package projects resolve node_modules imports
	// through these instead of the project resolver.
	PkgRojoResolvers []*rojo.RojoResolver
	// NodeModulesPathMapping maps the canonical types-entry path (.d.ts) of
	// each typeRoot package to its shipped main (.lua) path
	// (createNodeModulesPathMapping.ts). Keys are canonicalized via
	// rojo.CanonicalFileName with UseCaseSensitiveFileNames.
	NodeModulesPathMapping map[string]string
	// NodeModulesPath is upstream data.nodeModulesPath:
	// <package.json dir>/node_modules (createProjectData.ts:31).
	NodeModulesPath string
	// TypeRoots is compilerOptions.typeRoots as tsgo resolved them (absolute,
	// slash-separated); validateModule checks npm scopes against these.
	TypeRoots []string
	// UseCaseSensitiveFileNames feeds the canonical-file-name lookups
	// (Shared/util/getCanonicalFileName.ts).
	UseCaseSensitiveFileNames bool
	ImportPathMap             map[string]string
}

// NewMultiState returns an empty compilation-step cache container.
func NewMultiState() *MultiState {
	return &MultiState{
		IsMethodCache:                        make(map[*ast.Symbol]bool),
		IsDefinedAsLetCache:                  make(map[*ast.Symbol]bool),
		IsReportedByNoAnyCache:               make(map[*ast.Symbol]bool),
		IsReportedByMultipleDefinitionsCache: make(map[*ast.Symbol]bool),
		GetModuleExportsCache:                make(map[*ast.Symbol][]*ast.Symbol),
		GetModuleExportsAliasMapCache:        make(map[*ast.Symbol]map[*ast.Symbol]string),
	}
}

// State ports TSTransformer/classes/TransformState.ts: the per-source-file
// transform state. It owns prereq-statement stacking, hoisting bookkeeping,
// runtime-lib usage tracking, type caching, and module-export id mapping.
//
// Phase 3/4: emit-resolver-dependent state is absent for now — upstream's
// `resolver = typeChecker.getEmitResolver(sourceFile)` is consumed only by
// JSX factory entity lookups (getJsxFactoryEntity/getJsxFragmentFactoryEntity)
// and import/export elision (isReferencedAliasDeclaration).
type State struct {
	Program    *compiler.Program // may be nil in mechanics tests
	Checker    *checker.Checker  // may be nil in mechanics tests
	SourceFile *ast.SourceFile
	Diags      *DiagService
	Multi      *MultiState

	// Rojo is the project-level Rojo context (nil in mechanics tests and in
	// any State that never reaches runtime-lib emission). Upstream threads the
	// equivalent fields through the TransformState constructor; rotor keeps
	// NewState's signature stable for the test fleet and installs the context
	// via SetRojoContext.
	Rojo *RojoContext

	// ProjectType + IsInReplicatedFirst gate the runtimeLibUsedInReplicatedFirst
	// warning in RuntimeLib. SetRojoContext computes IsInReplicatedFirst per
	// the upstream constructor (TransformState.ts:62-65:
	// pathTranslator.getOutputPath + rojoResolver.getRbxPathFromFilePath,
	// rbxPath[0] === "ReplicatedFirst"); without a Rojo context both stay
	// zero-valued and the warning never fires.
	ProjectType         ProjectType
	IsInReplicatedFirst bool

	// SourceFileText is the full source text including leading trivia
	// (upstream sourceFile.getFullText()); used for comment extraction and
	// raw-text literal slicing.
	SourceFileText string

	sourcePositionMap    map[int]*SourcePosition
	sourceEndPositionMap map[int]*SourcePosition

	// UsesRuntimeLib is set ONLY by RuntimeLib (upstream TS() is the only
	// assignment in the entire repo); gates runtime-lib import emission.
	UsesRuntimeLib bool

	// LogTruthyChanges plumbs the `logTruthyChanges` project option into
	// createTruthinessChecks warnings (default off).
	LogTruthyChanges bool

	// Env is the project-level compile-time environment snapshot consumed by
	// the rotor $env macro (envmacro.go) — loaded once per compile pass by
	// compile.newProjectContext (like the Rojo context) and shared by every
	// file's State. nil in mechanics tests and checker-light states; the
	// macro then resolves names from the process environment only
	// (dotenv.Env.Lookup is nil-receiver safe).
	Env *dotenv.Env

	// Assets is the project-level asset resolver consumed by the rotor $asset
	// macro (assetmacro.go) — constructed once per compile pass by
	// compile.newProjectContext (lockfile + optional cloud client) and shared
	// by every file's State. nil in mechanics tests and checker-light states;
	// the macro then emits a clear diagnostic (DiagRotorAssetNoResolver)
	// rather than panicking.
	Assets AssetResolver

	// Files is the project-level data-file reader consumed by the rotor $file
	// macro (filemacro.go) — constructed once per compile pass by
	// compile.newProjectContext (rooted at the project dir) and shared by every
	// file's State. nil in mechanics tests and checker-light states; the macro
	// then emits a clear diagnostic (DiagRotorFileNoResolver) rather than
	// panicking.
	Files FileResolver

	// Stamps is the project-level build/VCS provider consumed by the rotor $git
	// and $buildTime macros (gitmacro.go) — constructed once per compile pass by
	// compile.newProjectContext (reads .git natively + time.Now) and shared by
	// every file's State. nil in mechanics tests and checker-light states; the
	// macros then fall back to a no-op provider (empty/false) so they never
	// panic.
	Stamps StampProvider

	// OptimizedLoops plumbs the `optimizedLoops` project option into
	// transformForStatement (upstream gate: transformForStatement.ts L492;
	// DEFAULT_PROJECT_OPTIONS.optimizedLoops = true). NewState defaults it on
	// so option-free call sites keep the upstream-default behavior.
	OptimizedLoops bool

	// HasExportEquals is set by transformExportAssignment when `export = x`
	// is seen; changes export emission shape.
	HasExportEquals bool
	// HasExportFrom is set by transformExportDeclaration when
	// `export ... from "..."` is seen; forces the `local exports = {}` form.
	HasExportFrom bool

	// prereqStack ports prereqStatementsStack — THE core mechanism: a stack of
	// statement lists that prerequisite statements are appended onto.
	prereqStack []*luau.List[luau.Statement]

	// tryUsesStack ports tryUsesStack (TransformState.ts:68-97): one TryUses
	// entry per enclosing try statement currently being transformed. The
	// break/continue/return transforms mark the TOP entry when they reroute;
	// transformTryStatement reads the flags back after popping.
	tryUsesStack []*TryUses

	// labelStack holds one labelEntry per TS loop label currently in scope,
	// innermost last (rotor extension — see labeledstatement.go). breakDepth
	// counts the enclosing BREAK BOUNDARIES: constructs that a plain Luau
	// `break` exits in the emitted output (a real loop, the
	// `repeat ... until true` a switch lowers to, and the one a labeled
	// non-loop statement lowers to). Together they answer both questions the
	// label lowering needs: is this label on the innermost boundary (plain
	// break/continue), and which checks must be emitted after a boundary ends.
	labelStack []*labelEntry
	breakDepth int

	// getTypeCache memoizes GetType by the ORIGINAL node pointer (not the
	// SkipUpwards result).
	getTypeCache map[*ast.Node]*checker.Type

	// HoistsByStatement maps a ts.Statement (or ts.CaseClause) to the
	// ts.Identifier nodes needing a `local x` hoist emitted just before that
	// statement. NOTE: these are TS identifiers, not luau nodes —
	// createHoistDeclaration runs transformIdentifierDefined on them later.
	HoistsByStatement map[*ast.Node][]*ast.Node
	// IsHoisted memoizes per-symbol hoist decisions (upstream reads
	// `.get(symbol) !== undefined`; once decided, never reconsidered).
	IsHoisted map[*ast.Symbol]bool

	// SymbolToID maps a symbol to its replacement temp id (upstream
	// symbolToIdMap, used by transformIdentifierDefined for renamed/captured
	// vars).
	SymbolToID map[*ast.Symbol]*luau.TemporaryIdentifier

	// ClassIdentifierMap maps a ClassLikeDeclaration node to its INTERNAL luau
	// identifier (upstream classIdentifierMap, TransformState.ts:28) — for a
	// named class expression that is the inner name (`Inner`), not the `_class`
	// temp. Consumed by transformThisExpression for `this` in static blocks
	// and static property initializers.
	ClassIdentifierMap map[*ast.Node]luau.AnyIdentifier

	// classElementToObjectKeyMap ports classElementToObjectKeyMap
	// (TransformState.ts:390): the pinned object key of a decorated class
	// element, set by transformMethodDeclaration and read back by the
	// decorator transforms.
	classElementToObjectKeyMap map[*ast.Node]luau.Expression

	// moduleIDBySymbol maps a module symbol to the luau id holding that
	// module's exports table (source file symbol -> `exports`; namespaces ->
	// their container id).
	moduleIDBySymbol map[*ast.Symbol]luau.AnyIdentifier

	declarationToOptimizableVarArgs map[*ast.Node]*VarArgsData
}

// NewState constructs the per-file transform state. program/chk/sourceFile may
// be nil for checker-free prereq-mechanics tests (see NewTestState).
func NewState(program *compiler.Program, chk *checker.Checker, sourceFile *ast.SourceFile, diags *DiagService, multi *MultiState) *State {
	s := &State{
		Program:                         program,
		Checker:                         chk,
		SourceFile:                      sourceFile,
		Diags:                           diags,
		Multi:                           multi,
		OptimizedLoops:                  true, // DEFAULT_PROJECT_OPTIONS.optimizedLoops
		getTypeCache:                    make(map[*ast.Node]*checker.Type),
		HoistsByStatement:               make(map[*ast.Node][]*ast.Node),
		IsHoisted:                       make(map[*ast.Symbol]bool),
		SymbolToID:                      make(map[*ast.Symbol]*luau.TemporaryIdentifier),
		moduleIDBySymbol:                make(map[*ast.Symbol]luau.AnyIdentifier),
		declarationToOptimizableVarArgs: make(map[*ast.Node]*VarArgsData),
		sourcePositionMap:               make(map[int]*SourcePosition),
		sourceEndPositionMap:            make(map[int]*SourcePosition),

		ClassIdentifierMap:         make(map[*ast.Node]luau.AnyIdentifier),
		classElementToObjectKeyMap: make(map[*ast.Node]luau.Expression),
	}
	if sourceFile != nil {
		s.SourceFileText = sourceFile.Text()
	}
	// One MacroManager per compilation pass (upstream constructs it once per
	// Program); the first checker-bearing State builds it.
	if multi.macroManager == nil && chk != nil {
		multi.macroManager = NewMacroManager(chk)
	}
	return s
}

// SourcePosition is a 0-indexed Source Map v3 source coordinate. Columns use
// UTF-16 code units, matching TypeScript and the Source Map v3 specification.
type SourcePosition struct {
	Line   int
	Column int
}

func sourcePositionKey(node luau.Node) int {
	return int(reflect.ValueOf(node).Pointer())
}

func (s *State) getOriginalSourcePosition(node *ast.Node, useEnd bool) *SourcePosition {
	if node == nil {
		return nil
	}
	sourceFile := ast.GetSourceFileOfNode(node)
	if sourceFile == nil {
		sourceFile = s.SourceFile
	}
	if sourceFile == nil {
		return nil
	}

	position := scanner.GetTokenPosOfNode(node, sourceFile, false)
	if useEnd {
		position = node.End() - 1
		if position < node.Pos() {
			position = node.Pos()
		}
	}
	line, column := scanner.GetECMALineAndUTF16CharacterOfPosition(sourceFile, position)
	return &SourcePosition{Line: line, Column: int(column)}
}

func (s *State) setSourcePosition(node luau.Node, position *SourcePosition) {
	if position != nil {
		s.sourcePositionMap[sourcePositionKey(node)] = position
	}
}

func (s *State) setSourceEndPosition(node luau.Node, position *SourcePosition) {
	if position != nil {
		s.sourceEndPositionMap[sourcePositionKey(node)] = position
	}
}

func (s *State) setStatementListSourcePositions(statements *luau.List[luau.Statement], sourceNode *ast.Node) {
	start := s.getOriginalSourcePosition(sourceNode, false)
	end := s.getOriginalSourcePosition(sourceNode, true)
	statements.ForEach(func(statement luau.Statement) {
		s.setSourcePosition(statement, start)
		s.setSourceEndPosition(statement, end)
	})
}

// SetRojoContext installs the project-level Rojo context and project type,
// then computes IsInReplicatedFirst exactly as the upstream TransformState
// constructor does (TransformState.ts:62-65). Call before TransformSourceFile;
// upstream takes these as constructor arguments, rotor keeps NewState's
// signature stable for existing call sites.
func (s *State) SetRojoContext(rc *RojoContext, projectType ProjectType) {
	s.Rojo = rc
	s.ProjectType = projectType
	if rc != nil && s.SourceFile != nil {
		sourceOutPath := rc.PathTranslator.GetOutputPath(s.SourceFile.FileName())
		rbxPath, ok := rc.Resolver.GetRbxPathFromFilePath(sourceOutPath)
		s.IsInReplicatedFirst = ok && len(rbxPath) > 0 && rbxPath[0] == "ReplicatedFirst"
	}
}

// NewTestState returns a state with nil program/checker for prereq-mechanics
// tests that never touch types.
func NewTestState() *State {
	return NewState(nil, nil, nil, NewDiagService(), NewMultiState())
}

// ---------------------------------------------------------------------------
// Prereq statement stack (upstream lines 99-178)
// ---------------------------------------------------------------------------

// Prereq appends a prerequisite statement to the top of the prereq stack
// (upstream prereq). Calling with an empty stack panics, matching the
// upstream JS crash (`undefined.push`); every transformStatement call site is
// wrapped in Capture, so a list is always present during transformation.
func (s *State) Prereq(stmt luau.Statement) {
	s.prereqStack[len(s.prereqStack)-1].Push(stmt)
}

// PrereqList splices an entire statement list onto the top of the prereq
// stack (upstream prereqList; the source list must not be reused).
func (s *State) PrereqList(list *luau.List[luau.Statement]) {
	s.prereqStack[len(s.prereqStack)-1].PushList(list)
}

func (s *State) pushPrereqStack() *luau.List[luau.Statement] {
	list := luau.NewList[luau.Statement]()
	s.prereqStack = append(s.prereqStack, list)
	return list
}

func (s *State) popPrereqStack() *luau.List[luau.Statement] {
	n := len(s.prereqStack)
	if n == 0 {
		panic("transformer: popPrereqStack on empty stack") // upstream assert
	}
	top := s.prereqStack[n-1]
	s.prereqStack = s.prereqStack[:n-1]
	return top
}

// CaptureStatements runs cb with a fresh prereq list on the stack and returns
// the statements it produced (upstream capturePrereqs).
func (s *State) CaptureStatements(cb func()) *luau.List[luau.Statement] {
	depth := len(s.prereqStack)
	list := s.pushPrereqStack()
	// DIVERGENCE: upstream JS has no try/finally — an exception aborts the
	// whole compile, so stack hygiene doesn't matter there. In Go a recovered
	// panic (e.g. NoPrereqs' assert caught by a test) would leave the stack
	// dirty, so restore the entry depth on the way out. This is a no-op on
	// the normal path, which pops exactly the list pushed above.
	defer func() {
		if len(s.prereqStack) > depth {
			s.prereqStack = s.prereqStack[:depth]
		}
	}()
	cb()
	s.popPrereqStack()
	return list
}

// Capture returns the expression produced by cb along with its prerequisite
// statements (upstream capture<T>; Go methods cannot be generic, so this is
// specialized to the dominant luau.Expression case).
func (s *State) Capture(cb func() luau.Expression) (luau.Expression, *luau.List[luau.Statement]) {
	var value luau.Expression
	prereqs := s.CaptureStatements(func() { value = cb() })
	return value, prereqs
}

// NoPrereqs runs cb and asserts it created no prerequisite statements
// (upstream noPrereqs; the assert is a panic, as upstream).
func (s *State) NoPrereqs(cb func() luau.Expression) luau.Expression {
	var expression luau.Expression
	statements := s.CaptureStatements(func() { expression = cb() })
	if statements.IsNonEmpty() {
		panic("transformer: NoPrereqs callback created prerequisite statements")
	}
	return expression
}

// ---------------------------------------------------------------------------
// Try uses stack (upstream lines 68-97, types.ts L7-11)
// ---------------------------------------------------------------------------

// TryUses ports types.ts TryUses: which flow-control kinds escape a try block
// and must be rerouted through the TS.try exitType protocol.
type TryUses struct {
	UsesReturn   bool
	UsesBreak    bool
	UsesContinue bool
}

// PushTryUsesStack ports pushTryUsesStack: push a fresh all-false TryUses and
// RETURN it — the caller keeps the pointer and reads the flags after pop.
func (s *State) PushTryUsesStack() *TryUses {
	uses := &TryUses{}
	s.tryUsesStack = append(s.tryUsesStack, uses)
	return uses
}

// PopTryUsesStack ports popTryUsesStack.
func (s *State) PopTryUsesStack() {
	n := len(s.tryUsesStack)
	if n == 0 {
		panic("transformer: PopTryUsesStack on empty stack") // upstream assert
	}
	s.tryUsesStack = s.tryUsesStack[:n-1]
}

// markTryUses ports markTryUses(property): set a flag on the TOP entry;
// a NO-OP when the stack is empty (upstream L87-89) — this is what makes
// return/break/continue outside any try free.
func (s *State) markTryUses(mark func(*TryUses)) {
	if len(s.tryUsesStack) == 0 {
		return
	}
	mark(s.tryUsesStack[len(s.tryUsesStack)-1])
}

// MarkTryUsesReturn ports markTryUses("usesReturn").
func (s *State) MarkTryUsesReturn() { s.markTryUses(func(u *TryUses) { u.UsesReturn = true }) }

// MarkTryUsesBreak ports markTryUses("usesBreak").
func (s *State) MarkTryUsesBreak() { s.markTryUses(func(u *TryUses) { u.UsesBreak = true }) }

// MarkTryUsesContinue ports markTryUses("usesContinue").
func (s *State) MarkTryUsesContinue() { s.markTryUses(func(u *TryUses) { u.UsesContinue = true }) }

// ---------------------------------------------------------------------------
// Loop labels (rotor extension — no upstream counterpart in rbxtsc 3.0.0)
// ---------------------------------------------------------------------------

// Label flag values. Luau has no labels and no goto, so a TS label becomes a
// string flag variable read by checks emitted after each enclosing break
// boundary. The three values mirror the LoopLabel enum of roblox-ts PR #2928
// so a future upstream release lines up.
const (
	labelNone     = "none"
	labelBreak    = "break"
	labelContinue = "continue"
)

// labelEntry is one live TS label. Depth is the break depth of the construct
// the label is attached to (a label pushed while breakDepth is d attaches to
// the construct opened next, at d+1). EverBroken/EverContinued record whether
// any `break <name>` / `continue <name>` actually had to route through the
// flag; both are known only after the labeled construct's body is transformed,
// which is why the `local <id>` declaration and the per-iteration reset are
// decided at that point.
type labelEntry struct {
	ID            *luau.TemporaryIdentifier
	Name          string
	Depth         int
	IsLoop        bool
	EverBroken    bool
	EverContinued bool
}

// used reports whether the flag variable is read anywhere, i.e. whether it
// needs to be declared at all.
func (e *labelEntry) used() bool { return e.EverBroken || e.EverContinued }

// PushLabel opens a new label scope for the construct about to be transformed.
func (s *State) PushLabel(name string, isLoop bool) *labelEntry {
	entry := &labelEntry{
		ID:     luau.TempID(name),
		Name:   name,
		Depth:  s.breakDepth + 1,
		IsLoop: isLoop,
	}
	s.labelStack = append(s.labelStack, entry)
	return entry
}

// PopLabel closes the innermost label scope.
func (s *State) PopLabel() {
	n := len(s.labelStack)
	if n == 0 {
		panic("transformer: PopLabel on empty stack")
	}
	s.labelStack = s.labelStack[:n-1]
}

// FindLabel resolves a label name innermost-first, returning nil when no such
// label is in scope (only reachable when the TS error was suppressed).
func (s *State) FindLabel(name string) *labelEntry {
	for i := len(s.labelStack) - 1; i >= 0; i-- {
		if s.labelStack[i].Name == name {
			return s.labelStack[i]
		}
	}
	return nil
}

// EnterBreakScope opens a break boundary; every loop, switch and labeled
// non-loop statement transform must pair it with ExitBreakScope (use defer —
// one missed exit corrupts every later depth).
func (s *State) EnterBreakScope() { s.breakDepth++ }

// ExitBreakScope closes a break boundary.
func (s *State) ExitBreakScope() {
	if s.breakDepth == 0 {
		panic("transformer: ExitBreakScope below zero")
	}
	s.breakDepth--
}

// BreakDepth reports the current break-boundary depth.
func (s *State) BreakDepth() int { return s.breakDepth }

// LabelResets returns the `<id> = "none"` statements to unshift into the body
// of the loop currently being transformed. Only continued labels need one: a
// "break" value can never be observed twice, since a break always unwinds out
// of the labeled construct, whereas a "continue" that reached its target loop
// would otherwise still be set on the next iteration.
func (s *State) LabelResets() *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	for _, entry := range s.labelStack {
		if entry.Depth == s.breakDepth && entry.EverContinued {
			result.Push(luau.NewAssignment(entry.ID, "=", luau.Str(labelNone)))
		}
	}
	return result
}

// LabelChecks returns the statements to emit immediately after the break
// boundary at the current depth ends — that is, in the enclosing boundary at
// depth-1. Only labels attached FURTHER OUT than this boundary need a check;
// a label on this very boundary was already satisfied by the plain `break`.
//
//	depth-1, loop, continued -> `if _a == "continue" then continue end`
//	anything else, still set -> `if _a == "break" then break end`
//
// The continue check ORs every qualifying label into ONE statement, so two
// labels on the same loop both work. Everything left over ORs into one break
// check: a flag targeting a construct further out than the enclosing one must
// keep unwinding, whichever value it holds.
func (s *State) LabelChecks() *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	depth := s.breakDepth

	var continueCondition luau.Expression
	var breakCondition luau.Expression
	or := func(into luau.Expression, term luau.Expression) luau.Expression {
		if into == nil {
			return term
		}
		return luau.NewBinary(into, "or", term)
	}
	isFlag := func(entry *labelEntry, value string) luau.Expression {
		return luau.NewBinary(entry.ID, "==", luau.Str(value))
	}

	for _, entry := range s.labelStack {
		if entry.Depth >= depth {
			continue
		}
		targetsEnclosing := entry.Depth == depth-1
		if entry.EverContinued {
			if targetsEnclosing && entry.IsLoop {
				continueCondition = or(continueCondition, isFlag(entry, labelContinue))
			} else {
				breakCondition = or(breakCondition, isFlag(entry, labelContinue))
			}
		}
		if entry.EverBroken {
			breakCondition = or(breakCondition, isFlag(entry, labelBreak))
		}
	}

	if continueCondition != nil {
		result.Push(luau.NewIf(continueCondition,
			luau.NewList[luau.Statement](luau.NewContinue()), nil))
	}
	if breakCondition != nil {
		result.Push(luau.NewIf(breakCondition,
			luau.NewList[luau.Statement](luau.NewBreak()), nil))
	}
	return result
}

// WithoutLabels runs fn with an empty label stack and a zeroed break depth,
// restoring both afterwards. This is the FUNCTION BARRIER: a Luau `break`
// cannot cross a function boundary and a flag variable read across one would
// target the wrong loop, so a nested function body (and the functions rotor's
// try lowering emits) starts from a clean slate.
func (s *State) WithoutLabels(fn func()) {
	savedStack, savedDepth := s.labelStack, s.breakDepth
	s.labelStack, s.breakDepth = nil, 0
	defer func() { s.labelStack, s.breakDepth = savedStack, savedDepth }()
	fn()
}

// ---------------------------------------------------------------------------
// pushToVar family (upstream lines 267-306)
// ---------------------------------------------------------------------------

// PushToVar declares and defines a new temp: emits `local <temp> = <expr>` as
// a prereq and returns the temp (upstream pushToVar). expr may be nil to
// pre-declare a temp without a value (`local _temp`). The temp's name hint is
// nameHint if non-empty, else derived via ValueToIdStr — exactly upstream's
// `name || (expression && valueToIdStr(expression))` ("" is falsy in JS).
func (s *State) PushToVar(expr luau.Expression, nameHint string) *luau.TemporaryIdentifier {
	name := nameHint
	if name == "" && expr != nil {
		name = ValueToIdStr(expr)
	}
	temp := luau.TempID(name)
	var right luau.NodeOrList
	if expr != nil {
		right = expr
	}
	s.Prereq(luau.NewVariableDeclaration(temp, right))
	return temp
}

// PushToVarIfComplex returns expr unchanged when it is simple (upstream
// luau.isSimple: Identifier, TemporaryIdentifier, NilLiteral, TrueLiteral,
// FalseLiteral, NumberLiteral, StringLiteral), else pushes it to a temp.
func (s *State) PushToVarIfComplex(expr luau.Expression, nameHint string) luau.Expression {
	if luau.IsSimple(expr) {
		return expr
	}
	return s.PushToVar(expr, nameHint)
}

// PushToVarIfNonID returns expr unchanged when it is already an identifier
// (upstream luau.isAnyIdentifier: Identifier | TemporaryIdentifier only),
// else pushes it to a temp.
func (s *State) PushToVarIfNonID(expr luau.Expression, nameHint string) luau.AnyIdentifier {
	if id, ok := expr.(luau.AnyIdentifier); ok {
		return id
	}
	return s.PushToVar(expr, nameHint)
}

// ---------------------------------------------------------------------------
// Class element object keys (upstream lines 390-399)
// ---------------------------------------------------------------------------

// SetClassElementObjectKey ports TransformState.setClassElementObjectKey
// (TransformState.ts:392-395): record the pinned key for a decorated class
// element, asserting no overwrite.
func (s *State) SetClassElementObjectKey(classElement *ast.Node, identifier luau.Expression) {
	if _, ok := s.classElementToObjectKeyMap[classElement]; ok {
		panic("transformer: SetClassElementObjectKey: key already set") // upstream assert
	}
	s.classElementToObjectKeyMap[classElement] = identifier
}

// GetClassElementObjectKey ports TransformState.getClassElementObjectKey
// (TransformState.ts:397-399).
func (s *State) GetClassElementObjectKey(classElement *ast.Node) luau.Expression {
	return s.classElementToObjectKeyMap[classElement]
}

// ---------------------------------------------------------------------------
// getType (upstream lines 183-186)
// ---------------------------------------------------------------------------

// GetType returns the type at SkipUpwards(node), memoized by the original
// node pointer (upstream getType). The nil-recompute mirrors upstream
// getOrSetDefault, which re-computes when the stored value is undefined.
func (s *State) GetType(node *ast.Node) *checker.Type {
	if t := s.getTypeCache[node]; t != nil {
		return t
	}
	t := s.Checker.GetTypeAtLocation(SkipUpwards(node))
	s.getTypeCache[node] = t
	return t
}

// ---------------------------------------------------------------------------
// RuntimeLib (upstream TS(), lines 188-197)
// ---------------------------------------------------------------------------

// RuntimeLib ports upstream TS(node, name): returns `TS.<name>` and flips
// UsesRuntimeLib — this is the SOLE place UsesRuntimeLib is set. The node
// parameter exists solely for the warning's source location and may be nil
// outside Game/ReplicatedFirst files.
func (s *State) RuntimeLib(node *ast.Node, name string) luau.IndexableExpression {
	s.UsesRuntimeLib = true
	if s.ProjectType == ProjectTypeGame && s.IsInReplicatedFirst {
		// Emitted once per call, not deduped (upstream).
		s.Diags.Add(DiagRuntimeLibUsedInReplicatedFirst(node))
	}
	return luau.GlobalProperty("TS", name)
}

// ---------------------------------------------------------------------------
// guessVirtualPath (upstream lines 365-384)
// ---------------------------------------------------------------------------

// GuessVirtualPath ports TransformState.guessVirtualPath: attempts a reverse
// symlink lookup so a realpathed node_modules file (pnpm installs the real
// files under node_modules/.pnpm/... and symlinks node_modules/<pkg> to them;
// tsgo realpaths such resolutions) maps back onto its symlink-side "virtual"
// path. Walks the realpath's ancestor directories from innermost out; the
// first ancestor present in the symlink cache's realpath->symlink reverse map
// rebases the file onto the symlink side. Returns "" when no ancestor is
// known to the cache (upstream returns undefined; the caller falls back to
// the realpath).
func (s *State) GuessVirtualPath(fsPath string) string {
	// s.Program may be nil in mechanics tests; upstream's optional chain on
	// getSymlinkCache covers the analogous TS-version probe.
	if s.Program == nil {
		return ""
	}
	byRealpath := s.Program.GetSymlinkCache().DirectoriesByRealpath()
	original := fsPath
	for {
		// reverseSymlinkMap always has trailing slashes as it is constructed
		// from `SymlinkedDirectory.real` (upstream comment; tsgo's
		// KnownDirectoryLink.RealPath keys are EnsureTrailingDirectorySeparator
		// canonical paths, and ToPath of a trailing-separator string keeps the
		// separator).
		parent := tspath.EnsureTrailingDirectorySeparator(tspath.GetDirectoryPath(fsPath))
		if fsPath == parent {
			break
		}
		fsPath = parent
		key := tspath.ToPath(fsPath, s.Program.GetCurrentDirectory(), s.Program.UseCaseSensitiveFileNames())
		if set, ok := byRealpath.Load(key); ok {
			nodeModulesPath := ""
			if s.Rojo != nil {
				nodeModulesPath = s.Rojo.NodeModulesPath
			}
			symlink := selectSymlinkPath(set.ToSlice(), nodeModulesPath)
			if symlink != "" {
				// path.join(symlink, path.relative(fsPath, original)): fsPath
				// is an ancestor of original (built by repeated dirname, same
				// casing) with a trailing separator, so the relative part is
				// the prefix-stripped remainder.
				return tspath.CombinePaths(symlink, strings.TrimPrefix(original, fsPath))
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Emit resolver (upstream state.resolver, TransformState.ts:61)
// ---------------------------------------------------------------------------

// EmitResolver returns the checker's emit resolver, the upstream
// `state.resolver = typeChecker.getEmitResolver(sourceFile)`. Its only
// consumer in this phase is IsReferencedAliasDeclaration (import/export
// elision, digest §1.4).
//
// CHECKER-IDENTITY CONTRACT: `aliasSymbolLinks.referenced` marks are stored on
// the checker INSTANCE that semantically checked the file (markAliasReferenced,
// tsgo checker.go:28500). tsgo's built-in pool spreads files round-robin over
// up to 4 checkers and GetSemanticDiagnostics uses the file-associated one, so
// s.Checker MUST be that same instance or elision queries silently return
// false. rotor guarantees this by pinning the pool to a single checker
// (compilerOptions.Checkers = 1 in compile.newProjectProgram) and running
// program.GetSemanticDiagnostics(ctx, file) before transforming each file —
// proven by TestCompileProjectImports's elision cases.
func (s *State) EmitResolver() *checker.EmitResolver {
	return s.Checker.GetEmitResolver()
}

// ---------------------------------------------------------------------------
// MacroManager access (upstream services.macroManager)
// ---------------------------------------------------------------------------

// Macros returns the pass-wide MacroManager (upstream
// state.services.macroManager), creating an empty one lazily for
// checker-free mechanics states.
func (s *State) Macros() *MacroManager {
	if s.Multi.macroManager == nil {
		s.Multi.macroManager = NewMacroManager(s.Checker)
	}
	return s.Multi.macroManager
}

// AmbientSymbol resolves a global ambient symbol by name through the
// MacroManager's SYMBOL_NAMES registry (upstream
// macroManager.getSymbolOrThrow; rotor returns nil instead of throwing so
// checker-light test projects keep working — callers nil-guard).
func (s *State) AmbientSymbol(name string) *ast.Symbol {
	return s.Macros().Symbol(name)
}

// LuaTupleNominalSymbol resolves the `_nominal_LuaTuple` property symbol from
// the ambient LuaTuple<T> type alias (see MacroManager.LuaTupleNominalSymbol).
func (s *State) LuaTupleNominalSymbol() *ast.Symbol {
	return s.Macros().LuaTupleNominalSymbol()
}

// ---------------------------------------------------------------------------
// Module ids (upstream lines 339-354)
// ---------------------------------------------------------------------------

// SetModuleIDBySymbol records the luau id holding moduleSymbol's exports
// table (upstream setModuleIdBySymbol; transformSourceFile maps the file's
// module symbol to `exports`).
func (s *State) SetModuleIDBySymbol(moduleSymbol *ast.Symbol, moduleID luau.AnyIdentifier) {
	s.moduleIDBySymbol[moduleSymbol] = moduleID
}

// GetModuleIDFromSymbol returns the recorded module id, panicking if absent
// (upstream getModuleIdFromSymbol assert).
func (s *State) GetModuleIDFromSymbol(moduleSymbol *ast.Symbol) luau.AnyIdentifier {
	id, ok := s.moduleIDBySymbol[moduleSymbol]
	if !ok {
		panic("transformer: GetModuleIDFromSymbol: no module id recorded for symbol")
	}
	return id
}

// GetModuleIDFromNode ports TransformState.getModuleIdFromNode: the module id
// of the nearest module ancestor (file level => `exports`). Consumed by
// transformExportDeclaration and transformExportAssignment.
func (s *State) GetModuleIDFromNode(node *ast.Node) luau.AnyIdentifier {
	return s.GetModuleIDFromSymbol(s.getModuleSymbolFromNode(node))
}

func (s *State) registerOptimizableVarArgs(declaration *ast.Node, data *VarArgsData) {
	s.declarationToOptimizableVarArgs[declaration] = data
}

func (s *State) unregisterOptimizableVarArgs(declaration *ast.Node) {
	delete(s.declarationToOptimizableVarArgs, declaration)
}

func (s *State) getOptimizableVarArgsData(node *ast.Node) *VarArgsData {
	expression := SkipDownwards(node)
	if !ast.IsIdentifier(expression) {
		return nil
	}

	symbol := s.Checker.GetSymbolAtLocation(expression)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	return s.declarationToOptimizableVarArgs[symbol.ValueDeclaration]
}

// getModuleSymbolFromNode ports TransformState.getModuleSymbolFromNode: the
// export symbol of the nearest SourceFile or ModuleDeclaration ancestor
// (traversal.ts getModuleAncestor).
func (s *State) getModuleSymbolFromNode(node *ast.Node) *ast.Symbol {
	moduleAncestor := ast.FindAncestor(node, func(n *ast.Node) bool {
		return ast.IsSourceFile(n) || ast.IsModuleDeclaration(n)
	})
	location := moduleAncestor
	if !ast.IsSourceFile(moduleAncestor) {
		location = moduleAncestor.Name()
	}
	exportSymbol := s.Checker.GetSymbolAtLocation(location)
	if exportSymbol == nil {
		panic("transformer: getModuleSymbolFromNode: no module symbol") // upstream assert
	}
	return exportSymbol
}

// GetModuleIDPropertyAccess ports TransformState.getModuleIdPropertyAccess:
// `exports.<name>` (or the namespace container's id) when idSymbol is exported
// from its module, else nil. This is how `export let x` reads/writes become
// `exports.x` (transformIdentifier.ts:161-171, transformVariableStatement.ts:
// 26-40).
func (s *State) GetModuleIDPropertyAccess(idSymbol *ast.Symbol) *luau.PropertyAccessExpression {
	if idSymbol.ValueDeclaration != nil {
		moduleSymbol := s.getModuleSymbolFromNode(idSymbol.ValueDeclaration)
		if alias, ok := s.GetModuleExportsAliasMap(moduleSymbol)[idSymbol]; ok {
			return luau.NewPropertyAccess(s.GetModuleIDFromSymbol(moduleSymbol), alias)
		}
	}
	return nil
}
