package transformer_test

import (
	"path/filepath"
	"testing"

	"rotor/internal/luau"
	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

// All expected text in this file is byte-for-byte what rbxtsc 3.0.0 emits for
// the same source (compiled through testdata/diff/project on 2026-06-07;
// header and trailing return stripped — those belong to TransformSourceFile,
// not the statement list under test).

func renderSpreadFixture(t *testing.T, relPath string) (string, *transformer.State) {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "spread"), relPath)
	if symbol := s.Checker.GetSymbolAtLocation(s.SourceFile.AsNode()); symbol != nil {
		s.SetModuleIDBySymbol(symbol, luau.GlobalID("exports"))
	}
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	return render.RenderAST(statements), s
}

// TestObjectSpread pins the object-literal spread shapes (digest §3.1/§5.6):
//
//   - fast path ONLY when a definitely-object spread hits an EMPTY inline map
//     (spread-first): `local _object = table.clone(exp)` + metatable strip —
//     even `{ ...a, ...b }` clones only for the first spread;
//   - `{ z: 3, ...base }` (spread not first) NEVER table.clones: the inline
//     map spills to a temp and a generalized-iteration copy loop runs (NO
//     pairs());
//   - possibly-undefined spread: if-wrapped loop; a bare identifier needs no
//     `_spread` temp and the truthiness check is the bare identifier, while a
//     property access pins `local _spread = props.event`;
//   - computed keys after a spread assign through the temp
//     (`_object_7[getObj().a] = 9`).
func TestObjectSpread(t *testing.T) {
	got, s := renderSpreadFixture(t, "src/object.ts")

	want := `local base = {
	x = 1,
	y = 2,
}
local extra = {
	z = 3,
}
local function getObj()
	return {
		a = "k",
		b = 1,
	}
end
local _object = table.clone(base)
setmetatable(_object, nil)
local spreadOnly = _object
local _object_1 = table.clone(base)
setmetatable(_object_1, nil)
_object_1.z = 3
local spreadFirst = _object_1
local _object_2 = {
	z = 3,
}
for _k, _v in base do
	_object_2[_k] = _v
end
local spreadLast = _object_2
local _object_3 = table.clone(base)
setmetatable(_object_3, nil)
for _k, _v in extra do
	_object_3[_k] = _v
end
local spreadTwice = _object_3
local _object_4 = {}
if maybe then
	for _k, _v in maybe do
		_object_4[_k] = _v
	end
end
_object_4.q = 1
local spreadMaybe = _object_4
local _object_5 = {
	a = 1,
}
local _spread = props.event
if _spread then
	for _k, _v in _spread do
		_object_5[_k] = _v
	end
end
local spreadOptionalProp = _object_5
local _object_6 = table.clone(getObj())
setmetatable(_object_6, nil)
_object_6.b = 2
local spreadCall = _object_6
local _object_7 = table.clone(base)
setmetatable(_object_7, nil)
_object_7[getObj().a] = 9
local computedAfterSpread = _object_7
print(spreadOnly, spreadFirst, spreadLast, spreadTwice, spreadMaybe, spreadOptionalProp, spreadCall, computedAfterSpread)
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

// TestArraySpread pins the array-literal spread shapes
// (transformArrayLiteralExpression.ts + getAddIterableToArrayBuilder.ts):
//
//   - `[...arr1]`: `local _array = {}` / `local _length = #_array` /
//     `table.move(arr1, 1, #arr1, _length + 1, _array)` — `#arr1` stays
//     inline because nothing follows the spread;
//   - `[...arr1, 5]`: `#arr1` pinned to `_arr1Length`, `_length += ...`
//     re-sync, then `_array[_length + 1] = 5`;
//   - `[5, ...arr1]`: inline `{ 5 }` spills at the spread;
//   - `[...arr1, 5, ...arr2, 6]`: amtElementsSinceUpdate is NOT reset by a
//     spread — the second table.move lands at `_length_4 + 2` and the final
//     element at `_length_4 + 2` after the re-sync (verbatim upstream);
//   - Set/Map/string spreads use the for-loop builders (Map entries rebuild
//     `{ _k, _v }` pairs; strings iterate `string.gmatch(str,
//     utf8.charpattern)`).
func TestArraySpread(t *testing.T) {
	got, s := renderSpreadFixture(t, "src/array.ts")

	want := `local function getObj()
	return {
		b = 1,
	}
end
local arr1 = { 1, 2 }
local arr2 = { 3, 4 }
local _array = {}
local _length = #_array
table.move(arr1, 1, #arr1, _length + 1, _array)
local a1 = _array
local _array_1 = {}
local _length_1 = #_array_1
local _arr1Length = #arr1
table.move(arr1, 1, _arr1Length, _length_1 + 1, _array_1)
_length_1 += _arr1Length
_array_1[_length_1 + 1] = 5
local a2 = _array_1
local _array_2 = { 5 }
local _length_2 = #_array_2
table.move(arr1, 1, #arr1, _length_2 + 1, _array_2)
local a3 = _array_2
local _array_3 = {}
local _length_3 = #_array_3
local _arr1Length_1 = #arr1
table.move(arr1, 1, _arr1Length_1, _length_3 + 1, _array_3)
_length_3 += _arr1Length_1
table.move(arr2, 1, #arr2, _length_3 + 1, _array_3)
local a4 = _array_3
local _array_4 = {}
local _length_4 = #_array_4
local _arr1Length_2 = #arr1
table.move(arr1, 1, _arr1Length_2, _length_4 + 1, _array_4)
_length_4 += _arr1Length_2
_array_4[_length_4 + 1] = 5
local _arr2Length = #arr2
table.move(arr2, 1, _arr2Length, _length_4 + 2, _array_4)
_length_4 += _arr2Length
_array_4[_length_4 + 2] = 6
local a5 = _array_4
local _array_5 = {}
local _length_5 = #_array_5
for _v in nset do
	_length_5 += 1
	_array_5[_length_5] = _v
end
local a6 = _array_5
local _array_6 = {}
local _length_6 = #_array_6
for _k, _v in nmap do
	_length_6 += 1
	_array_6[_length_6] = { _k, _v }
end
local a7 = _array_6
local _array_7 = {}
local _length_7 = #_array_7
for _char in string.gmatch(str, utf8.charpattern) do
	_length_7 += 1
	_array_7[_length_7] = _char
end
local a8 = _array_7
local _array_8 = {}
local _length_8 = #_array_8
local _arr1Length_3 = #arr1
table.move(arr1, 1, _arr1Length_3, _length_8 + 1, _array_8)
_length_8 += _arr1Length_3
_array_8[_length_8 + 1] = getObj().b
local a9 = _array_8
print(a1, a2, a3, a4, a5, a6, a7, a8, a9)
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

// TestCallSpread pins call-argument spread (transformSpreadElement.ts): a
// definitely-array spread is `unpack(arr1)` inline (also after leading
// arguments); a non-array iterable collects into a fresh `_array`/`_length`
// pair (NOTE `local _length = 0`, not `#_array`, in this path) through the
// shared builders before unpacking.
func TestCallSpread(t *testing.T) {
	got, s := renderSpreadFixture(t, "src/call.ts")

	want := `local arr1 = { 1, 2 }
local function takeNums(...)
	return select("#", ...)
end
print(takeNums(unpack(arr1)))
print(takeNums(1, unpack(arr1)))
local _array = {}
local _length = 0
for _v in nset do
	_length += 1
	_array[_length] = _v
end
print(takeNums(unpack(_array)))
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

// TestLogicalAssignments pins `??=`/`&&=`/`||=` (digest §4/§5.6):
//
//   - `??=` is a pure `== nil` if-wrap with the RHS evaluated lazily inside;
//     no temps for identifier targets, `local _index = i + 1` for element
//     access (array index +1 pinned), `local _exp = get()` for call-rooted
//     property access;
//   - `&&=`/`||=` capture `local _condition = <writable>`, truthiness-test
//     the ORIGINAL writable (string: `s ~= "" and s`; number:
//     `n ~= 0 and n == n and n`; `||=` negates), then UNCONDITIONALLY write
//     back `s = _condition`;
//   - expression position (`const useAsExpr = (v ??= 2)`) re-reads the
//     writable as the value.
func TestLogicalAssignments(t *testing.T) {
	got, s := renderSpreadFixture(t, "src/logical.ts")

	want := `local v
if v == nil then
	v = 1
end
local holder = {}
local function f()
	return 5
end
if holder.p == nil then
	holder.p = f()
end
local arrIdx = {}
local i = 0
local _index = i + 1
if arrIdx[_index] == nil then
	arrIdx[_index] = f()
end
local function get()
	return holder
end
local _exp = get()
if _exp.p == nil then
	_exp.p = f()
end
local s = "x"
local _condition = s
if s ~= "" and s then
	_condition = "y"
end
s = _condition
local _condition_1 = s
if not (s ~= "" and s) then
	_condition_1 = "z"
end
s = _condition_1
local n = 0
local _condition_2 = n
if not (n ~= 0 and n == n and n) then
	_condition_2 = 7
end
n = _condition_2
local _condition_3 = n
if n ~= 0 and n == n and n then
	_condition_3 = 8
end
n = _condition_3
if v == nil then
	v = 2
end
local useAsExpr = v
print(v, holder.p, arrIdx[i + 1], s, n, useAsExpr, i)
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

// TestNoPrecedingSpreadElement: a call-argument spread that is not the LAST
// argument raises noPrecedingSpreadElement (transformSpreadElement.ts L17-19).
func TestNoPrecedingSpreadElement(t *testing.T) {
	_, s := renderSpreadFixture(t, "src/diag.ts")
	found := false
	for _, d := range s.Diags.Flush() {
		if d.Code == "noPrecedingSpreadElement" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected noPrecedingSpreadElement diagnostic")
	}
}
