package transformer_test

import (
	"path/filepath"
	"testing"

	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

func TestSharedTable(t *testing.T) {
	s := buildState(t, filepath.Join("testdata", "forof"), "src/sharedtable.ts")
	if s.AmbientSymbol("SharedTable") == nil {
		t.Fatal("SharedTable symbol was not registered")
	}
	ids := collectIdentifiers(s.SourceFile.AsNode(), "t")
	if len(ids) < 1 {
		t.Fatal("no identifiers named 't' found")
	}
	if !transformer.IsSharedTableType(s, s.GetType(ids[0])) {
		t.Fatal("SharedTable type was not detected by its checker symbol")
	}
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	if diagnostics := s.Diags.Flush(); len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	want := `local function iterateTable(t)
	local sum = 0
	for k, v in t do
		sum += v
	end
	return sum
end
`
	if got := render.RenderAST(statements); got != want {
		t.Errorf("rendered output differs:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
