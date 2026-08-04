package transformer_test

import (
	"path/filepath"
	"testing"

	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

// TestNamedFunctionExpressionDiagnostic: `const f = function named() {}` is
// upstream's noFunctionExpressionName error; the transform continues with
// the name dropped (output shape per the reference — rbxtsc aborts emission
// on the error, so only the diagnostic is oracle-pinned).
func TestNamedFunctionExpressionDiagnostic(t *testing.T) {
	s := buildState(t, filepath.Join("testdata", "functions"), "src/namedexpr.ts")
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)

	ds := s.Diags.Flush()
	if !hasDiagnostic(ds, "noFunctionExpressionName", "Function expression names are not supported!") {
		t.Errorf("no noFunctionExpressionName diagnostic; got: %v", ds)
	}

	want := `local f = function(x)
	return x
end
print(f(1))
`
	if got := render.RenderAST(statements); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_emits_local_declaration(t *testing.T) {
	want := `local function doSomeBullshit()
end
useEffect(doSomeBullshit, {})
`
	if got := renderFunctionsFile(t, "src/namedcallarg.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_matching_const_emits_recursive_local_declaration(t *testing.T) {
	want := `local function namedFunction(value)
	return if value == 0 then 0 else namedFunction(value - 1)
end
print(namedFunction(2))
`
	if got := renderFunctionsFile(t, "src/namedconst.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_preserves_recursion_and_argument_order(t *testing.T) {
	want := `local function before()
	return 1
end
local function after()
	return 2
end
local _exp = before()
local function recurse(value)
	return if value == 0 then 0 else recurse(value - 1)
end
consume(_exp, recurse, after())
`
	if got := renderFunctionsFile(t, "src/namedcallordering.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_unsupported_contexts_keep_diagnostics(t *testing.T) {
	diagnostics := transformExpectingDiagnostics(t, "src/namedunsupported.ts")
	namedFunctionDiagnostics := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "noFunctionExpressionName" {
			namedFunctionDiagnostics++
		}
	}
	if namedFunctionDiagnostics != 15 {
		t.Errorf("noFunctionExpressionName diagnostic count = %d, want 15; got: %v", namedFunctionDiagnostics, diagnostics)
	}
}

func TestNamedFunctionExpression_matching_const_preserves_switch_case_predeclaration(t *testing.T) {
	want := `repeat
	local namedFunction
	if selector == 0 then
		function namedFunction()
			return namedFunction
		end
		break
	end
	if selector == 1 then
		-- @ts-expect-error exercising cross-clause predeclaration lowering
		print(namedFunction)
		break
	end
until true
`
	if got := renderFunctionsFile(t, "src/namedconstswitch.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_does_not_shadow_callee(t *testing.T) {
	want := `local function useEffect_1()
	return useEffect_1()
end
useEffect(useEffect_1, {})
`
	if got := renderFunctionsFile(t, "src/namedcallcalleecollision.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_does_not_shadow_outer_const(t *testing.T) {
	want := `local collide = 5
local function collide_1(value)
	return if value == 0 then 0 else collide_1(value - 1)
end
consume(collide, collide_1, collide)
print(collide)
`
	if got := renderFunctionsFile(t, "src/namedcalloutercollision.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_ignores_noncolliding_generated_prefix(t *testing.T) {
	want := `local function _collide(callback)
	return callback()
end
local function collide()
	return collide()
end
_collide(collide)
`
	if got := renderFunctionsFile(t, "src/namedcallgeneratedcollision.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_ignores_noncolliding_ambient_prefix(t *testing.T) {
	want := `local function collide()
	return collide()
end
_collide(collide)
`
	if got := renderFunctionsFile(t, "src/namedcallambientcollision.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestNamedFunctionExpression_direct_call_argument_ignores_noncolliding_nested_prefix(t *testing.T) {
	want := `local function collide()
	return collide()
end
consume(collide)
local function later()
	return _collide()
end
`
	if got := renderFunctionsFile(t, "src/namedcallnestedcollision.ts"); got != want {
		t.Errorf("rendered output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
