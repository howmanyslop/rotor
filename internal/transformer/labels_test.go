package transformer_test

import (
	"path/filepath"
	"testing"

	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

// ---------------------------------------------------------------------------
// Loop labels — ROTOR EXTENSION.
//
// UNLIKE every other expectation in this package, the Luau below is AUTHORED,
// not rbxtsc-derived: rbxtsc 3.0.0 rejects every one of these programs with
// `noLabeledStatement`, so there is no oracle to diff against. The expectations
// were reviewed against the Luau semantics of `break` / `continue` /
// `repeat ... until true`; the runtime behaviour is pinned separately by the
// Lune behavioral suite (internal/conformance).
//
// No fixture here may be mirrored into testdata/diff or testdata/conformance —
// rbxtsc cannot generate those goldens, and byte parity for LABEL-FREE code is
// the hard constraint this feature ships under.
// ---------------------------------------------------------------------------

func renderLabelFixture(t *testing.T, name, want string) {
	t.Helper()
	if got := renderFileStatements(t, "src/"+name); got != want {
		t.Errorf("rendered output differs:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// labelDiagnostics transforms a fixture and returns its diagnostics instead of
// failing on them (renderFileStatements fails on any diagnostic).
func labelDiagnostics(t *testing.T, name string) []transformer.Diagnostic {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "control"), "src/"+name)
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	render.RenderAST(statements) // exercise the renderer even on the error paths
	return s.Diags.Flush()
}

// TestLabelBreakOutOfNestedLoop: `break outer` from inside a nested loop cannot
// be a plain Luau `break` (that would only leave the inner loop), so the flag
// is set and the check emitted after the inner loop re-breaks.
func TestLabelBreakOutOfNestedLoop(t *testing.T) {
	renderLabelFixture(t, "labelbreak.ts", `local n = 0
local _outer
for _, a in { 1, 2 } do
	for _1, b in { 1, 2 } do
		if b == 2 then
			_outer = "break"
			break
		end
		n += a * b
	end
	if _outer == "break" then
		break
	end
end
print(n)
`)
}

// TestLabelContinueOutOfNestedLoop: the continue counterpart. The inner loop
// still exits via a plain `break`; the check after it does the `continue`, and
// the labeled loop resets the flag at the top of every iteration — a "continue"
// value survives into the next iteration, unlike "break".
func TestLabelContinueOutOfNestedLoop(t *testing.T) {
	renderLabelFixture(t, "labelcontinue.ts", `local n = 0
local _outer
for _, a in { 1, 2 } do
	_outer = "none"
	for _1, b in { 1, 2 } do
		if b == 2 then
			_outer = "continue"
			break
		end
		n += 1
	end
	if _outer == "continue" then
		continue
	end
	n += 100
end
print(n)
`)
}

// TestLabelBreakThroughSwitch pins the fix for upstream gap 2: rotor lowers a
// switch to `repeat ... until true`, which a plain `break` exits, so the switch
// MUST count as a break boundary. Emitting a bare `break` here (as PR #2928
// does) would silently just leave the switch.
func TestLabelBreakThroughSwitch(t *testing.T) {
	renderLabelFixture(t, "labelswitch.ts", `local n = 0
local _outer
for _, a in { 1, 2, 3 } do
	repeat
		if a == 2 then
			_outer = "break"
			break
		end
	until true
	if _outer == "break" then
		break
	end
	n += a
end
print(n)
`)
}

// TestLabelBlock pins the fix for upstream gap 3: a label on a block becomes
// `repeat ... until true` so it can be broken out of. The label targets that
// innermost boundary, so no flag variable is emitted at all — and the block
// does NOT nest a redundant `do ... end` inside the repeat.
func TestLabelBlock(t *testing.T) {
	renderLabelFixture(t, "labelblock.ts", `local n = 0
repeat
	n += 1
	if n == 1 then
		break
	end
	n += 100
until true
print(n)
`)
}

// TestLabelIfStatement: the same wrapper covers any other labeled non-loop
// statement, which upstream mishandles by emitting a bare `break`.
func TestLabelIfStatement(t *testing.T) {
	renderLabelFixture(t, "labelif.ts", `local n = 0
repeat
	if n == 0 then
		n += 1
		if n == 1 then
			break
		end
		n += 100
	end
until true
print(n)
`)
}

// TestLabelMultipleOnOneLoop pins the fix for upstream gap 5: `a: b: for`
// attaches BOTH labels to the same loop, and a continue check must cover every
// one of them, not just the first.
func TestLabelMultipleOnOneLoop(t *testing.T) {
	renderLabelFixture(t, "labelmulti.ts", `local n = 0
local _a
local _b
for _, x in { 1, 2, 3 } do
	_a = "none"
	_b = "none"
	for _1, y in { 1, 2 } do
		if y == 1 then
			_a = "continue"
			break
		end
		if x == 3 then
			_b = "continue"
			break
		end
		n += 1
	end
	if _a == "continue" or _b == "continue" then
		continue
	end
	n += 100
end
print(n)
`)
}

// TestLabelUnlabeledOuterLoop pins the fix for upstream gap 1. PR #2928 indexes
// the label stack by loop-nesting depth, assuming every enclosing loop carries
// a label; with an UNLABELED loop outside the labeled one it emits a second
// `if _a == "break" then break end` after the `a` loop, killing the outer loop
// too. rotor keys on the label's own break depth, so the check exists only on
// boundaries strictly inside the owner — `n += 100` still runs.
func TestLabelUnlabeledOuterLoop(t *testing.T) {
	renderLabelFixture(t, "labelouter.ts", `local n = 0
for _, x in { 1, 2 } do
	local _a
	for _1, y in { 1, 2 } do
		for _2, z in { 1, 2 } do
			if z == 2 then
				_a = "break"
				break
			end
			n += 1
		end
		if _a == "break" then
			break
		end
	end
	n += 100
end
print(n)
`)
}

// TestLabelFunctionBarrier pins the fix for upstream gap 4: a loop inside a
// closure inside a labeled loop must NOT inherit the label state — a Luau
// `break` cannot cross a function boundary, and the check would read an upvalue
// and break the wrong loop. The closure's loop emits nothing extra.
func TestLabelFunctionBarrier(t *testing.T) {
	renderLabelFixture(t, "labelclosure.ts", `local n = 0
local _outer
for _, a in { 1, 2 } do
	local f = function()
		for _1, b in { 1, 2 } do
			n += b
		end
	end
	f()
	for _1, c in { 1, 2 } do
		if c == 2 then
			_outer = "break"
			break
		end
	end
	if _outer == "break" then
		break
	end
end
print(n)
`)
}

// TestLabelTryBarrier: rotor's try lowering emits Luau functions, so it is a
// barrier too. A label declared INSIDE the try body is fine — its checks stay
// within the same function.
func TestLabelTryBarrier(t *testing.T) {
	renderLabelFixture(t, "labeltryinner.ts", `local n = 0
for _, x in { 1, 2 } do
	TS.try(function()
		local _inner
		for _1, y in { 1, 2 } do
			for _2, z in { 1, 2 } do
				if z == 2 then
					_inner = "break"
					break
				end
				n += 1
			end
			if _inner == "break" then
				break
			end
		end
	end, function(e)
		n += 1
	end)
end
print(n)
`)
}

// TestLabelUnused: a label nothing targets costs nothing — no flag variable, no
// reset, no checks. (Upstream always emits both.)
func TestLabelUnused(t *testing.T) {
	renderLabelFixture(t, "labelunused.ts", `local n = 0
for _, a in { 1, 2 } do
	n += a
end
print(n)
`)
}

// TestLabelWhileStatement: the resets land above the loop body, which for a
// while loop is also above any hoisted condition prereqs.
func TestLabelWhileStatement(t *testing.T) {
	renderLabelFixture(t, "labelwhile.ts", `local n = 0
local _outer
while n < 10 do
	_outer = "none"
	for _, a in { 1, 2 } do
		if a == 2 then
			_outer = "continue"
			break
		end
		n += 1
	end
	if _outer == "continue" then
		continue
	end
	n += 100
end
print(n)
`)
}

// TestLabelDoStatement: `do ... while` lowers to `repeat`, whose body nests the
// statements in an inner `do ... end`. The check lands inside that `do`, where
// a Luau `break` still exits the repeat. Break-only, so no reset.
func TestLabelDoStatement(t *testing.T) {
	renderLabelFixture(t, "labeldo.ts", `local n = 0
local _outer
repeat
	do
		for _, a in { 1, 2 } do
			if a == 2 then
				_outer = "break"
				break
			end
			n += 1
		end
		if _outer == "break" then
			break
		end
	end
until not (n < 10)
print(n)
`)
}

// TestLabelOptimizedForStatement: the numeric-for fast path keeps its shape;
// the reset is unshifted into the same body list.
func TestLabelOptimizedForStatement(t *testing.T) {
	renderLabelFixture(t, "labeloptfor.ts", `local n = 0
local _outer
for i = 0, 2 do
	_outer = "none"
	for j = 0, 2 do
		if j == 2 then
			_outer = "continue"
			break
		end
		n += 1
	end
	if _outer == "continue" then
		continue
	end
	n += 100
end
print(n)
`)
}

// TestLabelFallbackForStatement pins the addFinalizers interaction: the
// generated `if _outer == "continue" then continue end` is an IfStatement at
// body level, so the loop-carried write-back `_i = i` is spliced before its
// `continue` exactly as it is before a hand-written one. The reset sits at the
// very top, above the `_shouldIncrement` guard.
func TestLabelFallbackForStatement(t *testing.T) {
	renderLabelFixture(t, "labelfallbackfor.ts", `local fns = {}
local _outer
do
	local _i = 0
	local _shouldIncrement = false
	while true do
		_outer = "none"
		local i = _i
		if _shouldIncrement then
			i += 1
		else
			_shouldIncrement = true
		end
		if not (i ~= 3) then
			break
		end
		table.insert(fns, function()
			return i
		end)
		for _, a in { 1, 2 } do
			if a == 2 then
				_outer = "continue"
				break
			end
		end
		if _outer == "continue" then
			_i = i
			continue
		end
		_i = i
	end
end
print(#fns)
`)
}

// TestLabelRangeMacro: `$range` takes its own numeric-for path inside
// transformForOfStatement; it shares the one break scope.
func TestLabelRangeMacro(t *testing.T) {
	renderLabelFixture(t, "labelrange.ts", `local n = 0
local _outer
for i = 1, 3 do
	for j = 1, 3 do
		if j == 2 then
			_outer = "break"
			break
		end
		n += i
	end
	if _outer == "break" then
		break
	end
end
print(n)
`)
}

// TestLabelForOfDestructuring: the inline `[k, v]` Map fast path unshifts its
// own bindings into the same body list the resets go into.
func TestLabelForOfDestructuring(t *testing.T) {
	renderLabelFixture(t, "labeldestructure.ts", `local n = 0
local map = {}
local _outer
for k, v in map do
	for _, w in { 1, 2 } do
		if w == 2 then
			_outer = "break"
			break
		end
		n += v
	end
	if _outer == "break" then
		break
	end
end
print(n)
`)
}

// TestLabelWithinTryCatchDiagnostic: a label whose target lies outside an
// enclosing try is rejected. The try reroute rides the TS.TRY_BREAK /
// TRY_CONTINUE sentinel protocol, which carries no label — extending it is
// deliberately out of scope. Message is byte-exact with roblox-ts PR #2928.
func TestLabelWithinTryCatchDiagnostic(t *testing.T) {
	for _, name := range []string{"labeltrybreak.ts", "labeltrycontinue.ts"} {
		ds := labelDiagnostics(t, name)
		if !hasDiagnostic(ds, "noLabeledStatementsWithinTryCatch", "labels are not supported within try/catch blocks!") {
			t.Errorf("%s: expected noLabeledStatementsWithinTryCatch, got %v", name, ds)
		}
	}
}

// TestLabelUnknownDiagnostic: TypeScript already errors on an undefined label,
// so this is only reachable behind `// @ts-ignore`. rotor fails loudly rather
// than emitting a `break` aimed at whatever loop happens to be nearest.
func TestLabelUnknownDiagnostic(t *testing.T) {
	ds := labelDiagnostics(t, "labelunknown.ts")
	if !hasDiagnostic(ds, "rotorUnknownLabel", "no enclosing label named `nope`") {
		t.Errorf("expected rotorUnknownLabel, got %v", ds)
	}
}

// TestLabelCapturedFlowControlDiagnostic: the `repeat ... until true` wrapper a
// labeled non-loop statement needs is itself a break boundary, so an UNLABELED
// `break`/`continue` inside it that TypeScript binds to an enclosing loop would
// silently retarget the wrapper. Rejected rather than miscompiled.
func TestLabelCapturedFlowControlDiagnostic(t *testing.T) {
	ds := labelDiagnostics(t, "labelcapture.ts")
	if !hasDiagnostic(ds, "rotorLabeledStatementFlowControl", "cannot contain an unlabeled") {
		t.Errorf("expected rotorLabeledStatementFlowControl, got %v", ds)
	}
}

// TestLabelContinueInDoWhileDiagnostic: `do { ... } while (<needs temps>)` puts
// the condition's prereq locals in the emitted repeat body, and Luau forbids a
// `continue` that jumps over them. See TestPlainContinueInDoWhileUnchanged for
// why this is diagnosed only on the labeled path.
func TestLabelContinueInDoWhileDiagnostic(t *testing.T) {
	ds := labelDiagnostics(t, "labeldocond.ts")
	if !hasDiagnostic(ds, "rotorLabeledContinueInDoWhile", "cannot target a do/while loop") {
		t.Errorf("expected rotorLabeledContinueInDoWhile, got %v", ds)
	}
}

// TestPlainContinueInDoWhileUnchanged pins the PRE-EXISTING rbxtsc hole the
// diagnostic above sidesteps: an unlabeled `continue` in a do/while with a
// complex condition already emits a `continue` ahead of `local _condition`,
// which Luau rejects. rbxtsc 3.0.0 emits exactly this, so rotor must too —
// byte parity outranks correctness here, and the label feature must not become
// a second way in.
func TestPlainContinueInDoWhileUnchanged(t *testing.T) {
	renderLabelFixture(t, "labelplaindocond.ts", `local n = 0
local function a()
	n += 1
	return n < 5
end
repeat
	do
		if n == 2 then
			continue
		end
		n += 1
	end
	local _condition = a()
	if _condition then
		local _original = n
		n += 1
		_condition = _original < 20
	end
until not _condition
print(n)
`)
}
