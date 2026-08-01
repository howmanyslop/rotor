package transformer

import (
	"testing"

	"rotor/internal/luau"
	"rotor/internal/luau/render"
	"rotor/tsgo/ast"
)

func TestBitwiseFlattening(t *testing.T) {
	_, expression := parseFirstExpression(t, "a & b & c;")

	operands := flattenBitwiseChain(ast.KindAmpersandToken, expression.AsBinaryExpression().Left)
	if len(operands) != 2 {
		t.Fatalf("flattened left spine length = %d, want 2", len(operands))
	}
	if operands[0].Text() != "a" || operands[1].Text() != "b" {
		t.Fatalf("flattened operands = %q, %q; want a, b", operands[0].Text(), operands[1].Text())
	}

	_, mixed := parseFirstExpression(t, "a & b | c;")
	if got := len(flattenBitwiseChain(ast.KindBarToken, mixed.AsBinaryExpression().Left)); got != 1 {
		t.Fatalf("mixed-precedence chain length = %d, want 1", got)
	}

	call := createBitwiseCall(ast.KindAmpersandToken, []luau.Expression{luau.ID("a"), luau.ID("b"), luau.ID("c")})
	if got, want := render.RenderAST(luau.NewList[luau.Statement](luau.NewCallStatement(call))), "bit32.band(a, b, c)\n"; got != want {
		t.Fatalf("rendered bitwise call = %q, want %q", got, want)
	}
	compound := createBitwiseCall(ast.KindAmpersandEqualsToken, []luau.Expression{luau.ID("x"), luau.Num(3)})
	if got, want := render.RenderAST(luau.NewList[luau.Statement](luau.NewCallStatement(compound))), "bit32.band(x, 3)\n"; got != want {
		t.Fatalf("rendered compound bitwise call = %q, want %q", got, want)
	}
}

func TestRangeLiteralStep(t *testing.T) {
	if got, ok := getLiteralNumberValue(luau.Num(-1)); !ok || got != -1 {
		t.Fatalf("getLiteralNumberValue(-1) = (%v, %t), want (-1, true)", got, ok)
	}
	if _, ok := getLiteralNumberValue(luau.ID("step")); ok {
		t.Fatal("getLiteralNumberValue(step) = true, want false")
	}
}

func TestNullableLuaTuple(t *testing.T) {
	// Spread assignment: [a, ...rest] = f() — should wrap LuaTuple
	_, expression := parseFirstExpression(t, "[a, ...rest] = f();")
	callNode := expression.AsBinaryExpression().Right
	if !shouldWrapLuaTuple(NewTestState(), callNode, luau.NewCall(luau.ID("f"), luau.NewList[luau.Expression]())) {
		t.Fatal("spread assignment unexpectedly consumed a LuaTuple call")
	}

	// Optional chain: f?.()[0] — should wrap LuaTuple because call has QuestionDotToken
	_, optional := parseFirstExpression(t, "f?.()[0];")
	optionalCall := optional.AsElementAccessExpression().Expression
	if !shouldWrapLuaTuple(NewTestState(), optionalCall, luau.NewCall(luau.ID("f"), luau.NewList[luau.Expression]())) {
		t.Fatal("optional-chain call unexpectedly skipped LuaTuple wrapping")
	}
}

func TestBigIntPropertyDiagnostic(t *testing.T) {
	s, expression := parseFirstExpression(t, "({ 1n: value });")
	property := expression.AsParenthesizedExpression().Expression.AsObjectLiteralExpression().Properties.Nodes[0]
	name := property.AsPropertyAssignment().Name()
	objectAccessor(s, luau.ID("obj"), nil, name)
	diagnostics := s.Diags.Flush()
	if len(diagnostics) != 1 || diagnostics[0].Code != "noBigInt" {
		t.Fatalf("BigInt diagnostics = %#v, want one noBigInt diagnostic", diagnostics)
	}
}
