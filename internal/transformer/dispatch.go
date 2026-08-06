package transformer

import (
	"rotor/internal/luau"
	"rotor/tsgo/ast"
)

func init() {
	// Wire the statement dispatcher into the func var TransformStatementList
	// consults (declared in statementlist.go to break the cycle).
	TransformStatement = transformStatementDispatch
}

// TransformExpression ports nodes/expressions/transformExpression.ts: table
// dispatch on node.kind. Banned kinds add their upstream diagnostic and
// return luau.NewNone() (NO_EMIT) — diagnostics never abort the walk.
// Expression kinds upstream supports but rotor has not ported yet raise
// DiagRotorNotYetSupported instead of upstream's `assert(false)`.
func TransformExpression(s *State, node *ast.Node) luau.Expression {
	switch node.Kind {
	// banned expressions
	case ast.KindBigIntLiteral:
		s.Diags.Add(DiagNoBigInt(node))
		return luau.NewNone()
	case ast.KindNullKeyword:
		s.Diags.Add(DiagNoNullLiteral(node))
		return luau.NewNone()
	case ast.KindPrivateIdentifier:
		s.Diags.Add(DiagNoPrivateIdentifier(node))
		return luau.NewNone()
	case ast.KindRegularExpressionLiteral:
		s.Diags.Add(DiagNoRegex(node))
		return luau.NewNone()
	case ast.KindTypeOfExpression:
		s.Diags.Add(DiagNoTypeOfExpression(node))
		return luau.NewNone()

	// skip transforms
	case ast.KindImportKeyword:
		return luau.NewNone()

	// regular transforms
	case ast.KindArrayLiteralExpression:
		return transformArrayLiteralExpression(s, node)
	case ast.KindArrowFunction, ast.KindFunctionExpression:
		return transformFunctionExpression(s, node)
	case ast.KindAwaitExpression:
		return transformAwaitExpression(s, node)
	case ast.KindYieldExpression:
		return transformYieldExpression(s, node)
	case ast.KindBinaryExpression:
		return transformBinaryExpression(s, node)
	case ast.KindCallExpression:
		return transformCallExpression(s, node)
	case ast.KindClassExpression:
		return transformClassExpression(s, node)
	case ast.KindConditionalExpression:
		return transformConditionalExpression(s, node)
	case ast.KindDeleteExpression:
		return transformDeleteExpression(s, node)
	case ast.KindElementAccessExpression:
		return transformElementAccessExpression(s, node)
	case ast.KindPropertyAccessExpression:
		return transformPropertyAccessExpression(s, node)
	case ast.KindFalseKeyword:
		return luau.Bool(false)
	case ast.KindTrueKeyword:
		return luau.Bool(true)
	case ast.KindIdentifier:
		return TransformIdentifier(s, node)
	case ast.KindJsxElement:
		return transformJsxElement(s, node)
	case ast.KindJsxExpression:
		return transformJsxExpression(s, node)
	case ast.KindJsxFragment:
		return transformJsxFragment(s, node)
	case ast.KindJsxSelfClosingElement:
		return transformJsxSelfClosingElement(s, node)
	case ast.KindNewExpression:
		return transformNewExpression(s, node)
	case ast.KindNoSubstitutionTemplateLiteral:
		return transformNoSubstitutionTemplateLiteral(s, node)
	case ast.KindNumericLiteral:
		return transformNumericLiteral(s, node)
	case ast.KindObjectLiteralExpression:
		return transformObjectLiteralExpression(s, node)
	case ast.KindParenthesizedExpression:
		return transformParenthesizedExpression(s, node)
	case ast.KindPostfixUnaryExpression:
		return transformPostfixUnaryExpression(s, node)
	case ast.KindPrefixUnaryExpression:
		return transformPrefixUnaryExpression(s, node)
	case ast.KindSpreadElement:
		return transformSpreadElement(s, node)
	case ast.KindStringLiteral:
		return transformStringLiteral(s, node)
	case ast.KindTaggedTemplateExpression:
		return transformTaggedTemplateExpression(s, node)
	case ast.KindTemplateExpression:
		return transformTemplateExpression(s, node)
	case ast.KindThisKeyword:
		return transformThisExpression(s, node)
	case ast.KindSuperKeyword:
		return transformSuperKeyword()
	case ast.KindVoidExpression:
		return transformVoidExpression(s, node)

	// type-only wrappers -> transformTypeExpression (inner expression)
	case ast.KindAsExpression,
		ast.KindExpressionWithTypeArguments,
		ast.KindNonNullExpression,
		ast.KindSatisfiesExpression,
		ast.KindTypeAssertionExpression:
		return TransformExpression(s, node.Expression())
	}

	// Upstream-supported kinds awaiting their port (classes, JSX, new, await,
	// delete, ...) and genuinely unknown kinds both fail loudly.
	s.Diags.Add(DiagRotorNotYetSupported(node, kindName(node.Kind)))
	return luau.NewNone()
}

// transformStatementDispatch ports nodes/statements/transformStatement.ts.
func transformStatementDispatch(s *State, node *ast.Node) *luau.List[luau.Statement] {
	// declare-modifier skip: `declare` means the identifier is defined
	// elsewhere; emit nothing (upstream transformStatement L103-107).
	if ast.CanHaveModifiers(node) {
		if modifiers := node.Modifiers(); modifiers != nil {
			for _, modifier := range modifiers.Nodes {
				if modifier.Kind == ast.KindDeclareKeyword {
					return luau.NewList[luau.Statement]()
				}
			}
		}
	}

	switch node.Kind {
	// no emit
	case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindEmptyStatement:
		return luau.NewList[luau.Statement]()

	// banned statements
	case ast.KindForInStatement:
		s.Diags.Add(DiagNoForInStatement(node))
		return luau.NewList[luau.Statement]()
	case ast.KindDebuggerStatement:
		s.Diags.Add(DiagNoDebuggerStatement(node))
		return luau.NewList[luau.Statement]()

	// regular transforms
	case ast.KindBlock:
		return transformBlock(s, node)
	case ast.KindBreakStatement:
		return transformBreakStatement(s, node)
	case ast.KindClassDeclaration:
		return transformClassDeclaration(s, node)
	case ast.KindContinueStatement:
		return transformContinueStatement(s, node)
	case ast.KindDoStatement:
		return transformDoStatement(s, node)
	case ast.KindEnumDeclaration:
		return transformEnumDeclaration(s, node)
	case ast.KindExportAssignment:
		return transformExportAssignment(s, node)
	case ast.KindExportDeclaration:
		return transformExportDeclaration(s, node)
	case ast.KindForStatement:
		return transformForStatement(s, node)
	case ast.KindForOfStatement:
		return transformForOfStatement(s, node)
	case ast.KindFunctionDeclaration:
		return transformFunctionDeclaration(s, node)
	case ast.KindImportDeclaration:
		return transformImportDeclaration(s, node)
	case ast.KindImportEqualsDeclaration:
		return transformImportEqualsDeclaration(s, node)
	case ast.KindIfStatement:
		return transformIfStatement(s, node)
	case ast.KindLabeledStatement:
		return transformLabeledStatement(s, node)
	case ast.KindModuleDeclaration:
		return transformModuleDeclaration(s, node)
	case ast.KindReturnStatement:
		return transformReturnStatement(s, node)
	case ast.KindSwitchStatement:
		return transformSwitchStatement(s, node)
	case ast.KindThrowStatement:
		return transformThrowStatement(s, node)
	case ast.KindTryStatement:
		return transformTryStatement(s, node)
	case ast.KindVariableStatement:
		return transformVariableStatement(s, node)
	case ast.KindExpressionStatement:
		return transformExpressionStatement(s, node)
	case ast.KindWhileStatement:
		return transformWhileStatement(s, node)
	}

	// Statement kinds not yet ported report a not-yet-supported diagnostic
	// rather than emitting wrong output.
	s.Diags.Add(DiagRotorNotYetSupported(node, kindName(node.Kind)))
	return luau.NewList[luau.Statement]()
}
