-- Compiled with @isentinel/roblox-ts v4.0.11
local TS = _G[script]
local function runtimePromise()
	return TS.Promise.new(function(resolve)
		return resolve(42)
	end)
end
return {
	runtimePromise = runtimePromise,
}
