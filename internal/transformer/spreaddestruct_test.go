package transformer_test

import "testing"

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
