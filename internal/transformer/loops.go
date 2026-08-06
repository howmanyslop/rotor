package transformer

import (
	"math"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
	"rotor/tsgo/jsnum"
)

// ---------------------------------------------------------------------------
// While statements — statements/transformWhileStatement.ts
// ---------------------------------------------------------------------------

// transformWhileStatement ports transformWhileStatement.ts (L9-37). A
// condition whose transform produced prereqs must be re-evaluated every
// iteration, so the prereqs move into the loop body followed by
// `if not cond then break end`, and the while condition becomes `true`.
func transformWhileStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	statement := node.AsWhileStatement()
	s.EnterBreakScope()
	defer s.ExitBreakScope()
	whileStatements := luau.NewList[luau.Statement]()

	conditionExp, conditionPrereqs := s.Capture(func() luau.Expression {
		return CreateTruthinessChecks(s,
			TransformExpression(s, statement.Expression), statement.Expression, s.GetType(statement.Expression))
	})

	if !conditionPrereqs.IsEmpty() {
		whileStatements.PushList(conditionPrereqs)
		whileStatements.Push(luau.NewIf(
			luau.NewUnary("not", conditionExp),
			luau.NewList[luau.Statement](luau.NewBreak()),
			nil,
		))
		conditionExp = luau.Bool(true)
	}

	whileStatements.PushList(TransformStatementList(s, statement.Statement, getStatements(statement.Statement), nil))

	// Label bookkeeping (rotor extension): the resets go above the hoisted
	// condition prereqs so a labeled `continue` reaching this loop starts the
	// iteration with a cleared flag.
	whileStatements.UnshiftList(s.LabelResets())

	result := luau.NewList[luau.Statement](luau.NewWhile(conditionExp, whileStatements))
	result.PushList(s.LabelChecks())
	return result
}

// ---------------------------------------------------------------------------
// Do statements — statements/transformDoStatement.ts (do/while)
// ---------------------------------------------------------------------------

// transformDoStatement ports transformDoStatement.ts (L9-37). The body is
// wrapped in an inner `do ... end` so its locals cannot collide with the
// condition (repeat..until conditions can see body locals in Luau). Inversion
// micro-opt: `do {} while (!x)` strips the `!` and skips the `not` wrap,
// since repeat..until exits when the condition is true while do/while
// continues when true (the two inversions cancel).
func transformDoStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	doStatement := node.AsDoStatement()
	expression := doStatement.Expression
	s.EnterBreakScope()
	defer s.ExitBreakScope()
	statements := TransformStatementList(s, doStatement.Statement, getStatements(doStatement.Statement), nil)

	conditionIsInvertedInLuau := true
	if ast.IsPrefixUnaryExpression(expression) && expression.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
		expression = expression.AsPrefixUnaryExpression().Operand
		conditionIsInvertedInLuau = false
	}

	condition, conditionPrereqs := s.Capture(func() luau.Expression {
		return CreateTruthinessChecks(s, TransformExpression(s, expression), expression, s.GetType(expression))
	})

	conditionNeedsPrereqs := conditionPrereqs.IsNonEmpty()

	repeatStatements := luau.NewList[luau.Statement]()
	repeatStatements.Push(luau.NewDo(statements))
	repeatStatements.PushList(conditionPrereqs)
	// Above the inner `do ... end`, so a labeled `continue` reaching this loop
	// clears the flag before the body runs again. A non-empty reset list also
	// means an inner boundary emitted a `continue` inside this repeat, which
	// Luau rejects when the condition declared locals after it.
	resets := s.LabelResets()
	if resets.IsNonEmpty() && conditionNeedsPrereqs {
		s.Diags.Add(DiagRotorLabeledContinueInDoWhile(node))
	}
	repeatStatements.UnshiftList(resets)

	if conditionIsInvertedInLuau {
		condition = luau.NewUnary("not", condition)
	}

	result := luau.NewList[luau.Statement](luau.NewRepeat(condition, repeatStatements))
	result.PushList(s.LabelChecks())
	return result
}

// ---------------------------------------------------------------------------
// C-style for statements — statements/transformForStatement.ts
// ---------------------------------------------------------------------------

// getDeclaredVariablesFromBindingName ports util/getDeclaredVariables.ts
// getDeclaredVariablesFromBindingName (L3-17): flatten every declared
// identifier out of a binding name, recursing object/array binding patterns
// and skipping omitted array elements.
func getDeclaredVariablesFromBindingName(node *ast.Node, list *[]*ast.Node) {
	if ast.IsIdentifier(node) {
		*list = append(*list, node)
	} else if ast.IsObjectBindingPattern(node) {
		for _, element := range node.AsBindingPattern().Elements.Nodes {
			getDeclaredVariablesFromBindingName(element.Name(), list)
		}
	} else if ast.IsArrayBindingPattern(node) {
		for _, element := range node.AsBindingPattern().Elements.Nodes {
			if !ast.IsOmittedExpression(element) {
				getDeclaredVariablesFromBindingName(element.Name(), list)
			}
		}
	}
}

// getDeclaredVariables ports util/getDeclaredVariables.ts (L19-29) for the
// VariableDeclarationList input shape transformForStatementFallback uses.
func getDeclaredVariables(declarationList *ast.Node) []*ast.Node {
	list := []*ast.Node{}
	for _, declaration := range declarationList.AsVariableDeclarationList().Declarations.Nodes {
		getDeclaredVariablesFromBindingName(declaration.AsVariableDeclaration().Name(), &list)
	}
	return list
}

// canSkipClone ports transformForStatement.ts canSkipClone (L73-76): when the
// loop variable is not referenced inside its own declaration list (`let i =
// 0`, unlike `let i = 0, j = i`), the outer loop-carried slot can BE the
// declared temp — no separate `_iCopy` indirection is needed.
func canSkipClone(s *State, initializer *ast.Node, id *ast.Node) bool {
	// is symbol used in initializer (besides its definition)
	return !IsSymbolReferenced(s.Checker, id, initializer)
}

// isIdWriteOrAsyncRead ports transformForStatement.ts isIdWriteOrAsyncRead
// (L78-100): does this loop variable need the per-iteration copy treatment?
// True when any reference inside the ForStatement is (a) a write anywhere
// except (solely) inside the incrementor, or (b) a read from inside a
// function-like nested in the loop (closure capture — the "async read").
func isIdWriteOrAsyncRead(s *State, forStatementNode *ast.Node, id *ast.Node) bool {
	incrementor := forStatementNode.AsForStatement().Incrementor
	return ForEachSymbolReference(s.Checker, id, forStatementNode, func(token *ast.Node) bool {
		// write
		if ast.IsWriteAccess(token) && (incrementor == nil || !isAncestorOf(incrementor, token)) {
			return true
		}

		// async read
		ancestor := ast.FindAncestor(token, func(v *ast.Node) bool {
			return v == forStatementNode || ast.IsFunctionLike(v)
		})
		if ancestor != nil && ancestor != forStatementNode {
			return true
		}
		return false
	})
}

// addFinalizersToIfStatement ports transformForStatement.ts
// addFinalizersToIfStatement (L22-33): recurse into both branches, following
// else-if chains.
func addFinalizersToIfStatement(node *luau.IfStatement, finalizers *luau.List[luau.Statement]) {
	if node.Statements.IsNonEmpty() {
		addFinalizers(node.Statements, node.Statements.Head, finalizers)
	}
	if elseList, ok := node.ElseBody.(*luau.List[luau.Statement]); ok {
		if elseList.IsNonEmpty() {
			addFinalizers(elseList, elseList.Head, finalizers)
		}
	} else if elseIf, ok := node.ElseBody.(*luau.IfStatement); ok {
		addFinalizersToIfStatement(elseIf, finalizers)
	}
}

// addFinalizers ports transformForStatement.ts addFinalizers (L35-71): walk
// the emitted Luau statement list, splicing a CLONE of the finalizer list
// (`_i = i` write-backs) before every ContinueStatement so `continue` carries
// the current iteration's values into the next condition test. Recurses into
// DoStatement bodies and IfStatement branches but NOT into nested loops —
// their `continue` targets the inner loop, which has its own finalizers (or
// none). Parent fixups mirror upstream L47; upstream leaves the clone head's
// prev pointer dangling (only forward links are repaired) — ported verbatim,
// rendering only walks forward.
func addFinalizers(list *luau.List[luau.Statement], node *luau.ListNode[luau.Statement], finalizers *luau.List[luau.Statement]) {
	if list.IsEmpty() {
		panic("transformer: addFinalizers on empty list") // upstream assert
	}

	statement := node.Value
	if _, ok := statement.(*luau.ContinueStatement); ok {
		finalizersClone := finalizers.Clone()

		// fix node parents
		finalizersClone.ForEach(func(stmt luau.Statement) { luau.SetParent(stmt, statement.Parent()) })

		if node.Prev != nil {
			node.Prev.Next = finalizersClone.Head
		} else if node == list.Head {
			list.Head = finalizersClone.Head
		}

		node.Prev = finalizersClone.Tail

		finalizersClone.Tail.Next = node
	}

	if doStatement, ok := statement.(*luau.DoStatement); ok {
		if doStatement.Statements.IsNonEmpty() {
			addFinalizers(doStatement.Statements, doStatement.Statements.Head, finalizers)
		}
	} else if ifStatement, ok := statement.(*luau.IfStatement); ok {
		addFinalizersToIfStatement(ifStatement, finalizers)
	}

	if node.Next != nil {
		addFinalizers(list, node.Next, finalizers)
	}
}

// transformForStatementFallback ports transformForStatementFallback
// (L102-297), the fully general while-loop desugaring, including the
// per-iteration let-capture copy machinery: loop variables that are written
// in the body or captured by a closure get an outer loop-carried slot
// (`local _i = 0`), a per-iteration copy at the body head (`local i = _i`),
// and `_i = i` finalizers at the body tail and before every `continue`, so
// closures capture each iteration's binding (TS per-iteration `let`
// semantics) and writes survive into the next condition test.
func transformForStatementFallback(s *State, node *ast.Node) *luau.List[luau.Statement] {
	forStatement := node.AsForStatement()
	initializer, condition, incrementor := forStatement.Initializer, forStatement.Condition, forStatement.Incrementor
	statement := forStatement.Statement

	result := luau.NewList[luau.Statement]()
	whileStatements := luau.NewList[luau.Statement]()
	finalizerStatements := luau.NewList[luau.Statement]()

	// Reference analyses (L109-124): which declared variables need copies,
	// and which can collapse the Copy indirection.
	var variables []*ast.Node
	hasWriteOrAsyncRead := map[*ast.Symbol]bool{}
	skipClone := map[*ast.Symbol]bool{}
	symbolOf := func(id *ast.Node) *ast.Symbol {
		symbol := s.Checker.GetSymbolAtLocation(id)
		if symbol == nil {
			panic("transformer: transformForStatementFallback: no symbol") // upstream assert
		}
		return symbol
	}

	if initializer != nil && ast.IsVariableDeclarationList(initializer) {
		variables = getDeclaredVariables(initializer)
		for _, id := range variables {
			symbol := symbolOf(id)
			if isIdWriteOrAsyncRead(s, node, id) {
				hasWriteOrAsyncRead[symbol] = true
			}
			if canSkipClone(s, initializer, id) {
				skipClone[symbol] = true
			}
		}
	}

	if initializer != nil {
		if ast.IsVariableDeclarationList(initializer) {
			if isVarDeclaration(initializer) {
				s.Diags.Add(DiagNoVar(node))
			}

			// Pre-bind needs-copy symbols to temp ids (L132-143) so the
			// declaration transform below emits `local _i = 0` (skipClone) or
			// `local _iCopy = 0` instead of `local i = 0` —
			// transformIdentifierDefined consults SymbolToID.
			for _, id := range variables {
				symbol := symbolOf(id)
				if hasWriteOrAsyncRead[symbol] {
					if skipClone[symbol] {
						s.SymbolToID[symbol] = luau.TempID(id.Text())
					} else {
						s.SymbolToID[symbol] = luau.TempID(id.Text() + "Copy")
					}
				}
			}

			// transformVariableDeclaration per declaration (L145-157): each
			// declaration's prereqs land before its statements.
			for _, declaration := range initializer.AsVariableDeclarationList().Declarations.Nodes {
				var decStatements *luau.List[luau.Statement]
				decPrereqs := s.CaptureStatements(func() {
					decStatements = transformVariableDeclaration(s, declaration)
				})
				result.PushList(decPrereqs)
				result.PushList(decStatements)
			}

			// Copies + finalizers per needs-copy symbol (L159-203).
			for _, id := range variables {
				symbol := symbolOf(id)
				if !hasWriteOrAsyncRead[symbol] {
					continue
				}
				var tempID *luau.TemporaryIdentifier
				if skipClone[symbol] {
					tempID = s.SymbolToID[symbol]
					if tempID == nil {
						panic("transformer: transformForStatementFallback: missing temp id") // upstream assert
					}
				} else {
					tempID = luau.TempID(id.Text())
					copyID := s.SymbolToID[symbol]
					if copyID == nil {
						panic("transformer: transformForStatementFallback: missing copy id") // upstream assert
					}

					// local _i = _iCopy
					result.Push(luau.NewVariableDeclaration(tempID, copyID))
				}
				delete(s.SymbolToID, symbol)
				realID := TransformIdentifierDefined(s, id)

				// local i = _i
				whileStatements.Push(luau.NewVariableDeclaration(realID, tempID))

				// _i = i
				finalizerStatements.Push(luau.NewAssignment(tempID, "=", realID))
			}
		} else {
			// Expression initializer (L204-208).
			var statements *luau.List[luau.Statement]
			prereqs := s.CaptureStatements(func() {
				statements = transformExpressionStatementInner(s, initializer)
			})
			result.PushList(prereqs)
			result.PushList(statements)
		}
	}

	// Incrementor (L211-247): guarded so it runs at the TOP of every
	// iteration except the first — `continue` still triggers the increment,
	// since Luau `continue` jumps to the loop top.
	if incrementor != nil {
		shouldIncrement := luau.TempID("shouldIncrement")

		// local _shouldIncrement = false
		result.Push(luau.NewVariableDeclaration(shouldIncrement, luau.Bool(false)))

		incrementorStatements := luau.NewList[luau.Statement]()
		var statements *luau.List[luau.Statement]
		prereqs := s.CaptureStatements(func() {
			statements = transformExpressionStatementInner(s, incrementor)
		})
		incrementorStatements.PushList(prereqs)
		incrementorStatements.PushList(statements)

		// if _shouldIncrement then
		// 	[incrementorStatements]
		// else
		// 	_shouldIncrement = true
		// end
		whileStatements.Push(luau.NewIf(
			shouldIncrement,
			incrementorStatements,
			luau.NewList[luau.Statement](luau.NewAssignment(shouldIncrement, "=", luau.Bool(true))),
		))
	}

	// Condition (L249-274): if ANY whileStatements precede the body
	// (incrementor guard or condition prereqs), the condition must be
	// evaluated after them — `if not cond then break end` and the while
	// condition becomes `true`.
	conditionExp, conditionPrereqs := s.Capture(func() luau.Expression {
		if condition != nil {
			return CreateTruthinessChecks(s, TransformExpression(s, condition), condition, s.GetType(condition))
		}
		return luau.Bool(true)
	})

	whileStatements.PushList(conditionPrereqs)

	if !whileStatements.IsEmpty() {
		if condition != nil {
			// if not [conditionExp] then
			//	break
			// end
			whileStatements.Push(luau.NewIf(
				luau.NewUnary("not", conditionExp),
				luau.NewList[luau.Statement](luau.NewBreak()),
				nil,
			))
		}
		conditionExp = luau.Bool(true)
	}

	whileStatements.PushList(TransformStatementList(s, statement, getStatements(statement), nil))

	// Finalizer placement (L278-284): splice clones before every `continue`
	// (without descending into nested loops), then append at the tail unless
	// the body already ends in a final statement (return/break/continue —
	// the continue case got its splice above).
	if whileStatements.IsNonEmpty() && finalizerStatements.IsNonEmpty() {
		addFinalizers(whileStatements, whileStatements.Head, finalizerStatements)
	}

	if whileStatements.Tail == nil || !luau.IsFinalStatement(whileStatements.Tail.Value) {
		whileStatements.PushList(finalizerStatements)
	}

	// Label resets go at the very top, above the incrementor guard: a labeled
	// `continue` reaching this loop lands there like any other `continue`.
	whileStatements.UnshiftList(s.LabelResets())

	result.Push(luau.NewWhile(conditionExp, whileStatements))

	// Assembly (L286-296): multiple statements wrap in `do ... end` to scope
	// the loop variable declarations.
	if result.Head == result.Tail {
		return result
	}
	return luau.NewList[luau.Statement](luau.NewDo(result))
}

// getOptimizedIncrementorStepValue ports getOptimizedIncrementorStepValue
// (L299-332): `i += intLit` / `i -= intLit` / `i++` / `i--` yield the step
// value; anything else disqualifies the optimization. Upstream quirk ported
// faithfully: the `-=` branch (L309-315) never checks that the left side is
// the loop variable, unlike the `+=` branch.
func getOptimizedIncrementorStepValue(s *State, incrementor *ast.Node, idSymbol *ast.Symbol) (float64, bool) {
	if ast.IsBinaryExpression(incrementor) {
		binary := incrementor.AsBinaryExpression()
		if ast.IsIdentifier(binary.Left) &&
			s.Checker.GetSymbolAtLocation(binary.Left) == idSymbol &&
			binary.OperatorToken.Kind == ast.KindPlusEqualsToken &&
			ast.IsNumericLiteral(binary.Right) &&
			isProbablyInteger(s, binary.Right) {
			value, err := luau.JSNumberParse(getText(s, binary.Right))
			return value, err == nil
		} else if binary.OperatorToken.Kind == ast.KindMinusEqualsToken &&
			ast.IsNumericLiteral(binary.Right) &&
			isProbablyInteger(s, binary.Right) {
			value, err := luau.JSNumberParse(getText(s, binary.Right))
			return -value, err == nil
		}
	} else if ast.IsPostfixUnaryExpression(incrementor) || ast.IsPrefixUnaryExpression(incrementor) {
		operand, operator := unaryOperandAndOperator(incrementor)
		if ast.IsIdentifier(operand) && s.Checker.GetSymbolAtLocation(operand) == idSymbol {
			if operator == ast.KindPlusPlusToken {
				return 1, true
			} else if operator == ast.KindMinusMinusToken {
				return -1, true
			}
		}
	}
	return 0, false
}

// unaryOperandAndOperator extracts the operand/operator pair from either
// unary expression flavor.
func unaryOperandAndOperator(node *ast.Node) (*ast.Node, ast.Kind) {
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		return unary.Operand, unary.Operator
	}
	unary := node.AsPostfixUnaryExpression()
	return unary.Operand, unary.Operator
}

// isSizeMacro ports isSizeMacro (L334-346): a call whose callee symbol is the
// `size` property-call macro (e.g. `arr.size()`). Upstream:
// `macroManager.getPropertyCallMacro(symbol)` non-nil AND symbol.name ===
// "size".
func isSizeMacro(s *State, expression *ast.Node) bool {
	if ast.IsCallExpression(expression) {
		expType := s.Checker.GetNonOptionalType(s.GetType(expression.AsCallExpression().Expression))
		symbol := GetFirstDefinedSymbol(s, expType)
		if symbol != nil && symbol.Name == "size" && s.Macros().GetPropertyCallMacro(symbol) != nil {
			return true
		}
	}
	return false
}

// isUnaryExpressionWithWrite ports ts.isUnaryExpressionWithWrite: postfix
// unary (always ++/--) or prefix unary with ++/--.
func isUnaryExpressionWithWrite(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindPostfixUnaryExpression:
		return true
	case ast.KindPrefixUnaryExpression:
		operator := node.AsPrefixUnaryExpression().Operator
		return operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken
	}
	return false
}

// isMutatedInBody ports isMutatedInBody (L348-366): true when any reference
// to the loop variable inside the body is an assignment target or a ++/--
// operand.
func isMutatedInBody(s *State, identifier *ast.Node, body *ast.Node) bool {
	return ForEachSymbolReference(s.Checker, identifier, body, func(token *ast.Node) bool {
		parent := SkipUpwards(token).Parent
		if parent == nil {
			return false
		}
		if ast.IsAssignmentExpression(parent, false) && SkipDownwards(parent.AsBinaryExpression().Left) == token {
			return true
		}
		if isUnaryExpressionWithWrite(parent) {
			operand, _ := unaryOperandAndOperator(parent)
			if SkipDownwards(operand) == token {
				return true
			}
		}
		return false
	})
}

// isProbablyInteger ports isProbablyInteger (L368-390): integer numeric
// literal; `+ - * **` of two such; unary ± of one; a `.size()` macro call; or
// a checker type that is an integer number literal. NOTE the upstream chain
// shape: a binary/prefix-unary expression with a non-matching operator falls
// straight to false without consulting the checker.
func isProbablyInteger(s *State, expression *ast.Node) bool {
	if ast.IsNumericLiteral(expression) {
		value, err := luau.JSNumberParse(getText(s, expression))
		return err == nil && !math.IsInf(value, 0) && value == math.Trunc(value)
	} else if ast.IsBinaryExpression(expression) {
		binary := expression.AsBinaryExpression()
		switch binary.OperatorToken.Kind {
		case ast.KindPlusToken, ast.KindMinusToken, ast.KindAsteriskToken, ast.KindAsteriskAsteriskToken:
			return isProbablyInteger(s, binary.Left) && isProbablyInteger(s, binary.Right)
		}
	} else if ast.IsPrefixUnaryExpression(expression) {
		unary := expression.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusToken || unary.Operator == ast.KindMinusToken {
			return isProbablyInteger(s, unary.Operand)
		}
	} else if isSizeMacro(s, expression) {
		return true
	} else if IsDefinitelyType(s, s.GetType(expression), isIntegerLiteralTypeCheck) {
		return true
	}
	return false
}

// isIntegerLiteralTypeCheck ports the L386 callback:
// `t.isNumberLiteral() && Number.isInteger(t.value)`.
var isIntegerLiteralTypeCheck = TypeCheck{check: func(t *checker.Type) bool {
	if !t.IsNumberLiteral() {
		return false
	}
	value, ok := t.AsLiteralType().Value().(jsnum.Number)
	if !ok {
		return false
	}
	f := float64(value)
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f)
}}

// transformForStatementOptimized ports transformForStatementOptimized
// (L392-489): `for (let i = a; i < b; i += s)` (and <=, >, >= variants)
// becomes Luau `for i = a, b±1, s do`. Returns nil when any precondition
// fails: single identifier declaration with an isProbablyInteger initializer;
// incrementor with an extractable integer step; condition operator direction
// matching the step sign (`<`/`<=` need step >= 0, `>`/`>=` need step <= 0;
// `!==` etc. never optimize); isProbablyInteger condition RHS; loop variable
// not mutated in the body. Emitted bounds: `<` -> offset(end, -1), `>` ->
// offset(end, +1) — both constant-fold through offsetExpr when the bound is a
// literal (`i < 10` -> `9`) and stay symbolic otherwise (`i < limit` ->
// `limit - 1`); `<=`/`>=` use the bound as-is.
func transformForStatementOptimized(s *State, node *ast.Node) *luau.List[luau.Statement] {
	forStatement := node.AsForStatement()
	initializer, condition, incrementor := forStatement.Initializer, forStatement.Condition, forStatement.Incrementor
	statement := forStatement.Statement

	// validate initializer exists and is a single identifier `x` with a value
	// that is _probably_ an integer

	if initializer == nil || !ast.IsVariableDeclarationList(initializer) ||
		len(initializer.AsVariableDeclarationList().Declarations.Nodes) != 1 {
		return nil
	}

	declaration := initializer.AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	decName, decInit := declaration.Name(), declaration.Initializer
	if !ast.IsIdentifier(decName) || decInit == nil {
		return nil
	}

	idSymbol := s.Checker.GetSymbolAtLocation(decName)
	if idSymbol == nil {
		return nil
	}

	if !isProbablyInteger(s, decInit) {
		return nil
	}

	// validate incrementor exists and is _probably_ an integer change in `x`

	if incrementor == nil {
		return nil
	}

	stepValue, ok := getOptimizedIncrementorStepValue(s, incrementor, idSymbol)
	if !ok {
		return nil
	}

	// validate condition exists and is a BinaryExpression with an operator
	// that matches the incrementor

	if condition == nil || !ast.IsBinaryExpression(condition) {
		return nil
	}
	conditionBinary := condition.AsBinaryExpression()
	operatorKind := conditionBinary.OperatorToken.Kind

	switch operatorKind {
	case ast.KindLessThanToken, ast.KindLessThanEqualsToken:
		// do not optimize for cases which should never run like:
		// for (let i = 10; i < 0; i--)
		if stepValue < 0 {
			return nil
		}
	case ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken:
		// do not optimize for cases which should never run like:
		// for (let i = 0; i > 10; i++)
		if stepValue > 0 {
			return nil
		}
	default:
		// do not optimize for other comparison operators like !==, ===
		return nil
	}

	if !isProbablyInteger(s, conditionBinary.Right) {
		return nil
	}

	if isMutatedInBody(s, decName, statement) {
		return nil
	}

	// commit to the optimization and start transforming..

	result := luau.NewList[luau.Statement]()

	id := TransformIdentifierDefined(s, decName)

	start, startPrereqs := s.Capture(func() luau.Expression { return TransformExpression(s, decInit) })
	result.PushList(startPrereqs)

	end, endPrereqs := s.Capture(func() luau.Expression { return TransformExpression(s, conditionBinary.Right) })
	result.PushList(endPrereqs)

	step := luau.Num(stepValue)
	statements := TransformStatementList(s, statement, getStatements(statement), nil)
	statements.UnshiftList(s.LabelResets())

	if operatorKind == ast.KindLessThanToken {
		end = offsetExpr(end, -1)
	} else if operatorKind == ast.KindGreaterThanToken {
		end = offsetExpr(end, 1)
	}

	result.Push(luau.NewNumericFor(id, start, end, step, statements))

	return result
}

// transformForStatement ports transformForStatement (L491-499): the
// optimized pass is gated on `projectOptions.optimizedLoops` (default true),
// plumbed from `--optimizedLoops` through State.OptimizedLoops.
//
// The break scope wraps BOTH paths so the label bookkeeping stays balanced
// when the optimized pass bails out (it bails before transforming anything,
// so no label state leaks); the checks are appended to whichever list wins,
// which for the fallback is the outer `do ... end` wrap.
func transformForStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	s.EnterBreakScope()
	defer s.ExitBreakScope()

	var result *luau.List[luau.Statement]
	if s.OptimizedLoops {
		result = transformForStatementOptimized(s, node)
	}
	if result == nil {
		result = transformForStatementFallback(s, node)
	}
	result.PushList(s.LabelChecks())
	return result
}
