package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

type transformedFunctionBody struct {
	parameters   *luau.List[luau.AnyIdentifier]
	statements   *luau.List[luau.Statement]
	hasDotDotDot bool
}

// ---------------------------------------------------------------------------
// Functions — statements/transformFunctionDeclaration.ts,
// expressions/transformFunctionExpression.ts, nodes/transformMethodDeclaration.ts
// ---------------------------------------------------------------------------

// transformFunctionDeclaration ports transformFunctionDeclaration.ts (L13-76).
//
//   - bodiless overload signatures emit nothing;
//   - anonymous functions are only legal as `export default` and emit under
//     the literal name `default`;
//   - localize: an export-default anonymous function is always localized; a
//     NAMED function is localized unless its symbol was hoisted (a `local f`
//     already emitted by createHoistDeclaration — e.g. mutual recursion),
//     in which case the non-local `function f() end` assigns into it.
func transformFunctionDeclaration(s *State, node *ast.Node) *luau.List[luau.Statement] {
	declaration := node.AsFunctionDeclaration()

	// overload signatures emit nothing
	if declaration.Body == nil {
		return luau.NewList[luau.Statement]()
	}

	// NOTE ts.hasSyntacticModifier matches ANY selected flag, so a plain
	// `export function f` also sets isExportDefault — harmless upstream and
	// here, because the named branch below overrides localize and anonymous
	// declarations require a real `export default`. Ported verbatim.
	isExportDefault := ast.HasSyntacticModifier(node, ast.ModifierFlagsExportDefault)

	nameNode := declaration.Name()
	if nameNode == nil && !isExportDefault {
		panic("transformer: anonymous FunctionDeclaration must be export default") // upstream assert
	}

	var name luau.AnyIdentifier
	if nameNode != nil {
		ValidateIdentifier(s, nameNode)
		name = TransformIdentifierDefined(s, nameNode)
	} else {
		name = luau.ID("default")
	}

	body := transformFunctionBody(s, node)

	localize := isExportDefault
	if nameNode != nil {
		symbol := s.Checker.GetSymbolAtLocation(nameNode)
		if symbol == nil {
			panic("transformer: FunctionDeclaration name has no symbol") // upstream assert
		}
		localize = !s.IsHoisted[symbol]
	}

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	if declaration.AsteriskToken != nil {
		if isAsync {
			s.Diags.Add(DiagNoAsyncGeneratorFunctions(node))
		}
		body.statements = wrapStatementsAsGenerator(s, node, body.statements)
	}

	// The async path REPLACES the FunctionDeclaration emit entirely: `local f
	// = TS.async(function() ... end)` when localized, `f = TS.async(...)` when
	// hoisted (the hoist machinery emitted the `local f` header at the
	// premature use; async function declarations are hoist-SENSITIVE — the
	// self-reference exemption in identifier.go excludes them). Generator
	// DECLARATIONS stay real FunctionDeclarations whose body is the
	// TS.generator return.
	if isAsync {
		right := luau.NewCall(s.RuntimeLib(node, "async"), luau.NewList[luau.Expression](
			luau.NewFunctionExpression(body.parameters, body.hasDotDotDot, body.statements)))
		if localize {
			return luau.NewList[luau.Statement](luau.NewVariableDeclaration(name, right))
		}
		return luau.NewList[luau.Statement](luau.NewAssignment(name, "=", right))
	}

	return luau.NewList[luau.Statement](
		luau.NewFunctionDeclaration(localize, name, body.parameters, body.hasDotDotDot, body.statements))
}

func transformFunctionBody(s *State, node *ast.Node) transformedFunctionBody {
	parameters, statements, hasDotDotDot, varArgsData := transformParameters(s, node)
	restParam := registerOptimizableVarArgsForFunction(s, node, varArgsData)

	body := node.Body()
	if ast.IsBlock(body) {
		statements.PushList(TransformStatementList(s, body, body.AsBlock().Statements.Nodes, nil))
	} else {
		var returnStatements *luau.List[luau.Statement]
		prereqs := s.CaptureStatements(func() {
			returnStatements = transformReturnStatementInner(s, body)
		})
		statements.PushList(prereqs)
		statements.PushList(returnStatements)
	}
	if restParam != nil {
		s.unregisterOptimizableVarArgs(restParam)
	}

	return transformedFunctionBody{
		parameters:   parameters,
		statements:   statements,
		hasDotDotDot: hasDotDotDot,
	}
}

// transformFunctionExpression ports transformFunctionExpression.ts (L11-47):
// FunctionExpression and ArrowFunction share one transform. A synchronous
// named function expression is lifted to a local function declaration in
// ANY expression position — the prereq machinery already places the
// declaration where the expression is evaluated, so short-circuit operands
// and conditional arms stay conditional. Only async and generator
// expressions keep the diagnostic: their name binds to the TS.async /
// TS.generator wrapper rather than to the lifted closure, so a
// self-reference inside the body would reach the wrong function. Arrow
// expression bodies reuse the full return transform with prereqs captured
// into the function body — that is the only implicit-return mechanism.
func transformFunctionExpression(s *State, node *ast.Node) luau.Expression {
	if ast.IsFunctionExpression(node) {
		if name := node.AsFunctionExpression().Name(); name != nil {
			if isSynchronousNonGeneratorFunctionExpression(node) {
				ValidateIdentifier(s, name)
				identifier := luau.ExactTempID(name.Text())
				body := transformNamedFunctionExpressionBody(s, node, identifier)
				s.Prereq(luau.NewFunctionDeclaration(
					true,
					identifier,
					body.parameters,
					body.hasDotDotDot,
					body.statements,
				))
				return identifier
			}
			s.Diags.Add(DiagNoFunctionExpressionName(name))
		}
	}

	body := transformFunctionBody(s, node)

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	var asteriskToken *ast.Node
	if ast.IsFunctionExpression(node) {
		asteriskToken = node.AsFunctionExpression().AsteriskToken
	}
	if asteriskToken != nil {
		if isAsync {
			s.Diags.Add(DiagNoAsyncGeneratorFunctions(node))
		}
		body.statements = wrapStatementsAsGenerator(s, node, body.statements)
	}

	var expression luau.Expression = luau.NewFunctionExpression(body.parameters, body.hasDotDotDot, body.statements)

	if isAsync {
		expression = luau.NewCall(s.RuntimeLib(node, "async"), luau.NewList[luau.Expression](expression))
	}

	return expression
}

// transformMethodDeclaration ports nodes/transformMethodDeclaration.ts
// (L14-106): object-literal methods (`{ m() {} }`, inline-map pointer) and
// class methods (identifier pointer) share this transform.
func transformMethodDeclaration(s *State, node *ast.Node, ptr *MapPointer) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()

	declaration := node.AsMethodDeclaration()
	if declaration.Body == nil {
		return luau.NewList[luau.Statement]()
	}

	nameNode := declaration.Name()
	if nameNode == nil {
		panic("transformer: MethodDeclaration has no name") // upstream assert
	}
	if nameNode.Kind == ast.KindPrivateIdentifier {
		s.Diags.Add(DiagNoPrivateIdentifier(nameNode))
		return luau.NewList[luau.Statement]()
	}

	parameters, statements, hasDotDotDot, varArgsData := transformParameters(s, node)
	restParam := registerOptimizableVarArgsForFunction(s, node, varArgsData)
	statements.PushList(TransformStatementList(s, declaration.Body, declaration.Body.AsBlock().Statements.Nodes, nil))
	if restParam != nil {
		s.unregisterOptimizableVarArgs(restParam)
	}

	name := transformPropertyName(s, nameNode)

	// Decorator key pinning (upstream L36-49): a decorated method (or a method
	// with decorated parameters) records the object key the decorator
	// transforms re-read (transformDecorators, Phase 3c Task 3); a computed
	// non-literal key is pinned to a temp first so it only evaluates once.
	hasParameterDecorators := false
	for _, parameter := range node.Parameters() {
		if ast.HasDecorators(parameter) {
			hasParameterDecorators = true
			break
		}
	}
	if ast.HasDecorators(node) || hasParameterDecorators {
		if !luau.IsSimplePrimitive(name) {
			tempID := luau.TempID("key")
			result.Push(luau.NewVariableDeclaration(tempID, name))
			name = tempID
		}
		s.SetClassElementObjectKey(node, name)
	}

	isAsync := ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)

	// Generator wrap happens FIRST and does NOT exclude the method shape — a
	// generator method may still emit as `function Class:m()` whose body is
	// `return TS.generator(...)`.
	if declaration.AsteriskToken != nil {
		if isAsync {
			s.Diags.Add(DiagNoAsyncGeneratorFunctions(node))
		}
		statements = wrapStatementsAsGenerator(s, node, statements)
	}

	// can we use `function class:name() end`? — only when the pointer was
	// already spilled to a temp id (an inline map field can't hold a
	// function statement). Async methods are EXCLUDED (the !isAsync gate):
	// they fall to the map-pointer path with `self` kept in parameters
	// (`work = TS.async(function(self, n)`).
	nameStr, nameIsStr := name.(*luau.StringLiteral)
	_, ptrIsMap := ptr.Value.(*luau.Map)
	if !isAsync && nameIsStr && !ptrIsMap && luau.IsValidIdentifier(nameStr.Value) {
		if isMethod(s, node) {
			parameters.Shift() // remove `self`
			result.Push(luau.NewMethodDeclaration(
				ptr.Value.(luau.IndexableExpression), nameStr.Value, parameters, hasDotDotDot, statements))
		} else {
			result.Push(luau.NewFunctionDeclaration(
				false, /*localize*/
				luau.NewPropertyAccess(ptr.Value.(luau.IndexableExpression), nameStr.Value),
				parameters, hasDotDotDot, statements))
		}
		return result
	}

	var expression luau.Expression = luau.NewFunctionExpression(parameters, hasDotDotDot, statements)

	if isAsync {
		expression = luau.NewCall(s.RuntimeLib(node, "async"), luau.NewList[luau.Expression](expression))
	}

	// we have to use `class[name] = function()`
	result.PushList(s.CaptureStatements(func() {
		AssignToMapPointer(s, ptr, name, expression)
	}))

	return result
}
