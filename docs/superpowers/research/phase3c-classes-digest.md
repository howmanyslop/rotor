# Phase 3c Digest — Classes, object spread, logical assignment operators

Source of truth for porting the Phase 3c unlock set without re-reading the TS. All upstream
paths relative to `reference/roblox-ts/src/TSTransformer/` unless prefixed; tsgo paths relative
to repo root. Checker/type-API usage flagged `CHECKER:`. Builds on `phase2-transforms-digest.md`
(P2), `phase2b-transforms-digest.md` (P2b), `phase2-transformstate-digest.md` (TS-state),
`phase3b-digest.md` (P3b). SCOPE: (1) acceptance-target census, (2) class-like declarations
COMPLETE (boilerplate, constructors, methods, statics, this/super, decorators, hoisting),
(3) object spread in literals + the destructuring-rest ban, (4) `??=`/`&&=`/`||=`,
(5) oracle-verified worked examples, (6) diagnostics, (7) tsgo mapping + rotor sketch,
(8) quirks. Every worked example in §5 was produced 2026-06-07 by rbxtsc 3.0.0
(`testdata/diff/project`, `--type model`); scratch artifacts deleted.

Rotor hook points already in place (do not re-port): `transformMethodDeclaration`
(`internal/transformer/functions.go:139-199`, minus the decorator key-pinning block, §2.5),
`transformParameters` (functions.go), `isMethod`/`isMethodFromType` (types.go),
MapPointer machinery (pointer.go), `transformWritableExpression`/`transformWritableAssignment`
(assignment.go:98,147), `CreateTruthinessChecks` (truthiness; rotor signature takes the type
explicitly), `checkIdentifierHoist` incl. both class branches (identifier.go:178-216),
`createHoistDeclaration` + `HoistsByStatement`/`IsHoisted` (statementlist.go:138, state.go:158-160),
`validateMethodAssignment` minus the ClassElement arm (methodassignment.go:75-84), ALL class
diagnostics (diagnostics.go:71,75,99,128,132,156,203,207,280), `luau.IsMetamethod`/
`luau.IsReservedClassField` (internal/luau/validate.go:40,47), `GetFirstDefinedSymbol`,
`MacroManager.IsMacroOnlyClass` (macromanager.go:441).

---

## 1. Acceptance-target census (randomness)

Smoke blockers and the exact constructs behind them, from `C:\Users\user\Source\Roblox\randomness\src`:

### 1.1 ClassDeclaration — 1 file

`src/client/ui/error-boundary.tsx` (the ONLY `class` in the project):

```ts
@ReactComponent
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
	public state: ErrorBoundaryState = { hasError: false };
	public componentDidCatch(message: unknown, info: ErrorInfo) { ... this.setState({...}); }
	public render() { if (this.state.hasError) { ... } else { return this.props.children; } }
}
```

Needs: class declaration + `export`; `extends` an IMPORTED identifier with type args
(`Component<P,S>` — heritage `ExpressionWithTypeArguments`, transform `.expression` only);
NO explicit constructor → implicit constructor with extends (`super.constructor(self, ...)`
+ vararg passthrough); ONE property initializer (`state = {...}`, object literal); two
plain instance methods (`function ErrorBoundary:componentDidCatch(...)`); `this.X`
property/method access (`self.X`); a LEGACY class decorator `@ReactComponent`
(randomness tsconfig has `"experimentalDecorators": true`) — emits
`ErrorBoundary = ReactComponent(ErrorBoundary) or ErrorBoundary`. The file is `.tsx` but
contains NO JSX syntax — classes + decorators suffice for it (JSX is a separate workstream).
No statics, no getters, no parameter properties, no toString.

### 1.2 Object spread assignments — 6 files (all OBJECT-LITERAL spread; zero destructuring rest)

| file | constructs |
|---|---|
| `server/combat/fighter-registry.ts:70-78` | `{ ...f, damagePercent: 0, ..., invulnUntil }` — spread first (identifier, definitely-object) + 6 fields incl. shorthand + call-initializer field |
| `client/controller/keybinds.ts:37,42,46` | `{ ...DEFAULTS }`, `{ ...prev, [action]: key }` (COMPUTED key after spread), `{ ...DEFAULTS }` |
| `client/killfeed/killfeed-state.ts:28,43` | `{ ...e, leaving: true }`; line 43 is `[...prev, { id, ...entry }]` — ALSO needs ARRAY spread (separate blocker; the inner `{ id, ...entry }` is object spread NOT first ⇒ loop form) |
| `client/ui/button.tsx:17-24` | `{ Activated: ..., ..., ...props.event }` — spread LAST, `props.event` OPTIONAL type ⇒ if-wrapped loop + `_spread` temp |
| `client/ui/reactive-button/use-button-state.ts:32-34` | `{ ...state, press: ... }` ×3 — spread first |
| `client/ui/reactive-button/use-button-animation.ts:55,58` | `{ ...springs.bubbly, impulse: -100 }` — spread first via property access, definitely-object ⇒ `table.clone(springs.bubbly)` |

### 1.3 `??=` — 1 file

`src/client/ui/outline.tsx:36-38`: three statement-position `??=` on plain identifier
targets (destructured parameter locals, optional object types):
`innerThickness ??= rem(3); outerThickness ??= rem(1.5); cornerRadius ??= new UDim(0, rem(8));`
⇒ pure `if x == nil then x = <rhs> end` — no temps. No `&&=`/`||=` anywhere in randomness.

---

## 2. Classes — upstream walkthrough

### 2.1 Entry points & dispatch

- Statement: `transformStatement.ts` routes `ts.SyntaxKind.ClassDeclaration` →
  `statements/transformClassDeclaration.ts` (7 lines):
  `return transformClassLikeDeclaration(state, node).statements;`
- Expression: `transformExpression.ts:88` routes `ClassExpression` →
  `expressions/transformClassExpression.ts` (9 lines):
  `const { statements, name } = transformClassLikeDeclaration(state, node); state.prereqList(statements); return name;`
- Also new in expression dispatch for classes: `ThisKeyword` →
  `transformThisExpression` (L116), `SuperKeyword` → `transformSuperKeyword` (L113).
  rotor's dispatch.go currently NYS-faults all four kinds.

### 2.2 transformClassLikeDeclaration (nodes/class/transformClassLikeDeclaration.ts:199-384)

Output statement shape (statement position):

```lua
local ClassName        -- omitted when hoisted (§2.10)
do
	<boilerplate §2.3>
	<constructor §2.4>
	<methods §2.5>
	<__tostring magic §2.6>
	<static props + static blocks §2.6>
	<internal-name assignment (named class expr only)>
	<decorators §2.9>
end
```

Pseudocode (verbatim semantics):

```
transformClassLikeDeclaration(state, node):                      # L199
  isClassExpression = ts.isClassExpression(node)                 # L200
  statements = []
  isExportDefault = hasSyntacticModifier(node, ModifierFlags.ExportDefault)  # L203
  if node.name: validateIdentifier(state, node.name)             # L205-207

  shouldUseInternalName = isClassExpression && node.name != undefined  # L217
  returnVar =                                                    # L219-228
    shouldUseInternalName -> tempId("class")                     # `_class`
    node.name             -> transformIdentifierDefined(node.name)
    isExportDefault       -> luau.id("default")                  # literally `default`
    else                  -> tempId("class")
  internalName = shouldUseInternalName ? transformIdentifierDefined(node.name)
                                       : returnVar               # L230-235
  state.classIdentifierMap.set(node, internalName)               # L236 (used by `this` in statics, §2.7)
  if !isClassHoisted(state, node):                               # L237-245
    statements.push(local returnVar)        # VariableDeclaration, no right
  # isClassHoisted (L190-197): node.name -> CHECKER: getSymbolAtLocation(node.name),
  # assert(symbol); return state.isHoisted.get(symbol) === true. Unnamed -> false.

  inner = createBoilerplate(state, node, internalName, isClassExpression)  # L248-249, §2.3

  ctor = findConstructor(node)   # util/findConstructor.ts: first ConstructorDeclaration
                                 # WITH body (overload signatures have none)
  inner += ctor ? transformClassConstructor(state, ctor, internalName)        # L251-256
                : transformImplicitClassConstructor(state, node, internalName)

  # validation pass 1 (L258-271):
  for member in node.members:
    if (isPropertyDeclaration(m) || isMethodDeclaration(m))
       && (isIdentifier(m.name) || isStringLiteral(m.name))
       && luau.isReservedClassField(m.name.text):     # {"__index","new"}
      addDiagnostic(noReservedClassFields(m.name))
    if ts.isAutoAccessorPropertyDeclaration(m):       # `accessor x = ...`
      keyword = getModifiers(m).find(kind == AccessorKeyword)
      addDiagnostic(noAutoAccessorModifiers(keyword))

  # member triage (L273-299):
  methods = []; staticDeclarations = []
  for member in node.members:
    validateMethodAssignment(state, member)           # heritage-clause method-ness, §2.8
    Constructor|IndexSignature|SemicolonClassElement -> skip
    MethodDeclaration            -> methods.push
    PropertyDeclaration          -> static ? staticDeclarations.push : skip (ctor handles)
    isAccessor (get/set)         -> addDiagnostic(noGetterSetter(member))
    ClassStaticBlockDeclaration  -> staticDeclarations.push
    else assert(false)

  # CHECKER (L301-302):
  classType    = typeChecker.getTypeOfSymbolAtLocation(node.symbol, node)
  instanceType = typeChecker.getDeclaredTypeOfSymbol(node.symbol)

  for method in methods:                              # L304-326
    if method.name is Identifier|StringLiteral:
      if luau.isMetamethod(name.text): addDiagnostic(noClassMetamethods(method.name))
      if hasStaticModifier(method):
        if instanceType.getProperty(text) != undefined: noInstanceMethodCollisions(method)
      else:
        if classType.getProperty(text)   != undefined: noStaticMethodCollisions(method)
    [stmts, prereqs] = capture(transformMethodDeclaration(state, method,
                               { name: "name", value: internalName }))   # ptr is identifier
    inner += prereqs; inner += stmts

  # __tostring magic (L328-348): MAGIC_TO_STRING_METHOD = "toString" (L23)
  toStringProperty = instanceType.getProperty("toString")   # INHERITED properties count!
  if toStringProperty && (toStringProperty.flags & SymbolFlags.Method):
    inner += `function internalName:__tostring() return self:toString() end`

  for declaration in staticDeclarations:               # L350-360 (source order)
    ClassStaticBlockDeclaration -> inner += transformBlock(state, declaration.body)  # `do ... end`
    else: [stmts, prereqs] = capture(transformPropertyDeclaration(state, decl, internalName))
          inner += prereqs; inner += stmts

  if shouldUseInternalName:                            # L362-372
    inner += `returnVar = internalName`                # `_class = Inner`

  inner += transformDecorators(state, node, returnVar) # L374, §2.9

  statements.push(DoStatement(inner))                  # L376-381
  return { statements, name: returnVar }
```

### 2.3 createBoilerplate (transformClassLikeDeclaration.ts:37-188)

```
createBoilerplate(state, node, className, isClassExpression):
  isAbstract = ts.hasAbstractModifier(node)            # L43
  extendsNode = getExtendsNode(node)
  # util/getExtendsNode.ts: first heritageClause with token == ExtendsKeyword -> clause.types[0]
  # (an ExpressionWithTypeArguments; only `.expression` is ever transformed)

  if isAbstract && !extendsNode:                       # L68-76
    statements.push(`className = {}`)                  # Assignment, right = luau.map()
    # NOTE: no metatable, no __tostring, NO `className.__index = className`, no `.new`
  else:
    metatableFields = [ __tostring = function() return "<NAME>" end ]   # L78-85
      # <NAME> = "Anonymous" when className is a TemporaryIdentifier, else className.name (L83)
    if extendsNode:                                    # L87-107
      [extendsExp, prereqs] = capture(transformExpression(state, extendsNode.expression))
      statements += prereqs
      statements.push(`local super = extendsExp`)      # plain id "super" (luau.id), L91-99
      metatableFields.push(__index = super)            # L100-106
    metatable = setmetatable({}, { metatableFields })  # L109-112
    if isClassExpression && node.name:                 # L114-121
      statements.push(`local <node.name> = metatable`) # VariableDeclaration (the ONLY local-decl form)
    else:
      statements.push(`className = metatable`)         # Assignment (className was pre-declared)
    statements.push(`className.__index = className`)   # L134-141

  if !isAbstract:                                      # L145-185
    statements.push(
      function className.new(...)                      # FunctionDeclaration, hasDotDotDot=true, localize=false
        local self = setmetatable({}, className)
        return self:constructor(...) or self           # MethodCallExpression + binary "or"
      end)
  return statements
```

Byte shapes (oracle §5.1/§5.2): plain class, derived class (with `local super = Base` and
`__index = super` as the SECOND map field after `__tostring`), abstract-no-extends
(`Abs = {}` only), abstract-with-extends (full metatable boilerplate, NO `.new`, constructor
still emitted).

### 2.4 Constructors (nodes/class/transformClassConstructor.ts)

`transformPropertyInitializers` (L16-50) — shared by both paths; per NON-static
PropertyDeclaration in `node.members`, in member order:

- `ts.isPrivateIdentifier(member.name)` → `addDiagnostic(noPrivateIdentifier(node))` —
  **node is the CLASS**, so the diagnostic spans the whole class declaration (oracle-confirmed,
  §6) — port verbatim; `continue`.
- no initializer → skip (declare-only properties emit nothing).
- `[index, indexPrereqs] = capture(transformPropertyName(member.name))`; push prereqs;
  `[right, rightPrereqs] = capture(transformExpression(initializer))`; push prereqs;
  push `self[index] = right` (ComputedIndexExpression on `luau.globals.self`; renderer
  emits `self.legs = 4` for valid-identifier string indices).

`transformImplicitClassConstructor` (L52-88) — no constructor found:

```lua
function className:constructor(<nothing>)   -- hasDotDotDot = extends? true : false
	super.constructor(self, ...)             -- ONLY if getExtendsNode(node); CallStatement,
	                                         -- luau.property(luau.globals.super, "constructor")
	<property initializers>
end
```

`transformClassConstructor` (L90-130) — explicit constructor (body guaranteed):

```
{statements, parameters, hasDotDotDot} = transformParameters(state, node)
  # NB: isMethod(state, ConstructorDeclaration) is FALSE (constructor type has construct
  # signatures, no call signatures) so `self` is NOT prepended; the `:` sugar supplies it.
  # statements carries default-value `if p == nil then p = d end` + binding-pattern prereqs.
bodyStatements = getStatements(node.body)
superIndex = bodyStatements.findIndex(isExpressionStatement && isSuperCall(stmt.expression))
  # only TOP-LEVEL statements of the body; -1 when absent
statements += transformStatementList(state, node.body, bodyStatements.slice(0, superIndex+1))
  # statements before AND INCLUDING the first super() call (slice(0,0)=[] when none)
for parameter in node.parameters:                      # L103-115 parameter properties
  if ts.isParameterPropertyDeclaration(parameter, parameter.parent):
    paramId = transformIdentifierDefined(parameter.name)
    statements.push(`self.<paramId.name> = paramId`)   # luau.property — NAME access, not computed
statements += transformPropertyInitializers(state, node.parent)
statements += transformStatementList(state, node.body, bodyStatements.slice(superIndex+1))
return [ MethodDeclaration(expression=name, name="constructor", statements, parameters, hasDotDotDot) ]
```

Resulting order inside `function X:constructor(...)`: param defaults/destructure → body up
to+incl `super(...)` → parameter-property assignments → property initializers → rest of body.
(Oracle §5.2: `Base:constructor(x, y)` emits `if y == nil then y = 2 end; self.x = x;
self.y = y; print(...)`.)

Super CALL emit lives in transformCallExpression.ts:128-133 (`ts.isSuperCall(node)`):
`luau.call(luau.property(convertToIndexableExpression(<transformed super>), "constructor"),
[luau.globals.self, ...ensureTransformOrder(node.arguments)])` → `super.constructor(self, args)`.

### 2.5 Methods (nodes/transformMethodDeclaration.ts:14-106)

Already ported as `transformMethodDeclaration` (functions.go:139-199) for object literals;
classes pass `ptr = { name: "name", value: internalName }` (identifier pointer, never a Map),
so the fast path applies. MISSING piece in rotor — the decorator key-pinning block (L36-49),
required before §2.9:

```
name = transformPropertyName(node.name)
if ts.hasDecorators(node) || node.parameters.some(p => ts.hasDecorators(p)):
  if !luau.isSimplePrimitive(name):        # computed non-literal key
    tempId = tempId("key"); result.push(`local tempId = name`); name = tempId
  state.setClassElementObjectKey(node, name)   # TransformState map, §7.2
```

Recap of the emit selection (upstream L61-105; rotor functions.go:179-198 matches):

- `!isAsync && name is StringLiteral && ptr.value is not Map && isValidIdentifier(name)`:
  - `isMethod(state, node)` (i.e. real `this`) → `function Class:name(params)` with
    `parameters.shift()` removing the `self` transformParameters injected;
  - else (e.g. `this: void`) → `function Class.name(params)` (FunctionDeclaration, localize=false).
- fallback (computed name, async, or inline-map ptr): `expression = FunctionExpression`;
  if async → `expression = TS.async(expression)` (CHECKER-free; `state.TS(node, "async")` =
  rotor `s.RuntimeLib(node, "async")`); then `assignToMapPointer(state, ptr, name, expression)`
  → `Class[KEY] = function(self) ... end` / `Class.go = TS.async(function(self) ... end)`
  (oracle §5.3 — note `self` stays an EXPLICIT first parameter here).
- STATIC methods: upstream runs the same code — `isMethod` is TRUE for statics (their
  `this` is the class type), so statics emit `function Class:create()` and call sites emit
  `Animal:create()` (oracle §5.1). Do NOT special-case statics.
- generator methods: `node.asteriskToken` → wrapStatementsAsGenerator (+
  noAsyncGeneratorFunctions if also async) — rotor currently NYS for generators; keep that
  NYS (randomness needs none).
- `node.body` undefined (overload signature) → emit nothing. PrivateIdentifier name →
  noPrivateIdentifier(node.name), emit nothing.

### 2.6 Statics

Static PROPERTY (nodes/class/transformPropertyDeclaration.ts:9-37) — called per static
PropertyDeclaration inside `state.capture`:

- non-static → empty (defensive); PrivateIdentifier name → noPrivateIdentifier(node) →
  empty; no initializer → empty.
- else ONE statement: `name[transformPropertyName(node.name)] = transformExpression(node.initializer)`
  — both transforms run INLINE (their prereqs flow to the caller's capture and are emitted
  BEFORE the assignment; index prereqs before value prereqs).

Static BLOCK (`static { ... }`): `transformBlock(state, declaration.body)` → a nested
`do ... end` inside the class do-block (oracle §5.3). `this` inside it resolves via
classIdentifierMap (§2.7).

`__tostring` magic method (transformClassLikeDeclaration.ts:328-348): when CHECKER
`instanceType.getProperty("toString")` exists AND `symbol.flags & ts.SymbolFlags.Method`:

```lua
function internalName:__tostring()
	return self:toString()
end
```

Emitted AFTER all methods, BEFORE static declarations. Because `getProperty` sees inherited
members, EVERY subclass of a toString-defining class re-emits this wrapper (oracle §5.2:
Derived, NoCtor, AbsExtends all carry it; FromAbs doesn't).

### 2.7 this (expressions/transformThisExpression.ts:7-29)

```
symbol = CHECKER getSymbolAtLocation(node)
if symbol === macroManager.getSymbolOrThrow("globalThis"): addDiagnostic(noGlobalThis(node))
if symbol:
  container = ts.getThisContainer(node, /*includeArrowFunctions*/ false, /*includeClassComputedPropertyName*/ false)
  isStatic = ts.hasStaticModifier(container) || ts.isClassStaticBlockDeclaration(container)
  if isStatic && !ts.isMethodDeclaration(container) && ts.isClassLike(container.parent):
    id = state.classIdentifierMap.get(container.parent)
    if id: return id          # static block / static property initializer: `this` -> class id
return luau.globals.self      # everything else: literal `self`
```

(Static METHODS still return `self` — the `:` declaration form binds it; only static blocks
and static property initializers need the class identifier. Oracle §5.3: `print(this.counter)`
in a static block → `print(Statics.counter)`.)

### 2.8 super reads & calls

- `transformSuperKeyword()` (expressions/transformSuperKeyword.ts) → `luau.globals.super`
  unconditionally (the boilerplate's `local super` is in scope inside the do-block).
- super METHOD call: transformCallExpression.ts:170-175 (`ts.isSuperProperty(expression)` in
  transformPropertyCallExpressionInner): `super.name(self, args...)` — a PLAIN call on
  `luau.property(baseExpression, name)` with `luau.globals.self` prepended (NOT `:` form).
  Element form L232-240: `super[index](self, args...)`.
- super CALL (`super(...)`): §2.4. rotor: replace the three `DiagRotorNotYetSupported(node,
  "`super` calls")` arms at call.go:305, 348, 414.
- bare property read `super.x` falls through normal PropertyAccess transform over the
  `super` identifier.
- validateMethodAssignment ClassElement arm (util/validateMethodAssignment.ts:63-67): for a
  class member with a name, for each `typeNode` in `ts.getAllSuperTypeNodes(node.parent)` run
  validateHeritageClause (L37-46): `name = ts.getPropertyNameForPropertyNameNode(node.name)`;
  CHECKER `propertyType = getTypeOfPropertyOfType(getType(typeNode), name)`; if found,
  `validateTypes(state, node, getType(node), propertyType)` → expectedMethodGotFunction /
  expectedFunctionGotMethod. rotor: fill the stub at methodassignment.go:81-84.
  tsgo has the helper UNEXPORTED at `tsgo/ls/utilities.go:915` — reimplement with
  `ast.GetHeritageElements(node, KindExtendsKeyword)` (interfaces) /
  `ast.GetClassExtendsHeritageElement(node)` + `ast.GetImplementsTypeNodes(node)` (classes).

### 2.9 Decorators (nodes/class/transformDecorators.ts) — LEGACY experimental only

Top-level `transformDecorators(state, node, classId /*= returnVar*/)` (L242-279), called as
the LAST thing inside the do-block. Order:

1. INSTANCE members (declaration order): MethodDeclaration with body → method decorators;
   PropertyDeclaration → property decorators.
2. STATIC members (declaration order): same two forms.
3. CLASS decorators (incl. constructor-parameter decorators).

`transformMemberDecorators` (L58-92) — shared driver; per decorator i (SOURCE order):
`[expression, prereqs] = capture(transformExpression(decorator.expression))`; push prereqs to
`initializers`; if `!shouldInline(...)` → `local _decorator = expression` pushed to
initializers, expression = temp; `finalizers.UNSHIFT(callback(convertToIndexableExpression(expression)))`
— so initialization runs first-to-last, application runs LAST-TO-FIRST (TC39 order).
Returns `[initializers, finalizers]`.

`shouldInline` (L17-56): inline iff `!expressionMightMutate(state, expression, decorator.expression)`
(util/expressionMightMutate.ts — tempIds/primitives/functions never mutate; identifiers
consult CHECKER `isSymbolMutable`); else only if it is the LAST decorator AND no parameter
decorators must run in between (method with decorated params; class with decorated ctor
params; parameter with decorated later siblings).

Per-kind callbacks:

- METHOD (L94-157): needs `key = state.getClassElementObjectKey(member)` (assert non-nil —
  set in §2.5). Emit:
  ```lua
  local _descriptor = decorator(Class, KEY, { value = Class[KEY] })
  if _descriptor then
  	Class[KEY] = _descriptor.value
  end
  ```
  (tempId hint "descriptor"; map literal key is the string `"value"`.) Sandwich:
  initializers, then transformParameterDecorators(member), then finalizers.
- PROPERTY (L159-180): `key = state.noPrereqs(() => transformPropertyName(member.name))`
  (property keys are statically enforced) → `decorator(Class, KEY)` CallStatement.
- PARAMETER (L182-212): per parameter i:
  `decorator(Class, KEY_or_nil, i)` — `member.name ? getClassElementObjectKey(member) : luau.nil()`
  (constructor params pass nil); i is the 0-BASED parameter index (`luau.number(i)`).
  Param finalizers also unshift (last param applied first).
- CLASS (L214-240): callback emits `Class = decorator(Class) or Class`; between initializers
  and finalizers, constructor parameter decorators run (findConstructor → transformParameterDecorators).

Oracle §5.4 confirms the full interleave. NOTE rotor: decorators only parse as decorators
under `experimentalDecorators: true`; randomness sets it. tsgo: decorators live in the
Modifiers list — `ast.HasDecorators(node)` (tsgo/ast/utilities.go:3734); collect actual
nodes by filtering `node.Modifiers().Nodes` for `KindDecorator`.

### 2.10 Hoisting interplay

- `checkIdentifierHoist` (expressions/transformIdentifier.ts:47-109; PORTED identifier.go):
  use-before-declare of a class name registers the identifier in
  `hoistsByStatement[sibling]` and sets `isHoisted[symbol] = true`; TWO class carve-outs
  already ported: class EXPRESSIONS self-referencing their internal name (L60-62) and
  same-statement self-reference inside a ClassDeclaration (L91-103).
- `TransformStatementList` emits `local A, B` via createHoistDeclaration BEFORE the
  triggering statement (already ported).
- transformClassLikeDeclaration then SKIPS its own `local className` when
  `isClassHoisted(state, node)` (L237-245) — oracle §5.5.
- Binder/exports: classes are NOT bound in the binder's functions-first pass —
  `exportSortKey` (exports.go:336) already puts them in pass 1 (statement order). Verified
  by oracle run 1: `export class Animal` → trailing `return { Animal = Animal }`. No change
  needed.

---

## 3. Object spread

### 3.1 Object LITERAL spread — SUPPORTED (expressions/transformObjectLiteralExpression.ts:34-90)

`transformSpreadAssignment(state, ptr, property)`:

```
expType = CHECKER getNonOptionalType(getType(property.expression))     # L35
symbol = getFirstDefinedSymbol(state, expType)
if symbol && macroManager.isMacroOnlyClass(symbol):
  addDiagnostic(noMacroObjectSpread(property))                         # macro classes banned

type = getType(property.expression)                                    # L41 (NOT non-optional!)
definitelyObject = isDefinitelyType(type, isObjectType)                # TypeFlags.Object

# FAST PATH (L44-56): spread is FIRST member (ptr still an EMPTY inline Map)
if definitelyObject && ptr.value is Map && ptr.value.fields empty:
  ptr.value = pushToVar(table.clone(transformExpression(property.expression)), ptr.name)
                                                       # ptr.name = "object" -> `_object`
  prereq( setmetatable(ptr.value, nil) )   # CallStatement — strip the metatable because
                                           # class instances can be spread
  return

# GENERAL PATH (L58-89):
disableMapInline(state, ptr)               # spill inline fields into `local _object = {...}`
spreadExp = transformExpression(property.expression)
if !definitelyObject: spreadExp = pushToVarIfComplex(spreadExp, "spread")
loop = `for _k, _v in spreadExp do ptr.value[_k] = _v end`     # tempIds "k","v"; NO pairs()
if !definitelyObject:
  loop = `if <createTruthinessChecks(state, spreadExp, property.expression)> then loop end`
prereq(loop)
```

Caller (transformObjectLiteralExpression L92-115; PORTED literals.go:117-144 minus this
branch): the `ts.isSpreadAssignment(property)` arm currently NYS at literals.go:135 —
replace with the above. `validateSpread` runs from validateMethodAssignment (already ported)
when the spread expression is not itself an object literal.

### 3.2 Spread/rest in DESTRUCTURING — BANNED everywhere (oracle-proven)

Upstream raises its rest-spread rejection = `"Operator `...` is not supported for
destructuring!"` and ABORTS that pattern's transform (early `return`) at ALL seven sites:

| construct | upstream site |
|---|---|
| `const { a, ...rest } = o` | binding/transformObjectBindingPattern.ts:20-23 |
| `const [a, ...rest] = arr` | binding/transformArrayBindingPattern.ts:27 |
| `({ a, ...rest } = o)` (assignment) | binding/transformObjectAssignmentPattern.ts:40-42 |
| `([a, ...rest] = arr)` (assignment) | binding/transformArrayAssignmentPattern.ts:30 + transformBinaryExpression.ts:49 |
| `...[a, b]` array-pattern param rest element | transformParameters.ts:25-28 (inside optimizeArraySpreadParameter) |
| `const { ...r } / [...r]` in variable statements with LuaTuple opts | statements/transformVariableStatement.ts:70 |
| for-of destructure rest | statements/transformForOfStatement.ts:151,175 |

(`...rest: T[]` as a PARAMETER is fine — that's transformParameters' vararg path, already
ported.) The "object spread assignments" smoke-blocker label refers to §3.1 (NYS text at
literals.go:135), NOT to destructuring — randomness has zero destructuring rest (§1.2).
rotor's binding transforms (binding.go/bindingarray.go) should already carry the ban for the
ported paths; verify the object-ASSIGNMENT-pattern arm exists when porting (binary.go:163ff
object destructuring assignment is ported, so its rest arm must Diag too).

---

## 4. Logical assignment operators (nodes/transformLogicalOrCoalescingAssignmentExpression.ts)

Dispatch (BOTH must be wired):

- Statement position: transformExpressionStatement.ts:32-33 — FIRST check inside the
  isBinaryExpression arm: `ts.isLogicalOrCoalescingAssignmentExpression(expression)` →
  `transformLogicalOrCoalescingAssignmentExpressionStatement` =
  `state.capturePrereqs(() => transformLogicalOrCoalescingAssignmentExpression(state, node))`
  (L132-137) — the returned writable is DISCARDED. rotor: replace NYS at statements.go:244-248.
- Expression position: transformBinaryExpression.ts:137-138 (after the `&&`/`||`/`??` logical
  arm, before isAssignmentOperator) → returns the writable (value = re-read of the target).
  rotor: replace NYS at binary.go:125-128.
- Operator keys: `QuestionQuestionEqualsToken` → coalescing; `AmpersandAmpersandEqualsToken`
  → and; `BarBarEqualsToken` (else-branch) → or. tsgo guard:
  `ast.IsLogicalOrCoalescingAssignmentExpression` (tsgo/ast/utilities.go:240).

### 4.1 `??=` — transformCoalescingAssignmentExpression (L8-36)

```
writable = transformWritableExpression(state, left, /*readAfterWrite*/ true)
  # identifier -> itself; a.b -> pushToVarIfNonId(obj) when obj not an id (`local _exp = get()`);
  # a[i] -> also pushToVarIfComplex(index, "index")
[value, valuePrereqs] = capture(transformExpression(state, right))
prereq( if writable == nil then  <valuePrereqs>  writable = value  end )
return writable
```

NO truthiness machinery — strictly `== nil`. RHS prereqs evaluate lazily INSIDE the if.

### 4.2 `&&=` — transformLogicalAndAssignmentExpression (L38-76)

```
writable = transformWritableExpression(state, left, true)
[value, valuePrereqs] = capture(transformExpression(state, right))
conditionId = pushToVar(writable, "condition")          # `local _condition = writable`
prereq( if createTruthinessChecks(state, writable, left) then
          <valuePrereqs>; conditionId = value
        end )
prereq( writable = conditionId )                        # UNCONDITIONAL write-back
return writable
```

Truthiness uses the LEFT node's type (rotor: `CreateTruthinessChecks(s, writable, left,
s.GetType(left))`): `s: string|undefined` → `s ~= "" and s`; `n: number` →
`n ~= 0 and n == n and n` (oracle §5.6). NOTE the condition tests `writable` (the original
target), not conditionId.

### 4.3 `||=` — transformLogicalOrAssignmentExpression (L78-116)

Identical to `&&=` except the if-condition is
`luau.unary("not", createTruthinessChecks(state, writable, left))` →
`if not (n ~= 0 and n == n and n) then`.

Temp-name hints: "condition" (`_condition`, `_condition_1`, ...); writable's object temp
"exp" (`_exp`); index temp "index". `??=` produces NO temps for identifier/id-rooted
property targets.

---

## 5. Oracle-verified worked examples (rbxtsc 3.0.0, 2026-06-07)

### 5.1 Basic class: ctor + property initializer + method + statics + export

```ts
export class Animal {
	name: string;
	legs = 4;
	constructor(name: string) { this.name = name; }
	walk(dist: number) { print(this.name, dist); }
	static create() { return new Animal("a"); }
	static VERSION = 1;
}
class Plain {}
const p = new Plain();
const a = Animal.create();
a.walk(Animal.VERSION);
```

```lua
local Animal
do
	Animal = setmetatable({}, {
		__tostring = function()
			return "Animal"
		end,
	})
	Animal.__index = Animal
	function Animal.new(...)
		local self = setmetatable({}, Animal)
		return self:constructor(...) or self
	end
	function Animal:constructor(name)
		self.legs = 4
		self.name = name
	end
	function Animal:walk(dist)
		print(self.name, dist)
	end
	function Animal:create()
		return Animal.new("a")
	end
	Animal.VERSION = 1
end
local Plain
do
	Plain = setmetatable({}, {
		__tostring = function()
			return "Plain"
		end,
	})
	Plain.__index = Plain
	function Plain.new(...)
		local self = setmetatable({}, Plain)
		return self:constructor(...) or self
	end
	function Plain:constructor()
	end
end
local p = Plain.new()
local a = Animal:create()
a:walk(Animal.VERSION)
...
return {
	Animal = Animal,
}
```

Note `static create` emits/calls as a METHOD (`Animal:create()`) — §2.5. Property
initializer precedes ctor body (no super). `name: string` (no initializer) emits nothing.

### 5.2 Inheritance / super / toString / parameter properties / abstract

```ts
class Base {
	constructor(public x: number, private readonly y = 2) { print("base", x, y); }
	toString() { return "Base"; }
	m() { return this.x; }
}
class Derived extends Base {
	z: number;
	constructor() { print("before super"); super(1); this.z = 3; print("after super"); }
	m() { return super.m() + 1; }
}
class NoCtor extends Base {}
abstract class Abs { abstract a(): void; b() { print("b"); } }
abstract class AbsExtends extends Base { abstract c(): void; }
class FromAbs extends Abs { a() {} }
```

```lua
local Base
do
	Base = setmetatable({}, {
		__tostring = function()
			return "Base"
		end,
	})
	Base.__index = Base
	function Base.new(...)
		local self = setmetatable({}, Base)
		return self:constructor(...) or self
	end
	function Base:constructor(x, y)
		if y == nil then
			y = 2
		end
		self.x = x
		self.y = y
		print("base", x, y)
	end
	function Base:toString()
		return "Base"
	end
	function Base:m()
		return self.x
	end
	function Base:__tostring()
		return self:toString()
	end
end
local Derived
do
	local super = Base
	Derived = setmetatable({}, {
		__tostring = function()
			return "Derived"
		end,
		__index = super,
	})
	Derived.__index = Derived
	function Derived.new(...)
		local self = setmetatable({}, Derived)
		return self:constructor(...) or self
	end
	function Derived:constructor()
		print("before super")
		super.constructor(self, 1)
		self.z = 3
		print("after super")
	end
	function Derived:m()
		return super.m(self) + 1
	end
	function Derived:__tostring()
		return self:toString()
	end
end
local NoCtor
do
	local super = Base
	NoCtor = setmetatable({}, {
		__tostring = function()
			return "NoCtor"
		end,
		__index = super,
	})
	NoCtor.__index = NoCtor
	function NoCtor.new(...)
		local self = setmetatable({}, NoCtor)
		return self:constructor(...) or self
	end
	function NoCtor:constructor(...)
		super.constructor(self, ...)
	end
	function NoCtor:__tostring()
		return self:toString()
	end
end
local Abs
do
	Abs = {}
	function Abs:constructor()
	end
	function Abs:b()
		print("b")
	end
end
local AbsExtends
do
	local super = Base
	AbsExtends = setmetatable({}, {
		__tostring = function()
			return "AbsExtends"
		end,
		__index = super,
	})
	AbsExtends.__index = AbsExtends
	function AbsExtends:constructor(...)
		super.constructor(self, ...)
	end
	function AbsExtends:__tostring()
		return self:toString()
	end
end
local FromAbs
do
	local super = Abs
	FromAbs = setmetatable({}, {
		__tostring = function()
			return "FromAbs"
		end,
		__index = super,
	})
	FromAbs.__index = FromAbs
	function FromAbs.new(...)
		local self = setmetatable({}, FromAbs)
		return self:constructor(...) or self
	end
	function FromAbs:constructor(...)
		super.constructor(self, ...)
	end
	function FromAbs:a()
	end
end
print(Derived.new(), NoCtor.new(2), FromAbs.new(), tostring(Base.new(1)))
```

### 5.3 Class expressions / default export / static block / computed + async methods

```ts
const Foo = class Inner { m() { return Inner; } };
const Bar = class { m() { return 1; } };
class Statics { static counter = 0; static { Statics.counter = 5; print(this.counter); } }
const KEY = "dyn";
class Computed { [KEY]() { return 1; } async go() { return 2; } }
export default class { m() {} }
```

```lua
local TS = require(script.Parent.include.RuntimeLib)
local _class
do
	local Inner = setmetatable({}, {
		__tostring = function()
			return "Inner"
		end,
	})
	Inner.__index = Inner
	function Inner.new(...)
		local self = setmetatable({}, Inner)
		return self:constructor(...) or self
	end
	function Inner:constructor()
	end
	function Inner:m()
		return Inner
	end
	_class = Inner
end
local Foo = _class
local _class_1
do
	_class_1 = setmetatable({}, {
		__tostring = function()
			return "Anonymous"
		end,
	})
	_class_1.__index = _class_1
	function _class_1.new(...)
		local self = setmetatable({}, _class_1)
		return self:constructor(...) or self
	end
	function _class_1:constructor()
	end
	function _class_1:m()
		return 1
	end
end
local Bar = _class_1
local Statics
do
	Statics = setmetatable({}, { __tostring = function() return "Statics" end })  -- (formatted as above)
	Statics.__index = Statics
	function Statics.new(...) ... end
	function Statics:constructor()
	end
	Statics.counter = 0
	do
		Statics.counter = 5
		print(Statics.counter)
	end
end
local KEY = "dyn"
local Computed
do
	... boilerplate ...
	Computed[KEY] = function(self)
		return 1
	end
	Computed.go = TS.async(function(self)
		return 2
	end)
end
local default
do
	default = setmetatable({}, {
		__tostring = function()
			return "default"
		end,
	})
	default.__index = default
	function default.new(...) ... end
	function default:constructor()
	end
	function default:m()
	end
end
return {
	default = default,
}
```

Key facts: named class expression uses VariableDeclaration `local Inner = setmetatable(...)`
inside the do, plus pre-declared temp `_class` and trailing `_class = Inner`; anonymous gets
`__tostring` name "Anonymous"; tempId hint "class" (`_class`, `_class_1`); export-default
class names its identifier literally `default`; static block `this` → `Statics`.

### 5.4 Decorators (experimentalDecorators)

```ts
@Component
@ComponentFactory("hi")
class Decorated {
	@LogProp value = 1;
	@LogProp static svalue = 2;
	@LogMethod method() {}
	@LogMethod static smethod() {}
}
```

```lua
	-- (inside the class do-block, after members)
	LogProp(Decorated, "value")
	local _descriptor = LogMethod(Decorated, "method", {
		value = Decorated.method,
	})
	if _descriptor then
		Decorated.method = _descriptor.value
	end
	LogProp(Decorated, "svalue")
	local _descriptor_1 = LogMethod(Decorated, "smethod", {
		value = Decorated.smethod,
	})
	if _descriptor_1 then
		Decorated.smethod = _descriptor_1.value
	end
	Decorated = ComponentFactory("hi")(Decorated) or Decorated
	Decorated = Component(Decorated) or Decorated
```

(Both class decorators inlined: `Component` is an immutable local function symbol;
`ComponentFactory("hi")` mutates but is the LAST-INITIALIZED decorator → bottom-up
application means `ComponentFactory("hi")` applies FIRST, textually first.)

### 5.5 Hoisting (use before declaration)

```ts
function makeInstance() { return new Late(); }
class Late { tag = "late"; }
```

```lua
local Late
local function makeInstance()
	return Late.new()
end
do
	Late = setmetatable({}, { ... })
	...
end
```

(`local Late` from createHoistDeclaration; the class transform skips its own local.)

### 5.6 Object spread + logical assignments

```ts
const base = { x: 1, y: 2 };
const spreadFirst = { ...base, z: 3 };
const spreadLast = { z: 3, ...base };
const spreadOnly = { ...base };
const spreadTwice = { ...base, ...spreadFirst, w: 4 };
declare const maybe: { x: number } | undefined;
const spreadMaybe = { ...maybe, q: 1 };
const spreadCall = { ...getObj(), b: 2 };
const computedAfterSpread = { ...base, [getObj().a]: 9 };
let v: number | undefined;       v ??= 1;
const holder: { p?: number } = {};  holder.p ??= f();
let s: string | undefined = "x"; s &&= "y"; s ||= "z";
let n = 0;                       n ||= 7;
get().p ??= f();
const useAsExpr = (v ??= 2);
```

```lua
local base = {
	x = 1,
	y = 2,
}
local _object = table.clone(base)
setmetatable(_object, nil)
_object.z = 3
local spreadFirst = _object
local _object_1 = {
	z = 3,
}
for _k, _v in base do
	_object_1[_k] = _v
end
local spreadLast = _object_1
local _object_2 = table.clone(base)
setmetatable(_object_2, nil)
local spreadOnly = _object_2
local _object_3 = table.clone(base)
setmetatable(_object_3, nil)
for _k, _v in spreadFirst do
	_object_3[_k] = _v
end
_object_3.w = 4
local spreadTwice = _object_3
local _object_4 = {}
if maybe then
	for _k, _v in maybe do
		_object_4[_k] = _v
	end
end
_object_4.q = 1
local spreadMaybe = _object_4
local _object_5 = table.clone(getObj())
setmetatable(_object_5, nil)
_object_5.b = 2
local spreadCall = _object_5
local _object_6 = table.clone(base)
setmetatable(_object_6, nil)
_object_6[getObj().a] = 9
local computedAfterSpread = _object_6
local v
if v == nil then
	v = 1
end
local holder = {}
if holder.p == nil then
	holder.p = f()
end
local s = "x"
local _condition = s
if s ~= "" and s then
	_condition = "y"
end
s = _condition
local _condition_1 = s
if not (s ~= "" and s) then
	_condition_1 = "z"
end
s = _condition_1
local n = 0
local _condition_2 = n
if not (n ~= 0 and n == n and n) then
	_condition_2 = 7
end
n = _condition_2
local _exp = get()
if _exp.p == nil then
	_exp.p = f()
end
if v == nil then
	v = 2
end
local useAsExpr = v
```

Note `{ ...maybe, q: 1 }`: `maybe` is an identifier so NO `_spread` temp (pushToVarIfComplex
keeps it) and the truthiness check is bare `maybe` (object|undefined contributes no 0/NaN/""
checks). `{ ...props.event }` (button.tsx) WILL temp: property access is complex.

---

## 6. Diagnostics — byte-exact texts (all already in diagnostics.go)

| key | text (+ extra lines) | trigger |
|---|---|---|
| noReservedClassFields | `Cannot use class field reserved for compiler internal usage.` | prop/method named `new` or `__index` (luau-ast isReservedClassField) — span: member.name |
| noClassMetamethods | `Metamethods cannot be used in class definitions!` | method named any of LUAU_METAMETHODS: `__index __newindex __call __concat __unm __add __sub __mul __div __mod __pow __tostring __metatable __eq __lt __le __mode __gc __len` — span: method.name |
| noGetterSetter | `Getters and Setters are not supported!` + `More information: https://github.com/roblox-ts/roblox-ts/issues/457` | get/set accessor (class member OR object literal) — span: whole accessor |
| noAutoAccessorModifiers | `Getters and Setters are not supported!` + `The `accessor` keyword requires generating get/set accessors` + issue 457 line | `accessor x = ...` — span: the `accessor` KEYWORD modifier |
| noPrivateIdentifier | `Private identifiers are not supported!` | `#x` member — span: the WHOLE CLASS when raised from transformPropertyInitializers (§8 quirk), the identifier elsewhere |
| noInstanceMethodCollisions | `Static methods cannot use the same name as instance methods!` | CHECKER instanceType.getProperty(name) exists for a static method — span: method |
| noStaticMethodCollisions | `Instance methods cannot use the same name as static methods!` | CHECKER classType.getProperty(name) exists for an instance method — span: method |
| noMacroObjectSpread | `Macro classes cannot be used in an object spread!` + suggestion `Did you mean to use an array spread? `[ ...exp ]`` | spread of macro-only class value |
| noGlobalThis | `` `globalThis` is not supported!`` | this/identifier resolving to globalThis symbol |
| expectedMethodGotFunction / expectedFunctionGotMethod | `Attempted to assign non-method where method was expected.` / `Attempted to assign method where non-method was expected.` | heritage-clause method-ness mismatch (§2.8) |
| noAsyncGeneratorFunctions | `Async generator functions are not supported!` | `async *m()` |

Diagnostic ORDER quirk (oracle-confirmed): all constructor-path diagnostics (private
identifier via transformPropertyInitializers) fire BEFORE the member-validation loops; loop 1
(reserved fields, auto-accessor) fires before loop 2 (getter/setter), which fires before the
per-method metamethod check. `new.target` has no special handling (no diagnostic exists; TS
itself permits it only inside functions — not a 3c concern).

## 7. tsgo mapping + rotor implementation sketch

### 7.1 tsgo API equivalences (all verified present)

| upstream | tsgo |
|---|---|
| ts.isClassLike / isClassExpression / isClassDeclaration | `ast.IsClassLike` (tsgo/ast/utilities.go:531), `ast.IsClassExpression`, `ast.IsClassDeclaration` |
| ts.hasAbstractModifier | `ast.HasAbstractModifier` (utilities.go:4211) |
| ts.hasStaticModifier | `ast.HasStaticModifier` (utilities.go:1010) |
| ts.hasSyntacticModifier(n, ModifierFlags.ExportDefault) | `ast.HasSyntacticModifier(n, ast.ModifierFlagsExportDefault)` |
| ts.getDecorators / hasDecorators | `ast.HasDecorators` (utilities.go:3734); collect via `n.Modifiers().Nodes` filtered `KindDecorator` |
| ts.isParameterPropertyDeclaration(p, p.parent) | `ast.IsParameterPropertyDeclaration(p, parent)` (utilities.go:608) |
| ts.isSuperCall / isSuperProperty | `ast.IsSuperCall` (:2055) / `ast.IsSuperProperty` (:4512) |
| ts.isAutoAccessorPropertyDeclaration | `ast.IsAutoAccessorPropertyDeclaration` (:604) |
| ts.isClassStaticBlockDeclaration | `ast.IsClassStaticBlockDeclaration` (ast_generated.go:3700) |
| ts.isAccessor / isSemicolonClassElement / isIndexSignatureDeclaration / isConstructorDeclaration | `ast.IsAccessor` (utilities.go:256), `KindSemicolonClassElement`, `ast.IsIndexSignatureDeclaration`, `ast.IsConstructorDeclaration` |
| ts.getThisContainer(n, false, false) | `ast.GetThisContainer(n, false, false)` (utilities.go:1756) |
| node.symbol | `node.Symbol()` (tsgo/ast/ast.go:229, via DeclarationData) |
| CHECKER getTypeOfSymbolAtLocation | `c.GetTypeOfSymbolAtLocation` (tsgo/checker/checker.go:16323) |
| CHECKER getDeclaredTypeOfSymbol | `c.GetDeclaredTypeOfSymbol` (tsgo/checker/exports.go:160) |
| type.getProperty(name) | `c.GetPropertyOfType(t, name)` (exports.go:124) |
| ts.SymbolFlags.Method | `ast.SymbolFlagsMethod` (symbolflags.go:22) |
| ts.isLogicalOrCoalescingAssignmentExpression | `ast.IsLogicalOrCoalescingAssignmentExpression` (utilities.go:240) |
| ts.getAllSuperTypeNodes | UNEXPORTED `tsgo/ls/utilities.go:915` — reimplement (3 lines) from `ast.GetHeritageElements`/`ast.GetClassExtendsHeritageElement`/`ast.GetImplementsTypeNodes` |
| heritage clause walk (getExtendsNode) | `node.HeritageClauses()` loop, token `KindExtendsKeyword`, `clause.Types.Nodes[0]` — or directly `ast.GetClassExtendsHeritageElement(node)` |
| ts.getPropertyNameForPropertyNameNode | check `ast`/`checker` for exported equivalent; fallback: name.Text() for Identifier/StringLiteral/NumericLiteral (validateHeritageClause arm only) |

luau AST: everything needed exists — `NewMethodCall` (create.go:85), `NewVarArgs` (:100),
MethodDeclaration/FunctionDeclaration/Map/MapField/DoStatement, `luau.ID("self")`/`luau.ID("super")`
(globals.go reserves both), `luau.IsMetamethod`/`IsReservedClassField` (validate.go),
`GlobalProperty("table", "clone")`, `s.RuntimeLib(node, "async")`.

### 7.2 New rotor files / edits

1. `internal/transformer/class.go` (new): transformClassLikeDeclaration + createBoilerplate
   + isClassHoisted + transformClassDeclaration + transformClassExpression +
   transformImplicitClassConstructor + transformClassConstructor +
   transformPropertyInitializers + transformPropertyDeclaration (statics) + getExtendsNode
   + findConstructor + getAllSuperTypeNodes + validateHeritageClause (fills
   methodassignment.go stub).
2. `internal/transformer/decorators.go` (new): transformDecorators + transformMemberDecorators
   + shouldInline + expressionMightMutate (needs `IsSymbolMutable` — already in exports.go
   as the isDefinedAsLet machinery; check name) + the four per-kind callbacks.
3. `internal/transformer/logicalassignment.go` (new, ~90 lines): the three transforms +
   expression/statement entry points.
4. dispatch.go: add `KindClassDeclaration` (statement), `KindClassExpression`,
   `KindThisKeyword`, `KindSuperKeyword` (expressions).
5. statements.go:244-248: NYS → transformLogicalOrCoalescingAssignmentExpressionStatement.
   binary.go:125-128: NYS → expression form.
6. literals.go:133-135: NYS → transformSpreadAssignment (§3.1).
7. call.go:305/348/414: super NYS arms → super emit (§2.4/§2.8).
8. state.go: add `ClassIdentifierMap map[*ast.Node]luau.AnyIdentifier` (key: ClassLike node)
   and `ClassElementObjectKeys map[*ast.Node]luau.Expression` + setter asserting
   no-overwrite (upstream TransformState.ts:387-399) + a `NoPrereqs` helper if absent
   (upstream noPrereqs used by property-decorator keys; equivalent: Capture + assert empty).
9. functions.go transformMethodDeclaration: insert the decorator key-pinning block (§2.5)
   between `name :=` and the isAsync handling.
10. transformThisExpression: needs macroManager globalThis symbol — rotor already registers
    SYMBOL_NAMES globals (identifier.go uses them); reuse.

### 7.3 Risks / verification notes

- BYTE-PARITY of temp numbering: temps allocate in transform order — boilerplate captures
  the extends expression BEFORE `local super`; method prereqs before the method; static
  property name-prereqs before value-prereqs. Follow §2.2 ordering exactly.
- `transformParameters` on a ConstructorDeclaration must NOT inject `self` — confirm
  rotor's `isMethod` returns false for constructor declarations (construct signatures only;
  `GetCallSignatures` empty). Add a regression fixture (`function Base:constructor(x, y)`
  with no self param).
- `instanceType.getProperty("toString")` must see INHERITED props — tsgo GetPropertyOfType
  on the declared (instance) type does. Subclass-re-emission of `__tostring` is the proof
  fixture (§5.2).
- The for-loop in object spread uses GENERALIZED ITERATION (`for _k, _v in spreadExp do`) —
  no `pairs`.
- Static blocks: tsgo `ClassStaticBlockDeclaration.Body` via `node.Body()`; `transformBlock`
  exists (dispatch case KindBlock) but takes the Block node — call the inner list driver the
  same way dispatch does.
- async methods currently NYS in rotor (functions.go:171) — class port keeps that behavior;
  ErrorBoundary has no async members. (TS.async wrap shape documented in §2.5 for when async
  lands.)
- JSX is NOT unlocked by this digest; error-boundary.tsx compiles only because it contains
  no JSX syntax. Other randomness .tsx files still blocked on JSX.

## 8. Quirks (verbatim-port oddities — do NOT "fix")

1. **noPrivateIdentifier spans the whole class** when a `#field` PROPERTY is hit:
   transformPropertyInitializers passes the CLASS node (`errors.noPrivateIdentifier(node)`,
   transformClassConstructor.ts:24) — oracle shows the squiggle covering lines 4-16.
   Method `#names` pass the NAME node instead (transformMethodDeclaration.ts:27).
2. **`return self:constructor(...) or self`** — `.new` returns the constructor's return
   value when truthy (constructors returning a table hijack construction, JS semantics).
3. **Abstract no-extends classes are a bare `{}`** — no metatable, no `__tostring`, AND no
   `X.__index = X` (the `__index` self-assignment lives in the else branch). Abstract
   classes still emit `function X:constructor()` and methods, just no `.new`.
4. **Static methods are colon-methods** (`function C:create()`, called `C:create()`):
   isMethod is type-driven (`this` = class type), not staticness-driven. A static with
   `this: void` would emit dot-form.
5. **`__tostring` wrapper re-emitted per subclass** whose instance type inherits a
   `toString` METHOD (symbol flag check excludes function-typed PROPERTIES).
6. **Anonymous classes stringify as `"Anonymous"`**; export-default unnamed classes are
   named `default` (a valid Luau identifier, not reserved).
7. **`local super` is a plain identifier** — `super` is in luau-ast's reserved globals, so
   user locals named `super`/`self` get renamed elsewhere, never the boilerplate.
8. **Implicit derived constructor is variadic** (`constructor(...)` + `super.constructor(self, ...)`)
   even when the base constructor takes no args.
9. **Super method calls pass self explicitly** (`super.m(self) + 1`) — dot-call, never colon.
10. **Property initializers run between super() and the rest of the body** — and parameter
    properties run BEFORE property initializers. Initializer prereqs (e.g. complex
    defaults) interleave per member inside the constructor.
11. **Spread fast path strips metatables** (`setmetatable(_object, nil)`) even for plain
    object literals — `table.clone` copies the metatable and classes can be spread.
12. **The spread fast path keys off the ptr being an EMPTY inline map** — `{ a: 1, ...b }`
    never table.clones, even though spread-not-first; `{ ...a, ...b }` clones only for the
    first spread.
13. **`&&=`/`||=` always write back** (`s = _condition`) even when the RHS branch did not
    run — a redundant self-assignment in the not-taken case. `??=` does NOT (assignment
    happens inside the if).
14. **Truthiness of the logical-assignment condition tests the ORIGINAL writable**, while
    the mutation goes through `_condition` — order: capture cond temp, if-test writable,
    assign temp, write back.
15. **Decorator application order is bottom-up** (finalizers unshift) while initialization
    is top-down; parameter decorators sandwich between a method's/class's init and apply.
16. **classIdentifierMap uses the INTERNAL name** (named class expressions: the inner
    `Inner`, not `_class`) — `this` in static contexts of a named class expression
    references `Inner`.
17. **Method/property STATIC emission order is source order within staticDeclarations**, but
    ALL methods (static and instance) emit before ALL static properties/blocks.
18. **`validateMethodAssignment` runs for EVERY class member** (loop 2) — including
    constructors and index signatures — before triage skips them.
