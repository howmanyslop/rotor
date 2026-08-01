-- Compiled with @isentinel/roblox-ts v4.0.11
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
