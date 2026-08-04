package transformer_test

import (
	"path/filepath"
	"strings"
	"testing"

	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

// The expected text in every test below is byte-for-byte what rbxtsc 3.0.0
// emits for the same source (verified via scratch files compiled through
// testdata/diff/project). renderFunctionsFile pins statement lists (header and
// module return stripped); renderFunctionsSourceFile pins whole files
// (header and export return included).

func renderFunctionsFile(t *testing.T, relPath string) string {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "functions"), relPath)
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	return render.RenderAST(statements)
}

func renderFunctionsSourceFile(t *testing.T, relPath string) string {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "functions"), relPath)
	statements := transformer.TransformSourceFile(s)
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	return render.RenderAST(statements)
}

// TestBodilessOverloadsDropped: overload signatures (`function over(a:
// number): number;`) have no body and emit NOTHING; only the implementation
// signature compiles.
func TestBodilessOverloadsDropped(t *testing.T) {
	want := `local function over(a)
	return 1
end
print(over(1), over("x"))
`
	if got := renderFunctionsFile(t, "src/overloads.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestExportDefaultNamedFunction: `export default function foo` keeps its own
// name for the local (and for self-recursion) — only the export KEY is
// `default`.
func TestExportDefaultNamedFunction(t *testing.T) {
	want := `-- Compiled with roblox-ts v3.0.0
local function foo(n)
	return if n == 0 then 1 else n * foo(n - 1)
end
return {
	default = foo,
}
`
	if got := renderFunctionsSourceFile(t, "src/defaultnamed.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestExportDefaultAnonymousFunction: an anonymous `export default function`
// is emitted under the literal name `default` and always localized.
func TestExportDefaultAnonymousFunction(t *testing.T) {
	want := `-- Compiled with roblox-ts v3.0.0
local function default(x)
	return x + 10
end
return {
	default = default,
}
`
	if got := renderFunctionsSourceFile(t, "src/defaultanon.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestObjectLiteralMethods: method shorthand `{ m() {} }` AND the
// FunctionExpression quirk `{ f: function () {} }` are both implicitly
// methods — each gets an explicit leading `self` parameter and call sites
// use `:`. (Arrows in object literals get NO self — pinned by fixture
// 15_arrows.)
func TestObjectLiteralMethods(t *testing.T) {
	want := `local obj = {
	m = function(self, x)
		return x + 1
	end,
	f = function(self, x)
		return x * 2
	end,
}
print(obj:m(1), obj:f(2))
`
	if got := renderFunctionsFile(t, "src/methods.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func transformExpectingDiagnostics(t *testing.T, relPath string) []transformer.Diagnostic {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "functions"), relPath)
	transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	return s.Diags.Flush()
}

func hasDiagnostic(ds []transformer.Diagnostic, code, messageSubstring string) bool {
	for _, d := range ds {
		if d.Code == code && strings.Contains(d.Message, messageSubstring) {
			return true
		}
	}
	return false
}

// TestAsyncFunctionDeclaration: a localized async function declaration emits
// `local f = TS.async(function() ... end)` — never a function statement
// (async_test.go pins the hoisted/method/expression variants).
func TestAsyncFunctionDeclaration(t *testing.T) {
	want := `local fetchValue = TS.async(function()
	return 1
end)
print(fetchValue())
`
	if got := renderFunctionsFile(t, "src/asyncfn.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestGeneratorFunctionDeclaration: generator declarations stay real function
// declarations; the body is swapped for `return TS.generator(function()
// <body> end)`.
func TestGeneratorFunctionDeclaration(t *testing.T) {
	want := `local function gen()
	return TS.generator(function()
		coroutine.yield(1)
	end)
end
print(gen())
`
	if got := renderFunctionsFile(t, "src/genfn.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestAsyncGeneratorFunctionsDiagnostic: `async function*` is upstream's own
// noAsyncGeneratorFunctions error — rbxtsc 3.0.0 reports exactly "Async
// generator functions are not supported!" (verified via the oracle project).
func TestAsyncGeneratorFunctionsDiagnostic(t *testing.T) {
	ds := transformExpectingDiagnostics(t, "src/asyncgen.ts")
	found := false
	for _, d := range ds {
		if d.Code == "noAsyncGeneratorFunctions" {
			found = true
			if !strings.Contains(d.Message, "Async generator functions are not supported!") {
				t.Errorf("noAsyncGeneratorFunctions message = %q, want upstream text", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("no noAsyncGeneratorFunctions diagnostic; got: %v", ds)
	}
}

// TestValidateMethodAssignment: assigning an arrow (callback) where the
// contextual type declares a method raises expectedMethodGotFunction;
// assigning a FunctionExpression (implicitly a method inside an object
// literal) where the contextual type declares a callback raises
// expectedFunctionGotMethod. Both messages verified against rbxtsc 3.0.0.
func TestValidateMethodAssignment(t *testing.T) {
	ds := transformExpectingDiagnostics(t, "src/methodmismatch.ts")
	if !hasDiagnostic(ds, "expectedMethodGotFunction", "Attempted to assign non-method where method was expected.") {
		t.Errorf("no expectedMethodGotFunction diagnostic; got: %v", ds)
	}
	if !hasDiagnostic(ds, "expectedFunctionGotMethod", "Attempted to assign method where non-method was expected.") {
		t.Errorf("no expectedFunctionGotMethod diagnostic; got: %v", ds)
	}
}
