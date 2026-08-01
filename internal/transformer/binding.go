package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
)

// This file ports the shared destructuring plumbing:
// nodes/binding/transformBindingName.ts, util/binding/
// getAccessorForBindingType.ts, and util/binding/objectAccessor.ts.
// The pattern transforms themselves live in bindingarray.go /
// bindingobject.go.

// ---------------------------------------------------------------------------
// transformBindingName — nodes/binding/transformBindingName.ts (L8-30)
// ---------------------------------------------------------------------------

// transformBindingName produces the single identifier bound by a BindingName:
// identifiers directly, patterns through a `_binding` temp whose destructure
// statements are appended to initializers (used by for-of loop headers;
// variable declarations go through transformVariableDeclaration instead).
func transformBindingName(s *State, name *ast.Node, initializers *luau.List[luau.Statement]) luau.AnyIdentifier {
	if ast.IsIdentifier(name) {
		return TransformIdentifierDefined(s, name)
	}
	id := luau.TempID("binding")
	initializers.PushList(s.CaptureStatements(func() {
		if ast.IsArrayBindingPattern(name) {
			transformArrayBindingPattern(s, name, id)
		} else {
			transformObjectBindingPattern(s, name, id)
		}
	}))
	return id
}

// isOmittedBindingElement is the rotor equivalent of upstream
// `ts.isOmittedExpression(element)` over ArrayBindingPattern elements: the
// tsgo parser represents an array-binding hole (`const [, x] = ...`) as a
// BindingElement with a nil name, not as strada's OmittedExpression node
// (parser.go parseArrayBindingElement: "These are all nil for a missing
// element").
func isOmittedBindingElement(element *ast.Node) bool {
	return ast.IsOmittedExpression(element) ||
		(ast.IsBindingElement(element) && element.AsBindingElement().Name() == nil)
}

// ---------------------------------------------------------------------------
// Accessor table — util/binding/getAccessorForBindingType.ts (COMPLETE)
// ---------------------------------------------------------------------------

// bindingAccessor ports the BindingAccessor signature: produce the expression
// for one array-position element. idStack carries iteration state BETWEEN
// elements of one pattern (the gmatch matcher, the last `next` key);
// isOmitted=true means the element is hole-only — stateful accessors still
// advance (side-effect prereqs), nothing is bound.
type bindingAccessor func(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression

// arrayAccessor ports the array entry (L32-37): `parentId[index + 1]` with
// the +1 folded into the literal; an omitted element emits nothing.
func arrayAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	return luau.NewComputedIndex(parentID, luau.Num(float64(index+1)))
}

// peekIDStack returns the last identifier on the idStack (upstream peek), or
// nil when empty.
func peekIDStack(idStack *[]luau.AnyIdentifier) luau.AnyIdentifier {
	if len(*idStack) == 0 {
		return nil
	}
	return (*idStack)[len(*idStack)-1]
}

// stringAccessor ports the string entry (L39-63): the FIRST element pushes a
// `local _matcher = string.gmatch(parentId, utf8.charpattern)` temp onto the
// idStack; every element reads `_matcher()`. An omitted element still calls
// the matcher (as a statement) so iteration advances.
func stringAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	var id luau.AnyIdentifier
	if len(*idStack) == 0 {
		id = s.PushToVar(
			luau.NewCall(luau.GlobalProperty("string", "gmatch"),
				luau.NewList[luau.Expression](parentID, luau.GlobalProperty("utf8", "charpattern"))),
			"matcher",
		)
		*idStack = append(*idStack, id)
	} else {
		id = (*idStack)[0]
	}

	callExp := luau.NewCall(id, luau.NewList[luau.Expression]())

	if isOmitted {
		s.Prereq(luau.NewCallStatement(callExp))
		return luau.NewNone()
	}
	return callExp
}

// setAccessor ports the Set entry (L65-84): `next(parentId[, lastValue])`
// continuation — each bound element lands in a `_value` temp pushed onto the
// idStack so the NEXT element continues from it. NOTE (upstream verbatim): an
// omitted element calls next as a statement but does NOT push, so the
// following element re-reads from the same key.
func setAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	args := luau.NewList[luau.Expression](parentID)
	if lastID := peekIDStack(idStack); lastID != nil {
		args.Push(lastID)
	}
	callExp := luau.NewCall(luau.GlobalID("next"), args)
	if isOmitted {
		s.Prereq(luau.NewCallStatement(callExp))
		return luau.NewNone()
	}
	id := s.PushToVar(callExp, "value")
	*idStack = append(*idStack, id)
	return id
}

// mapAccessor ports the Map entry (L86-103):
// `local _k, _v = next(parentId[, lastK])`, value = `{ _k, _v }`. The key
// temp continues the iteration via the idStack. NOTE (upstream verbatim): the
// accessor ignores isOmitted — an omitted element still declares its `_k, _v`
// pair (which advances iteration), it just binds nothing.
func mapAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	args := luau.NewList[luau.Expression](parentID)
	if lastID := peekIDStack(idStack); lastID != nil {
		args.Push(lastID)
	}
	keyID := luau.TempID("k")
	valueID := luau.TempID("v")
	s.Prereq(luau.NewVariableDeclaration(
		luau.NewList[luau.AnyIdentifier](keyID, valueID),
		luau.NewCall(luau.GlobalID("next"), args),
	))
	*idStack = append(*idStack, keyID)
	return luau.NewArray(luau.NewList[luau.Expression](keyID, valueID))
}

// iterableFunctionLuaTupleAccessor ports the IterableFunction<LuaTuple<T>>
// entry (L105-117): value = `{ parentId() }` (the call's multiple returns
// packed); an omitted element calls the function as a statement to advance.
func iterableFunctionLuaTupleAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	callExp := luau.NewCall(parentID, luau.NewList[luau.Expression]())
	if isOmitted {
		s.Prereq(luau.NewCallStatement(callExp))
		return luau.NewNone()
	}
	return luau.NewArray(luau.NewList[luau.Expression](callExp))
}

// iterableFunctionAccessor ports the IterableFunction<T> entry (L119-131):
// value = `parentId()`; an omitted element calls as a statement to advance.
func iterableFunctionAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	callExp := luau.NewCall(parentID, luau.NewList[luau.Expression]())
	if isOmitted {
		s.Prereq(luau.NewCallStatement(callExp))
		return luau.NewNone()
	}
	return callExp
}

// iterAccessor ports the generator/iterator-object entry (L133-141): value =
// `parentId.next().value`; an omitted element calls `.next()` as a statement
// to advance.
func iterAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	callExp := luau.NewCall(luau.NewPropertyAccess(parentID, "next"), luau.NewList[luau.Expression]())
	if isOmitted {
		s.Prereq(luau.NewCallStatement(callExp))
		return luau.NewNone()
	}
	return luau.NewPropertyAccess(callExp, "value")
}

// noneAccessor stands in for upstream's `() => luau.none()` accessor returned
// after the noIterableIteration diagnostic was raised at dispatch.
func noneAccessor(s *State, parentID luau.AnyIdentifier, index int, idStack *[]luau.AnyIdentifier, isOmitted bool) luau.Expression {
	return luau.NewNone()
}

// getAccessorForBindingType ports getAccessorForBindingType (L143-167): the
// 8-entry isDefinitelyType dispatch, in upstream order. Iterable<T> keeps
// upstream's own noIterableIteration error. The fallthrough is upstream's
// `assert(false, ...)`.
func getAccessorForBindingType(s *State, node *ast.Node, t *checker.Type) bindingAccessor {
	if IsDefinitelyType(s, t, IsArrayType(s)) {
		return arrayAccessor
	} else if IsDefinitelyType(s, t, IsStringType) {
		return stringAccessor
	} else if IsDefinitelyType(s, t, IsSetType(s)) {
		return setAccessor
	} else if IsDefinitelyType(s, t, IsMapType(s)) || IsSharedTableType(s, t) {
		return mapAccessor
	} else if IsDefinitelyType(s, t, IsIterableFunctionLuaTupleType(s)) {
		return iterableFunctionLuaTupleAccessor
	} else if IsDefinitelyType(s, t, IsIterableFunctionType(s)) {
		return iterableFunctionAccessor
	} else if IsDefinitelyType(s, t, IsIterableType(s)) {
		s.Diags.Add(DiagNoIterableIteration(node))
		return noneAccessor
	} else if IsDefinitelyType(s, t, IsGeneratorType(s)) ||
		IsDefinitelyType(s, t, IsObjectType) ||
		node.Kind == ast.KindThisKeyword {
		return iterAccessor
	}
	panic("transformer: Destructuring not supported for type: " + s.Checker.TypeToString(t)) // upstream assert(false)
}

// ---------------------------------------------------------------------------
// objectAccessor — util/binding/objectAccessor.ts (L11-36)
// ---------------------------------------------------------------------------

// objectAccessor produces the read expression for one object-pattern
// property. t is the PARENT pattern's type.
//
// QUIRK (port verbatim): computed names get the +1 array adjustment, but
// literal numeric names do NOT — `{ 0: x }` over a tuple emits `parent[0]`
// while `{ [0]: x }` emits `parent[1]` when the parent is array-typed.
func objectAccessor(s *State, parentID luau.AnyIdentifier, t *checker.Type, name *ast.Node) luau.Expression {
	if name.Kind == ast.KindBigIntLiteral {
		s.Diags.Add(DiagNoBigInt(name))
		return luau.NewNone()
	}

	addIndexDiagnostics(s, name, s.GetType(name))

	if ast.IsIdentifier(name) {
		return luau.NewPropertyAccess(parentID, name.Text())
	} else if ast.IsComputedPropertyName(name) {
		return luau.NewComputedIndex(parentID,
			addOneIfArrayType(s, t, TransformExpression(s, name.AsComputedPropertyName().Expression)))
	} else if ast.IsNumericLiteral(name) || ast.IsStringLiteral(name) || ast.IsNoSubstitutionTemplateLiteral(name) {
		return luau.NewComputedIndex(parentID, TransformExpression(s, name))
	} else if ast.IsPrivateIdentifier(name) {
		s.Diags.Add(DiagNoPrivateIdentifier(name))
		return luau.NewNone()
	}
	panic("transformer: objectAccessor unexpected name kind: " + kindName(name.Kind)) // upstream assertNever
}
