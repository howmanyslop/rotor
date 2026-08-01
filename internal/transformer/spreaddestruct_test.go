package transformer_test

import "testing"

func TestObjectRest(t *testing.T) {
	want := `local _binding = {
	x = 1,
	y = 2,
}
local x = _binding.x
local _extracted = {
	["x"] = true,
}
local _rest = {}
for _k, _v in _binding do
	if not _extracted[_k] then
		_rest[_k] = _v
	end
end
local restObject = _rest
print(x, restObject)
`

	if got := renderDestructuringFile(t, "src/restobject.ts"); got != want {
		t.Errorf("rendered output differs from fork fixture:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestObjectRestAssignment(t *testing.T) {
	want := `local x = 0
local restObject = {}
local object = {
	x = 1,
	y = 2,
}
local _binding = object
x = _binding.x
local _extracted = {
	["x"] = true,
}
local _rest = {}
for _k, _v in _binding do
	if not _extracted[_k] then
		_rest[_k] = _v
	end
end
restObject = _rest
print(x, restObject)
`

	if got := renderDestructuringFile(t, "src/restobjectassign.ts"); got != want {
		t.Errorf("rendered output differs from fork fixture:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestArrayRestDestructuring(t *testing.T) {
	want := `local _binding = { 1, 2, 3 }
local first = _binding[1]
local restBinding = table.move(_binding, 2, #_binding, 1, {})
print(first, restBinding)
`

	if got := renderDestructuringFile(t, "src/restbinding.ts"); got != want {
		t.Errorf("rendered output differs from fork fixture:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRestDestructuring(t *testing.T) {
	want := `local first = 0
local restAssign = {}
local _binding = { 1, 2, 3 }
first = _binding[1]
restAssign = table.move(_binding, 2, #_binding, 1, {})
print(first, restAssign)
`

	if got := renderDestructuringFile(t, "src/restassign.ts"); got != want {
		t.Errorf("rendered output differs from fork fixture:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
