package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

func arrayLikeExpressionContainsSpread(node *ast.Node) bool {
	if ast.IsArrayBindingPattern(node) || ast.IsArrayLiteralExpression(node) {
		var elements []*ast.Node
		if ast.IsArrayBindingPattern(node) {
			elements = node.AsBindingPattern().Elements.Nodes
		} else {
			elements = node.AsArrayLiteralExpression().Elements.Nodes
		}

		for _, element := range elements {
			if ast.IsBindingElement(element) {
				bindingElement := element.AsBindingElement()
				if bindingElement.DotDotDotToken != nil {
					return true
				}
				name := bindingElement.Name()
				if name != nil && arrayLikeExpressionContainsSpread(name) {
					return true
				}
			} else if ast.IsSpreadElement(element) {
				return true
			} else if ast.IsArrayBindingPattern(element) || ast.IsArrayLiteralExpression(element) {
				if arrayLikeExpressionContainsSpread(element) {
					return true
				}
			}
		}
	}
	return false
}

func getTargetIDForBindingPattern(s *State, pattern *ast.Node, value luau.Expression) luau.AnyIdentifier {
	if id, ok := value.(luau.AnyIdentifier); ok {
		if hasBindingPatternInitializers(pattern) {
			return s.PushToVar(value, "binding")
		}
		return id
	}
	return s.PushToVar(value, "binding")
}

func hasBindingPatternInitializers(pattern *ast.Node) bool {
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		if ast.IsBindingElement(element) && element.AsBindingElement().Initializer != nil {
			return true
		}
	}
	return false
}
