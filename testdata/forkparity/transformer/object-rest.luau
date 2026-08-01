-- Compiled with @isentinel/roblox-ts v4.0.11
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
