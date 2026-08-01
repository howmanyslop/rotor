package transformer_test

import (
	"path/filepath"
	"testing"

	"rotor/internal/luau"
	"rotor/internal/luau/render"
	"rotor/internal/transformer"
)

// All expected text in this file is byte-for-byte what rbxtsc 3.0.0 emits for
// the same source (compiled through testdata/diff/project on 2026-06-07;
// header and trailing return stripped — those belong to TransformSourceFile,
// not the statement list under test).

func renderTryFixture(t *testing.T, relPath string) (string, *transformer.State) {
	t.Helper()
	s := buildState(t, filepath.Join("testdata", "try"), relPath)
	if symbol := s.Checker.GetSymbolAtLocation(s.SourceFile.AsNode()); symbol != nil {
		s.SetModuleIDBySymbol(symbol, luau.GlobalID("exports"))
	}
	statements := transformer.TransformStatementList(s, s.SourceFile.AsNode(), s.SourceFile.Statements.Nodes, nil)
	return render.RenderAST(statements), s
}

// TestTryBasicShapes pins the no-rerouting TS.try call shapes (digest §3.4-3.5,
// §6.2-6.3):
//
//   - try/catch -> bare `TS.try(fn, catchFn)` call statement (2 args);
//   - try/finally (no catch) -> `TS.try(fn, nil, finallyFn)` — the explicit
//     `nil` catch placeholder;
//   - `catch` without a binding -> `function()` (no parameter);
//   - `throw` inside try is NOT rerouted — plain `error(...)` inside the try
//     callback;
//   - catch-then-destructure routes through the ordinary binding transforms
//     (`local _binding = e`);
//   - try/catch/finally with returns everywhere -> 3 args, both try and catch
//     reroute `return TS.TRY_RETURN, { v }`, finally stays plain.
func TestTryBasicShapes(t *testing.T) {
	got, s := renderTryFixture(t, "src/basic.ts")

	want := `local function f1()
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
local function throwInTry()
	TS.try(function()
		error("err")
	end, function(e)
		print(e)
	end)
end
local function catchDestructure()
	TS.try(function()
		print("t")
	end, function(e)
		local name = e.name
		print(name)
	end)
end
local function tcf()
	local _exitType, _returns = TS.try(function()
		return TS.TRY_RETURN, { 1 }
	end, function(e)
		print(e)
		return TS.TRY_RETURN, { 2 }
	end, function()
		print("fin")
	end)
	if _exitType then
		return unpack(_returns)
	end
	return 3
end
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

// TestTryFlowControlRerouting pins every rerouting shape (digest §3.6, §6.2-6.3):
//
//   - f5: return in try AND catch -> `local _exitType, _returns = TS.try(...)`
//     plus single-case collapse `if _exitType then return unpack(_returns) end`;
//   - f6: break + continue (no return) -> the two-id declaration is STILL
//     emitted (`_returns` stays nil); two cases collapse to
//     `if _exitType == TS.TRY_BREAK then break elseif _exitType then continue end`
//     (the LAST case's condition is replaced by the bare truthiness test);
//   - f7: return + break -> `if _exitType == TS.TRY_RETURN then return
//     unpack(_returns) elseif _exitType then break end`;
//   - f8: nested try — the OUTER pair gets the unsuffixed `_exitType`/`_returns`
//     names even though the inner declaration renders first textually (temps
//     created BEFORE the body transform), and the inner try re-tunnels
//     `return _exitType_1, _returns_1` (returnBlocked from inner.parent finds
//     the outer try; the after-pop markTryUses gives the outer its declaration
//     form);
//   - sw: break in try in switch — rerouted as TRY_BREAK; the post-try
//     `if _exitType then break end` breaks the switch's repeat-until-true
//     (ZERO switch-transform changes);
//   - contOnly: continue-only -> single case, bare `if _exitType then continue end`;
//   - finReturn: `return` inside finally also reroutes (TS.try semantics make
//     finally's exitType override everything).
func TestTryFlowControlRerouting(t *testing.T) {
	got, s := renderTryFixture(t, "src/reroute.ts")

	want := `local function f5()
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
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}

// TestTryBoundaries pins the nearest-of blocked checks and the `{ vals }`
// packing rules (digest §3.7-3.8, oracle 2026-06-07):
//
//   - `return $tuple(1, "a")` inside try -> the multi-value list spreads as
//     array MEMBERS: `return TS.TRY_RETURN, { 1, "a" }`;
//   - returning a LuaTuple-returning CALL boxes the call (its multi-returns
//     spread as the array's members): `return TS.TRY_RETURN, { getTuple() }`;
//   - bare `return` inside try -> `return TS.TRY_RETURN, {}`;
//   - try inside an async function: the TS.async wrapper function is the
//     function boundary for statements OUTSIDE the try, but `return` INSIDE
//     the try still reroutes and the collapse `return unpack(_returns)`
//     becomes the async callback's return;
//   - a loop INSIDE try resets break: `break` belongs to the loop (plain);
//   - a function INSIDE try resets return: `return 5` is plain.
func TestTryBoundaries(t *testing.T) {
	got, s := renderTryFixture(t, "src/boundaries.ts")

	want := `local function multi()
	local _exitType, _returns = TS.try(function()
		return TS.TRY_RETURN, { 1, "a" }
	end, function() end)
	if _exitType then
		return unpack(_returns)
	end
	return getTuple()
end
local function tupleVal()
	local _exitType, _returns = TS.try(function()
		return TS.TRY_RETURN, { getTuple() }
	end, function() end)
	if _exitType then
		return unpack(_returns)
	end
	return getTuple()
end
local function bareReturn()
	local _exitType, _returns = TS.try(function()
		if math.random() > 0.5 then
			return TS.TRY_RETURN, {}
		end
		print("x")
	end, function() end)
	if _exitType then
		return unpack(_returns)
	end
end
local tryInAsync = TS.async(function()
	local _exitType, _returns = TS.try(function()
		return TS.TRY_RETURN, { TS.await(TS.Promise.resolve(1)) }
	end, function(e)
		return TS.TRY_RETURN, { 2 }
	end)
	if _exitType then
		return unpack(_returns)
	end
end)
local function loopInTry()
	TS.try(function()
		for i = 0, 2 do
			if i == 1 then
				break
			end
			print(i)
		end
	end, function() end)
end
local function fnInTry()
	TS.try(function()
		local f = function()
			return 5
		end
		print(f())
	end, function() end)
end
`
	if got != want {
		t.Errorf("rendered output differs from rbxtsc:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if ds := s.Diags.Flush(); len(ds) != 0 {
		t.Errorf("unexpected diagnostics: %v", ds)
	}
}
