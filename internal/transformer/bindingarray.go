package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// This file ports the array-pattern destructuring transforms:
// nodes/binding/transformArrayBindingPattern.ts,
// nodes/binding/transformArrayAssignmentPattern.ts, and the optimized
// multi-local / multi-assign paths from
// nodes/statements/transformVariableStatement.ts (L57-99) and
// expressions/transformBinaryExpression.ts (L36-111).

// ---------------------------------------------------------------------------
// Binding patterns — const/let declarations and parameters
// ---------------------------------------------------------------------------

func transformArrayBindingPattern(s *State, bindingPattern *ast.Node, parentID luau.AnyIdentifier) {
	validateNotAnyType(s, bindingPattern)

	index := 0
	idStack := []luau.AnyIdentifier{}
	patternType := s.GetType(bindingPattern)
	accessor := getAccessorForBindingType(s, bindingPattern, patternType)
	destructor := getSpreadDestructorForType(s, patternType)
	for _, element := range bindingPattern.AsBindingPattern().Elements.Nodes {
		if isOmittedBindingElement(element) {
			accessor(s, parentID, index, &idStack, true)
		} else {
			bindingElement := element.AsBindingElement()
			var value luau.Expression
			if bindingElement.DotDotDotToken != nil {
				if destructor == nil {
					s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
					return
				}
				value = destructor(s, parentID, index, idStack)
			} else {
				value = accessor(s, parentID, index, &idStack, false)
			}
			name := bindingElement.Name()
			if ast.IsIdentifier(name) {
				id := transformVariable(s, name, value)
				if bindingElement.Initializer != nil {
					s.Prereq(transformInitializer(s, id, bindingElement.Initializer))
				}
			} else {
				id := s.PushToVar(value, "binding")
				if bindingElement.Initializer != nil {
					s.Prereq(transformInitializer(s, id, bindingElement.Initializer))
				}
				if ast.IsArrayBindingPattern(name) {
					transformArrayBindingPattern(s, name, id)
				} else {
					transformObjectBindingPattern(s, name, id)
				}
			}
		}
		index++
	}
}

// transformOptimizedArrayBindingPattern ports transformVariableStatement.ts
// transformOptimizedArrayBindingPattern (L57-99): the single multi-local form
// `local a, b = <rhs...>` used when the RHS is a LuaTuple call (direct
// unpack) or a literal array (members inlined) AND no bound identifier is
// hoisted (arrayBindingPatternContainsHoists — "we can't localize multiple
// variables at the same time if any of them are hoisted"). NOTE this path
// bypasses transformVariable, so no export-let/hoist handling per element —
// upstream relies on optimized patterns being local-only in practice (port
// verbatim). Defaults and nested destructures are emitted AFTER the
// declaration.
func transformOptimizedArrayBindingPattern(s *State, bindingPattern *ast.Node, rhs luau.NodeOrList) *luau.List[luau.Statement] {
	return s.CaptureStatements(func() {
		ids := luau.NewList[luau.AnyIdentifier]()
		statements := s.CaptureStatements(func() {
			for _, element := range bindingPattern.AsBindingPattern().Elements.Nodes {
				if isOmittedBindingElement(element) {
					ids.Push(luau.TempID(""))
				} else {
					bindingElement := element.AsBindingElement()
					if bindingElement.DotDotDotToken != nil {
						s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
						return
					}
					name := bindingElement.Name()
					if ast.IsIdentifier(name) {
						ValidateIdentifier(s, name)
						id := TransformIdentifierDefined(s, name)
						ids.Push(id)
						if bindingElement.Initializer != nil {
							s.Prereq(transformInitializer(s, id.(luau.WritableExpression), bindingElement.Initializer))
						}
					} else {
						id := luau.TempID("binding")
						ids.Push(id)
						if bindingElement.Initializer != nil {
							s.Prereq(transformInitializer(s, id, bindingElement.Initializer))
						}
						if ast.IsArrayBindingPattern(name) {
							transformArrayBindingPattern(s, name, id)
						} else {
							transformObjectBindingPattern(s, name, id)
						}
					}
				}
			}
		})
		if ids.IsEmpty() {
			panic("transformer: transformOptimizedArrayBindingPattern: no ids") // upstream assert
		}
		s.Prereq(luau.NewVariableDeclaration(ids, rhs))
		s.PrereqList(statements)
	})
}

// ---------------------------------------------------------------------------
// Assignment patterns — `[a, b] = exp`
// ---------------------------------------------------------------------------

func transformArrayAssignmentPattern(s *State, assignmentPattern *ast.Node, parentID luau.AnyIdentifier) {
	index := 0
	idStack := []luau.AnyIdentifier{}
	patternType := s.Checker.GetTypeOfAssignmentPattern(assignmentPattern)
	accessor := getAccessorForBindingType(s, assignmentPattern, patternType)
	destructor := getSpreadDestructorForType(s, patternType)
	for _, element := range assignmentPattern.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsOmittedExpression(element) {
			accessor(s, parentID, index, &idStack, true)
		} else {
			var initializer *ast.Node
			if ast.IsBinaryExpression(element) {
				binary := element.AsBinaryExpression()
				initializer = SkipDownwards(binary.Right)
				element = SkipDownwards(binary.Left)
			}

			var value luau.Expression
			if ast.IsSpreadElement(element) {
				spread := element.AsSpreadElement()
				if ast.IsObjectLiteralExpression(spread.Expression) || ast.IsArrayLiteralExpression(spread.Expression) || destructor == nil {
					s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
					index++
					continue
				}
				value = destructor(s, parentID, index, idStack)
				element = spread.Expression
			} else {
				value = accessor(s, parentID, index, &idStack, false)
			}
			if ast.IsIdentifier(element) || ast.IsElementAccessExpression(element) || ast.IsPropertyAccessExpression(element) {
				id := transformWritableExpression(s, element, initializer != nil)
				s.Prereq(luau.NewAssignment(id, "=", value))
				if initializer != nil {
					s.Prereq(transformInitializer(s, id, initializer))
				}
			} else if ast.IsArrayLiteralExpression(element) {
				id := s.PushToVar(value, "binding")
				if initializer != nil {
					s.Prereq(transformInitializer(s, id, initializer))
				}
				transformArrayAssignmentPattern(s, element, id)
			} else if ast.IsObjectLiteralExpression(element) {
				id := s.PushToVar(value, "binding")
				if initializer != nil {
					s.Prereq(transformInitializer(s, id, initializer))
				}
				transformObjectAssignmentPattern(s, element, id)
			} else {
				panic("transformer: transformArrayAssignmentPattern invalid element: " + kindName(element.Kind)) // upstream assert
			}
		}
		index++
	}
}

// transformOptimizedArrayAssignmentPattern ports transformBinaryExpression.ts
// transformOptimizedArrayAssignmentPattern (L36-111): the multi-assign form
// `a, b = <rhs...>` (the swap pattern `[a, b] = [b, a]` -> `a, b = b, a`).
// Used for LuaTuple-call RHS and for literal-array RHS in statement position
// — note there is NO hoist gate here (assignment targets are already
// declared). Emission order: `local _t1, _t2` (temps for nested patterns) ->
// target prereqs (index computations) -> the multi-Assignment -> trailing
// statements (defaults + nested destructures).
func transformOptimizedArrayAssignmentPattern(s *State, assignmentPattern *ast.Node, rhs luau.NodeOrList) {
	variables := luau.NewList[luau.AnyIdentifier]()
	writes := luau.NewList[luau.WritableExpression]()
	writesPrereqs := luau.NewList[luau.Statement]()
	statements := s.CaptureStatements(func() {
		for _, element := range assignmentPattern.AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsOmittedExpression(element) {
				writes.Push(luau.TempID(""))
			} else if ast.IsSpreadElement(element) {
				s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(element))
			} else {
				var initializer *ast.Node
				if ast.IsBinaryExpression(element) {
					binary := element.AsBinaryExpression()
					initializer = SkipDownwards(binary.Right)
					element = SkipDownwards(binary.Left)
				}

				if ast.IsIdentifier(element) || ast.IsElementAccessExpression(element) || ast.IsPropertyAccessExpression(element) {
					var id luau.WritableExpression
					idPrereqs := s.CaptureStatements(func() {
						id = transformWritableExpression(s, element, true)
					})
					writesPrereqs.PushList(idPrereqs)
					writes.Push(id)
					if initializer != nil {
						s.Prereq(transformInitializer(s, id, initializer))
					}
				} else if ast.IsArrayLiteralExpression(element) {
					id := luau.TempID("binding")
					variables.Push(id)
					writes.Push(id)
					if initializer != nil {
						s.Prereq(transformInitializer(s, id, initializer))
					}
					transformArrayAssignmentPattern(s, element, id)
				} else if ast.IsObjectLiteralExpression(element) {
					id := luau.TempID("binding")
					variables.Push(id)
					writes.Push(id)
					if initializer != nil {
						s.Prereq(transformInitializer(s, id, initializer))
					}
					transformObjectAssignmentPattern(s, element, id)
				} else {
					panic("transformer: transformOptimizedArrayAssignmentPattern invalid element: " + kindName(element.Kind)) // upstream assert
				}
			}
		}
	})
	if variables.IsNonEmpty() {
		s.Prereq(luau.NewVariableDeclaration(variables, nil))
	}
	s.PrereqList(writesPrereqs)
	if writes.IsEmpty() {
		panic("transformer: transformOptimizedArrayAssignmentPattern: no writes") // upstream assert
	}
	s.Prereq(luau.NewAssignment(writes, "=", rhs))
	s.PrereqList(statements)
}
