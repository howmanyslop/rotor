// Package transformer ports roblox-ts's TSTransformer to Go over the tsgo
// compiler frontend.
package transformer

import (
	"fmt"
	"strings"

	"rotor/tsgo/ast"
)

// Diagnostic is the Go port of an upstream diagnostic produced by a factory
// from reference/roblox-ts/src/Shared/diagnostics.ts. Code is the upstream
// factory name (e.g. "noAny"); Message is the exact upstream message text
// (multi-part messages joined with "\n", ANSI color stripped); Node is the
// position source; Warning distinguishes ts.DiagnosticCategory.Warning from
// Error.
type Diagnostic struct {
	Code    string // upstream factory name, e.g. "noAny"
	Message string
	Node    *ast.Node // position source
	Warning bool
}

const repoURL = "https://github.com/roblox-ts/roblox-ts"

// suggestion ports Shared/diagnostics.ts suggestion(): prefixes
// "Suggestion: " (upstream additionally colors the text with kleur.yellow,
// which is presentation-only and not part of the message bytes we keep).
func suggestion(text string) string {
	return "Suggestion: " + text
}

// issue ports Shared/util/createGithubLink.ts issue(): a GitHub issue link
// suffix line (upstream greys the URL with kleur — presentation-only).
func issue(id int) string {
	return fmt.Sprintf("More information: %s/issues/%d", repoURL, id)
}

// errorDiag mirrors upstream error(...messages): joins message parts with
// "\n" into an Error-category diagnostic.
func errorDiag(code string, node *ast.Node, messages ...string) Diagnostic {
	return Diagnostic{Code: code, Message: strings.Join(messages, "\n"), Node: node}
}

// warningDiag mirrors upstream warning(...messages).
func warningDiag(code string, node *ast.Node, messages ...string) Diagnostic {
	return Diagnostic{Code: code, Message: strings.Join(messages, "\n"), Node: node, Warning: true}
}

// ----------------------------------------------------------------------------
// errors — one constructor per upstream factory, in source order of
// reference/roblox-ts/src/Shared/diagnostics.ts. Message text is byte-exact.
// ----------------------------------------------------------------------------

// reserved identifiers

func DiagNoInvalidIdentifier(node *ast.Node) Diagnostic {
	return errorDiag("noInvalidIdentifier", node,
		"Invalid Luau identifier!",
		"Luau identifiers must start with a letter and only contain letters, numbers, and underscores.",
		"Reserved Luau keywords cannot be used as identifiers.",
	)
}

func DiagNoReservedIdentifier(node *ast.Node) Diagnostic {
	return errorDiag("noReservedIdentifier", node, "Cannot use identifier reserved for compiler internal usage.")
}

func DiagNoReservedClassFields(node *ast.Node) Diagnostic {
	return errorDiag("noReservedClassFields", node, "Cannot use class field reserved for compiler internal usage.")
}

func DiagNoClassMetamethods(node *ast.Node) Diagnostic {
	return errorDiag("noClassMetamethods", node, "Metamethods cannot be used in class definitions!")
}

// banned statements

func DiagNoForInStatement(node *ast.Node) Diagnostic {
	return errorDiag("noForInStatement", node, "for-in loop statements are not supported!")
}

// DiagNoLabeledStatementsWithinTryCatch replaces upstream 3.0.0's blanket
// `noLabeledStatement`: rotor supports loop labels (see labeledstatement.go)
// but a label cannot cross a try/catch, whose break/continue reroute rides the
// TS.TRY_BREAK/TRY_CONTINUE sentinel protocol and carries no label. Message
// byte-exact with roblox-ts PR #2928 so a future upstream release lines up.
func DiagNoLabeledStatementsWithinTryCatch(node *ast.Node) Diagnostic {
	return errorDiag("noLabeledStatementsWithinTryCatch", node,
		"labels are not supported within try/catch blocks!")
}

func DiagNoDebuggerStatement(node *ast.Node) Diagnostic {
	return errorDiag("noDebuggerStatement", node, "`debugger` is not supported!")
}

// banned expressions

func DiagNoNullLiteral(node *ast.Node) Diagnostic {
	return errorDiag("noNullLiteral", node, "`null` is not supported!", suggestion("Use `undefined` instead."))
}

func DiagNoPrivateIdentifier(node *ast.Node) Diagnostic {
	return errorDiag("noPrivateIdentifier", node, "Private identifiers are not supported!")
}

func DiagNoTypeOfExpression(node *ast.Node) Diagnostic {
	return errorDiag("noTypeOfExpression", node,
		"`typeof` operator is not supported!",
		suggestion("Use `typeIs(value, type)` or `typeOf(value)` instead."),
	)
}

func DiagNoRegex(node *ast.Node) Diagnostic {
	return errorDiag("noRegex", node, "Regular expressions are not supported!")
}

func DiagNoBigInt(node *ast.Node) Diagnostic {
	return errorDiag("noBigInt", node, "BigInt literals are not supported!")
}

// banned features

func DiagNoAny(node *ast.Node) Diagnostic {
	return errorDiag("noAny", node, "Using values of type `any` is not supported!", suggestion("Use `unknown` instead."))
}

func DiagNoVar(node *ast.Node) Diagnostic {
	return errorDiag("noVar", node, "`var` keyword is not supported!", suggestion("Use `let` or `const` instead."))
}

func DiagNoGetterSetter(node *ast.Node) Diagnostic {
	return errorDiag("noGetterSetter", node, "Getters and Setters are not supported!", issue(457))
}

func DiagNoAutoAccessorModifiers(node *ast.Node) Diagnostic {
	return errorDiag("noAutoAccessorModifiers", node,
		"Getters and Setters are not supported!",
		"The `accessor` keyword requires generating get/set accessors",
		issue(457),
	)
}

func DiagNoEqualsEquals(node *ast.Node) Diagnostic {
	return errorDiag("noEqualsEquals", node, "operator `==` is not supported!", suggestion("Use `===` instead."))
}

func DiagNoExclamationEquals(node *ast.Node) Diagnostic {
	return errorDiag("noExclamationEquals", node, "operator `!=` is not supported!", suggestion("Use `!==` instead."))
}

func DiagNoEnumMerging(node *ast.Node) Diagnostic {
	return errorDiag("noEnumMerging", node, "Enum merging is not supported!")
}

func DiagNoNamespaceMerging(node *ast.Node) Diagnostic {
	return errorDiag("noNamespaceMerging", node, "Namespace merging is not supported!")
}

func DiagNoNestedSpreadsInAssignmentPatterns(node *ast.Node) Diagnostic {
	return errorDiag("noNestedSpreadsInAssignmentPatterns", node, "Nesting spreads in assignment patterns is not supported!")
}

func DiagNoRestSpreadingOfRobloxTypes(node *ast.Node) Diagnostic {
	return errorDiag("noRestSpreadingOfRobloxTypes", node, "Operator `...` is not allowed on Roblox types!")
}

func DiagNoFunctionExpressionName(node *ast.Node) Diagnostic {
	return errorDiag("noFunctionExpressionName", node, "Function expression names are not supported!")
}

func DiagNoPrecedingSpreadElement(node *ast.Node) Diagnostic {
	return errorDiag("noPrecedingSpreadElement", node, "Spread element must come last in a list of arguments!")
}

func DiagNoLuaTupleDestructureAssignmentExpression(node *ast.Node) Diagnostic {
	return errorDiag("noLuaTupleDestructureAssignmentExpression", node,
		"Cannot destructure LuaTuple<T> expression outside of an ExpressionStatement!",
	)
}

func DiagNoExportAssignmentLet(node *ast.Node) Diagnostic {
	return errorDiag("noExportAssignmentLet", node, "Cannot use `export =` on a `let` variable!", suggestion("Use `const` instead."))
}

func DiagNoGlobalThis(node *ast.Node) Diagnostic {
	return errorDiag("noGlobalThis", node, "`globalThis` is not supported!")
}

func DiagNoArguments(node *ast.Node) Diagnostic {
	return errorDiag("noArguments", node, "`arguments` is not supported!")
}

func DiagNoPrototype(node *ast.Node) Diagnostic {
	return errorDiag("noPrototype", node, "`prototype` is not supported!")
}

func DiagNoRobloxSymbolInstanceof(node *ast.Node) Diagnostic {
	// NOTE: upstream's suggestion text is missing the closing backtick and
	// trailing period — preserved byte-exact.
	return errorDiag("noRobloxSymbolInstanceof", node,
		"The `instanceof` operator can only be used on roblox-ts classes!",
		suggestion(`Use `+"`"+`typeIs(myThing, "TypeToCheck") instead`),
	)
}

func DiagNoNonNumberStringRelationOperator(node *ast.Node) Diagnostic {
	return errorDiag("noNonNumberStringRelationOperator", node, "Relation operators can only be used on number or string types!")
}

func DiagNoInstanceMethodCollisions(node *ast.Node) Diagnostic {
	return errorDiag("noInstanceMethodCollisions", node, "Static methods cannot use the same name as instance methods!")
}

func DiagNoStaticMethodCollisions(node *ast.Node) Diagnostic {
	return errorDiag("noStaticMethodCollisions", node, "Instance methods cannot use the same name as static methods!")
}

func DiagNoUnaryPlus(node *ast.Node) Diagnostic {
	return errorDiag("noUnaryPlus", node, "Unary `+` is not supported!", suggestion("Use `tonumber(x)` instead."))
}

func DiagNoNonNumberUnaryMinus(node *ast.Node) Diagnostic {
	return errorDiag("noNonNumberUnaryMinus", node, "Unary `-` is only supported for number types!")
}

func DiagNoAwaitForOf(node *ast.Node) Diagnostic {
	return errorDiag("noAwaitForOf", node, "`await` is not supported in for-of loops!")
}

func DiagNoAsyncGeneratorFunctions(node *ast.Node) Diagnostic {
	return errorDiag("noAsyncGeneratorFunctions", node, "Async generator functions are not supported!")
}

func DiagNoNonStringModuleSpecifier(node *ast.Node) Diagnostic {
	return errorDiag("noNonStringModuleSpecifier", node, "Module specifiers must be a string literal.")
}

func DiagNoIterableIteration(node *ast.Node) Diagnostic {
	return errorDiag("noIterableIteration", node, "Iterating on Iterable<T> is not supported! You must use a more specific type.")
}

func DiagNoMixedTypeCall(node *ast.Node) Diagnostic {
	return errorDiag("noMixedTypeCall", node,
		"Attempted to call a function with mixed types! All definitions must either be a method or a callback.",
	)
}

func DiagNoIndexWithoutCall(node *ast.Node) Diagnostic {
	return errorDiag("noIndexWithoutCall", node,
		"Cannot index a method without calling it!",
		suggestion("Use the form `() => a.b()` instead of `a.b`."),
	)
}

func DiagNoCommentDirectives(node *ast.Node) Diagnostic {
	return errorDiag("noCommentDirectives", node,
		"Usage of `@ts-ignore`, `@ts-expect-error`, and `@ts-nocheck` are not supported!",
		"roblox-ts needs type and symbol info to compile correctly.",
		suggestion("Consider using type assertions or `declare` statements."),
	)
}

// macro methods

func DiagNoOptionalMacroCall(node *ast.Node) Diagnostic {
	return errorDiag("noOptionalMacroCall", node,
		"Macro methods can not be optionally called!",
		suggestion("Macros always exist. Use a normal call."),
	)
}

func DiagNoConstructorMacroWithoutNew(node *ast.Node) Diagnostic {
	return errorDiag("noConstructorMacroWithoutNew", node, "Cannot index a constructor macro without using the `new` operator!")
}

func DiagNoMacroExtends(node *ast.Node) Diagnostic {
	return errorDiag("noMacroExtends", node,
		"Cannot extend from a macro class!",
		suggestion("Store an instance of the macro class in a property."),
	)
}

func DiagNoMacroUnion(node *ast.Node) Diagnostic {
	return errorDiag("noMacroUnion", node, "Macro cannot be applied to a union type!")
}

func DiagNoMacroObjectSpread(node *ast.Node) Diagnostic {
	return errorDiag("noMacroObjectSpread", node,
		"Macro classes cannot be used in an object spread!",
		suggestion("Did you mean to use an array spread? `[ ...exp ]`"),
	)
}

func DiagNoVarArgsMacroSpread(node *ast.Node) Diagnostic {
	return errorDiag("noVarArgsMacroSpread", node, "Macros which use variadic arguments do not support spread expressions!", issue(1149))
}

func DiagNoRangeMacroOutsideForOf(node *ast.Node) Diagnostic {
	return errorDiag("noRangeMacroOutsideForOf", node, "$range() macro is only valid as an expression of a for-of loop!")
}

func DiagNoTupleMacroOutsideReturn(node *ast.Node) Diagnostic {
	return errorDiag("noTupleMacroOutsideReturn", node, "$tuple() macro is only valid as an expression of a return statement!")
}

// import/export

func DiagNoModuleSpecifierFile(node *ast.Node) Diagnostic {
	return errorDiag("noModuleSpecifierFile", node, "Could not find file for import. Did you forget to `npm install`?")
}

func DiagNoInvalidModule(node *ast.Node) Diagnostic {
	return errorDiag("noInvalidModule", node, "You can only use npm scopes that are listed in your typeRoots.")
}

func DiagNoUnscopedModule(node *ast.Node) Diagnostic {
	return errorDiag("noUnscopedModule", node, "You cannot use modules directly under node_modules.")
}

func DiagNoNonModuleImport(node *ast.Node) Diagnostic {
	return errorDiag("noNonModuleImport", node, "Cannot import a non-ModuleScript!")
}

func DiagNoIsolatedImport(node *ast.Node) Diagnostic {
	return errorDiag("noIsolatedImport", node, "Attempted to import a file inside of an isolated container from outside!")
}

func DiagNoServerImport(node *ast.Node) Diagnostic {
	return errorDiag("noServerImport", node,
		"Cannot import a server file from a shared or client location!",
		suggestion("Move the file you want to import to a shared location."),
	)
}

// jsx

func DiagNoPrecedingJsxSpreadElement(node *ast.Node) Diagnostic {
	return errorDiag("noPrecedingJsxSpreadElement", node, "JSX spread expression must come last in children!")
}

// semantic

func DiagExpectedMethodGotFunction(node *ast.Node) Diagnostic {
	return errorDiag("expectedMethodGotFunction", node, "Attempted to assign non-method where method was expected.")
}

func DiagExpectedFunctionGotMethod(node *ast.Node) Diagnostic {
	return errorDiag("expectedFunctionGotMethod", node, "Attempted to assign method where non-method was expected.")
}

// files
//
// Upstream errorWithContext factories: the formatted context lines are
// appended after the static messages; entries that evaluate to `false` are
// filtered out (noRojoData's suggestion when !isPackage).

func DiagNoRojoData(node *ast.Node, path string, isPackage bool) Diagnostic {
	messages := []string{
		fmt.Sprintf("Could not find Rojo data. There is no $path in your Rojo config that covers %s", path),
	}
	if isPackage {
		messages = append(messages, suggestion("Did you forget to add a custom npm scope to your default.project.json?"))
	}
	return errorDiag("noRojoData", node, messages...)
}

func DiagNoPackageImportWithoutScope(node *ast.Node, path string, rbxPath []string) Diagnostic {
	return errorDiag("noPackageImportWithoutScope", node,
		"Imported package Roblox path is missing an npm scope!",
		fmt.Sprintf("Package path: %s", path),
		fmt.Sprintf("Roblox path: %s", strings.Join(rbxPath, ".")),
		suggestion("You might need to update your \"node_modules\" in default.project.json to match:\n"+
			"\"node_modules\": {\n\t\"$className\": \"Folder\",\n\t\"@rbxts\": {\n\t\t\"$path\": \"node_modules/@rbxts\"\n\t}\n}"),
	)
}

// Upstream errorText factories (incorrectFileName, rojoPathInSrc) have no
// node; they surface as project-level text diagnostics, so Node stays nil.

func DiagIncorrectFileName(originalFileName, suggestedFileName, fullPath string) Diagnostic {
	return errorDiag("incorrectFileName", nil,
		fmt.Sprintf("Incorrect file name: `%s`!", originalFileName),
		fmt.Sprintf("Full path: %s", fullPath),
		suggestion(fmt.Sprintf("Change `%s` to `%s`.", originalFileName, suggestedFileName)),
	)
}

func DiagRojoPathInSrc(partitionPath, suggestedPath string) Diagnostic {
	return errorDiag("rojoPathInSrc", nil,
		"Invalid Rojo configuration. $path fields should be relative to out directory.",
		// Upstream interpolates the paths raw inside literal quotes
		// (`from "${partitionPath}" to "${suggestedPath}"`, diagnostics.ts:233);
		// Go's %q would double-escape Windows backslashes, so plain %s.
		suggestion(fmt.Sprintf("Change the value of $path from \"%s\" to \"%s\".", partitionPath, suggestedPath)),
	)
}

// ----------------------------------------------------------------------------
// rotor-specific (no upstream counterpart)
// ----------------------------------------------------------------------------

// DiagRotorNotYetSupported marks a construct that upstream compiles but rotor
// has not ported yet. Deliberately distinct from upstream banned-kind
// diagnostics (which reproduce rbxtsc messages byte-exact) so unsupported
// input always fails loudly instead of producing silently wrong output.
func DiagRotorNotYetSupported(node *ast.Node, what string) Diagnostic {
	return errorDiag("rotorNotYetSupported", node, fmt.Sprintf("rotor: %s not yet supported", what))
}

// DiagRotorUnknownLabel fires when `break <name>` / `continue <name>` names a
// label that is not in scope. TypeScript already reports this, so it is only
// reachable when the TS error was suppressed (`// @ts-ignore`); emitting a
// misdirected `break` instead would be silently wrong output (rotor extension).
func DiagRotorUnknownLabel(node *ast.Node) Diagnostic {
	return errorDiag("rotorUnknownLabel", node,
		fmt.Sprintf("rotor: no enclosing label named `%s`", node.Text()))
}

// DiagRotorLabeledStatementFlowControl rejects a label on a NON-loop statement
// whose body also contains an unlabeled `break`/`continue` bound to an
// enclosing loop or switch. rotor lowers such a label to `repeat ... until
// true`, which is itself a break boundary, so that inner unlabeled statement
// would silently retarget the wrapper (rotor extension).
func DiagRotorLabeledStatementFlowControl(node *ast.Node) Diagnostic {
	return errorDiag("rotorLabeledStatementFlowControl", node,
		"rotor: a label on a non-loop statement cannot contain an unlabeled `break`/`continue` that targets an enclosing loop or switch",
		suggestion("Label the enclosing loop and use `break <label>` / `continue <label>` instead."),
	)
}

// DiagRotorLabeledContinueInDoWhile rejects a labeled `continue` that reaches
// a `do ... while (<condition needing prereqs>)`. The condition's prereq
// statements live in the emitted `repeat` body, and Luau rejects a `continue`
// that jumps over a local the `until` condition reads. rbxtsc has the same
// hole for an UNLABELED `continue` in that position; rotor reproduces that
// byte-for-byte to keep parity, but refuses to add a second way in through a
// rotor-only feature (rotor extension).
func DiagRotorLabeledContinueInDoWhile(node *ast.Node) Diagnostic {
	return errorDiag("rotorLabeledContinueInDoWhile", node,
		"rotor: `continue <label>` cannot target a do/while loop whose condition needs temporaries — Luau forbids `continue` before those locals",
		suggestion("Move the condition into the loop body, or restructure as a `while` loop."),
	)
}

// DiagRotorNoProjectContext guards transformer paths that require the Rojo
// project context (State.Rojo) when none was attached — typically a
// transformer-level unit test exercising import emission without
// SetRojoContext. Upstream always constructs TransformState with full
// ProjectData, so this condition has no rbxtsc counterpart.
func DiagRotorNoProjectContext(node *ast.Node) Diagnostic {
	return errorDiag("rotorNoProjectContext", node, "rotor: imports require project context (no Rojo resolver attached)")
}

// DiagRotorEnvNonLiteralArg rejects a dynamic $env name or fallback —
// the macro inlines values at compile time, so both must be string literals
// (rotor extension; see envmacro.go).
func DiagRotorEnvNonLiteralArg(node *ast.Node) Diagnostic {
	return errorDiag("rotorEnvNonLiteralArg", node,
		"rotor: $env arguments must be string literals — the value is inlined at compile time",
		suggestion("Use `$env(\"NAME\")`, `$env(\"NAME\", \"fallback\")`, `$env.NAME`, or `$env[\"NAME\"]`."),
	)
}

// DiagRotorEnvBadUsage rejects $env in any position other than a direct
// call, property access, or element access (e.g. `const x = $env`) — the
// macro identifier has no runtime value to emit (rotor extension).
func DiagRotorEnvBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorEnvBadUsage", node,
		"rotor: $env must be used as `$env(\"NAME\")`, `$env(\"NAME\", \"fallback\")`, `$env.NAME`, or `$env[\"NAME\"]`",
	)
}

// DiagRotorAssetNonLiteralArg rejects a dynamic $asset path — the macro
// resolves and inlines the asset id at compile time, so the path must be a
// string literal (rotor extension; see assetmacro.go).
func DiagRotorAssetNonLiteralArg(node *ast.Node) Diagnostic {
	return errorDiag("rotorAssetNonLiteralArg", node,
		"rotor: $asset path must be a string literal — the asset id is resolved and inlined at compile time",
		suggestion("Use `$asset(\"assets/logo.png\")`."),
	)
}

// DiagRotorAssetBadUsage rejects $asset in any position other than a direct
// call (e.g. `const x = $asset`) — the macro identifier has no runtime value
// to emit (rotor extension).
func DiagRotorAssetBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorAssetBadUsage", node,
		"rotor: $asset must be called as `$asset(\"path/to/file.png\")`",
	)
}

// DiagRotorAssetNotCached rejects a $asset reference whose file has no entry
// in rotor-lock.json and could not be uploaded (no ROBLOX_API_KEY at build
// time). The id cannot be inlined offline (rotor extension).
func DiagRotorAssetNotCached(node *ast.Node, path string) Diagnostic {
	return errorDiag("rotorAssetNotCached", node,
		fmt.Sprintf("rotor: asset %q is not synced and cannot be uploaded offline", path),
		suggestion("Run `rotor asset sync`, or set ROBLOX_API_KEY so the build can upload it."),
	)
}

// DiagRotorAssetFileNotFound rejects a $asset reference to a file that does
// not exist on disk (rotor extension).
func DiagRotorAssetFileNotFound(node *ast.Node, path string) Diagnostic {
	return errorDiag("rotorAssetFileNotFound", node,
		fmt.Sprintf("rotor: asset file %q does not exist", path),
	)
}

// DiagRotorAssetResolveFailed carries a non-sentinel resolver error (e.g. an
// Open Cloud upload failure) as a clear $asset diagnostic (rotor extension).
func DiagRotorAssetResolveFailed(node *ast.Node, path string, message string) Diagnostic {
	return errorDiag("rotorAssetResolveFailed", node,
		fmt.Sprintf("rotor: could not resolve asset %q: %s", path, message),
	)
}

// DiagRotorAssetNoResolver guards $asset use in a State without an attached
// asset resolver (transformer-level unit tests, or a single-file path that
// never set State.Assets). Upstream has no $asset macro, so no counterpart.
func DiagRotorAssetNoResolver(node *ast.Node) Diagnostic {
	return errorDiag("rotorAssetNoResolver", node,
		"rotor: $asset requires project context (no asset resolver attached)",
	)
}

// $nameof (rotor extension; see nameofmacro.go)

// DiagRotorNameofInvalid rejects a $nameof argument that has no statically-
// knowable trailing name (an index expression, a call, a literal, `this`, ...).
func DiagRotorNameofInvalid(node *ast.Node) Diagnostic {
	return errorDiag("rotorNameofInvalid", node,
		"rotor: $nameof requires an expression ending in an identifier or property access",
		suggestion("Use `$nameof(foo)` or `$nameof(a.b.c)` — the trailing name is inlined as a string."),
	)
}

// DiagRotorNameofBadUsage rejects $nameof in any position other than a direct
// call (e.g. `const x = $nameof`) — the macro identifier has no runtime value.
func DiagRotorNameofBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorNameofBadUsage", node,
		"rotor: $nameof must be called as `$nameof(expr)`",
	)
}

// $keys (rotor extension; see keysmacro.go)

// DiagRotorKeysNoType rejects a $keys call with no type argument — the macro
// enumerates a type's string keys at compile time, so the type is required.
func DiagRotorKeysNoType(node *ast.Node) Diagnostic {
	return errorDiag("rotorKeysNoType", node,
		"rotor: $keys requires a single type argument whose string keys are inlined",
		suggestion("Use `$keys<{ x: number; y: string }>()`."),
	)
}

// DiagRotorKeysBadUsage rejects $keys in any position other than a direct call
// (e.g. `const x = $keys`) — the macro identifier has no runtime value.
func DiagRotorKeysBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorKeysBadUsage", node,
		"rotor: $keys must be called as `$keys<T>()`",
	)
}

// $file (rotor extension; see filemacro.go)

// DiagRotorFileNonLiteralArg rejects a dynamic $file path — the macro reads and
// inlines the file contents at compile time, so the path must be a string
// literal.
func DiagRotorFileNonLiteralArg(node *ast.Node) Diagnostic {
	return errorDiag("rotorFileNonLiteralArg", node,
		"rotor: $file path must be a string literal — the contents are read and inlined at compile time",
		suggestion("Use `$file(\"config.json\")`."),
	)
}

// DiagRotorFileBadUsage rejects $file in any position other than a direct call
// (e.g. `const x = $file`) — the macro identifier has no runtime value.
func DiagRotorFileBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorFileBadUsage", node,
		"rotor: $file must be called as `$file(\"path/to/file.json\")`",
	)
}

// DiagRotorFileNotFound rejects a $file reference to a file that does not exist
// on disk.
func DiagRotorFileNotFound(node *ast.Node, path string) Diagnostic {
	return errorDiag("rotorFileNotFound", node,
		fmt.Sprintf("rotor: file %q does not exist", path),
	)
}

// DiagRotorFileInvalidJSON rejects a $file reference whose `.json` contents do
// not parse as valid JSON.
func DiagRotorFileInvalidJSON(node *ast.Node, path string, message string) Diagnostic {
	return errorDiag("rotorFileInvalidJSON", node,
		fmt.Sprintf("rotor: file %q is not valid JSON: %s", path, message),
	)
}

// DiagRotorFileNoResolver guards $file use in a State without an attached file
// resolver (transformer-level unit tests, or a single-file path that never set
// State.Files).
func DiagRotorFileNoResolver(node *ast.Node) Diagnostic {
	return errorDiag("rotorFileNoResolver", node,
		"rotor: $file requires project context (no file resolver attached)",
	)
}

// $git / $buildTime (rotor extension; see gitmacro.go)

// DiagRotorGitNonLiteralArg rejects a dynamic $git field — the macro inlines a
// build-time value, so the field must be a string literal.
func DiagRotorGitNonLiteralArg(node *ast.Node) Diagnostic {
	return errorDiag("rotorGitNonLiteralArg", node,
		"rotor: $git field must be a string literal — the value is inlined at compile time",
		suggestion("Use `$git(\"sha\")`, `$git(\"branch\")`, `$git(\"tag\")`, or `$git(\"dirty\")`."),
	)
}

// DiagRotorGitBadField rejects a $git field name outside the supported set
// (defensive — the ambient overloads already restrict it).
func DiagRotorGitBadField(node *ast.Node, field string) Diagnostic {
	return errorDiag("rotorGitBadField", node,
		fmt.Sprintf("rotor: unknown $git field %q", field),
		suggestion("Valid fields: \"sha\", \"branch\", \"tag\", \"dirty\"."),
	)
}

// DiagRotorGitBadUsage rejects $git in any position other than a direct call.
func DiagRotorGitBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorGitBadUsage", node,
		"rotor: $git must be called as `$git(\"sha\"|\"branch\"|\"tag\"|\"dirty\")`",
	)
}

// DiagRotorBuildTimeBadUsage rejects $buildTime in any position other than a
// direct zero-argument call.
func DiagRotorBuildTimeBadUsage(node *ast.Node) Diagnostic {
	return errorDiag("rotorBuildTimeBadUsage", node,
		"rotor: $buildTime must be called as `$buildTime()`",
	)
}

// kindName strips tsgo's stringer prefix: KindCallExpression -> "CallExpression"
// (matches upstream getKindName output for diagnostics/debugging).
func kindName(kind ast.Kind) string {
	return strings.TrimPrefix(kind.String(), "Kind")
}

// ----------------------------------------------------------------------------
// warnings
// ----------------------------------------------------------------------------

func DiagTruthyChange(node *ast.Node, checksStr string) Diagnostic {
	return warningDiag("truthyChange", node, fmt.Sprintf("Value will be checked against %s", checksStr))
}

func DiagStringOffsetChange(node *ast.Node, text string) Diagnostic {
	return warningDiag("stringOffsetChange", node, fmt.Sprintf("String macros no longer offset inputs: %s", text))
}

// DiagTransformerNotFound ports the warningText factory (no node). err is the
// stringified load error (upstream concatenates `"More info: " + err`).
func DiagTransformerNotFound(name string, err string) Diagnostic {
	return warningDiag("transformerNotFound", nil,
		fmt.Sprintf("Transformer `%s` was not found!", name),
		"More info: "+err,
		suggestion("Did you forget to install the package?"),
	)
}

func DiagRuntimeLibUsedInReplicatedFirst(node *ast.Node) Diagnostic {
	return warningDiag("runtimeLibUsedInReplicatedFirst", node,
		"This statement would generate a call to the runtime library. The runtime library should not be used from ReplicatedFirst.",
	)
}
