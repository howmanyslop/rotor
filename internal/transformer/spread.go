package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// This file ports the spread machinery:
//   - transformSpreadAssignment (expressions/transformObjectLiteralExpression.ts
//     L34-90) — object-literal spread;
//   - getAddIterableToArrayBuilder (util/getAddIterableToArrayBuilder.ts) — the
//     per-iterable-type "append to array" builders shared by array-literal
//     spread and call-argument spread;
//   - transformSpreadElement (expressions/transformSpreadElement.ts) —
//     call/new argument spread (`f(...exp)` -> `f(unpack(exp))`).

// ---------------------------------------------------------------------------
// Object-literal spread — transformObjectLiteralExpression.ts L34-90
// ---------------------------------------------------------------------------

// transformSpreadAssignment ports transformSpreadAssignment. Fast path: the
// spread is definitely-object AND the pointer is still an EMPTY inline map
// (i.e. the spread is the first member) — `local _object = table.clone(exp)`
// + `setmetatable(_object, nil)` (the metatable is stripped because things
// like classes can be spread). Otherwise: spill the inline map, then a
// generalized-iteration copy loop `for _k, _v in exp do _object[_k] = _v end`,
// if-wrapped in truthiness checks when not definitely-object (with the spread
// expression pinned to a `_spread` temp when complex).
func transformSpreadAssignment(s *State, ptr *MapPointer, property *ast.Node) {
	expression := property.AsSpreadAssignment().Expression
	expType := s.Checker.GetNonOptionalType(s.GetType(expression))
	symbol := GetFirstDefinedSymbol(s, expType)
	if symbol != nil && s.Macros().IsMacroOnlyClass(symbol) {
		s.Diags.Add(DiagNoMacroObjectSpread(property))
	}

	t := s.GetType(expression) // NOT the non-optional type!
	definitelyObject := IsDefinitelyType(s, t, IsObjectType)

	if m, ok := ptr.Value.(*luau.Map); definitelyObject && ok && m.Fields.IsEmpty() {
		ptr.Value = s.PushToVar(
			luau.NewCall(luau.GlobalProperty("table", "clone"),
				luau.NewList[luau.Expression](TransformExpression(s, expression))),
			ptr.Name,
		)
		// Explicitly remove metatable because things like classes can be spread
		s.Prereq(luau.NewCallStatement(luau.NewCall(
			luau.GlobalID("setmetatable"),
			luau.NewList[luau.Expression](ptr.Value, luau.Nil()),
		)))
		return
	}

	DisableMapInline(s, ptr)
	spreadExp := TransformExpression(s, expression)
	if !definitelyObject {
		spreadExp = s.PushToVarIfComplex(spreadExp, "spread")
	}

	keyID := luau.TempID("k")
	valueID := luau.TempID("v")
	var statement luau.Statement = luau.NewFor(
		luau.NewList[luau.AnyIdentifier](keyID, valueID),
		spreadExp,
		luau.NewList[luau.Statement](luau.NewAssignment(
			luau.NewComputedIndex(ptr.Value.(luau.IndexableExpression), keyID),
			"=",
			valueID,
		)),
	)

	if !definitelyObject {
		statement = luau.NewIf(
			CreateTruthinessChecks(s, spreadExp, expression, s.GetType(expression)),
			luau.NewList[luau.Statement](statement),
			luau.NewList[luau.Statement](),
		)
	}

	s.Prereq(statement)
}

// ---------------------------------------------------------------------------
// addIterableToArrayBuilder — util/getAddIterableToArrayBuilder.ts
// ---------------------------------------------------------------------------

// addIterableToArrayBuilder ports `type AddIterableToArrayBuilder`: append the
// (already transformed) iterable expression's elements to arrayID, tracking
// lengthID. amtElementsSinceUpdate is the number of inline elements pushed
// since lengthID was last accurate; shouldUpdateLengthID is true when more
// elements follow the spread.
type addIterableToArrayBuilder func(
	s *State,
	expression luau.Expression,
	arrayID luau.AnyIdentifier,
	lengthID luau.AnyIdentifier,
	amtElementsSinceUpdate int,
	shouldUpdateLengthID bool,
) *luau.List[luau.Statement]

// addArrayToArray ports addArray: `table.move(input, 1, #input, lengthID +
// amt + 1, arrayID)`. The input is pinned to a temp when not an identifier;
// `#input` is pinned (named `<input>Length`) only when the length must be
// updated afterwards.
func addArrayToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()

	inputArray := s.PushToVarIfNonID(expression, "array")
	var inputLength luau.Expression = luau.NewUnary("#", inputArray)
	if shouldUpdateLengthID {
		inputLength = s.PushToVar(inputLength, ValueToIdStr(inputArray)+"Length")
	}

	result.Push(luau.NewCallStatement(luau.NewCall(
		luau.GlobalProperty("table", "move"),
		luau.NewList[luau.Expression](
			inputArray,
			luau.Num(1),
			inputLength,
			luau.NewBinary(lengthID, "+", luau.Num(float64(amtElementsSinceUpdate+1))),
			arrayID,
		),
	)))

	if shouldUpdateLengthID {
		result.Push(luau.NewAssignment(lengthID, "+=", inputLength))
	}

	return result
}

// catchUpLengthID emits the shared `lengthID += amt` prefix the non-array
// builders start with when inline elements were pushed since the last update.
func catchUpLengthID(result *luau.List[luau.Statement], lengthID luau.AnyIdentifier, amtElementsSinceUpdate int) {
	if amtElementsSinceUpdate > 0 {
		result.Push(luau.NewAssignment(lengthID, "+=", luau.Num(float64(amtElementsSinceUpdate))))
	}
}

// appendValueLoop builds the common loop body `lengthID += 1; arrayID[lengthID]
// = value` used by the set/map/string/iterable-function/generator builders.
func appendValueLoop(arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, value luau.Expression) (luau.Statement, luau.Statement) {
	return luau.NewAssignment(lengthID, "+=", luau.Num(1)),
		luau.NewAssignment(luau.NewComputedIndex(arrayID, lengthID), "=", value)
}

// addStringToArray ports addString:
// `for _char in string.gmatch(exp, utf8.charpattern) do`.
func addStringToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	catchUpLengthID(result, lengthID, amtElementsSinceUpdate)

	valueID := luau.TempID("char")
	increment, assign := appendValueLoop(arrayID, lengthID, valueID)
	result.Push(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](valueID),
		luau.NewCall(luau.GlobalProperty("string", "gmatch"),
			luau.NewList[luau.Expression](expression, luau.GlobalProperty("utf8", "charpattern"))),
		luau.NewList[luau.Statement](increment, assign),
	))

	return result
}

// addSetToArray ports addSet: `for _v in exp do`.
func addSetToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	catchUpLengthID(result, lengthID, amtElementsSinceUpdate)

	valueID := luau.TempID("v")
	increment, assign := appendValueLoop(arrayID, lengthID, valueID)
	result.Push(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](valueID),
		expression,
		luau.NewList[luau.Statement](increment, assign),
	))

	return result
}

// addMapToArray ports addMap: `for _k, _v in exp do` appending `{ _k, _v }`
// entry pairs.
func addMapToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	catchUpLengthID(result, lengthID, amtElementsSinceUpdate)

	keyID := luau.TempID("k")
	valueID := luau.TempID("v")
	increment, assign := appendValueLoop(arrayID, lengthID,
		luau.NewArray(luau.NewList[luau.Expression](keyID, valueID)))
	result.Push(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](keyID, valueID),
		expression,
		luau.NewList[luau.Statement](increment, assign),
	))

	return result
}

// addIterableFunctionToArray ports addIterableFunction: `for _result in exp do`.
func addIterableFunctionToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	catchUpLengthID(result, lengthID, amtElementsSinceUpdate)

	valueID := luau.TempID("result")
	increment, assign := appendValueLoop(arrayID, lengthID, valueID)
	result.Push(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](valueID),
		expression,
		luau.NewList[luau.Statement](increment, assign),
	))

	return result
}

// addIterableFunctionLuaTupleToArray ports addIterableFunctionLuaTuple: the
// while-true protocol collecting each `{ iterFunc() }` tuple until empty.
func addIterableFunctionLuaTupleToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	catchUpLengthID(result, lengthID, amtElementsSinceUpdate)

	iterFuncID := s.PushToVar(expression, "iterFunc")
	valueID := luau.TempID("results")
	increment, assign := appendValueLoop(arrayID, lengthID, valueID)
	result.Push(luau.NewWhile(luau.Bool(true), luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(valueID, luau.NewArray(luau.NewList[luau.Expression](
			luau.NewCall(iterFuncID, luau.NewList[luau.Expression]()),
		))),
		luau.NewIf(
			luau.NewBinary(luau.NewUnary("#", valueID), "==", luau.Num(0)),
			luau.NewList[luau.Statement](luau.NewBreak()),
			luau.NewList[luau.Statement](),
		),
		increment,
		assign,
	)))

	return result
}

// addGeneratorToArray ports addGenerator: the .next()/.done/.value protocol.
func addGeneratorToArray(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	catchUpLengthID(result, lengthID, amtElementsSinceUpdate)

	iterID := luau.TempID("result")
	increment, assign := appendValueLoop(arrayID, lengthID, luau.NewPropertyAccess(iterID, "value"))
	result.Push(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](iterID),
		luau.NewPropertyAccess(convertToIndexableExpression(expression), "next"),
		luau.NewList[luau.Statement](
			luau.NewIf(
				luau.NewPropertyAccess(iterID, "done"),
				luau.NewList[luau.Statement](luau.NewBreak()),
				luau.NewList[luau.Statement](),
			),
			increment,
			assign,
		),
	))

	return result
}

// emptyAddIterableToArrayBuilder stands in for upstream's
// `() => luau.list.make()` builder returned after a diagnostic.
func emptyAddIterableToArrayBuilder(s *State, expression luau.Expression, arrayID luau.AnyIdentifier, lengthID luau.AnyIdentifier, amtElementsSinceUpdate int, shouldUpdateLengthID bool) *luau.List[luau.Statement] {
	return luau.NewList[luau.Statement]()
}

// getAddIterableToArrayBuilder ports getAddIterableToArrayBuilder (L348-376):
// the isDefinitelyType dispatch, in upstream order (NOTE: string before
// set/map here, unlike getLoopBuilder). Iterable<T> and unions keep upstream's
// own noIterableIteration / noMacroUnion errors (both return a builder that
// emits nothing); the fallthrough is upstream's `assert(false, ...)`.
func getAddIterableToArrayBuilder(s *State, node *ast.Node, t *checker.Type) addIterableToArrayBuilder {
	if IsDefinitelyType(s, t, IsArrayType(s)) {
		return addArrayToArray
	} else if IsDefinitelyType(s, t, IsStringType) {
		return addStringToArray
	} else if IsDefinitelyType(s, t, IsSetType(s)) {
		return addSetToArray
	} else if IsDefinitelyType(s, t, IsMapType(s)) || IsSharedTableType(s, t) {
		return addMapToArray
	} else if IsDefinitelyType(s, t, IsIterableFunctionLuaTupleType(s)) {
		return addIterableFunctionLuaTupleToArray
	} else if IsDefinitelyType(s, t, IsIterableFunctionType(s)) {
		return addIterableFunctionToArray
	} else if IsDefinitelyType(s, t, IsGeneratorType(s)) {
		return addGeneratorToArray
	} else if IsDefinitelyType(s, t, IsIterableType(s)) {
		s.Diags.Add(DiagNoIterableIteration(node))
		return emptyAddIterableToArrayBuilder
	} else if t.IsUnion() {
		s.Diags.Add(DiagNoMacroUnion(node))
		return emptyAddIterableToArrayBuilder
	}
	panic("transformer: Iteration type not implemented: " + s.Checker.TypeToString(t)) // upstream assert(false)
}

// ---------------------------------------------------------------------------
// Call-argument spread — expressions/transformSpreadElement.ts
// ---------------------------------------------------------------------------

// transformSpreadElement ports transformSpreadElement: `f(...exp)`. A
// definitely-array spread is `unpack(exp)` directly; any other iterable is
// collected into a fresh `_array`/`_length` pair through the shared builders
// and then unpacked. A spread that is not the LAST argument raises
// noPrecedingSpreadElement.
func transformSpreadElement(s *State, node *ast.Node) luau.Expression {
	spreadExpression := node.AsSpreadElement().Expression
	validateNotAnyType(s, spreadExpression)

	// array literal is caught and handled separately in transformArrayLiteralExpression.ts
	parent := node.Parent
	if ast.IsArrayLiteralExpression(parent) || (!ast.IsCallExpression(parent) && !ast.IsNewExpression(parent)) {
		panic("transformer: transformSpreadElement: unexpected parent " + kindName(parent.Kind)) // upstream assert
	}
	arguments := parent.Arguments()
	if arguments[len(arguments)-1] != node {
		s.Diags.Add(DiagNoPrecedingSpreadElement(node))
	}
	if data := s.getOptimizableVarArgsData(spreadExpression); data != nil && data.IsOptimizable {
		return luau.NewVarArgs()
	}

	expression := TransformExpression(s, spreadExpression)

	t := s.GetType(spreadExpression)
	if IsDefinitelyType(s, t, IsArrayType(s)) {
		return luau.NewCall(luau.GlobalID("unpack"), luau.NewList[luau.Expression](expression))
	}
	builder := getAddIterableToArrayBuilder(s, spreadExpression, t)
	arrayID := s.PushToVar(luau.NewArray(luau.NewList[luau.Expression]()), "array")
	lengthID := s.PushToVar(luau.Num(0), "length")
	s.PrereqList(builder(s, expression, arrayID, lengthID, 0, false))
	return luau.NewCall(luau.GlobalID("unpack"), luau.NewList[luau.Expression](arrayID))
}
