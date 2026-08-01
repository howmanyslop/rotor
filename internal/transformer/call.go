package transformer

import (
	"fmt"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// This file ports expressions/transformCallExpression.ts,
// nodes/transformOptionalChain.ts (flatten + dispatch; the optional path
// lives in optionalchain.go), and their utils:
// util/convertToIndexableExpression.ts, util/expressionMightMutate.ts,
// util/wrapReturnIfLuaTuple.ts, util/arrayBindingPatternContainsHoists.ts.
//
// Macro hook points query the MacroManager (macromanager.go); entries run
// through runCallMacro. All upstream macro tables are implemented (Phase 3b
// Task 5); the nil-Macro branches below are defensive — a registered entry
// with a nil Macro would mean a known upstream macro rotor has not
// implemented, raising rotorNotYetSupported instead of silently-wrong
// output.

// convertToIndexableExpression ports util/convertToIndexableExpression.ts:
// wrap non-indexable expressions in parentheses so they can be indexed or
// called.
func convertToIndexableExpression(expression luau.Expression) luau.IndexableExpression {
	if luau.IsIndexableExpression(expression) {
		return expression.(luau.IndexableExpression)
	}
	return luau.NewParenthesized(expression)
}

// ---------------------------------------------------------------------------
// expressionMightMutate — util/expressionMightMutate.ts (COMPLETE)
// ---------------------------------------------------------------------------

// expressionMightMutate reports whether the rendered luau expression could
// change value if statements run between its computation and its use. node is
// the originating TS expression (optional): reads of const identifiers cannot
// change.
func expressionMightMutate(s *State, expression luau.Expression, node *ast.Node) bool {
	switch e := expression.(type) {
	case *luau.TemporaryIdentifier:
		// Assume tempIds are never re-assigned after being returned
		return false
	case *luau.ParenthesizedExpression:
		return expressionMightMutate(s, e.Expression, nil)
	case *luau.FunctionExpression:
		return false
	case *luau.VarArgsLiteral:
		return false
	case *luau.IfExpression:
		return expressionMightMutate(s, e.Condition, nil) ||
			expressionMightMutate(s, e.Expression, nil) ||
			expressionMightMutate(s, e.Alternative, nil)
	case *luau.BinaryExpression:
		return expressionMightMutate(s, e.Left, nil) || expressionMightMutate(s, e.Right, nil)
	case *luau.UnaryExpression:
		return expressionMightMutate(s, e.Expression, nil)
	case *luau.Array:
		return e.Members.Some(func(member luau.Expression) bool { return expressionMightMutate(s, member, nil) })
	case *luau.Set:
		return e.Members.Some(func(member luau.Expression) bool { return expressionMightMutate(s, member, nil) })
	case *luau.Map:
		return e.Fields.Some(func(field *luau.MapField) bool {
			return expressionMightMutate(s, field.Index, nil) || expressionMightMutate(s, field.Value, nil)
		})
	}

	if luau.IsSimplePrimitive(expression) {
		return false
	}

	if node != nil {
		node = SkipDownwards(node)
		if ast.IsIdentifier(node) {
			if symbol := s.Checker.GetSymbolAtLocation(node); symbol != nil && !IsSymbolMutable(s, symbol) {
				return false
			}
		}
	}

	// Identifier / ComputedIndexExpression / PropertyAccessExpression /
	// CallExpression / MethodCallExpression
	return true
}

// ---------------------------------------------------------------------------
// wrapReturnIfLuaTuple — util/wrapReturnIfLuaTuple.ts
// ---------------------------------------------------------------------------

// arrayBindingPatternContainsHoists ports
// util/arrayBindingPatternContainsHoists.ts: does any identifier bound by the
// pattern require hoisting?
func arrayBindingPatternContainsHoists(s *State, arrayBindingPattern *ast.Node) bool {
	for _, element := range arrayBindingPattern.AsBindingPattern().Elements.Nodes {
		// If it's not a BindingElement, it must be an OmittedExpression — no variable.
		// Nested binding patterns get temp ids; their hoisting is handled elsewhere.
		if ast.IsBindingElement(element) {
			name := element.Name()
			if name != nil && ast.IsIdentifier(name) {
				if symbol := s.Checker.GetSymbolAtLocation(name); symbol != nil {
					// isHoisted is marked inside checkVariableHoist
					checkVariableHoist(s, name, symbol)
					if s.IsHoisted[symbol] {
						return true
					}
				}
			}
		}
	}
	return false
}

// shouldWrapLuaTuple ports wrapReturnIfLuaTuple.ts shouldWrapLuaTuple
// (L8-56): a LuaTuple-typed call is packed `{ f() }` UNLESS the syntactic
// context consumes the multiple values.
func shouldWrapLuaTuple(s *State, node *ast.Node, exp luau.Expression) bool {
	if !luau.IsCall(exp) {
		return true
	}

	child := SkipUpwards(node)
	parent := child.Parent
	if parent == nil {
		return true
	}

	// `foo();`
	if ast.IsExpressionStatement(parent) {
		return false
	}

	// if part of for statement definition, except if used as the condition
	if ast.IsForStatement(parent) && parent.AsForStatement().Condition != child {
		return false
	}

	// `const [a] = foo()`
	if ast.IsVariableDeclaration(parent) {
		name := parent.AsVariableDeclaration().Name()
		if name != nil && ast.IsArrayBindingPattern(name) &&
			!arrayBindingPatternContainsHoists(s, name) &&
			!arrayLikeExpressionContainsSpread(name) &&
			node.AsCallExpression().QuestionDotToken == nil {
			return false
		}
	}

	// `[a] = foo()`
	if ast.IsAssignmentExpression(parent, false) && ast.IsArrayLiteralExpression(parent.AsBinaryExpression().Left) {
		left := parent.AsBinaryExpression().Left
		if !arrayLikeExpressionContainsSpread(left) && node.AsCallExpression().QuestionDotToken == nil {
			return false
		}
	}

	// `foo()[n]`
	if ast.IsElementAccessExpression(parent) && node.AsCallExpression().QuestionDotToken == nil {
		return false
	}

	// `return foo()`
	if ast.IsReturnStatement(parent) {
		return false
	}

	// `void foo()`
	if ast.IsVoidExpression(parent) {
		return false
	}

	return true
}

// wrapReturnIfLuaTuple ports wrapReturnIfLuaTuple.ts (L58-63).
func wrapReturnIfLuaTuple(s *State, node *ast.Node, exp luau.Expression) luau.Expression {
	if IsLuaTupleType(s).Check(s.Checker.GetNonNullableType(s.GetType(node))) && shouldWrapLuaTuple(s, node, exp) {
		return luau.NewArray(luau.NewList[luau.Expression](exp))
	}
	return exp
}

// ---------------------------------------------------------------------------
// runCallMacro — transformCallExpression.ts L21-83
// ---------------------------------------------------------------------------

// runCallMacro ports runCallMacro: transform the arguments (expanding a
// trailing tuple-typed spread into temp ids; vararg macros reject spreads),
// push mutable arguments and the base expression to temps when prereqs
// intervene, then run the macro. The macro parameter is an unnamed func type
// so both CallMacro and PropertyCallMacro values convert implicitly (upstream
// types the parameter `CallMacro | PropertyCallMacro`).
func runCallMacro(
	macro func(*State, *ast.Node, luau.Expression, []luau.Expression) luau.Expression,
	s *State,
	node *ast.Node,
	expression luau.Expression,
	nodeArguments []*ast.Node,
) luau.Expression {
	call := node.AsCallExpression()

	var args []luau.Expression
	prereqs := s.CaptureStatements(func() {
		args = ensureTransformOrder(s, nodeArguments)
		if len(nodeArguments) > 0 {
			if lastArg := nodeArguments[len(nodeArguments)-1]; ast.IsSpreadElement(lastArg) {
				signature := s.Checker.GetSignaturesOfType(s.GetType(call.Expression), checker.SignatureKindCall)[0]
				parameters := signature.Parameters()

				lastParameter := parameters[len(parameters)-1].ValueDeclaration
				if lastParameter != nil && ast.IsParameterDeclaration(lastParameter) && lastParameter.AsParameterDeclaration().DotDotDotToken != nil {
					s.Diags.Add(DiagNoVarArgsMacroSpread(lastArg))
					return
				}

				// use .expression for the tuple type, simply `lastArg` would
				// give the tuple's element type
				tupleArgType := s.GetType(lastArg.AsSpreadElement().Expression)
				// Since we've excluded vararg macros, TS will have ensured
				// that the spread is from a tuple type
				if !tupleArgType.IsTupleType() {
					panic("transformer: runCallMacro spread argument is not a tuple type") // upstream assert
				}
				argumentCount := len(tupleArgType.TargetTupleType().ElementFlags())

				spread := args[len(args)-1]
				args = args[:len(args)-1]
				tempIds := luau.NewList[luau.AnyIdentifier]()
				for i := len(args); i < argumentCount; i++ {
					tempID := luau.TempID(fmt.Sprintf("spread%d", i))
					args = append(args, tempID)
					tempIds.Push(tempID)
				}
				s.Prereq(luau.NewVariableDeclaration(tempIds, spread))
			}
		}

		for i := range args {
			// spread-expanded args extend past nodeArguments; upstream indexes
			// past the array end (undefined), Go passes nil.
			var nodeArg *ast.Node
			if i < len(nodeArguments) {
				nodeArg = nodeArguments[i]
			}
			if expressionMightMutate(s, args[i], nodeArg) {
				name := ValueToIdStr(args[i])
				if name == "" {
					name = fmt.Sprintf("arg%d", i)
				}
				args[i] = s.PushToVar(args[i], name)
			}
		}
	})

	nodeExpression := call.Expression
	if ast.IsPropertyAccessExpression(nodeExpression) || ast.IsElementAccessExpression(nodeExpression) {
		nodeExpression = nodeExpression.Expression()
	}

	if prereqs.IsNonEmpty() && expressionMightMutate(s, expression, nodeExpression) {
		name := ValueToIdStr(expression)
		if name == "" {
			name = "exp"
		}
		expression = s.PushToVar(expression, name)
	}
	s.PrereqList(prereqs)

	return wrapReturnIfLuaTuple(s, node, macro(s, node, expression, args))
}

// ---------------------------------------------------------------------------
// fixVoidArgumentsForRobloxFunctions — transformCallExpression.ts L96-113
// ---------------------------------------------------------------------------

// fixVoidArgumentsForRobloxFunctions wraps possibly-undefined call-expression
// arguments of Roblox API functions in parentheses: `(foo())` truncates Lua
// multi-returns/void so C functions like `tonumber()` don't error on zero
// values.
func fixVoidArgumentsForRobloxFunctions(s *State, expType *checker.Type, args []luau.Expression, nodeArguments []*ast.Node) {
	if !IsPossiblyType(s, expType, IsRobloxType(s)) {
		return
	}
	for i, nodeArg := range nodeArguments {
		if ast.IsCallExpression(nodeArg) && IsPossiblyType(s, s.GetType(nodeArg), IsUndefinedType) {
			args[i] = luau.NewParenthesized(args[i])
		}
	}
}

func tryTransformOptimizableVarArgsSizeCall(s *State, node *ast.Node) luau.Expression {
	call := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}

	propertyAccess := call.Expression.AsPropertyAccessExpression()
	if propertyAccess.Name().Text() != "size" {
		return nil
	}

	data := s.getOptimizableVarArgsData(propertyAccess.Expression)
	if data == nil || !data.IsOptimizable {
		return nil
	}
	if data.usesLengthCache() {
		return data.LengthID
	}
	return createVarArgsLengthSelect()
}

// ---------------------------------------------------------------------------
// The three call transforms — transformCallExpression.ts
// ---------------------------------------------------------------------------

// transformCallExpressionInner ports transformCallExpressionInner (L115-155)
// — plain `f(...)`.
func transformCallExpressionInner(s *State, node *ast.Node, expression luau.Expression, nodeArguments []*ast.Node) luau.Expression {
	if ast.IsImportCall(node) {
		return transformImportExpression(s, node)
	}

	call := node.AsCallExpression()

	// a in a()
	validateNotAnyType(s, call.Expression)

	if ast.IsSuperCall(node) {
		// super(...) -> super.constructor(self, <args>)
		return luau.NewCall(
			luau.NewPropertyAccess(convertToIndexableExpression(expression), "constructor"),
			luau.NewList(append([]luau.Expression{luau.GlobalID("self")},
				ensureTransformOrder(s, call.Arguments.Nodes)...)...),
		)
	}
	if expression := tryTransformOptimizableVarArgsSizeCall(s, node); expression != nil {
		return expression
	}

	expType := s.Checker.GetNonOptionalType(s.GetType(call.Expression))
	if symbol := GetFirstDefinedSymbol(s, expType); symbol != nil {
		if entry := s.Macros().GetCallMacro(symbol); entry != nil {
			if entry.Macro != nil {
				return runCallMacro(entry.Macro, s, node, expression, nodeArguments)
			}
			// nil Macro: known upstream macro not yet ported (Phase 3b).
			s.Diags.Add(DiagRotorNotYetSupported(node, "macro `"+entry.Name+"`"))
			return luau.NewNone()
		}
	}

	var args []luau.Expression
	prereqs := s.CaptureStatements(func() { args = ensureTransformOrder(s, nodeArguments) })
	fixVoidArgumentsForRobloxFunctions(s, expType, args, nodeArguments)

	if prereqs.IsNonEmpty() && expressionMightMutate(s, expression, call.Expression) {
		expression = s.PushToVar(expression, "fn")
	}
	s.PrereqList(prereqs)

	exp := luau.NewCall(convertToIndexableExpression(expression), luau.NewList(args...))

	return wrapReturnIfLuaTuple(s, node, exp)
}

// transformPropertyCallExpressionInner ports
// transformPropertyCallExpressionInner (L157-215) — `a.b(...)`. expression is
// the ts PropertyAccessExpression; baseExpression the already-transformed `a`.
func transformPropertyCallExpressionInner(s *State, node *ast.Node, expression *ast.Node, baseExpression luau.Expression, name string, nodeArguments []*ast.Node) luau.Expression {
	propertyAccess := expression.AsPropertyAccessExpression()
	call := node.AsCallExpression()

	// a in a.b()
	validateNotAnyType(s, propertyAccess.Expression)
	// a.b in a.b()
	validateNotAnyType(s, call.Expression)

	if ast.IsSuperProperty(expression) {
		// QUIRK: super method calls pass self explicitly — dot-call, never `:`.
		return luau.NewCall(
			luau.NewPropertyAccess(convertToIndexableExpression(baseExpression), propertyAccess.Name().Text()),
			luau.NewList(append([]luau.Expression{luau.GlobalID("self")},
				ensureTransformOrder(s, call.Arguments.Nodes)...)...),
		)
	}
	if optimized := tryTransformOptimizableVarArgsSizeCall(s, node); optimized != nil {
		return optimized
	}

	expType := s.Checker.GetNonOptionalType(s.GetType(call.Expression))
	if symbol := GetFirstDefinedSymbol(s, expType); symbol != nil {
		if entry := s.Macros().GetPropertyCallMacro(symbol); entry != nil {
			if entry.Macro != nil {
				return runCallMacro(entry.Macro, s, node, baseExpression, nodeArguments)
			}
			// nil Macro: known upstream macro not yet ported (Phase 3b).
			s.Diags.Add(DiagRotorNotYetSupported(node, "macro `"+entry.Name+"`"))
			return luau.NewNone()
		}
	}

	var args []luau.Expression
	prereqs := s.CaptureStatements(func() { args = ensureTransformOrder(s, nodeArguments) })
	fixVoidArgumentsForRobloxFunctions(s, expType, args, nodeArguments)

	if prereqs.IsNonEmpty() && expressionMightMutate(s, baseExpression, propertyAccess.Expression) {
		baseExpression = s.PushToVar(baseExpression, "")
	}
	s.PrereqList(prereqs)

	var exp luau.Expression
	if isMethod(s, expression) {
		// check that the name isn't a Luau keyword
		// if it is, we need to use PropertyAccessExpression and manually add the self argument
		if luau.IsValidIdentifier(name) {
			exp = luau.NewMethodCall(name, convertToIndexableExpression(baseExpression), luau.NewList(args...))
		} else {
			baseExpression = s.PushToVarIfComplex(baseExpression, "")
			args = append([]luau.Expression{baseExpression}, args...)
			exp = luau.NewCall(
				luau.NewPropertyAccess(convertToIndexableExpression(baseExpression), name),
				luau.NewList(args...),
			)
		}
	} else {
		// PropertyAccessExpression will wrap the identifier for us if necessary
		exp = luau.NewCall(
			luau.NewPropertyAccess(convertToIndexableExpression(baseExpression), name),
			luau.NewList(args...),
		)
	}

	return wrapReturnIfLuaTuple(s, node, exp)
}

// transformElementCallExpressionInner ports
// transformElementCallExpressionInner (L217-280) — `a[b](...)`. NOTE the
// index expression is ordered WITH the arguments, and `a[b]` can never use
// `:` sugar: methods always get an explicit self argument.
func transformElementCallExpressionInner(s *State, node *ast.Node, expression *ast.Node, baseExpression luau.Expression, argumentExpression *ast.Node, nodeArguments []*ast.Node) luau.Expression {
	elementAccess := expression.AsElementAccessExpression()
	call := node.AsCallExpression()

	// a in a[b]()
	validateNotAnyType(s, elementAccess.Expression)
	// b in a[b]()
	validateNotAnyType(s, elementAccess.ArgumentExpression)
	// a[b] in a[b]()
	validateNotAnyType(s, call.Expression)

	if ast.IsSuperProperty(expression) {
		// QUIRK: no addOneIfArrayType here (upstream omits it for super
		// element calls); self is explicit, never `:`.
		return luau.NewCall(
			luau.NewComputedIndex(
				convertToIndexableExpression(baseExpression),
				TransformExpression(s, elementAccess.ArgumentExpression),
			),
			luau.NewList(append([]luau.Expression{luau.GlobalID("self")},
				ensureTransformOrder(s, call.Arguments.Nodes)...)...),
		)
	}
	if optimized := tryTransformOptimizableVarArgsSizeCall(s, node); optimized != nil {
		return optimized
	}

	expType := s.Checker.GetNonOptionalType(s.GetType(call.Expression))
	if symbol := GetFirstDefinedSymbol(s, expType); symbol != nil {
		if entry := s.Macros().GetPropertyCallMacro(symbol); entry != nil {
			if entry.Macro != nil {
				return runCallMacro(entry.Macro, s, node, baseExpression, nodeArguments)
			}
			// nil Macro: known upstream macro not yet ported (Phase 3b).
			s.Diags.Add(DiagRotorNotYetSupported(node, "macro `"+entry.Name+"`"))
			return luau.NewNone()
		}
	}

	var ordered []luau.Expression
	prereqs := s.CaptureStatements(func() {
		ordered = ensureTransformOrder(s, append([]*ast.Node{argumentExpression}, nodeArguments...))
	})
	argumentExp, args := ordered[0], ordered[1:]

	fixVoidArgumentsForRobloxFunctions(s, expType, args, nodeArguments)

	if prereqs.IsNonEmpty() && expressionMightMutate(s, baseExpression, elementAccess.Expression) {
		baseExpression = s.PushToVar(baseExpression, "")
	}
	s.PrereqList(prereqs)

	if isMethod(s, expression) {
		baseExpression = s.PushToVarIfComplex(baseExpression, "")
		args = append([]luau.Expression{baseExpression}, args...)
	}

	exp := luau.NewCall(
		luau.NewComputedIndex(
			convertToIndexableExpression(baseExpression),
			addOneIfArrayType(s,
				s.Checker.GetNonOptionalType(s.GetType(elementAccess.Expression)),
				argumentExp),
		),
		luau.NewList(args...),
	)

	return wrapReturnIfLuaTuple(s, node, exp)
}

// transformCallExpression ports transformCallExpression (L282-284): every
// call routes through the optional-chain walker.
func transformCallExpression(s *State, node *ast.Node) luau.Expression {
	return transformOptionalChain(s, node)
}

// ---------------------------------------------------------------------------
// Optional chain — nodes/transformOptionalChain.ts (flatten + item dispatch;
// transformOptionalChain[Inner] live in optionalchain.go)
// ---------------------------------------------------------------------------

type chainItemKind int

const (
	chainPropertyAccess chainItemKind = iota
	chainElementAccess
	chainCall
	chainPropertyCall
	chainElementCall
)

// chainItem ports the OptionalChainItem family (L24-66). The eager type
// snapshots upstream records (`type`/`callType`) are vestigial — nothing
// reads them; transformOptionalChainInner re-queries the (memoized) checker
// live — so they are omitted.
type chainItem struct {
	kind     chainItemKind
	node     *ast.Node // the PropertyAccess/ElementAccess/Call expression
	optional bool

	name               string    // PropertyAccess / PropertyCall
	argumentExpression *ast.Node // ElementAccess / ElementCall

	// compound call items (PropertyCall / ElementCall)
	expression   *ast.Node // the inner access expression
	callOptional bool
	args         []*ast.Node
}

// flattenOptionalChain ports flattenOptionalChain (L135-162): walk down
// `.expression` collecting chain items; a CallExpression whose callee (skipped
// downwards) is a property/element access becomes a compound item.
func flattenOptionalChain(expression *ast.Node) ([]chainItem, *ast.Node) {
	var chain []chainItem // built outer-to-inner, reversed below (upstream unshifts)
	for {
		if ast.IsPropertyAccessExpression(expression) {
			propertyAccess := expression.AsPropertyAccessExpression()
			chain = append(chain, chainItem{
				kind:     chainPropertyAccess,
				node:     expression,
				optional: propertyAccess.QuestionDotToken != nil,
				name:     propertyAccess.Name().Text(),
			})
			expression = propertyAccess.Expression
		} else if ast.IsElementAccessExpression(expression) {
			elementAccess := expression.AsElementAccessExpression()
			chain = append(chain, chainItem{
				kind:               chainElementAccess,
				node:               expression,
				optional:           elementAccess.QuestionDotToken != nil,
				argumentExpression: elementAccess.ArgumentExpression,
			})
			expression = elementAccess.Expression
		} else if ast.IsCallExpression(expression) {
			// this is a bit of a mess..
			call := expression.AsCallExpression()
			subExp := SkipDownwards(call.Expression)
			if ast.IsPropertyAccessExpression(subExp) {
				propertyAccess := subExp.AsPropertyAccessExpression()
				chain = append(chain, chainItem{
					kind:         chainPropertyCall,
					node:         expression,
					expression:   subExp,
					optional:     propertyAccess.QuestionDotToken != nil,
					name:         propertyAccess.Name().Text(),
					callOptional: call.QuestionDotToken != nil,
					args:         call.Arguments.Nodes,
				})
				expression = propertyAccess.Expression
			} else if ast.IsElementAccessExpression(subExp) {
				elementAccess := subExp.AsElementAccessExpression()
				chain = append(chain, chainItem{
					kind:               chainElementCall,
					node:               expression,
					expression:         subExp,
					optional:           elementAccess.QuestionDotToken != nil,
					argumentExpression: elementAccess.ArgumentExpression,
					callOptional:       call.QuestionDotToken != nil,
					args:               call.Arguments.Nodes,
				})
				expression = elementAccess.Expression
			} else {
				chain = append(chain, chainItem{
					kind:     chainCall,
					node:     expression,
					optional: call.QuestionDotToken != nil,
					args:     call.Arguments.Nodes,
				})
				expression = subExp
			}
		} else {
			break
		}
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, expression
}

// transformChainItem ports transformChainItem (L164-190): dispatch to the
// matching Inner transform.
func transformChainItem(s *State, baseExpression luau.Expression, item chainItem) luau.Expression {
	switch item.kind {
	case chainPropertyAccess:
		return transformPropertyAccessExpressionInner(s, item.node, baseExpression, item.name)
	case chainElementAccess:
		return transformElementAccessExpressionInner(s, item.node, baseExpression, item.argumentExpression)
	case chainCall:
		return transformCallExpressionInner(s, item.node, baseExpression, item.args)
	case chainPropertyCall:
		return transformPropertyCallExpressionInner(s, item.node, item.expression, baseExpression, item.name, item.args)
	default: // chainElementCall
		return transformElementCallExpressionInner(s, item.node, item.expression, baseExpression, item.argumentExpression, item.args)
	}
}
