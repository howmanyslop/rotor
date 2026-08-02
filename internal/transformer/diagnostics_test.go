package transformer

import (
	"testing"

	"rotor/tsgo/ast"
)

// Expected message strings are copied byte-exact from
// reference/roblox-ts/src/Shared/diagnostics.ts (multi-part messages are
// joined with "\n"; suggestion(text) prefixes "Suggestion: "; issue(id)
// renders "More information: https://github.com/roblox-ts/roblox-ts/issues/<id>").
func TestDiagnosticMessages(t *testing.T) {
	node := new(ast.Node)
	cases := []struct {
		d       Diagnostic
		code    string
		msg     string
		warning bool
	}{
		{
			d:    DiagNoAny(node),
			code: "noAny",
			msg:  "Using values of type `any` is not supported!\nSuggestion: Use `unknown` instead.",
		},
		{
			d:    DiagNoNullLiteral(node),
			code: "noNullLiteral",
			msg:  "`null` is not supported!\nSuggestion: Use `undefined` instead.",
		},
		{
			d:    DiagNoForInStatement(node),
			code: "noForInStatement",
			msg:  "for-in loop statements are not supported!",
		},
		{
			d:    DiagNoVar(node),
			code: "noVar",
			msg:  "`var` keyword is not supported!\nSuggestion: Use `let` or `const` instead.",
		},
		{
			d:    DiagNoGetterSetter(node),
			code: "noGetterSetter",
			msg:  "Getters and Setters are not supported!\nMore information: https://github.com/roblox-ts/roblox-ts/issues/457",
		},
		{
			d:    DiagNoInvalidIdentifier(node),
			code: "noInvalidIdentifier",
			msg: "Invalid Luau identifier!\n" +
				"Luau identifiers must start with a letter and only contain letters, numbers, and underscores.\n" +
				"Reserved Luau keywords cannot be used as identifiers.",
		},
		{
			// Upstream's suggestion text genuinely lacks the closing backtick
			// and trailing period; preserved faithfully.
			d:    DiagNoRobloxSymbolInstanceof(node),
			code: "noRobloxSymbolInstanceof",
			msg:  "The `instanceof` operator can only be used on roblox-ts classes!\nSuggestion: Use `typeIs(myThing, \"TypeToCheck\") instead",
		},
		{
			d:    DiagNoEqualsEquals(node),
			code: "noEqualsEquals",
			msg:  "operator `==` is not supported!\nSuggestion: Use `===` instead.",
		},
		{
			d:    DiagNoVarArgsMacroSpread(node),
			code: "noVarArgsMacroSpread",
			msg:  "Macros which use variadic arguments do not support spread expressions!\nMore information: https://github.com/roblox-ts/roblox-ts/issues/1149",
		},
		{
			d:       DiagTruthyChange(node, "0, 0/0, \"\""),
			code:    "truthyChange",
			msg:     "Value will be checked against 0, 0/0, \"\"",
			warning: true,
		},
		{
			d:       DiagRuntimeLibUsedInReplicatedFirst(node),
			code:    "runtimeLibUsedInReplicatedFirst",
			msg:     "This statement would generate a call to the runtime library. The runtime library should not be used from ReplicatedFirst.",
			warning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if tc.d.Code != tc.code {
				t.Errorf("Code = %q, want %q", tc.d.Code, tc.code)
			}
			if tc.d.Message != tc.msg {
				t.Errorf("Message mismatch\ngot:  %q\nwant: %q", tc.d.Message, tc.msg)
			}
			if tc.d.Warning != tc.warning {
				t.Errorf("Warning = %v, want %v", tc.d.Warning, tc.warning)
			}
			if tc.d.Node != node {
				t.Errorf("Node not propagated")
			}
		})
	}
}

func TestDiagnosticContextMessages(t *testing.T) {
	// noRojoData: the suggestion line is included only when isPackage is true
	// (upstream: `isPackage && suggestion(...)`, false entries filtered out).
	d := DiagNoRojoData(nil, "src/shared/module.ts", false)
	want := "Could not find Rojo data. There is no $path in your Rojo config that covers src/shared/module.ts"
	if d.Message != want {
		t.Errorf("noRojoData (not package)\ngot:  %q\nwant: %q", d.Message, want)
	}
	d = DiagNoRojoData(nil, "src/shared/module.ts", true)
	want += "\nSuggestion: Did you forget to add a custom npm scope to your default.project.json?"
	if d.Message != want {
		t.Errorf("noRojoData (package)\ngot:  %q\nwant: %q", d.Message, want)
	}

	d = DiagNoPackageImportWithoutScope(nil, "node_modules/foo", []string{"ReplicatedStorage", "node_modules", "foo"})
	want = "Imported package Roblox path is missing an npm scope!\n" +
		"Package path: node_modules/foo\n" +
		"Roblox path: ReplicatedStorage.node_modules.foo\n" +
		"Suggestion: You might need to update your \"node_modules\" in default.project.json to match:\n" +
		"\"node_modules\": {\n\t\"$className\": \"Folder\",\n\t\"@rbxts\": {\n\t\t\"$path\": \"node_modules/@rbxts\"\n\t}\n}"
	if d.Message != want {
		t.Errorf("noPackageImportWithoutScope\ngot:  %q\nwant: %q", d.Message, want)
	}
}

func TestSpreadDiagnostics(t *testing.T) {
	node := new(ast.Node)
	tests := []struct {
		name string
		d    Diagnostic
		code string
		msg  string
	}{
		{
			name: "nested spreads in assignment patterns",
			d:    DiagNoNestedSpreadsInAssignmentPatterns(node),
			code: "noNestedSpreadsInAssignmentPatterns",
			msg:  "Nesting spreads in assignment patterns is not supported!",
		},
		{
			name: "rest spreading of roblox types",
			d:    DiagNoRestSpreadingOfRobloxTypes(node),
			code: "noRestSpreadingOfRobloxTypes",
			msg:  "Operator `...` is not allowed on Roblox types!",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.d.Code != tc.code {
				t.Fatalf("Code = %q, want %q", tc.d.Code, tc.code)
			}
			if tc.d.Message != tc.msg {
				t.Fatalf("Message = %q, want %q", tc.d.Message, tc.msg)
			}
			if tc.d.Node != node {
				t.Fatal("Node not propagated")
			}
		})
	}
}

func TestDiagServiceAddSingleDedupe(t *testing.T) {
	s := NewDiagService()
	s.Add(DiagNoVar(nil))
	s.Add(DiagNoVar(nil)) // Add never dedupes
	s.AddSingle(DiagNoAny(nil))
	s.AddSingle(DiagNoAny(nil)) // second occurrence ignored
	s.AddSingle(DiagNoVar(nil)) // different code via AddSingle still added

	ds := s.Flush()
	if len(ds) != 4 {
		t.Fatalf("len(Flush()) = %d, want 4", len(ds))
	}
	wantCodes := []string{"noVar", "noVar", "noAny", "noVar"}
	for i, d := range ds {
		if d.Code != wantCodes[i] {
			t.Errorf("ds[%d].Code = %q, want %q", i, d.Code, wantCodes[i])
		}
	}
}

func TestDiagServiceFlushClears(t *testing.T) {
	s := NewDiagService()
	s.AddSingle(DiagNoAny(nil))
	if got := len(s.Flush()); got != 1 {
		t.Fatalf("first Flush len = %d, want 1", got)
	}
	if got := len(s.Flush()); got != 0 {
		t.Fatalf("second Flush len = %d, want 0", got)
	}
	// Flush also resets the AddSingle dedupe set (mirrors upstream
	// DiagnosticService.flush clearing singleDiagnostics).
	s.AddSingle(DiagNoAny(nil))
	if got := len(s.Flush()); got != 1 {
		t.Fatalf("Flush after re-AddSingle len = %d, want 1", got)
	}
}

func TestDiagServiceHasErrors(t *testing.T) {
	s := NewDiagService()
	if s.HasErrors() {
		t.Fatal("empty service must not have errors")
	}
	s.Add(DiagTruthyChange(nil, "0"))
	if s.HasErrors() {
		t.Fatal("warnings alone must not count as errors")
	}
	s.Add(DiagNoAny(nil))
	if !s.HasErrors() {
		t.Fatal("error diagnostic must be detected")
	}
	s.Flush()
	if s.HasErrors() {
		t.Fatal("HasErrors must be false after Flush")
	}
}

func TestAddDiagnosticWithCache(t *testing.T) {
	s := NewDiagService()
	cache := map[string]bool{}
	AddDiagnosticWithCache(s, "keyA", DiagNoAny(nil), cache)
	AddDiagnosticWithCache(s, "keyA", DiagNoAny(nil), cache) // deduped by caller cache
	AddDiagnosticWithCache(s, "keyB", DiagNoAny(nil), cache) // distinct key passes
	if got := len(s.Flush()); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
}
