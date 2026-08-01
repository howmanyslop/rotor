package transformer

import (
	"strings"

	"rotor/internal/luau"
	"rotor/internal/rojo"
	"rotor/tsgo/ast"
)

// CompilerVersion is the rbxtsc release rotor targets byte-for-byte. Upstream
// COMPILER_VERSION (Shared/constants.ts:9, the package.json version) is used
// ONLY in the header comment, which transformSourceFile.ts:225 prepends
// inside the transformer — not in compileFiles/renderAST.
const CompilerVersion = "3.0.0"

// HeaderComment is the text of the leading build-comment line (rendered as
// `--<HeaderComment>`). It defaults to the upstream rbxtsc header so the
// differential harness stays a strict byte comparison against real rbxtsc
// output; `rotor build` rebrands it (the v1.0 contract is byte-identical
// output, header-normalized).
var HeaderComment = " Compiled with roblox-ts v" + CompilerVersion

// TransformSourceFile ports nodes/transformSourceFile.ts: transform the
// source file held by state into the final Luau statement list, ready for
// rendering.
func TransformSourceFile(s *State) *luau.List[luau.Statement] {
	node := s.SourceFile

	// The file's module symbol maps to the identifier `exports`.
	symbol := s.Checker.GetSymbolAtLocation(node.AsNode())
	if symbol == nil {
		// Upstream assert: roblox-ts validates non-module files elsewhere.
		panic("transformer: source file has no module symbol")
	}
	s.SetModuleIDBySymbol(symbol, luau.GlobalID("exports"))

	statements := TransformStatementList(s, node.AsNode(), node.Statements.Nodes, nil)

	handleExports(s, node, symbol, statements)

	// moduleScripts must `return nil` if they do not export any values.
	// Upstream consults rojoResolver.getRbxTypeFromFilePath over the
	// translated output path (transformSourceFile.ts:213-220); states built
	// without a Rojo context (transformer unit tests) treat every file as a
	// ModuleScript, which has been true of all Phase 2 fixtures.
	isModuleScript := true
	if s.Rojo != nil {
		outputPath := s.Rojo.PathTranslator.GetOutputPath(node.FileName())
		isModuleScript = s.Rojo.Resolver.GetRbxTypeFromFilePath(outputPath) == rojo.RbxTypeModuleScript
	}
	ensureModuleReturn(statements, isModuleScript)

	prependHeader(s, statements)

	return statements
}

func getExportSyntaxAnchor(exportSymbol *ast.Symbol, sourceFile *ast.SourceFile) *ast.Node {
	for _, declaration := range exportSymbol.Declarations {
		if ast.GetSourceFileOfNode(declaration) != sourceFile {
			continue
		}
		if ast.IsExportSpecifier(declaration) || ast.IsExportAssignment(declaration) {
			return declaration
		}
		if statement := ast.FindAncestor(declaration, ast.IsStatement); statement != nil &&
			ast.HasSyntacticModifier(statement, ast.ModifierFlagsExport) {
			return statement
		}
	}

	for _, declaration := range exportSymbol.Declarations {
		if ast.GetSourceFileOfNode(declaration) == sourceFile {
			if statement := ast.FindAncestor(declaration, ast.IsStatement); statement != nil {
				return statement
			}
			return declaration
		}
	}
	if len(exportSymbol.Declarations) > 0 {
		return exportSymbol.Declarations[0]
	}
	return nil
}

// ensureModuleReturn ports transformSourceFile.ts:213-220: append a plain
// `return nil` only when (a) the output file is a ModuleScript and (b) the
// last non-comment statement isn't already a return.
func ensureModuleReturn(statements *luau.List[luau.Statement], isModuleScript bool) {
	lastStatement := getLastNonCommentStatement(statements.Tail)
	if lastStatement == nil || lastStatement.Value.Kind() != luau.KindReturnStatement {
		if isModuleScript {
			statements.Push(luau.NewReturn(luau.Nil()))
		}
	}
}

// getLastNonCommentStatement ports transformSourceFile.ts:191-196: walk
// backwards past comments.
func getLastNonCommentStatement(listNode *luau.ListNode[luau.Statement]) *luau.ListNode[luau.Statement] {
	for listNode != nil && listNode.Value.Kind() == luau.KindComment {
		listNode = listNode.Prev
	}
	return listNode
}

// prependHeader ports transformSourceFile.ts:222-242: prepend the build
// header — the version comment plus, when the file used the runtime library,
// the `local TS = ...` import (CreateRuntimeLibImport) — hoisting any leading
// Luau directive comments (`--!strict`, sourced from leading `//!...` TS
// comments) above it. The header comment text starts with a space so it
// renders `-- Compiled with roblox-ts v3.0.0` (or the HeaderComment override).
func prependHeader(s *State, statements *luau.List[luau.Statement]) {
	headerStatements := luau.NewList[luau.Statement]()
	headerStatements.Push(luau.NewComment(HeaderComment))

	if s.UsesRuntimeLib {
		headerStatements.Push(s.CreateRuntimeLibImport(s.SourceFile))
	}

	// Only a run of comments already at the very head of the list qualifies;
	// the scan stops at the first non-`!` comment or non-comment.
	directiveComments := luau.NewList[luau.Statement]()
	for statements.Head != nil {
		comment, ok := statements.Head.Value.(*luau.Comment)
		if !ok || !strings.HasPrefix(comment.Text, "!") {
			break
		}
		shifted, _ := statements.Shift()
		directiveComments.Push(shifted)
	}

	statements.UnshiftList(headerStatements)
	statements.UnshiftList(directiveComments)
}
