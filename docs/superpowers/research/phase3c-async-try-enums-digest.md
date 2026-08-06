# Phase 3c Digest — async/await, generators, try/catch/finally, enums, namespaces

Source of truth for porting the Phase 3c set without re-reading the TS. All upstream paths
relative to `reference/roblox-ts/src/` unless prefixed; tsgo paths relative to repo root.
Every checker/type-API usage is flagged `CHECKER:`. Builds on `phase2-transforms-digest.md`,
`phase2-transformstate-digest.md`, `phase3-imports-digest.md` (module-export machinery),
`phase3b-digest.md` (for-of builders consume `.next`; macro tables). SCOPE: (1) TS.async /
TS.await, (2) TS.generator + yield/yield*, (3) transformTryStatement + the COMPLETE
TRY_RETURN/TRY_BREAK/TRY_CONTINUE flow-control rerouting, (4) transformEnumDeclaration all
variants, (5) transformModuleDeclaration (namespaces), (6) diagnostics delta. All worked
examples in §6 were oracle-verified 2026-06-07 with rbxtsc 3.0.0 (`testdata/diff/project`,
`rbxtsc --type model`); outputs are EXACT bytes from `out/_scratch3c.luau`.

Rotor hook points already in place (do not re-port): the three async/generator NYS stubs in
`internal/transformer/functions.go` (L66-78 decl, L116-127 expr, L163-172 method); the
documented try-rerouting no-ops (`internal/transformer/statements.go:364-370` NOTE +
`transformBreakStatement`/`transformContinueStatement` at statements.go:447-463, and
`transformReturnStatementInner` at statements.go:401-427 which omits upstream L51-63);
`state.go:100` promises the tryUsesStack; `DiagNoAwaitForOf` already fires in
`forof.go:498-500`; `getConstantValueLiteral` (const-enum access inlining) already ported at
`access.go:171-181`; `ExportInfo` namespace-export plumbing already live in
`statementlist.go:19-22,64-72`; module-id machinery (`SetModuleIDBySymbol`,
`GetModuleIDPropertyAccess`, `GetModuleExports`, `IsSymbolMutable`, `isSymbolOfValue`,
`checker.SkipAlias`) all live in `state.go`/`exports.go`; hoisting (`IsHoisted`,
`HoistsByStatement`, `createHoistDeclaration`) live in `identifier.go`/`statementlist.go`.

---

## 1. async/await

### 1.1 Runtime contract (context only — emitted code calls these)

`RuntimeLib.lua:136-151`: `TS.async(callback)` returns a function that wraps `callback` in
`Promise.new` + `coroutine.wrap` + `pcall`, resolving with the callback's (single) return
value or rejecting with the pcall error. `RuntimeLib.lua:153-166`: `TS.await(promise)` —
non-Promise values pass through unchanged (`if not Promise.is(promise) then return promise end`),
resolved promises return the value, rejected ones `error(value, 2)`, cancelled ones
`error("The awaited Promise was cancelled", 2)`.

### 1.2 transformAwaitExpression (expressions/transformAwaitExpression.ts L7-9) — ENTIRE transform

```
transformAwaitExpression(state, node):
    return luau.call(state.TS(node, "await"),
                     [transformExpression(state, skipDownwards(node.expression))])
```

That is the whole file. No diagnostics, no type checks, no async-context validation (the TS
checker already rejects `await` outside async functions). `await x` used as an expression
statement flows through wrapExpressionStatement and survives as a CallStatement (it is a
call). rotor: one-liner using `s.RuntimeLib(node, "await")` + `SkipDownwards`.

### 1.3 The TS.async wrapper — three production sites

`isAsync = ts.hasSyntacticModifier(node, ts.ModifierFlags.Async)` at every site
(rotor: `ast.HasSyntacticModifier(node, ast.ModifierFlagsAsync)` — already computed at all
three stub sites).

**(a) transformFunctionDeclaration.ts L38-75.** After parameters+body are built and
`localize` decided (existing rotor code, functions.go:55-62):

```
isAsync = hasSyntacticModifier(node, Async)
if node.asteriskToken:                       // generator (see §2)
    if isAsync: addDiagnostic(noAsyncGeneratorFunctions(node))
    statements = wrapStatementsAsGenerator(state, node, statements)
if isAsync:
    right = luau.call(state.TS(node, "async"),
                      [FunctionExpression{hasDotDotDot, parameters, statements}])
    if localize: return [ VariableDeclaration{left: name, right} ]      // local f = TS.async(...)
    else:        return [ Assignment{left: name, op: "=", right} ]      // f = TS.async(...)  (hoisted)
else:
    return [ FunctionDeclaration{localize, name, statements, parameters, hasDotDotDot} ]
```

NOTE the async path REPLACES the FunctionDeclaration emit entirely — a hoisted async
function becomes `local f` (hoist decl at the premature-use statement) + `f = TS.async(...)`
(oracle §6.1 `fetchValue`). An `export default async function` keeps name `default` and is
always localized.

**(b) transformFunctionExpression.ts L27-46** (FunctionExpression + ArrowFunction):

```
isAsync = hasSyntacticModifier(node, Async)
if node.asteriskToken:                       // FunctionExpression only; arrows can't be generators
    if isAsync: addDiagnostic(noAsyncGeneratorFunctions(node))
    statements = wrapStatementsAsGenerator(state, node, statements)
expression = FunctionExpression{hasDotDotDot, parameters, statements}
if isAsync: expression = luau.call(state.TS(node, "async"), [expression])
return expression
```

rotor: `node.AsArrowFunction()` has no AsteriskToken field — keep the existing
`if ast.IsFunctionExpression(node)` guard (functions.go:113-115).

**(c) transformMethodDeclaration.ts L51-97.** isAsync computed BEFORE the
`function class:name()` shape decision and EXCLUDES it (L61: `if (!isAsync && ...)`).
Generator wrap (L53-58) happens first and does NOT exclude the method shape — a generator
method may still emit as `function Class:m()` whose body is `return TS.generator(...)`.
Async methods fall to the map-pointer path with
`expression = luau.call(state.TS(node, "async"), [FunctionExpression{...}])` (L95-97) —
note `self` stays in `parameters` (oracle §6.3: `work = TS.async(function(self, n)`).

### 1.4 noAwaitForOf

transformForOfStatement.ts L480-482: `if (node.awaitModifier) addDiagnostic(errors.noAwaitForOf(node))`
— ALREADY PORTED (forof.go:498-500). Nothing to do.

---

## 2. Generators

### 2.1 Runtime contract

`RuntimeLib.lua:240-258`: `TS.generator(callback)` creates `co = coroutine.create(callback)`
and returns a table with one field `next = function(...)`: dead coroutine → `{ done = true }`;
otherwise `coroutine.resume(co, ...)` (errors rethrown `error(value, 2)`), returning
`{ value = value, done = coroutine.status(co) == "dead" }`. Phase 3b's for-of generator
builder already consumes this `.next` protocol.

### 2.2 wrapStatementsAsGenerator (util/wrapStatementsAsGenerator.ts L5-17) — ENTIRE util

```
wrapStatementsAsGenerator(state, node, statements):
    return luau.list.make(
        ReturnStatement{ expression:
            luau.call(state.TS(node, "generator"),
                      [FunctionExpression{hasDotDotDot: false, parameters: [], statements}]) })
```

i.e. the (already fully transformed) body is swapped for
`return TS.generator(function() <body> end)`. The OUTER function keeps the original
parameters; the inner wrapper function takes none. Applied at the three sites in §1.3 when
`node.asteriskToken` is set. Declaration-site generators stay real FunctionDeclarations
(`local function gen() return TS.generator(...) end` — oracle §6.1), unlike async which
switches to a variable.

### 2.3 transformYieldExpression (expressions/transformYieldExpression.ts L8-54) — COMPLETE

`yield` is an EXPRESSION kind (dispatch in transformExpression). Three cases:

```
transformYieldExpression(state, node):
    if !node.expression:
        return luau.call(luau.globals.coroutine.yield, [])          // bare `yield`
    expression = transformExpression(state, node.expression)
    if node.asteriskToken:                                          // yield*
        loopId = luau.tempId("result")
        finalizer = list( BreakStatement{} )
        evaluated = luau.none()
        if !isUsedAsStatement(node):
            returnValue = state.pushToVar(undefined, "returnValue") // `local _returnValue` (no init)
            finalizer.unshift( Assignment{left: returnValue, op: "=",
                                          right: luau.property(loopId, "value")} )
            evaluated = returnValue
        state.prereq( ForStatement{                                 // generic for
            ids: [loopId],
            expression: luau.property(convertToIndexableExpression(expression), "next"),
            statements: [
                IfStatement{ condition: luau.property(loopId, "done"),
                             statements: finalizer, elseBody: [] },
                CallStatement{ luau.call(luau.globals.coroutine.yield,
                                         [luau.property(loopId, "value")]) },
            ]})
        return evaluated
    else:
        return luau.call(luau.globals.coroutine.yield, [expression])
```

Notes:
- `yield v` → `coroutine.yield(v)`; resume arguments surface as the call's return value, so
  `const got = yield 1` → `local got = coroutine.yield(1)` (oracle §6.4).
- `yield*` lowers to a generic for over the inner generator's bare `.next` (NO parens — the
  function value itself is the iterator), re-yielding each value; when the result is used,
  the inner generator's RETURN value (`_result.value` at `done`) is captured in
  `_returnValue` BEFORE `break` and becomes the expression value. Order of emitted
  statements: `local _returnValue` (pushToVar, no initializer) THEN the for loop (both
  prereqs), then use site (oracle §6.1 `gen2`).
- `isUsedAsStatement` (util/isUsedAsStatement.ts) is already ported
  (`internal/transformer` — used by arraymacros); `convertToIndexableExpression` likewise.
- Temp creation order for byte parity: `loopId` ("result") is created BEFORE
  `returnValue` ("returnValue").

---

## 3. try/catch/finally + flow-control rerouting (THE critical section)

### 3.1 Runtime protocol (RuntimeLib.lua:183-238) — exact contract the emit targets

```lua
TS.TRY_RETURN = 1
TS.TRY_BREAK = 2
TS.TRY_CONTINUE = 3

function TS.try(try, catch, finally)  -- returns: exitType, returns
```

Semantics (needed to understand WHY the emit shapes are correct, and for porting tests):
- `try` is called via `pcall`. Its return values are `exitType, returns` — i.e. the try
  callback returns NOTHING on normal completion (exitType nil), `TS.TRY_BREAK` /
  `TS.TRY_CONTINUE` (one value) for rerouted break/continue, or
  `TS.TRY_RETURN, { values... }` for rerouted return.
- If try errored and `catch` exists, `catch(tryError)` is pcalled; a non-nil exitType from
  catch OVERRIDES (`exitType, returns = newExitType, newReturns`).
- `finally()` is then called UNPROTECTED (no pcall); a non-nil exitType from finally
  overrides everything (this is why `return` inside `finally` also reroutes — §6.3
  `finReturn`).
- If the final exitType is NOT one of the three flow constants: a catch-thrown error is
  rethrown (`error(catchError, 2)`); a try error with no catch is rethrown.
- Returns `exitType, returns` to the caller.

### 3.2 TransformState additions (classes/TransformState.ts L68-97, types.ts L7-11)

```go
type TryUses struct { UsesReturn, UsesBreak, UsesContinue bool }
```

- `tryUsesStack` — plain stack of `*TryUses`.
- `pushTryUsesStack()` — push a fresh all-false TryUses, RETURN it (the caller keeps the
  pointer; flags are read after pop).
- `markTryUses(property)` — set the flag on the TOP entry; **no-op when the stack is empty**
  (L87-89) — this is what makes return/break/continue outside any try free.
- `popTryUsesStack()`.

State.go already reserves the spot (state.go:100 comment).

### 3.3 transformTryStatement (statements/transformTryStatement.ts L188-200) — driver

```
transformTryStatement(state, node):
    statements = []
    exitTypeId = luau.tempId("exitType")     // created FIRST — temp numbering parity
    returnsId  = luau.tempId("returns")      // created SECOND
    tryUses = state.pushTryUsesStack()
    statements.push( transformIntoTryCall(state, node, exitTypeId, returnsId, tryUses) )
    state.popTryUsesStack()                  // POP BEFORE transformFlowControl —
    statements.pushList( transformFlowControl(state, node, exitTypeId, returnsId, tryUses) )
    return statements                        //   markTryUses inside it hits the OUTER try
```

ORDERING IS LOAD-BEARING twice: (1) exitTypeId/returnsId are allocated before the try body
is transformed, so in nested tries the OUTER pair gets the unsuffixed names `_exitType`,
`_returns` and the inner pair `_exitType_1`, `_returns_1` even though the inner declaration
renders first textually (oracle §6.2 `f8`); (2) the pop-before-flow-control means the
propagation marks in §3.6 land on the ENCLOSING try's TryUses — that is the entire
nested-try tunneling mechanism.

### 3.4 transformCatchClause (L13-30)

```
parameters = []; statements = []
if node.variableDeclaration:
    parameters.push( transformBindingName(state, node.variableDeclaration.name, statements) )
statements.pushList( transformStatementList(state, node.block, node.block.statements) )
return FunctionExpression{parameters, hasDotDotDot: false, statements}
```

- `catch (e)` → `function(e)`; `catch` (no binding) → `function()` (oracle §6.2 `f4`).
- Binding patterns route through the already-ported `transformBindingName`
  (binding.go:23) — destructure statements land BEFORE the block's statements.
- rotor: `node.AsCatchClause().VariableDeclaration` / `.Block`; the variableDeclaration's
  `.Name()` is the binding name.

### 3.5 transformIntoTryCall (L32-76)

```
tryCallArgs = [ FunctionExpression{params: [], statements:
                    transformStatementList(state, node.tryBlock, node.tryBlock.statements)} ]
if node.catchClause: tryCallArgs.push( transformCatchClause(state, node.catchClause) )
else:                assert(node.finallyBlock); tryCallArgs.push( luau.nil() )   // placeholder
if node.finallyBlock:
    tryCallArgs.push( FunctionExpression{params: [], statements:
                          transformStatementList(state, node.finallyBlock, ...)} )

if !tryUses.usesReturn && !tryUses.usesBreak && !tryUses.usesContinue:
    return CallStatement{ luau.call(state.TS(node, "try"), tryCallArgs) }        // bare TS.try(...)
return VariableDeclaration{ left: list(exitTypeId, returnsId),                   // local a, b = TS.try(...)
                            right: luau.call(state.TS(node, "try"), tryCallArgs) }
```

- try-only + catch: 2 args. try + finally (no catch): `TS.try(f, nil, fin)` — the explicit
  `nil` placeholder (oracle §6.2 `f2`). try+catch+finally: 3 args.
- The flags are read AFTER the three blocks are transformed (the same `tryUses` object the
  block transforms mutated via markTryUses) — this works because the returned statement is
  CONSTRUCTED after transformStatementList ran. In Go simply build args first, then choose
  the statement shape.
- NOTE the two-id VariableDeclaration is emitted even when only break/continue are used
  (`local _exitType, _returns = TS.try(...)` — `_returns` stays nil; oracle §6.2 `f6`).

### 3.6 transformFlowControl (L109-186) + collapseFlowControlCases (L89-107) — COMPLETE

```
transformFlowControl(state, node, exitTypeId, returnsId, tryUses):
    flowControlCases = []
    if no flags set: return []                                       // bare-call case

    returnBlocked = isReturnBlockedByTryStatement(node.parent)       // NOTE: node.parent!
    breakBlocked  = isBreakBlockedByTryStatement(node.parent)

    // propagation to the ENCLOSING try (stack already popped — see §3.3):
    if tryUses.usesReturn   && returnBlocked: state.markTryUses("usesReturn")
    if tryUses.usesBreak    && breakBlocked:  state.markTryUses("usesBreak")
    if tryUses.usesContinue && breakBlocked:  state.markTryUses("usesContinue")

    if tryUses.usesReturn:
        if returnBlocked:
            cases.push({ condition: exitTypeId == TS.TRY_RETURN,
                         statements: [ return exitTypeId, returnsId ] })   // re-tunnel both values
            if breakBlocked: return collapse(exitTypeId, cases)            // EARLY EXIT (see note)
        else:
            cases.push({ condition: exitTypeId == TS.TRY_RETURN,
                         statements: [ return unpack(returnsId) ] })       // real return

    if tryUses.usesBreak || tryUses.usesContinue:
        if breakBlocked:
            cases.push({ condition: NONE,                                  // unconditional tail
                         statements: [ return exitTypeId ] })              // re-tunnel flag only
        else:
            if tryUses.usesBreak:
                cases.push({ condition: exitTypeId == TS.TRY_BREAK,    statements: [ break ] })
            if tryUses.usesContinue:
                cases.push({ condition: exitTypeId == TS.TRY_CONTINUE, statements: [ continue ] })

    return collapse(exitTypeId, cases)
```

`createFlowControlCondition` (L78-85): `luau.binary(exitTypeId, "==", state.TS(node, "TRY_RETURN"|"TRY_BREAK"|"TRY_CONTINUE"))`
— the constants are RuntimeLib property reads (`TS.TRY_RETURN` etc.), each via state.TS so
runtime-lib usage is flagged.

`collapseFlowControlCases` (L89-107): builds an elseif chain from the cases, with one twist —
**the LAST case's condition is REPLACED by the bare `exitTypeId`** (truthiness test):

```
collapse(exitTypeId, cases):                       // assert(cases.length > 0)
    next = IfStatement{ condition: exitTypeId,     // last case: bare truthy check
                        statements: cases[last].statements, elseBody: [] }
    for i = len-2 .. 0:
        next = IfStatement{ condition: cases[i].condition or exitTypeId,
                            statements: cases[i].statements, elseBody: next }
    return [ next ]
```

Consequences (all oracle-verified, §6.2):
- Single case → `if _exitType then <stmts> end` regardless of which flag (f5, f7-inner-style,
  contOnly, finReturn, sw).
- Two cases → `if _exitType == TS.TRY_RETURN then ... elseif _exitType then ... end` (f7) or
  `if _exitType == TS.TRY_BREAK then break elseif _exitType then continue end` (f6).
- The early exit when `returnBlocked && breakBlocked`: the single TRY_RETURN-conditioned
  case collapses to a bare `if _exitType then return _exitType, _returns end`, which
  correctly re-tunnels ALL THREE flags (TRY_BREAK/TRY_CONTINUE ride along with returns=nil) —
  so the break/continue section is intentionally skipped.
- INVARIANT (derivable from §3.7, useful for tests): breakBlocked ⟹ returnBlocked — a try
  found before any loop/switch is necessarily found before any function boundary. So the
  `returnBlocked=false && breakBlocked=true` combination is impossible.

### 3.7 isBlockedByTryStatement (util/isBlockedByTryStatement.ts L3-18) — ENTIRE util

```
isReturnBlockedByTryStatement(node):
    ancestor = findAncestor(node, a => isTryStatement(a) || isFunctionLikeDeclaration(a))
    return ancestor != nil && isTryStatement(ancestor)

isBreakBlockedByTryStatement(node):
    ancestor = findAncestor(node, a => isTryStatement(a) || isIterationStatement(a, false)
                                       || isSwitchStatement(a))
    return ancestor != nil && isTryStatement(ancestor)
```

Boundary rules (the "nearest wins" semantics):
- `return`: blocked iff a TryStatement is hit before any function-like. A function inside a
  try resets — returns inside it are plain.
- `break`/`continue`: blocked iff a TryStatement is hit before any loop OR switch. A loop
  inside try → break belongs to the loop (plain). A try inside a case clause → break is
  rerouted as TRY_BREAK and the post-try `if _exitType then break end` breaks the switch's
  `repeat ... until true` (oracle §6.3 `sw` — NO switch-transform changes needed).
- IMPORTANT: upstream calls these with the STATEMENT node for break/continue/bare-return,
  with `returnExp` (the expression) for value returns, and with `node.parent` for the try's
  own blocked checks. findAncestor in tsgo includes the start node itself — upstream
  ts.findAncestor does too, and since a BreakStatement/Expression is never a
  TryStatement/loop itself the inclusive/exclusive distinction is moot, EXCEPT for
  `node.parent` at the try site which deliberately starts above the try.
- tsgo: `ast.FindAncestor`, `ast.IsTryStatement`, `ast.IsFunctionLikeDeclaration` exist;
  `ts.isIterationStatement(a, false)` (lookInLabeledStatements=false) — tsgo has
  `ast.IsIterationStatement(node)` (no labels param; labels are banned in roblox-ts anyway —
  CHECK signature at port time, the no-label variant is equivalent here since a labeled
  statement would already have errored).

### 3.8 The three flag producers (COMPLETE list of consumers/producers)

Grep-verified exhaustive: ONLY transformReturnStatement.ts, transformBreakStatement.ts,
transformContinueStatement.ts produce flags; ONLY transformTryStatement.ts consumes them
(plus TransformState owns the stack). Loops, switch, and functions need NO changes.

**transformBreakStatement.ts L8-25** (replace rotor statements.go:447-453):

```
if node.label: addDiagnostic(noLabeledStatement(node.label)); return []
if isBreakBlockedByTryStatement(node):
    state.markTryUses("usesBreak")
    return [ ReturnStatement{ expression: state.TS(node, "TRY_BREAK") } ]   // return TS.TRY_BREAK
return [ BreakStatement{} ]
```

**transformContinueStatement.ts L8-25** — identical with `usesContinue` / `"TRY_CONTINUE"` /
ContinueStatement.

**transformReturnStatement.ts L51-63 and L71-84** (extend rotor
transformReturnStatementInner + transformReturnStatement):

```
// in transformReturnStatementInner, AFTER expression is computed (incl. LuaTuple handling):
if isReturnBlockedByTryStatement(returnExp):
    state.markTryUses("usesReturn")
    result.push( ReturnStatement{ expression: list(
        state.TS(returnExp, "TRY_RETURN"),
        Array{ members: isList(expression) ? expression : list(expression) } ) })
        // i.e.  return TS.TRY_RETURN, { <value(s)> }
else:
    result.push( ReturnStatement{expression} )

// in transformReturnStatement, bare `return`:
if !node.expression:
    if isReturnBlockedByTryStatement(node):
        state.markTryUses("usesReturn")
        return [ ReturnStatement{ expression: list(state.TS(node, "TRY_RETURN"), luau.array()) } ]
        // return TS.TRY_RETURN, {}
    return [ ReturnStatement{ expression: luau.nil() } ]
```

The returned values are ALWAYS boxed in an array literal (LuaTuple multi-returns spread as
array MEMBERS — `expression` is already a luau.list in that case); the unbox happens at the
consumer (`return unpack(_returns)` for a real return, or pass-through for tunneling).

Dead-code elision keeps working unchanged: the rerouted forms are still ReturnStatements,
so `luau.IsFinalStatement` truncation in TransformStatementList behaves identically.

---

## 4. Enums (statements/transformEnumDeclaration.ts) — COMPLETE

### 4.1 needsInverseEntry (L14-16)

```
needsInverseEntry(state, member) = typeof state.typeChecker.getConstantValue(member) !== "string"
```

CHECKER: `getConstantValue(member)` on an EnumMember returns the member's resolved constant
(string | number; never undefined for non-computed; for computed non-foldable members it IS
undefined → "not a string" → still gets an inverse entry).

### 4.2 transformEnumDeclaration (L18-128)

```
transformEnumDeclaration(state, node):
    // const enum: no emit unless preserveConstEnums
    if hasSyntacticModifier(node, ModifierFlags.Const) && compilerOptions.preserveConstEnums !== true:
        return []

    // merging ban
    symbol = typeChecker.getSymbolAtLocation(node.name)                          // CHECKER
    if symbol && hasMultipleDefinitions(symbol, d => isEnumDeclaration(d)
                                                  && !hasSyntacticModifier(d, Const)):
        DiagnosticService.addDiagnosticWithCache(symbol, errors.noEnumMerging(node),
            state.multiTransformState.isReportedByMultipleDefinitionsCache)
        return []

    validateIdentifier(state, node.name)
    left = transformIdentifierDefined(state, node.name)
    isHoisted = symbol != nil && state.isHoisted.get(symbol) == true

    // FAST PATH: all members string-valued → plain map, no inverse table
    if node.members.every(m => !needsInverseEntry(state, m)):
        right = luau.map( members.map(m => [
            state.pushToVarIfComplex(transformPropertyName(state, m.name)),
            luau.string(getConstantValue(m) as string) ]))
        return [ isHoisted ? Assignment{left, "=", right}
                           : VariableDeclaration{left, right} ]

    // GENERAL PATH: setmetatable + inverse, inside a do-block
    statements = state.capturePrereqs(() => {
        inverseId = state.pushToVar(luau.map(), "inverse")               // local _inverse = {}
        state.prereq( Assignment{ left, "=",
            luau.call(setmetatable, [ luau.map(),
                                      luau.map([[ "__index", inverseId ]]) ]) })
        for member in node.members:
            name = transformPropertyName(state, member.name)
            index = expressionMightMutate(state, name,
                        isComputedPropertyName(member.name) ? member.name.expression
                                                            : member.name)
                    ? state.pushToVar(name)          // NOT pushToVarIfComplex — see quirk §9
                    : name
            value = typeChecker.getConstantValue(member)                 // CHECKER
            if typeof value == "string":   valueExp = luau.string(value)
            elif typeof value == "number": valueExp = luau.number(value)
            else:                                                        // computed, non-foldable
                assert(member.initializer)
                valueExp = state.pushToVarIfComplex(
                    transformExpression(state, member.initializer), "value")
            state.prereq( Assignment{ ComputedIndex(left, index), "=", valueExp } )
            if needsInverseEntry(state, member):
                state.prereq( Assignment{ ComputedIndex(inverseId, valueExp), "=", index } )
    })

    list = [ DoStatement{statements} ]
    if !isHoisted: list.unshift( VariableDeclaration{left, right: nil} )  // `local E` header
    return list
```

Shape notes (oracle §6.3):
- General path renders as `local E` / `do local _inverse = {} ; E = setmetatable({}, { __index = _inverse }) ; E.A = 0 ; _inverse[0] = "A" ; ... end`.
  Member/inverse assignments INTERLEAVE per member. `E.A = 0` is a ComputedIndex that the
  renderer collapses to dot-form for valid-identifier string indices; non-identifier keys
  render as `E["a b"] = 1`.
- Heterogeneous enums use the general path; string members simply skip the inverse entry
  (`Hetero.Str = "str"` with no `_inverse[...]`).
- CHECKER constant folding is REAL folding: `X = base * 2` with `const base = 10` emits
  `Computed.X = 20` (the checker's evaluator folds across const locals). Only members the
  evaluator cannot fold (e.g. `"y".size()`) take the initializer-transform path:
  `local _value = #"y"` then `Computed.Y = _value` / `_inverse[_value] = "Y"`.
- Hoisted enums (use-before-declare): NO `local E` unshift; the metatable line becomes the
  pre-existing hoist local's assignment. Hoisted STRING enums become `E = { ... }`
  (Assignment branch of the fast path) — oracle §6.4 `SE`.
- `declare enum` never reaches the transform (declare-modifier skip in dispatch).
- Const enums: zero emit; member ACCESSES are already inlined by the ported
  getConstantValueLiteral (access.go:171) — oracle §6.3 `print(0, 1)`. CHECKER NOTE: tsgo's
  `Checker.GetConstantValue` (tsgo/checker/services.go:821-848) inlines property/element
  accesses ONLY for const enums (`ast.IsEnumConst(member.Parent)`) — byte-identical
  behavior to TS5 (same comment upstream).
- `export enum` needs nothing special — the source-file export table picks the local up
  (non-mutable symbol).

### 4.3 hasMultipleDefinitions (util/hasMultipleDefinitions.ts L3-14) — NEW SMALL UTIL

```
hasMultipleDefinitions(symbol, filter): count symbol.getDeclarations() passing filter; true if > 1
```

rotor: `symbol.Declarations` field. Shared by enums (§4.2) and namespaces (§5.2).

---

## 5. Namespaces (statements/transformModuleDeclaration.ts) — COMPLETE

### 5.1 Entry: transformModuleDeclaration (L124-146)

```
transformModuleDeclaration(state, node):
    if !ts.isInstantiatedModule(node, false): return []        // type-only namespace → no emit
    symbol = typeChecker.getSymbolAtLocation(node.name)        // CHECKER
    if symbol && hasMultipleDefinitions(symbol, d => isDeclarationOfNamespace(d)):
        addDiagnosticWithCache(symbol, errors.noNamespaceMerging(node),
                               isReportedByMultipleDefinitionsCache)
        return []
    assert(!isStringLiteral(node.name))     // `declare module "X"` filtered by declare-skip
    assert(node.body && !isIdentifier(node.body))
    return transformNamespace(state, node.name, node.body)
```

tsgo: `ast.IsInstantiatedModule(node, false)` (tsgo/ast/utilities.go:2409) — the `false` is
preserveConstEnums=false at BOTH upstream call sites (here and §5.2's filter).

### 5.2 isDeclarationOfNamespace (L16-30) — the merge filter

A declaration counts toward "namespace value definitions" iff:
- it has NO `declare` modifier, AND
- (`isModuleDeclaration(d) && isInstantiatedModule(d, false)`) OR
  (`isFunctionDeclaration(d) && d.body`) OR `isClassDeclaration(d)`.

So namespace+interface merging is fine; namespace+namespace, namespace+function-with-body,
namespace+class all trigger noNamespaceMerging (on the symbol, reported once via the cache,
positioned at whichever declaration transforms first = the first one).

### 5.3 getValueDeclarationStatement (L32-44)

For an export symbol, find the STATEMENT that declares its value:

```
for declaration in symbol.getDeclarations() ?? []:
    statement = getAncestor(declaration, ts.isStatement)
    if statement:
        if isFunctionDeclaration(statement) && !statement.body: continue   // overload sigs
        if isTypeAliasDeclaration(statement): continue
        if isInterfaceDeclaration(statement): continue
        if statement has `declare` modifier: continue
        return statement
return undefined
```

### 5.4 transformNamespace (L46-122) — the emit

```
transformNamespace(state, name, body):           // body: ModuleBlock | nested ModuleDeclaration
    symbol = typeChecker.getSymbolAtLocation(name); assert(symbol)        // CHECKER
    validateIdentifier(state, name)
    nameExp = transformIdentifierDefined(state, name)
    statements = []; doStatements = []
    containerId = luau.tempId("container")
    state.setModuleIdBySymbol(symbol, containerId)        // ← export-write redirection root

    statements.push( state.isHoisted.get(symbol)
        ? Assignment{nameExp, "=", luau.map()}            // hoisted:  X = {}
        : VariableDeclaration{nameExp, luau.map()} )      // else:     local X = {}

    moduleExports = state.getModuleExports(symbol)        // CHECKER (cached)
    if moduleExports.length > 0:
        doStatements.push( VariableDeclaration{containerId, nameExp} )   // local _container = X

    if isModuleBlock(body):
        exportsMap = Map<ts.Statement, string[]>()
        if moduleExports.length > 0:
            for exportSymbol in moduleExports:
                originalSymbol = ts.skipAlias(exportSymbol, typeChecker)  // CHECKER
                if isSymbolOfValue(originalSymbol) && !isSymbolMutable(state, originalSymbol):
                    stmt = getValueDeclarationStatement(exportSymbol)
                    if stmt: exportsMap.getOrSet(stmt, []).push(exportSymbol.name)
        doStatements.pushList( transformStatementList(state, body, body.statements,
                                                      { id: containerId, mapping: exportsMap }) )
    else:                                                 // dotted `namespace A.B { ... }`
        doStatements.pushList( transformNamespace(state, body.name, body.body) )
        doStatements.push( Assignment{ luau.property(containerId, body.name.text), "=",
                                       transformIdentifierDefined(state, body.name) } )

    statements.push( DoStatement{doStatements} )
    return statements
```

Behavior summary (oracle §6.3, §6.4):
- `local X = {}` + `do ... end`. With exports: first do-statement is
  `local _container = X`; after each export-declaring statement the statement-list driver
  appends `_container.<name> = <name>` (rotor's ExportInfo already does EXACTLY this —
  statementlist.go:64-72, including the dead-code-break interaction).
- MUTABLE exports (`export let`) are EXCLUDED from exportsMap; instead
  `setModuleIdBySymbol(symbol, containerId)` makes the existing variable/identifier
  machinery emit `_container.mut = 2` at the declaration and `X.mut` reads/writes outside —
  rotor's GetModuleIDPropertyAccess + transformVariableStatement path lights up as soon as
  SetModuleIDBySymbol is called with containerId. No extra work.
- Namespace WITHOUT value exports: do-block with no `_container` line (oracle `NoExports`).
- Nested namespace STATEMENT inside a block: ordinary statement recursion; the inner
  namespace's statements land in the outer doStatements via the statement list, then the
  exportsMap appends `_container.Inner = Inner`.
- DOTTED `namespace A.B {}`: tsgo parses to nested ModuleDeclarations
  (tsgo/parser/parser.go:2210-2233; the nested decl gets an IMPLICIT export modifier with
  `NodeFlagsReparsed`), so `body` is a ModuleDeclaration node → the else-branch recursion +
  explicit `_container.CD = CD` assignment.
- Hoisted namespace: `NS = {}` assignment form (no `local`), hoist header emitted by
  createHoistDeclaration at the premature use (oracle §6.4).
- `declare namespace` / `declare module "X"`: killed by the dispatch-level declare skip;
  `namespace TypesOnly { types only }` killed by isInstantiatedModule.
- import= interplay: nothing new — transformImportEqualsDeclaration (already ported) reads
  the alias; identifier reads of namespace members resolve through normal property access
  on the namespace local. rotor identifier.go:98-107 already excludes non-namespace module
  ancestors for the export-let indirection; namespace-internal reads of own mutable exports
  go through `_container`/`X` correctly via GetModuleIDPropertyAccess (module symbol →
  containerId after SetModuleIDBySymbol).

---

## 6. Oracle-verified worked examples (rbxtsc 3.0.0, exact bytes)

All compiled in `testdata/diff/project` (`--type model`); header lines
`-- Compiled with roblox-ts v3.0.0` + `local TS = require(script.Parent.include.RuntimeLib)`
elided below except where runtime-lib-free.

### 6.1 async + generators

```ts
async function fetchValue(x: number): Promise<number> {
	const a = await Promise.resolve(x);
	const b = await fetchValue(a);
	return a + b;
}
const asyncArrow = async (n: number) => n * 2;
export async function exported(): Promise<void> {
	await asyncArrow(1);
}
function* gen(): Generator<number, string, undefined> {
	yield 1;
	yield 2;
	return "done";
}
function* gen2() {
	yield;
	const v = yield* gen();
	print(v);
}
export function useGen() {
	for (const x of gen2()) {
		print(x);
	}
}
```

```luau
local fetchValue
fetchValue = TS.async(function(x)
	local a = TS.await(TS.Promise.resolve(x))
	local b = TS.await(fetchValue(a))
	return a + b
end)
local asyncArrow = TS.async(function(n)
	return n * 2
end)
local exported = TS.async(function()
	TS.await(asyncArrow(1))
end)
local function gen()
	return TS.generator(function()
		coroutine.yield(1)
		coroutine.yield(2)
		return "done"
	end)
end
local function gen2()
	return TS.generator(function()
		coroutine.yield()
		local _returnValue
		for _result in gen().next do
			if _result.done then
				_returnValue = _result.value
				break
			end
			coroutine.yield(_result.value)
		end
		local v = _returnValue
		print(v)
	end)
end
local function useGen()
	for _result in gen2().next do
		if _result.done then
			break
		end
		local x = _result.value
		print(x)
	end
end
```

Note: `fetchValue` self-references → isHoisted → `local fetchValue` hoist header +
Assignment form of TS.async (async functions are hoist-SENSITIVE unlike plain function
declarations, which self-refer without hoisting — see identifier.go:209-220's async carve-out,
already ported).

### 6.2 try/catch/finally + every rerouting shape

```ts
export function f1() { try { print("try"); } catch (e) { print("caught", e); } }
export function f2() { try { print("try"); } finally { print("finally"); } }
export function f4() { try { print("x"); } catch { print("no binding"); } }
export function f5(): number {
	try { return 1; } catch { return 2; }
	return 3;
}
export function f6() {
	for (let i = 0; i < 10; i++) {
		try {
			if (i === 5) { break; }
			print(i);
		} catch { continue; }
	}
}
export function f7(): number {
	while (true) {
		try { return 42; } catch { break; }
	}
	return 0;
}
export function f8(): number {
	try {
		try { return 1; } catch {}
	} catch {}
	return 2;
}
```

```luau
local function f1()
	TS.try(function()
		print("try")
	end, function(e)
		print("caught", e)
	end)
end
local function f2()
	TS.try(function()
		print("try")
	end, nil, function()
		print("finally")
	end)
end
local function f4()
	TS.try(function()
		print("x")
	end, function()
		print("no binding")
	end)
end
local function f5()
	local _exitType, _returns = TS.try(function()
		return TS.TRY_RETURN, { 1 }
	end, function()
		return TS.TRY_RETURN, { 2 }
	end)
	if _exitType then
		return unpack(_returns)
	end
	return 3
end
local function f6()
	for i = 0, 9 do
		local _exitType, _returns = TS.try(function()
			if i == 5 then
				return TS.TRY_BREAK
			end
			print(i)
		end, function()
			return TS.TRY_CONTINUE
		end)
		if _exitType == TS.TRY_BREAK then
			break
		elseif _exitType then
			continue
		end
	end
end
local function f7()
	while true do
		local _exitType, _returns = TS.try(function()
			return TS.TRY_RETURN, { 42 }
		end, function()
			return TS.TRY_BREAK
		end)
		if _exitType == TS.TRY_RETURN then
			return unpack(_returns)
		elseif _exitType then
			break
		end
	end
	return 0
end
local function f8()
	local _exitType, _returns = TS.try(function()
		local _exitType_1, _returns_1 = TS.try(function()
			return TS.TRY_RETURN, { 1 }
		end, function() end)
		if _exitType_1 then
			return _exitType_1, _returns_1
		end
	end, function() end)
	if _exitType then
		return unpack(_returns)
	end
	return 2
end
```

f8 is the nested-tunneling proof: inner try blocked (returnBlocked from inner.parent finds
the outer try) → `return _exitType_1, _returns_1` re-tunnels; the markTryUses-after-pop set
the OUTER usesReturn so the outer also gets the declaration form. Note empty catch emits
`function() end` (single line).

Catch with a destructured-after-the-fact binding (`catch (e)` then
`const { name } = e as ...`) emits `function(e) local _binding = e ... end` — a direct
binding-pattern catch parameter routes through transformBindingName the same way.

### 6.3 More try boundaries + finally-return + methods + enums + namespaces

```ts
export const obj = {
	async work(n: number) { return (await Promise.resolve(n)) + 1; },
	*counter() { yield 1; },
};
enum Weird { ["a b"] = 1, ["ok"] = 2 }
namespace NoExports { const x = 1; print(x); }
export function sw(v: number) {
	switch (v) {
		case 1:
			try { break; } catch {}
		default:
			print("d");
	}
}
export function contOnly() {
	for (let i = 0; i < 3; i++) { try { continue; } catch {} }
}
export function finReturn(): number {
	try { print("t"); } finally { return 9; }
}
```

```luau
local obj = {
	work = TS.async(function(self, n)
		return (TS.await(TS.Promise.resolve(n))) + 1
	end),
	counter = function(self)
		return TS.generator(function()
			coroutine.yield(1)
		end)
	end,
}
local Weird
do
	local _inverse = {}
	Weird = setmetatable({}, {
		__index = _inverse,
	})
	Weird["a b"] = 1
	_inverse[1] = "a b"
	Weird.ok = 2
	_inverse[2] = "ok"
end
local NoExports = {}
do
	local x = 1
	print(x)
end
local function sw(v)
	repeat
		local _fallthrough = false
		if v == 1 then
			local _exitType, _returns = TS.try(function()
				return TS.TRY_BREAK
			end, function() end)
			if _exitType then
				break
			end
		end
		print("d")
	until true
end
local function contOnly()
	for i = 0, 2 do
		local _exitType, _returns = TS.try(function()
			return TS.TRY_CONTINUE
		end, function() end)
		if _exitType then
			continue
		end
	end
end
local function finReturn()
	local _exitType, _returns = TS.try(function()
		print("t")
	end, nil, function()
		return TS.TRY_RETURN, { 9 }
	end)
	if _exitType then
		return unpack(_returns)
	end
end
```

Enum bodies (numeric/string/heterogeneous/computed/const) — exact bytes:

```ts
enum Fruit { Apple, Banana, Cherry }
enum Mixed { A = 5, B, C = 10 }
enum Color { Red = "red", Green = "green" }
enum Hetero { Num = 1, Str = "str" }
const base = 10;
enum Computed { X = base * 2, Y = "y".size() }
const enum Direction { Up, Down }
// inside a function: print(Fruit.Apple, Fruit[1], ...); print(Direction.Up, Direction.Down);
```

```luau
local Fruit
do
	local _inverse = {}
	Fruit = setmetatable({}, {
		__index = _inverse,
	})
	Fruit.Apple = 0
	_inverse[0] = "Apple"
	Fruit.Banana = 1
	_inverse[1] = "Banana"
	Fruit.Cherry = 2
	_inverse[2] = "Cherry"
end
local Mixed
do
	local _inverse = {}
	Mixed = setmetatable({}, {
		__index = _inverse,
	})
	Mixed.A = 5
	_inverse[5] = "A"
	Mixed.B = 6
	_inverse[6] = "B"
	Mixed.C = 10
	_inverse[10] = "C"
end
local Color = {
	Red = "red",
	Green = "green",
}
local Hetero
do
	local _inverse = {}
	Hetero = setmetatable({}, {
		__index = _inverse,
	})
	Hetero.Num = 1
	_inverse[1] = "Num"
	Hetero.Str = "str"
end
local base = 10
local Computed
do
	local _inverse = {}
	Computed = setmetatable({}, {
		__index = _inverse,
	})
	Computed.X = 20
	_inverse[20] = "X"
	local _value = #"y"
	Computed.Y = _value
	_inverse[_value] = "Y"
end
-- const enum               <-- the SOURCE COMMENT only; Direction emits nothing
-- (uses) print(Fruit.Apple, Fruit[1], Mixed.B, Color.Red, Hetero.Str, Computed.X)
--        print(0, 1)       <-- Direction.Up / Direction.Down inlined
```

Namespace with const/let/function exports + nested:

```ts
namespace Outer {
	export const value = 1;
	export let mut = 2;
	export function fn() { return value; }
	const hidden = 3;
	export namespace Inner { export const deep = hidden; }
}
export function useNs() {
	print(Outer.value, Outer.fn(), Outer.Inner.deep, Outer.mut);
	Outer.mut = 5;
}
```

```luau
local Outer = {}
do
	local _container = Outer
	local value = 1
	_container.value = value
	_container.mut = 2
	local function fn()
		return value
	end
	_container.fn = fn
	local hidden = 3
	local Inner = {}
	do
		local _container_1 = Inner
		local deep = hidden
		_container_1.deep = deep
	end
	_container.Inner = Inner
end
local function useNs()
	print(Outer.value, Outer.fn(), Outer.Inner.deep, Outer.mut)
	Outer.mut = 5
end
```

### 6.4 Hoisting + dotted namespaces + namespace-enum interplay + resume args

```ts
export function early(): number { return E.A; }
enum E { A }
export function earlyStr(): string { return SE.S; }
enum SE { S = "s" }
export function earlyNs(): number { return NS.v; }
namespace NS { export const v = 7; }
namespace AB.CD { export const v = 1; }
namespace HasEnum {
	export enum Inner { X }
	export const c = Inner.X;
}
export async function chain(a: () => Promise<number>, b: () => Promise<number>) {
	const x = (await a()) + (await b());
	return x;
}
export function resume() {
	const g = (function* (): Generator<number, void, number> {
		const got = yield 1;
		print(got);
	})();
	g.next();
	g.next(5);
}
```

```luau
local E
local function early()
	return E.A
end
do
	local _inverse = {}
	E = setmetatable({}, {
		__index = _inverse,
	})
	E.A = 0
	_inverse[0] = "A"
end
local SE
local function earlyStr()
	return SE.S
end
SE = {
	S = "s",
}
local NS
local function earlyNs()
	return NS.v
end
NS = {}
do
	local _container = NS
	local v = 7
	_container.v = v
end
local AB = {}
do
	local _container = AB
	local CD = {}
	do
		local _container_1 = CD
		local v = 1
		_container_1.v = v
	end
	_container.CD = CD
end
local HasEnum = {}
do
	local _container = HasEnum
	local Inner
	do
		local _inverse = {}
		Inner = setmetatable({}, {
			__index = _inverse,
		})
		Inner.X = 0
		_inverse[0] = "X"
	end
	_container.Inner = Inner
	local c = Inner.X
	_container.c = c
end
local chain = TS.async(function(a, b)
	local x = (TS.await(a())) + (TS.await(b()))
	return x
end)
local function resume()
	local g = (function()
		return TS.generator(function()
			local got = coroutine.yield(1)
			print(got)
		end)
	end)()
	g.next()
	g.next(5)
end
```

Note the enum-inside-namespace: the do-block enum statements run inside the namespace
do-block, and the ExportInfo mapping appends `_container.Inner = Inner` AFTER the enum's
statements (the enum statement is the mapping key).

---

## 7. Diagnostics — byte-exact (Shared/diagnostics.ts)

| id | text | rotor status |
|---|---|---|
| noLabeledStatement (L106) | `labels are not supported!` | NOT ported — loop labels are a rotor extension (docs.md "rotor extensions"); replaced by `noLabeledStatementsWithinTryCatch` ("labels are not supported within try/catch blocks!", byte-exact with roblox-ts PR #2928) plus the rotor-only `rotorUnknownLabel` / `rotorLabeledStatementFlowControl` / `rotorLabeledContinueInDoWhile` |
| noDebuggerStatement (L107) | `` `debugger` is not supported! `` | ported (:89) |
| noEnumMerging (L130) | `Enum merging is not supported!` | ported (:148), UNWIRED |
| noNamespaceMerging (L131) | `Namespace merging is not supported!` | ported (:152), UNWIRED |
| noFunctionExpressionName (L133) | `Function expression names are not supported!` | ported, wired |
| noAwaitForOf (L151) | `` `await` is not supported in for-of loops! `` | ported, wired (forof.go:498) |
| noAsyncGeneratorFunctions (L152) | `Async generator functions are not supported!` | ported (diagnostics.go:222), wired at all 3 sites |

Oracle-verified CLI rendering (merging diags report ONCE per symbol, at the FIRST
declaration, via `addDiagnosticWithCache(symbol, ..., isReportedByMultipleDefinitionsCache)` —
rotor's `AddDiagnosticWithCache` + `Multi.IsReportedByMultipleDefinitionsCache` are ready):

```
src/_scratch3c.ts:1:1 - error TS roblox-ts: Enum merging is not supported!
src/_scratch3c.ts:8:1 - error TS roblox-ts: Namespace merging is not supported!
src/_scratch3c.ts:15:1 - error TS roblox-ts: Async generator functions are not supported!
```

No other diagnostics exist in these subject areas (full diagnostics.ts L95-200 re-audited).
There is no "no with" (TS has no `with` in modules) and labels/debugger are already wired in
dispatch.go:120-125.

---

## 8. tsgo mapping + rotor implementation sketch

### 8.1 Node kinds / AST shapes (tsgo/ast/ast_generated.go)

| upstream | tsgo accessor | fields |
|---|---|---|
| ts.AwaitExpression | `node.AsAwaitExpression()` (L5189) | `Expression` |
| ts.YieldExpression | `node.AsYieldExpression()` (L4022) | `AsteriskToken` (opt), `Expression` (opt) |
| ts.TryStatement | `node.AsTryStatement()` (L1577) | `TryBlock`, `CatchClause` (opt), `FinallyBlock` (opt) |
| ts.CatchClause | `node.AsCatchClause()` (L1626) | `VariableDeclaration` (opt), `Block` |
| ts.EnumDeclaration | `node.AsEnumDeclaration()` (L2542) | `Name()`, `Members *EnumMemberList` |
| ts.EnumMember | `member.AsEnumMember()` (L2497) | `Name()`, `Initializer` (opt) |
| ts.ModuleDeclaration | `node.AsModuleDeclaration()` (L8210) | `Keyword Kind`, `Name()`, `Body` (via BodyBase: `node.Body()`) |
| ts.ModuleBlock | `body.AsModuleBlock()` (L2591) | `Statements *StatementList` |

Dotted `namespace A.B {}`: nested ModuleDeclarations exactly like TS5
(tsgo/parser/parser.go:2210-2233); the nested declaration carries an implicit `export`
modifier flagged `NodeFlagsReparsed`. Distinguish branches via `ast.IsModuleBlock(body)`.
`declare module "X"` has a StringLiteral name + Declare modifier (dispatch declare-skip
catches it first).

### 8.2 Checker APIs

- CHECKER `s.Checker.GetConstantValue(node)` (tsgo/checker/services.go:821-848) → `any`:
  `string` | `jsnum.Number` | nil. EnumMember nodes always resolve; property/element
  accesses only for const enums (byte-identical to TS5). Reuse the access.go:172-179 type
  switch (`string` / `jsnum.Number` / `float64`) for enum member values. upstream
  `typeof v !== "string"` (needsInverseEntry) → `_, isStr := v.(string); !isStr`.
- `ast.IsInstantiatedModule(node, false)` (tsgo/ast/utilities.go:2409).
- `ast.HasSyntacticModifier(node, ast.ModifierFlagsConst)` — ModifierFlagsConst = 1<<12
  ("Const enum", tsgo/ast/modifierflags.go:21).
- preserveConstEnums: `s.Program.Options().PreserveConstEnums.IsTrue()` (upstream
  `!== true` → emit unless IsTrue; nil Program in mechanics tests → false, matches).
- `checker.SkipAlias`, `s.GetModuleExports`, `IsSymbolMutable`, `isSymbolOfValue`,
  `s.Checker.GetSymbolAtLocation` — all already consumed elsewhere in rotor.
- `ast.FindAncestor`, `ast.IsTryStatement`, `ast.IsFunctionLikeDeclaration`,
  `ast.IsIterationStatement`, `ast.IsSwitchStatement` for §3.7. Verify the tsgo
  IsIterationStatement signature (TS5 takes lookInLabeledStatements; labels are banned so
  any variant is equivalent in valid input).

### 8.3 File / wiring plan

1. **state.go**: add `tryUsesStack []*TryUses` + `PushTryUsesStack/MarkTryUses/PopTryUsesStack`
   (§3.2). Replace the Phase-2 promise comment at state.go:100.
2. **statements.go**: extend `transformBreakStatement` / `transformContinueStatement` /
   `transformReturnStatement(+Inner)` per §3.8; delete the NOTE at L364-370. New helpers
   `isReturnBlockedByTryStatement` / `isBreakBlockedByTryStatement` (10 lines).
3. **trystatement.go** (new): §3.3-3.6 (~140 lines). Temps `_exitType`/`_returns` created
   before body transform (byte parity — §6.2 f8).
4. **functions.go**: replace the three NYS stubs with §1.3(a-c) + §2.2's
   `wrapStatementsAsGenerator`. The method-shape gate gains `!isAsync` only (generator
   methods keep the shape).
5. **dispatch.go**: expressions — `KindAwaitExpression → transformAwaitExpression`,
   `KindYieldExpression → transformYieldExpression`; statements —
   `KindTryStatement → transformTryStatement`, `KindEnumDeclaration → transformEnumDeclaration`,
   `KindModuleDeclaration → transformModuleDeclaration`.
6. **enums.go** (new): §4 (~90 lines) + `hasMultipleDefinitions` (shared util — maybe
   exports.go or a small util file).
7. **namespaces.go** (new): §5 (~110 lines). ExportInfo already exists; pass
   `&ExportInfo{ID: containerId, Mapping: exportsMap}`.

### 8.4 Risks / interactions

- **Rerouting × existing loop transforms: NONE.** Loops/switch never consult the flags; the
  post-try `if` chain is ordinary appended statements. Verified by oracle for numeric-for,
  while, and switch (repeat-until) bodies (§6.2-6.3).
- **Temp-id numbering**: upstream luau-ast numbers temp collisions by CREATION order, not
  render order (f8: outer `_exitType` created first, renders after inner `_exitType_1`).
  rotor's render/solvetempids.go already implements this; just match creation order
  (exitType, returns, then body; enum: inverse before per-member temps; namespace:
  container at function top; yield*: result before returnValue).
- **markTryUses after pop** — easy to get wrong; the f8 oracle bytes are the regression
  test (outer try MUST get the declaration form + `if _exitType then` tail).
- **Dead-code elision**: rerouted return/break/continue are ReturnStatements →
  IsFinalStatement still truncates the rest of the block, same as upstream
  (transformStatementList stops after final statements). No change.
- **Async + hoisting**: async function declarations switch to VariableDeclaration/Assignment;
  the existing hoist machinery (identifier.go:209-220 refuses self-reference exemption for
  ASYNC function declarations — already ported) produces the `local f` + `f = TS.async(...)`
  pair. Do not "optimize" a non-hoisted async into a function statement.
- **Statement-position `TS.await(...)`**: flows through wrapExpressionStatement as a call —
  already correct.
- **GetConstantValue caching**: tsgo's checkExpressionCached inside GetConstantValue mutates
  checker state — fine single-threaded (rotor transforms are).
- **getModuleExports for namespace symbols**: rotor's GetModuleExports
  (exports.go:269-298) was built for file modules; it takes the namespace symbol directly
  upstream — confirm it uses `typeChecker.getExportsOfModule(symbol)` equivalent for
  namespace symbols too (upstream shares one code path; the cache is per-symbol).
- **Diff-fleet coverage**: add fixtures mirroring §6 (the byte outputs above are the
  expected files).
- **Acceptance impact: ZERO for randomness** — census 2026-06-07: `randomness/src` contains
  no async/await/generator/try/enum/namespace SYNTAX at all (one "async" in a comment;
  `namespace(...)` is an imported @rbxts/remo function, not the keyword). Phase 3c is for
  compiler completeness, not the randomness unlock.

---

## 9. Quirks — verbatim upstream comments & gotchas

- transformEnumDeclaration.ts L79-81 (the index spill):
  `// note: we don't use pushToVarIfComplex here / because identifier also needs to be pushed / since the value calculation might reassign the variable`
- transformEnumDeclaration.ts L92:
  `// constantValue is always number without initializer, so assert is safe`
- transformModuleDeclaration.ts L141:
  `// ts.StringLiteral is only in the case of \`declare module "X" {}\`? Should be filtered out above`
- transformModuleDeclaration.ts L144:
  `// unsure how to filter out ts.JSDocNamespaceBody` (the `as ts.NamespaceBody` cast — in
  tsgo just pass the Body node).
- RuntimeLib TS.try L224: `-- if exit type is a control flow, do not rethrow errors` — a
  rerouted return/break/continue from CATCH suppresses rethrow of a catch error; from the
  digest's perspective: emitted shapes never need to care.
- TS.await passes non-Promises through unchanged → `TS.await(5)` is legal emitted code
  (§6.3 awaitPlain); do NOT elide the wrapper for non-promise operands.
- Bare `yield` emits `coroutine.yield()` with zero args (NOT nil).
- collapseFlowControlCases REPLACES the last case's condition with the bare `exitTypeId`
  truthiness test — never emit `elseif _exitType == TS.TRY_CONTINUE then` as the final
  branch.
- transformIntoTryCall: `assert(node.finallyBlock)` when no catch — a TryStatement with
  neither is a parse error; the `luau.nil()` catch placeholder appears ONLY when finally
  exists.
- The try's blocked checks start at `node.parent` (NOT node) — starting at the try itself
  would always find the try and infinitely tunnel.
- breakBlocked ⟹ returnBlocked (a try nearer than any loop/switch is nearer than any
  function): the `returnBlocked && breakBlocked` early-exit in transformFlowControl is the
  only multi-flag tunnel shape; `!returnBlocked && breakBlocked` is unreachable.
- Empty catch clause renders `function() end` — luau renderer handles the empty function
  body inline; nothing special to emit.
- Generator methods MAY use the `function Class:m()` / map-field shape; async methods may
  NOT (the `!isAsync` in transformMethodDeclaration.ts L61). Generator wrap composes with
  the method shape (body swapped before the shape decision).
- tsgo nested (dotted) namespace declarations carry a synthesized `export` modifier with
  `NodeFlagsReparsed` (parser.go:2220-2223) — harmless for the transform but visible to
  HasSyntacticModifier(Export).
- `Fruit[1]` reverse lookup works at RUNTIME via the `__index = _inverse` metatable — the
  compiler emits a plain element access; no transform involvement.
- String-only enums get NO inverse table and NO do-block — a plain map literal (and plain
  `E = {...}` assignment when hoisted).
- Heterogeneous enum string members write the forward entry inside the general path but
  SKIP the inverse entry (needsInverseEntry per member, not per enum).
