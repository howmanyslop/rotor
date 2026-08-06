package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// ---------------------------------------------------------------------------
// Variable statements — statements/transformVariableStatement.ts
// ---------------------------------------------------------------------------

// transformVariable ports transformVariableStatement.ts transformVariable
// (L19-55) — the identifier-binding core, shared later by for-loop
// initializers and catch clauses.
func transformVariable(s *State, identifier *ast.Node, right luau.Expression) luau.WritableExpression {
	ValidateIdentifier(s, identifier)

	symbol := s.Checker.GetSymbolAtLocation(identifier)
	if symbol == nil {
		panic("transformer: transformVariable: no symbol") // upstream assert
	}

	// export let — declarations of mutable exported symbols write through the
	// exports table instead of creating a local.
	if IsSymbolMutable(s, symbol) {
		if exportAccess := s.GetModuleIDPropertyAccess(symbol); exportAccess != nil {
			if right != nil {
				s.Prereq(luau.NewAssignment(exportAccess, "=", right))
			}
			return exportAccess
		}
	}

	left := TransformIdentifierDefined(s, identifier)

	checkVariableHoist(s, identifier, symbol)
	if s.IsHoisted[symbol] {
		// no need to do `x = nil` if the variable is already created
		if right != nil {
			s.Prereq(luau.NewAssignment(left, "=", right))
		}
	} else {
		var rightValue luau.NodeOrList
		if right != nil {
			rightValue = right
		}
		s.Prereq(luau.NewVariableDeclaration(left, rightValue))
	}

	// Go interface note: luau.AnyIdentifier doesn't embed WritableExpression,
	// but both concrete identifier types implement it.
	return left.(luau.WritableExpression)
}

// checkVariableHoist ports util/checkVariableHoist.ts (L6-39): a let/const
// declared directly inside a switch CaseClause is block-scoped to the whole
// case BLOCK in TS, so a reference from a sibling clause needs a `local x`
// hoisted to the top of the declaring clause (before its `if`) and the
// declaration demoted to an assignment (transformVariable sees IsHoisted).
// Runs at DECLARATIONS, distinct from checkIdentifierHoist (use-before-
// declare, at references). Keyed on the CaseClause in HoistsByStatement —
// consumed by the switch transform's createHoistDeclaration; switch
// statements are live, so this fires for case-clause-scoped declarations.
func checkVariableHoist(s *State, identifier *ast.Node, symbol *ast.Symbol) {
	if _, decided := s.IsHoisted[symbol]; decided {
		return
	}

	statement := ast.FindAncestor(identifier, ast.IsStatement)
	if statement == nil {
		return
	}

	caseClause := statement.Parent
	if caseClause == nil || !ast.IsCaseClause(caseClause) {
		return
	}
	caseBlock := caseClause.Parent

	isUsedOutsideOfCaseClause := ForEachSymbolReference(s.Checker, identifier, caseBlock,
		func(token *ast.Node) bool {
			return !isAncestorOf(caseClause, token)
		})

	if isUsedOutsideOfCaseClause {
		s.HoistsByStatement[caseClause] = append(s.HoistsByStatement[caseClause], identifier)
		s.IsHoisted[symbol] = true
	}
}

// transformVariableDeclaration ports transformVariableStatement.ts
// transformVariableDeclaration (L101-167). The initializer is transformed
// BEFORE the name "so references inside of value can be hoisted" (upstream
// comment) — and before any arrayBindingPatternContainsHoists check, for the
// same reason. Array binding patterns have three paths:
//
//	a. LuaTuple direct unpack — `local a, b = f()` (the call was NOT
//	   array-wrapped thanks to shouldWrapLuaTuple);
//	b. literal-array RHS — `local a, b = x, y` (members inlined; this is why
//	   `const [head] = [10]` collapses to `local head = 10`);
//	c. fallback — `local _binding = <value>` + per-element accessor reads.
//
// a/b are gated on !arrayBindingPatternContainsHoists ("we can't localize
// multiple variables at the same time if any of them are hoisted"); an
// identifier RHS is NOT a literal array, so it always takes the fallback.
// Object binding patterns always take the `_binding` temp path.
func transformVariableDeclaration(s *State, declaration *ast.Node) *luau.List[luau.Statement] {
	statements := luau.NewList[luau.Statement]()
	decl := declaration.AsVariableDeclaration()

	var value luau.Expression
	if decl.Initializer != nil {
		statements.PushList(s.CaptureStatements(func() {
			value = TransformExpression(s, decl.Initializer)
		}))
	}

	name := decl.Name()
	if ast.IsIdentifier(name) {
		statements.PushList(s.CaptureStatements(func() {
			transformVariable(s, name, value)
		}))
	} else {
		// in destructuring, rhs must be executed first
		if decl.Initializer == nil || value == nil {
			panic("transformer: transformVariableDeclaration: binding pattern without initializer") // upstream assert
		}

		// optimize empty destructure — only the RHS side effects remain
		if len(name.AsBindingPattern().Elements.Nodes) == 0 {
			if array, ok := value.(*luau.Array); !ok || array.Members.IsNonEmpty() {
				statements.PushList(wrapExpressionStatement(value))
			}
			return statements
		}

		if ast.IsArrayBindingPattern(name) {
			array, isArray := value.(*luau.Array)
			if luau.IsCall(value) &&
				IsLuaTupleType(s).Check(s.GetType(decl.Initializer)) &&
				!arrayBindingPatternContainsHoists(s, name) &&
				!arrayLikeExpressionContainsSpread(name) {
				statements.PushList(transformOptimizedArrayBindingPattern(s, name, value))
			} else if isArray && array.Members.IsNonEmpty() &&
				// we can't localize multiple variables at the same time if any of them are hoisted
				!arrayBindingPatternContainsHoists(s, name) &&
				!arrayLikeExpressionContainsSpread(name) {
				statements.PushList(transformOptimizedArrayBindingPattern(s, name, array.Members))
			} else {
				statements.PushList(s.CaptureStatements(func() {
					transformArrayBindingPattern(s, name, getTargetIDForBindingPattern(s, name, value))
				}))
			}
		} else {
			statements.PushList(s.CaptureStatements(func() {
				transformObjectBindingPattern(s, name, getTargetIDForBindingPattern(s, name, value))
			}))
		}
	}

	return statements
}

// isVarDeclaration ports transformVariableStatement.ts isVarDeclaration
// (L169-171): neither Const nor Let flag.
func isVarDeclaration(declarationList *ast.Node) bool {
	return declarationList.Flags&ast.NodeFlagsConst == 0 && declarationList.Flags&ast.NodeFlagsLet == 0
}

// transformVariableDeclarationList ports transformVariableStatement.ts
// transformVariableDeclarationList (L173-189): `var` diagnostic, then each
// declaration's prereqs and statements in order.
func transformVariableDeclarationList(s *State, declarationList *ast.Node) *luau.List[luau.Statement] {
	if isVarDeclaration(declarationList) {
		s.Diags.Add(DiagNoVar(declarationList))
	}

	statements := luau.NewList[luau.Statement]()
	for _, declaration := range declarationList.AsVariableDeclarationList().Declarations.Nodes {
		var variableStatements *luau.List[luau.Statement]
		prereqs := s.CaptureStatements(func() {
			variableStatements = transformVariableDeclaration(s, declaration)
		})
		statements.PushList(prereqs)
		statements.PushList(variableStatements)
	}

	return statements
}

// transformVariableStatement ports transformVariableStatement.ts
// transformVariableStatement (L191-196).
func transformVariableStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	if declaration := transformMatchingNamedFunctionConst(s, node); declaration != nil {
		return luau.NewList[luau.Statement](declaration)
	}
	return transformVariableDeclarationList(s, node.AsVariableStatement().DeclarationList)
}

// ---------------------------------------------------------------------------
// Expression statements — statements/transformExpressionStatement.ts
// ---------------------------------------------------------------------------

// transformExpressionStatement ports transformExpressionStatement (L86-89).
func transformExpressionStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	expression := SkipDownwards(node.AsExpressionStatement().Expression)
	return transformExpressionStatementInner(s, expression)
}

// transformUnaryExpressionStatement ports transformExpressionStatement.ts
// transformUnaryExpressionStatement (L13-24): ++/-- as a whole statement
// needs no temp — `transformWritableExpression(operand, readAfterWrite =
// false)` plus a single `+= 1` / `-= 1` assignment.
func transformUnaryExpressionStatement(s *State, expression *ast.Node) luau.Statement {
	var operand *ast.Node
	var operatorKind ast.Kind
	if ast.IsPrefixUnaryExpression(expression) {
		unary := expression.AsPrefixUnaryExpression()
		operand, operatorKind = unary.Operand, unary.Operator
	} else {
		unary := expression.AsPostfixUnaryExpression()
		operand, operatorKind = unary.Operand, unary.Operator
	}

	writable := transformWritableExpression(s, operand, false)
	operator := luau.AssignmentOperator("+=")
	if operatorKind == ast.KindMinusMinusToken {
		operator = "-="
	}
	return luau.NewAssignment(writable, operator, luau.Num(1))
}

// isUnaryAssignmentOperator ports typeGuards.ts isUnaryAssignmentOperator
// (L13-17).
func isUnaryAssignmentOperator(operator ast.Kind) bool {
	return operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken
}

// transformExpressionStatementInner ports transformExpressionStatementInner
// (L26-84): the statement-position specializations for assignments and
// ++/-- (no value temps), with wrapExpressionStatement discard semantics for
// everything else. Destructuring-assignment LHS falls through to
// transformBinaryExpression (upstream routes it the same way), whose
// destructuring branch prereqs the statements and returns a dropped value.
func transformExpressionStatementInner(s *State, expression *ast.Node) *luau.List[luau.Statement] {
	if ast.IsBinaryExpression(expression) {
		binary := expression.AsBinaryExpression()
		operatorKind := binary.OperatorToken.Kind
		if ast.IsLogicalOrCoalescingAssignmentExpression(expression) {
			return transformLogicalOrCoalescingAssignmentExpressionStatement(s, expression)
		} else if ast.IsAssignmentOperator(operatorKind) &&
			!ast.IsArrayLiteralExpression(binary.Left) &&
			!ast.IsObjectLiteralExpression(binary.Left) {
			writableType := s.GetType(binary.Left)
			valueType := s.GetType(binary.Right)
			operator, isSimple := getSimpleAssignmentOperator(s, writableType, operatorKind, valueType)
			// NOTE both flags are false for simple operators: statement
			// position never re-reads the target (compare the expression form
			// in transformBinaryExpression, which passes true, !isSimple).
			assignment := transformWritableAssignment(s, binary.Left, binary.Right, !isSimple, !isSimple)
			if isSimple {
				return luau.NewList[luau.Statement](luau.NewAssignment(
					assignment.writable,
					operator,
					getAssignableValue(s, operator, assignment.value, valueType),
				))
			}
			return luau.NewList[luau.Statement](createCompoundAssignmentStatement(
				s, assignment.writable, writableType, assignment.readable, operatorKind, assignment.value, valueType,
			))
		}
	} else if (ast.IsPrefixUnaryExpression(expression) && isUnaryAssignmentOperator(expression.AsPrefixUnaryExpression().Operator)) ||
		(ast.IsPostfixUnaryExpression(expression) && isUnaryAssignmentOperator(expression.AsPostfixUnaryExpression().Operator)) {
		return luau.NewList[luau.Statement](transformUnaryExpressionStatement(s, expression))
	}

	return wrapExpressionStatement(TransformExpression(s, expression))
}

// ---------------------------------------------------------------------------
// If statements — statements/transformIfStatement.ts
// ---------------------------------------------------------------------------

// getStatements ports util/getStatements.ts: a Block's statement list, or the
// single statement itself.
func getStatements(statement *ast.Node) []*ast.Node {
	if ast.IsBlock(statement) {
		return statement.AsBlock().Statements.Nodes
	}
	return []*ast.Node{statement}
}

// transformIfStatementInner ports transformIfStatement.ts
// transformIfStatementInner (L9-38): truthiness-wrapped condition, statement
// lists for both branches. An `else if` whose transform produced prereqs
// cannot live in an `elseif` clause, so the elseBody becomes a statement list
// `[...prereqs, IfStatement]` (rendering as `else` + nested `if`); otherwise
// the IfStatement attaches directly (renders as `elseif`).
func transformIfStatementInner(s *State, node *ast.Node) *luau.IfStatement {
	statement := node.AsIfStatement()
	condition := CreateTruthinessChecks(s, TransformExpression(s, statement.Expression), statement.Expression, s.GetType(statement.Expression))

	statements := TransformStatementList(s, statement.ThenStatement, getStatements(statement.ThenStatement), nil)

	elseStatement := statement.ElseStatement

	var elseBody luau.NodeOrList
	if elseStatement == nil {
		elseBody = luau.NewList[luau.Statement]()
	} else if ast.IsIfStatement(elseStatement) {
		var elseIf *luau.IfStatement
		elseIfPrereqs := s.CaptureStatements(func() {
			elseIf = transformIfStatementInner(s, elseStatement)
		})
		if elseIfPrereqs.IsEmpty() {
			elseBody = elseIf
		} else {
			elseIfStatements := luau.NewList[luau.Statement]()
			elseIfStatements.PushList(elseIfPrereqs)
			elseIfStatements.Push(elseIf)
			elseBody = elseIfStatements
		}
	} else {
		elseBody = TransformStatementList(s, elseStatement, getStatements(elseStatement), nil)
	}

	return luau.NewIf(condition, statements, elseBody)
}

// transformIfStatement ports transformIfStatement.ts transformIfStatement
// (L40-42).
func transformIfStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	return luau.NewList[luau.Statement](transformIfStatementInner(s, node))
}

// wrapExpressionStatement ports util/wrapExpressionStatement.ts: an
// expression in statement position is dropped if a temp/none, kept as a call
// statement if a call, otherwise bound to a discarded `local _ = <exp>`.
func wrapExpressionStatement(expression luau.Expression) *luau.List[luau.Statement] {
	switch expression.Kind() {
	case luau.KindTemporaryIdentifier, luau.KindNone:
		return luau.NewList[luau.Statement]()
	}
	if luau.IsCall(expression) {
		return luau.NewList[luau.Statement](luau.NewCallStatement(expression))
	}
	return luau.NewList[luau.Statement](luau.NewVariableDeclaration(luau.TempID(""), expression))
}

// ---------------------------------------------------------------------------
// Blocks — statements/transformBlock.ts
// ---------------------------------------------------------------------------

// transformBlock ports transformBlock.ts (L6-12): a free-standing `{ ... }`
// becomes `do ... end`, preserving scoping.
func transformBlock(s *State, node *ast.Node) *luau.List[luau.Statement] {
	return luau.NewList[luau.Statement](luau.NewDo(
		TransformStatementList(s, node, node.AsBlock().Statements.Nodes, nil),
	))
}

// ---------------------------------------------------------------------------
// Return statements — statements/transformReturnStatement.ts
// ---------------------------------------------------------------------------

// isTupleReturningCall ports transformReturnStatement.ts isTupleReturningCall
// (L10-16): a call expression whose own type (at the call site, intentionally
// NOT via s.GetType which uses SkipUpwards) is a LuaTuple — its multi-returns
// propagate through `return` unchanged.
func isTupleReturningCall(s *State, tsExpression *ast.Node, luaExpression luau.Expression) bool {
	return luau.IsCall(luaExpression) &&
		IsLuaTupleType(s).Check(s.Checker.GetTypeAtLocation(SkipDownwards(tsExpression)))
}

// isTupleMacro ports transformReturnStatement.ts isTupleMacro (L18-26): the
// return expression is a direct `$tuple(...)` call (its callee's type symbol
// is THE registered global $tuple function symbol).
func isTupleMacro(s *State, expression *ast.Node) bool {
	if ast.IsCallExpression(expression) {
		symbol := GetFirstDefinedSymbol(s, s.GetType(expression.AsCallExpression().Expression))
		if tupleSymbol := s.Macros().Symbol("$tuple"); symbol != nil && symbol == tupleSymbol {
			return true
		}
	}
	return false
}

// transformReturnStatementInner ports transformReturnStatementInner (L28-69).
// A direct `return $tuple(...)` spreads its arguments into a multi-value
// return (intercepted BEFORE the expression transform — outside return
// position the $tuple CALL macro raises noTupleMacroOutsideReturn instead).
// Returning a LuaTuple VALUE (array literal or variable) must likewise spread
// — array members inline (`return a, b`), anything else through
// `return unpack(exp)`. A return blocked by a try statement (L51-63) reroutes
// as `return TS.TRY_RETURN, { <value(s)> }` — the values are ALWAYS boxed in
// an array literal (LuaTuple multi-returns spread as array MEMBERS); the
// unbox happens at the consumer (transformFlowControl).
func transformReturnStatementInner(s *State, returnExp *ast.Node) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()

	var expression luau.NodeOrList
	if ast.IsCallExpression(returnExp) && isTupleMacro(s, returnExp) {
		var args []luau.Expression
		prereqs := s.CaptureStatements(func() {
			args = ensureTransformOrder(s, returnExp.Arguments())
		})
		result.PushList(prereqs)
		expression = luau.NewList(args...)
	} else {
		expression = TransformExpression(s, SkipDownwards(returnExp))
		if IsLuaTupleType(s).Check(s.GetType(returnExp)) &&
			!isTupleReturningCall(s, returnExp, expression.(luau.Expression)) {
			if array, ok := expression.(*luau.Array); ok {
				expression = array.Members
			} else {
				expression = luau.NewCall(luau.GlobalID("unpack"),
					luau.NewList[luau.Expression](expression.(luau.Expression)))
			}
		}
	}

	if isReturnBlockedByTryStatement(returnExp) {
		s.MarkTryUsesReturn()
		var members *luau.List[luau.Expression]
		if list, ok := expression.(*luau.List[luau.Expression]); ok {
			members = list
		} else {
			members = luau.NewList[luau.Expression](expression.(luau.Expression))
		}
		result.Push(luau.NewReturn(luau.NewList[luau.Expression](
			s.RuntimeLib(returnExp, "TRY_RETURN"),
			luau.NewArray(members),
		)))
	} else {
		result.Push(luau.NewReturn(expression))
	}
	return result
}

// transformReturnStatement ports transformReturnStatement (L71-84): a bare
// `return` emits an explicit `return nil` (preserving JS `undefined`), never
// a bare Luau `return` — unless blocked by a try statement, in which case it
// reroutes as `return TS.TRY_RETURN, {}`.
func transformReturnStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	expression := node.AsReturnStatement().Expression
	if expression == nil {
		if isReturnBlockedByTryStatement(node) {
			s.MarkTryUsesReturn()
			return luau.NewList[luau.Statement](luau.NewReturn(luau.NewList[luau.Expression](
				s.RuntimeLib(node, "TRY_RETURN"),
				luau.NewArray(luau.NewList[luau.Expression]()),
			)))
		}
		return luau.NewList[luau.Statement](luau.NewReturn(luau.Nil()))
	}
	return transformReturnStatementInner(s, expression)
}

// ---------------------------------------------------------------------------
// Break / continue — statements/transformBreakStatement.ts,
// transformContinueStatement.ts
// ---------------------------------------------------------------------------

// transformBreakStatement ports transformBreakStatement.ts (L8-25). A break
// blocked by a try statement (nearest-of try vs loop-or-switch) reroutes as
// `return TS.TRY_BREAK` and marks the enclosing try's TryUses.
//
// ROTOR EXTENSION: `break <label>` targeting the innermost break boundary is a
// plain `break`; targeting anything further out sets the label's flag first,
// and the checks emitted after each enclosing boundary keep unwinding
// (labeledstatement.go, State.LabelChecks). A label crossing a try is rejected
// — the TS.TRY_BREAK sentinel protocol carries no label.
func transformBreakStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	blockedByTry := isBreakBlockedByTryStatement(node)

	if label := node.AsBreakStatement().Label; label != nil {
		if blockedByTry {
			s.Diags.Add(DiagNoLabeledStatementsWithinTryCatch(label))
			return luau.NewList[luau.Statement]()
		}
		entry := s.FindLabel(label.Text())
		if entry == nil {
			s.Diags.Add(DiagRotorUnknownLabel(label))
			return luau.NewList[luau.Statement]()
		}
		if entry.Depth != s.BreakDepth() {
			entry.EverBroken = true
			s.Prereq(luau.NewAssignment(entry.ID, "=", luau.Str(labelBreak)))
		}
		return luau.NewList[luau.Statement](luau.NewBreak())
	}

	if blockedByTry {
		s.MarkTryUsesBreak()
		return luau.NewList[luau.Statement](luau.NewReturn(s.RuntimeLib(node, "TRY_BREAK")))
	}
	return luau.NewList[luau.Statement](luau.NewBreak())
}

// transformContinueStatement ports transformContinueStatement.ts (L8-25):
// identical shape — Luau has native `continue`; the try-blocked reroute emits
// `return TS.TRY_CONTINUE`.
//
// ROTOR EXTENSION: `continue <label>` on the innermost boundary is a plain
// `continue`; otherwise the flag is set and a plain `break` leaves the
// innermost boundary, after which the check emitted inside the labeled loop
// does the `continue`.
func transformContinueStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	blockedByTry := isBreakBlockedByTryStatement(node)

	if label := node.AsContinueStatement().Label; label != nil {
		if blockedByTry {
			s.Diags.Add(DiagNoLabeledStatementsWithinTryCatch(label))
			return luau.NewList[luau.Statement]()
		}
		entry := s.FindLabel(label.Text())
		if entry == nil {
			s.Diags.Add(DiagRotorUnknownLabel(label))
			return luau.NewList[luau.Statement]()
		}
		if entry.Depth == s.BreakDepth() && entry.IsLoop {
			return luau.NewList[luau.Statement](luau.NewContinue())
		}
		entry.EverContinued = true
		s.Prereq(luau.NewAssignment(entry.ID, "=", luau.Str(labelContinue)))
		return luau.NewList[luau.Statement](luau.NewBreak())
	}

	if blockedByTry {
		s.MarkTryUsesContinue()
		return luau.NewList[luau.Statement](luau.NewReturn(s.RuntimeLib(node, "TRY_CONTINUE")))
	}
	return luau.NewList[luau.Statement](luau.NewContinue())
}

// ---------------------------------------------------------------------------
// Throw statements — statements/transformThrowStatement.ts
// ---------------------------------------------------------------------------

// transformThrowStatement ports transformThrowStatement.ts (L6-16):
// `throw x` -> `error(x)`; a (grammatically impossible in modern TS) bare
// `throw` would emit `error()`. No type checks, no validateNotAny upstream.
func transformThrowStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	args := luau.NewList[luau.Expression]()
	if expression := node.AsThrowStatement().Expression; expression != nil {
		args.Push(TransformExpression(s, expression))
	}
	return luau.NewList[luau.Statement](luau.NewCallStatement(
		luau.NewCall(luau.GlobalID("error"), args),
	))
}
