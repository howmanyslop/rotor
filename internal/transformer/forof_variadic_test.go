package transformer_test

import "testing"

func TestForOfVariadicLuaTupleUsesUnknownArityLoop(t *testing.T) {
	want := `local function collect(iterator)
	local _iterator = iterator
	while true do
		local entry = { _iterator() }
		if #entry == 0 then
			break
		end
		print(entry[1])
	end
end
`
	if got := renderForOfFile(t, "src/tuplevariadic.ts"); got != want {
		t.Errorf("rendered output differs:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
