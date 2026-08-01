-- Compiled with @isentinel/roblox-ts v4.0.11
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
