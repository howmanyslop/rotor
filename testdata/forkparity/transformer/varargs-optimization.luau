-- Compiled with @isentinel/roblox-ts v4.0.11
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
