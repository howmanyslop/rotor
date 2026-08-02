package transformer

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"rotor/internal/luau"
	"rotor/internal/luau/render"
	"rotor/tsgo/ast"
)

const base64VLQAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

type sourceMapV3 struct {
	Version        int      `json:"version"`
	File           string   `json:"file"`
	Sources        []string `json:"sources"`
	SourcesContent []string `json:"sourcesContent"`
	Mappings       string   `json:"mappings"`
}

type sourceMapMapping struct {
	generatedLine   int
	generatedColumn int
	sourceLine      int
	sourceColumn    int
}

// RenderSourceMap returns the deterministic V3 source map for statements.
func (s *State) RenderSourceMap(statements *luau.List[luau.Statement], sourceFile *ast.SourceFile) string {
	mappings := []sourceMapMapping{}
	s.addStatementListMappings(&mappings, statements, 0)
	sort.SliceStable(mappings, func(left, right int) bool {
		if mappings[left].generatedLine != mappings[right].generatedLine {
			return mappings[left].generatedLine < mappings[right].generatedLine
		}
		return mappings[left].generatedColumn < mappings[right].generatedColumn
	})

	fileName := sourceFile.FileName()
	outputFileName := strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".luau"
	encoded, err := json.Marshal(sourceMapV3{
		Version:        3,
		File:           outputFileName,
		Sources:        []string{fileName},
		SourcesContent: []string{sourceFile.Text()},
		Mappings:       encodeSourceMapMappings(mappings),
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (s *State) addStatementListMappings(mappings *[]sourceMapMapping, statements *luau.List[luau.Statement], startLine int) {
	line := startLine
	for listNode := statements.Head; listNode != nil; listNode = listNode.Next {
		statement := listNode.Value
		s.addStartMapping(mappings, statement, line)
		rendered := renderSourceMapStatement(statement)
		s.addInnerMappings(mappings, statement, line)
		line += strings.Count(rendered, "\n")
	}
}

func (s *State) addStartMapping(mappings *[]sourceMapMapping, statement luau.Statement, line int) {
	if position := s.sourcePositionMap[sourcePositionKey(statement)]; position != nil {
		*mappings = append(*mappings, sourceMapMapping{
			generatedLine: line,
			sourceLine:    position.Line,
			sourceColumn:  position.Column,
		})
	}
}

func (s *State) addEndMapping(mappings *[]sourceMapMapping, statement luau.Statement, line int) {
	position := s.sourceEndPositionMap[sourcePositionKey(statement)]
	if position == nil {
		position = s.sourcePositionMap[sourcePositionKey(statement)]
	}
	if position != nil {
		*mappings = append(*mappings, sourceMapMapping{
			generatedLine: line,
			sourceLine:    position.Line,
			sourceColumn:  position.Column,
		})
	}
}

func (s *State) addInnerMappings(mappings *[]sourceMapMapping, statement luau.Statement, startLine int) {
	switch node := statement.(type) {
	case *luau.DoStatement:
		s.addBlockMappings(mappings, statement, node.Statements, startLine)
	case *luau.WhileStatement:
		s.addBlockMappings(mappings, statement, node.Statements, startLine)
	case *luau.RepeatStatement:
		s.addStatementListMappings(mappings, node.Statements, startLine+1)
	case *luau.IfStatement:
		s.addIfMappings(mappings, node, startLine, false)
	case *luau.NumericForStatement:
		s.addBlockMappings(mappings, statement, node.Statements, startLine)
	case *luau.ForStatement:
		s.addBlockMappings(mappings, statement, node.Statements, startLine)
	case *luau.FunctionDeclaration:
		s.addBlockMappings(mappings, statement, node.Statements, startLine)
	case *luau.MethodDeclaration:
		s.addBlockMappings(mappings, statement, node.Statements, startLine)
	case *luau.CallStatement:
		s.addCallMappings(mappings, node, startLine)
	}
}

func (s *State) addBlockMappings(mappings *[]sourceMapMapping, statement luau.Statement, statements *luau.List[luau.Statement], startLine int) {
	s.addStatementListMappings(mappings, statements, startLine+1)
	lineCount := strings.Count(renderSourceMapStatement(statement), "\n")
	if lineCount > 0 {
		s.addEndMapping(mappings, statement, startLine+lineCount-1)
	}
}

func (s *State) addIfMappings(mappings *[]sourceMapMapping, statement *luau.IfStatement, startLine int, isElseIf bool) {
	s.addStatementListMappings(mappings, statement.Statements, startLine+1)
	bodyLines := statementListLineCount(statement.Statements)
	switch elseBody := statement.ElseBody.(type) {
	case *luau.List[luau.Statement]:
		if elseBody.IsNonEmpty() {
			s.addStatementListMappings(mappings, elseBody, startLine+bodyLines+2)
		}
	case *luau.IfStatement:
		elseIfLine := startLine + bodyLines + 1
		s.addStartMapping(mappings, elseBody, elseIfLine)
		s.addIfMappings(mappings, elseBody, elseIfLine, true)
	}
	if !isElseIf {
		lineCount := strings.Count(renderSourceMapStatement(statement), "\n")
		if lineCount > 0 {
			s.addEndMapping(mappings, statement, startLine+lineCount-1)
		}
	}
}

func (s *State) addCallMappings(mappings *[]sourceMapMapping, statement *luau.CallStatement, startLine int) {
	call, ok := statement.Expression.(*luau.CallExpression)
	if !ok {
		return
	}
	fullRendered := renderSourceMapStatement(statement)
	for argument := call.Args.Head; argument != nil; argument = argument.Next {
		function, ok := argument.Value.(*luau.FunctionExpression)
		if !ok {
			continue
		}
		functionRendered := render.Render(render.NewRenderState(), function)
		beforeFunction, _, found := strings.Cut(fullRendered, functionRendered)
		if !found {
			continue
		}
		linesBeforeFunction := strings.Count(beforeFunction, "\n")
		s.addStatementListMappings(mappings, function.Statements, startLine+linesBeforeFunction+1)
		functionLines := strings.Count(functionRendered, "\n")
		if functionLines > 0 {
			s.addEndMapping(mappings, statement, startLine+linesBeforeFunction+functionLines)
		}
	}
}

func renderSourceMapStatement(statement luau.Statement) string {
	return render.RenderAST(luau.NewList(statement))
}

func statementListLineCount(statements *luau.List[luau.Statement]) int {
	lineCount := 0
	for listNode := statements.Head; listNode != nil; listNode = listNode.Next {
		lineCount += strings.Count(renderSourceMapStatement(listNode.Value), "\n")
	}
	return lineCount
}

func encodeSourceMapMappings(mappings []sourceMapMapping) string {
	var builder strings.Builder
	lastGeneratedLine := 0
	lastGeneratedColumn := 0
	lastSourceLine := 0
	lastSourceColumn := 0
	firstMappingOnLine := true

	for _, mapping := range mappings {
		for lastGeneratedLine < mapping.generatedLine {
			builder.WriteByte(';')
			lastGeneratedLine++
			lastGeneratedColumn = 0
			firstMappingOnLine = true
		}
		if !firstMappingOnLine {
			builder.WriteByte(',')
		}
		appendBase64VLQ(&builder, mapping.generatedColumn-lastGeneratedColumn)
		appendBase64VLQ(&builder, 0)
		appendBase64VLQ(&builder, mapping.sourceLine-lastSourceLine)
		appendBase64VLQ(&builder, mapping.sourceColumn-lastSourceColumn)
		lastGeneratedColumn = mapping.generatedColumn
		lastSourceLine = mapping.sourceLine
		lastSourceColumn = mapping.sourceColumn
		firstMappingOnLine = false
	}
	return builder.String()
}

func appendBase64VLQ(builder *strings.Builder, value int) {
	if value < 0 {
		value = (-value << 1) | 1
	} else {
		value <<= 1
	}
	for {
		digit := value & 31
		value >>= 5
		if value > 0 {
			digit |= 32
		}
		builder.WriteByte(base64VLQAlphabet[digit])
		if value == 0 {
			return
		}
	}
}
