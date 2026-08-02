package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/checker"
)

type spreadDestructor func(s *State, parentID luau.AnyIdentifier, index int, idStack []luau.AnyIdentifier) luau.Expression

func getSpreadDestructorForType(s *State, t *checker.Type) spreadDestructor {
	if IsDefinitelyType(s, t, IsArrayType(s)) {
		return spreadDestructureArray
	}
	if IsDefinitelyType(s, t, IsSetType(s)) {
		return spreadDestructureSet
	}
	if IsDefinitelyType(s, t, IsMapType(s)) || IsSharedTableType(s, t) {
		return spreadDestructureMap
	}
	if IsDefinitelyType(s, t, IsGeneratorType(s)) || IsDefinitelyType(s, t, IsObjectType) {
		return spreadDestructureGenerator
	}
	return nil
}

func spreadDestructureArray(s *State, parentID luau.AnyIdentifier, index int, idStack []luau.AnyIdentifier) luau.Expression {
	return luau.NewCall(luau.GlobalProperty("table", "move"), luau.NewList[luau.Expression](
		parentID,
		luau.Num(float64(index+1)),
		luau.NewUnary("#", parentID),
		luau.Num(1),
		luau.NewArray(luau.NewList[luau.Expression]()),
	))
}

func spreadDestructureSet(s *State, parentID luau.AnyIdentifier, index int, idStack []luau.AnyIdentifier) luau.Expression {
	extracted := luau.NewList[*luau.MapField]()
	for _, id := range idStack {
		extracted.Push(luau.NewMapField(id, luau.Bool(true)))
	}
	extractedID := s.PushToVar(luau.NewMap(extracted), "extracted")
	restID := s.PushToVar(luau.NewArray(luau.NewList[luau.Expression]()), "rest")
	keyID := luau.TempID("k")
	s.Prereq(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](keyID),
		parentID,
		luau.NewList[luau.Statement](
			luau.NewIf(
				luau.NewUnary("not", luau.NewComputedIndex(extractedID, keyID)),
				luau.NewList[luau.Statement](luau.NewCallStatement(luau.NewCall(
					luau.GlobalProperty("table", "insert"),
					luau.NewList[luau.Expression](restID, keyID),
				))),
				nil,
			),
		),
	))
	return restID
}

func spreadDestructureMap(s *State, parentID luau.AnyIdentifier, index int, idStack []luau.AnyIdentifier) luau.Expression {
	extracted := luau.NewList[*luau.MapField]()
	for _, id := range idStack {
		extracted.Push(luau.NewMapField(id, luau.Bool(true)))
	}
	extractedID := s.PushToVar(luau.NewMap(extracted), "extracted")
	restID := s.PushToVar(luau.NewArray(luau.NewList[luau.Expression]()), "rest")
	keyID := luau.TempID("k")
	valueID := luau.TempID("v")
	s.Prereq(luau.NewFor(
		luau.NewList[luau.AnyIdentifier](keyID, valueID),
		parentID,
		luau.NewList[luau.Statement](
			luau.NewIf(
				luau.NewUnary("not", luau.NewComputedIndex(extractedID, keyID)),
				luau.NewList[luau.Statement](luau.NewCallStatement(luau.NewCall(
					luau.GlobalProperty("table", "insert"),
					luau.NewList[luau.Expression](restID, luau.NewArray(luau.NewList[luau.Expression](keyID, valueID))),
				))),
				nil,
			),
		),
	))
	return restID
}

func spreadDestructureGenerator(s *State, parentID luau.AnyIdentifier, index int, idStack []luau.AnyIdentifier) luau.Expression {
	restID := s.PushToVar(luau.NewArray(luau.NewList[luau.Expression]()), "rest")
	valueID := luau.TempID("v")
	s.Prereq(luau.NewWhile(luau.Bool(true), luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(valueID, luau.NewCall(luau.NewPropertyAccess(parentID, "next"), luau.NewList[luau.Expression]())),
		luau.NewIf(
			luau.NewBinary(luau.NewPropertyAccess(valueID, "done"), "==", luau.Bool(true)),
			luau.NewList[luau.Statement](luau.NewBreak()),
			nil,
		),
		luau.NewCallStatement(luau.NewCall(
			luau.GlobalProperty("table", "insert"),
			luau.NewList[luau.Expression](restID, luau.NewPropertyAccess(valueID, "value")),
		)),
	)))
	return restID
}

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
