package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
	"rotor/tsgo/ls/lsutil"
	"rotor/tsgo/scanner"
)

// TransformStatement dispatches one TS statement to its transform, returning
// the resulting Luau statements (upstream nodes/statements/transformStatement.ts).
// dispatch.go assigns the real dispatch at init; it stays a func var so tests
// can stub it (see withStubDispatch in internal/compile).
var TransformStatement func(s *State, node *ast.Node) *luau.List[luau.Statement]

// ExportInfo carries namespace-export assignment info into a statement list
// (upstream transformStatementList exportInfo parameter): after a statement
// that declares exported names, `containerId.<name> = <name>` assignments are
// appended per the mapping.
type ExportInfo struct {
	ID      luau.AnyIdentifier
	Mapping map[*ast.Node][]string
}

// TransformStatementList ports nodes/transformStatementList.ts: the generic
// statement-list driver used by TransformSourceFile and every block.
//
// Ordering invariant per statement: leading comments -> hoisted `local a, b`
// -> prereq statements -> transformed statement(s) -> namespace export
// assignments. Iteration stops after a final statement (break/continue/
// return) — trailing dead code is elided.
func TransformStatementList(s *State, parent *ast.Node, statements []*ast.Node, exportInfo *ExportInfo) *luau.List[luau.Statement] {
	// ROTOR EXTENSION (loop labels): a function body is a barrier for label
	// lowering — a Luau `break` cannot cross it, and a flag check emitted
	// inside a closure would target the wrong loop. This is the one choke
	// point every function-like body passes through: function declarations,
	// function/arrow expressions, methods and class constructors all reach
	// their body block here, and a block's parent is function-like ONLY when
	// that block IS the body.
	if parent != nil && ast.IsBlock(parent) && ast.IsFunctionLike(parent.Parent) {
		var result *luau.List[luau.Statement]
		s.WithoutLabels(func() {
			result = transformStatementListWorker(s, parent, statements, exportInfo)
		})
		return result
	}
	return transformStatementListWorker(s, parent, statements, exportInfo)
}

func transformStatementListWorker(s *State, parent *ast.Node, statements []*ast.Node, exportInfo *ExportInfo) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()

	for _, statement := range statements {
		// Capture prerequisite statements for the ts.Statement while
		// transforming it. transformStatement runs BEFORE
		// createHoistDeclaration is consulted, so hoists registered while
		// transforming statement N (a use-before-declare inside N) are
		// picked up for N itself.
		var transformed *luau.List[luau.Statement]
		prereqs := s.CaptureStatements(func() {
			if TransformStatement == nil {
				panic("transformer: statement dispatch not wired")
			}
			transformed = TransformStatement(s, statement)
		})

		if !s.RemoveComments() {
			result.PushList(s.GetLeadingComments(statement))
		}

		if hoist := createHoistDeclaration(s, statement); hoist != nil {
			result.Push(hoist)
		}

		s.setStatementListSourcePositions(prereqs, statement)
		s.setStatementListSourcePositions(transformed, statement)
		result.PushList(prereqs)
		lastNode := transformed.Tail
		result.PushList(transformed)

		if lastNode != nil && luau.IsFinalStatement(lastNode.Value) {
			break
		}

		if exportInfo != nil {
			for _, exportName := range exportInfo.Mapping[statement] {
				assignment := luau.NewAssignment(
					luau.NewPropertyAccess(exportInfo.ID, exportName),
					"=",
					luau.ID(exportName),
				)
				s.setSourcePosition(assignment, s.getOriginalSourcePosition(statement, false))
				result.Push(assignment)
			}
		}
	}

	if !s.RemoveComments() {
		if lastToken := getLastToken(s.SourceFile, parent, statements); lastToken != nil {
			result.PushList(s.GetLeadingComments(lastToken))
		}
	}

	return result
}

// getLastToken ports transformStatementList.ts getLastToken: the trailing
// `}`/EOF token whose leading trivia holds the block's trailing comments.
// tsgo does not materialize `}` nodes in the visited tree, so use the token
// scanner-backed lsutil.GetLastToken helper and keep upstream's
// !isNodeDescendantOf(lastToken, lastStatement) guard.
func getLastToken(sourceFile *ast.SourceFile, parent *ast.Node, statements []*ast.Node) *ast.Node {
	if sourceFile == nil {
		return nil
	}
	if len(statements) > 0 {
		lastStatement := statements[len(statements)-1]
		if p := lastStatement.Parent; p != nil {
			lastToken := lsutil.GetLastToken(p, sourceFile)
			if lastToken != nil && !isNodeDescendantOf(lastToken, lastStatement) {
				return lastToken
			}
		}
		return nil
	}
	if parent != nil {
		return lsutil.GetLastToken(parent, sourceFile)
	}
	return nil
}

func isNodeDescendantOf(node, ancestor *ast.Node) bool {
	if node == nil || ancestor == nil {
		return false
	}
	for current := node; current != nil; current = current.Parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

// RemoveComments reports compilerOptions.removeComments === true, the gate
// upstream transformStatementList checks before emitting leading comments.
// Checker-free test states (nil Program) emit comments, matching the
// upstream default of removeComments being unset.
func (s *State) RemoveComments() bool {
	return s.Program != nil && s.Program.Options().RemoveComments.IsTrue()
}

// GetLeadingComments ports TransformState.getLeadingComments (upstream lines
// 139-153): scan the leading trivia of node for comment ranges and convert
// each to a luau comment. `// foo` -> text " foo" (keeps the space, drops
// "//"); `/* foo */` -> " foo " (drops both delimiters; multi-line text
// renders as a --[[ ... ]] block).
func (s *State) GetLeadingComments(node *ast.Node) *luau.List[luau.Statement] {
	result := luau.NewList[luau.Statement]()
	var factory ast.NodeFactory
	for commentRange := range scanner.GetLeadingCommentRanges(&factory, s.SourceFileText, node.Pos()) {
		end := commentRange.End()
		if commentRange.Kind != ast.KindSingleLineCommentTrivia {
			end -= 2 // strip trailing "*/"
		}
		result.Push(luau.NewComment(s.SourceFileText[commentRange.Pos()+2 : end]))
	}
	return result
}

// createHoistDeclaration ports util/createHoistDeclaration.ts: when prior
// transforms recorded use-before-declaration identifiers against statement,
// emit a `local a, b` declaration (no initializer) immediately before it.
func createHoistDeclaration(s *State, statement *ast.Node) luau.Statement {
	hoists := s.HoistsByStatement[statement]
	if len(hoists) == 0 {
		return nil
	}
	left := luau.NewList[luau.AnyIdentifier]()
	for _, hoist := range hoists {
		ValidateIdentifier(s, hoist)
		left.Push(TransformIdentifierDefined(s, hoist))
	}
	return luau.NewVariableDeclaration(left, nil)
}

// ValidateIdentifier ports util/validateIdentifier.ts: reserved-word /
// invalid-Luau-identifier diagnostics.
func ValidateIdentifier(s *State, node *ast.Node) {
	text := node.Text()
	if !luau.IsValidIdentifier(text) {
		s.Diags.Add(DiagNoInvalidIdentifier(node))
	} else if luau.IsReservedIdentifier(text) {
		s.Diags.Add(DiagNoReservedIdentifier(node))
	}
}
