package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

var logicalBitwiseOperatorMap = map[ast.Kind]string{
	ast.KindAmpersandToken: "band",
	ast.KindBarToken:       "bor",
	ast.KindCaretToken:     "bxor",
}

func flattenBitwiseChainInto(expressions *[]*ast.Node, operatorKind ast.Kind, node *ast.Node) {
	if ast.IsBinaryExpression(node) {
		expression := node.AsBinaryExpression()
		if expression.OperatorToken.Kind == operatorKind {
			flattenBitwiseChainInto(expressions, operatorKind, expression.Left)
			*expressions = append(*expressions, SkipDownwards(expression.Right))
		} else {
			*expressions = append(*expressions, node)
		}
		return
	}

	*expressions = append(*expressions, SkipDownwards(node))
}

func flattenBitwiseChain(operatorKind ast.Kind, node *ast.Node) []*ast.Node {
	expressions := make([]*ast.Node, 0, 2)
	flattenBitwiseChainInto(&expressions, operatorKind, node)
	return expressions
}

func isBitwiseLogicalOperator(operatorKind ast.Kind) bool {
	_, ok := logicalBitwiseOperatorMap[operatorKind]
	return ok
}

func isBitwiseOperator(operatorKind ast.Kind) bool {
	if isBitwiseLogicalOperator(operatorKind) {
		return true
	}
	_, ok := bitwiseOperatorMap[operatorKind]
	return ok
}

func createBitwiseCall(operatorKind ast.Kind, expressions []luau.Expression) luau.Expression {
	name, ok := logicalBitwiseOperatorMap[operatorKind]
	if !ok {
		name, ok = bitwiseOperatorMap[operatorKind]
	}
	if !ok {
		panic("transformer: createBitwiseCall unknown operator: " + kindName(operatorKind))
	}
	return luau.NewCall(luau.GlobalProperty("bit32", name), luau.NewList(expressions...))
}

func createBitwiseFromOperator(s *State, operatorKind ast.Kind, node *ast.Node) luau.Expression {
	expression := node.AsBinaryExpression()
	operands := make([]*ast.Node, 0, 2)
	if isBitwiseLogicalOperator(operatorKind) {
		operands = append(operands, flattenBitwiseChain(operatorKind, expression.Left)...)
		operands = append(operands, expression.Right)
	} else {
		operands = append(operands, expression.Left, expression.Right)
	}

	return createBitwiseCall(operatorKind, ensureTransformOrder(s, operands))
}
