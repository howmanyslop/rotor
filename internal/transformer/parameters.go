package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// ---------------------------------------------------------------------------
// Parameters — nodes/transformParameters.ts, nodes/transformInitializer.ts
// ---------------------------------------------------------------------------

// transformInitializer ports transformInitializer.ts (L6-20): the `= default`
// shape. Default expressions are evaluated lazily inside the nil-check (TS
// semantics: a default is only computed when undefined is passed):
//
//	if id == nil then
//		<init prereqs>
//		id = <init>
//	end
func transformInitializer(s *State, id luau.WritableExpression, initializer *ast.Node) luau.Statement {
	statements := s.CaptureStatements(func() {
		s.Prereq(luau.NewAssignment(id, "=", TransformExpression(s, initializer)))
	})
	return luau.NewIf(luau.NewBinary(id, "==", luau.Nil()), statements, nil)
}

// transformParameters ports transformParameters.ts (L53-124). Returns the
// Luau parameter list, the body-head statements (rest capture, defaults,
// destructuring), and whether the parameter list ends in `...`.
//
//   - methods get an explicit leading `self` parameter (isMethod, CHECKER);
//   - a TS `this` parameter emits nothing (its only effect is via isMethod);
//   - rest `...args` sets hasDotDotDot and prepends `local args = { ... }`;
//   - defaults run BEFORE destructuring, via transformInitializer.
func transformParameters(s *State, node *ast.Node) (parameters *luau.List[luau.AnyIdentifier], statements *luau.List[luau.Statement], hasDotDotDot bool, varArgsData *VarArgsData) {
	parameters = luau.NewList[luau.AnyIdentifier]()
	statements = luau.NewList[luau.Statement]()

	if isMethod(s, node) {
		parameters.Push(luau.GlobalID("self"))
	}

	for _, parameterNode := range node.Parameters() {
		parameter := parameterNode.AsParameterDeclaration()
		name := parameter.Name()

		if ast.IsThisIdentifier(name) {
			continue
		}

		if parameter.DotDotDotToken != nil && ast.IsArrayBindingPattern(name) && !arrayLikeExpressionContainsSpread(name) {
			// `...[a, b]: [A, B]` flattens the pattern elements into real
			// parameters (no `...` capture).
			statements.PushList(s.CaptureStatements(func() {
				optimizeArraySpreadParameter(s, parameters, name)
			}))
			continue
		}

		var paramID luau.AnyIdentifier
		if ast.IsIdentifier(name) {
			paramID = TransformIdentifierDefined(s, name)
			ValidateIdentifier(s, name)
		} else {
			paramID = luau.TempID("param")
		}

		if parameter.DotDotDotToken != nil {
			hasDotDotDot = true
			varArgsData = analyzeVarArgsOptimization(s, parameterNode.AsNode(), node)
			// local args = { ... }
			statements.Push(luau.NewVariableDeclaration(paramID,
				luau.NewArray(luau.NewList[luau.Expression](luau.NewVarArgs()))))
		} else {
			parameters.Push(paramID)
		}

		if parameter.Initializer != nil {
			statements.Push(transformInitializer(s, paramID.(luau.WritableExpression), parameter.Initializer))
		}

		// destructuring
		if !ast.IsIdentifier(name) {
			statements.PushList(s.CaptureStatements(func() {
				if ast.IsArrayBindingPattern(name) {
					transformArrayBindingPattern(s, name, paramID)
				} else {
					transformObjectBindingPattern(s, name, paramID)
				}
			}))
		}
	}

	return parameters, statements, hasDotDotDot, varArgsData
}

// optimizeArraySpreadParameter ports transformParameters.ts
// optimizeArraySpreadParameter (L16-51): parameters in the form
// `...[a, b, c]: [A, B, C]` become just `(a, b, c)`. An omitted element gets
// a placeholder temp parameter; a rest element raises
// noNestedSpreadsInAssignmentPatterns
// and aborts the pattern; nested patterns get a `_param` temp destructured
// in the body (initializer first, then the recursion). Callers capture the
// prereqs into the body-head statements.
func optimizeArraySpreadParameter(s *State, parameters *luau.List[luau.AnyIdentifier], bindingPattern *ast.Node) {
	for _, element := range bindingPattern.AsBindingPattern().Elements.Nodes {
		if isOmittedBindingElement(element) {
			parameters.Push(luau.TempID(""))
		} else {
			bindingElement := element.AsBindingElement()
			if bindingElement.DotDotDotToken != nil {
				s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
				return
			}
			name := bindingElement.Name()
			if ast.IsIdentifier(name) {
				paramID := TransformIdentifierDefined(s, name)
				ValidateIdentifier(s, name)
				parameters.Push(paramID)
				if bindingElement.Initializer != nil {
					s.Prereq(transformInitializer(s, paramID.(luau.WritableExpression), bindingElement.Initializer))
				}
			} else {
				paramID := luau.TempID("param")
				parameters.Push(paramID)
				if bindingElement.Initializer != nil {
					s.Prereq(transformInitializer(s, paramID, bindingElement.Initializer))
				}
				if ast.IsArrayBindingPattern(name) {
					transformArrayBindingPattern(s, name, paramID)
				} else {
					transformObjectBindingPattern(s, name, paramID)
				}
			}
		}
	}
}
