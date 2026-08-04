package render

import (
	"testing"

	"rotor/internal/luau"
)

func solveFor(stmts *luau.List[luau.Statement]) *RenderState {
	s := NewRenderState()
	solveTempIDs(s, stmts)
	return s
}

func TestTempIdsBasic(t *testing.T) {
	t1, t2 := luau.TempID(""), luau.TempID("")
	stmts := luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(t1, luau.Num(1)),
		luau.NewVariableDeclaration(t2, luau.Num(2)),
	)
	s := solveFor(stmts)
	if got := s.seenTempNodes[t1.ID]; got != "_" {
		t.Errorf("first temp = %q, want %q", got, "_")
	}
	if got := s.seenTempNodes[t2.ID]; got != "_1" {
		t.Errorf("second temp = %q, want %q", got, "_1")
	}
}

func TestTempIdsNamed(t *testing.T) {
	t1, t2 := luau.TempID("foo"), luau.TempID("foo")
	stmts := luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(t1, luau.Num(1)),
		luau.NewVariableDeclaration(t2, luau.Num(2)),
	)
	s := solveFor(stmts)
	if got := s.seenTempNodes[t1.ID]; got != "_foo" {
		t.Errorf("first = %q, want _foo", got)
	}
	if got := s.seenTempNodes[t2.ID]; got != "_foo_1" {
		t.Errorf("second = %q, want _foo_1", got)
	}
}

func TestTempIdsAvoidDeclaredLocals(t *testing.T) {
	tmp := luau.TempID("foo")
	stmts := luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(luau.ID("_foo"), luau.Num(1)),
		luau.NewVariableDeclaration(tmp, luau.Num(2)),
	)
	s := solveFor(stmts)
	if got := s.seenTempNodes[tmp.ID]; got != "_foo_1" {
		t.Errorf("temp = %q, want _foo_1 (collides with declared _foo)", got)
	}
}

func TestTempIdsAvoidLocalFunctionDeclarations(t *testing.T) {
	tmp := luau.TempID("foo")
	statements := luau.NewList[luau.Statement](
		luau.NewFunctionDeclaration(
			true,
			luau.ID("_foo"),
			luau.NewList[luau.AnyIdentifier](),
			false,
			luau.NewList[luau.Statement](),
		),
		luau.NewFunctionDeclaration(
			true,
			tmp,
			luau.NewList[luau.AnyIdentifier](),
			false,
			luau.NewList[luau.Statement](),
		),
	)
	state := solveFor(statements)
	if got := state.seenTempNodes[tmp.ID]; got != "_foo_1" {
		t.Errorf("temp = %q, want _foo_1 (collides with local function _foo)", got)
	}
}

func TestTempIdsAvoidIdentifierUses(t *testing.T) {
	tmp := luau.TempID("foo")
	statements := luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(tmp, luau.Num(1)),
		luau.NewCallStatement(luau.NewCall(
			luau.ID("_foo"),
			luau.NewList[luau.Expression](),
		)),
	)
	state := solveFor(statements)
	if got := state.seenTempNodes[tmp.ID]; got != "_foo_1" {
		t.Errorf("temp = %q, want _foo_1 (collides with identifier use _foo)", got)
	}
}

func TestTempIdsAvoidNestedIdentifierUses(t *testing.T) {
	tmp := luau.TempID("foo")
	statements := luau.NewList[luau.Statement](
		luau.NewVariableDeclaration(tmp, luau.Num(1)),
		luau.NewFunctionDeclaration(
			true,
			luau.ID("later"),
			luau.NewList[luau.AnyIdentifier](),
			false,
			luau.NewList[luau.Statement](luau.NewCallStatement(luau.NewCall(
				luau.ID("_foo"),
				luau.NewList[luau.Expression](),
			))),
		),
	)
	state := solveFor(statements)
	if got := state.seenTempNodes[tmp.ID]; got != "_foo_1" {
		t.Errorf("root temp = %q, want _foo_1 (collides with nested identifier use _foo)", got)
	}
}

func TestTempIdsScopedFunctionsDontCollide(t *testing.T) {
	// two function expressions each with their own temp: both may use "_"
	mk := func() (*luau.TemporaryIdentifier, luau.Statement) {
		tmp := luau.TempID("")
		body := luau.NewList[luau.Statement](luau.NewVariableDeclaration(tmp, luau.Num(1)))
		fn := luau.NewFunctionExpression(luau.NewList[luau.AnyIdentifier](), false, body)
		return tmp, luau.NewVariableDeclaration(luau.ID("f"), fn)
	}
	ta, sa := mk()
	tb, sb := mk()
	s := solveFor(luau.NewList[luau.Statement](sa, sb))
	if s.seenTempNodes[ta.ID] != "_" || s.seenTempNodes[tb.ID] != "_" {
		t.Errorf("separate function scopes should both get %q: got %q, %q",
			"_", s.seenTempNodes[ta.ID], s.seenTempNodes[tb.ID])
	}
}
