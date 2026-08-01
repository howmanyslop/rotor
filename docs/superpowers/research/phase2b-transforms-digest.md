# Phase 2b Transforms Digest — functions, destructuring, for-of, switch

Source of truth for porting roblox-ts v3.0.0 Phase 2b transforms to Go without reading the TS.
All paths relative to `reference/roblox-ts/src/`. Every checker/type-API usage is flagged
`CHECKER:`. Builds on `phase2-transforms-digest.md` (§refs like "P2 §3.1" point there).
Phase 2b SCOPE: function declarations/expressions/arrows (sync, non-generator emission; async/
generator DETECTED and documented as Phase 3 runtime-lib entry points), parameter machinery,
array/object destructuring over ARRAY/OBJECT types (full accessor table documented anyway),
for-of over arrays (full builder table documented anyway), switch, and the for-loop
closure-capture copy system that real closures activate.

---

## 1. Functions

### 1.1 Dispatch entries
- `transformExpression.ts` L83: `ts.SyntaxKind.ArrowFunction → transformFunctionExpression`;
  L94: `FunctionExpression → transformFunctionExpression`. **Arrows and function expressions
  share ONE transform** (`transformFunctionExpression.ts` takes
  `ts.FunctionExpression | ts.ArrowFunction`).
- `transformStatement.ts` L85: `FunctionDeclaration → transformFunctionDeclaration`.
- Reminder (P2 §1.2): `transformStatement` drops any statement with a `declare` modifier first,
  so `declare function` never reaches the transform. Bodiless overloads are dropped inside the
  transform (1.2 step 1).

### 1.2 transformFunctionDeclaration — `nodes/statements/transformFunctionDeclaration.ts` L13–76

1. `if (!node.body) return luau.list.make()` (L14–16) — overload signatures emit nothing.
2. `isExportDefault = ts.hasSyntacticModifier(node, ts.ModifierFlags.ExportDefault)` (L18);
   `assert(node.name || isExportDefault)` (anonymous functions only legal as default export).
3. If named: `validateIdentifier(state, node.name)` (L23; P2 §3.3 tail — raises
   `noInvalidIdentifier`/`noReservedIdentifier`).
4. `name = node.name ? transformIdentifierDefined(state, node.name) : luau.id("default")` (L26) —
   anonymous `export default function` is emitted under the literal name `default`.
   CHECKER: transformIdentifierDefined does `getSymbolAtLocation`/`getShorthandAssignmentValueSymbol`
   + symbolToIdMap lookup (P2 §3.1).
5. `let { statements, parameters, hasDotDotDot } = transformParameters(state, node)` (§1.4), then
   `pushList(statements, transformStatementList(state, node.body, node.body.statements))` (L28–29)
   — parameter-default/destructure statements come FIRST in the body, then the transformed body.
   No implicit return is ever inserted: Luau functions return nil implicitly.
6. `localize` decision (L31–36):
   ```ts
   let localize = isExportDefault;
   if (node.name) {
       const symbol = state.typeChecker.getSymbolAtLocation(node.name);   // CHECKER (L33)
       assert(symbol);
       localize = state.isHoisted.get(symbol) !== true;
   }
   ```
   If the symbol was hoisted (a `local f` already emitted by createHoistDeclaration, P2 §1.3/§3.3),
   emit non-local `function f() end`; otherwise `local function f() end`. Note an export-default
   NAMED function also goes through the `node.name` branch (localize from hoisting), while an
   anonymous default is always localized.
7. Generator (L40–45): if `node.asteriskToken`: if also async → `errors.noAsyncGeneratorFunctions`;
   then `statements = wrapStatementsAsGenerator(state, node, statements)` (§1.7).
8. Async (L38, 47–70): `isAsync = ts.hasSyntacticModifier(node, ts.ModifierFlags.Async)`. Async
   functions become `TS.async(function(<params>) <stmts> end)` (runtime lib, `state.TS` sets
   `usesRuntimeLib`), assigned via:
   - localize → `luau.VariableDeclaration { left: name, right }` → `local f = TS.async(...)`
   - else → `luau.Assignment { left: name, operator: "=", right }` → `f = TS.async(...)` (hoisted).
9. Sync (L71–75): one `luau.FunctionDeclaration { localize, name, statements, parameters,
   hasDotDotDot }`.

Export rules for functions: there is NO `util/isBlockedByIsolatedContainer.ts` in the reference
(the task prompt's name doesn't exist; "isolated containers" only appear in import-path validation
— `util/createImportExpression.ts` `errors.noIsolatedImport` — unrelated to functions). What
actually governs exported functions:
- file-level `export function f` produces no extra code at the statement; the exports table is
  assembled by `nodes/transformSourceFile.ts handleExports` (L94–189): non-mutable value exports
  are collected as `[exportKey, luau.id(name)]` pairs via `getExportPair` (L14–31; for
  `exportSymbol.name === "default"` with a NAMED FunctionDeclaration/ClassDeclaration the id used
  is the declaration's own name, key stays `default`). With no export-let/export-from, the file
  just ends `return { f = f, default = foo }` (L168–188); with mutable/`export *`/`export =`
  involvement the `local exports = {}` + assignments + `return exports` path runs (L143–167).
  CHECKER: `getModuleExports` (typeChecker.getExportsOfModule), `ts.skipAlias`,
  `symbol.flags & ts.SymbolFlags.Prototype`, `isSymbolOfValue`, `isSymbolMutable`.
- namespace-level exported functions use the `transformStatementList` exportInfo bookkeeping
  (P2 §1.3 step 6: `containerId.<name> = <name>` after the statement).

### 1.3 transformFunctionExpression — `nodes/expressions/transformFunctionExpression.ts` L11–47

Handles `function() {}` AND arrows.
1. `if (node.name) errors.noFunctionExpressionName(node.name)` (L12–14) — named function
   expressions (`const f = function g() {}`) are banned; transform continues (name dropped).
2. `transformParameters` (§1.4).
3. Body (L18–25):
   ```ts
   const body = node.body;
   if (ts.isFunctionBody(body)) {
       luau.list.pushList(statements, transformStatementList(state, body, body.statements));
   } else {
       const [returnStatements, prereqs] = state.capture(() => transformReturnStatementInner(state, body));
       luau.list.pushList(statements, prereqs);
       luau.list.pushList(statements, returnStatements);
   }
   ```
   `ts.isFunctionBody` = is a Block. Arrow expression bodies (`x => x + 1`) reuse the FULL return
   transform (§1.8) with prereqs captured into the function body, so `x => f().y` emits the call
   prereq then `return ...`. This is how implicit arrow returns work — there is no other
   implicit-return mechanism.
4. Generator/async exactly as 1.2 steps 7–8 (L27–34: asteriskToken → noAsyncGeneratorFunctions if
   async, wrapStatementsAsGenerator; L42–44: wrap whole FunctionExpression in
   `TS.async(<funcExp>)`).
5. Returns `luau.FunctionExpression { hasDotDotDot, parameters, statements }` (possibly wrapped).

### 1.4 transformParameters — `nodes/transformParameters.ts` L53–124

Input: any `ts.SignatureDeclarationBase` (function decl/expr, arrow, method, constructor).
Returns `{ parameters: luau.List<AnyIdentifier>, statements: luau.List<Statement>, hasDotDotDot }`.

1. `if (isMethod(state, node)) luau.list.push(parameters, luau.globals.self)` (L58–60) — methods
   get an explicit leading `self` parameter (because emission uses `function expr.name(self, ...)`
   or a FunctionExpression assigned to a key). CHECKER: §1.6.
2. Per `node.parameters` (L62–117):
   - `ts.isThisIdentifier(parameter.name)` → `continue` (L63–65). The TS `this` parameter emits
     NOTHING; its only effect is via isMethod (a `this: void` first param makes the function a
     callback, see §1.6).
   - **Spread-array-pattern optimization** (L67–73): `parameter.dotDotDotToken && isArrayBindingPattern`
     i.e. `...[a, b, c]: [A, B, C]` → `optimizeArraySpreadParameter` (L16–51) flattens the pattern
     elements into REAL parameters (no `...` capture):
     - OmittedExpression → push `luau.tempId()` as a parameter (placeholder).
     - element with `dotDotDotToken` → upstream rest-spread rejection, abort the pattern (return).
     - Identifier name → `transformIdentifierDefined` + `validateIdentifier`, push as param;
       `element.initializer` → prereq `transformInitializer(state, paramId, init)` (§1.5).
     - Nested pattern name → `paramId = luau.tempId("param")` pushed as param; initializer first,
       then recurse `transformArrayBindingPattern`/`transformObjectBindingPattern` (§2.3/2.4).
     All prereqs are captured (`state.capturePrereqs`) and appended to `statements` (L68–71).
   - Param id (L75–81): Identifier name → `transformIdentifierDefined` + `validateIdentifier`;
     binding-pattern name → `luau.tempId("param")`.
   - Rest param `...args` (L83–93): `hasDotDotDot = true`; param id is NOT added to parameters;
     instead prepend body statement `local args = { ... }` (VariableDeclaration whose right is an
     Array containing a `VarArgsLiteral`).
   - Else push paramId onto parameters (L95).
   - Default value (L98–100): `statements.push(transformInitializer(state, paramId, parameter.initializer))`
     — runs BEFORE destructuring.
   - Parameter destructuring (L103–116): if name is a pattern, append
     `state.capturePrereqs(() => transformArrayBindingPattern(state, pattern, paramId))` (or object
     variant). So `function f({ a } = {}) {}` emits `if _param == nil then _param = {} end` then
     `local a = _param.a`.

### 1.5 transformInitializer — `nodes/transformInitializer.ts` L6–20 (the `= default` shape)

```ts
return luau.create(luau.SyntaxKind.IfStatement, {
    condition: luau.binary(id, "==", luau.nil()),
    elseBody: luau.list.make(),
    statements: state.capturePrereqs(() => {
        state.prereq(luau.create(luau.SyntaxKind.Assignment, {
            left: id, operator: "=", right: transformExpression(state, initializer),
        }));
    }),
});
```
i.e. `if id == nil then <init prereqs>; id = <init> end`. Default expressions are evaluated lazily
inside the nil-check (TS semantics: default only computed when undefined is passed). Used by:
parameter defaults, binding-element defaults, assignment-pattern defaults, for-of/map shapes.

### 1.6 isMethod — `util/isMethod.ts` (COMPLETE; decides the implicit `self`)

- `getThisParameter(parameters)` (L9–17): first param whose name is identifier `this`.
- `isMethodDeclaration(state, node)` (L19–49), for `ts.isFunctionLike` nodes:
  - has `this` param → method iff CHECKER: `!(state.getType(thisParam).flags & ts.TypeFlags.Void)`
    (L23) — `this: void` forces callback, any other `this` type forces method.
  - no `this` param:
    - FunctionDeclaration → false ("namespace declare functions with `this` arg defined (i.e. utf8)").
    - MethodDeclaration | MethodSignature → true.
    - **Quirk (L34–43, verbatim comment "for some reason, FunctionExpressions within
      ObjectLiteralExpressions are implicitly methods")**: a FunctionExpression whose
      `skipUpwards(node).parent` is a PropertyAssignment whose `skipUpwards(parent).parent` is an
      ObjectLiteralExpression → true. So `{ foo: function() {} }` gets `self`; arrows
      (`{ foo: () => {} }`) do NOT (not FunctionExpression).
    - else false.
- `isMethodInner(state, node, type)` (L51–77): over CHECKER: `type.getCallSignatures()` —
  per signature, if CHECKER: `callSignature.thisParameter?.valueDeclaration` exists, method iff
  CHECKER: `!(state.getType(thisValueDeclaration).flags & TypeFlags.Void)` (L58); else if
  `callSignature.declaration` exists → `isMethodDeclaration` on it. Tally
  hasMethodDefinition/hasCallbackDefinition; if BOTH → `errors.noMixedTypeCall` (L73). Returns
  hasMethodDefinition.
- `isMethodFromType(state, node, type)` (L79–91): `walkTypes(type, t => ...)` (P2 §7.2 — recurse
  union/intersection members + constraints); for each leaf with `t.symbol`, memoized per-symbol in
  `multiTransformState.isMethodCache`: `isMethodInner`. OR-fold results.
- `isMethod(state, node)` (L93–98) = `isMethodFromType(state, node, state.getType(node))`.
  CHECKER: `state.getType(node)`.

### 1.7 Async/generator entry points (Phase 3 — detection documented now)

- Detection: `node.asteriskToken` (generator) and `ts.hasSyntacticModifier(node,
  ts.ModifierFlags.Async)`; both → `errors.noAsyncGeneratorFunctions` (decl L41–43, expr L30–32,
  method L54–56) but the generator wrap still proceeds.
- `wrapStatementsAsGenerator` — `util/wrapStatementsAsGenerator.ts` L5–17: replaces the whole body
  with `return TS.generator(function() <original statements> end)` (inner FunctionExpression has no
  params, no dotDotDot). Runtime-lib: `TS.generator`.
- Async: body unchanged; the FUNCTION VALUE is wrapped `TS.async(<FunctionExpression>)`. Runtime-lib:
  `TS.async`. For declarations this switches the emission from FunctionDeclaration to
  VariableDeclaration/Assignment (§1.2 step 8).
- Phase 2b port: keep detection + diagnostics; emitting `TS.async`/`TS.generator` calls requires
  only `state.TS` (P2 §0.1) — runtime lib itself is Phase 3.
- Related dispatch-level bans already exist for await/yield (`transformAwaitExpression`/
  `transformYieldExpression` — out of scope here, they also call `state.TS`).

### 1.8 Return plumbing — `nodes/statements/transformReturnStatement.ts` (COMPLETE)

`transformReturnStatement` (L71–84):
- No expression: if `isReturnBlockedByTryStatement(node)` → `state.markTryUses("usesReturn")` and
  emit `return TS.TRY_RETURN, {}` (Phase 3 try interplay); else emit `return nil` (L81) —
  upstream always materializes the nil.
- With expression → `transformReturnStatementInner(state, node.expression)`.

`transformReturnStatementInner` (L28–69):
1. `$tuple(...)` macro return (L36–39): `ts.isCallExpression(returnExp) && isTupleMacro(...)` —
   CHECKER: `getFirstDefinedSymbol(state, state.getType(expression.expression))` (L20) compared to
   macroManager `$tuple` symbol — args via `ensureTransformOrder` (prereqs pushed into result),
   expression becomes the multi-value list `return a, b, c`.
2. Else `expression = transformExpression(state, skipDownwards(returnExp))` (L41), then LuaTuple
   flattening (L42–48): if CHECKER: `isLuaTupleType(state)(state.getType(returnExp))` AND NOT
   `isTupleReturningCall` — which is (L10–16, verbatim comment "intentionally NOT using
   state.getType() here, because that uses skipUpwards"):
   ```ts
   luau.isCall(luaExpression) &&
   isLuaTupleType(state)(state.typeChecker.getTypeAtLocation(skipDownwards(tsExpression)))   // CHECKER (L14)
   ```
   then: a literal luau Array → return its members as a multi-return (`return a, b`); anything else
   → `return unpack(expression)`. (A call that itself returns a LuaTuple passes through —
   multi-returns chain.)
3. Try-block gate (L51–63): `isReturnBlockedByTryStatement(returnExp)` →
   `return TS.TRY_RETURN, { <expr(s)> }` (Phase 3); else plain `return <expr>` (L65).

`isReturnBlockedByTryStatement` — `util/isBlockedByTryStatement.ts` L3–9:
`ts.findAncestor(node, a => ts.isTryStatement(a) || ts.isFunctionLikeDeclaration(a))` is a
TryStatement. I.e. a `return` INSIDE A FUNCTION BODY stops at the function boundary —
function-level returns are plain Luau returns; only returns lexically inside `try` (without an
intervening function) need the runtime protocol. Source-file level has no ts.ReturnStatement at
all — the module return is synthesized by transformSourceFile (return exports / `return nil` for
ModuleScripts, §1.2 export notes).

### 1.9 wrapReturnIfLuaTuple — `util/wrapReturnIfLuaTuple.ts` (COMPLETE; function-context rules)

`wrapReturnIfLuaTuple(state, node /*CallExpression*/, exp)` (L58–63): if
CHECKER: `isLuaTupleType(state)(state.getType(node))` and `shouldWrapLuaTuple` → `luau.array([exp])`
(truncate multi-return to a table); else pass through.
`shouldWrapLuaTuple` (L8–56) — DON'T wrap (use the raw multi-value call) when:
- `exp` is not a luau Call → wrap (true) immediately (L9–11);
- parent (via `skipUpwards(node).parent`) is: ExpressionStatement; ForStatement with
  `parent.condition !== child` (initializer/incrementor position); VariableDeclaration whose name
  is an ArrayBindingPattern **and `!arrayBindingPatternContainsHoists(state, parent.name)`**
  (hoisted element kills the direct-unpack optimization, see §2.8/§2.10); AssignmentExpression with
  ArrayLiteralExpression LHS; ElementAccessExpression (`foo()[n]` → `select` handled elsewhere);
  ReturnStatement; VoidExpression.
- otherwise wrap.

### 1.10 transformMethodDeclaration — `nodes/transformMethodDeclaration.ts` (object-literal methods)

Called from transformObjectLiteralExpression (P2 §2.7) with the object's `Pointer`.
1. `!node.body` → empty (L21–23). PrivateIdentifier name → `errors.noPrivateIdentifier`, empty
   (L26–29).
2. `transformParameters` + body via transformStatementList (L31–32); `name =
   transformPropertyName(state, node.name)` (P2 §2.7).
3. Decorator bookkeeping (L36–49, classes/Phase 4): if `ts.hasDecorators(node)` or any param has
   decorators, non-simple-primitive names get a `local _key = <name>` temp and
   `state.setClassElementObjectKey(node, name)`.
4. Generator/async detection identical to §1.7 (L51–58).
5. **`function obj:name()` / `function obj.name()` optimization** (L60–87): when NOT async, name is
   a luau StringLiteral, `ptr.value` is NOT still an inline Map (i.e. the pointer was already
   spilled to a temp id), and `luau.isValidIdentifier(name.value)`:
   - `isMethod(state, node)` (CHECKER §1.6) → shift off the `self` param and emit luau
     `MethodDeclaration { expression: ptr.value, name, ... }` (`function obj:name(...)`);
   - else emit `FunctionDeclaration { name: luau.property(ptr.value, name.value), localize: false }`
     (`function obj.name(...)`).
6. Fallback (L89–105): build FunctionExpression (async → wrap `TS.async(...)`), then
   `assignToMapPointer(state, ptr, name, expression)` — inline MapField if the map is still inline,
   else `obj[name] = function...` prereq.

### 1.11 validateMethodAssignment — `util/validateMethodAssignment.ts` (COMPLETE; deferred from P2 Task 6)

Entry (L63–77): for a ClassElement with class parent and a name → `validateHeritageClause` per
`ts.getAllSuperTypeNodes(node.parent)` (Phase 4). For ObjectLiteralElementLike: SpreadAssignment
whose expression is NOT an object literal → `validateSpread`; else `validateObjectLiteralElement`.
- `hasCallSignatures(type)` (L8–14): walkTypes; CHECKER: `t.getCallSignatures().length > 0`.
- `validateTypes(state, node, baseType, assignmentType)` (L16–27): only when BOTH types have call
  signatures; `assignmentIsMethod = isMethodFromType(state, node, assignmentType)`; if
  `isMethodFromType(baseType) !== assignmentIsMethod` → `errors.expectedMethodGotFunction` (when
  assignment is the method) / `errors.expectedFunctionGotMethod`.
- `validateObjectLiteralElement` (L29–35): CHECKER: `state.getType(node)` and
  `typeChecker.getContextualTypeForObjectLiteralElement(node)` (L31); compare only when contextual
  type exists and differs from own type.
- `validateSpread` (L48–61): CHECKER: `state.getType(node.expression)`,
  `typeChecker.getContextualType(node.expression)` (L50), then per
  CHECKER: `type.getProperties()`: `typeChecker.getTypeOfPropertyOfType(type, property.name)` vs
  `(contextualType, property.name)` (L54–55) and validateTypes each pair.
- `validateHeritageClause` (L37–46): `ts.getPropertyNameForPropertyNameNode(node.name)`;
  CHECKER: `state.getType(node)`, `state.getType(typeNode)`,
  `typeChecker.getTypeOfPropertyOfType(...)` (L42).

---

## 2. Destructuring

Two parallel systems with identical accessor plumbing:
- **Binding patterns** (`ts.ArrayBindingPattern`/`ts.ObjectBindingPattern`) — in `const/let`
  declarations and parameters. Elements are `ts.BindingElement` (name, optional propertyName,
  optional dotDotDotToken, optional initializer) or `ts.OmittedExpression`.
- **Assignment patterns** (`ts.ArrayLiteralExpression`/`ts.ObjectLiteralExpression` as assignment
  LHS) — `[a, b] = exp`, `({ a } = exp)`. Defaults appear as BinaryExpression `a = 1` inside the
  literal; targets may be identifiers OR property/element accesses.

### 2.1 transformBindingName — `nodes/binding/transformBindingName.ts` L8–30

Identifier → `transformIdentifierDefined`. Pattern → `id = luau.tempId("binding")`, then
`capturePrereqs(transform{Array,Object}BindingPattern(state, name, id))` appended to the caller's
`initializers` list. Returns the id. (Used by for-of initializers; variable declarations go through
transformVariableDeclaration instead, §2.8.)

### 2.2 transformArrayBindingPattern — `nodes/binding/transformArrayBindingPattern.ts` L12–51

```ts
validateNotAnyType(state, bindingPattern);            // CHECKER via P2 §11.2
let index = 0;
const idStack = new Array<luau.AnyIdentifier>();
const accessor = getAccessorForBindingType(state, bindingPattern, state.getType(bindingPattern));  // CHECKER (L21)
for (const element of bindingPattern.elements) {
    if (ts.isOmittedExpression(element)) {
        accessor(state, parentId, index, idStack, true);          // isOmitted=true: side-effect only
    } else {
        if (element.dotDotDotToken) { upstream rest-spread rejection; return; }
        const name = element.name;
        const value = accessor(state, parentId, index, idStack, false);
        if (ts.isIdentifier(name)) {
            const id = transformVariable(state, name, value);     // §2.8 — handles export-let/hoist
            if (element.initializer) state.prereq(transformInitializer(state, id, element.initializer));
        } else {
            const id = state.pushToVar(value, "binding");
            if (element.initializer) state.prereq(transformInitializer(state, id, element.initializer));
            if (ts.isArrayBindingPattern(name)) transformArrayBindingPattern(state, name, id);
            else transformObjectBindingPattern(state, name, id);
        }
    }
    index++;
}
```
All output goes through `state.prereq*` (callers wrap in capturePrereqs). Rest elements
(`...rest`) → upstream rest-spread rejection and ABORT the rest of the pattern (return). Defaults run
before nested-pattern recursion. Omitted elements still invoke the accessor with `isOmitted=true`
so stateful accessors (string/set/map/iter) advance; the array accessor's omitted call is a no-op
(returns an expression nobody uses, emits nothing).

### 2.3 transformObjectBindingPattern — `nodes/binding/transformObjectBindingPattern.ts` L13–48

`validateNotAnyType`. Per element:
- `dotDotDotToken` (`...rest`) → upstream rest-spread rejection, return.
- name Identifier: `value = objectAccessor(state, parentId, state.getType(bindingPattern),
  prop ?? name)` — CHECKER (L27); `prop` is `element.propertyName` (`{ a: b }` reads `a`, declares
  `b`); `id = transformVariable(state, name, value)`; initializer → transformInitializer prereq.
- name is nested pattern: `assert(prop)` ("in that case, prop is guaranteed to exist" — `{ a: [b] }`);
  `value = objectAccessor(..., prop)` (CHECKER L36); `id = pushToVar(value, "binding")`;
  initializer; recurse array/object.

### 2.4 The accessor table — `util/binding/getAccessorForBindingType.ts` (COMPLETE)

`BindingAccessor = (state, parentId, index, idStack, isOmitted) => luau.Expression`. `idStack`
carries iteration state BETWEEN elements of one pattern (e.g. the gmatch matcher, the last `next`
key). Dispatch (L143–167), in order, all via `isDefinitelyType` (P2 §7.1):
| predicate | accessor | emission per element |
|---|---|---|
| `isArrayType(state)` | arrayAccessor (L32–37) | `parentId[index + 1]` (literal-folded number); omitted: nothing |
| `isStringType` | stringAccessor (L39–63) | first element: `local _matcher = string.gmatch(parentId, utf8.charpattern)` pushed to idStack; value = `_matcher()`; omitted: bare CallStatement `_matcher()` |
| `isSetType(state)` | setAccessor (L65–84) | `next(parentId[, lastId])`; non-omitted: `local _value = next(...)` pushed to idStack (continuation key); omitted: CallStatement |
| `isMapType(state)` | mapAccessor (L86–103) | `local _k, _v = next(parentId[, lastK])` (multi-assign VariableDeclaration); pushes `_k`; returns `luau.Array { _k, _v }` — the `{ _k, _v }` table is then destructured by the nested `[k, v]` pattern; NOTE: no isOmitted branch (omitted map element still emits the local decl) |
| `isIterableFunctionLuaTupleType(state)` | iterableFunctionLuaTupleAccessor (L105–117) | value = `{ parentId() }` (array-wrap the multi-return); omitted: CallStatement `parentId()` |
| `isIterableFunctionType(state)` | iterableFunctionAccessor (L119–131) | value = `parentId()`; omitted: CallStatement |
| `isIterableType(state)` | `errors.noIterableIteration` (L157), accessor = `() => luau.none()` |
| `isGeneratorType(state)` ∥ `isObjectType` ∥ `ts.isThis(node)` | iterAccessor (L133–141) | value = `parentId.next().value`; omitted: CallStatement `parentId.next()` |
| else | `assert(false, "Destructuring not supported for type: " + typeChecker.typeToString(type))` (L166) |

Phase 2b ships arrayAccessor (+ objectAccessor §2.5); the others are pure-Luau emissions (no
runtime lib!) needed when Phase 3 enables Map/Set/Generator/IterableFunction types.
CHECKER: predicates use macroManager symbol identity — `util/types.ts`: `isSetType` L139–144
(symbol === Set|ReadonlySet|WeakSet), `isMapType` L146–151 (Map|ReadonlyMap|WeakMap),
`isGeneratorType` L153–155 (Generator), `isIterableFunctionType` L157–159 (IterableFunction),
`isIterableType` L177–179 (Iterable), `isIterableFunctionLuaTupleType` L167–175
(IterableFunction AND CHECKER: `getTypeArguments(state, type)[0]` is LuaTuple via
`isLuaTupleType` = CHECKER: `type.getProperty("_nominal_LuaTuple")` identity, L161–165).

### 2.5 objectAccessor — `util/binding/objectAccessor.ts` L11–36

```ts
addIndexDiagnostics(state, name, state.getType(name));     // CHECKER (L17); P2 §9 — noPrototype/
                                                           // noIndexWithoutCall/validateNotAnyType
if (ts.isIdentifier(name)) return luau.property(parentId, name.text);
else if (ts.isComputedPropertyName(name))
    return ComputedIndexExpression { expression: parentId,
        index: addOneIfArrayType(state, type, transformExpression(state, name.expression)) };  // CHECKER
else if (ts.isNumericLiteral(name) || ts.isStringLiteral(name) || ts.isNoSubstitutionTemplateLiteral(name))
    return ComputedIndexExpression { expression: parentId, index: transformExpression(state, name) };
else if (ts.isPrivateIdentifier(name)) { errors.noPrivateIdentifier(name); return luau.none(); }
assertNever
```
QUIRK: computed names get the +1 array adjustment (`type` is the PARENT pattern's type), but
literal numeric names do NOT (`{ 0: x }` over a tuple emits `parent[0]`, `{ [0]: x }` emits
`parent[1]` when parent is array-typed). Port verbatim.

### 2.6 transformArrayAssignmentPattern — `nodes/binding/transformArrayAssignmentPattern.ts` L14–73

Same skeleton as §2.2 but over `ts.ArrayLiteralExpression`:
- accessor from CHECKER: `state.typeChecker.getTypeOfAssignmentPattern(assignmentPattern)` (L24)
  — NOT getType (assignment patterns need the special checker API).
- Element forms: OmittedExpression → accessor(isOmitted). SpreadElement → upstream rest-spread rejection
  (NOTE: does NOT return — keeps iterating, unlike binding patterns). Default unwrapping:
  ```ts
  if (ts.isBinaryExpression(element)) { initializer = skipDownwards(element.right); element = skipDownwards(element.left); }
  ```
- Identifier | ElementAccessExpression | PropertyAccessExpression targets:
  `id = transformWritableExpression(state, element, initializer !== undefined)` (P2 §4.3 —
  readAfterWrite only when a default needs to re-read), prereq `id = value` Assignment, then
  optional transformInitializer.
- Nested ArrayLiteral/ObjectLiteral → `id = pushToVar(value, "binding")`, optional initializer,
  recurse transform{Array,Object}AssignmentPattern.
- else `assert(false, "transformArrayAssignmentPattern invalid element: " + getKindName(...))`.
idStack typed `Array<luau.Identifier>` here (cosmetic).

### 2.7 transformObjectAssignmentPattern — `nodes/binding/transformObjectAssignmentPattern.ts` L14–90

Per property of the object literal LHS:
- ShorthandPropertyAssignment `{ a }` / `{ a = 1 }` (L20–39): `value = objectAccessor(state,
  parentId, CHECKER: getTypeOfAssignmentPattern(assignmentPattern) (L25), name)`;
  `id = transformWritableExpression(state, name, property.objectAssignmentInitializer !== undefined)`;
  prereq `id = value`; `assert(luau.isAnyIdentifier(id))`; default from
  `property.objectAssignmentInitializer`.
- SpreadAssignment → upstream rest-spread rejection, RETURN (aborts remaining properties).
- PropertyAssignment `{ a: target }` / `{ a: target = 1 }` (L43–85): default unwrapping when
  `property.initializer` is a BinaryExpression (init = left, initializer = right, both
  skipDownwards). `value = objectAccessor(..., getTypeOfAssignmentPattern, name)` (CHECKER L55).
  Targets: Identifier/ElementAccess/PropertyAccess → transformWritableExpression + `id = value` +
  optional default; nested ArrayLiteral (asserts `ts.isIdentifier(name)`) / ObjectLiteral →
  pushToVar + default + recurse; else assert with getKindName.

### 2.8 Variable-declaration destructuring — `nodes/statements/transformVariableStatement.ts`

`transformVariable(state, identifier, right?)` (L19–55) — single-name declaration (also used per
binding element):
1. `validateIdentifier`; CHECKER: `getSymbolAtLocation(identifier)` (L22), assert.
2. export-let indirection (L26–40): `isSymbolMutable(state, symbol)` (P2 §0.3 CHECKER) and
   `state.getModuleIdPropertyAccess(symbol)` → prereq `exports.x = right` (when right) and return
   the export access as the "id" (subsequent destructure defaults write through it).
3. `left = transformIdentifierDefined`; `checkVariableHoist(state, identifier, symbol)` (§4.3!);
   if `state.isHoisted.get(symbol) === true` → prereq `left = right` Assignment only when right
   exists ("no need to do `x = nil`"); else prereq `local left = right` (right may be undefined →
   `local left`). Returns left.

`transformVariableDeclaration(state, node)` (L101–167):
1. Initializer transformed FIRST with prereqs captured (L107–114; comment "must transform right
   _before_ checking isHoisted, that way references inside of value can be hoisted").
2. Identifier name → capturePrereqs(transformVariable(name, value)).
3. Pattern name: `assert(node.initializer && value)`.
   - **Empty-pattern optimization** (L127–132): `name.elements.length === 0` → keep only the RHS
     side effects: skip entirely if value is a literal empty Array, else
     `wrapExpressionStatement(value)` (P2 §0.3).
   - ArrayBindingPattern (L134–155), three paths:
     a. LuaTuple direct unpack: `luau.isCall(value)` AND CHECKER:
        `isLuaTupleType(state)(state.getType(node.initializer))` AND
        `!arrayBindingPatternContainsHoists(state, name)` →
        `transformOptimizedArrayBindingPattern(state, name, value)` — `local a, b = f()` (the call
        was NOT array-wrapped thanks to §1.9).
     b. Literal-array RHS: `luau.isArray(value)` non-empty AND no hoists →
        `transformOptimizedArrayBindingPattern(state, name, value.members)` — `local a, b = x, y`
        (comment L144: "we can't localize multiple variables at the same time if any of them are
        hoisted").
     c. Fallback: `transformArrayBindingPattern(state, name, state.pushToVar(value, "binding"))`.
   - ObjectBindingPattern (L156–163): always `transformObjectBindingPattern(state, name,
     pushToVar(value, "binding"))`.

`transformOptimizedArrayBindingPattern` (L57–99): builds `ids` parallel to elements —
omitted → `luau.tempId()`; identifier → validate + transformIdentifierDefined (NOTE: bypasses
transformVariable, so NO export-let/hoist handling — safe because hoist-containing patterns were
excluded and export-let only applies at module level where... it is NOT excluded; upstream relies
on optimized patterns being local-only in practice — port verbatim); rest element →
upstream rest-spread rejection + abort; nested pattern → tempId("binding") + recurse (§2.2/2.3). Defaults
via transformInitializer prereqs captured into `statements` which are emitted AFTER the single
`local a, b, c = <rhs...>` VariableDeclaration (multi-left, multi-right).

`arrayBindingPatternContainsHoists` — `util/arrayBindingPatternContainsHoists.ts` L5–25: for each
direct BindingElement with Identifier name: CHECKER: `getSymbolAtLocation` (L14), run
`checkVariableHoist(state, element.name, symbol)` (SIDE EFFECT: marks hoists!), return true if
`state.isHoisted.get(symbol)`. Nested patterns ignored (their locals are tempIds; "hoisting logic
is handled elsewhere").

`isVarDeclaration` (L169–171): `!(flags & Const) && !(flags & Let)` → in
`transformVariableDeclarationList` (L173–189) raises `errors.noVar` then still transforms each
declaration (capture prereqs, push prereqs then statements per declaration).

### 2.9 Assignment-expression destructuring — `expressions/transformBinaryExpression.ts` L141–184

Inside `ts.isAssignmentOperator(operatorKind)`:
- ArrayLiteral LHS (L143–169): `rightExp = transformExpression(node.right)` ("in destructuring,
  rhs must be executed first").
  - Empty `[] = exp` (L147–152): if used as statement and rightExp is a literal empty Array →
    `luau.none()`; else return rightExp (value passthrough).
  - LuaTuple optimization (L154–160): `luau.isCall(rightExp)` && CHECKER:
    `isLuaTupleType(state)(state.getType(node.right))` →
    `transformOptimizedArrayAssignmentPattern(state, node.left, rightExp)`; if NOT
    `isUsedAsStatement(node)` → `errors.noLuaTupleDestructureAssignmentExpression`; returns
    `luau.none()`.
  - Literal-array RHS used as statement (L162–165): optimized with `rightExp.members`; `luau.none()`.
  - Fallback (L167–169): `parentId = pushToVar(rightExp, "binding")`;
    `transformArrayAssignmentPattern(state, node.left, parentId)`; return parentId (the expression
    value of `[a] = exp` is exp).
- ObjectLiteral LHS (L170–184): empty-pattern optimization mirrors array (literal empty Map);
  else pushToVar + `transformObjectAssignmentPattern`; return parentId.

`transformOptimizedArrayAssignmentPattern` (L36–111) — the multi-assign form `a, b = f()`:
collects `writes` (assignment targets), `variables` (tempIds needing `local` pre-declaration for
nested patterns), `writesPrereqs` (target prereqs e.g. index computations). Per element: omitted →
tempId write; spread → upstream rest-spread rejection (continues); default unwrap as §2.6;
identifier/element/property access → `state.capture(transformWritableExpression(element, true))`
(prereqs → writesPrereqs), defaults captured into trailing `statements`; nested array/object
literal → tempId pushed to BOTH variables and writes, default + recurse captured into trailing
statements. Emission order (L93–110): `local _t1, _t2` (if any variables) → writesPrereqs →
`a, b, _t1 = <rhs...>` (multi-Assignment, rhs is the call or member list) → trailing statements
(defaults + nested destructures). `assert(!writes empty)`.

### 2.10 Parameters
Entry documented in §1.4 — parameter binding patterns reuse §2.2/§2.3 with `paramId` as parent;
defaults precede destructuring; `...[a,b]` flattening per §1.4.

---

## 3. for-of — `nodes/statements/transformForOfStatement.ts` (COMPLETE)

### 3.1 Entry — transformForOfStatement (L479–508)

```ts
if (node.awaitModifier) errors.noAwaitForOf(node);                       // continues
if (ts.isVariableDeclarationList(node.initializer)) {
    const name = node.initializer.declarations[0].name;
    if (ts.isIdentifier(name)) validateIdentifier(state, name);
}
const rangeMacroCall = findRangeMacro(state, node);                      // §3.5
if (rangeMacroCall) return transformForOfRangeMacro(state, node, rangeMacroCall);
const [exp, expPrereqs] = state.capture(() => transformExpression(state, node.expression));
// expPrereqs pushed to result
const expType = state.getType(node.expression);                          // CHECKER (L501)
const statements = transformStatementList(state, node.statement, getStatements(node.statement));
const loopBuilder = getLoopBuilder(state, node.expression, expType);
luau.list.pushList(result, loopBuilder(state, statements, node.initializer, exp));
```
The BODY is transformed BEFORE the loop shape is built; builders prepend initializer statements.

### 3.2 LoopBuilder machinery (L34–57)

`LoopBuilder = (state, statements /*already-transformed body*/, initializer, exp) => List<Statement>`.
`makeForLoopBuilder(callback)`: callback fills `ids` (the generic-for binding list) and
`initializers` (statements to prepend to the body), returns the for-expression. Then
`unshiftList(statements, initializers)` and wrap: `for <ids> in <expression> do <statements> end`
(luau `ForStatement` = generalized iteration; **no ipairs/pairs anywhere in 3.0**).

### 3.3 Initializer plumbing

`transformForInitializer(state, initializer, initializers)` (L94–128) — produce ONE identifier for
the loop binding:
- VariableDeclarationList → `transformBindingName(state, initializer.declarations[0].name,
  initializers)` (§2.1: identifier directly as the loop variable, or `_binding` tempId + pattern
  destructure prepended to body).
- ArrayLiteralExpression (`for ([a, b] of ...)`) → tempId("binding") +
  capturePrereqs(transformArrayAssignmentPattern) into initializers.
- ObjectLiteralExpression → same with object pattern.
- other writable expression (`for (x of ...)`, `for (a.b of ...)`) → `valueId = tempId("v")` as
  loop binding; initializers get `<writable> = _v` (transformWritableExpression readAfterWrite=false).

`transformForInitializerExpressionDirect(state, initializer, initializers, value)` (L59–92) — same
but assigning a known `value` expression (used by map/generator builders): array pattern →
`local _binding = <value>` (pushToVar inside capture) + destructure; object pattern → same; else
`<writable> = <value>` assignment.

### 3.4 Builder dispatch — getLoopBuilder (L414–438)

Order of `isDefinitelyType` tests (CHECKER predicates per §2.4 table): Array → Set → Map → String
→ IterableFunction<LuaTuple> → IterableFunction → Generator → Iterable (error) → union (error) →
`assert(false, "ForOf iteration type not implemented: " + typeToString)`.
- `errors.noIterableIteration` (L430) / `errors.noMacroUnion` (L433) both return a builder that
  emits nothing.

**buildArrayLoop** (L130–134) — Phase 2b scope:
```ts
luau.list.push(ids, luau.tempId());                                    // discard slot
luau.list.push(ids, transformForInitializer(state, initializer, initializers));
return exp;
```
→ `for _, x in exp do ... end`. There is NO index variable in TS for-of; the first generic-for
binding is an unnamed tempId (renders `_` when unreferenced). No ipairs — generalized iteration
directly over the array expression. Pattern initializers destructure inside the body
(`for _, _binding in exp do local a = _binding[1] ... end`).

**buildSetLoop** (L136–139): single id → `for x in exp do`.

**buildMapLoop** (L224–257): inline-destructure fast path — if initializer is a
VariableDeclarationList whose first name is an ArrayBindingPattern →
`transformInLineArrayBindingPattern` (L141–160): each pattern element becomes a generic-for
binding directly (`for k, v in exp do`); omitted → tempId; spread → upstream rest-spread rejection;
defaults via transformInitializer appended to initializers; nested patterns via
transformBindingName. If initializer is an ArrayLiteralExpression (assignment form) →
`transformInLineArrayAssignmentPattern` (L162–222): same idea, each slot gets
`valueId = tempId("binding")` as the for binding, with `target = _binding` assignments / nested
destructures / defaults captured into initializers (writables computed with
readAfterWrite=initializer-present). Fallback (non-array initializer over a Map, e.g.
`for (const pair of map)`): bindings `_k, _v`; declaration-list →
`local pair = { _k, _v }` + pattern bindingList; expression form →
transformForInitializerExpressionDirect with `{ _k, _v }`.

**buildStringLoop** (L259–262): id = initializer;
expression = `string.gmatch(exp, utf8.charpattern)` → `for c in string.gmatch(exp, utf8.charpattern) do`.

**buildIterableFunctionLoop** (L264–267): `for x in exp do` (Luau calls the function each
iteration; nil terminates).

**buildIterableFunctionLuaTupleLoop** (L286–382) — `type` is captured for tuple introspection:
- Array-pattern fast path: VariableDeclarationList with ArrayBindingPattern name, or
  ArrayLiteralExpression initializer → `makeIterableFunctionLuaTupleShorthand` (L269–284):
  inline bindings → `for a, b in exp do`.
- Else: CHECKER: `luaTupleType = type.getCallSignatures()[0].getReturnType()` (L303);
  `assert(luaTupleType.aliasTypeArguments?.length === 1, "Incorrect LuaTuple<T> type arguments")`;
  `tupleArgType = aliasTypeArguments[0]`.
  - If declaration-list initializer AND CHECKER: `typeChecker.isTupleType(tupleArgType)` AND NOT
    CHECKER: `(tupleArgType as ts.TupleTypeReference).target.combinedFlags & ts.ElementFlags.Rest`
    (L313–315): build `iteratorReturnIds` — one tempId per CHECKER: `target.elementFlags.length`,
    named from CHECKER: `target.labeledElementDeclarations[i]` labels when the label name is an
    identifier and `luau.isValidIdentifier`, else "element" (L317–327). Then (L364–381)
    `tupleId = transformForInitializer(...)` into the OUTER statements, and the loop is
    `for _el1, _el2 in exp do local t = { _el1, _el2 } ... end` (VariableDeclaration tupleId =
    array of return ids, prepended via initializers).
  - Else (unknown arity / expression initializer) while-loop protocol (L328–362):
    ```
    local _iterFunc = exp                  -- pushToVar(exp, valueToIdStr(exp) || "iterFunc")
    while true do
        local _v = { _iterFunc() }
        if #_v == 0 then break end
        <initializerStatements (binds _v)>
        <body>
    end
    ```

**buildGeneratorLoop** (L384–412):
```
for _result in exp.next do                 -- luau.property(convertToIndexableExpression(exp), "next")
    if _result.done then break end
    local x = _result.value                -- or pattern/writable via the two initializer helpers
    <body>
end
```

### 3.5 $range macro (L440–477)

`findRangeMacro`: expression (skipDownwards) is a CallExpression whose
CHECKER: `getFirstDefinedSymbol(state, state.getType(expression.expression))` (L443) equals
macroManager `$range` symbol. `transformForOfRangeMacro`: id = transformForInitializer (statements
prepended to body); `[start, end, step] = ensureTransformOrder(macroCall.arguments)` (prereqs
captured to result); emits luau `NumericForStatement { id, start, end, step }` where
`step = (undefined || NumberLiteral) ? step : luau.binary(step, "or", luau.number(1))` (L471) —
non-literal step gets `or 1` to match `Number()` defaulting.

---

## 4. switch — `nodes/statements/transformSwitchStatement.ts` (COMPLETE)

### 4.1 Overall shape — transformSwitchStatement (L112–165)

```ts
const expression = state.pushToVarIfComplex(transformExpression(state, node.expression), "exp");
```
(NOT captured — expression prereqs flow to the outer statement list; complex switch subject gets
`local _exp = ...`.) `fallThroughFlagId = luau.tempId("fallthrough")`. Iterate
`node.caseBlock.clauses`:
- CaseClause: `shouldUpdateFallThroughFlag = i < clauses.length - 1 &&
  ts.isCaseClause(clauses[i+1])` (no flag update needed before a default clause or at the end);
  transformCaseClause (§4.2); push its prereqs then clauseStatements;
  `canFallThroughTo = canFallThroughFrom`; any canFallThroughFrom sets
  `isFallThroughFlagNeeded = true` (NOTE: even on the LAST case clause — can declare a
  never-read flag; port verbatim).
- DefaultClause (the `else` branch, L143–146): push
  `transformStatementList(state, caseClauseNode, caseClauseNode.statements)` INLINE (no condition)
  and **`break` the loop — any clauses AFTER a default clause are silently dropped** (upstream
  quirk; TS would still match them — port verbatim).
If flag needed: unshift `local _fallthrough = false`. Wrap EVERYTHING in
`RepeatStatement { condition: luau.bool(true), statements }`:
```
repeat
    local _fallthrough = false          -- only when needed
    <case prereqs / if-blocks / default statements>
until true
```
TS `break` inside the switch → plain Luau `break` (transformBreakStatement) which exits the
`repeat ... until true`. PORT NOTE: a TS `continue` inside a switch inside a loop emits a Luau
`continue` inside the repeat, which in Luau jumps to the `until` of the REPEAT (effectively acting
like a switch-break, not a loop-continue) — upstream emits this as-is; replicate byte-for-byte.

### 4.2 transformCaseClause (L54–110) + transformCaseClauseExpression (L8–52)

Case condition: `[expression, prereqStatements] = state.capture(transformExpression(caseExpr))`;
expression is ALWAYS wrapped in a luau ParenthesizedExpression (L17). Base condition:
`switchExpression == (caseExpr)`.
Fallthrough plumbing — when `canFallThroughTo` (the previous case clause can reach this one):
- case expression had prereqs (L22–42): prereqs must only run when not already falling through:
  ```
  if not _fallthrough then
      <prereqs>
      _fallthrough = _exp == (<caseExpr>)
  end
  ```
  (the assignment is appended to the prereq block) and the clause condition becomes just
  `_fallthrough`.
- no prereqs: condition = `_fallthrough or (_exp == (<caseExpr>))` (L44).

Body (L70–87): `nonEmptyStatements = node.statements.filter(!ts.isEmptyStatement)`; if exactly one
statement and it's a Block → transform the BLOCK's statements with the block as parent (case-level
braces don't double-nest); else transform `node.statements` with the CaseClause as parent.
`canFallThroughFrom = statements.tail === undefined || !luau.isFinalStatement(statements.tail.value)`
(empty body or body not ending in return/break/continue). If `canFallThroughFrom &&
shouldUpdateFallThroughFlag` → append `_fallthrough = true` to the body.
Clause emission (L89–103): `clauseStatements = [ createHoistDeclaration(state, node /*CaseClause*/)?,
IfStatement { condition, statements, elseBody: {} } ]`. The hoist declaration (`local x` merged
list, P2 §1.3) lands BEFORE the `if`, making variables visible to later clauses — populated by
checkVariableHoist (§4.3) keyed on the CaseClause (`hoistsByStatement: Map<ts.Statement |
ts.CaseClause, ...>`).
Returns `{ canFallThroughFrom, prereqs: prereqStatements, clauseStatements }`; caller emits prereqs
(possibly the guarded-prereq if-block) then the clause if-block, sequentially in the repeat body.

### 4.3 checkVariableHoist — `util/checkVariableHoist.ts` L6–39 (cross-clause hoisting)

Called from `transformVariable` (every identifier declaration, §2.8) and
`arrayBindingPatternContainsHoists` (§2.8). Distinct from `checkIdentifierHoist` (P2 §3.3 —
use-before-declare at REFERENCES); this one runs at DECLARATIONS and only handles case clauses:
```ts
if (state.isHoisted.get(symbol) !== undefined) return;          // already decided
const statement = getAncestor(node, ts.isStatement);
if (!statement) return;
const caseClause = statement.parent;
if (!ts.isCaseClause(caseClause)) return;                       // only case-clause scope matters
const caseBlock = caseClause.parent;
const isUsedOutsideOfCaseClause =
    ts.FindAllReferences.Core.eachSymbolReferenceInFile(
        node, state.typeChecker, node.getSourceFile(),
        token => { if (!isAncestorOf(caseClause, token)) return true; },
        caseBlock,                                              // search container!
    ) === true;
if (isUsedOutsideOfCaseClause) {
    getOrSetDefault(state.hoistsByStatement, statement.parent /* the CaseClause */, () => []).push(node);
    state.isHoisted.set(symbol, true);
}
```
Semantics: a `let/const` declared directly in a case clause is block-scoped to the whole case
BLOCK in TS; if any reference lies outside the declaring clause (i.e. in a sibling clause —
references outside the switch are TS scope errors), emit `local x` at the top of the declaring
clause (before its `if`) and demote the declaration to an assignment (`transformVariable` sees
`isHoisted === true`). CHECKER: `ts.FindAllReferences.Core.eachSymbolReferenceInFile` — internal
TS API; see §6 for the Go walker spec.

---

## 5. Loop closure-capture copies — `nodes/statements/transformForStatement.ts` (COMPLETE)

TS `for (let i = 0; ...)` creates a fresh `i` binding per iteration (closures capture per-iteration
values); Luau `while` reuses one local. Upstream fixes this with per-iteration copies, but ONLY for
variables that need it.

### 5.1 Reference analyses

`isIdWriteOrAsyncRead(state, forStatement, id)` (L78–100) — does the loop variable need the
copy treatment? True if ANY reference (searchContainer = the ForStatement; §6 walker) satisfies:
```ts
// write
if (ts.isWriteAccess(token) && (!forStatement.incrementor || !isAncestorOf(forStatement.incrementor, token)))
    return true;
// async read
const ancestor = getAncestor(token, v => v === forStatement || ts.isFunctionLike(v));
if (ancestor && ancestor !== forStatement) return true;
```
i.e. (a) the variable is WRITTEN anywhere in the loop except (solely) inside the incrementor, or
(b) any reference sits inside a FunctionLike nested in the loop (closure capture = "async read").
`ts.isWriteAccess` semantics for the port: token is a declaration name, LHS of any assignment
(simple or compound), operand of `++`/`--`, or `delete` target (compound/inc-dec count as
read+write). The definition identifier itself is excluded by the walker (§6).

`canSkipClone(state, initializer, id)` (L73–76): `!isSymbolReferencedInFile(id, checker,
sourceFile, initializer)` — symbol not referenced inside the VariableDeclarationList (besides its
own definition, which the walker skips). If the initializer doesn't self-reference (`let i = 0`,
not `let i = 0, j = i`), the outer copy var can BE the declared var (skip the `Copy` indirection).

### 5.2 transformForStatementFallback (L102–297)

Setup: `variables = getDeclaredVariables(initializer)` when declaration-list
(`util/getDeclaredVariables.ts` L3–29: flatten all identifiers out of all declaration names,
recursing object/array binding patterns, skipping omitted). For each id, CHECKER:
`getSymbolAtLocation` (L115) and populate `hasWriteOrAsyncRead` / `skipClone` symbol sets
(L113–124).

Initializer emission (L126–209):
- Declaration list: `isVarDeclaration` → `errors.noVar`. FIRST (L132–143), for each
  needs-copy symbol: `state.symbolToIdMap.set(symbol, luau.tempId(id.getText()))` when skipClone,
  else `set(symbol, luau.tempId(id.getText() + "Copy"))` — so the upcoming declaration transform
  emits `local _i = 0` / `local _iCopy = 0` instead of `local i = 0` (transformIdentifierDefined
  consults symbolToIdMap, P2 §3.1). Then transform each declaration (capture prereqs+statements,
  L145–157). Then (L159–203) per needs-copy symbol:
  - skipClone: `tempId = symbolToIdMap.get(symbol)` (the declared `_i` itself).
  - else: `tempId = luau.tempId(id.getText())`; emit `local _i = _iCopy` after the declarations
    (the Copy holds the initializer value; `_i` is the loop-carried slot).
  - `symbolToIdMap.delete(symbol)`; `realId = transformIdentifierDefined(state, id)` (CHECKER) —
    now maps back to the real name.
  - Body head gets `local i = _i` (per-iteration copy; pushed to whileStatements).
  - `finalizerStatements` get `_i = i` (write-back).
- Expression initializer: `transformExpressionStatementInner` (capture; L205–208) — uses the
  statement-form assignment/unary paths (`nodes/statements/transformExpressionStatement.ts`
  L26–84: logical-assignment route, simple/compound assignment via transformWritableAssignment
  with `readAfterWrite = readBeforeWrite = (operator === undefined)`, `++/--` →
  `writable += 1`, else wrapExpressionStatement).

Incrementor (L211–247): guarded by a first-iteration flag:
```
local _shouldIncrement = false
while <cond> do
    if _shouldIncrement then <incrementor stmts (via transformExpressionStatementInner, captured)>
    else _shouldIncrement = true end
    ...
```
Condition (L249–274): captured `createTruthinessChecks(transformExpression(condition))` (or
`true` if absent). Prereqs pushed into whileStatements. If whileStatements is non-empty at this
point (copies, incrementor gate, or condition prereqs): the while condition becomes `true` and,
when a TS condition exists, body head gets `if not <cond> then break end`. Otherwise the truthy
condition is used directly as the while condition.

Body + finalizers (L276–284): body statements appended; if both body and finalizers non-empty →
`addFinalizers(whileStatements, head, finalizerStatements)`; then unless the body already ends in
a final statement (return/break/continue), append finalizers at the end.

`addFinalizers` (L35–71) — recursive linked-list walk over the emitted Luau statements: before
every `ContinueStatement`, splice in a CLONE of the finalizer list (clone per site; parents fixed
up L44–57); recurse into DoStatement bodies and IfStatement branches incl. else-if chains
(`addFinalizersToIfStatement` L22–33); advance `node.next`. Does NOT recurse into nested
loops (their `continue` targets the inner loop) — While/Repeat/For statement bodies are not
visited. So `continue` runs `_i = i` write-backs before re-testing the condition.

Result wrapping (L294–296): if more than one statement accumulated before the while (initializer
locals, copies, shouldIncrement), wrap everything in `do ... end` (DoStatement) to scope the temps;
a bare while is returned unwrapped.

### 5.3 transformForStatementOptimized (L392–489) — numeric-for fast path

Gated by `state.data.projectOptions.optimizedLoops` (L492; default true in upstream project
options). Bails (returns undefined → fallback) unless ALL hold:
- initializer is a declaration list with EXACTLY one `ts.isIdentifier` declaration WITH initializer;
  CHECKER: `getSymbolAtLocation(decName)` (L408) exists; `isProbablyInteger(decInit)`.
- incrementor exists and `getOptimizedIncrementorStepValue` (L299–332) yields a step: `i += <int
  literal>` where CHECKER: `getSymbolAtLocation(incrementor.left) === idSymbol` (L303); **QUIRK
  (L310–315): the `-=` branch does NOT check the LHS symbol** (verbatim — `j -= 1` as incrementor
  of an `i` loop would match); `++i/i++` → 1, `--i/i--` → −1 with symbol check (L317–330).
  `isProbablyInteger` (L368–390): numeric literal with `Number.isInteger(Number(getText()))`;
  recursively `+ - * **` binaries of probably-integers; prefix `+`/`-`; `isSizeMacro` (L334–346:
  call whose CHECKER: `getNonOptionalType(getType(expression.expression))` (L336) →
  `getFirstDefinedSymbol` has a property-call macro named `size`); or CHECKER:
  `isDefinitelyType(state.getType(expression), t => t.isNumberLiteral() && Number.isInteger(t.value))` (L386).
- condition is `<` or `<=` with step > 0, or `>` `>=` with step < 0 (L430–455; wrong-direction or
  `===`/`!==` → bail); `isProbablyInteger(condition.right)`.
- `!isMutatedInBody(state, decName, statement)` (L348–366): §6 walker over the body; a reference
  is a mutation when `skipUpwards(token).parent` is an AssignmentExpression with
  `skipDownwards(parent.left) === token` or a `ts.isUnaryExpressionWithWrite` (++/--) with
  `skipDownwards(parent.operand) === token`.
Emission (L467–488): `id = transformIdentifierDefined(decName)`; start/end captured transforms
(prereqs to result); `step = luau.number(stepValue)`; `<` → `end = offset(end, -1)`, `>` →
`offset(end, 1)` (P2 §0.3 constant-folding); emits
`NumericForStatement { id, start, end, step, statements }` — `for i = 0, n - 1 do`. NOTE: no
closure-copy machinery here (loop var of a numeric for is per-iteration in Luau already; mutation
in body was excluded).

---

## 6. Reference-walker spec (`ts.FindAllReferences.Core` internals → Go)

Used at: checkVariableHoist.ts:23 (`eachSymbolReferenceInFile`), transformForStatement.ts:79
(`eachSymbolReferenceInFile`), :75 (`isSymbolReferencedInFile`), :350
(`eachSymbolReferenceInFile`). All callers pass a `searchContainer` (caseBlock / forStatement /
declaration list / loop body) and a `definition` identifier that IS the declaration name.

TypeScript implementation semantics (services/findAllReferences.ts) — what the Go walker must do:
```
eachSymbolReferenceInFile(definition, checker, sourceFile, cb, searchContainer = sourceFile):
    symbol = checker.getSymbolAtLocation(definition)        // (parameter-property special case n/a here)
    if !symbol: return undefined
    for each Identifier token, in source order, within searchContainer's span:
        // upstream finds candidates by text-scanning sourceFile.text for symbol.name and
        // resolving each position to its touching token; an AST walk over identifiers with
        // token.text === definition.text is equivalent
        if token === definition: continue
        if token.text !== definition.text: continue
        refSymbol = checker.getSymbolAtLocation(token)
        if refSymbol === symbol
           || checker.getShorthandAssignmentValueSymbol(token.parent) === symbol
           || (token.parent is ExportSpecifier && localSymbolOfExportSpecifier == symbol):
            res = cb(token)
            if res: return res          // early-out on first truthy result
    return undefined
isSymbolReferencedInFile(definition, checker, sourceFile, searchContainer) =
    eachSymbolReferenceInFile(..., () => true, searchContainer) === true
```
Go port minimal requirements (all Phase 2b call sites operate on block-scoped locals, single
file): recursive AST walk of `searchContainer` in document order; visit every Identifier node;
skip the definition node itself; cheap text pre-filter (`name == symbol.Name`); resolve via
CHECKER: `GetSymbolAtLocation` and compare symbol identity; also test the shorthand-property
value-symbol case (`{ x }` references); the export-specifier case can be omitted-or-stubbed for
locals but document the divergence. Must support early termination (boolean callback). No
cross-file or alias-following semantics needed.

---

## 7. Diagnostics raisable by Phase 2b transforms

| Name | Message | Raised at |
|---|---|---|
| noFunctionExpressionName | "Function expression names are not supported!" | transformFunctionExpression:13 |
| noAsyncGeneratorFunctions | "Async generator functions are not supported!" | transformFunctionDeclaration:42; transformFunctionExpression:31; transformMethodDeclaration:55 |
| noLuaTupleDestructureAssignmentExpression | "Cannot destructure LuaTuple<T> expression outside of an ExpressionStatement!" | transformBinaryExpression:157 |
| noIterableIteration | "Iterating on Iterable<T> is not supported! You must use a more specific type." | getAccessorForBindingType:157; getLoopBuilder:430 |
| noMacroUnion | "Macro cannot be applied to a union type!" | getLoopBuilder:433 |
| noAwaitForOf | "`await` is not supported in for-of loops!" | transformForOfStatement:481 |
| noVar | (P2 §11.3) | transformVariableDeclarationList:178; transformForStatementFallback:129 |
| noPrivateIdentifier | (P2 §11.3) | objectAccessor:32; transformMethodDeclaration:27 |
| noMixedTypeCall | (P2 §11.3) | isMethodInner:73 (now reachable via transformParameters/isMethod) |
| expectedMethodGotFunction / expectedFunctionGotMethod | (P2 §11.3) | validateMethodAssignment:21/23 |
| noInvalidIdentifier / noReservedIdentifier | (P2 §11.3) | validateIdentifier at: function decl name, params, binding elements, for-of declaration names |
| noAny | (P2 §11.3) | validateNotAnyType at binding-pattern roots (array:17, object:18) and via addIndexDiagnostics in objectAccessor |

Rotor status: ALL of the above already exist in `internal/transformer/diagnostics.go`
(noFunctionExpressionName L159, noAsyncGeneratorFunctions L223, upstream rest-spread rejection,
noLuaTupleDestructureAssignmentExpression L168, noIterableIteration L231, noMacroUnion L276,
noAwaitForOf L219, plus the P2 set). **No new diagnostics need porting for Phase 2b.**

---

## 8. Inventory

### 8.1 Reference files digested (all read in full)
Functions: `nodes/statements/transformFunctionDeclaration.ts`,
`nodes/expressions/transformFunctionExpression.ts` (covers ArrowFunction),
`nodes/transformParameters.ts`, `nodes/transformInitializer.ts`, `util/isMethod.ts`,
`util/wrapStatementsAsGenerator.ts`, `nodes/transformMethodDeclaration.ts`,
`util/validateMethodAssignment.ts`, `nodes/statements/transformReturnStatement.ts`,
`util/isBlockedByTryStatement.ts`, `util/wrapReturnIfLuaTuple.ts`,
`nodes/transformSourceFile.ts` (export-pair rules), `nodes/statements/transformBreakStatement.ts`,
`nodes/statements/transformContinueStatement.ts`,
`nodes/statements/transformExpressionStatement.ts` (Inner form),
`nodes/expressions/transformOmittedExpression.ts` (returns `luau.nil()`).
NOTE: `util/isBlockedByIsolatedContainer.ts` named in the task does NOT exist in the reference.
Destructuring: `nodes/binding/transformBindingName.ts`, `transformArrayBindingPattern.ts`,
`transformObjectBindingPattern.ts`, `transformArrayAssignmentPattern.ts`,
`transformObjectAssignmentPattern.ts`, `util/binding/getAccessorForBindingType.ts`,
`util/binding/objectAccessor.ts`, `nodes/statements/transformVariableStatement.ts` (full),
`util/arrayBindingPatternContainsHoists.ts`, `expressions/transformBinaryExpression.ts` L36–184
(destructure paths), `util/getDeclaredVariables.ts`.
for-of/for/switch: `nodes/statements/transformForOfStatement.ts`,
`nodes/statements/transformForStatement.ts`, `nodes/statements/transformSwitchStatement.ts`,
`util/checkVariableHoist.ts`.
Types: `util/types.ts` L139–179 (isSetType, isMapType, isGeneratorType, isIterableFunctionType,
isLuaTupleType, isIterableFunctionLuaTupleType, isIterableType).
Diagnostics: `Shared/diagnostics.ts` (Phase 2b factories), rotor `internal/transformer/diagnostics.go`.

### 8.2 CHECKER call-site inventory (new in Phase 2b)
TypeChecker methods:
- `getTypeOfAssignmentPattern` — transformArrayAssignmentPattern.ts:24;
  transformObjectAssignmentPattern.ts:25,55. (NEW API for the port.)
- `getSymbolAtLocation` — transformFunctionDeclaration.ts:33; transformVariableStatement.ts:22;
  arrayBindingPatternContainsHoists.ts:14; transformForStatement.ts:115,133,160,303,319,326;
  transformForOfStatement.ts (via getFirstDefinedSymbol):443; plus walker resolutions (§6).
- `getNonOptionalType` — transformForStatement.ts:336 (isSizeMacro).
- `isTupleType` — transformForOfStatement.ts:314.
- `typeToString` — getAccessorForBindingType.ts:166; transformForOfStatement.ts:436 (assert text).
- `getContextualTypeForObjectLiteralElement` — validateMethodAssignment.ts:31.
- `getContextualType` — validateMethodAssignment.ts:50.
- `getTypeOfPropertyOfType` — validateMethodAssignment.ts:42,54,55.
- `getTypeAtLocation` raw (skipDownwards, not state.getType) — transformReturnStatement.ts:14.
- `ts.FindAllReferences.Core.eachSymbolReferenceInFile` — checkVariableHoist.ts:23;
  transformForStatement.ts:79,350; `isSymbolReferencedInFile` — transformForStatement.ts:75
  (internal API — Go scoped walker per §6).
Type/Signature object API: `type.getCallSignatures()` (isMethod.ts:55; validateMethodAssignment.ts:11;
transformForOfStatement.ts:303), `signature.thisParameter?.valueDeclaration` (isMethod.ts:56),
`signature.declaration` (isMethod.ts:63), `signature.getReturnType()` (transformForOfStatement.ts:303),
`type.aliasTypeArguments` (:305–308), `(type as TupleTypeReference).target` + `combinedFlags &
ts.ElementFlags.Rest` + `elementFlags.length` + `labeledElementDeclarations` (:315–321),
`type.getProperties()` (validateMethodAssignment.ts:53), `type.isUnion()` (getLoopBuilder:432),
`type.isNumberLiteral()/.value` (transformForStatement.ts:386), `state.getType(...).flags &
TypeFlags.Void` (isMethod.ts:23,58).
Node-level TS helpers (new): `ts.isThisIdentifier`, `ts.isThis`, `ts.isFunctionBody`,
`ts.isFunctionLike`, `ts.isFunctionLikeDeclaration`, `ts.hasSyntacticModifier(ExportDefault|Async)`,
`ts.hasDecorators`, `ts.getAllSuperTypeNodes`, `ts.getPropertyNameForPropertyNameNode`,
`ts.isWriteAccess`, `ts.isAssignmentExpression`, `ts.isUnaryExpressionWithWrite`,
`ts.isOmittedExpression`, `ts.isBindingElement`, `ts.isCaseClause`, `ts.isEmptyStatement`,
`ts.ElementFlags.Rest`, `ts.SymbolFlags.Prototype`.
Macro symbol identities: SYMBOL_NAMES `$range` (for-of), `$tuple` (return), Set/ReadonlySet/WeakSet,
Map/ReadonlyMap/WeakMap, Generator, IterableFunction, Iterable, `_nominal_LuaTuple`; property-call
macro named `size` (isSizeMacro).

### 8.3 Runtime-lib entries surfaced (Phase 3)
`TS.async` (async functions/methods/expressions), `TS.generator` (generator wrap),
`TS.TRY_RETURN` (return-in-try; with `state.markTryUses("usesReturn")`), `TS.TRY_BREAK`/
`TS.TRY_CONTINUE` (break/continue-in-try). Phase 2b emits diagnostics-only or `state.TS` stubs.

### 8.4 Phase 2b diagnostic name list (complete)
noFunctionExpressionName, noAsyncGeneratorFunctions, upstream rest-spread rejection,
noLuaTupleDestructureAssignmentExpression, noIterableIteration, noMacroUnion, noAwaitForOf,
noVar, noPrivateIdentifier, noMixedTypeCall, expectedMethodGotFunction, expectedFunctionGotMethod,
noInvalidIdentifier, noReservedIdentifier, noAny. All present in rotor's diagnostics.go — none new.
