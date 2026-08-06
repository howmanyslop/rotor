package compile

import (
	"testing"
)

// ----------------------------------------------------------------------------
// Loop labels — rotor extension (no rbxtsc counterpart).
//
// rbxtsc 3.0.0 rejects every labeled statement with `noLabeledStatement`, so
// no label program has an rbxtsc golden and none may enter the byte-parity
// corpora. This end-to-end fixture proves a real project carrying labels
// builds through CompileProject, exercising the three shapes that need
// different lowerings: a labeled `continue` out of a nested loop (flag +
// per-iteration reset), a labeled `break` out of a switch (which lowers to
// `repeat ... until true` and would otherwise swallow the break), and a
// labeled block (which needs a `repeat ... until true` of its own to be broken
// out of). Statement-level expectations live in
// internal/transformer/labels_test.go; runtime behaviour is pinned by the Lune
// behavioral suite (testdata/conformance/rotor/label.spec.ts).
// ----------------------------------------------------------------------------

func TestLabelsModel(t *testing.T) {
	files := compileRuntimeLibProject(t, "labels_model")

	want := "-- Compiled with roblox-ts v3.0.0\n" +
		"local n = 0\n" +
		"local _outer\n" +
		"for i = 0, 2 do\n" +
		"\t_outer = \"none\"\n" +
		"\tfor j = 0, 2 do\n" +
		"\t\tif j == 1 then\n" +
		"\t\t\t_outer = \"continue\"\n" +
		"\t\t\tbreak\n" +
		"\t\tend\n" +
		"\t\tn += 1\n" +
		"\tend\n" +
		"\tif _outer == \"continue\" then\n" +
		"\t\tcontinue\n" +
		"\tend\n" +
		"\tn += 100\n" +
		"end\n" +
		"local _search\n" +
		"for i = 0, 2 do\n" +
		"\trepeat\n" +
		"\t\tif i == 2 then\n" +
		"\t\t\t_search = \"break\"\n" +
		"\t\t\tbreak\n" +
		"\t\tend\n" +
		"\tuntil true\n" +
		"\tif _search == \"break\" then\n" +
		"\t\tbreak\n" +
		"\tend\n" +
		"\tn += i\n" +
		"end\n" +
		"repeat\n" +
		"\tn += 1\n" +
		"\tif n > 0 then\n" +
		"\t\tbreak\n" +
		"\tend\n" +
		"\tn += 100\n" +
		"until true\n" +
		"print(n)\n" +
		"return nil\n"
	if got := files["out/main.luau"]; got != want {
		t.Errorf("out/main.luau:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if len(files) != 1 {
		t.Errorf("produced files = %d, want 1 (%v)", len(files), keys(files))
	}
}
