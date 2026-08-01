package transformer

import (
	"testing"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/core"
	"rotor/tsgo/parser"
	"rotor/tsgo/tspath"
)

func parseBindingPatternNode(t *testing.T, source string) *ast.Node {
	t.Helper()
	sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{
		FileName: "/test.ts",
		Path:     tspath.Path("/test.ts"),
	}, source, core.ScriptKindTS)
	statement := sourceFile.Statements.Nodes[0]
	if !ast.IsVariableStatement(statement) {
		t.Fatalf("first statement is %s, want VariableStatement", statement.Kind)
	}
	declaration := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]
	return declaration.Name()
}

func TestArrayLikeExpressionContainsSpread(t *testing.T) {
	tests := []struct {
		name string
		node *ast.Node
		want bool
	}{
		{
			name: "array binding pattern with rest element",
			node: parseBindingPatternNode(t, "const [a, ...rest] = value;"),
			want: true,
		},
		{
			name: "nested array binding pattern with rest element",
			node: parseBindingPatternNode(t, "const [a, [b, ...rest]] = value;"),
			want: true,
		},
		{
			name: "array binding pattern without rest element",
			node: parseBindingPatternNode(t, "const [a, [b]] = value;"),
			want: false,
		},
		{
			name: "array literal with spread element",
			node: func() *ast.Node {
				_, expression := parseFirstExpression(t, "[1, ...rest];")
				return expression
			}(),
			want: true,
		},
		{
			name: "nested array literal with spread element",
			node: func() *ast.Node {
				_, expression := parseFirstExpression(t, "[[1, ...rest]];")
				return expression
			}(),
			want: true,
		},
		{
			name: "array literal without spread element",
			node: func() *ast.Node {
				_, expression := parseFirstExpression(t, "[1, [2]];")
				return expression
			}(),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := arrayLikeExpressionContainsSpread(tc.node); got != tc.want {
				t.Fatalf("arrayLikeExpressionContainsSpread() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTargetIDForBindingPattern(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		value      luau.Expression
		wantReuse  bool
		wantPrerqs int
	}{
		{
			name:      "array binding pattern without initializers reuses identifier",
			source:    "const [a, b] = value;",
			value:     luau.ID("value"),
			wantReuse: true,
		},
		{
			name:      "object binding pattern without initializers reuses identifier",
			source:    "const { a, b } = value;",
			value:     luau.ID("value"),
			wantReuse: true,
		},
		{
			name:       "array binding pattern with initializer allocates temp",
			source:     "const [a = 1] = value;",
			value:      luau.ID("value"),
			wantReuse:  false,
			wantPrerqs: 1,
		},
		{
			name:       "object binding pattern with initializer allocates temp",
			source:     "const { a = 1 } = value;",
			value:      luau.ID("value"),
			wantReuse:  false,
			wantPrerqs: 1,
		},
		{
			name:       "complex value allocates temp even without initializers",
			source:     "const [a, b] = value;",
			value:      luau.NewBinary(luau.ID("left"), "+", luau.ID("right")),
			wantReuse:  false,
			wantPrerqs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewTestState()
			pattern := parseBindingPatternNode(t, tc.source)
			var got luau.AnyIdentifier
			prereqs := s.CaptureStatements(func() {
				got = getTargetIDForBindingPattern(s, pattern, tc.value)
			})
			if prereqs.Size() != tc.wantPrerqs {
				t.Fatalf("prereqs = %d, want %d", prereqs.Size(), tc.wantPrerqs)
			}
			if tc.wantReuse {
				if got != tc.value {
					t.Fatalf("got %v, want reuse of %v", got, tc.value)
				}
				return
			}
			if got == tc.value {
				t.Fatalf("got %v, want a temp identifier", got)
			}
			if _, ok := got.(*luau.TemporaryIdentifier); !ok {
				t.Fatalf("got %T, want *luau.TemporaryIdentifier", got)
			}
		})
	}
}
