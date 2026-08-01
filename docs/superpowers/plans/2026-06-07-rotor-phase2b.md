# rotor Phase 2b Implementation Plan — Functions, Destructuring, for-of, switch

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** rotor compiles the constructs that unlock most real-world rbxts code — function declarations/expressions/arrows with full parameter machinery, array/object destructuring, for-of over arrays, switch — plus the reference-walker that completes loop closure copies and case-clause hoisting, eliminating Phase 2's known byte-divergence.

**Architecture:** Same machine as Phase 2: port from `docs/superpowers/research/phase2b-transforms-digest.md` (source of truth, with `reference/roblox-ts/src/` winning on any disagreement), prove with the differential harness (new fixtures → `tools/oracle/oracle.ps1` → `internal/diff/manifest.go`), oracle-pin unit tests via the scratch-file technique. Authority order: reference > digest > plan > tests.

**Conventions (established, not repeated per task):** TDD; byte parity or loud diagnostics — never silent wrong output; upstream quirks ported verbatim with comments; `// Phase 3:` markers for deferred branches (async/generators/macros/non-array iteration), never panics reachable from CompileFile without the recover boundary; PATH refresh for `go` in every shell; full suite + vet + gofmt before every commit; commit per task.

**Out of scope (Phase 3+):** async/generator bodies (`TS.async`/`TS.generator` — emit diagnostics), macros, non-array for-of builders (Map/Set/string/generator — diagnostics; the dispatch table lands now), spread/rest destructuring (the upstream rest-spread rejection is parity work, not deferral), classes, try/catch, imports.

---

## Fixtures (Task 1 adds all; later tasks enable as transforms land)

`14_functions.ts` — function declarations (local + exported), parameters with defaults, rest param usage as array, explicit returns, recursion, function calls of user functions, hoisted mutual recursion:
```typescript
function add(a: number, b: number) {
	return a + b;
}
function greet(name: string, punct = "!") {
	return "hi " + name + punct;
}
function sum(...nums: Array<number>) {
	let total = 0;
	for (const n of nums) {
		total += n;
	}
	return total;
}
function isEven(n: number): boolean {
	return n === 0 ? true : isOdd(n - 1);
}
function isOdd(n: number): boolean {
	return n === 0 ? false : isEven(n - 1);
}
export function double(x: number) {
	return x * 2;
}
print(add(1, 2), greet("bob"), greet("ann", "?"), sum(1, 2, 3), isEven(4), double(21));
```

`15_arrows.ts` — arrow functions: expression bodies, block bodies, stored in consts, passed as arguments, closures over locals, object-literal function values:
```typescript
const square = (x: number) => x * x;
const clamp = (x: number, lo: number, hi: number) => {
	if (x < lo) {
		return lo;
	}
	return x > hi ? hi : x;
};
let counter = 0;
const bump = () => {
	counter += 1;
	return counter;
};
const ops = {
	twice: (x: number) => x * 2,
	apply: (f: (n: number) => number, v: number) => f(v),
};
print(square(5), clamp(15, 0, 10), bump(), bump(), ops.twice(4), ops.apply(square, 6));
```

`16_destructuring.ts` — array/object binding patterns with defaults, nesting, omitted elements; assignment patterns:
```typescript
const arr = [1, 2, 3, 4];
const [first, second] = arr;
const [, , third] = arr;
const [head, ...nothing] = [10];
const obj = { x: 1, y: 2, nested: { z: 3 } };
const { x, y: renamed } = obj;
const { nested: { z } } = obj;
const { x: dx = 99, missing = 7 } = { x: 5 } as { x: number; missing?: number };
let a = 0, b = 0;
[a, b] = [b, a];
({ x: a } = obj);
print(first, second, third, head, x, renamed, z, dx, missing, a, b);
```
NOTE: `...nothing` rest element → upstream raises its rest-spread rejection — REMOVE that line if rbxtsc rejects the file outright (it will — diagnostics abort compilation); keep fixtures compiling clean, and cover the diagnostic in a unit test instead.

`17_forof.ts` — for-of over arrays:
```typescript
const items = [10, 20, 30];
let total = 0;
for (const item of items) {
	total += item;
}
for (const [i, v] of [[1, 2], [3, 4]]) {
	total += i + v;
}
const words = ["a", "b"];
let joined = "";
for (const w of words) {
	joined += w;
	if (joined.size() > 5) {
		break;
	}
}
print(total, joined);
```
NOTE: `.size()` is a MACRO — remove that if/break (use a plain condition like `joined !== ""` … which is banned `!==`? no — `!==` is fine, `!=` is banned). Keep fixtures macro-free.

`18_switch.ts` — switch: literal cases, fallthrough, default, break, case-local variables:
```typescript
function describe(n: number) {
	switch (n) {
		case 0:
			return "zero";
		case 1:
		case 2:
			return "small";
		case 3: {
			const label = "three";
			return label;
		}
		default:
			return "big";
	}
}
let mode = "idle";
switch (mode) {
	case "idle":
		mode = "running";
		break;
	default:
		mode = "unknown";
}
print(describe(0), describe(2), describe(3), describe(9), mode);
```

`19_closures.ts` — loop closure captures + body-write loops (the Phase 2 divergence/panic cases, now fully supported):
```typescript
const fns: Array<() => number> = [];
for (let i = 0; i < 3; i++) {
	fns.push(() => i);
}
```
NOTE: `fns.push` is a MACRO (Array.push) — restructure to avoid macros: assign into a pre-sized record via index writes? `fns[i] = () => i` — element WRITE on array... that's a plain assignment (no macro). Verify rbxtsc accepts it; the optimized loop may make copies unnecessary (per Phase 2 finding upstream optimizes closure-containing loops when preconditions hold) — so ALSO include a non-optimizable variant:
```typescript
const fns: Array<() => number> = [];
for (let i = 0; i !== 3; i++) {
	fns[i] = () => i;
}
let calls = 0;
for (let j = 0; j !== 5; ) {
	j = j + 2;
	calls += 1;
}
print(fns[0](), fns[1](), fns[2](), calls);
```
(The `j` loop is the body-write-no-closure case — Phase 2's documented byte-divergence, which Task 2 fixes.) Final fixture content = whatever compiles clean under rbxtsc; document adjustments.

---

### Task 1: Fixtures + goldens

Add the six fixtures above to `testdata/diff/project/src/` (adjusting per the NOTEs — every fixture must compile clean under rbxtsc; document every adjustment), run `tools/oracle/oracle.ps1`, verify the existing 13 goldens unchanged (`git diff testdata/diff/golden/`), commit fixtures+goldens WITHOUT enabling any (manifest untouched). Read each new golden and include observations in the report (especially: 19's loop shapes — which loops got copies, which optimized; 18's repeat-until-true structure; 14's hoisting of mutual recursion).
Commit: `"diff: phase 2b fixtures and goldens"`

### Task 2: Reference walker + loop closure copies + case hoisting prerequisite

**Digest:** §5 (closure-copy system: isIdWriteOrAsyncRead/canSkipClone, symbolToIdMap rebinding, addFinalizers continue-splicing without nested-loop recursion, _shouldIncrement interplay) + §6 (the walker spec: in-order identifier walk, skip definition, text prefilter, symbol identity + shorthand-value-symbol match, early-out).

Build `internal/transformer/refwalker.go` (the Go `eachSymbolReferenceInFile` per spec §6, unit-tested standalone against a typeprobe-style project) and complete the loop fallback path in `loops.go`: replace `panicOnLoopClosureCapture` with the real copy machinery (copies, finalizers, continue handling). Also wire `checkVariableHoist` in `statements.go` (currently no-op) to the walker per the digest — it only fires for CaseClause-scoped declarations, unreachable until Task 6 enables switch, but land it now WITH its machinery and unit-test it directly via TransformStatementList over a synthetic case-clause-shaped input if expressible, else via Task 6's fixture.
Enable `19_closures`. The `j` body-write loop golden must now match (Phase 2's divergence gone). Remove the CompileFile panic-boundary test's closure expectation (the panic no longer exists; keep the boundary itself — it still guards internal errors). Oracle-pin unit tests for: closure in optimized loop (no copies — verify), closure forcing fallback copies, continue+finalizer interaction.
Commit: `"transformer: reference walker, loop closure copies, case hoisting"`

### Task 3: Functions

**Digest:** §1 (transformFunctionExpression covers FunctionExpression+ArrowFunction; arrow expression-body via transformReturnStatementInner; transformFunctionDeclaration: bodiless overload drop, localize rules, anonymous export default → `local default`; async→diagnostic for now `// Phase 3: TS.async`, generator→diagnostic, async+generator→noAsyncGeneratorFunctions) + transformParameters (implicit `self` via isMethod incl. the object-literal PropertyAssignment quirk, `this` param elision, rest→`local args = { ... }`? — per digest exact shape, defaults via `if p == nil then p = init end`, the `...[a,b]` flattening optimization) + validateMethodAssignment (object literals, deferred from Phase 2) + wrapReturnIfLuaTuple function-context rules + function-body return semantics vs source-file returns.

Files: `internal/transformer/functions.go`, `parameters.go`; wire FunctionDeclaration/FunctionExpression/ArrowFunction in dispatch.go (replacing not-yet-supported), complete `getLastToken` block trailing-comment handling if the digest says function bodies need it. Enable `14_functions`, `15_arrows`. Oracle-pin unit tests: default param shapes, rest param, method-quirk object literal (`{ m: function() {} }` — implicit self?), bodiless overloads, `export default function`.
Commit: `"transformer: functions, arrows, parameters"`

### Task 4: Destructuring

**Digest:** §2 (binding vs assignment pattern systems; the 8-entry accessor table — implement ARRAY + OBJECT accessors live, others raise Phase-3 diagnostics from the same dispatch; objectAccessor incl. computed-vs-literal-numeric +1 quirk; getTypeOfAssignmentPattern CHECKER API — find the tsgo equivalent; optimized multi-local/multi-assign paths gated by arrayBindingPatternContainsHoists; upstream rest-spread rejection firing positions).

Files: `internal/transformer/binding.go` (+ split if large: `bindingarray.go`/`bindingobject.go`). Wire: variable-declaration binding patterns (statements.go), assignment patterns (binary.go destructure branch), parameter patterns (parameters.go from Task 3). Enable `16_destructuring`. Unit tests: nested patterns, defaults with prereqs, omitted elements, and the swap pattern `[a, b] = [b, a]`.
Commit: `"transformer: array and object destructuring"`

### Task 5: for-of (arrays)

**Digest:** §3 (array loop `for _, x in exp do` shapes — exact id handling when the binding is a pattern (inline destructure fast path for `[a, b]` over array-of-arrays per digest), the full builder dispatch table with non-array types raising Phase-3 diagnostics, `$range`/`$tuple` macro entries raising macro diagnostics).

Files: `internal/transformer/forof.go`. Enable `17_forof`. Unit tests: binding-pattern element destructure in loop header, break/continue inside for-of, loop over array-typed expression with prereqs.
Commit: `"transformer: for-of over arrays"`

### Task 6: switch

**Digest:** §4 (repeat-until-true wrapper, parenthesized case expressions, `_fallthrough` flag logic incl. prereq-guarded variant, clauses-after-default silently dropped (verbatim quirk), case-clause hoisting via checkVariableHoist — Task 2 landed the machinery; this wires + proves it).

Files: `internal/transformer/switch.go`. Enable `18_switch`. Unit tests: fallthrough with prereq case expressions, case-local hoisting across clauses (the checkVariableHoist trigger), default-in-middle quirk.
Commit: `"transformer: switch statements"`

### Task 7: Conformance sweep + final gates + merge

1. All 19 fixtures enabled, full suite green, vet, gofmt.
2. Adversarial pass: `20_mixed2b.ts` — closures capturing destructured bindings, functions returning functions, switch inside for-of inside function, arrow defaults referencing earlier params. Oracle, enable, fix divergences.
3. **Real-world smoke**: attempt `CompileFile` over a handful of files from `~/Source/Roblox/randomness` that use only Phase-2/2b constructs (find candidates by grepping for files without imports/classes — there may be none; if none compile-eligible, note it and skip). Report what new not-yet-supported diagnostics dominate (informs Phase 3 priorities).
4. README roadmap: add a "2b" row or fold into the Phase 2 row description (keep it honest: functions/destructuring/for-of/switch ✅). Update memory.
5. Final whole-branch review (opus, same scope as Phase 2's) → fix findings → merge to master → push.
Commit: `"Phase 2b complete: functions, destructuring, for-of, switch"`

---

## Done criteria
1. 19-20 fixtures byte-identical (incl. the former divergence case in 19_closures).
2. The loop closure-capture panic is GONE — replaced by the real copy machinery.
3. Functions/arrows/destructuring/for-of(arrays)/switch compile byte-identically; async/generators/macros/non-array iteration raise clean diagnostics.
4. checkVariableHoist + reference walker live and tested.

**Next (Phase 3):** MacroManager centralization FIRST (review risk #1), then macro tables, classes, try/catch, async/generators, remaining for-of builders, optional chaining.
