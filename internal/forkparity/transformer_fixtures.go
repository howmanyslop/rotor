package forkparity

// allow: SIZE_OK - authoritative compiler-captured fixture table.

// TransformerFixture is one self-contained TypeScript semantic probe and its
// archive-authoritative compiler result.
type TransformerFixture struct {
	Name         string
	TSCode       string
	ExpectedLuau string
	Diagnostics  []string
	Category     string
}

// FixtureProvenance identifies the immutable compiler input used for fixtures.
type FixtureProvenance struct {
	ZipDigest          string
	ExtractionCommand  string
	CompilerInvocation string
}

// TransformerFixtureProvenance describes the archive and command that captured
// the transformer fixture goldens.
func TransformerFixtureProvenance() FixtureProvenance {
	return FixtureProvenance{
		ZipDigest:          committedZipDigest,
		ExtractionCommand:  "VerifyAndExtract(FindZip(repoRoot))",
		CompilerInvocation: "node <extractDir>/roblox-ts/dist/CLI/cli.cjs --project <fixtureDir>",
	}
}

// AllTransformerFixtures returns the fork-authoritative transformer fixtures in
// deterministic compiler-feature order.
func AllTransformerFixtures() []TransformerFixture {
	return []TransformerFixture{
		{
			Name:     "array-rest",
			Category: "spread-destructuring",
			TSCode: `export function arrayRest() {
	const [a, ...b] = [1, 2, 3];
	return b;
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function arrayRest()
	local _binding = { 1, 2, 3 }
	local a = _binding[1]
	local b = table.move(_binding, 2, #_binding, 1, {})
	return b
end
return {
	arrayRest = arrayRest,
}
`,
		},
		{
			Name:     "object-rest",
			Category: "spread-destructuring",
			TSCode: `export function objectRest() {
	const obj = { a: 1, b: 2, c: 3 };
	const { b: bb, ...rest } = obj;
	return rest;
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function objectRest()
	local obj = {
		a = 1,
		b = 2,
		c = 3,
	}
	local bb = obj.b
	local _extracted = {
		["b"] = true,
	}
	local _rest = {}
	for _k, _v in obj do
		if not _extracted[_k] then
			_rest[_k] = _v
		end
	end
	local rest = _rest
	return rest
end
return {
	objectRest = objectRest,
}
`,
		},
		{
			Name:     "iterable-rest",
			Category: "spread-destructuring",
			TSCode: `export function restSet(s: Set<number>) {
	const [x, ...rest] = s;
	return rest;
}

export function restMap(m: Map<string, number>) {
	const [x, ...rest] = m;
	return rest;
}

export function* gen() {
	yield 1;
	yield 2;
	yield 3;
}

export function restGenerator() {
	const [x, ...rest] = gen();
	return rest;
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local TS = _G[script]
local function restSet(s)
	local _value = next(s)
	local x = _value
	local _extracted = {
		[_value] = true,
	}
	local _rest = {}
	for _k in s do
		if not _extracted[_k] then
			table.insert(_rest, _k)
		end
	end
	local rest = _rest
	return rest
end
local function restMap(m)
	local _k, _v = next(m)
	local x = { _k, _v }
	local _extracted = {
		[_k] = true,
	}
	local _rest = {}
	for _k_1, _v_1 in m do
		if not _extracted[_k_1] then
			table.insert(_rest, { _k_1, _v_1 })
		end
	end
	local rest = _rest
	return rest
end
local function gen()
	return TS.generator(function()
		coroutine.yield(1)
		coroutine.yield(2)
		coroutine.yield(3)
	end)
end
local function restGenerator()
	local _binding = gen()
	local x = _binding.next().value
	local _rest = {}
	while true do
		local _v = _binding.next()
		if _v.done == true then
			break
		end
		table.insert(_rest, _v.value)
	end
	local rest = _rest
	return rest
end
return {
	restSet = restSet,
	restMap = restMap,
	gen = gen,
	restGenerator = restGenerator,
}
`,
		},
		{
			Name:     "varargs-optimization",
			Category: "varargs",
			TSCode: `export function safeRead(index: number, ...args: Array<number>) {
	const a = args[0];
	const b = args[1];
	const c = args[index];
	return a + b + c;
}

export function safeSize(...args: Array<number>) {
	return args.size();
}

export function safeForOf(...args: Array<number>) {
	let sum = 0;
	for (const x of args) {
		sum += x;
	}
	return sum;
}

export function unsafeNested(...args: Array<number>) {
	return function () {
		return args[0];
	};
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function safeRead(index, ...)
	local a = (...)
	local b = (select(2, ...))
	local c = (select(index + 1, ...))
	return a + b + c
end
local function safeSize(...)
	return select("#", ...)
end
local function safeForOf(...)
	local sum = 0
	for _i = 1, select("#", ...) do
		local x = (select(_i, ...))
		sum += x
	end
	return sum
end
local function unsafeNested(...)
	local args = { ... }
	return function()
		return args[1]
	end
end
return {
	safeRead = safeRead,
	safeSize = safeSize,
	safeForOf = safeForOf,
	unsafeNested = unsafeNested,
}
`,
		},
		{
			Name:     "bitwise-flatten-and-compound",
			Category: "bitwise",
			TSCode: `export function flatAnd(a: number, b: number, c: number) {
	return a & b & c;
}

export function compound(bw: number) {
	bw &= 3;
	return bw;
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function flatAnd(a, b, c)
	return bit32.band(a, b, c)
end
local function compound(bw)
	bw = bit32.band(bw, 3)
	return bw
end
return {
	flatAnd = flatAnd,
	compound = compound,
}
`,
		},
		{
			Name:     "shared-table-iteration",
			Category: "shared-table",
			TSCode: `export function iterateTable(t: SharedTable) {
	let sum = 0;
	for (const [k, v] of t) {
		sum += v as number;
	}
	return sum;
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function iterateTable(t)
	local sum = 0
	for k, v in t do
		sum += v
	end
	return sum
end
return {
	iterateTable = iterateTable,
}
`,
		},
		{
			Name:     "range-literal-step",
			Category: "range",
			TSCode: `export function descendingRange() {
	for (const i of $range(9, 1, -1)) {
		print(i);
	}
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function descendingRange()
	for i = 9, 1, -1 do
		print(i)
	end
end
return {
	descendingRange = descendingRange,
}
`,
		},
		{
			Name:     "nullable-lua-tuple",
			Category: "lua-tuple",
			TSCode: `export function foo(): LuaTuple<[number, string]> | undefined {
	return $tuple(1, "a");
}

export function bar(): LuaTuple<[number, string]> | undefined {
	return undefined;
}

export function indexNullable() {
	const a = foo()?.[1];
	const b = bar()?.[1];
	return a;
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local function foo()
	return 1, "a"
end
local function bar()
	return nil
end
local function indexNullable()
	local _a = { foo() }
	if _a ~= nil then
		_a = _a[2]
	end
	local a = _a
	local _b = { bar() }
	if _b ~= nil then
		_b = _b[2]
	end
	local b = _b
	return a
end
return {
	foo = foo,
	bar = bar,
	indexNullable = indexNullable,
}
`,
		},
		{
			Name:     "bigint-property-diagnostic",
			Category: "diagnostics",
			TSCode: `export function bigintProperty() {
	const value = { 1n: 2 };
	return value;
}
`,
			Diagnostics: []string{
				"TS1539: A 'bigint' literal cannot be used as a property name.",
			},
		},
		{
			Name:     "runtime-import",
			Category: "runtime",
			TSCode: `export function runtimePromise() {
	return new Promise<number>(resolve => resolve(42));
}
`,
			ExpectedLuau: `-- Compiled with @isentinel/roblox-ts v4.0.11
local TS = _G[script]
local function runtimePromise()
	return TS.Promise.new(function(resolve)
		return resolve(42)
	end)
end
return {
	runtimePromise = runtimePromise,
}
`,
		},
	}
}
