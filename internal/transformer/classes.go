package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// This file ports the class transforms:
//   - nodes/class/transformClassLikeDeclaration.ts (boilerplate + driver)
//   - nodes/class/transformClassConstructor.ts (explicit/implicit constructor
//     + property initializers)
//   - nodes/class/transformPropertyDeclaration.ts (static properties)
//   - statements/transformClassDeclaration.ts, expressions/
//     transformClassExpression.ts (entry points)
//   - expressions/transformThisExpression.ts, transformSuperKeyword.ts
//   - util/getExtendsNode.ts, util/findConstructor.ts
//   - validateMethodAssignment's ClassElement arm (util/
//     validateMethodAssignment.ts:37-46,64-67) + a local getAllSuperTypeNodes
//     (tsgo keeps its equivalent unexported in tsgo/ls/utilities.go).
//
// nodes/class/transformDecorators.ts lives in decorators.go.

// magicToStringMethod ports MAGIC_TO_STRING_METHOD
// (transformClassLikeDeclaration.ts:23).
const magicToStringMethod = "toString"

const constructorMethodName = "constructor"

// getExtendsNode ports util/getExtendsNode.ts: the first `extends` heritage
// element (an ExpressionWithTypeArguments; only `.Expression()` is ever
// transformed), or nil.
func getExtendsNode(node *ast.Node) *ast.Node {
	return ast.GetClassExtendsHeritageElement(node)
}

// findConstructor ports util/findConstructor.ts: the first
// ConstructorDeclaration member WITH a body (overload signatures have none).
func findConstructor(node *ast.Node) *ast.Node {
	for _, member := range node.Members() {
		if ast.IsConstructorDeclaration(member) && member.Body() != nil {
			return member
		}
	}
	return nil
}

// getAllSuperTypeNodes reimplements ts.getAllSuperTypeNodes (tsgo has the
// equivalent unexported at tsgo/ls/utilities.go:915): the nodes in `extends`
// and `implements` clauses of a class or interface.
func getAllSuperTypeNodes(node *ast.Node) []*ast.Node {
	if ast.IsInterfaceDeclaration(node) {
		return ast.GetHeritageElements(node, ast.KindExtendsKeyword)
	}
	if ast.IsClassLike(node) {
		var result []*ast.Node
		if extendsElement := ast.GetClassExtendsHeritageElement(node); extendsElement != nil {
			result = append(result, extendsElement)
		}
		return append(result, ast.GetImplementsTypeNodes(node)...)
	}
	return nil
}

// createClassNameFunction ports transformClassLikeDeclaration.ts
// createNameFunction (L25-35): `function() return "<name>" end`.
func createClassNameFunction(name string) *luau.FunctionExpression {
	return luau.NewFunctionExpression(
		luau.NewList[luau.AnyIdentifier](),
		false,
		luau.NewList[luau.Statement](luau.NewReturn(luau.Str(name))),
	)
}

// createClassBoilerplate ports transformClassLikeDeclaration.ts
// createBoilerplate (L37-188):
//
//	className = setmetatable({}, {
//		__tostring = function() return "className" end,
//		__index = super,          -- extends only
//	})
//	className.__index = className
//	function className.new(...)
//		local self = setmetatable({}, className)
//		return self:constructor(...) or self
//	end
//
// QUIRKS (oracle-verified, do not "fix"):
//   - abstract no-extends classes are a bare `className = {}` — no metatable,
//     no `__tostring`, no `className.__index = className`;
//   - abstract classes (either form) never get `.new` but still get
//     `constructor` and methods;
//   - anonymous classes stringify as "Anonymous";
//   - `.new` returns `self:constructor(...) or self` — a constructor
//     returning a truthy value hijacks construction (JS semantics).
func createClassBoilerplate(s *State, node *ast.Node, className luau.AnyIdentifier, isClassExpression bool) *luau.List[luau.Statement] {
	isAbstract := ast.HasAbstractModifier(node)
	statements := luau.NewList[luau.Statement]()

	// if a class is abstract and it does not extend any class, it can just be
	// a plain table; otherwise we can use the default boilerplate
	extendsNode := getExtendsNode(node)
	if isAbstract && extendsNode == nil {
		statements.Push(luau.NewAssignment(className, "=", luau.NewMap(luau.NewList[*luau.MapField]())))
	} else {
		metatableFields := luau.NewList[*luau.MapField]()
		tostringName := "Anonymous"
		if id, ok := className.(*luau.Identifier); ok {
			tostringName = id.Name
		}
		metatableFields.Push(luau.NewMapField(luau.Str("__tostring"), createClassNameFunction(tostringName)))

		if extendsNode != nil {
			extendsExp, extendsExpPrereqs := s.Capture(func() luau.Expression {
				return TransformExpression(s, extendsNode.Expression())
			})
			superID := luau.GlobalID("super")
			statements.PushList(extendsExpPrereqs)
			statements.Push(luau.NewVariableDeclaration(superID, extendsExp))
			metatableFields.Push(luau.NewMapField(luau.Str("__index"), superID))
		}

		metatable := luau.NewCall(luau.GlobalID("setmetatable"), luau.NewList[luau.Expression](
			luau.NewMap(luau.NewList[*luau.MapField]()),
			luau.NewMap(metatableFields),
		))

		if isClassExpression && node.Name() != nil {
			// the ONLY local-declaration form: the named class expression's
			// inner name
			statements.Push(luau.NewVariableDeclaration(TransformIdentifierDefined(s, node.Name()), metatable))
		} else {
			statements.Push(luau.NewAssignment(className, "=", metatable))
		}

		//	className.__index = className
		statements.Push(luau.NewAssignment(luau.NewPropertyAccess(className, "__index"), "=", className))
	}

	// statements for className.new
	if !isAbstract {
		statementsInner := luau.NewList[luau.Statement]()

		//	local self = setmetatable({}, className)
		statementsInner.Push(luau.NewVariableDeclaration(
			luau.GlobalID("self"),
			luau.NewCall(luau.GlobalID("setmetatable"), luau.NewList[luau.Expression](
				luau.NewMap(luau.NewList[*luau.MapField]()),
				className,
			)),
		))

		//	return self:constructor(...) or self
		statementsInner.Push(luau.NewReturn(luau.NewBinary(
			luau.NewMethodCall(constructorMethodName, luau.GlobalID("self"),
				luau.NewList[luau.Expression](luau.NewVarArgs())),
			"or",
			luau.GlobalID("self"),
		)))

		//	function className.new(...)
		//	end
		statementsInner2 := luau.NewFunctionDeclaration(
			false, /*localize*/
			luau.NewPropertyAccess(className, "new"),
			luau.NewList[luau.AnyIdentifier](),
			true, /*hasDotDotDot*/
			statementsInner,
		)
		statements.Push(statementsInner2)
	}

	return statements
}

// isClassHoisted ports transformClassLikeDeclaration.ts isClassHoisted
// (L190-197).
func isClassHoisted(s *State, node *ast.Node) bool {
	if name := node.Name(); name != nil {
		symbol := s.Checker.GetSymbolAtLocation(name)
		if symbol == nil {
			panic("transformer: class name has no symbol") // upstream assert
		}
		return s.IsHoisted[symbol]
	}
	return false
}

// transformPropertyInitializers ports transformClassConstructor.ts
// transformPropertyInitializers (L16-50): per NON-static PropertyDeclaration
// in member order, `self[<name>] = <initializer>`. QUIRK: a `#field` private
// identifier raises noPrivateIdentifier spanning the WHOLE CLASS (upstream
// passes the class node) — port verbatim.
func transformPropertyInitializers(s *State, node *ast.Node) *luau.List[luau.Statement] {
	statements := luau.NewList[luau.Statement]()
	for _, member := range node.Members() {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		if ast.HasStaticModifier(member) {
			continue
		}

		name := member.Name()
		if ast.IsPrivateIdentifier(name) {
			s.Diags.Add(DiagNoPrivateIdentifier(node))
			continue
		}

		initializer := member.Initializer()
		if initializer == nil {
			continue
		}

		index, indexPrereqs := s.Capture(func() luau.Expression {
			return transformPropertyName(s, name)
		})
		statements.PushList(indexPrereqs)

		right, rightPrereqs := s.Capture(func() luau.Expression {
			return TransformExpression(s, initializer)
		})
		statements.PushList(rightPrereqs)

		statements.Push(luau.NewAssignment(
			luau.NewComputedIndex(luau.GlobalID("self"), index),
			"=",
			right,
		))
	}
	return statements
}

// transformImplicitClassConstructor ports transformClassConstructor.ts
// transformImplicitClassConstructor (L52-88). QUIRK: an implicit derived
// constructor is variadic (`constructor(...)` + `super.constructor(self, ...)`)
// even when the base constructor takes no arguments.
func transformImplicitClassConstructor(s *State, node *ast.Node, name luau.AnyIdentifier) *luau.List[luau.Statement] {
	statements := luau.NewList[luau.Statement]()

	hasDotDotDot := false

	// if extends + no constructor:
	// - add ... to params
	// - add super.constructor(self, ...)
	if getExtendsNode(node) != nil {
		hasDotDotDot = true
		statements.Push(luau.NewCallStatement(luau.NewCall(
			luau.NewPropertyAccess(luau.GlobalID("super"), constructorMethodName),
			luau.NewList[luau.Expression](luau.GlobalID("self"), luau.NewVarArgs()),
		)))
	}

	statements.PushList(transformPropertyInitializers(s, node))

	return luau.NewList[luau.Statement](luau.NewMethodDeclaration(
		name, constructorMethodName, luau.NewList[luau.AnyIdentifier](), hasDotDotDot, statements))
}

// transformClassConstructor ports transformClassConstructor.ts
// transformClassConstructor (L90-130). node is a ConstructorDeclaration with
// a body. Statement order inside `function X:constructor(...)`:
// parameter defaults/destructuring -> body up to AND INCLUDING the first
// top-level super() call -> parameter-property assignments -> property
// initializers -> rest of body.
//
// NOTE transformParameters does NOT inject `self` here: isMethod is false for
// ConstructorDeclarations (their type has construct signatures, no call
// signatures); the `:` declaration sugar supplies self.
func transformClassConstructor(s *State, node *ast.Node, name luau.AnyIdentifier) *luau.List[luau.Statement] {
	parameters, statements, hasDotDotDot, varArgsData := transformParameters(s, node)
	restParam := registerOptimizableVarArgsForFunction(s, node, varArgsData)
	bodyStatements := node.Body().AsBlock().Statements.Nodes

	// property parameters must come after the first super() call
	superIndex := -1
	for i, statement := range bodyStatements {
		if ast.IsExpressionStatement(statement) && ast.IsSuperCall(statement.Expression()) {
			superIndex = i
			break
		}
	}

	statements.PushList(TransformStatementList(s, node.Body(), bodyStatements[:superIndex+1], nil))

	for _, parameter := range node.Parameters() {
		if ast.IsParameterPropertyDeclaration(parameter, parameter.Parent) {
			paramID := TransformIdentifierDefined(s, parameter.Name())
			statements.Push(luau.NewAssignment(
				luau.NewPropertyAccess(luau.GlobalID("self"), anyIdentifierName(paramID)),
				"=",
				paramID,
			))
		}
	}

	statements.PushList(transformPropertyInitializers(s, node.Parent))

	statements.PushList(TransformStatementList(s, node.Body(), bodyStatements[superIndex+1:], nil))
	if restParam != nil {
		s.unregisterOptimizableVarArgs(restParam)
	}

	return luau.NewList[luau.Statement](luau.NewMethodDeclaration(
		name, constructorMethodName, parameters, hasDotDotDot, statements))
}

// anyIdentifierName returns the rendered-name field of an Identifier or
// TemporaryIdentifier (upstream reads `.name` off the AnyIdentifier union).
func anyIdentifierName(id luau.AnyIdentifier) string {
	switch id := id.(type) {
	case *luau.Identifier:
		return id.Name
	case *luau.TemporaryIdentifier:
		return id.Name
	}
	panic("transformer: anyIdentifierName: not an identifier")
}

// transformClassPropertyDeclaration ports nodes/class/
// transformPropertyDeclaration.ts (L9-37): one `name[<key>] = <value>`
// assignment per STATIC PropertyDeclaration with an initializer; both
// transforms run inline so their prereqs flow to the caller's capture (index
// prereqs before value prereqs).
func transformClassPropertyDeclaration(s *State, node *ast.Node, name luau.AnyIdentifier) *luau.List[luau.Statement] {
	if !ast.HasStaticModifier(node) {
		return luau.NewList[luau.Statement]()
	}

	if ast.IsPrivateIdentifier(node.Name()) {
		s.Diags.Add(DiagNoPrivateIdentifier(node))
		return luau.NewList[luau.Statement]()
	}

	if node.Initializer() == nil {
		return luau.NewList[luau.Statement]()
	}

	return luau.NewList[luau.Statement](luau.NewAssignment(
		luau.NewComputedIndex(name, transformPropertyName(s, node.Name())),
		"=",
		TransformExpression(s, node.Initializer()),
	))
}

// transformClassLikeDeclaration ports nodes/class/
// transformClassLikeDeclaration.ts (L199-384). Returns the statements and the
// luau identifier holding the class (the temp `_class` for a named class
// expression, the class name otherwise, `default` for an unnamed
// export-default class).
func transformClassLikeDeclaration(s *State, node *ast.Node) (*luau.List[luau.Statement], luau.AnyIdentifier) {
	isClassExpression := ast.IsClassExpression(node)
	statements := luau.NewList[luau.Statement]()

	isExportDefault := ast.HasSyntacticModifier(node, ast.ModifierFlagsExportDefault)

	if name := node.Name(); name != nil {
		ValidateIdentifier(s, name)
	}

	/*
		local className
		do
			OOP boilerplate
			class functions
		end
	*/

	shouldUseInternalName := isClassExpression && node.Name() != nil

	var returnVar luau.AnyIdentifier
	if shouldUseInternalName {
		returnVar = luau.TempID("class")
	} else if node.Name() != nil {
		returnVar = TransformIdentifierDefined(s, node.Name())
	} else if isExportDefault {
		returnVar = luau.ID("default")
	} else {
		returnVar = luau.TempID("class")
	}

	var internalName luau.AnyIdentifier
	if shouldUseInternalName {
		internalName = TransformIdentifierDefined(s, node.Name())
	} else {
		internalName = returnVar
	}
	s.ClassIdentifierMap[node] = internalName
	if !isClassHoisted(s, node) {
		statements.Push(luau.NewVariableDeclaration(returnVar, nil))
	}

	// OOP boilerplate + class functions
	statementsInner := luau.NewList[luau.Statement]()
	statementsInner.PushList(createClassBoilerplate(s, node, internalName, isClassExpression))

	if constructor := findConstructor(node); constructor != nil {
		statementsInner.PushList(transformClassConstructor(s, constructor, internalName))
	} else {
		statementsInner.PushList(transformImplicitClassConstructor(s, node, internalName))
	}

	for _, member := range node.Members() {
		if (ast.IsPropertyDeclaration(member) || ast.IsMethodDeclaration(member)) &&
			(ast.IsIdentifier(member.Name()) || ast.IsStringLiteral(member.Name())) &&
			luau.IsReservedClassField(member.Name().Text()) {
			s.Diags.Add(DiagNoReservedClassFields(member.Name()))
		}
		if ast.IsAutoAccessorPropertyDeclaration(member) {
			// member must have AccessorKeyword to be AutoAccessorPropertyDeclaration
			var keyword *ast.Node
			for _, modifier := range member.Modifiers().Nodes {
				if modifier.Kind == ast.KindAccessorKeyword {
					keyword = modifier
					break
				}
			}
			s.Diags.Add(DiagNoAutoAccessorModifiers(keyword))
		}
	}

	var methods []*ast.Node
	var staticDeclarations []*ast.Node

	for _, member := range node.Members() {
		validateMethodAssignment(s, member)
		switch {
		case ast.IsConstructorDeclaration(member),
			ast.IsIndexSignatureDeclaration(member),
			ast.IsSemicolonClassElement(member):
			continue
		case ast.IsMethodDeclaration(member):
			methods = append(methods, member)
		case ast.IsPropertyDeclaration(member):
			// non-static properties are done in transformClassConstructor
			if !ast.HasStaticModifier(member) {
				continue
			}
			staticDeclarations = append(staticDeclarations, member)
		case ast.IsAccessor(member):
			s.Diags.Add(DiagNoGetterSetter(member))
		case ast.IsClassStaticBlockDeclaration(member):
			staticDeclarations = append(staticDeclarations, member)
		default:
			panic("transformer: ClassMember kind not implemented: " + kindName(member.Kind)) // upstream assert
		}
	}

	classType := s.Checker.GetTypeOfSymbolAtLocation(node.Symbol(), node)
	instanceType := s.Checker.GetDeclaredTypeOfSymbol(node.Symbol())

	for _, method := range methods {
		if ast.IsIdentifier(method.Name()) || ast.IsStringLiteral(method.Name()) {
			if luau.IsMetamethod(method.Name().Text()) {
				s.Diags.Add(DiagNoClassMetamethods(method.Name()))
			}

			if ast.HasStaticModifier(method) {
				if s.Checker.GetPropertyOfType(instanceType, method.Name().Text()) != nil {
					s.Diags.Add(DiagNoInstanceMethodCollisions(method))
				}
			} else {
				if s.Checker.GetPropertyOfType(classType, method.Name().Text()) != nil {
					s.Diags.Add(DiagNoStaticMethodCollisions(method))
				}
			}
		}

		methodStatements := s.CaptureStatements(func() {
			// statements and prereqs are concatenated in order, so one capture
			// covers upstream's [statements, prereqs] split.
			s.PrereqList(transformMethodDeclaration(s, method, &MapPointer{Name: "name", Value: internalName}))
		})
		statementsInner.PushList(methodStatements)
	}

	// QUIRK: getPropertyOfType sees INHERITED members, so every subclass whose
	// instance type carries a toString METHOD (symbol flag check excludes
	// function-typed properties) re-emits the __tostring wrapper.
	toStringProperty := s.Checker.GetPropertyOfType(instanceType, magicToStringMethod)
	if toStringProperty != nil && toStringProperty.Flags&ast.SymbolFlagsMethod != 0 {
		statementsInner.Push(luau.NewMethodDeclaration(
			internalName,
			"__tostring",
			luau.NewList[luau.AnyIdentifier](),
			false,
			luau.NewList[luau.Statement](luau.NewReturn(
				luau.NewMethodCall(magicToStringMethod, luau.GlobalID("self"), luau.NewList[luau.Expression]()),
			)),
		))
	}

	for _, declaration := range staticDeclarations {
		if ast.IsClassStaticBlockDeclaration(declaration) {
			statementsInner.PushList(transformBlock(s, declaration.AsClassStaticBlockDeclaration().Body))
		} else {
			declarationStatements := s.CaptureStatements(func() {
				s.PrereqList(transformClassPropertyDeclaration(s, declaration, internalName))
			})
			statementsInner.PushList(declarationStatements)
		}
	}

	// if using internal name, assign to return var
	if shouldUseInternalName {
		statementsInner.Push(luau.NewAssignment(returnVar, "=", internalName))
	}

	statementsInner.PushList(transformDecorators(s, node, returnVar))

	statements.Push(luau.NewDo(statementsInner))

	return statements, returnVar
}

// transformClassDeclaration ports statements/transformClassDeclaration.ts.
func transformClassDeclaration(s *State, node *ast.Node) *luau.List[luau.Statement] {
	statements, _ := transformClassLikeDeclaration(s, node)
	return statements
}

// transformClassExpression ports expressions/transformClassExpression.ts.
func transformClassExpression(s *State, node *ast.Node) luau.Expression {
	statements, name := transformClassLikeDeclaration(s, node)
	s.PrereqList(statements)
	return name
}

// transformThisExpression ports expressions/transformThisExpression.ts
// (L7-29): `this` is `self`, EXCEPT inside static blocks and static property
// initializers where it is the class's internal identifier (static METHODS
// still use `self` — the `:` declaration form binds it).
func transformThisExpression(s *State, node *ast.Node) luau.Expression {
	symbol := s.Checker.GetSymbolAtLocation(node)
	if symbol != nil && isGlobalThisSymbol(symbol) {
		s.Diags.Add(DiagNoGlobalThis(node))
	}

	if symbol != nil {
		container := ast.GetThisContainer(node, false, false)

		// ts.hasStaticModifier doesn't work on static blocks
		isStatic := ast.HasStaticModifier(container) || ast.IsClassStaticBlockDeclaration(container)

		// MethodDeclaration creates it's own implicit this
		if isStatic && !ast.IsMethodDeclaration(container) &&
			container.Parent != nil && ast.IsClassLike(container.Parent) {
			if identifier := s.ClassIdentifierMap[container.Parent]; identifier != nil {
				return identifier
			}
		}
	}

	return luau.GlobalID("self")
}

// transformSuperKeyword ports expressions/transformSuperKeyword.ts: bare
// `super` is the plain identifier the boilerplate's `local super = Base`
// declared (in scope inside the class do-block).
func transformSuperKeyword() luau.Expression {
	return luau.GlobalID("super")
}

// validateHeritageClause ports util/validateMethodAssignment.ts
// validateHeritageClause (L37-46): compare a class element's type against the
// same-named property of one heritage type.
func validateHeritageClause(s *State, node *ast.Node, typeNode *ast.Node) {
	name := ast.GetPropertyNameForPropertyNameNode(node.Name())
	if name == ast.InternalSymbolNameMissing {
		// upstream ts.getPropertyNameForPropertyNameNode returns undefined for
		// late-bound computed names
		return
	}

	t := s.GetType(node)
	propertyType := s.Checker.GetTypeOfPropertyOfType(s.GetType(typeNode), name)
	if propertyType == nil {
		return
	}

	validateTypes(s, node, t, propertyType)
}
