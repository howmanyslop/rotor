-- Compiled with roblox-ts v3.0.0
local TS = require(script.Parent.include.RuntimeLib)
local function f1()
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
local function f5()
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
local function multi()
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
return {
	f1 = f1,
	f2 = f2,
	f4 = f4,
	f5 = f5,
	f6 = f6,
	f7 = f7,
	f8 = f8,
	sw = sw,
	contOnly = contOnly,
	finReturn = finReturn,
	tcf = tcf,
	throwInTry = throwInTry,
	catchDestructure = catchDestructure,
	multi = multi,
	tupleVal = tupleVal,
	bareReturn = bareReturn,
	tryInAsync = tryInAsync,
	loopInTry = loopInTry,
	fnInTry = fnInTry,
}
