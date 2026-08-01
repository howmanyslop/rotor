package transformer

import "rotor/tsgo/ast"

type VarArgsData struct {
	ParameterIndex   int
	SizeAccessCount  int
	ForOfAccessCount int
	IsOptimizable    bool
}

func analyzeVarArgsOptimization(s *State, param *ast.Node, funcNode *ast.Node) *VarArgsData {
	data := &VarArgsData{ParameterIndex: -1}
	parameter := param.AsParameterDeclaration()
	if parameter.DotDotDotToken == nil || !ast.IsIdentifier(parameter.Name()) {
		return data
	}

	for index, candidate := range funcNode.Parameters() {
		if candidate.AsNode() == param {
			data.ParameterIndex = index
			break
		}
	}
	if data.ParameterIndex == -1 || ast.GetFunctionFlags(funcNode)&ast.FunctionFlagsGenerator != 0 {
		return data
	}

	var body *ast.Node
	switch funcNode.Kind {
	case ast.KindFunctionDeclaration,
		ast.KindFunctionExpression,
		ast.KindArrowFunction,
		ast.KindMethodDeclaration,
		ast.KindConstructor:
		body = funcNode.Body()
	default:
		return data
	}
	if body == nil {
		return data
	}

	paramSymbol := s.Checker.GetSymbolAtLocation(parameter.Name())
	if paramSymbol == nil {
		return data
	}

	data.IsOptimizable = true
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !data.IsOptimizable {
			return true
		}
		if !ast.IsIdentifier(node) || node == parameter.Name() || s.Checker.GetSymbolAtLocation(node) != paramSymbol {
			return node.ForEachChild(visit)
		}

		reference := SkipUpwards(node)
		parent := reference.Parent
		if parent == nil || isVarArgsReferenceInsideNestedFunction(reference, funcNode) || isVarArgsReferenceInsideTry(reference, funcNode) {
			data.IsOptimizable = false
			return true
		}

		if ast.IsElementAccessExpression(parent) && parent.AsElementAccessExpression().Expression == reference {
			if ast.IsAssignmentTarget(parent) {
				data.IsOptimizable = false
				return true
			}
			return false
		}

		if ast.IsSpreadElement(parent) && parent.Expression() == reference {
			if ast.IsArrayLiteralExpression(parent.Parent) {
				data.IsOptimizable = false
				return true
			}
			return false
		}

		if ast.IsForOfStatement(parent) && parent.AsForInOrOfStatement().Expression == reference {
			initializer := parent.AsForInOrOfStatement().Initializer
			if ast.IsVariableDeclarationList(initializer) {
				declaration := initializer.AsVariableDeclarationList().Declarations.Nodes[0]
				if !ast.IsIdentifier(declaration.Name()) {
					data.IsOptimizable = false
					return true
				}
			} else if !ast.IsIdentifier(initializer) {
				data.IsOptimizable = false
				return true
			}
			data.ForOfAccessCount++
			return false
		}

		if ast.IsPropertyAccessExpression(parent) && parent.AsPropertyAccessExpression().Expression == reference {
			if parent.Name().Text() == "size" {
				call := parent.Parent
				if call != nil && ast.IsCallExpression(call) && call.AsCallExpression().Expression == parent {
					data.SizeAccessCount++
					return false
				}
			}
			data.IsOptimizable = false
			return true
		}

		data.IsOptimizable = false
		return true
	}

	visit(body)
	return data
}

func isVarArgsReferenceInsideNestedFunction(node *ast.Node, functionNode *ast.Node) bool {
	for current := SkipUpwards(node).Parent; current != nil && current != functionNode; current = SkipUpwards(current).Parent {
		if ast.IsFunctionLikeDeclaration(current) {
			return true
		}
	}
	return false
}

func isVarArgsReferenceInsideTry(node *ast.Node, functionNode *ast.Node) bool {
	for current := SkipUpwards(node).Parent; current != nil && current != functionNode; current = SkipUpwards(current).Parent {
		if ast.IsTryStatement(current) {
			return true
		}
	}
	return false
}

func registerOptimizableVarArgsForFunction(s *State, node *ast.Node, data *VarArgsData) *ast.Node {
	if data == nil || !data.IsOptimizable {
		return nil
	}
	for _, parameter := range node.Parameters() {
		if parameter.AsParameterDeclaration().DotDotDotToken != nil {
			restParam := parameter.AsNode()
			s.registerOptimizableVarArgs(restParam, data)
			return restParam
		}
	}
	return nil
}
