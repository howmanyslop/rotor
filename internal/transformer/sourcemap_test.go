package transformer

import (
	"encoding/json"
	"testing"

	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/core"
	"rotor/tsgo/parser"
	"rotor/tsgo/sourcemap"
	"rotor/tsgo/tspath"
)

func TestSourcePositionAnchorsPrereqsAndStatements(t *testing.T) {
	sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{
		FileName: "/test.ts",
		Path:     tspath.Path("/test.ts"),
	}, "first();\nsecond();\n", core.ScriptKindTS)
	state := NewState(nil, nil, sourceFile, NewDiagService(), NewMultiState())

	previousDispatch := TransformStatement
	TransformStatement = func(state *State, _ *ast.Node) *luau.List[luau.Statement] {
		state.Prereq(luau.NewVariableDeclaration(luau.ID("value"), luau.Num(1)))
		return luau.NewList[luau.Statement](luau.NewCallStatement(luau.NewCall(luau.ID("call"), luau.NewList[luau.Expression]())))
	}
	t.Cleanup(func() { TransformStatement = previousDispatch })

	statements := TransformStatementList(state, sourceFile.AsNode(), sourceFile.Statements.Nodes, nil).ToSlice()
	if len(statements) != 4 {
		t.Fatalf("statement count = %d, want 4", len(statements))
	}

	tests := []struct {
		name      string
		statement luau.Statement
		start     SourcePosition
		end       SourcePosition
	}{
		{name: "first prereq", statement: statements[0], start: SourcePosition{Line: 0, Column: 0}, end: SourcePosition{Line: 0, Column: 7}},
		{name: "first transformed", statement: statements[1], start: SourcePosition{Line: 0, Column: 0}, end: SourcePosition{Line: 0, Column: 7}},
		{name: "second prereq", statement: statements[2], start: SourcePosition{Line: 1, Column: 0}, end: SourcePosition{Line: 1, Column: 8}},
		{name: "second transformed", statement: statements[3], start: SourcePosition{Line: 1, Column: 0}, end: SourcePosition{Line: 1, Column: 8}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := state.sourcePositionMap[sourcePositionKey(test.statement)]; got == nil || *got != test.start {
				t.Errorf("start = %#v, want %#v", got, test.start)
			}
			if got := state.sourceEndPositionMap[sourcePositionKey(test.statement)]; got == nil || *got != test.end {
				t.Errorf("end = %#v, want %#v", got, test.end)
			}
		})
	}
}

func TestSourcePositionUsesUTF16Columns(t *testing.T) {
	sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{
		FileName: "/test.ts",
		Path:     tspath.Path("/test.ts"),
	}, "const value = \"😀\"; value;", core.ScriptKindTS)
	state := NewState(nil, nil, sourceFile, NewDiagService(), NewMultiState())
	secondStatement := sourceFile.Statements.Nodes[1]

	if got := state.getOriginalSourcePosition(secondStatement, false); got == nil || *got != (SourcePosition{Line: 0, Column: 20}) {
		t.Errorf("position = %#v, want %#v", got, SourcePosition{Line: 0, Column: 20})
	}
}

func TestSourcePositionExportSyntaxAnchors(t *testing.T) {
	sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{
		FileName: "/test.ts",
		Path:     tspath.Path("/test.ts"),
	}, "export const value = 1;\nexport { value as alias };\nexport default value;\n", core.ScriptKindTS)
	variableStatement := sourceFile.Statements.Nodes[0]
	exportDeclaration := sourceFile.Statements.Nodes[1]
	exportAssignment := sourceFile.Statements.Nodes[2]
	exportSpecifier := exportDeclaration.AsExportDeclaration().ExportClause.AsNamedExports().Elements.Nodes[0]
	variableDeclaration := variableStatement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]

	tests := []struct {
		name   string
		symbol *ast.Symbol
		want   *ast.Node
	}{
		{name: "export specifier", symbol: &ast.Symbol{Declarations: []*ast.Node{exportSpecifier}}, want: exportSpecifier},
		{name: "export assignment", symbol: &ast.Symbol{Declarations: []*ast.Node{exportAssignment}}, want: exportAssignment},
		{name: "export declaration", symbol: &ast.Symbol{Declarations: []*ast.Node{variableDeclaration}}, want: variableStatement},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := getExportSyntaxAnchor(test.symbol, sourceFile); got != test.want {
				t.Errorf("anchor = %p, want %p", got, test.want)
			}
		})
	}
}

func TestRenderSourceMap(t *testing.T) {
	sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{
		FileName: "/test.ts",
		Path:     tspath.Path("/test.ts"),
	}, "const a = 1;\nconst b = 2;\n", core.ScriptKindTS)
	state := NewState(nil, nil, sourceFile, NewDiagService(), NewMultiState())
	first := luau.NewVariableDeclaration(luau.ID("a"), luau.Num(1))
	second := luau.NewVariableDeclaration(luau.ID("b"), luau.Num(2))
	statements := luau.NewList[luau.Statement](first, second)
	state.sourcePositionMap[sourcePositionKey(first)] = &SourcePosition{Line: 0, Column: 0}
	state.sourceEndPositionMap[sourcePositionKey(first)] = &SourcePosition{Line: 0, Column: 11}
	state.sourcePositionMap[sourcePositionKey(second)] = &SourcePosition{Line: 1, Column: 0}
	state.sourceEndPositionMap[sourcePositionKey(second)] = &SourcePosition{Line: 1, Column: 11}

	var sourceMap struct {
		Version        int      `json:"version"`
		File           string   `json:"file"`
		Sources        []string `json:"sources"`
		SourcesContent []string `json:"sourcesContent"`
		Mappings       string   `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(state.RenderSourceMap(statements, sourceFile)), &sourceMap); err != nil {
		t.Fatal(err)
	}

	if sourceMap.Version != 3 {
		t.Errorf("version = %d, want 3", sourceMap.Version)
	}
	if sourceMap.File != "/test.luau" {
		t.Errorf("file = %q, want /test.luau", sourceMap.File)
	}
	if len(sourceMap.Sources) != 1 || sourceMap.Sources[0] != "/test.ts" {
		t.Errorf("sources = %v, want [/test.ts]", sourceMap.Sources)
	}
	if len(sourceMap.SourcesContent) != 1 || sourceMap.SourcesContent[0] != sourceFile.Text() {
		t.Errorf("sourcesContent = %v, want source text", sourceMap.SourcesContent)
	}
	if sourceMap.Mappings != "AAAA;AACA" {
		t.Errorf("mappings = %q, want %q", sourceMap.Mappings, "AAAA;AACA")
	}
	decoder := sourcemap.DecodeMappings(sourceMap.Mappings)
	for range decoder.Values() {
	}
	if err := decoder.Error(); err != nil {
		t.Errorf("mappings must be valid VLQ: %v", err)
	}
}

func TestRenderSourceMapUsesEndAnchorForBlockEnd(t *testing.T) {
	sourceFile := parser.ParseSourceFile(ast.SourceFileParseOptions{
		FileName: "/test.ts",
		Path:     tspath.Path("/test.ts"),
	}, "if (true) {\n\tconst value = 1;\n}\n", core.ScriptKindTS)
	state := NewState(nil, nil, sourceFile, NewDiagService(), NewMultiState())
	inner := luau.NewVariableDeclaration(luau.ID("value"), luau.Num(1))
	condition := luau.NewIf(luau.Bool(true), luau.NewList[luau.Statement](inner), nil)
	statements := luau.NewList[luau.Statement](condition)
	state.sourcePositionMap[sourcePositionKey(condition)] = &SourcePosition{Line: 0, Column: 0}
	state.sourcePositionMap[sourcePositionKey(inner)] = &SourcePosition{Line: 1, Column: 1}
	state.sourceEndPositionMap[sourcePositionKey(condition)] = &SourcePosition{Line: 2, Column: 0}

	var sourceMap struct {
		Mappings string `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(state.RenderSourceMap(statements, sourceFile)), &sourceMap); err != nil {
		t.Fatal(err)
	}
	if sourceMap.Mappings != "AAAA;AACC;AACD" {
		t.Errorf("mappings = %q, want %q", sourceMap.Mappings, "AAAA;AACC;AACD")
	}
}
