package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

// This file ports the object-pattern destructuring transforms:
// nodes/binding/transformObjectBindingPattern.ts and
// nodes/binding/transformObjectAssignmentPattern.ts.

// ---------------------------------------------------------------------------
// Binding patterns — `const { a, b: c } = exp`
// ---------------------------------------------------------------------------

func spreadDestructureObject(s *State, parentID luau.AnyIdentifier, preSpreadNames []luau.Expression) luau.Expression {
	extractedMembers := luau.NewList[luau.Expression]()
	for _, expression := range preSpreadNames {
		switch expression := expression.(type) {
		case *luau.PropertyAccessExpression:
			extractedMembers.Push(luau.Str(expression.Name))
		case *luau.ComputedIndexExpression:
			extractedMembers.Push(expression.Index)
		default:
			panic("transformer: spreadDestructureObject: unknown expression type")
		}
	}
	extracted := s.PushToVar(luau.NewSet(extractedMembers), "extracted")
	rest := s.PushToVar(luau.NewMap(luau.NewList[*luau.MapField]()), "rest")
	keyID := luau.TempID("k")
	valueID := luau.TempID("v")
	s.Prereq(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](keyID, valueID),
		parentID,
		luau.NewList[luau.Statement](
			luau.NewIf(
				luau.NewUnary("not", luau.NewComputedIndex(extracted, keyID)),
				luau.NewList[luau.Statement](
					luau.NewAssignment(luau.NewComputedIndex(rest, keyID), "=", valueID),
				),
				nil,
			),
		),
	))
	return rest
}

func transformObjectBindingPattern(s *State, bindingPattern *ast.Node, parentID luau.AnyIdentifier) {
	validateNotAnyType(s, bindingPattern)
	preSpreadNames := make([]luau.Expression, 0, len(bindingPattern.AsBindingPattern().Elements.Nodes))
	for _, element := range bindingPattern.AsBindingPattern().Elements.Nodes {
		bindingElement := element.AsBindingElement()
		name := bindingElement.Name()
		prop := bindingElement.PropertyName
		isSpread := bindingElement.DotDotDotToken != nil
		if ast.IsIdentifier(name) {
			var value luau.Expression
			if isSpread {
				value = spreadDestructureObject(s, parentID, preSpreadNames)
			} else {
				nameOrProp := prop
				if nameOrProp == nil {
					nameOrProp = name
				}
				value = objectAccessor(s, parentID, s.GetType(bindingPattern), nameOrProp)
			}
			preSpreadNames = append(preSpreadNames, value)
			if isSpread && IsPossiblyType(s, s.GetType(bindingPattern), IsRobloxType(s)) {
				s.Diags.Add(DiagNoRestSpreadingOfRobloxTypes(element))
				continue
			}
			id := transformVariable(s, name, value)
			if bindingElement.Initializer != nil {
				s.Prereq(transformInitializer(s, id, bindingElement.Initializer))
			}
		} else {
			if prop == nil {
				panic("transformer: transformObjectBindingPattern: nested pattern without property name") // upstream assert
			}
			if isSpread {
				panic("transformer: transformObjectBindingPattern: nested spread pattern")
			}
			value := objectAccessor(s, parentID, s.GetType(bindingPattern), prop)
			preSpreadNames = append(preSpreadNames, value)
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
}

// ---------------------------------------------------------------------------
// Assignment patterns — `({ a, b: target } = exp)`
// ---------------------------------------------------------------------------

// transformObjectAssignmentPattern ports transformObjectAssignmentPattern.ts
// (L14-90), over an ObjectLiteralExpression LHS. The pattern type comes from
// the checker's getTypeOfAssignmentPattern (NOT getType). Shorthand defaults
// arrive via objectAssignmentInitializer (`{ a = 1 }`); property-assignment
// defaults as BinaryExpression initializers (`{ a: b = 1 }`). A
// SpreadAssignment raises noNestedSpreadsInAssignmentPatterns and ABORTS the
// remaining properties.
func transformObjectAssignmentPattern(s *State, assignmentPattern *ast.Node, parentID luau.AnyIdentifier) {
	preSpreadNames := make([]luau.Expression, 0, len(assignmentPattern.AsObjectLiteralExpression().Properties.Nodes))
	for _, property := range assignmentPattern.AsObjectLiteralExpression().Properties.Nodes {
		if ast.IsShorthandPropertyAssignment(property) {
			shorthand := property.AsShorthandPropertyAssignment()
			name := shorthand.Name()
			value := objectAccessor(s, parentID,
				s.Checker.GetTypeOfAssignmentPattern(assignmentPattern), name)
			preSpreadNames = append(preSpreadNames, value)
			id := transformWritableExpression(s, name, shorthand.ObjectAssignmentInitializer != nil)
			s.Prereq(luau.NewAssignment(id, "=", value))
			if _, isAnyIdentifier := id.(luau.AnyIdentifier); !isAnyIdentifier {
				panic("transformer: transformObjectAssignmentPattern: shorthand target is not an identifier") // upstream assert
			}
			if shorthand.ObjectAssignmentInitializer != nil {
				s.Prereq(transformInitializer(s, id, shorthand.ObjectAssignmentInitializer))
			}
		} else if ast.IsSpreadAssignment(property) {
			value := spreadDestructureObject(s, parentID, preSpreadNames)
			expression := property.AsSpreadAssignment().Expression
			if ast.IsObjectLiteralExpression(expression) || ast.IsArrayLiteralExpression(expression) {
				s.Diags.Add(DiagNoNestedSpreadsInAssignmentPatterns(property))
				continue
			}
			id := transformWritableExpression(s, expression, true)
			s.Prereq(luau.NewAssignment(id, "=", value))
		} else if ast.IsPropertyAssignment(property) {
			propertyAssignment := property.AsPropertyAssignment()
			name := propertyAssignment.Name()
			init := propertyAssignment.Initializer
			var initializer *ast.Node
			if ast.IsBinaryExpression(propertyAssignment.Initializer) {
				binary := propertyAssignment.Initializer.AsBinaryExpression()
				initializer = SkipDownwards(binary.Right)
				init = SkipDownwards(binary.Left)
			}

			value := objectAccessor(s, parentID,
				s.Checker.GetTypeOfAssignmentPattern(assignmentPattern), name)
			preSpreadNames = append(preSpreadNames, value)
			if ast.IsIdentifier(init) || ast.IsElementAccessExpression(init) || ast.IsPropertyAccessExpression(init) {
				id := transformWritableExpression(s, init, initializer != nil)
				s.Prereq(luau.NewAssignment(id, "=", value))
				if initializer != nil {
					s.Prereq(transformInitializer(s, id, initializer))
				}
			} else if ast.IsArrayLiteralExpression(init) {
				id := s.PushToVar(value, "binding")
				if initializer != nil {
					s.Prereq(transformInitializer(s, id, initializer))
				}
				if !ast.IsIdentifier(name) {
					panic("transformer: transformObjectAssignmentPattern: nested array pattern with non-identifier name") // upstream assert
				}
				transformArrayAssignmentPattern(s, init, id)
			} else if ast.IsObjectLiteralExpression(init) {
				id := s.PushToVar(value, "binding")
				if initializer != nil {
					s.Prereq(transformInitializer(s, id, initializer))
				}
				transformObjectAssignmentPattern(s, init, id)
			} else {
				panic("transformer: transformObjectAssignmentPattern invalid initializer: " + kindName(init.Kind)) // upstream assert
			}
		} else {
			panic("transformer: transformObjectAssignmentPattern invalid property: " + kindName(property.Kind)) // upstream assert
		}
	}
}
