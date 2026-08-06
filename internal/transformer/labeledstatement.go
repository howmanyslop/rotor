package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// ---------------------------------------------------------------------------
// Labeled statements — ROTOR EXTENSION (rbxtsc 3.0.0 rejects labels with
// `noLabeledStatement`; roblox-ts PR #2928 is the design this follows).
//
// Luau has neither labels nor goto, so a TS label lowers to a string flag
// variable holding "none" | "break" | "continue":
//
//   - `break <name>` / `continue <name>` that targets the innermost break
//     boundary is a plain Luau `break` / `continue` — no flag at all;
//   - otherwise the flag is assigned and a plain `break` leaves the innermost
//     boundary, after which the checks emitted by State.LabelChecks re-break
//     (unwinding outwards) or continue at the labeled loop.
//
// A label on something that is not a loop (a block, an `if`, a `switch`, ...)
// needs a break boundary of its own to be broken out of, so it is wrapped in
// `repeat ... until true`, the same shape transformSwitchStatement uses.
// ---------------------------------------------------------------------------

// transformLabeledStatement lowers `name: <statement>`.
func transformLabeledStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	labeled := node.AsLabeledStatement()
	inner := labeled.Statement

	// `a: b: for (...)` labels the SAME loop twice, so look through nested
	// labels — both entries may be targeted by `continue`.
	entry := s.PushLabel(labeled.Label.Text(), ast.IsIterationStatement(inner, true))
	defer s.PopLabel()

	result := luau.NewList[luau.Statement]()

	if ast.IsIterationStatement(inner, false) || ast.IsLabeledStatement(inner) {
		// The loop (or the next label down) opens its own break boundary and
		// emits its own checks; nothing to wrap.
		result.PushList(TransformStatement(s, inner))
	} else {
		if wrapperCapturesFlowControl(inner) {
			s.Diags.Add(DiagRotorLabeledStatementFlowControl(node))
		}

		s.EnterBreakScope()
		// Transform through TransformStatementList rather than
		// TransformStatement so a labeled block dissolves into the repeat body
		// instead of nesting a redundant `do ... end`, and so any prereqs the
		// inner statement produces stay inside the boundary.
		statements := TransformStatementList(s, inner, getStatements(inner), nil)
		checks := s.LabelChecks()
		s.ExitBreakScope()

		result.Push(luau.NewRepeat(luau.Bool(true), statements))
		result.PushList(checks)
	}

	// Both the declaration and (in the loop transforms) the per-iteration reset
	// are decided here, AFTER the body is transformed — an unused label costs
	// nothing.
	if entry.used() {
		result.Unshift(luau.NewVariableDeclaration(entry.ID, nil))
	}

	return result
}

// wrapperCapturesFlowControl reports whether the `repeat ... until true` we
// wrap a labeled non-loop statement in would swallow an unlabeled `break` or
// `continue` that TypeScript binds to an enclosing loop or switch. Nested
// loops are skipped whole (both statements bind to them) and nested switches
// are searched for `continue` only (`break` binds to the switch); function
// bodies are skipped, since flow control cannot cross them either way.
func wrapperCapturesFlowControl(node *ast.Node) bool {
	var visit func(n *ast.Node, continueOnly bool) bool
	visit = func(n *ast.Node, continueOnly bool) bool {
		switch {
		case ast.IsFunctionLike(n) || ast.IsClassLike(n):
			return false
		case ast.IsIterationStatement(n, false):
			return false
		case ast.IsSwitchStatement(n):
			return n.ForEachChild(func(child *ast.Node) bool { return visit(child, true) })
		case n.Kind == ast.KindBreakStatement:
			return !continueOnly && n.AsBreakStatement().Label == nil
		case n.Kind == ast.KindContinueStatement:
			return n.AsContinueStatement().Label == nil
		}
		return n.ForEachChild(func(child *ast.Node) bool { return visit(child, continueOnly) })
	}
	return visit(node, false)
}
