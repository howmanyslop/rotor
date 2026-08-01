-- Compiled with roblox-ts v3.0.0
-- optional property access chains
local _num = holder
if _num ~= nil then
	_num = _num.value
	if _num ~= nil then
		_num = _num.num
	end
end
local num = _num
print(num)
-- pure optional access in statement position
local _result = holder
if _result ~= nil then
end
-- optional call of a callback property (a.b?.())
local _result_1 = exact.cb
if _result_1 ~= nil then
	_result_1()
end
local _c = exact.cb
if _c ~= nil then
	_c = _c()
end
local c = _c
print(c)
-- optional call of a real method (a.b?.() with isMethod -> _self)
local _self = exact
local _result_2 = _self.method
if _result_2 ~= nil then
	_result_2(_self, 5)
end
local _self_1 = exact
local _m = _self_1.method
if _m ~= nil then
	_m = _m(_self_1, 7)
end
local m = _m
print(m)
-- optional access AND optional call (a?.b?.() -> double nil check)
local _self_2 = holder
local _result_3 = _self_2
if _result_3 ~= nil then
	_result_3 = _result_3.method
	if _result_3 ~= nil then
		_result_3(_self_2, 1)
	end
end
local _self_3 = holder
local _dm = _self_3
if _dm ~= nil then
	_dm = _dm.method
	if _dm ~= nil then
		_dm = _dm(_self_3, 2)
	end
end
local dm = _dm
print(dm)
-- optional element access (+1 on array types)
local _first = maybeArr
if _first ~= nil then
	_first = _first[1]
end
local first = _first
print(first)
local _v = dict
if _v ~= nil then
	_v = _v.key
end
local v = _v
print(v)
-- bare optional call (fn?.())
local _result_4 = freeFn
if _result_4 ~= nil then
	_result_4()
end
local _f = freeFn
if _f ~= nil then
	_f = _f()
end
local f = _f
print(f)
-- LuaTuple results
local _t = tupleFn
if _t ~= nil then
	_t = { _t() }
end
local t = _t
print(t)
local _self_4 = exact
local _tm = _self_4.tupleMethod
if _tm ~= nil then
	_tm = { _tm(_self_4) }
end
local tm = _tm
print(tm)
-- property-call macro inside an optional chain (a?.pop())
local _result_5 = maybeArr
if _result_5 ~= nil then
	_result_5[#_result_5] = nil
end
local _popped = maybeArr
if _popped ~= nil then
	-- ▼ Array.pop ▼
	local _length = #_popped
	local _result_6 = _popped[_length]
	_popped[_length] = nil
	-- ▲ Array.pop ▲
	_popped = _result_6
end
local popped = _popped
print(popped)
-- macro call as the base of an optional chain
local lookup = {}
local _n2 = lookup.a
if _n2 ~= nil then
	_n2 = _n2.num
end
local n2 = _n2
print(n2)
-- chains ending in calls, methods, and accesses
local _self_5 = holder
local _deep = _self_5
if _deep ~= nil then
	_deep = _deep.getInner
	if _deep ~= nil then
		_deep = _deep(_self_5)
		if _deep ~= nil then
			_deep = _deep.num
		end
	end
end
local deep = _deep
print(deep)
local _result_6 = holder
if _result_6 ~= nil then
	_result_6 = _result_6.value
	if _result_6 ~= nil then
		_result_6 = _result_6.arr
		if _result_6 ~= nil then
			_result_6 = _result_6[2]
		end
	end
end
print(_result_6)
-- optional element calls (a[b]?.())
local _result_7 = handlers.go
if _result_7 ~= nil then
	_result_7()
end
local _h = handlers.stop
if _h ~= nil then
	_h = _h()
end
local h = _h
print(h)
local _result_8 = fnArr
if _result_8 ~= nil then
	_result_8 = _result_8[1]
	if _result_8 ~= nil then
		_result_8()
	end
end
local _fa = fnArr
if _fa ~= nil then
	_fa = _fa[2]
	if _fa ~= nil then
		_fa = _fa()
	end
end
local fa = _fa
print(fa)
return nil
