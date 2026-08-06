# Phase 2 Transforms Digest — First Wave (straight-line imperative TS)

Source of truth for porting roblox-ts v3.0.0 transforms to Go without reading the TS.
All paths relative to `reference/roblox-ts/src/`. Every checker/type-API usage is flagged
`CHECKER:`. Scope: literals, identifiers, binary/unary/logical, truthiness, calls,
property/element access, and the core statements. Excluded (noted as entry points only):
classes, destructuring, spread, async/generators, JSX, macros (error cleanly), imports/exports
beyond basic named exports.

---

## 0. Core machinery (prerequisite for everything below)

### 0.1 TransformState — `TSTransformer/classes/TransformState.ts`

The transform is a recursive tree walk that returns Luau expressions/statement-lists while
pushing "prerequisite statements" onto a stack of statement lists.

- `prereqStatementsStack: Array<luau.List<luau.Statement>>` (L99)
- `prereq(statement)` (L105): push one statement onto top of stack.
- `prereqList(statements)` (L113): push a list onto top of stack.
- `pushPrereqStatementsStack()` / `popPrereqStatementsStack()` (L120–133): push/pop a fresh list.
- `capturePrereqs(callback)` (L158): push stack, run callback, pop & return the collected list.
- `capture<T>(callback): [value, prereqs]` (L167): same but also returns callback's value.
- `noPrereqs(callback)` (L173): capture + assert the prereq list is empty.

Variable helpers:
- `pushToVar(expression, name?)` (L272): emits `local <tempId> = <expression>` as a prereq and
  returns the new `luau.TemporaryIdentifier`. Temp name falls back to `valueToIdStr(expression)`.
- `pushToVarIfComplex(expression, name?)` (L287): returns `expression` unchanged if
  `luau.isSimple(expression)` (literals, identifiers, temp ids, nil, true/false, etc.),
  else `pushToVar`.
- `pushToVarIfNonId(expression, name?)` (L301): returns unchanged if `luau.isAnyIdentifier`,
  else `pushToVar`.

Type lookup (THE central checker call):
- `getType(node)` (L184–186):
  ```ts
  public getType(node: ts.Node) {
      return getOrSetDefault(this.getTypeCache, node, () => this.typeChecker.getTypeAtLocation(skipUpwards(node)));
  }
  ```
  CHECKER: `typeChecker.getTypeAtLocation(skipUpwards(node))` — memoized per node. Note the
  `skipUpwards` — the type is taken at the highest enclosing parenthesized/assertion wrapper,
  so `(x as Foo)` queries the type of the `as` expression, not of `x`.

Runtime library:
- `TS(node, name)` (L189–197): sets `usesRuntimeLib = true`, returns `luau.property(luau.globals.TS, name)`
  i.e. `TS.<name>`; warns `warnings.runtimeLibUsedInReplicatedFirst` when project is a Game and the
  file lands in ReplicatedFirst. First wave only needs this for `instanceof` (`TS.instanceof`) and
  try/return interplay (out of scope); a port can stub it.

Comments:
- `getLeadingComments(node)` (L139–153): `ts.getLeadingCommentRanges(sourceFileText, node.pos)`,
  maps each range to `luau.comment(text)` where text strips the leading `//`/`/*` 2 chars and a
  trailing `*/` 2 chars for multi-line ranges.

Hoisting maps:
- `hoistsByStatement: Map<ts.Statement | ts.CaseClause, Array<ts.Identifier>>` (L180)
- `isHoisted: Map<ts.Symbol, boolean>` (L181)
- `symbolToIdMap: Map<ts.Symbol, luau.TemporaryIdentifier>` (L387) — identifier substitution used
  by the for-loop closure-copy logic and others.

Module-export access (needed for `export let`):
- `getModuleIdPropertyAccess(idSymbol)` (L356–364): if symbol's valueDeclaration belongs to a
  module with an id registered, returns `moduleId.<aliasName>`. Uses
  CHECKER: `typeChecker.getSymbolAtLocation(sourceFile | moduleDecl.name)` (L332),
  CHECKER: `typeChecker.getExportsOfModule(moduleSymbol)` (L310),
  CHECKER: `ts.skipAlias(exportSymbol, typeChecker)` (L318).

### 0.2 DiagnosticService — `TSTransformer/classes/DiagnosticService.ts`
Static accumulator: `addDiagnostic` pushes onto array (L15); `flush()` (L30) returns and clears.
Diagnostics never abort the walk — transforms return `luau.none()`/empty list and continue, so a
single compile reports many errors.

### 0.3 Small utils

- `wrapExpressionStatement(node)` — `util/wrapExpressionStatement.ts` L3–16 (discard semantics):
  ```ts
  if (luau.isTemporaryIdentifier(node) || luau.isNone(node)) return luau.list.make();
  else if (luau.isCall(node)) return list.make(CallStatement { expression: node });
  else return list.make(VariableDeclaration { left: luau.tempId(), right: node });
  ```
  i.e. an expression used as a statement is: dropped if a temp/none, kept as a call statement if a
  call, otherwise assigned to a discarded `local _ = <exp>` (Luau can't have bare expressions).

- `convertToIndexableExpression(expression)` — `util/convertToIndexableExpression.ts` L7–12:
  if already `luau.isIndexableExpression` return as-is, else wrap in `ParenthesizedExpression`.
  (Indexable = identifiers, property/index accesses, calls, parenthesized.)

- Traversal — `util/traversal.ts`:
  - `isAncestorOf(ancestor, node)` L3: walk `node.parent` chain.
  - `skipDownwards(node)` L13–25: unwraps `NonNullExpression | ParenthesizedExpression |
    AsExpression | TypeAssertionExpression | SatisfiesExpression` by following `.expression`.
  - `skipUpwards(node)` L27–41: inverse — climbs while parent is one of those wrappers.
  - `getAncestor(node, check)` L43: first ancestor (inclusive) satisfying check.
  - `getModuleAncestor(node)` L57: first SourceFile or ModuleDeclaration ancestor.

- `getKindName(kind)` — `util/getKindName.ts`: debug name for asserts only.

- `getStatements(statement)` — `util/getStatements.ts` L3–5:
  `ts.isBlock(statement) ? statement.statements : [statement]` (used so `if (x) y();` and
  `if (x) { y(); }` share a code path).

- `binaryExpressionChain(expressions, operator)` — `util/expressionChain.ts` L9–14:
  left-fold `expressions.reduce((acc, cur) => luau.binary(acc, operator, cur))`.
  `propertyAccessExpressionChain(expression, names)` L21–26: fold `luau.property`.

- `offset(expression, value)` — `util/offset.ts` L17–42 (constant-folds the +1 array offset):
  - `value === 0` → return unchanged.
  - If expression is a luau `BinaryExpression` with op `+`/`-` and a literal-number right side,
    fold: `newRightValue = rightValue + value * (op === "-" ? -1 : 1)`; if 0 return `expression.left`
    else rebuild with the folded literal (L24–34). This turns `array[i - 1 + 1]` into `array[i]`.
  - If the whole expression is a literal number (incl. unary minus over literals, see
    `getLiteralNumberValue` L3–15) → fold to one literal.
  - Else emit `expression + |value|` (or `-` when value < 0).

- Pointers — `util/pointer.ts`: `Pointer<T> = { name, value }`. `createMapPointer(name)` starts as
  `luau.map()` (inline table constructor); `createArrayPointer` as `luau.array()`.
  `disableMapInline`/`disableArrayInline` (L48–64): if value is still the inline Map/Array,
  `ptr.value = state.pushToVar(ptr.value, ptr.name)` — converts to a temp id so subsequent fields
  are emitted as assignments. `assignToMapPointer(state, ptr, left, right)` (L20–46): if still
  inline, push `MapField { index: left, value: right }`; else prereq `ptr.value[left] = right`.

- `valueToIdStr(value)` — `util/valueToIdStr.ts`: derive a temp-name hint: identifier name,
  property name, or `X` from `X.new()`; uncapitalized; "" if not a valid identifier.

- `expressionMightMutate(state, expression, node?)` — `util/expressionMightMutate.ts` L7–58.
  Decides whether a previously-transformed Luau expression could change value if prereqs run after
  it (used to decide pushToVar in call transforms). Returns false for: temporary identifiers
  ("Assume tempIds are never re-assigned after being returned"), simple primitives, function
  expressions, varargs literal; recurses through parenthesized/if/binary/unary/array/set/map
  members. Otherwise (Identifier, ComputedIndex, PropertyAccess, Call, MethodCall): returns true,
  EXCEPT a TS identifier whose symbol is not mutable:
  ```ts
  if (ts.isIdentifier(node)) {
      const symbol = state.typeChecker.getSymbolAtLocation(node);   // CHECKER (L45)
      if (symbol && !isSymbolMutable(state, symbol)) return false;
  }
  ```

- `isSymbolMutable(state, idSymbol)` — `util/isSymbolMutable.ts` L6–20, cached in
  `multiTransformState.isDefinedAsLetCache`:
  - parameters → mutable (true);
  - else find ancestor `ts.VariableDeclarationList` of `symbol.valueDeclaration` and return
    `!!(varDecList.flags & ts.NodeFlags.Let)`;
  - else false.
  CHECKER: `symbol.valueDeclaration` access; `ts.NodeFlags.Let` test.

- `isUsedAsStatement(expression)` — `util/isUsedAsStatement.ts` L4–22:
  `child = skipUpwards(expression); parent = child.parent;`
  true if parent is ExpressionStatement; or parent is a ForStatement and `parent.condition !== child`
  (i.e. the expression is the initializer or incrementor); or parent is a DeleteExpression that is
  itself used as a statement.

### 0.4 ensureTransformOrder — `util/ensureTransformOrder.ts` L22–55

Evaluation-order preservation for sibling expressions (call args, binary operands, template spans,
array literal elements):
```ts
const expressionInfoList = nodes.map(node => state.capture(() => transformer(state, node)));
const lastArgWithPrereqsIndex = findLastIndex(expressionInfoList, ([, prereqs]) => !luau.list.isEmpty(prereqs));
for (let i = 0; i < expressionInfoList.length; i++) {
    const [expression, prereqs] = expressionInfoList[i];
    state.prereqList(prereqs);
    let isConstVar = false;
    const exp = nodes[i];
    if (ts.isIdentifier(exp)) {
        const symbol = state.typeChecker.getSymbolAtLocation(exp);   // CHECKER (L37)
        if (symbol && !isSymbolMutable(state, symbol)) isConstVar = true;
    }
    if (i < lastArgWithPrereqsIndex && !luau.isSimplePrimitive(expression)
        && !luau.isTemporaryIdentifier(expression) && !isConstVar) {
        result.push(state.pushToVar(expression, "exp"));
    } else {
        result.push(expression);
    }
}
```
Semantics: each node is transformed with its own captured prereqs; all prereqs are re-emitted in
order; any expression that comes BEFORE the last expression-with-prereqs must be snapshotted into a
temp unless it is a simple primitive, already a temp, or a `const` identifier (cannot be mutated by
the later prereqs). Expressions at/after the last prereq position are safe as-is.

---

## 1. Dispatch

### 1.1 transformExpression — `nodes/expressions/transformExpression.ts`

Table dispatch on `node.kind` (`TRANSFORMER_BY_KIND`, L70–121). `transformExpression` (L123–129)
looks up and calls; `assert(false, "Unknown expression: ...")` if missing.

Banned kinds → `DIAGNOSTIC(factory)` helper (L46–49) which adds the diagnostic and returns
`luau.none()` (`NO_EMIT`, L44):
- `BigIntLiteral` → `errors.noBigInt`
- `NullKeyword` → `errors.noNullLiteral`
- `PrivateIdentifier` → `errors.noPrivateIdentifier`
- `RegularExpressionLiteral` → `errors.noRegex`
- `TypeOfExpression` → `errors.noTypeOfExpression`

No-emit: `ImportKeyword` → `luau.none()`.

First-wave kinds mapped (others exist in table, port later):
ArrayLiteralExpression, BinaryExpression, CallExpression, ElementAccessExpression, FalseKeyword,
Identifier, NoSubstitutionTemplateLiteral, NumericLiteral, ObjectLiteralExpression,
ParenthesizedExpression, PostfixUnaryExpression, PrefixUnaryExpression, PropertyAccessExpression,
StringLiteral, TemplateExpression, TrueKeyword. Type-only wrappers
(`AsExpression`/`ExpressionWithTypeArguments`/`NonNullExpression`/`SatisfiesExpression`/
`TypeAssertionExpression`) → `transformTypeExpression` which just transforms the inner expression.
`transformParenthesizedExpression` (`expressions/transformParenthesizedExpression.ts` L7–14):
`skipDownwards(node.expression)`, transform, return as-is if `luau.isSimple`, else wrap in a Luau
ParenthesizedExpression.

### 1.2 transformStatement — `nodes/statements/transformStatement.ts`

Same table pattern (L62–96). NO_EMIT (empty list): InterfaceDeclaration, TypeAliasDeclaration,
EmptyStatement. Banned: ForInStatement → `errors.noForInStatement`, LabeledStatement →
`errors.noLabeledStatement`, DebuggerStatement → `errors.noDebuggerStatement`.

`transformStatement` (L103–113) — declare-modifier skip happens here for ALL statements:
```ts
const modifiers = ts.canHaveModifiers(node) ? ts.getModifiers(node) : undefined;
if (modifiers?.some(v => v.kind === ts.SyntaxKind.DeclareKeyword)) return NO_EMIT();
```

### 1.3 transformStatementList — `nodes/transformStatementList.ts` L26–91

Per statement, in order:
1. `const [transformedStatements, prereqStatements] = state.capture(() => transformStatement(state, statement));`
   (L42) — prereqs produced by the statement's own expressions are captured here.
2. Comments: if `state.compilerOptions.removeComments !== true`, push
   `state.getLeadingComments(statement)` (L45–47). NOTE ordering: comments come before the hoist
   declaration and before prereqs.
3. Hoisting: `createHoistDeclaration(state, statement)` (L51); if non-undefined push it.
   `util/createHoistDeclaration.ts` L7–16: reads `state.hoistsByStatement.get(statement)`; if any
   hoisted identifiers were recorded against this statement (by a *later-running* hoist check that
   executed during an *earlier* statement's transform — see §3), validates each
   (`validateIdentifier`) and emits one `local a, b, c` (VariableDeclaration with list left,
   `right: undefined`). Hoist merging = multiple identifiers merged into one declaration per
   statement.
4. `pushList(result, prereqStatements)` then `pushList(result, transformedStatements)` (L56–57).
5. Early termination (L59–62): if the last transformed statement `luau.isFinalStatement`
   (return/break/continue), `break` out of the loop — dead statements after a terminator are
   dropped (Luau requires terminators to be last in a block).
6. Namespace export bookkeeping (L65–80, out of first-wave scope): for `exportInfo.mapping`
   entries, emit `containerId.<exportName> = <exportName>` assignments.

Trailing comments (L83–88): after the loop, `getLastToken(parent, statements)` (L7–18) finds the
parent's last token (e.g. `}` or EOF) when it is not inside the last statement, and pushes its
leading comments — this preserves comments after the final statement in a block/file.

---

## 2. Literals

### 2.1 Numeric — `expressions/transformNumericLiteral.ts` L5–9
```ts
return luau.create(luau.SyntaxKind.NumberLiteral, { value: node.getText() });
```
Raw source text (preserves hex/binary/underscores); the luau-ast renderer is responsible for any
normalization. No checker calls.

### 2.2 String — `expressions/transformStringLiteral.ts` L6–8
`luau.string(createStringFromLiteral(node))`.
`util/createStringFromLiteral.ts` L11–26: uses `node.getText()` NOT `node.text` ("Cannot just use
`node.text` because that converts `\\n` to be `\n`" — escapes must be preserved verbatim).
- StringLiteral / NoSubstitutionTemplateLiteral: `ts.stripQuotes(text)` (strip 1 char each side).
- TemplateHead: slice off leading `` ` `` (1) and trailing `${` (2).
- TemplateMiddle: slice off leading `}` (1) and trailing `${` (2).
- TemplateTail: slice off leading `}` (1) and trailing `` ` `` (1).

### 2.3 Boolean — `expressions/transformBooleanLiteral.ts`
`transformTrueKeyword` → `TrueLiteral {}`; `transformFalseKeyword` → `FalseLiteral {}`.

### 2.4 NoSubstitutionTemplateLiteral — `expressions/transformNoSubstitutionTemplateLiteral.ts` L8–12
Backtick strings without `${}` stay as Luau interpolated strings (valid Luau):
`InterpolatedString { parts: [transformInterpolatedStringPart(node)] }`.
`nodes/transformInterpolatedStringPart.ts` L5–7:
`InterpolatedStringPart { text: createStringFromLiteral(node) }`.

### 2.5 TemplateExpression — `expressions/transformTemplateExpression.ts` L7–29
TS template strings ALWAYS become Luau `InterpolatedString` (backtick `` `a{b}c` `` syntax) — there
is NO concatenation fallback in v3 (concatenation only appears for binary `+`, §4):
```ts
const parts = luau.list.make<luau.InterpolatedStringPart | luau.Expression>();
if (node.head.text.length > 0) parts.push(transformInterpolatedStringPart(node.head));
const orderedExpressions = ensureTransformOrder(state, node.templateSpans.map(s => s.expression));
for (let i = 0; i < node.templateSpans.length; i++) {
    parts.push(orderedExpressions[i]);
    const templateSpan = node.templateSpans[i];
    if (templateSpan.literal.text.length > 0) parts.push(transformInterpolatedStringPart(templateSpan.literal));
}
return luau.create(luau.SyntaxKind.InterpolatedString, { parts });
```
Empty text parts are skipped (`.text.length > 0` checks use the cooked `.text`, while emitted text
uses raw `getText()` slices). Span expressions go through `ensureTransformOrder` (CHECKER via
§0.4). NOTE: the Luau renderer's interpolation implies `tostring` semantics per part.

### 2.6 ArrayLiteralExpression — `expressions/transformArrayLiteralExpression.ts` L10–91

Non-spread fast path (L11–13):
```ts
if (!node.elements.find(element => ts.isSpreadElement(element))) {
    return luau.array(ensureTransformOrder(state, node.elements));
}
```
→ inline `{ e1, e2, ... }` with order preservation. This is the only path the first wave needs.

Spread path (L15–90), documented for later: uses an `ArrayPointer` (starts inline). A running
`lengthId` temp caches `#array` (`updateLengthId`, L20–40: first time `local _length = #_array`,
afterwards `_length = #_array`; resets `amtElementsSinceUpdate`). Per element:
- SpreadElement (L44–64): if pointer still inline, `disableArrayInline` + `updateLengthId`. Then
  CHECKER: `state.getType(element.expression)` (L51) feeds
  `getAddIterableToArrayBuilder(state, element.expression, type)` (runtime-helper selection by
  iterable kind — array/set/map/string/generator/IterableFunction/etc.; this is where spread forces
  runtime helpers like `TS.array_push`/loops). `shouldUpdateLengthId = i < len - 1`.
- Plain element (L66–87): `state.capture(transformExpression)`. If pointer inline and element had
  prereqs → disable inline + update length. If still inline → push member. Else prereq
  `_array[_length + (amtElementsSinceUpdate + 1)] = exp`; `amtElementsSinceUpdate++`.
Returns `ptr.value`.

### 2.7 ObjectLiteralExpression — `expressions/transformObjectLiteralExpression.ts` L92–115

Walks `node.properties` building a `MapPointer` (inline `{...}` until forced out):
- every property first hits `validateMethodAssignment(state, property)`
  (`util/validateMethodAssignment.ts`; for object literals: compares method-ness of the element
  type vs its contextual type; CHECKER: `state.getType(node)` L30,
  CHECKER: `typeChecker.getContextualTypeForObjectLiteralElement(node)` L31; for spread elements,
  CHECKER: `typeChecker.getContextualType(node.expression)` L50 and
  CHECKER: `typeChecker.getTypeOfPropertyOfType(type, name)` L54–55; mismatches raise
  `errors.expectedMethodGotFunction` / `errors.expectedFunctionGotMethod`).
- PropertyAssignment: if name is PrivateIdentifier → `errors.noPrivateIdentifier`, continue. Else
  `transformPropertyAssignment` (L14–32):
  ```ts
  let [left, leftPrereqs] = state.capture(() => transformPropertyName(state, name));
  const [right, rightPrereqs] = state.capture(() => transformExpression(state, initializer));
  if (!luau.list.isEmpty(leftPrereqs) || !luau.list.isEmpty(rightPrereqs)) {
      disableMapInline(state, ptr);
      state.prereqList(leftPrereqs);
      left = state.pushToVar(left, "left");
  }
  state.prereqList(rightPrereqs);
  assignToMapPointer(state, ptr, left, right);
  ```
  `transformPropertyName` (`nodes/transformPropertyName.ts` L6–14): plain identifier key `a` →
  `luau.string("a")`; computed `[a]` → transform `name.expression`; string/number literal names →
  transform directly. (The luau-ast Map renderer turns string-literal indices into `a = v` /
  `["a"] = v` as appropriate.)
- ShorthandPropertyAssignment `{ a }`: `transformPropertyAssignment(state, ptr, property.name,
  property.name)` — name doubles as initializer expression (symbol resolution for the value side
  uses `getShorthandAssignmentValueSymbol`, see §3).
- SpreadAssignment (out of scope; documented): `transformSpreadAssignment` (L34–90).
  CHECKER: `typeChecker.getNonOptionalType(state.getType(property.expression))` (L35),
  `getFirstDefinedSymbol` (L36) → macro-only class check → `errors.noMacroObjectSpread`.
  CHECKER: `isDefinitelyType(type, isObjectType)` (L42). If definitely object AND map still inline
  AND empty: `ptr.value = pushToVar(table.clone(<exp>))` + `setmetatable(ptr.value, nil)` prereq.
  Otherwise `disableMapInline`; non-definitely-object spreads get `pushToVarIfComplex` then a
  `for k, v in spreadExp do ptr[k] = v end` generic-for, wrapped in
  `if <truthinessChecks(spreadExp)> then ... end` when not definitely an object.
- MethodDeclaration: `transformMethodDeclaration` prereqs (function transforms; out of scope).
- AccessorDeclaration (get/set): `errors.noGetterSetter`.
Returns `ptr.value`.

---

## 3. Identifiers — `expressions/transformIdentifier.ts`

### 3.1 transformIdentifierDefined (L14–28) — the "raw" form (also used by declarations)
```ts
const symbol = ts.isShorthandPropertyAssignment(node.parent)
    ? state.typeChecker.getShorthandAssignmentValueSymbol(node.parent)   // CHECKER (L16)
    : state.typeChecker.getSymbolAtLocation(node);                       // CHECKER (L17)
assert(symbol);
const replacementId = state.symbolToIdMap.get(symbol);
if (replacementId) return replacementId;
return luau.create(luau.SyntaxKind.Identifier, { name: node.text });
```

### 3.2 transformIdentifier (L111–176) — full reference path

1. Synthetic-node bail (L115–117): `if (!node.parent || ts.positionIsSynthesized(node.pos))`
   → plain `Identifier { name: node.text }` (JSX factory entities).
2. Symbol lookup, same shorthand split as above (L119–121, CHECKER L120/L121). `assert(symbol)`.
3. Special symbols (L124–130):
   - CHECKER: `typeChecker.isUndefinedSymbol(symbol)` → return `luau.nil()` (this is the
     `undefined` → `nil` mapping).
   - CHECKER: `typeChecker.isArgumentsSymbol(symbol)` → `errors.noArguments` (falls through).
   - `symbol === macroManager.getSymbolOrThrow(SYMBOL_NAMES.globalThis)` → `errors.noGlobalThis`.
4. Identifier macros (L132–135): `macroManager.getIdentifierMacro(symbol)` → run macro
   (e.g. `script`/`Promise`). Port: error or implement the tiny set later.
5. Constructor-macro misuse (L137–150): `getFirstConstructSymbol(state, node)`
   (`util/types.ts` L226–242: CHECKER `state.getType(expression)`, then scans
   `type.symbol.getDeclarations()` for an InterfaceDeclaration ConstructSignature member, returns
   `member.symbol`). If that symbol has a constructor macro: when the identifier is a class
   `extends` expression → `errors.noMacroExtends`, else `errors.noConstructorMacroWithoutNew`.
6. Call-macro misuse (L152–159): `parent = skipUpwards(node).parent;` if NOT
   (`parent` is CallExpression with `skipDownwards(parent.expression) == node`) and
   `macroManager.getCallMacro(symbol)` exists → `errors.noIndexWithoutCall`, return `luau.none()`.
7. `export let` indirection (L161–171): if `symbol.valueDeclaration` exists, is in THIS source
   file, and has no non-namespace ModuleDeclaration ancestor: `state.getModuleIdPropertyAccess(symbol)`;
   if it returns an access AND `isSymbolMutable(state, symbol)` → return `exports.<name>` access
   (mutable exported variables live on the exports table; consts don't).
8. `checkIdentifierHoist(state, node, symbol)` (L173) then `transformIdentifierDefined` (L175).

### 3.3 checkIdentifierHoist (L47–109) — hoisting decision (use-before-declare)

Records that a symbol must be pre-declared (`local x` merged at the declaration statement) when it
is referenced lexically BEFORE/AT its declaring statement within the same block. Logic:
- Skip if `state.isHoisted.get(symbol) !== undefined` (already decided).
- `declaration = symbol.valueDeclaration ?? getDeclarationFromImport(symbol)` (L52;
  `getDeclarationFromImport` L38–45 scans `symbol.declarations` for one under any import syntax —
  "for some reason, symbol.valueDeclaration doesn't point to imports").
- Bail (no hoist) when: no declaration; declaration under a Parameter; declaration is a
  ShorthandPropertyAssignment (L55); declaration is a ClassLike that is an ancestor of the
  reference ("class expressions can self refer", L60); declaring statement missing or is a
  For/ForOf/Try statement (L64–70); statement's parent is not BlockLike
  (SourceFile/Block/ModuleBlock/CaseClause/DefaultClause — `typeGuards.ts isBlockLike`) (L74–76);
  the reference's ancestor-that-is-a-child-of-that-parent (`getAncestorWhichIsChildOf`, L30–35) is
  missing or not a statement (L79–81).
- Compare indices in `parent.statements`: `siblingIdx > declarationIdx` → no hoist (normal forward
  use). `siblingIdx === declarationIdx` (self-reference inside own declaring statement): allowed
  without hoist for non-async FunctionDeclarations (`!ts.hasSyntacticModifier(decl,
  ts.ModifierFlags.Async)`), ClassDeclarations, and VariableStatements where the reference's
  nearest Statement-or-FunctionLike ancestor IS the declaration statement (i.e. `const f = () =>
  f()` does NOT hoist but `const x = x` does… actually the condition L97–99 means: reference
  directly inside the same variable statement (not nested in a function) → return/no-hoist).
- Otherwise (L105–106): `hoistsByStatement.getOrSetDefault(sibling).push(node);
  state.isHoisted.set(symbol, true);` — `sibling` is the statement (in the same block) containing
  the premature reference, so `local x` is emitted just before it by transformStatementList §1.3.

Reserved identifiers: validation happens at declaration sites, not references —
`util/validateIdentifier.ts` L7–13: `!luau.isValidIdentifier(text)` → `errors.noInvalidIdentifier`
(also catches Luau keywords like `end`, `local`); else `luau.isReservedIdentifier(text)`
(compiler-internal names: `_G`, `TS`, temp-id forms like `_0`…) → `errors.noReservedIdentifier`.

---

## 4. Binary expressions

### 4.1 transformBinaryExpression — `expressions/transformBinaryExpression.ts` L113–253

```ts
const operatorKind = node.operatorToken.kind;
validateNotAnyType(state, node.left);    // §11.2
validateNotAnyType(state, node.right);
```
1. Banned (L120–126): `==` → `errors.noEqualsEquals`, `!=` → `errors.noExclamationEquals`;
   both return `luau.none()`.
2. Logical (L129–135): `&&`, `||`, `??` → `transformLogical` (§4.4).
3. `ts.isLogicalOrCoalescingAssignmentExpression(node)` (`&&=`, `||=`, `??=`) →
   `transformLogicalOrCoalescingAssignmentExpression` (out of scope, L137–139).
4. Assignment operators (`ts.isAssignmentOperator(operatorKind)`, L141–215):
   - Destructuring LHS (ArrayLiteral L143–169 / ObjectLiteral L170–184): out of first-wave scope.
     Entry behavior to note: RHS transformed first; empty-pattern optimizations; LuaTuple
     direct-unpack optimization (CHECKER: `isLuaTupleType(state)(state.getType(node.right))` L154)
     with `errors.noLuaTupleDestructureAssignmentExpression` when used as a non-statement;
     the upstream rest-spread rejection inside `transformOptimizedArrayAssignmentPattern` (L49).
   - Simple/compound assignment (L186–214):
     ```ts
     const writableType = state.getType(node.left);     // CHECKER (L186)
     const valueType = state.getType(node.right);       // CHECKER (L187)
     const operator = getSimpleAssignmentOperator(writableType, operatorKind, valueType);
     const { writable, readable, value } = transformWritableAssignment(
         state, node.left, node.right, /*readAfterWrite*/ true, /*readBeforeWrite*/ operator === undefined);
     if (operator !== undefined) {
         return createAssignmentExpression(state, writable, operator, getAssignableValue(operator, value, valueType));
     } else {
         return createCompoundAssignmentExpression(state, node, writable, writableType, readable, operatorKind, value, valueType);
     }
     ```
     This is the *expression* form (`x = (y += 1)`): `readAfterWrite = true` so the writable target
     is stabilized for re-reading (see §4.3); the function returns `writable` as the value of the
     expression after prereqing the assignment.
5. Non-assignment (L217–252):
   ```ts
   const [left, right] = ensureTransformOrder(state, [node.left, node.right]);
   ```
   - `in` (L219–227): `right[left] ~= nil` —
     `luau.binary(ComputedIndexExpression { expression: convertToIndexableExpression(right), index: left }, "~=", luau.nil())`.
   - `instanceof` (L228–233): if
     CHECKER: `isPossiblyType(state.getType(node.right), isRobloxType(state))` (L229) →
     `errors.noRobloxSymbolInstanceof`. Emits `TS.instanceof(left, right)` (runtime lib).
   - Relational `< <= > >=` (L238–250): diagnostic gate —
     ```ts
     if ((!isDefinitelyType(leftType, isStringType) && !isDefinitelyType(leftType, isNumberType)) ||
         (!isDefinitelyType(rightType, isStringType) && !isDefinitelyType(leftType, isNumberType)))
         errors.noNonNumberStringRelationOperator(node);
     ```
     (Note the upstream quirk: the second clause re-tests `leftType` for number — verbatim at
     L245–246.) Where CHECKER: `leftType = state.getType(node.left)` (L235),
     `rightType = state.getType(node.right)` (L236). Then falls through to
     `createBinaryFromOperator` which emits the same `< <= > >=`.
   - Everything else → `createBinaryFromOperator(state, node, left, leftType, operatorKind, right, rightType)`.

### 4.2 createBinaryFromOperator — `util/createBinaryFromOperator.ts` L58–90

`OPERATOR_MAP` (L9–24): `<`→`<`, `>`→`>`, `<=`→`<=`, `>=`→`>=`, `===`→`==`, `!==`→`~=`,
`-`→`-`, `*`→`*`, `/`→`/`, `**`→`^`, `%`→`%`.
`BITWISE_OPERATOR_MAP` (L26–42): `&`→`bit32.band`, `|`→`bit32.bor`, `^`→`bit32.bxor`,
`<<`→`bit32.lshift`, `>>>`→`bit32.rshift`, `>>`→`bit32.arshift`; same for the `=`-suffixed
compound forms (used via read-modify-write).
Order of resolution: simple map → plus → bitwise call → comma → assert-unreachable.

`+` / `+=` (L74–76) → `createBinaryAdd` (L44–56) — THE `..` vs `+` decision:
```ts
const leftIsString = isDefinitelyType(leftType, isStringType);    // CHECKER predicates
const rightIsString = isDefinitelyType(rightType, isStringType);
if (leftIsString || rightIsString) {
    return luau.binary(
        leftIsString ? left : luau.call(luau.globals.tostring, [left]),
        "..",
        rightIsString ? right : luau.call(luau.globals.tostring, [right]),
    );
} else {
    return luau.binary(left, "+", right);
}
```
If either side is *definitely* string → string concat with `tostring()` wrapped around any side not
definitely string. Otherwise numeric `+`. (TS type system guarantees `string|number + ...` is the
only ambiguity; "possibly string but not definitely" sides get tostring'd only when the other side
forces concat.)

Comma operator (L84–87): `state.prereqList(wrapExpressionStatement(left)); return right;`.

### 4.3 Assignment plumbing — `util/assignment.ts`, `nodes/transformWritable.ts`

`getSimpleAssignmentOperator(leftType, operatorKind, rightType)` (assignment.ts L23–34):
```ts
if (operatorKind === ts.SyntaxKind.PlusEqualsToken) {
    return isDefinitelyType(leftType, isStringType) || isDefinitelyType(rightType, isStringType) ? "..=" : "+=";
}
return COMPOUND_OPERATOR_MAP.get(operatorKind);
```
`COMPOUND_OPERATOR_MAP` (L7–21): `-=`→`-=`, `*=`→`*=`, `/=`→`/=`, `**=`→`^=`, `%=`→`%=`,
`++`→`+=`, `--`→`-=`, `=`→`=`. Returns `undefined` for everything else (bitwise compounds
`&= |= ^= <<= >>= >>>=`, and logical-assignment ops are routed earlier) → triggers the
read-modify-write fallback (`createCompoundAssignment*`: emit `writable = <binary(readable, op,
value)>` via `createBinaryFromOperator`, statement form L52–67, expression form L69–85).

`getAssignableValue(operator, value, valueType)` (`util/getAssignableValue.ts` L5–10):
when operator is `..=` and CHECKER: `!isDefinitelyType(valueType, isStringType)` → wrap value in
`tostring(value)`; else pass through. (Mirrors createBinaryAdd's coercion for the `+=` case.)

`transformWritableExpression(state, node, readAfterWrite)` (transformWritable.ts L13–41):
- `ts.isPrototypeAccess(node)` → `errors.noPrototype` (then continues).
- PropertyAccessExpression: transform `node.expression`; base becomes
  `pushToVarIfNonId(exp, "exp")` when `readAfterWrite` else `convertToIndexableExpression(exp)`;
  result `luau.property(base, node.name.text)`.
- ElementAccessExpression: `ensureTransformOrder(state, [node.expression, node.argumentExpression])`;
  `indexExp = addOneIfArrayType(state, state.getType(node.expression), index)` — CHECKER (L29);
  base stabilized same as above; when `readAfterWrite` also `pushToVarIfComplex(indexExp, "index")`;
  result `ComputedIndexExpression { expression: base, index }`.
- Else: `transformExpression(skipDownwards(node))`, assert `luau.isWritableExpression` (plain
  identifiers).

`transformWritableAssignment(state, writeNode, valueNode, readAfterWrite, readBeforeWrite)`
(L43–58):
```ts
const writable = transformWritableExpression(state, writeNode, readAfterWrite);
const [value, prereqs] = state.capture(() => transformExpression(state, valueNode));
const readable = !readBeforeWrite || luau.list.isEmpty(prereqs) ? writable : state.pushToVar(writable, "readable");
state.prereqList(prereqs);
return { writable, readable, value };
```
`readable` is a pre-RHS snapshot of the target used by compound fallback; only materialized into a
temp when the RHS had prereqs that could mutate the target.

`createAssignmentExpression` (assignment.ts L36–50): prereq `Assignment { left, operator, right }`,
return the writable as the expression's value.

### 4.4 transformLogical — `nodes/transformLogical.ts` L147–167 (with truthiness interplay)

`&&`:
```ts
return buildInlineConditionExpression(state, node, kind, "and",
    (conditionId, node) => createTruthinessChecks(state, conditionId, node));
```
`||`: same with luau op `"or"` and condition `luau.unary("not", createTruthinessChecks(...))`.
`??` (L156–164): condition builder is `conditionId == nil`. If
CHECKER: `!isPossiblyType(state.getType(node), isBooleanLiteralType(state, false))` (L158) → can
use the inline path (`a or b` is truthiness-correct because result can't be `false`); otherwise
NEVER inline: build chain with `enableInlining = false` and always materialize a temp
(`buildLogicalChainPrereqs`) — because Luau `or` would wrongly skip a legitimate `false`.

`flattenByOperator(node, operatorKind)` (L23–31): left-recursive flatten of same-operator chains:
`a && b && c` → `[a, b, c]`.

`getLogicalChain(state, binaryExp, binaryOperatorKind, enableInlining)` (L37–53): per item:
CHECKER: `state.getType(node)` (L44); capture transform; inline flag:
```ts
const willWrap = index < array.length - 1 && willCreateTruthinessChecks(type);
inline = luau.list.isEmpty(statements) && !willWrap;
```
(an item is inlineable only if it produced no prereqs AND — unless it's the last item — its
truthiness in Luau matches TS truthiness, i.e. no 0/NaN/"" possibility; the LAST item never needs
a wrap because its value is returned, not tested.)

`mergeInlineExpressions(chain, binaryOperator)` (L108–121): adjacent inline items merge into one
`binaryExpressionChain` item.

`buildInlineConditionExpression` (L126–145): after merging, if exactly one item remains and it's
inline → return it directly (pure `a and b and c`). Otherwise allocate
`conditionId = luau.tempId("condition")` and run:

`buildLogicalChainPrereqs(state, chain, conditionId, buildCondition, index = 0)` (L58–94),
recursive nesting:
```
[item0.statements]
local _condition = item0.expression            -- index 0; assignments thereafter
if buildCondition(_condition, item0.node) then
    [item1.statements]
    _condition = item1.expression
    if buildCondition(_condition, item1.node) then ... end
end
```
Result expression is `conditionId`.

---

## 5. Unary — `expressions/transformUnaryExpression.ts`

### 5.1 transformPostfixUnaryExpression (L13–40) — expression position
`validateNotAnyType(state, node.operand)`. Then:
```ts
const writable = transformWritableExpression(state, node.operand, true);
const origValue = luau.tempId("original");
prereq: local _original = <writable>
prereq: <writable> += 1   (or -= for MinusMinus)
return origValue;
```

### 5.2 transformPrefixUnaryExpression (L42–71)
`validateNotAnyType(state, node.operand)`.
- `++`/`--` (L45–55): `writable = transformWritableExpression(state, node.operand, true)`;
  prereq `writable += 1` / `-= 1`; return `writable` (the new value).
- Unary `+` (L56–58): `errors.noUnaryPlus`; still returns `transformExpression(operand)`.
- Unary `-` (L59–63): CHECKER: `!isDefinitelyType(state.getType(node.operand), isNumberType)`
  (L60) → `errors.noNonNumberUnaryMinus`; emits `luau.unary("-", exp)`.
- `!` (L64–66): `luau.unary("not", createTruthinessChecks(state, transformExpression(operand), operand))`.
- `~` (L67–68): `bit32.bnot(exp)`.

### 5.3 ++/-- as statements — `statements/transformExpressionStatement.ts` L13–24
When a Prefix/PostfixUnaryExpression with `++`/`--` is the whole expression statement
(`isUnaryAssignmentOperator`, typeGuards.ts L13–17), no temp is made:
`transformWritableExpression(state, node.operand, /*readAfterWrite*/ false)` then a single
`writable += 1` / `-= 1` Assignment statement.

---

## 6. Truthiness — `util/createTruthinessChecks.ts` (COMPLETE)

TS truthiness: `0`, `NaN`, `""`, `false`, `undefined`(nil) are falsy. Luau: only `false`/`nil`.
So conditions need extra guards when the static type admits 0, NaN, or "".

`willCreateTruthinessChecks(type)` (L9–15):
```ts
return isPossiblyType(type, isNumberLiteralType(0))
    || isPossiblyType(type, isNaNType)
    || isPossiblyType(type, isEmptyStringType);
```

`createTruthinessChecks(state, exp, node)` (L17–56):
```ts
const type = state.getType(node);                                    // CHECKER (L18)
const isAssignableToZero = isPossiblyType(type, isNumberLiteralType(0));
const isAssignableToNaN = isPossiblyType(type, isNaNType);
const isAssignableToEmptyString = isPossiblyType(type, isEmptyStringType);

if (isAssignableToZero || isAssignableToNaN || isAssignableToEmptyString) {
    exp = state.pushToVarIfComplex(exp, "value");    // exp evaluated once, reused in checks
}

const checks = new Array<luau.Expression>();
if (isAssignableToZero) checks.push(luau.binary(exp, "~=", luau.number(0)));
// workaround for https://github.com/microsoft/TypeScript/issues/32778
if (isAssignableToZero || isAssignableToNaN) checks.push(luau.binary(exp, "==", exp));   // NaN check
if (isAssignableToEmptyString) checks.push(luau.binary(exp, "~=", luau.string("")));
checks.push(exp);                                     // final: value itself (false/nil)
// logTruthyChanges diagnostics:
if (state.data.projectOptions.logTruthyChanges && (any of the three)) {
    const checkStrs = [];
    if (isAssignableToZero) checkStrs.push("0");
    if (isAssignableToZero || isAssignableToNaN) checkStrs.push("NaN");
    if (isAssignableToEmptyString) checkStrs.push('""');
    DiagnosticService.addDiagnostic(warnings.truthyChange(checkStrs.join(", "))(node));
}
return binaryExpressionChain(checks, "and");
```
Emission shapes: `x` (no checks) | `x ~= 0 and x == x and x` | `x == x and x` (NaN-only, i.e.
plain `number`) | `x ~= "" and x` | full `x ~= 0 and x == x and x ~= "" and x`. NOTE the
TS#32778 workaround: a type assignable to literal `0` also gets the NaN check (`x == x`) even if
`isNaNType` alone wouldn't fire. The `truthyChange` warning lists exactly which checks were added
("0", "NaN", `""`) and only when `projectOptions.logTruthyChanges` is on.

Predicate internals (see §7): `isNumberLiteralType(0)` matches literal-0 OR any non-literal
number-ish type; `isNaNType` = number-ish AND NOT a number literal; `isEmptyStringType` =
string literal `""`, or template literal type with all-empty texts, or any non-literal string-ish.

---

## 7. Type predicates — `util/types.ts` (port-grade)

`type TypeCheck = (type: ts.Type) => boolean` (L8).

### 7.1 Combinators

`getRecursiveBaseTypes(type: ts.InterfaceType)` (L10–23):
CHECKER: `type.getBaseTypes()` recursively (`baseType.isClassOrInterface()` to recurse).

`isDefinitelyType(type, ...callbacks)` (L38–40): operates on
CHECKER: `type.getConstraint() ?? type` (L39). Inner (L25–36):
```ts
if (type.isUnion()) return type.types.every(t => isDefinitelyTypeInner(t, callbacks));
else if (type.isIntersection()) return type.types.some(t => isDefinitelyTypeInner(t, callbacks));
else {
    if (type.isClassOrInterface() && getRecursiveBaseTypes(type).some(t => isDefinitelyTypeInner(t, callbacks)))
        return true;
    return callbacks.some(cb => cb(type));
}
```
union → EVERY member matches; intersection → SOME member; class/interface → also check base types.
CHECKER: `type.isUnion()`, `type.isIntersection()`, `type.types`, `type.isClassOrInterface()`.

`isPossiblyType(type, ...callbacks)` (L68–70), inner (L42–66):
```ts
if (type.isUnionOrIntersection()) return type.types.some(t => isPossiblyTypeInner(t, callbacks));
else {
    if (type.isClassOrInterface() && getRecursiveBaseTypes(type).some(...)) return true;
    // type variable without constraint, any, or unknown
    if (!!(type.flags & (ts.TypeFlags.TypeVariable | ts.TypeFlags.AnyOrUnknown))) return true;
    // defined type
    if (isDefinedType(type)) {
        if (callbacks.length === 1 && callbacks[0] === isUndefinedType) return false;
        return true;
    }
    return callbacks.some(cb => cb(type));
}
```
CHECKER flags tested: `ts.TypeFlags.TypeVariable | ts.TypeFlags.AnyOrUnknown` (unconstrained
generics/any/unknown are "possibly anything"). `isDefinedType` (L72–81) detects the rbxts
`defined` type (`{}`-like Object with nothing on it):
```ts
type.flags === ts.TypeFlags.Object && type.getProperties().length === 0
    && type.getCallSignatures().length === 0 && type.getConstructSignatures().length === 0
    && type.getNumberIndexType() === undefined && type.getStringIndexType() === undefined
```
CHECKER: exact-equality `type.flags === ts.TypeFlags.Object`, `getProperties`, `getCallSignatures`,
`getConstructSignatures`, `getNumberIndexType`, `getStringIndexType`. `defined` counts as
"possibly X" for every X except a pure `isUndefinedType` query.

### 7.2 Specific predicates the first wave needs

- `isAnyType(state)` (L83–85): `type === state.typeChecker.getAnyType()`.
  CHECKER: `typeChecker.getAnyType()` (intrinsic identity comparison).
- `isBooleanType` (L87–89): `type.flags & (ts.TypeFlags.Boolean | ts.TypeFlags.BooleanLiteral)`.
- `isBooleanLiteralType(state, value)` (L91–99): if `flags & BooleanLiteral`, compare identity
  against CHECKER: `typeChecker.getTrueType()` / `getFalseType()`; else fall back to
  `isBooleanType` (so plain `boolean` counts as possibly-false).
- `isNumberType` (L101–103): `flags & (Number | NumberLike | NumberLiteral)`.
- `isNumberLiteralType(value)` (L105–112): CHECKER `type.isNumberLiteral()` → compare
  `type.value === value`; non-literal falls back to `isNumberType`.
- `isNaNType` (L114–116): `isNumberType(type) && !type.isNumberLiteral()`.
- `isStringType` (L118–120): `flags & (String | StringLike | StringLiteral)`.
- `isEmptyStringType` (L189–197): CHECKER `type.isStringLiteral()` → `value === ""`;
  template-literal types (`isTemplateLiteralType` typeGuards.ts L19–21: `"texts" in type &&
  "types" in type && flags & ts.TypeFlags.TemplateLiteral`) → `texts.length === 0 ||
  texts.every(v => v.length === 0)`; else `isStringType`.
- `isArrayType(state)` (L122–137):
  ```ts
  if (!!(type.flags & ts.TypeFlags.Any)) return false;   // isArrayLikeType returns true for any
  return state.typeChecker.isTupleType(type) || state.typeChecker.isArrayLikeType(type)
      || type.symbol === macroManager.getSymbolOrThrow(SYMBOL_NAMES.ReadonlyArray)
      || ... Array | ReadVoxelsArray | TemplateStringsArray;
  ```
  CHECKER: `typeChecker.isTupleType`, `typeChecker.isArrayLikeType`, `type.symbol` identity vs
  ambient @rbxts symbols (port: resolve these global interface symbols once at startup).
- `isObjectType` (L181–183): `flags & ts.TypeFlags.Object`.
- `isUndefinedType` (L185–187): `flags & (ts.TypeFlags.Undefined | ts.TypeFlags.Void)`.
- `isLuaTupleType(state)` (L161–165): CHECKER `type.getProperty(NOMINAL_LUA_TUPLE_NAME)` identity
  vs the macroManager's nominal `_nominal_LuaTuple` symbol.
- `isRobloxType(state)` (L199–206): `type.symbol?.declarations?.some(d => d.getSourceFile()
  .fileName under node_modules/@rbxts/types)`.
- `walkTypes(type, callback)` (L210–224): recurse into union/intersection members; for others,
  follow CHECKER: `type.getConstraint()` when it exists AND `constraint !== type` ("in template
  literal types, constraint === type and this causes infinite recursion"); leaf → callback.
- `getFirstDefinedSymbol(state, type)` (L244–254): union/intersection → first member whose
  `t.symbol` exists and CHECKER: `!typeChecker.isUndefinedSymbol(t.symbol)`; else `type.symbol`.
- `getFirstConstructSymbol(state, expression)` (L226–242): see §3.2 step 5.
- `getTypeArguments(state, type)` (L256–258): CHECKER `typeChecker.getTypeArguments(type as
  ts.TypeReference) ?? []`.

tsgo mapping inventory of TypeFlags used: `Object`, `Any`, `AnyOrUnknown`, `TypeVariable`,
`Boolean`, `BooleanLiteral`, `Number`, `NumberLike`, `NumberLiteral`, `String`, `StringLike`,
`StringLiteral`, `Undefined`, `Void`, `TemplateLiteral`. Plus NodeFlags: `Let`, `Const`,
`Namespace`; ModifierFlags: `Async`; SignatureKind: `Call`; IndexKind: `Number`.

---

## 8. Calls — `expressions/transformCallExpression.ts`

`transformCallExpression` (L282–284) = `transformOptionalChain(state, node)` — ALL calls and
accesses are routed through the optional-chain flattener, which for fully non-optional chains
degenerates to sequential `transformChainItem` calls (see §9.3). The three "Inner" functions are
the real transforms.

### 8.1 transformCallExpressionInner (L115–155) — plain `f(...)`

```ts
if (ts.isImportCall(node)) return transformImportExpression(state, node);   // dynamic import; later
validateNotAnyType(state, node.expression);                                  // a in a()
if (ts.isSuperCall(node)) { ... super.constructor(self, args) ... }          // classes; later
const expType = state.typeChecker.getNonOptionalType(state.getType(node.expression));  // CHECKER (L135)
const symbol = getFirstDefinedSymbol(state, expType);
if (symbol) {
    const macro = state.services.macroManager.getCallMacro(symbol);
    if (macro) return runCallMacro(macro, state, node, expression, nodeArguments);   // PORT: error here
}
const [args, prereqs] = state.capture(() => ensureTransformOrder(state, nodeArguments));
fixVoidArgumentsForRobloxFunctions(state, expType, args, nodeArguments);
if (!luau.list.isEmpty(prereqs) && expressionMightMutate(state, expression, node.expression)) {
    expression = state.pushToVar(expression, "fn");
}
state.prereqList(prereqs);
const exp = luau.call(convertToIndexableExpression(expression), args);
return wrapReturnIfLuaTuple(state, node, exp);
```
Key ordering rule: callee expression was already transformed by the chain walker BEFORE the args;
if arg transformation produced prereqs and the callee expression could be affected
(`expressionMightMutate`), snapshot callee to `local _fn = ...` first.

Macro dispatch hook = `macroManager.getCallMacro(symbol)` (L138); for the port's first wave, emit
a clean "macros not yet supported" error at this exact point (likewise property-call macro hooks
below).

`fixVoidArgumentsForRobloxFunctions` (L96–113): if
CHECKER: `isPossiblyType(expType, isRobloxType(state))` (L102), then for each argument that is a
CallExpression with CHECKER: `isPossiblyType(state.getType(nodeArg), isUndefinedType)` (L106),
wrap the rendered arg in parentheses — `(foo())` truncates Lua multi-returns/void so C functions
like `tonumber()` don't error on zero values.

### 8.2 transformPropertyCallExpressionInner (L157–215) — `a.b(...)`

`validateNotAnyType` on `expression.expression` (a) AND `node.expression` (a.b) (L166–168).
Super-property call branch (L170–175; later). Macro: CHECKER:
`typeChecker.getNonOptionalType(state.getType(node.expression))` (L177) →
`getFirstDefinedSymbol` → `macroManager.getPropertyCallMacro(symbol)` (L180) → `runCallMacro`.
Then args via captured `ensureTransformOrder`, `fixVoidArgumentsForRobloxFunctions`, and the same
mutate-snapshot of `baseExpression` (L189–192). The METHOD decision (L194–212):
```ts
if (isMethod(state, expression)) {
    if (luau.isValidIdentifier(name)) {
        exp = MethodCallExpression { name, expression: convertToIndexableExpression(baseExpression), args };  // a:b(...)
    } else {
        baseExpression = state.pushToVarIfComplex(baseExpression);
        args.unshift(baseExpression);
        exp = luau.call(luau.property(convertToIndexableExpression(baseExpression), name), args);  // _a["if"](_a, ...)
    }
} else {
    exp = luau.call(luau.property(convertToIndexableExpression(baseExpression), name), args);      // a.b(...)
}
return wrapReturnIfLuaTuple(state, node, exp);
```

### 8.3 transformElementCallExpressionInner (L217–280) — `a[b](...)`

`validateNotAnyType` ×3: `expression.expression` (a), `expression.argumentExpression` (b),
`node.expression` (a[b]) (L226–230). Super branch (L232–240; later). PropertyCall macro check
identical to §8.2 (CHECKER `getNonOptionalType` L242). Then NOTE the index expression is ordered
WITH the args:
```ts
const [[argumentExp, ...args], prereqs] = state.capture(() =>
    ensureTransformOrder(state, [argumentExpression, ...nodeArguments]));
fixVoidArgumentsForRobloxFunctions(state, expType, args, nodeArguments);
if (!luau.list.isEmpty(prereqs) && expressionMightMutate(state, baseExpression, expression.expression))
    baseExpression = state.pushToVar(baseExpression);
state.prereqList(prereqs);
if (isMethod(state, expression)) {
    baseExpression = state.pushToVarIfComplex(baseExpression);
    args.unshift(baseExpression);    // a[b] can never use `:` sugar; always explicit self
}
const exp = luau.call(
    ComputedIndexExpression {
        expression: convertToIndexableExpression(baseExpression),
        index: addOneIfArrayType(state,
            state.typeChecker.getNonOptionalType(state.getType(expression.expression)),  // CHECKER (L272)
            argumentExp),
    }, args);
return wrapReturnIfLuaTuple(state, node, exp);
```

### 8.4 runCallMacro (L21–83) — macro path, documented for the later port
Captures arg transform; spread-last-arg handling: CHECKER:
`typeChecker.getSignaturesOfType(state.getType(node.expression), ts.SignatureKind.Call)[0]` (L33),
last parameter `dotDotDotToken` → `errors.noVarArgsMacroSpread`; otherwise CHECKER:
`state.getType(lastArg.expression)` must satisfy `typeChecker.isTupleType` (assert, L47) and the
tuple arity (`(tupleArgType as ts.TupleTypeReference).target.elementFlags.length`) spawns temp ids
bound by one `local _spread0, _spread1 = <spread>` declaration. Then every arg that
`expressionMightMutate` gets pushToVar; the macro target expression likewise when prereqs exist;
finally `wrapReturnIfLuaTuple(state, node, macro(...))`.

### 8.5 isMethod — `util/isMethod.ts` (FULL)

`isMethod(state, node)` (L93–98) = `isMethodFromType(state, node, state.getType(node))`. CHECKER.

`isMethodFromType` (L79–91): `walkTypes(type, t => { if (t.symbol) result ||=
getOrSetDefault(state.multiTransformState.isMethodCache, t.symbol, () => isMethodInner(...)) })` —
cached PER SYMBOL across the whole program (multiTransformState).

`isMethodInner(state, node, type)` (L51–77):
```ts
for (const callSignature of type.getCallSignatures()) {            // CHECKER (L55)
    const thisValueDeclaration = callSignature.thisParameter?.valueDeclaration;   // CHECKER
    if (thisValueDeclaration) {
        if (!(state.getType(thisValueDeclaration).flags & ts.TypeFlags.Void))     // CHECKER (L58)
            hasMethodDefinition = true;
        else hasCallbackDefinition = true;
    } else if (callSignature.declaration) {
        if (isMethodDeclaration(state, callSignature.declaration)) hasMethodDefinition = true;
        else hasCallbackDefinition = true;
    }
}
if (hasMethodDefinition && hasCallbackDefinition) errors.noMixedTypeCall(node);
return hasMethodDefinition;
```
A signature with an explicit `this` parameter typed `void` is a callback; any other `this` type
makes it a method.

`isMethodDeclaration(state, node)` (L19–49), for signatures without a thisParameter symbol:
- non-FunctionLike → false.
- explicit `this` identifier first param (`getThisParameter`, L9–17) →
  `!(state.getType(thisParam).flags & ts.TypeFlags.Void)` — CHECKER (L23).
- FunctionDeclaration → false ("namespace declare functions with `this` arg defined (i.e. utf8)").
- MethodDeclaration | MethodSignature → true.
- FunctionExpression directly assigned in an ObjectLiteralExpression PropertyAssignment → true
  ("for some reason, FunctionExpressions within ObjectLiteralExpressions are implicitly methods").
- else false.

### 8.6 wrapReturnIfLuaTuple — `util/wrapReturnIfLuaTuple.ts` (the LuaTuple check)

(L58–63):
```ts
if (isLuaTupleType(state)(state.getType(node)) && shouldWrapLuaTuple(state, node, exp))
    return luau.array([exp]);
return exp;
```
CHECKER: `state.getType(node)` + nominal-property LuaTuple test (§7.2). A multi-return call typed
`LuaTuple<T>` is packed `{ f() }` UNLESS the syntactic context consumes the multiple values.
`shouldWrapLuaTuple` (L8–56): wrap (true) if `exp` isn't a call; else with
`child = skipUpwards(node), parent = child.parent`, DON'T wrap when parent is: ExpressionStatement;
ForStatement with `parent.condition !== child`; VariableDeclaration whose `.name` is an
ArrayBindingPattern without hoists; assignment whose LHS is an ArrayLiteralExpression;
ElementAccessExpression (handled by select(), §9.2); ReturnStatement; VoidExpression. Otherwise wrap.

---

## 9. Access

### 9.1 transformPropertyAccessExpression — `expressions/transformPropertyAccessExpression.ts`

Entry (L36–43): `getConstantValueLiteral(state, node)` first —
`util/getConstantValueLiteral.ts` L5–17, CHECKER: `typeChecker.getConstantValue(node)`; const-enum
member accesses fold to `luau.string`/`luau.number`. Else `transformOptionalChain(state, node)`.

`transformPropertyAccessExpressionInner(state, node, expression, name)` (L11–34):
```ts
validateNotAnyType(state, node.expression);                                   // a in a.b
addIndexDiagnostics(state, node, state.typeChecker.getNonOptionalType(state.getType(node)));  // CHECKER (L20)
if (ts.isDeleteExpression(skipUpwards(node).parent)) {
    prereq: <exp>.<name> = nil; return luau.none();
}
return luau.property(convertToIndexableExpression(expression), name);
```
NOTE: `addIndexDiagnostics` receives the type of THE ACCESS ITSELF (`state.getType(node)`), used
for method-indexing errors. `util/addIndexDiagnostics.ts` L10–26:
```ts
const symbol = getFirstDefinedSymbol(state, expType);
if ((symbol && macroManager.getPropertyCallMacro(symbol)) ||
    (!isValidMethodIndexWithoutCall(state, skipUpwards(node)) && isMethod(state, node)))
    errors.noIndexWithoutCall(node);
if (ts.isPrototypeAccess(node)) errors.noPrototype(node);
```
`util/isValidMethodIndexWithoutCall.ts` L6–35: indexing a method WITHOUT calling is allowed when
parent is a BinaryExpression (`a.b !== undefined`), a PrefixUnaryExpression (`!a.b`), or the arg of
the `typeIs`/`typeOf` call macros (CHECKER: `typeChecker.getNonOptionalType(state.getType(
parent.expression))` L20 + `getFirstDefinedSymbol` + `getCallMacro`).

### 9.2 transformElementAccessExpression — `expressions/transformElementAccessExpression.ts`

Entry (L71–78): constant-value fold then `transformOptionalChain`.

`transformElementAccessExpressionInner(state, node, expression, argumentExpression)` (L15–69):
```ts
validateNotAnyType(state, node.expression);            // a in a[b]
validateNotAnyType(state, node.argumentExpression);    // b in a[b]
const expType = state.typeChecker.getNonOptionalType(state.getType(node.expression));  // CHECKER (L26)
addIndexDiagnostics(state, node, expType);     // NOTE: object type here, unlike property access
const [index, prereqs] = state.capture(() => transformExpression(state, argumentExpression));
if (!luau.list.isEmpty(prereqs)) {
    // hack because wrapReturnIfLuaTuple will not wrap this, but now we need to!
    if (isLuaTupleType(state)(expType)) expression = luau.array([expression]);
    expression = state.pushToVar(expression, "exp");
    state.prereqList(prereqs);
}
// LuaTuple<T> checks
if (luau.isCall(expression) && isLuaTupleType(state)(expType)) {
    if (!luau.isNumberLiteral(index) || Number(index.value) !== 0)
        expression = luau.call(luau.globals.select, [offset(index, 1), expression]);
    return ParenthesizedExpression { expression };   // parentheses trim to one value
}
if (ts.isDeleteExpression(skipUpwards(node).parent)) {
    prereq: (exp)[addOneIfArrayType(state, expType, index)] = nil; return luau.none();
}
return ComputedIndexExpression {
    expression: convertToIndexableExpression(expression),
    index: addOneIfArrayType(state, expType, index),
};
```
LuaTuple indexing of a direct call: `f()[0]` → `(f())`; `f()[n]` → `(select(n + 1, f()))`.

THE +1 ARRAY OFFSET — `util/addOneIfArrayType.ts` L7–13 (precise):
```ts
export function addOneIfArrayType(state: TransformState, type: ts.Type, expression: luau.Expression) {
    if (isDefinitelyType(type, isArrayType(state), isUndefinedType)) {
        return offset(expression, 1);
    } else {
        return expression;
    }
}
```
Conditions: the OBJECT type (already `getNonOptionalType`-stripped at all three call sites:
element access read/write L26, element call callee L272, writable element access
transformWritable.ts L29 — the writable site passes raw `state.getType(node.expression)` WITHOUT
getNonOptionalType but combines with `isUndefinedType` in the predicate list) must be DEFINITELY
(array-or-undefined) per `isDefinitelyType` union/intersection rules. `isArrayType` per §7.2
(tuple, array-like, or the @rbxts Array/ReadonlyArray/ReadVoxelsArray/TemplateStringsArray
symbols; `any` excluded). The `+1` goes through `offset()` so literal indices fold (`a[0]` →
`a[1]`) and `a[i - 1]` → `a[i]`. Maps/objects/LuaTuples get NO offset.

### 9.3 Optional chaining entry — `nodes/transformOptionalChain.ts`

`transformOptionalChain(state, node)` (L350–356): `flattenOptionalChain` (L135–162) walks down
`.expression` collecting `ChainItem`s (PropertyAccess / ElementAccess / Call / PropertyCall /
ElementCall — a CallExpression whose `skipDownwards(expression)` is a property/element access
becomes a compound item, L144–156; each item records `optional: node.questionDotToken !==
undefined` and CHECKER `state.getType(...)` snapshots, L67–131). Then
`transformOptionalChainInner(state, chain, transformExpression(state, expression))`.

NON-OPTIONAL PATH (all first-wave code): every item has `optional === false` (and compound items
`callOptional === false`), so the recursion takes the else branch (L339–347):
`transformChainItem(state, baseExpression, item)` (L164–190) dispatches to the matching Inner
function (§8.1–8.3, §9.1–9.2) and recurses with `index + 1`. Net effect: inner-to-outer
left-fold, base expression threaded through. A port that doesn't support `?.` yet can implement
exactly this fold and raise "not yet supported" if any `questionDotToken` is present.

Optional path summary (for later): builds nested `if tempId ~= nil then ... end` blocks
(`createNilCheck` L219–225) around per-link evaluation with a shared temp (`createOrSetTempId`
L192–217, temp named from an enclosing variable declaration or "result"); method calls with `?.()`
hoist self (`local _self = base`); macro + `?.()` → `errors.noOptionalMacroCall` (L285);
`isUsedAsStatement` drops the result value (L305); CHECKER:
`typeChecker.getNonOptionalType(state.getType(item.node.expression))` (L280).

---

## 10. Statements

### 10.1 transformVariableStatement — `statements/transformVariableStatement.ts`

`transformVariableStatement` (L191–196) → `transformVariableDeclarationList` (L173–189):
- `isVarDeclaration(node)` (L169–171): `!(flags & Const) && !(flags & Let)` → `errors.noVar`.
- Per declaration: `const [variableStatements, prereqs] = state.capture(() =>
  transformVariableDeclaration(state, declaration)); pushList(prereqs); pushList(variableStatements);`

`transformVariableDeclaration` (L101–167):
```ts
let value: luau.Expression | undefined;
if (node.initializer) {
    // must transform right _before_ checking isHoisted, that way references inside of value can be hoisted
    pushList(statements, state.capturePrereqs(() => (value = transformExpression(state, node.initializer!))));
}
const name = node.name;
if (ts.isIdentifier(name)) {
    pushList(statements, state.capturePrereqs(() => transformVariable(state, name, value)));
} else { /* binding patterns — entry for later, see below */ }
```

`transformVariable(state, identifier, right?)` (L19–55) — the identifier-binding core:
```ts
validateIdentifier(state, identifier);
const symbol = state.typeChecker.getSymbolAtLocation(identifier);   // CHECKER (L22)
assert(symbol);
// export let
if (isSymbolMutable(state, symbol)) {
    const exportAccess = state.getModuleIdPropertyAccess(symbol);
    if (exportAccess) {
        if (right) prereq: exportAccess = right;
        return exportAccess;
    }
}
const left = transformIdentifierDefined(state, identifier);
checkVariableHoist(state, identifier, symbol);    // switch-case hoisting, §10.1.1
if (state.isHoisted.get(symbol) === true) {
    // no need to do `x = nil` if the variable is already created
    if (right) prereq: Assignment { left, "=", right };
} else {
    prereq: VariableDeclaration { left, right };   // local x = right  (or bare `local x`)
}
return left;
```

10.1.1 `checkVariableHoist` — `util/checkVariableHoist.ts` L6–39: only applies when the declaring
statement sits directly in a `ts.CaseClause`; uses CHECKER:
`ts.FindAllReferences.Core.eachSymbolReferenceInFile(node, typeChecker, sourceFile, cb, caseBlock)`
to detect references outside the clause; if found, records the hoist against the case block and
sets `isHoisted`. (Switch is out of first-wave scope; port can stub to no-op until then.)

Destructuring entry points (later): ArrayBindingPattern with LuaTuple call RHS →
`transformOptimizedArrayBindingPattern` (L57–99; CHECKER `isLuaTupleType(state)(state.getType(
node.initializer))` L137; upstream rest-spread rejection L70); inline-array RHS unpacks members
directly (L142–147); generic path pushes RHS to `binding` temp and runs
`transformArrayBindingPattern`/`transformObjectBindingPattern`. Empty patterns: drop or keep RHS
via `wrapExpressionStatement` (L127–131).

### 10.2 transformExpressionStatement — `statements/transformExpressionStatement.ts` L86–89

`expression = skipDownwards(node.expression)` then `transformExpressionStatementInner` (L26–84):
- BinaryExpression:
  - logical-assignment (`&&= ||= ??=`) → dedicated statement transform (later).
  - assignment operator with non-destructuring LHS (L34–75): statement-form twin of §4.1 step 4:
    CHECKER `state.getType(expression.left/right)` (L39–40); `getSimpleAssignmentOperator`;
    `transformWritableAssignment(state, left, right, /*readAfterWrite*/ operator === undefined,
    /*readBeforeWrite*/ operator === undefined)` — note BOTH flags false for simple ops (no
    re-read needed in statement position; compare expression form which passes `true, op===undef`).
    Simple → one `Assignment { left: writable, operator, right: getAssignableValue(...) }`.
    Compound fallback → `createCompoundAssignmentStatement` (read-modify-write
    `writable = binary(readable, op, value)`).
- ++/-- statement → §5.3.
- Everything else: `wrapExpressionStatement(transformExpression(state, expression))` — discard
  semantics per §0.3 (temp/none dropped; call kept; other values bound to `local _ =`).

### 10.3 transformIfStatement — `statements/transformIfStatement.ts` L9–42

```ts
const condition = createTruthinessChecks(state, transformExpression(state, node.expression), node.expression);
const statements = transformStatementList(state, node.thenStatement, getStatements(node.thenStatement));
```
(condition prereqs flow into the CURRENT outer prereq capture — if-statements don't loop, so no
special handling.) Else handling (L16–31): none → empty list; `else if` → recursively
`state.capture(transformIfStatementInner)`; when the nested elseif produced prereqs they cannot
live in an `elseif` clause, so the elseBody becomes a statement list `[...prereqs, IfStatement]`
(rendering as `else` + nested `if`), otherwise the IfStatement is attached directly (renders as
`elseif`). Else-block → `transformStatementList`. Returns one `luau.IfStatement
{ condition, statements, elseBody }`.

### 10.4 transformWhileStatement — `statements/transformWhileStatement.ts` L9–37

```ts
let [conditionExp, conditionPrereqs] = state.capture(() =>
    createTruthinessChecks(state, transformExpression(state, node.expression), node.expression));
if (!luau.list.isEmpty(conditionPrereqs)) {
    // condition must be re-evaluated every iteration → move into loop body:
    whileStatements = [ ...conditionPrereqs,
        IfStatement { condition: not conditionExp, statements: [BreakStatement], elseBody: [] } ];
    conditionExp = luau.bool(true);
}
push transformStatementList(body);
return [ WhileStatement { condition: conditionExp, statements: whileStatements } ];
```

### 10.5 transformDoStatement — `statements/transformDoStatement.ts` L9–37 (do/while)

Body first: `transformStatementList`. Inversion micro-opt (L12–16): if the TS condition is
`!expr`, strip the `!` and remember `conditionIsInvertedInLuau = false`. Capture condition +
truthiness prereqs. Emit:
```
repeat
    do            -- body wrapped in DoStatement for correct local scoping vs condition
        [body]
    end
    [conditionPrereqs]
until <not condition | condition>
```
(`until not cond` normally, since `repeat..until` exits when true while do/while continues when
true; with the stripped-`!` case the inversion cancels.)

### 10.6 transformForStatement — `statements/transformForStatement.ts` (C-style)

`transformForStatement` (L491–499): if `state.data.projectOptions.optimizedLoops` (default on) try
`transformForStatementOptimized`; else/fallback `transformForStatementFallback`.

10.6.1 Fallback (L102–297) — fully general while-loop desugaring:
- Initializer (L126–209):
  - VariableDeclarationList: `noVar` check. CLOSURE-CAPTURE MACHINERY (the subtle part): for each
    declared identifier (`getDeclaredVariables`), compute
    `isIdWriteOrAsyncRead` (L78–100): CHECKER:
    `ts.FindAllReferences.Core.eachSymbolReferenceInFile(id, typeChecker, file, cb, forStatement)`
    — true if the variable is written anywhere in the loop except the incrementor, OR read from
    inside a nested function ("async read"); and `canSkipClone` (L73–76): CHECKER:
    `!ts.FindAllReferences.Core.isSymbolReferencedInFile(id, typeChecker, file, initializer)` —
    whether the initializer expression itself references the symbol.
    For affected symbols, `state.symbolToIdMap` temporarily maps the symbol to a temp
    (`_i` or `_iCopy`) while transforming the declarations (L132–143 →
    `transformVariableDeclaration` per declaration L145–157), then (L159–203): when a clone is
    needed emit `local _i = _iCopy` in the outer scope; remove the map entry; per iteration emit
    `local i = _i` at loop top (whileStatements) and `_i = i` as a FINALIZER. This reproduces JS
    per-iteration `let` capture semantics.
  - Expression initializer: `transformExpressionStatementInner` with captured prereqs (L204–208).
- Incrementor (L211–247): guarded so it runs at the TOP of every iteration EXCEPT the first:
  ```
  local _shouldIncrement = false
  while ... do
      if _shouldIncrement then [incrementor stmts] else _shouldIncrement = true end
      ...
  ```
  (this makes `continue` still trigger the increment, since Luau `continue` jumps to loop top).
- Condition (L249–274): captured `createTruthinessChecks` (or `luau.bool(true)` when absent). If
  ANY whileStatements precede the body (incrementor guard, per-iteration locals, or condition
  prereqs), the condition must be evaluated after them, so when a condition exists emit
  `if not cond then break end` after the prereqs and set the while condition to `true`.
- Body: `transformStatementList` (L276).
- Finalizers (L278–284): `addFinalizers` (L35–71) walks the emitted Luau statement list and CLONES
  the finalizer statements (`_i = i` writebacks) immediately BEFORE every `ContinueStatement`
  (recursing into Do/If bodies, L60–66); then unless the list already ends in a final statement,
  appends finalizers at the end.
- Assembly (L286–296): `[initializer stmts..., WhileStatement]`; if more than one statement, wrap
  everything in `do ... end` (scopes the loop variable copies).

10.6.2 Optimized numeric-for (L392–489), gated by `optimizedLoops`, converts
`for (let i = a; i < b; i += s)` (and <=, >, >= variants) into Luau `for i = a, b±1, s do`:
requirements — single identifier declaration with initializer that `isProbablyInteger` (L368–390:
integer NumericLiteral; `+ - * **` of such; unary ±; `.size()` macro call (`isSizeMacro` L334–346,
CHECKER `getNonOptionalType`/`getFirstDefinedSymbol`/`getPropertyCallMacro`); or CHECKER:
`isDefinitelyType(state.getType(exp), t => t.isNumberLiteral() && Number.isInteger(t.value))`);
incrementor step extracted by `getOptimizedIncrementorStepValue` (L299–332; `i += intLit`,
`i -= intLit`, `i++`, `i--`; CHECKER `getSymbolAtLocation` to match the loop symbol L303/319/326);
condition operator direction must match step sign; condition RHS `isProbablyInteger`; loop var not
mutated in body (`isMutatedInBody` L348–366, CHECKER FindAllReferences). Emits start/end captured
transforms, `<` → `offset(end, -1)`, `>` → `offset(end, +1)`, `NumericForStatement
{ id, start, end, step, statements }`.

### 10.7 transformBlock — `statements/transformBlock.ts` L6–12
Free-standing `{ ... }` → `DoStatement { statements: transformStatementList(state, node,
node.statements) }` (preserves scoping).

### 10.8 transformReturnStatement — `statements/transformReturnStatement.ts`

`transformReturnStatement` (L71–84): no expression → (try-block special: `return TS.TRY_RETURN,
{}` when `isReturnBlockedByTryStatement`; out of scope) else `return nil` — NOTE: explicit
`ReturnStatement { expression: luau.nil() }`, NOT a bare `return`, preserving JS `undefined`.
With expression → `transformReturnStatementInner` (L28–69):
- `$tuple(...)` macro returns (L36–39; later): args become a multi-value return list.
- Normal: `expression = transformExpression(state, skipDownwards(returnExp));` then the LuaTuple
  check (L42–48):
  ```ts
  if (isLuaTupleType(state)(state.getType(returnExp)) && !isTupleReturningCall(state, returnExp, expression)) {
      if (luau.isArray(expression)) expression = expression.members;   // return a, b, c
      else expression = luau.call(luau.globals.unpack, [expression]);  // return unpack(exp)
  }
  ```
  `isTupleReturningCall` (L10–16): `luau.isCall(luaExpression) && isLuaTupleType(state)(
  state.typeChecker.getTypeAtLocation(skipDownwards(tsExpression)))` — CHECKER (L14), deliberately
  NOT `state.getType` ("intentionally NOT using state.getType() here, because that uses
  skipUpwards"). Meaning: returning a call that itself yields a LuaTuple passes through unchanged
  (multi-returns propagate); returning a LuaTuple VALUE (array literal/variable) must be unpacked.
- Try-block wrapping (L51–63, later): `return TS.TRY_RETURN, { <values> }`.

### 10.9 transformBreakStatement — `statements/transformBreakStatement.ts` L8–25
Label → `errors.noLabeledStatement`, empty list. **ROTOR DIVERGENCE:** labels are a
rotor extension (transformer/labeledstatement.go); only a label crossing a try is
rejected, as `noLabeledStatementsWithinTryCatch`. `isBreakBlockedByTryStatement(node)`
(`util/isBlockedByTryStatement.ts` L11–18: nearest ancestor among
TryStatement|IterationStatement|SwitchStatement is a Try) → `return TS.TRY_BREAK` (later).
Else `BreakStatement {}`.

### 10.10 transformContinueStatement — `statements/transformContinueStatement.ts` L8–25
Identical shape: label → `noLabeledStatement` (same ROTOR DIVERGENCE as 10.9);
try-blocked → `return TS.TRY_CONTINUE`;
else `ContinueStatement {}` (Luau has native `continue`).

### 10.11 transformThrowStatement — `statements/transformThrowStatement.ts` L6–16
`throw x` → `CallStatement { error(<x>) }`; bare rethrow `throw;` (no expression) → `error()`.
No type checks, no validateNotAny.

---

## 11. Diagnostics

### 11.1 Infrastructure — `Shared/diagnostics.ts`
Factories are created at module load with sequential `id` (L20, L54); category Error or Warning;
messages joined with `\n`; `suggestion(text)` prefixes "Suggestion: " (L16–18); some carry a
GitHub `issue(n)` link. `getDiagnosticId` (L85–88) reads the id back. Port: stable enumeration of
ids matters only for the `--allowedDiagnostics`-style filtering and tests; preserve names+messages.

### 11.2 validateNotAnyType — `util/validateNotAny.ts` L9–36 (FULL)
```ts
if (ts.isSpreadElement(node)) node = skipDownwards(node.expression);
let type = state.getType(node);                                    // CHECKER (L14)
if (isDefinitelyType(type, isArrayType(state))) {
    // Array<T> -> T
    const indexType = state.typeChecker.getIndexTypeOfType(type, ts.IndexKind.Number);  // CHECKER (L18)
    if (indexType) type = indexType;
}
if (isDefinitelyType(type, isAnyType(state))) {
    // given a type like `a: { [index: string]: any }`, `a["b"]` will not have a symbol
    const symbol = getOriginalSymbolOfNode(state.typeChecker, node);
    // CHECKER (L26): getSymbolAtLocation + ts.skipAlias (util/getOriginalSymbolOfNode.ts L3-9)
    if (symbol) {
        if (!state.multiTransformState.isReportedByNoAnyCache.has(symbol)) {
            state.multiTransformState.isReportedByNoAnyCache.add(symbol);
            errors.noAny(node);
        }
    } else errors.noAny(node);
}
```
Arrays of any (`any[]`) are reported via their element type; the per-symbol cache dedupes repeat
reports for the same variable; symbol-less any accesses always report.

### 11.3 Diagnostics raisable by first-wave transforms (name → message)

| Name | Message (joined lines) | Raised at |
|---|---|---|
| noBigInt | "BigInt literals are not supported!" | transformExpression dispatch |
| noNullLiteral | "`null` is not supported!" + sugg. "Use `undefined` instead." | dispatch |
| noPrivateIdentifier | "Private identifiers are not supported!" | dispatch; object literal name |
| noRegex | "Regular expressions are not supported!" | dispatch |
| noTypeOfExpression | "`typeof` operator is not supported!" + sugg. "Use `typeIs(value, type)` or `typeOf(value)` instead." | dispatch |
| noForInStatement | "for-in loop statements are not supported!" | statement dispatch |
| noLabeledStatement | "labels are not supported!" | statement dispatch; break/continue label — NOT ported (rotor extension: loop labels compile; see 10.9) |
| noDebuggerStatement | "`debugger` is not supported!" | statement dispatch |
| noAny | "Using values of type `any` is not supported!" + sugg. "Use `unknown` instead." | validateNotAnyType (binary, unary, calls, access) |
| noVar | "`var` keyword is not supported!" + sugg. "Use `let` or `const` instead." | variable statement; for initializer |
| noGetterSetter | "Getters and Setters are not supported!" + issue(457) | object literal accessor |
| noEqualsEquals | "operator `==` is not supported!" + sugg. "Use `===` instead." | binary |
| noExclamationEquals | "operator `!=` is not supported!" + sugg. "Use `!==` instead." | binary |
| noLuaTupleDestructureAssignmentExpression | "Cannot destructure LuaTuple<T> expression outside of an ExpressionStatement!" | binary assignment destructure |
| noGlobalThis | "`globalThis` is not supported!" | identifier |
| noArguments | "`arguments` is not supported!" | identifier |
| noPrototype | "`prototype` is not supported!" | addIndexDiagnostics; transformWritableExpression |
| noRobloxSymbolInstanceof | "The `instanceof` operator can only be used on roblox-ts classes!" + sugg. 'Use `typeIs(myThing, "TypeToCheck") instead' | binary instanceof |
| noNonNumberStringRelationOperator | "Relation operators can only be used on number or string types!" | binary `< <= > >=` |
| noUnaryPlus | "Unary `+` is not supported!" + sugg. "Use `tonumber(x)` instead." | prefix unary |
| noNonNumberUnaryMinus | "Unary `-` is only supported for number types!" | prefix unary |
| noMixedTypeCall | "Attempted to call a function with mixed types! All definitions must either be a method or a callback." | isMethodInner |
| noIndexWithoutCall | "Cannot index a method without calling it!" + sugg. "Use the form `() => a.b()` instead of `a.b`." | addIndexDiagnostics; identifier call-macro misuse |
| noInvalidIdentifier | "Invalid Luau identifier!" + "Luau identifiers must start with a letter and only contain letters, numbers, and underscores." + "Reserved Luau keywords cannot be used as identifiers." | validateIdentifier |
| noReservedIdentifier | "Cannot use identifier reserved for compiler internal usage." | validateIdentifier |
| noOptionalMacroCall | "Macro methods can not be optionally called!" + sugg. "Macros always exist. Use a normal call." | optional chain (later) |
| noConstructorMacroWithoutNew | "Cannot index a constructor macro without using the `new` operator!" | identifier |
| noMacroExtends | "Cannot extend from a macro class!" + sugg. "Store an instance of the macro class in a property." | identifier |
| noMacroObjectSpread | "Macro classes cannot be used in an object spread!" + sugg. "Did you mean to use an array spread? `[ ...exp ]`" | object spread (later) |
| noVarArgsMacroSpread | "Macros which use variadic arguments do not support spread expressions!" + issue(1149) | runCallMacro (later) |
| expectedMethodGotFunction | "Attempted to assign non-method where method was expected." | validateMethodAssignment |
| expectedFunctionGotMethod | "Attempted to assign method where non-method was expected." | validateMethodAssignment |

Warnings: `truthyChange(checksStr)` → "Value will be checked against {checksStr}" (truthiness,
gated by `logTruthyChanges`); `runtimeLibUsedInReplicatedFirst` → "This statement would generate a
call to the runtime library. The runtime library should not be used from ReplicatedFirst."
(`state.TS`).

---

## 12. Inventory

### 12.1 Reference files digested (all read in full)

Dispatch/state: `TSTransformer/nodes/expressions/transformExpression.ts`,
`TSTransformer/nodes/statements/transformStatement.ts`,
`TSTransformer/nodes/transformStatementList.ts`, `TSTransformer/classes/TransformState.ts`,
`TSTransformer/classes/DiagnosticService.ts`, `TSTransformer/typeGuards.ts`.
Literals: `expressions/transformNumericLiteral.ts`, `expressions/transformStringLiteral.ts`,
`expressions/transformBooleanLiteral.ts`,
`expressions/transformNoSubstitutionTemplateLiteral.ts`,
`expressions/transformTemplateExpression.ts`, `nodes/transformInterpolatedStringPart.ts`,
`util/createStringFromLiteral.ts`, `expressions/transformArrayLiteralExpression.ts`,
`expressions/transformObjectLiteralExpression.ts`, `nodes/transformPropertyName.ts`,
`expressions/transformParenthesizedExpression.ts`.
Identifiers: `expressions/transformIdentifier.ts`, `util/validateIdentifier.ts`,
`util/checkVariableHoist.ts`, `util/createHoistDeclaration.ts`, `util/isSymbolMutable.ts`.
Binary/logical/assignment: `expressions/transformBinaryExpression.ts`,
`util/createBinaryFromOperator.ts`, `nodes/transformLogical.ts`, `util/assignment.ts`,
`util/getAssignableValue.ts`, `nodes/transformWritable.ts`.
Unary: `expressions/transformUnaryExpression.ts`.
Truthiness/types: `util/createTruthinessChecks.ts`, `util/types.ts`.
Calls/access: `expressions/transformCallExpression.ts`, `util/isMethod.ts`,
`util/wrapReturnIfLuaTuple.ts`, `expressions/transformPropertyAccessExpression.ts`,
`expressions/transformElementAccessExpression.ts`, `nodes/transformOptionalChain.ts`,
`util/addOneIfArrayType.ts`, `util/offset.ts`, `util/addIndexDiagnostics.ts`,
`util/isValidMethodIndexWithoutCall.ts`, `util/getConstantValueLiteral.ts`,
`util/validateMethodAssignment.ts`.
Statements: `statements/transformVariableStatement.ts`,
`statements/transformExpressionStatement.ts`, `statements/transformIfStatement.ts`,
`statements/transformWhileStatement.ts`, `statements/transformDoStatement.ts`,
`statements/transformForStatement.ts`, `statements/transformBlock.ts`,
`statements/transformReturnStatement.ts`, `statements/transformBreakStatement.ts`,
`statements/transformContinueStatement.ts`, `statements/transformThrowStatement.ts`,
`util/getStatements.ts`, `util/isBlockedByTryStatement.ts`.
Shared utils: `util/ensureTransformOrder.ts`, `util/expressionChain.ts`,
`util/convertToIndexableExpression.ts`, `util/wrapExpressionStatement.ts`, `util/traversal.ts`,
`util/isUsedAsStatement.ts`, `util/expressionMightMutate.ts`, `util/valueToIdStr.ts`,
`util/pointer.ts`, `util/getOriginalSymbolOfNode.ts`, `util/validateNotAny.ts`.
Diagnostics: `Shared/diagnostics.ts`.

### 12.2 CHECKER call-site inventory (tsgo API surface)

TypeChecker methods:
- `getTypeAtLocation` — TransformState.ts:185 (memoized `state.getType`, takes `skipUpwards`);
  transformReturnStatement.ts:14 (raw, takes `skipDownwards`).
- `getSymbolAtLocation` — transformIdentifier.ts:17,121; ensureTransformOrder.ts:37;
  expressionMightMutate.ts:45; transformVariableStatement.ts:22; transformForStatement.ts:115,133,
  160,303,319,326; getOriginalSymbolOfNode.ts:4; TransformState.ts:332.
- `getShorthandAssignmentValueSymbol` — transformIdentifier.ts:16,120.
- `isUndefinedSymbol` — transformIdentifier.ts:124; types.ts:247 (getFirstDefinedSymbol).
- `isArgumentsSymbol` — transformIdentifier.ts:126.
- `getNonOptionalType` — transformCallExpression.ts:135,177,242,272;
  transformPropertyAccessExpression.ts:20; transformElementAccessExpression.ts:26;
  transformObjectLiteralExpression.ts:35; transformOptionalChain.ts:280;
  isValidMethodIndexWithoutCall.ts:20; transformForStatement.ts:336.
- `getAnyType` — types.ts:84. `getTrueType`/`getFalseType` — types.ts:94.
- `isTupleType` — types.ts:129; transformCallExpression.ts:47.
- `isArrayLikeType` — types.ts:130.
- `getTypeArguments` — types.ts:257.
- `getSignaturesOfType(type, ts.SignatureKind.Call)` — transformCallExpression.ts:33 (macro path).
- `getIndexTypeOfType(type, ts.IndexKind.Number)` — validateNotAny.ts:18.
- `getConstantValue` — getConstantValueLiteral.ts:9.
- `getContextualType` — validateMethodAssignment.ts:50.
- `getContextualTypeForObjectLiteralElement` — validateMethodAssignment.ts:31.
- `getTypeOfPropertyOfType` — validateMethodAssignment.ts:42,54,55.
- `getExportsOfModule` — TransformState.ts:310. `getEmitResolver` — TransformState.ts:61.
- `ts.skipAlias(symbol, checker)` — getOriginalSymbolOfNode.ts:6; TransformState.ts:318.
- `ts.FindAllReferences.Core.eachSymbolReferenceInFile` — checkVariableHoist.ts:23;
  transformForStatement.ts:79,350. `...isSymbolReferencedInFile` — transformForStatement.ts:75.
  (Internal TS API — tsgo port needs a scoped reference walker.)

Type object API: `flags` (TypeFlags: Object, Any, AnyOrUnknown, TypeVariable, Boolean,
BooleanLiteral, Number, NumberLike, NumberLiteral, String, StringLike, StringLiteral, Undefined,
Void, TemplateLiteral), `symbol`, `isUnion`, `isIntersection`, `isUnionOrIntersection`, `types`,
`isClassOrInterface`, `getBaseTypes`, `getConstraint`, `isNumberLiteral`+`value`,
`isStringLiteral`+`value`, `getProperties`, `getProperty(name)`, `getCallSignatures`
(+ `thisParameter`, `declaration`, `parameters` on signatures), `getConstructSignatures`,
`getNumberIndexType`, `getStringIndexType`; TemplateLiteralType `texts`.
Symbol API: `valueDeclaration`, `declarations`, `getDeclarations`, `name`.
Node-level TS helpers used: `ts.canHaveModifiers`/`getModifiers`, `ts.hasSyntacticModifier`,
`ts.positionIsSynthesized`, `ts.stripQuotes`, `ts.getLeadingCommentRanges`,
`ts.isAssignmentOperator`, `ts.isLogicalOrCoalescingAssignmentExpression`, `ts.isPrototypeAccess`,
`ts.isSuperCall`/`isSuperProperty`, `ts.isImportCall`, `ts.findAncestor`,
`ts.isIterationStatement`, `ts.isNodeDescendantOf`, `ts.getLastToken`, `ts.isWriteAccess`,
`ts.isAssignmentExpression`, `ts.isUnaryExpressionWithWrite`, `ts.isAnyImportSyntax`,
NodeFlags.Let/Const/Namespace.

### 12.3 Macro hook points (port: raise clean "macro not supported" errors)
- `macroManager.getIdentifierMacro` — transformIdentifier.ts:132.
- `macroManager.getCallMacro` — transformIdentifier.ts:155; transformCallExpression.ts:138;
  isValidMethodIndexWithoutCall.ts:23.
- `macroManager.getPropertyCallMacro` — transformCallExpression.ts:180,245;
  addIndexDiagnostics.ts:17; transformOptionalChain.ts:283; transformForStatement.ts:339.
- `macroManager.getConstructorMacro` — transformIdentifier.ts:139.
- `macroManager.getSymbolOrThrow(SYMBOL_NAMES.*)` — identifier globalThis check; types.ts array/
  set/map/LuaTuple/etc. symbol identity; transformReturnStatement.ts:21 ($tuple).
- `macroManager.isMacroOnlyClass` — transformObjectLiteralExpression.ts:37.

### 12.4 First-wave diagnostic name list (complete)
noBigInt, noNullLiteral, noPrivateIdentifier, noRegex, noTypeOfExpression, noForInStatement,
noLabeledStatement, noDebuggerStatement, noAny, noVar, noGetterSetter, noEqualsEquals,
noExclamationEquals, upstream rest-spread rejection, noLuaTupleDestructureAssignmentExpression,
noGlobalThis, noArguments, noPrototype, noRobloxSymbolInstanceof,
noNonNumberStringRelationOperator, noUnaryPlus, noNonNumberUnaryMinus, noMixedTypeCall,
noIndexWithoutCall, noInvalidIdentifier, noReservedIdentifier, noOptionalMacroCall,
noConstructorMacroWithoutNew, noMacroExtends, noMacroObjectSpread, noVarArgsMacroSpread,
expectedMethodGotFunction, expectedFunctionGotMethod; warnings: truthyChange,
runtimeLibUsedInReplicatedFirst.
