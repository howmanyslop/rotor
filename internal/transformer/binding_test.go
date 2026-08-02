package transformer_test

import (
	"path/filepath"
	"testing"

	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

// The expected text in every test below is byte-for-byte what rbxtsc 3.0.0
// emits for the same source (verified via scratch files compiled through
// testdata/diff/project — see tools/oracle/oracle.ps1 for the technique).

func renderDestructuringFile(t *testing.T, relPath string) string {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "destructuring"), relPath)
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
	return render.RenderAST(statements)
}

func destructuringDiagnostics(t *testing.T, relPath string) []transformer.Diagnostic {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "destructuring"), relPath)
	transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	return s.Diags.Flush()
}

// TestNestedDestructuring: array-in-object and object-in-array patterns —
// one `_binding` temp per nesting level; the object-in-array's array pattern
// over an identifier-free literal RHS does NOT collapse (RHS is `records`, an
// identifier), so the fallback per-element form appears at both levels.
func TestNestedDestructuring(t *testing.T) {
	want := `local data = {
	list = { 1, 2 },
	info = {
		name = "n",
	},
}
local _binding = data.list
local u = _binding[1]
local v = _binding[2]
local _binding_1 = data.info
local name = _binding_1.name
local records = { {
	id = 1,
}, {
	id = 2,
} }
local _binding_2 = records[1]
local firstId = _binding_2.id
print(u, v, name, firstId)
`
	if got := renderDestructuringFile(t, "src/nested.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestDestructuringDefaultsWithPrereqs: a default expression with its own
// prereqs (`b++` needs an `_original` temp) — extract first, then nil-check,
// with the prereqs INSIDE the if (TS lazy-default semantics).
func TestDestructuringDefaultsWithPrereqs(t *testing.T) {
	want := `local b = 1
local source = {}
local _binding = source
local a = _binding[1]
if a == nil then
	local _original = b
	b += 1
	a = _original
end
local _binding_1 = {}
local c = _binding_1.m
if c == nil then
	local _original = b
	b += 1
	c = _original
end
print(a, b, c)
`
	if got := renderDestructuringFile(t, "src/defaults.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestDestructuringOmittedElements: holes advance the accessor index without
// binding anything (identifier RHS, fallback path); in the optimized
// literal-RHS multi-local form a hole becomes a `_` placeholder local.
func TestDestructuringOmittedElements(t *testing.T) {
	want := `local arr = { 1, 2, 3, 4, 5 }
local second = arr[2]
local fourth = arr[4]
local _, y = 10, 20
print(second, fourth, y)
`
	if got := renderDestructuringFile(t, "src/omitted.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestParameterDestructuring: binding-pattern parameters become a `_param`
// temp destructured at the top of the body; parameter defaults run BEFORE
// destructuring statements of later params; `...[p, q]` flattens the pattern
// elements into real parameters (no `...` capture).
func TestParameterDestructuring(t *testing.T) {
	want := `local function f(_param)
	local a = _param[1]
	local b = _param[2]
	return a + b
end
local function g(_param, scale)
	local x = _param.x
	local y = _param.y
	if scale == nil then
		scale = 2
	end
	return (x + y) * scale
end
local function h(p, q)
	return p * q
end
print(f({ 1, 2 }), g({
	x = 1,
	y = 2,
}), h(3, 4))
`
	if got := renderDestructuringFile(t, "src/params.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestDestructuringFromCallRHS: a (non-LuaTuple) call RHS lands in a
// `_binding` temp exactly once — both for the binding form and the
// assignment form (the call must execute exactly once).
func TestDestructuringFromCallRHS(t *testing.T) {
	want := `local function make()
	return { 1, 2 }
end
local _binding = make()
local p = _binding[1]
local q = _binding[2]
local m = 0
local n = 0
local _binding_1 = make()
m = _binding_1[1]
n = _binding_1[2]
print(p, q, m, n)
`
	if got := renderDestructuringFile(t, "src/callrhs.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestLuaTupleDestructuring: a LuaTuple-returning call RHS is NOT
// array-wrapped (shouldWrapLuaTuple) and unpacks directly — multi-local
// `local d1, d2 = multi()` for the binding form, multi-assign with a `_`
// placeholder for the omitted slot in the assignment form.
func TestLuaTupleDestructuring(t *testing.T) {
	want := `local d1, d2 = multi()
local e1 = 0
e1, _ = multi()
print(d1, d2, e1)
`
	if got := renderDestructuringFile(t, "src/luatuple.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestIterationAccessors: the non-array binding accessors —
// IterableFunction<LuaTuple<T>> packs each element read in `{ _binding() }`;
// an omitted IterableFunction<T> element calls the function as a bare
// statement to advance; an omitted Set element calls `next(_binding)` as a
// statement WITHOUT pushing the result onto the idStack, so the following
// element re-reads from the start (upstream quirk, ported verbatim).
func TestIterationAccessors(t *testing.T) {
	want := `local p1 = { pairsIter() }
local p2 = { pairsIter() }
print(p1[1], p2[2])
nums()
local second = nums()
print(second)
next(tags)
local _value = next(tags)
local t2 = _value
print(t2)
`
	if got := renderDestructuringFile(t, "src/iteraccessors.ts"); got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
