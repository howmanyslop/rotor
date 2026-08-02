package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// This file ports expressions/transformAwaitExpression.ts,
// expressions/transformYieldExpression.ts, and
// util/wrapStatementsAsGenerator.ts.

// transformAwaitExpression ports transformAwaitExpression.ts (L7-9) — the
// ENTIRE transform: `await x` -> `TS.await(x)`. No diagnostics, no type
// checks, no async-context validation (the TS checker already rejects `await`
// outside async functions). TS.await passes non-Promise values through
// unchanged at runtime, so the wrapper is NEVER elided for non-promise
// operands.
func transformAwaitExpression(s *State, node *ast.Node) luau.Expression {
	return luau.NewCall(s.RuntimeLib(node, "await"), luau.NewList[luau.Expression](
		TransformExpression(s, SkipDownwards(node.AsAwaitExpression().Expression))))
}

// coroutineYield returns `coroutine.yield` (upstream luau.globals.coroutine.yield).
func coroutineYield() luau.IndexableExpression {
	return luau.GlobalProperty("coroutine", "yield")
}

// transformYieldExpression ports transformYieldExpression.ts (L8-54). Three
// cases:
//
//   - bare `yield` -> `coroutine.yield()` (zero args, NOT nil);
//   - `yield v` -> `coroutine.yield(v)` — resume arguments surface as the
//     call's return value, so `const got = yield 1` becomes
//     `local got = coroutine.yield(1)`;
//   - `yield* g` lowers to a generic for over the inner generator's bare
//     `.next` (no parens — the function value itself is the iterator),
//     re-yielding each value; when the result is used as a value, the inner
//     generator's RETURN value (`_result.value` at `done`) is captured in
//     `_returnValue` before `break` and becomes the expression value. Temp
//     creation order for byte parity: `_result` BEFORE `_returnValue`.
func transformYieldExpression(s *State, node *ast.Node) luau.Expression {
	yield := node.AsYieldExpression()
	if yield.Expression == nil {
		return luau.NewCall(coroutineYield(), luau.NewList[luau.Expression]())
	}

	expression := TransformExpression(s, yield.Expression)
	if yield.AsteriskToken != nil {
		loopID := luau.TempID("result")

		finalizer := luau.NewList[luau.Statement](luau.NewBreak())
		var evaluated luau.Expression = luau.NewNone()

		if !isUsedAsStatement(node) {
			returnValue := s.PushToVar(nil, "returnValue")
			finalizer.Unshift(luau.NewAssignment(returnValue, "=", luau.NewPropertyAccess(loopID, "value")))
			evaluated = returnValue
		}

		s.Prereq(luau.NewFor(
			luau.NewList[luau.AnyIdentifier](loopID),
			luau.NewPropertyAccess(convertToIndexableExpression(expression), "next"),
			luau.NewList[luau.Statement](
				luau.NewIf(luau.NewPropertyAccess(loopID, "done"), finalizer, nil),
				luau.NewCallStatement(luau.NewCall(coroutineYield(),
					luau.NewList[luau.Expression](luau.NewPropertyAccess(loopID, "value")))),
			)))

		return evaluated
	}
	return luau.NewCall(coroutineYield(), luau.NewList[luau.Expression](expression))
}

func isVarArgsPackingStatement(statement luau.Statement) bool {
	declaration, ok := statement.(*luau.VariableDeclaration)
	if !ok {
		return false
	}
	array, ok := declaration.Right.(*luau.Array)
	if !ok {
		return false
	}
	members := array.Members.ToSlice()
	if len(members) != 1 {
		return false
	}
	_, ok = members[0].(*luau.VarArgsLiteral)
	return ok
}

func wrapStatementsAsGenerator(s *State, node *ast.Node, statements *luau.List[luau.Statement]) *luau.List[luau.Statement] {
	outerStatements := luau.NewList[luau.Statement]()
	generatorStatements := luau.NewList[luau.Statement]()
	statements.ForEach(func(statement luau.Statement) {
		if isVarArgsPackingStatement(statement) {
			outerStatements.Push(statement)
			return
		}
		generatorStatements.Push(statement)
	})
	outerStatements.Push(luau.NewReturn(luau.NewCall(
		s.RuntimeLib(node, "generator"),
		luau.NewList[luau.Expression](
			luau.NewFunctionExpression(luau.NewList[luau.AnyIdentifier](), false, generatorStatements),
		),
	)))
	return outerStatements
}
