package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

func isSynchronousNonGeneratorFunctionExpression(node *ast.Node) bool {
	return ast.IsFunctionExpression(node) &&
		!ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync) &&
		node.AsFunctionExpression().AsteriskToken == nil
}

func transformNamedFunctionExpressionBody(
	s *State,
	node *ast.Node,
	replacement *luau.TemporaryIdentifier,
) transformedFunctionBody {
	nameNode := node.AsFunctionExpression().Name()
	if nameNode == nil {
		panic("transformer: named FunctionExpression has no name")
	}
	nameSymbol := s.Checker.GetSymbolAtLocation(nameNode)
	if nameSymbol == nil {
		panic("transformer: FunctionExpression name has no symbol")
	}
	wasHoisted, hadHoistDecision := s.IsHoisted[nameSymbol]
	previousReplacement, hadReplacement := s.SymbolToID[nameSymbol]
	s.IsHoisted[nameSymbol] = false
	if replacement != nil {
		s.SymbolToID[nameSymbol] = replacement
	}
	defer func() {
		if hadHoistDecision {
			s.IsHoisted[nameSymbol] = wasHoisted
		} else {
			delete(s.IsHoisted, nameSymbol)
		}
		if hadReplacement {
			s.SymbolToID[nameSymbol] = previousReplacement
		} else {
			delete(s.SymbolToID, nameSymbol)
		}
	}()
	return transformFunctionBody(s, node)
}

func transformMatchingNamedFunctionConst(s *State, node *ast.Node) *luau.FunctionDeclaration {
	if ast.HasSyntacticModifier(node, ast.ModifierFlagsExport) {
		return nil
	}

	declarationList := node.AsVariableStatement().DeclarationList
	flags := declarationList.Flags
	if flags&ast.NodeFlagsConst == 0 || flags&ast.NodeFlagsUsing != 0 {
		return nil
	}

	declarations := declarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil
	}
	declaration := declarations[0].AsVariableDeclaration()
	bindingName := declaration.Name()
	initializer := declaration.Initializer
	if !ast.IsIdentifier(bindingName) || !isSynchronousNonGeneratorFunctionExpression(initializer) {
		return nil
	}

	functionName := initializer.AsFunctionExpression().Name()
	if functionName == nil || functionName.Text() != bindingName.Text() {
		return nil
	}

	ValidateIdentifier(s, bindingName)
	bindingSymbol := s.Checker.GetSymbolAtLocation(bindingName)
	if bindingSymbol == nil {
		panic("transformer: matching named function const has no binding symbol")
	}
	if isExportedLocalSymbol(s, bindingSymbol) {
		return nil
	}
	checkVariableHoist(s, bindingName, bindingSymbol)
	body := transformNamedFunctionExpressionBody(s, initializer, nil)
	return luau.NewFunctionDeclaration(
		!s.IsHoisted[bindingSymbol],
		TransformIdentifierDefined(s, bindingName),
		body.parameters,
		body.hasDotDotDot,
		body.statements,
	)
}

func isExportedLocalSymbol(s *State, localSymbol *ast.Symbol) bool {
	moduleSymbol := s.Checker.GetSymbolAtLocation(s.SourceFile.AsNode())
	if moduleSymbol == nil {
		panic("transformer: source file has no module symbol")
	}
	for _, exportSymbol := range s.GetModuleExports(moduleSymbol) {
		if checker.SkipAlias(exportSymbol, s.Checker) == localSymbol {
			return true
		}
	}
	return false
}
