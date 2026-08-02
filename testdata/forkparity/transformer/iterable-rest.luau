-- Compiled with @isentinel/roblox-ts v4.0.11
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
