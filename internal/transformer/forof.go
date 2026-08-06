package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// ---------------------------------------------------------------------------
// for-of — statements/transformForOfStatement.ts (COMPLETE)
// ---------------------------------------------------------------------------

// loopBuilder ports `type LoopBuilder` (L34-39): statements is the
// already-transformed body; the builder wraps it in the loop shape.
type loopBuilder func(s *State, statements *luau.List[luau.Statement], initializer *ast.Node, exp luau.Expression) *luau.List[luau.Statement]

// makeForLoopBuilder ports makeForLoopBuilder (L41-57): the callback fills
// `ids` (the generic-for binding list) and `initializers` (statements to
// prepend to the body) and returns the for-expression.
func makeForLoopBuilder(callback func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression) loopBuilder {
	return func(s *State, statements *luau.List[luau.Statement], initializer *ast.Node, exp luau.Expression) *luau.List[luau.Statement] {
		ids := luau.NewList[luau.AnyIdentifier]()
		initializers := luau.NewList[luau.Statement]()
		expression := callback(s, initializer, exp, ids, initializers)
		statements.UnshiftList(initializers)
		return luau.NewList[luau.Statement](luau.NewFor(ids, expression, statements))
	}
}

// transformForInitializerExpressionDirect ports
// transformForInitializerExpressionDirect (L59-92): bind a KNOWN value
// expression through the initializer (used by the Map and generator builders,
// whose loop bindings are protocol temps rather than the user's binding).
// Array/object literal targets destructure a `_binding` temp pushed to var
// from the value; any other writable target gets a direct assignment.
func transformForInitializerExpressionDirect(s *State, initializer *ast.Node, initializers *luau.List[luau.Statement], value luau.Expression) {
	if ast.IsArrayLiteralExpression(initializer) {
		initializers.PushList(s.CaptureStatements(func() {
			parentID := s.PushToVar(value, "binding")
			transformArrayAssignmentPattern(s, initializer, parentID)
		}))
	} else if ast.IsObjectLiteralExpression(initializer) {
		initializers.PushList(s.CaptureStatements(func() {
			parentID := s.PushToVar(value, "binding")
			transformObjectAssignmentPattern(s, initializer, parentID)
		}))
	} else {
		expression := transformWritableExpression(s, initializer, false)
		initializers.Push(luau.NewAssignment(expression, "=", value))
	}
}

// transformForInitializer ports transformForOfStatement.ts
// transformForInitializer (L94-128): produce the single identifier bound by
// the loop, prepending any binding statements to the body via initializers.
func transformForInitializer(s *State, initializer *ast.Node, initializers *luau.List[luau.Statement]) luau.AnyIdentifier {
	if ast.IsVariableDeclarationList(initializer) {
		return transformBindingName(s, initializer.AsVariableDeclarationList().Declarations.Nodes[0].Name(), initializers)
	} else if ast.IsArrayLiteralExpression(initializer) {
		// `for ([a, b] of ...)` — destructure a `_binding` temp inside the body.
		parentID := luau.TempID("binding")
		initializers.PushList(s.CaptureStatements(func() {
			transformArrayAssignmentPattern(s, initializer, parentID)
		}))
		return parentID
	} else if ast.IsObjectLiteralExpression(initializer) {
		parentID := luau.TempID("binding")
		initializers.PushList(s.CaptureStatements(func() {
			transformObjectAssignmentPattern(s, initializer, parentID)
		}))
		return parentID
	}
	// `for (x of ...)`, `for (a.b of ...)` — a `_v` temp is the loop binding,
	// assigned through the writable target at the top of the body.
	valueID := luau.TempID("v")
	expression := transformWritableExpression(s, initializer, false)
	initializers.Push(luau.NewAssignment(expression, "=", valueID))
	return valueID
}

// buildArrayLoop ports buildArrayLoop (L130-134): `for _, x in exp do` — the
// first generic-for binding is an unnamed temp (renders `_` when
// unreferenced); there is no ipairs in 3.0, generalized iteration directly
// over the array expression. Pattern initializers destructure inside the body
// (`for _, _binding in exp do local a = _binding[1] ... end`).
var buildArrayLoop = makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
	ids.Push(luau.TempID(""))
	ids.Push(transformForInitializer(s, initializer, initializers))
	return exp
})

// buildSetLoop ports buildSetLoop (L136-139): `for x in exp do` — Luau's
// generic iteration over a Set yields the element as the first binding.
var buildSetLoop = makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
	ids.Push(transformForInitializer(s, initializer, initializers))
	return exp
})

// transformInLineArrayBindingPattern ports transformInLineArrayBindingPattern
// (L141-160): the `for (const [k, v] of map)` fast path — each pattern
// element becomes a generic-for binding DIRECTLY (no `_binding` temp).
// Omitted elements get an unnamed temp; defaults are nil-checked in the body;
// nested patterns route through transformBindingName (which introduces a
// `_binding` temp just for that slot). NOTE upstream tests
// ts.isSpreadElement, which never matches ArrayBindingPattern elements (rest
// arrives as a BindingElement with dotDotDotToken) — the rest check is dead
// upstream and a rest element falls through to transformBindingName; ported
// verbatim.
func transformInLineArrayBindingPattern(s *State, pattern *ast.Node, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) {
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		if isOmittedBindingElement(element) {
			ids.Push(luau.TempID(""))
		} else if ast.IsSpreadElement(element) {
			s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
		} else {
			bindingElement := element.AsBindingElement()
			id := transformBindingName(s, bindingElement.Name(), initializers)
			if bindingElement.Initializer != nil {
				initializers.Push(transformInitializer(s, id.(luau.WritableExpression), bindingElement.Initializer))
			}
			ids.Push(id)
		}
	}
}

// transformInLineArrayAssignmentPattern ports
// transformInLineArrayAssignmentPattern (L162-222): the `for ([a, b] of map)`
// assignment-form fast path — each slot gets a `_binding` temp as the
// generic-for binding, with `target = _binding` assignments / nested
// destructures / defaults captured into initializers (writables computed with
// readAfterWrite = initializer-present).
func transformInLineArrayAssignmentPattern(s *State, assignmentPattern *ast.Node, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) {
	initializers.PushList(s.CaptureStatements(func() {
		for _, element := range assignmentPattern.AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsOmittedExpression(element) {
				ids.Push(luau.TempID(""))
			} else if ast.IsSpreadElement(element) {
				s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
			} else {
				var initializer *ast.Node
				if ast.IsBinaryExpression(element) {
					binary := element.AsBinaryExpression()
					initializer = SkipDownwards(binary.Right)
					element = SkipDownwards(binary.Left)
				}

				valueID := luau.TempID("binding")
				if ast.IsIdentifier(element) || ast.IsElementAccessExpression(element) || ast.IsPropertyAccessExpression(element) {
					id := transformWritableExpression(s, element, initializer != nil)
					s.Prereq(luau.NewAssignment(id, "=", valueID))
					if initializer != nil {
						s.Prereq(transformInitializer(s, id, initializer))
					}
				} else if ast.IsArrayLiteralExpression(element) {
					if initializer != nil {
						s.Prereq(transformInitializer(s, valueID, initializer))
					}
					transformArrayAssignmentPattern(s, element, valueID)
				} else if ast.IsObjectLiteralExpression(element) {
					if initializer != nil {
						s.Prereq(transformInitializer(s, valueID, initializer))
					}
					transformObjectAssignmentPattern(s, element, valueID)
				} else {
					panic("transformer: transformInLineArrayAssignmentPattern invalid element: " + kindName(element.Kind)) // upstream assert
				}

				ids.Push(valueID)
			}
		}
	}))
}

// buildMapLoop ports buildMapLoop (L224-257): `for _k, _v in exp do` with the
// inline `[k, v]`-destructure fast path (loop ids bind directly, no temp).
// The fallback (a plain id or object pattern over a Map) binds `_k, _v` and
// reconstitutes the entry: declaration form `local pair = { _k, _v }` +
// pattern bindings; expression form assigns `{ _k, _v }` through the target.
var buildMapLoop = makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
	if ast.IsVariableDeclarationList(initializer) {
		name := initializer.AsVariableDeclarationList().Declarations.Nodes[0].Name()
		if ast.IsArrayBindingPattern(name) && !arrayLikeExpressionContainsSpread(name) {
			transformInLineArrayBindingPattern(s, name, ids, initializers)
			return exp
		}
	} else if ast.IsArrayLiteralExpression(initializer) && !arrayLikeExpressionContainsSpread(initializer) {
		transformInLineArrayAssignmentPattern(s, initializer, ids, initializers)
		return exp
	}

	keyID := luau.TempID("k")
	valueID := luau.TempID("v")
	ids.Push(keyID)
	ids.Push(valueID)

	if ast.IsVariableDeclarationList(initializer) {
		bindingList := luau.NewList[luau.Statement]()
		initializers.Push(luau.NewVariableDeclaration(
			transformForInitializer(s, initializer, bindingList),
			luau.NewArray(luau.NewList[luau.Expression](keyID, valueID)),
		))
		initializers.PushList(bindingList)
	} else {
		transformForInitializerExpressionDirect(s, initializer, initializers,
			luau.NewArray(luau.NewList[luau.Expression](keyID, valueID)))
	}

	return exp
})

// buildStringLoop ports buildStringLoop (L259-262):
// `for c in string.gmatch(exp, utf8.charpattern) do`.
var buildStringLoop = makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
	ids.Push(transformForInitializer(s, initializer, initializers))
	return luau.NewCall(luau.GlobalProperty("string", "gmatch"),
		luau.NewList[luau.Expression](exp, luau.GlobalProperty("utf8", "charpattern")))
})

// buildIterableFunctionLoop ports buildIterableFunctionLoop (L264-267):
// `for x in exp do` — Luau calls the function each iteration; nil terminates.
var buildIterableFunctionLoop = makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
	ids.Push(transformForInitializer(s, initializer, initializers))
	return exp
})

// makeIterableFunctionLuaTupleShorthand ports
// makeIterableFunctionLuaTupleShorthand (L269-284): the array-pattern fast
// path for IterableFunction<LuaTuple<T>> — inline bindings become the
// generic-for ids (`for a, b in exp do`), no tuple table is built.
func makeIterableFunctionLuaTupleShorthand(s *State, array *ast.Node, statements *luau.List[luau.Statement], expression luau.Expression) *luau.List[luau.Statement] {
	ids := luau.NewList[luau.AnyIdentifier]()
	initializers := luau.NewList[luau.Statement]()
	if ast.IsArrayBindingPattern(array) {
		transformInLineArrayBindingPattern(s, array, ids, initializers)
	} else {
		transformInLineArrayAssignmentPattern(s, array, ids, initializers)
	}
	statements.UnshiftList(initializers)
	return luau.NewList[luau.Statement](luau.NewFor(ids, expression, statements))
}

// tupleCombinedFlags reproduces upstream's
// `(tupleArgType as ts.TupleTypeReference).target.combinedFlags` read: tsgo
// keeps combinedFlags unexported, but createTupleTypeWorker computes it as
// the bitwise OR of every element's flags (checker.go L24656), so OR-ing the
// exported ElementInfos is identity-equivalent.
func tupleCombinedFlags(target *checker.TupleType) checker.ElementFlags {
	var combined checker.ElementFlags
	for _, info := range target.ElementInfos() {
		combined |= info.TupleElementFlags()
	}
	return combined
}

// buildIterableFunctionLuaTupleLoop ports buildIterableFunctionLuaTupleLoop
// (L286-382). Three shapes, in order:
//  1. array-pattern initializer (binding or assignment form) — the inline
//     fast path (makeIterableFunctionLuaTupleShorthand);
//  2. declaration-list initializer whose LuaTuple<T> argument is a tuple type
//     WITHOUT rest elements — tuple-arity introspection: one generic-for temp
//     per tuple slot (named from the tuple labels when valid identifiers,
//     else "element"), reassembled into `local t = { _el1, _el2 }`;
//  3. otherwise (unknown arity / expression initializer) — the while-true
//     protocol: `local _v = { _iterFunc() } if #_v == 0 then break end`.
//
// tsgo mapping for the CHECKER introspection (upstream L303-327):
//
//	type.getCallSignatures()[0].getReturnType() -> Checker.GetReturnTypeOfSignature(Checker.GetCallSignatures(t)[0])
//	luaTupleType.aliasTypeArguments             -> Type.Alias().TypeArguments()
//	typeChecker.isTupleType(t)                  -> Type.IsTupleType()
//	(t as TupleTypeReference).target            -> Type.TargetTupleType()
//	target.combinedFlags & ElementFlags.Rest    -> tupleCombinedFlags(target) & checker.ElementFlagsRest
//	target.elementFlags.length                  -> len(target.ElementInfos())
//	target.labeledElementDeclarations[i]        -> target.ElementInfos()[i].LabeledDeclaration()
func buildIterableFunctionLuaTupleLoop(t *checker.Type) loopBuilder {
	return func(s *State, statements *luau.List[luau.Statement], initializer *ast.Node, exp luau.Expression) *luau.List[luau.Statement] {
		if ast.IsVariableDeclarationList(initializer) {
			// for (const [a, b] of iter())
			name := initializer.AsVariableDeclarationList().Declarations.Nodes[0].Name()
			if ast.IsArrayBindingPattern(name) && !arrayLikeExpressionContainsSpread(name) {
				return makeIterableFunctionLuaTupleShorthand(s, name, statements, exp)
			}
		} else if ast.IsArrayLiteralExpression(initializer) && !arrayLikeExpressionContainsSpread(initializer) {
			// for ([a, b] of iter())
			return makeIterableFunctionLuaTupleShorthand(s, initializer, statements, exp)
		}

		var iteratorReturnIds []*luau.TemporaryIdentifier

		// get call signature of IterableFunction<T> which is `(): T`
		// and get return type of call signature which is `T`
		luaTupleType := s.Checker.GetReturnTypeOfSignature(s.Checker.GetCallSignatures(t)[0])
		if luaTupleType == nil || luaTupleType.Alias() == nil || len(luaTupleType.Alias().TypeArguments()) != 1 {
			panic("transformer: Incorrect LuaTuple<T> type arguments") // upstream assert
		}
		tupleArgType := luaTupleType.Alias().TypeArguments()[0]
		// if initializer is a variable declaration `for (const a of iter())`
		// and LuaTuple has a fixed element count
		// then use lua for-in loop, specifying all elements and putting them in table
		if ast.IsVariableDeclarationList(initializer) &&
			tupleArgType.IsTupleType() &&
			tupleCombinedFlags(tupleArgType.TargetTupleType())&checker.ElementFlagsVariable == 0 {
			tupleType := tupleArgType.TargetTupleType()
			for _, info := range tupleType.ElementInfos() {
				name := "element"
				if label := info.LabeledDeclaration(); label != nil {
					if labelName := label.Name(); labelName != nil && ast.IsIdentifier(labelName) && luau.IsValidIdentifier(labelName.Text()) {
						name = labelName.Text()
					}
				}
				iteratorReturnIds = append(iteratorReturnIds, luau.TempID(name))
			}
		} else {
			nameHint := ValueToIdStr(exp)
			if nameHint == "" {
				nameHint = "iterFunc"
			}
			iterFuncID := s.PushToVar(exp, nameHint)
			loopStatements := luau.NewList[luau.Statement]()

			initializerStatements := luau.NewList[luau.Statement]()
			valueID := transformForInitializer(s, initializer, initializerStatements)

			loopStatements.Push(luau.NewVariableDeclaration(
				valueID,
				luau.NewArray(luau.NewList[luau.Expression](
					luau.NewCall(iterFuncID, luau.NewList[luau.Expression]()),
				)),
			))

			loopStatements.Push(luau.NewIf(
				luau.NewBinary(luau.NewUnary("#", valueID), "==", luau.Num(0)),
				luau.NewList[luau.Statement](luau.NewBreak()),
				nil,
			))

			loopStatements.PushList(initializerStatements)

			loopStatements.PushList(statements)

			return luau.NewList[luau.Statement](luau.NewWhile(luau.Bool(true), loopStatements))
		}

		tupleID := transformForInitializer(s, initializer, statements)

		builder := makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
			members := luau.NewList[luau.Expression]()
			for _, id := range iteratorReturnIds {
				ids.Push(id)
				members.Push(id)
			}

			initializers.Push(luau.NewVariableDeclaration(tupleID, luau.NewArray(members)))
			return exp
		})

		return builder(s, statements, initializer, exp)
	}
}

// buildGeneratorLoop ports buildGeneratorLoop (L384-412): the .next()/.done/
// .value protocol —
//
//	for _result in exp.next do
//	    if _result.done then break end
//	    local x = _result.value
//	    <body>
//	end
var buildGeneratorLoop = makeForLoopBuilder(func(s *State, initializer *ast.Node, exp luau.Expression, ids *luau.List[luau.AnyIdentifier], initializers *luau.List[luau.Statement]) luau.Expression {
	loopID := luau.TempID("result")
	ids.Push(loopID)

	initializers.Push(luau.NewIf(
		luau.NewPropertyAccess(loopID, "done"),
		luau.NewList[luau.Statement](luau.NewBreak()),
		nil,
	))

	if ast.IsVariableDeclarationList(initializer) {
		bindingList := luau.NewList[luau.Statement]()
		initializers.Push(luau.NewVariableDeclaration(
			transformForInitializer(s, initializer, bindingList),
			luau.NewPropertyAccess(loopID, "value"),
		))
		initializers.PushList(bindingList)
	} else {
		transformForInitializerExpressionDirect(s, initializer, initializers,
			luau.NewPropertyAccess(loopID, "value"))
	}

	return luau.NewPropertyAccess(convertToIndexableExpression(exp), "next")
})

// emptyLoopBuilder stands in for upstream's `() => luau.list.make()` builder
// returned after a diagnostic was raised at dispatch.
func emptyLoopBuilder(s *State, statements *luau.List[luau.Statement], initializer *ast.Node, exp luau.Expression) *luau.List[luau.Statement] {
	return luau.NewList[luau.Statement]()
}

// getLoopBuilder ports getLoopBuilder (L414-438): the isDefinitelyType
// dispatch, in upstream order. Iterable<T> and union keep upstream's own
// noIterableIteration / noMacroUnion errors (both return a builder that emits
// nothing). The fallthrough is upstream's `assert(false, ...)`.
func getLoopBuilder(s *State, node *ast.Node, t *checker.Type) loopBuilder {
	if IsDefinitelyType(s, t, IsArrayType(s)) {
		return buildArrayLoop
	} else if IsDefinitelyType(s, t, IsSetType(s)) {
		return buildSetLoop
	} else if IsDefinitelyType(s, t, IsMapType(s)) || IsSharedTableType(s, t) {
		return buildMapLoop
	} else if IsDefinitelyType(s, t, IsStringType) {
		return buildStringLoop
	} else if IsDefinitelyType(s, t, IsIterableFunctionLuaTupleType(s)) {
		return buildIterableFunctionLuaTupleLoop(t)
	} else if IsDefinitelyType(s, t, IsIterableFunctionType(s)) {
		return buildIterableFunctionLoop
	} else if IsDefinitelyType(s, t, IsGeneratorType(s)) {
		return buildGeneratorLoop
	} else if IsDefinitelyType(s, t, IsIterableType(s)) {
		s.Diags.Add(DiagNoIterableIteration(node))
		return emptyLoopBuilder
	} else if t.IsUnion() {
		s.Diags.Add(DiagNoMacroUnion(node))
		return emptyLoopBuilder
	}
	panic("transformer: ForOf iteration type not implemented: " + s.Checker.TypeToString(t)) // upstream assert(false)
}

// findRangeMacro ports findRangeMacro (L440-448): the loop expression
// (skipped downwards) is a call whose callee resolves to the `$range` macro
// symbol. Upstream compares against macroManager.getSymbolOrThrow($range);
// rotor matches the MacroManager call-macro entry by name (same symbol
// identity — the entry was registered from that resolution). Checked
// BEFORE the expression is transformed, matching upstream's position — the
// generic macro sentinel inside TransformExpression is never reached for it.
func findRangeMacro(s *State, node *ast.Node) *ast.Node {
	expression := SkipDownwards(node.AsForInOrOfStatement().Expression)
	if ast.IsCallExpression(expression) {
		symbol := GetFirstDefinedSymbol(s, s.GetType(expression.AsCallExpression().Expression))
		if symbol != nil {
			if macro := s.Macros().GetCallMacro(symbol); macro != nil && macro.Name == "$range" {
				return expression
			}
		}
	}
	return nil
}

// transformForOfRangeMacro ports transformForOfRangeMacro (L450-477):
// `for (const i of $range(start, end[, step]))` becomes a Luau numeric for.
// The macro arguments transform via ensureTransformOrder (prereqs land before
// the loop); a non-literal step expression gets `or 1` appended to match
// `Number()` defaulting (a number-literal step passes through — the renderer
// drops a literal step of exactly 1).
func transformForOfRangeMacro(s *State, node *ast.Node, macroCall *ast.Node) *luau.List[luau.Statement] {
	forOf := node.AsForInOrOfStatement()
	result := luau.NewList[luau.Statement]()

	statements := luau.NewList[luau.Statement]()
	id := transformForInitializer(s, forOf.Initializer, statements)

	var args []luau.Expression
	prereqs := s.CaptureStatements(func() {
		args = ensureTransformOrder(s, macroCall.AsCallExpression().Arguments.Nodes)
	})
	result.PushList(prereqs)
	// [start, end, step] destructure — missing slots are nil (upstream
	// undefined); the checker enforces the two-argument minimum.
	var start, end, step luau.Expression
	if len(args) > 0 {
		start = args[0]
	}
	if len(args) > 1 {
		end = args[1]
	}
	if len(args) > 2 {
		step = args[2]
	}

	statements.PushList(TransformStatementList(s, forOf.Statement, getStatements(forOf.Statement), nil))
	statements.UnshiftList(s.LabelResets())

	if step != nil {
		if _, isLiteralNumber := getLiteralNumberValue(step); !isLiteralNumber {
			step = luau.NewBinary(step, "or", luau.Num(1))
		}
	}
	result.Push(luau.NewNumericFor(id, start, end, step, statements))

	return result
}

func transformOptimizableVarArgsForOf(s *State, node *ast.Node, result *luau.List[luau.Statement]) *luau.List[luau.Statement] {
	forOf := node.AsForInOrOfStatement()
	data := s.getOptimizableVarArgsData(forOf.Expression)
	if data == nil || !data.IsOptimizable {
		return result
	}

	output := luau.NewList[luau.Statement]()
	var loop *luau.ForStatement
	result.ForEach(func(statement luau.Statement) {
		if forStatement, ok := statement.(*luau.ForStatement); ok {
			loop = forStatement
			return
		}
		output.Push(statement)
	})
	if loop == nil {
		panic("transformer: optimizable varargs for-of has no array loop")
	}

	var valueID luau.AnyIdentifier
	if ast.IsVariableDeclarationList(forOf.Initializer) {
		name := forOf.Initializer.AsVariableDeclarationList().Declarations.Nodes[0].Name()
		if ast.IsIdentifier(name) {
			valueID = luau.ID(name.Text())
		} else {
			valueID = luau.TempID("v")
		}
	} else {
		loopIDs := loop.IDs.ToSlice()
		if len(loopIDs) != 2 {
			panic("transformer: optimizable varargs for-of has invalid array loop ids")
		}
		valueID = loopIDs[1]
	}

	indexID := luau.TempID("i")
	loopStatements := luau.NewList[luau.Statement](luau.NewVariableDeclaration(
		valueID,
		luau.NewParenthesized(luau.NewCall(
			luau.GlobalID("select"),
			luau.NewList[luau.Expression](indexID, luau.NewVarArgs()),
		)),
	))
	loopStatements.PushList(loop.Statements)

	length := createVarArgsLengthSelect()
	if data.usesLengthCache() {
		length = data.LengthID
	}
	output.Push(luau.NewNumericFor(indexID, luau.Num(1), length, nil, loopStatements))
	return output
}

// transformForOfStatement ports transformForOfStatement (L479-508). The body
// is transformed BEFORE the loop shape is built; builders prepend initializer
// statements.
func transformForOfStatement(s *State, node *ast.Node) *luau.List[luau.Statement] {
	forOf := node.AsForInOrOfStatement()

	if forOf.AwaitModifier != nil {
		s.Diags.Add(DiagNoAwaitForOf(node))
	}

	if ast.IsVariableDeclarationList(forOf.Initializer) {
		name := forOf.Initializer.AsVariableDeclarationList().Declarations.Nodes[0].Name()
		if ast.IsIdentifier(name) {
			ValidateIdentifier(s, name)
		}
	}

	// One break scope covers the range-macro numeric for and every builder
	// shape (rotor extension).
	s.EnterBreakScope()
	defer s.ExitBreakScope()

	var result *luau.List[luau.Statement]
	if rangeMacroCall := findRangeMacro(s, node); rangeMacroCall != nil {
		result = transformForOfRangeMacro(s, node, rangeMacroCall)
	} else {
		result = luau.NewList[luau.Statement]()

		var exp luau.Expression
		expPrereqs := s.CaptureStatements(func() {
			exp = TransformExpression(s, forOf.Expression)
		})
		result.PushList(expPrereqs)

		expType := s.GetType(forOf.Expression)
		statements := TransformStatementList(s, forOf.Statement, getStatements(forOf.Statement), nil)
		// Unshift into the body list HERE: it is still writable, and every
		// builder either unshifts its own initializers above these or splices
		// the list into a larger body.
		statements.UnshiftList(s.LabelResets())

		loopBuilder := getLoopBuilder(s, forOf.Expression, expType)
		result.PushList(loopBuilder(s, statements, forOf.Initializer, exp))

		// ORDER-CRITICAL: the varargs pass rebuilds the result list and
		// re-appends the loop last, so the checks must go on afterwards.
		result = transformOptimizableVarArgsForOf(s, node, result)
	}

	result.PushList(s.LabelChecks())
	return result
}
