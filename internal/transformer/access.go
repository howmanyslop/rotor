package transformer

import (
	"math"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
	"rotor/tsgo/jsnum"
)

// This file ports expressions/transformPropertyAccessExpression.ts,
// expressions/transformElementAccessExpression.ts, and their utils:
// util/getConstantValueLiteral.ts, util/offset.ts, util/addOneIfArrayType.ts,
// util/addIndexDiagnostics.ts, util/isValidMethodIndexWithoutCall.ts, and
// util/validateNotAny.ts (+ util/getOriginalSymbolOfNode.ts).

// ---------------------------------------------------------------------------
// validateNotAny — util/validateNotAny.ts
// ---------------------------------------------------------------------------

// validateNotAnyType ports util/validateNotAny.ts (L9-37): `any`-typed
// expressions are a noAny error, deduped per symbol via the program-wide
// isReportedByNoAnyCache. Array types are unwrapped to their element type
// first so `any[]` is reported too.
func validateNotAnyType(s *State, node *ast.Node) {
	if ast.IsSpreadElement(node) {
		node = SkipDownwards(node.Expression())
	}

	t := s.GetType(node)

	if IsDefinitelyType(s, t, IsArrayType(s)) {
		// Array<T> -> T
		if indexType := s.Checker.GetNumberIndexType(t); indexType != nil {
			t = indexType
		}
	}

	if IsDefinitelyType(s, t, IsAnyType(s)) {
		// given a type like `a: { [index: string]: any }`, `a["b"]` will not have a symbol
		if symbol := getOriginalSymbolOfNode(s, node); symbol != nil {
			AddDiagnosticWithCache(s.Diags, symbol, DiagNoAny(node), s.Multi.IsReportedByNoAnyCache)
		} else {
			s.Diags.Add(DiagNoAny(node))
		}
	}
}

// ---------------------------------------------------------------------------
// offset — util/offset.ts
// ---------------------------------------------------------------------------

// getLiteralNumberValue ports offset.ts getLiteralNumberValue: number
// literals (and unary-minus-wrapped ones) fold to their JS numeric value.
func getLiteralNumberValue(expression luau.Expression) (float64, bool) {
	switch e := expression.(type) {
	case *luau.NumberLiteral:
		value, err := luau.JSNumberParse(e.Value)
		if err != nil {
			return 0, false
		}
		return value, true
	case *luau.UnaryExpression:
		if e.Operator == "-" {
			if inner, ok := getLiteralNumberValue(e.Expression); ok {
				return -inner, true
			}
		}
	}
	return 0, false
}

// offsetExpr ports offset.ts offset(expression, value): constant-fold the
// offset into number literals and into the right operand of an existing
// `+`/`-` binary (so `arr[i - 1]` + 1 -> `arr[i]`), else append `+ n`/`- n`.
func offsetExpr(expression luau.Expression, value float64) luau.Expression {
	if value == 0 {
		return expression
	}

	// this special case handles when the expression is a binary expression with a number literal on its right
	// i.e. array[offset - 1 + 1] -> array[offset]
	if binary, ok := expression.(*luau.BinaryExpression); ok && (binary.Operator == "+" || binary.Operator == "-") {
		if rightValue, ok := getLiteralNumberValue(binary.Right); ok {
			sign := 1.0
			if binary.Operator == "-" {
				sign = -1.0
			}
			newRightValue := rightValue + value*sign
			if newRightValue == 0 {
				return binary.Left
			}
			return luau.NewBinary(binary.Left, binary.Operator, luau.Num(newRightValue))
		}
	}

	if literalValue, ok := getLiteralNumberValue(expression); ok {
		return luau.Num(literalValue + value)
	}
	operator := luau.BinaryOperator("+")
	if value < 0 {
		operator = "-"
	}
	return luau.NewBinary(expression, operator, luau.Num(math.Abs(value)))
}

// addOneIfArrayType ports util/addOneIfArrayType.ts (L7-13): TS 0-based array
// indices become Luau 1-based — THE +1 offset — when the object type is
// definitely array-or-undefined.
func addOneIfArrayType(s *State, t *checker.Type, expression luau.Expression) luau.Expression {
	if IsDefinitelyType(s, t, IsArrayType(s), IsUndefinedType) {
		return offsetExpr(expression, 1)
	}
	return expression
}

// ---------------------------------------------------------------------------
// index diagnostics — util/addIndexDiagnostics.ts + isValidMethodIndexWithoutCall.ts
// ---------------------------------------------------------------------------

// isValidMethodIndexWithoutCall ports util/isValidMethodIndexWithoutCall.ts
// (L6-35): indexing a method without calling it is allowed when the parent is
// a BinaryExpression (`a.b !== undefined`), a PrefixUnaryExpression (`!a.b`),
// or an argument of the typeIs/typeOf call macros.
func isValidMethodIndexWithoutCall(s *State, node *ast.Node) bool {
	parent := node.Parent
	if parent == nil {
		return false
	}
	// a.b !== undefined
	if ast.IsBinaryExpression(parent) {
		return true
	}
	// !a.b
	if ast.IsPrefixUnaryExpression(parent) {
		return true
	}
	// typeIs/typeOf macros
	if ast.IsCallExpression(parent) {
		expType := s.Checker.GetNonOptionalType(s.GetType(parent.AsCallExpression().Expression))
		if symbol := GetFirstDefinedSymbol(s, expType); symbol != nil && s.Macros().IsTypeCheckCallMacro(symbol) {
			return true
		}
	}
	return false
}

// addIndexDiagnostics ports util/addIndexDiagnostics.ts (L10-26). NOTE the
// expType callers pass differs: property access passes the type of the access
// itself, element access passes the object's type.
func addIndexDiagnostics(s *State, node *ast.Node, expType *checker.Type) {
	symbol := GetFirstDefinedSymbol(s, expType)
	if (symbol != nil && s.Macros().GetPropertyCallMacro(symbol) != nil) ||
		(!isValidMethodIndexWithoutCall(s, SkipUpwards(node)) && isMethod(s, node)) {
		s.Diags.Add(DiagNoIndexWithoutCall(node))
	}

	if ast.IsPrototypeAccess(node) {
		s.Diags.Add(DiagNoPrototype(node))
	}
}

// ---------------------------------------------------------------------------
// constant folding — util/getConstantValueLiteral.ts
// ---------------------------------------------------------------------------

// getConstantValueLiteral ports util/getConstantValueLiteral.ts (L5-17):
// const-enum member accesses fold to their literal value; nil when the node
// has no constant value.
func getConstantValueLiteral(s *State, node *ast.Node) luau.Expression {
	switch value := s.Checker.GetConstantValue(node).(type) {
	case string:
		return luau.Str(value)
	case jsnum.Number:
		return luau.Num(float64(value))
	case float64:
		return luau.Num(value)
	}
	return nil
}

// ---------------------------------------------------------------------------
// property access — expressions/transformPropertyAccessExpression.ts
// ---------------------------------------------------------------------------

// transformPropertyAccessExpressionInner ports
// transformPropertyAccessExpressionInner (L11-34). expression is the
// already-transformed object (threaded through the chain fold).
func transformPropertyAccessExpressionInner(s *State, node *ast.Node, expression luau.Expression, name string) luau.Expression {
	// a in a.b
	validateNotAnyType(s, node.AsPropertyAccessExpression().Expression)

	addIndexDiagnostics(s, node, s.Checker.GetNonOptionalType(s.GetType(node)))

	if parent := SkipUpwards(node).Parent; parent != nil && ast.IsDeleteExpression(parent) {
		s.Prereq(luau.NewAssignment(
			luau.NewPropertyAccess(convertToIndexableExpression(expression), name),
			"=",
			luau.Nil(),
		))
		return luau.NewNone()
	}

	return luau.NewPropertyAccess(convertToIndexableExpression(expression), name)
}

// transformPropertyAccessExpression ports transformPropertyAccessExpression
// (L36-43): const-enum constant fold, else the optional-chain walker.
func transformPropertyAccessExpression(s *State, node *ast.Node) luau.Expression {
	if constantValue := getConstantValueLiteral(s, node); constantValue != nil {
		return constantValue
	}
	return transformOptionalChain(s, node)
}

// ---------------------------------------------------------------------------
// element access — expressions/transformElementAccessExpression.ts
// ---------------------------------------------------------------------------

// transformElementAccessExpressionInner ports
// transformElementAccessExpressionInner (L15-69).
func transformElementAccessExpressionInner(s *State, node *ast.Node, expression luau.Expression, argumentExpression *ast.Node) luau.Expression {
	elementAccess := node.AsElementAccessExpression()
	// a in a[b]
	validateNotAnyType(s, elementAccess.Expression)
	// b in a[b]
	validateNotAnyType(s, elementAccess.ArgumentExpression)

	expType := s.Checker.GetNonOptionalType(s.GetType(elementAccess.Expression))
	addIndexDiagnostics(s, node, expType)

	index, prereqs := s.Capture(func() luau.Expression { return TransformExpression(s, argumentExpression) })
	if data := s.getOptimizableVarArgsData(elementAccess.Expression); data != nil && data.IsOptimizable {
		s.PrereqList(prereqs)
		if indexValue, ok := getLiteralNumberValue(index); ok && indexValue == 0 {
			return luau.NewParenthesized(luau.NewVarArgs())
		}
		return luau.NewParenthesized(luau.NewCall(
			luau.GlobalID("select"),
			luau.NewList[luau.Expression](offsetExpr(index, 1), luau.NewVarArgs()),
		))
	}

	if prereqs.IsNonEmpty() {
		// hack because wrapReturnIfLuaTuple will not wrap this, but now we need to!
		if IsLuaTupleType(s).Check(expType) {
			expression = luau.NewArray(luau.NewList[luau.Expression](expression))
		}

		expression = s.PushToVar(expression, "exp")
		s.PrereqList(prereqs)
	}

	// LuaTuple<T> checks
	if luau.IsCall(expression) && IsLuaTupleType(s).Check(expType) {
		// wrap in select() if it isn't the first value
		num, isNum := index.(*luau.NumberLiteral)
		var numValue float64
		if isNum {
			numValue, _ = luau.JSNumberParse(num.Value)
		}
		if !isNum || numValue != 0 {
			expression = luau.NewCall(luau.GlobalID("select"),
				luau.NewList[luau.Expression](offsetExpr(index, 1), expression))
		}
		// parentheses to trim off the rest of the values
		return luau.NewParenthesized(expression)
	}

	if parent := SkipUpwards(node).Parent; parent != nil && ast.IsDeleteExpression(parent) {
		s.Prereq(luau.NewAssignment(
			luau.NewComputedIndex(
				convertToIndexableExpression(expression),
				addOneIfArrayType(s, expType, index),
			),
			"=",
			luau.Nil(),
		))
		return luau.NewNone()
	}

	return luau.NewComputedIndex(
		convertToIndexableExpression(expression),
		addOneIfArrayType(s, expType, index),
	)
}

// transformElementAccessExpression ports transformElementAccessExpression
// (L71-78): const-enum constant fold, else the optional-chain walker.
func transformElementAccessExpression(s *State, node *ast.Node) luau.Expression {
	if constantValue := getConstantValueLiteral(s, node); constantValue != nil {
		return constantValue
	}
	return transformOptionalChain(s, node)
}
