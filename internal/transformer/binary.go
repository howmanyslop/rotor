package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// This file ports expressions/transformBinaryExpression.ts and
// util/createBinaryFromOperator.ts.

// operatorMap ports createBinaryFromOperator.ts OPERATOR_MAP (L9-24): TS
// binary operator tokens with a 1:1 Luau binary operator.
var operatorMap = map[ast.Kind]luau.BinaryOperator{
	// comparison
	ast.KindLessThanToken:                "<",
	ast.KindGreaterThanToken:             ">",
	ast.KindLessThanEqualsToken:          "<=",
	ast.KindGreaterThanEqualsToken:       ">=",
	ast.KindEqualsEqualsEqualsToken:      "==",
	ast.KindExclamationEqualsEqualsToken: "~=",

	// math
	ast.KindMinusToken:            "-",
	ast.KindAsteriskToken:         "*",
	ast.KindSlashToken:            "/",
	ast.KindAsteriskAsteriskToken: "^",
	ast.KindPercentToken:          "%",
}

// bitwiseOperatorMap ports createBinaryFromOperator.ts BITWISE_OPERATOR_MAP
// (L26-42): TS bitwise tokens to bit32 function names. The `=`-suffixed
// compound forms reach this map through the read-modify-write fallback
// (createCompoundAssignment* passes the compound token straight through).
var bitwiseOperatorMap = map[ast.Kind]string{
	// bitwise
	ast.KindAmpersandToken:                         "band",
	ast.KindBarToken:                               "bor",
	ast.KindCaretToken:                             "bxor",
	ast.KindLessThanLessThanToken:                  "lshift",
	ast.KindGreaterThanGreaterThanGreaterThanToken: "rshift",
	ast.KindGreaterThanGreaterThanToken:            "arshift",

	// bitwise compound assignment
	ast.KindAmpersandEqualsToken:                         "band",
	ast.KindBarEqualsToken:                               "bor",
	ast.KindCaretEqualsToken:                             "bxor",
	ast.KindLessThanLessThanEqualsToken:                  "lshift",
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: "rshift",
	ast.KindGreaterThanGreaterThanEqualsToken:            "arshift",
}

// createBinaryAdd ports createBinaryFromOperator.ts createBinaryAdd (L44-56)
// — THE `..` vs `+` decision: if either side is definitely a string, emit
// `..` concatenation with `tostring()` wrapped around any side that is not
// definitely a string; else numeric `+`.
func createBinaryAdd(s *State, left luau.Expression, leftType *checker.Type, right luau.Expression, rightType *checker.Type) luau.Expression {
	leftIsString := IsDefinitelyType(s, leftType, IsStringType)
	rightIsString := IsDefinitelyType(s, rightType, IsStringType)
	if leftIsString || rightIsString {
		if !leftIsString {
			left = luau.NewCall(luau.GlobalID("tostring"), luau.NewList[luau.Expression](left))
		}
		if !rightIsString {
			right = luau.NewCall(luau.GlobalID("tostring"), luau.NewList[luau.Expression](right))
		}
		return luau.NewBinary(left, "..", right)
	}
	return luau.NewBinary(left, "+", right)
}

// createBinaryFromOperator ports createBinaryFromOperator.ts (L58-90).
// Resolution order: simple map -> plus -> bitwise call -> comma ->
// assert-unreachable.
func createBinaryFromOperator(s *State, left luau.Expression, leftType *checker.Type, operatorKind ast.Kind, right luau.Expression, rightType *checker.Type) luau.Expression {
	// simple
	if operator, ok := operatorMap[operatorKind]; ok {
		return luau.NewBinary(left, operator, right)
	}

	// plus
	if operatorKind == ast.KindPlusToken || operatorKind == ast.KindPlusEqualsToken {
		return createBinaryAdd(s, left, leftType, right, rightType)
	}

	// bitwise
	if _, ok := bitwiseOperatorMap[operatorKind]; ok {
		return createBitwiseCall(operatorKind, []luau.Expression{left, right})
	}

	if operatorKind == ast.KindCommaToken {
		s.PrereqList(wrapExpressionStatement(left))
		return right
	}

	panic("transformer: createBinaryFromOperator unknown operator: " + kindName(operatorKind)) // upstream assert(false)
}

// transformBinaryExpression ports transformBinaryExpression.ts (L113-253).
func transformBinaryExpression(s *State, node *ast.Node) luau.Expression {
	expression := node.AsBinaryExpression()
	operatorKind := expression.OperatorToken.Kind

	validateNotAnyType(s, expression.Left)
	validateNotAnyType(s, expression.Right)

	// banned
	if operatorKind == ast.KindEqualsEqualsToken {
		s.Diags.Add(DiagNoEqualsEquals(node))
		return luau.NewNone()
	} else if operatorKind == ast.KindExclamationEqualsToken {
		s.Diags.Add(DiagNoExclamationEquals(node))
		return luau.NewNone()
	}

	// logical
	if operatorKind == ast.KindAmpersandAmpersandToken ||
		operatorKind == ast.KindBarBarToken ||
		operatorKind == ast.KindQuestionQuestionToken {
		return transformLogical(s, node)
	}

	// logical assignment (&&=, ||=, ??=) in expression position: the value is
	// the writable (a re-read of the target after the conditional write).
	if ast.IsLogicalOrCoalescingAssignmentExpression(node) {
		return transformLogicalOrCoalescingAssignmentExpression(s, node)
	}

	if ast.IsAssignmentOperator(operatorKind) {
		// Destructuring assignment (upstream L141-184): "in destructuring, rhs
		// must be executed first". Array LHS has the optimized multi-assign
		// paths (`[a, b] = [b, a]` -> `a, b = b, a`); object LHS always
		// destructures through a `_binding` temp. The expression VALUE of a
		// destructuring assignment is the (temp-bound) RHS.
		if ast.IsArrayLiteralExpression(expression.Left) {
			rightExp := TransformExpression(s, expression.Right)

			// optimize empty array destructure
			if len(expression.Left.AsArrayLiteralExpression().Elements.Nodes) == 0 {
				if array, ok := rightExp.(*luau.Array); ok && isUsedAsStatement(node) && array.Members.IsEmpty() {
					return luau.NewNone()
				}
				return rightExp
			}

			if luau.IsCall(rightExp) && IsLuaTupleType(s).Check(s.GetType(expression.Right)) {
				transformOptimizedArrayAssignmentPattern(s, expression.Left, rightExp)
				if !isUsedAsStatement(node) {
					s.Diags.Add(DiagNoLuaTupleDestructureAssignmentExpression(node))
				}
				return luau.NewNone()
			}

			if array, ok := rightExp.(*luau.Array); ok && array.Members.IsNonEmpty() && isUsedAsStatement(node) {
				transformOptimizedArrayAssignmentPattern(s, expression.Left, array.Members)
				return luau.NewNone()
			}

			parentID := s.PushToVar(rightExp, "binding")
			transformArrayAssignmentPattern(s, expression.Left, parentID)
			return parentID
		} else if ast.IsObjectLiteralExpression(expression.Left) {
			rightExp := TransformExpression(s, expression.Right)

			// optimize empty object destructure
			if len(expression.Left.AsObjectLiteralExpression().Properties.Nodes) == 0 {
				if mapExp, ok := rightExp.(*luau.Map); ok && isUsedAsStatement(node) && mapExp.Fields.IsEmpty() {
					return luau.NewNone()
				}
				return rightExp
			}

			parentID := s.PushToVar(rightExp, "binding")
			transformObjectAssignmentPattern(s, expression.Left, parentID)
			return parentID
		}

		writableType := s.GetType(expression.Left)
		valueType := s.GetType(expression.Right)
		operator, isSimple := getSimpleAssignmentOperator(s, writableType, operatorKind, valueType)
		assignment := transformWritableAssignment(s, expression.Left, expression.Right, true, !isSimple)
		if isSimple {
			return createAssignmentExpression(s, assignment.writable, operator, getAssignableValue(s, operator, assignment.value, valueType))
		}
		return createCompoundAssignmentExpression(s, assignment.writable, writableType, assignment.readable, operatorKind, assignment.value, valueType)
	}

	if isBitwiseOperator(operatorKind) {
		return createBitwiseFromOperator(s, operatorKind, node)
	}

	ordered := ensureTransformOrder(s, []*ast.Node{expression.Left, expression.Right})
	left, right := ordered[0], ordered[1]

	if operatorKind == ast.KindInKeyword {
		return luau.NewBinary(
			luau.NewComputedIndex(convertToIndexableExpression(right), left),
			"~=",
			luau.Nil(),
		)
	} else if operatorKind == ast.KindInstanceOfKeyword {
		if IsPossiblyType(s, s.GetType(expression.Right), IsRobloxType(s)) {
			s.Diags.Add(DiagNoRobloxSymbolInstanceof(expression.Right))
		}
		return luau.NewCall(s.RuntimeLib(node, "instanceof"), luau.NewList[luau.Expression](left, right))
	}

	leftType := s.GetType(expression.Left)
	rightType := s.GetType(expression.Right)

	if operatorKind == ast.KindLessThanToken ||
		operatorKind == ast.KindLessThanEqualsToken ||
		operatorKind == ast.KindGreaterThanToken ||
		operatorKind == ast.KindGreaterThanEqualsToken {
		// NOTE: verbatim upstream quirk (transformBinaryExpression.ts
		// L244-247): the second clause re-tests LEFT type for number where
		// symmetry suggests rightType — ported as-is, byte parity over sanity.
		if (!IsDefinitelyType(s, leftType, IsStringType) && !IsDefinitelyType(s, leftType, IsNumberType)) ||
			(!IsDefinitelyType(s, rightType, IsStringType) && !IsDefinitelyType(s, leftType, IsNumberType)) {
			s.Diags.Add(DiagNoNonNumberStringRelationOperator(node))
		}
	}

	return createBinaryFromOperator(s, left, leftType, operatorKind, right, rightType)
}
