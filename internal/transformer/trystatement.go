package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// ---------------------------------------------------------------------------
// Try statements — statements/transformTryStatement.ts +
// util/isBlockedByTryStatement.ts (COMPLETE)
//
// Runtime protocol (RuntimeLib TS.try): `TS.try(tryFn, catchFn|nil, finallyFn?)`
// returns `exitType, returns`. The three callbacks return NOTHING on normal
// completion, `TS.TRY_BREAK`/`TS.TRY_CONTINUE` (one value) for rerouted
// break/continue, or `TS.TRY_RETURN, { values... }` for rerouted return; a
// non-nil exitType from catch overrides try's, finally's overrides everything.
// Producers are ONLY the break/continue/return transforms (statements.go);
// only transformTryStatement consumes. Loops/switch need ZERO changes — the
// post-try if-chain is ordinary appended statements (oracle-verified incl.
// break-in-try-in-switch).
// ---------------------------------------------------------------------------

// isReturnBlockedByTryStatement ports util/isBlockedByTryStatement.ts (L3-9):
// `return` is blocked iff a TryStatement is hit before any function-like —
// a function inside a try resets (returns inside it are plain).
func isReturnBlockedByTryStatement(node *ast.Node) bool {
	ancestor := ast.FindAncestor(node, func(a *ast.Node) bool {
		return ast.IsTryStatement(a) || ast.IsFunctionLikeDeclaration(a)
	})
	return ancestor != nil && ast.IsTryStatement(ancestor)
}

// isBreakBlockedByTryStatement ports util/isBlockedByTryStatement.ts (L11-18):
// `break`/`continue` are blocked iff a TryStatement is hit before any loop OR
// switch. NOTE breakBlocked ⟹ returnBlocked: a try found before any
// loop/switch is necessarily found before any function boundary.
func isBreakBlockedByTryStatement(node *ast.Node) bool {
	ancestor := ast.FindAncestor(node, func(a *ast.Node) bool {
		return ast.IsTryStatement(a) || ast.IsIterationStatement(a, false) || ast.IsSwitchStatement(a)
	})
	return ancestor != nil && ast.IsTryStatement(ancestor)
}

// transformCatchClause ports transformTryStatement.ts transformCatchClause
// (L13-30): `catch (e)` -> `function(e)`; `catch` (no binding) -> `function()`.
// Binding patterns route through transformBindingName — destructure statements
// land BEFORE the block's statements.
func transformCatchClause(s *State, node *ast.Node) *luau.FunctionExpression {
	clause := node.AsCatchClause()
	parameters := luau.NewList[luau.AnyIdentifier]()
	statements := luau.NewList[luau.Statement]()

	if clause.VariableDeclaration != nil {
		parameters.Push(transformBindingName(s, clause.VariableDeclaration.Name(), statements))
	}

	statements.PushList(TransformStatementList(s, clause.Block, clause.Block.AsBlock().Statements.Nodes, nil))

	return luau.NewFunctionExpression(parameters, false, statements)
}

// transformIntoTryCall ports transformTryStatement.ts transformIntoTryCall
// (L32-76). try+catch: 2 args; try+finally (no catch): `TS.try(f, nil, fin)` —
// the explicit `nil` catch placeholder appears ONLY when finally exists (a
// TryStatement with neither is a parse error). The tryUses flags are read
// AFTER the blocks are transformed (the same TryUses object the block
// transforms mutated via markTryUses). NOTE the two-id VariableDeclaration is
// emitted even when only break/continue are used (`_returns` stays nil).
func transformIntoTryCall(s *State, node *ast.Node, exitTypeID, returnsID *luau.TemporaryIdentifier, tryUses *TryUses) luau.Statement {
	try := node.AsTryStatement()
	tryCallArgs := luau.NewList[luau.Expression]()

	tryCallArgs.Push(luau.NewFunctionExpression(
		luau.NewList[luau.AnyIdentifier](),
		false,
		TransformStatementList(s, try.TryBlock, try.TryBlock.AsBlock().Statements.Nodes, nil),
	))

	if try.CatchClause != nil {
		tryCallArgs.Push(transformCatchClause(s, try.CatchClause))
	} else {
		if try.FinallyBlock == nil {
			panic("transformer: try statement without catch or finally") // upstream assert
		}
		tryCallArgs.Push(luau.Nil())
	}

	if try.FinallyBlock != nil {
		tryCallArgs.Push(luau.NewFunctionExpression(
			luau.NewList[luau.AnyIdentifier](),
			false,
			TransformStatementList(s, try.FinallyBlock, try.FinallyBlock.AsBlock().Statements.Nodes, nil),
		))
	}

	if !tryUses.UsesReturn && !tryUses.UsesBreak && !tryUses.UsesContinue {
		return luau.NewCallStatement(luau.NewCall(s.RuntimeLib(node, "try"), tryCallArgs))
	}

	return luau.NewVariableDeclaration(
		luau.NewList[luau.AnyIdentifier](exitTypeID, returnsID),
		luau.NewCall(s.RuntimeLib(node, "try"), tryCallArgs),
	)
}

// createFlowControlCondition ports createFlowControlCondition (L78-85):
// `exitTypeId == TS.TRY_RETURN|TRY_BREAK|TRY_CONTINUE` — the constants are
// RuntimeLib property reads, each via RuntimeLib so usage is flagged.
func createFlowControlCondition(s *State, node *ast.Node, exitTypeID *luau.TemporaryIdentifier, flowControlConstant string) luau.Expression {
	return luau.NewBinary(exitTypeID, "==", s.RuntimeLib(node, flowControlConstant))
}

// flowControlCase ports the FlowControlCase shape: a nil condition means an
// unconditional tail (collapse substitutes the bare exitTypeId truthy test).
type flowControlCase struct {
	condition  luau.Expression
	statements *luau.List[luau.Statement]
}

// collapseFlowControlCases ports collapseFlowControlCases (L89-107): builds an
// elseif chain from the cases, with one twist — the LAST case's condition is
// REPLACED by the bare `exitTypeId` truthiness test (never emit
// `elseif _exitType == TS.TRY_CONTINUE then` as the final branch).
func collapseFlowControlCases(exitTypeID *luau.TemporaryIdentifier, cases []flowControlCase) *luau.List[luau.Statement] {
	if len(cases) == 0 {
		panic("transformer: collapseFlowControlCases: no cases") // upstream assert
	}

	next := luau.NewIf(exitTypeID, cases[len(cases)-1].statements, luau.NewList[luau.Statement]())

	for i := len(cases) - 2; i >= 0; i-- {
		condition := cases[i].condition
		if condition == nil {
			condition = exitTypeID
		}
		next = luau.NewIf(condition, cases[i].statements, next)
	}

	return luau.NewList[luau.Statement](next)
}

// transformFlowControl ports transformFlowControl (L109-186): the post-try
// dispatch on `_exitType`. The blocked checks start at node.Parent (NOT node) —
// starting at the try itself would always find the try and infinitely tunnel.
// The propagation marks run AFTER the pop in transformTryStatement, so they
// land on the ENCLOSING try's TryUses — the entire nested-try tunneling
// mechanism. The `returnBlocked && breakBlocked` early exit collapses to a
// bare `if _exitType then return _exitType, _returns end`, which correctly
// re-tunnels ALL THREE flags (TRY_BREAK/TRY_CONTINUE ride along with
// returns=nil) — the break/continue section is intentionally skipped; the
// `!returnBlocked && breakBlocked` combination is unreachable.
func transformFlowControl(s *State, node *ast.Node, exitTypeID, returnsID *luau.TemporaryIdentifier, tryUses *TryUses) *luau.List[luau.Statement] {
	var flowControlCases []flowControlCase

	if !tryUses.UsesReturn && !tryUses.UsesBreak && !tryUses.UsesContinue {
		return luau.NewList[luau.Statement]()
	}

	returnBlocked := isReturnBlockedByTryStatement(node.Parent)
	breakBlocked := isBreakBlockedByTryStatement(node.Parent)

	if tryUses.UsesReturn && returnBlocked {
		s.MarkTryUsesReturn()
	}
	if tryUses.UsesBreak && breakBlocked {
		s.MarkTryUsesBreak()
	}
	if tryUses.UsesContinue && breakBlocked {
		s.MarkTryUsesContinue()
	}

	if tryUses.UsesReturn {
		if returnBlocked {
			flowControlCases = append(flowControlCases, flowControlCase{
				condition: createFlowControlCondition(s, node, exitTypeID, "TRY_RETURN"),
				statements: luau.NewList[luau.Statement](
					luau.NewReturn(luau.NewList[luau.Expression](exitTypeID, returnsID)),
				),
			})
			if breakBlocked {
				return collapseFlowControlCases(exitTypeID, flowControlCases)
			}
		} else {
			flowControlCases = append(flowControlCases, flowControlCase{
				condition: createFlowControlCondition(s, node, exitTypeID, "TRY_RETURN"),
				statements: luau.NewList[luau.Statement](
					luau.NewReturn(luau.NewCall(luau.GlobalID("unpack"), luau.NewList[luau.Expression](returnsID))),
				),
			})
		}
	}

	if tryUses.UsesBreak || tryUses.UsesContinue {
		if breakBlocked {
			flowControlCases = append(flowControlCases, flowControlCase{
				statements: luau.NewList[luau.Statement](luau.NewReturn(exitTypeID)),
			})
		} else {
			if tryUses.UsesBreak {
				flowControlCases = append(flowControlCases, flowControlCase{
					condition:  createFlowControlCondition(s, node, exitTypeID, "TRY_BREAK"),
					statements: luau.NewList[luau.Statement](luau.NewBreak()),
				})
			}
			if tryUses.UsesContinue {
				flowControlCases = append(flowControlCases, flowControlCase{
					condition:  createFlowControlCondition(s, node, exitTypeID, "TRY_CONTINUE"),
					statements: luau.NewList[luau.Statement](luau.NewContinue()),
				})
			}
		}
	}

	return collapseFlowControlCases(exitTypeID, flowControlCases)
}

// transformTryStatement ports transformTryStatement (L188-200). ORDERING IS
// LOAD-BEARING twice: (1) exitTypeID/returnsID are allocated BEFORE the try
// body is transformed, so in nested tries the OUTER pair gets the unsuffixed
// names `_exitType`/`_returns` and the inner pair `_exitType_1`/`_returns_1`
// even though the inner declaration renders first textually; (2) the
// pop-before-transformFlowControl means the propagation marks land on the
// ENCLOSING try's TryUses.
func transformTryStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	statements := luau.NewList[luau.Statement]()
	exitTypeID := luau.TempID("exitType") // created FIRST — temp numbering parity
	returnsID := luau.TempID("returns")   // created SECOND

	tryUses := s.PushTryUsesStack()
	// ROTOR EXTENSION (loop labels): TS.try's three callbacks are Luau
	// functions, so they are label barriers exactly like any other function
	// body — a label check emitted inside one would have no loop to break.
	// Labels declared INSIDE the try body still work; only ones crossing it
	// are rejected (transformBreakStatement).
	var tryCall luau.Statement
	s.WithoutLabels(func() {
		tryCall = transformIntoTryCall(s, node, exitTypeID, returnsID, tryUses)
	})
	statements.Push(tryCall)
	s.PopTryUsesStack()

	statements.PushList(transformFlowControl(s, node, exitTypeID, returnsID, tryUses))

	return statements
}
