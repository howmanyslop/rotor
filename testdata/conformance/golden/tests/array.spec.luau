-- Compiled with roblox-ts v3.0.0
local TS = require(script.Parent.Parent.include.RuntimeLib)
return function()
	it("should support optional chain call on array index", function()
		local v = {
			onEnd = { function()
				assert(false, "first member should not be called")
			end, function(arg)
				local _arg0 = arg == "foo"
				assert(_arg0)
			end },
		}
		local n = 1
		local _result = v.onEnd[n + 1]
		if _result ~= nil then
			_result("foo")
		end
		v.onEnd[n + 1]("foo")
	end)
	it("should support optional chain method call on array index", function()
		local v = {
			onEnd = { function(self)
				assert(false, "first member should not be called")
			end, function(self, arg)
				local _arg0 = arg == "foo"
				assert(_arg0)
			end },
		}
		local n = 1
		local _self = v.onEnd
		local _result = _self[n + 1]
		if _result ~= nil then
			_result(_self, "foo")
		end
		local _onEnd = v.onEnd
		_onEnd[n + 1](_onEnd, "foo")
	end)
	it("should support element access", function()
		local arr = { 1, 2, 3 }
		expect(arr[1]).to.equal(1)
		expect(arr[2]).to.equal(2)
		expect(arr[3]).to.equal(3)
		expect(({ 1, 2, 3 })[1]).to.equal(1)
		expect(({ 1, 2, 3 })[2]).to.equal(2)
		expect(({ 1, 2, 3 })[3]).to.equal(3)
		local function foo()
			local result = { 1, 2, 3 }
			return result
		end
		expect(foo()[1]).to.equal(1)
		expect(foo()[2]).to.equal(2)
		expect(foo()[3]).to.equal(3)
		local i = 2
		local a = arr[i + 1]
		local b = arr[3]
		local c = arr[i + 1]
		local d = arr[3]
		expect(a).to.equal(3)
		expect(b).to.equal(3)
		expect(c).to.equal(3)
		expect(d).to.equal(3)
		a = arr[i + 1]
		b = arr[3]
		local _binding = arr
		c = _binding[i + 1]
		local _binding_1 = arr
		d = _binding_1[3]
		expect(a).to.equal(3)
		expect(b).to.equal(3)
		expect(c).to.equal(3)
		expect(d).to.equal(3)
		local function f(v)
			local a = {}
			a[v + 1] = 3
		end
		local Foo
		do
			local _inverse = {}
			Foo = setmetatable({}, {
				__index = _inverse,
			})
			Foo.A = 0
			_inverse[0] = "A"
			Foo.B = 1
			_inverse[1] = "B"
			Foo.C = 2
			_inverse[2] = "C"
		end
		local function g(v)
			local a = {}
			a[v + 1] = 3
		end
	end)
	it("should support.size()", function()
		expect(#{}).to.equal(0)
		expect(#{ 1 }).to.equal(1)
		expect(#{ 1, 2 }).to.equal(2)
		expect(#{ 1, 2, 3 }).to.equal(3)
	end)
	it("should support push", function()
		local a = {}
		table.insert(a, 123)
		expect(a[1]).to.equal(123)
		local Noodle
		do
			Noodle = setmetatable({}, {
				__tostring = function()
					return "Noodle"
				end,
			})
			Noodle.__index = Noodle
			function Noodle.new(...)
				local self = setmetatable({}, Noodle)
				return self:constructor(...) or self
			end
			function Noodle:constructor()
				self.strings = {}
			end
		end
		local noodle = Noodle.new()
		local _exp = noodle.strings
		table.insert(_exp, "Rigatoni")
		local strings = noodle.strings
		table.insert(strings, "Spaghetti")
		table.insert(strings, "Lasagna")
		local _value = #strings
		if _value ~= 0 and _value == _value and _value then
		end
		local _exp_1 = noodle.strings
		table.insert(_exp_1, "Penne")
		expect(#_exp_1).to.equal(4)
		expect(noodle.strings[1]).to.equal("Rigatoni")
		expect(noodle.strings[2]).to.equal("Spaghetti")
		expect(noodle.strings[3]).to.equal("Lasagna")
		expect(noodle.strings[4]).to.equal("Penne")
		local arr = { 0 }
		local _arr = arr
		local _arg2 = arr[2]
		-- ▼ Array.push ▼
		table.insert(_arr, 1)
		table.insert(_arr, 2)
		table.insert(_arr, _arg2)
		-- ▲ Array.push ▲
		expect(arr[1]).to.equal(0)
		expect(arr[2]).to.equal(1)
		expect(arr[3]).to.equal(2)
		expect(arr[4]).to.equal(nil)
		expect(#{ 1, 2 }).to.equal(2)
		local _exp_2 = { 1, 2, 3 }
		-- ▼ Array.push ▼
		table.insert(_exp_2, 4)
		table.insert(_exp_2, 5)
		table.insert(_exp_2, 6)
		-- ▲ Array.push ▲
		expect(#_exp_2).to.equal(6)
	end)
	it("should support pop", function()
		local a = { 456 }
		-- ▼ Array.pop ▼
		local _length = #a
		local _result = a[_length]
		a[_length] = nil
		-- ▲ Array.pop ▲
		local b = _result
		expect(b).to.equal(456)
		expect(#a).to.equal(0)
		expect(a[1]).never.to.be.ok()
	end)
	it("should support join", function()
		local a = { 1, 2, 3 }
		local b = { true, false, true }
		expect(table.concat(a, ", ")).to.equal("1, 2, 3")
		expect(table.concat({ 1, 2, 3 }, ", ")).to.equal("1, 2, 3")
		-- ▼ ReadonlyArray.join ▼
		local _result = table.create(#b)
		for _k, _v in b do
			_result[_k] = tostring(_v)
		end
		-- ▲ ReadonlyArray.join ▲
		expect(table.concat(_result, ", ")).to.equal("true, false, true")
		local _exp = { true, false, true }
		-- ▼ ReadonlyArray.join ▼
		local _result_1 = table.create(#_exp)
		for _k, _v in _exp do
			_result_1[_k] = tostring(_v)
		end
		-- ▲ ReadonlyArray.join ▲
		expect(table.concat(_result_1, ", ")).to.equal("true, false, true")
		local _exp_1 = { 1, "a", true }
		-- ▼ ReadonlyArray.join ▼
		local _result_2 = table.create(#_exp_1)
		for _k, _v in _exp_1 do
			_result_2[_k] = tostring(_v)
		end
		-- ▲ ReadonlyArray.join ▲
		expect(table.concat(_result_2, ", ")).to.equal("1, a, true")
	end)
	it("should support move", function()
		local a = { 1, 2, 3 }
		local b = { 0 }
		table.move(a, 2, 3, 1)
		expect(table.concat(a, ", ")).to.equal("2, 3, 3")
		local _b = b
		table.move(a, 1, 3, 2, _b)
		expect(table.concat(b, ", ")).to.equal("0, 2, 3, 3")
		table.move(b, 1, 2, 3)
		expect(table.concat(b, ", ")).to.equal("0, 2, 0, 2")
	end)
	it("should support shift", function()
		local a = { 1, 2, 3 }
		local b = table.remove(a, 1)
		expect(b).to.equal(1)
		expect(#a).to.equal(2)
		expect(a[1]).to.equal(2)
		expect(a[2]).to.equal(3)
	end)
	it("should support sort", function()
		local x = { 4, 2, 6, 2, 1 }
		table.sort(x)
		expect(x[1]).to.equal(1)
		expect(x[2]).to.equal(2)
		expect(x[3]).to.equal(2)
		expect(x[4]).to.equal(4)
		expect(x[5]).to.equal(6)
		table.sort(x, function(a, b)
			return a < b
		end)
		expect(x).to.equal(x)
		expect(x[1]).to.equal(1)
		expect(x[2]).to.equal(2)
		expect(x[3]).to.equal(2)
		expect(x[4]).to.equal(4)
		expect(x[5]).to.equal(6)
		table.sort(x, function(a, b)
			return a > b
		end)
		expect(x).to.equal(x)
		expect(x[1]).to.equal(6)
		expect(x[2]).to.equal(4)
		expect(x[3]).to.equal(2)
		expect(x[4]).to.equal(2)
		expect(x[5]).to.equal(1)
	end)
	it("should support unshift", function()
		local a = { 1, 2, 3 }
		table.insert(a, 1, 4)
		local b = #a
		table.insert(a, 1, 5)
		expect(a[1]).to.equal(5)
		expect(a[2]).to.equal(4)
		expect(a[3]).to.equal(1)
		expect(a[4]).to.equal(2)
		expect(a[5]).to.equal(3)
		expect(b).to.equal(4)
	end)
	it("should support indexOf", function()
		local a = { 7, 1, 8, 1, 9 }
		expect((table.find(a, 1) or 0) - 1).to.equal(1)
		expect((table.find(a, 2) or 0) - 1).to.equal(-1)
	end)
	it("should support every", function()
		local function even(value)
			return value % 2 == 0
		end
		local function odd(value)
			return not even(value)
		end
		local a = { 1, 2, 3, 4, 5, 6 }
		-- ▼ ReadonlyArray.every ▼
		local _result = true
		for _k, _v in a do
			if not even(_v, _k - 1, a) then
				_result = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result).to.equal(false)
		-- ▼ ReadonlyArray.every ▼
		local _result_1 = true
		for _k, _v in a do
			if not odd(_v, _k - 1, a) then
				_result_1 = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result_1).to.equal(false)
		local b = { 1, 3, 5 }
		-- ▼ ReadonlyArray.every ▼
		local _result_2 = true
		for _k, _v in b do
			if not even(_v, _k - 1, b) then
				_result_2 = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result_2).to.equal(false)
		-- ▼ ReadonlyArray.every ▼
		local _result_3 = true
		for _k, _v in b do
			if not odd(_v, _k - 1, b) then
				_result_3 = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result_3).to.equal(true)
		local c = { 2, 4, 6 }
		-- ▼ ReadonlyArray.every ▼
		local _result_4 = true
		for _k, _v in c do
			if not even(_v, _k - 1, c) then
				_result_4 = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result_4).to.equal(true)
		-- ▼ ReadonlyArray.every ▼
		local _result_5 = true
		for _k, _v in c do
			if not odd(_v, _k - 1, c) then
				_result_5 = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result_5).to.equal(false)
	end)
	it("should support some", function()
		local a = { 1, 2, 3 }
		-- ▼ ReadonlyArray.some ▼
		local _result = false
		local _callback = function(v)
			return v == 2
		end
		for _k, _v in a do
			if _callback(_v, _k - 1, a) then
				_result = true
				break
			end
		end
		-- ▲ ReadonlyArray.some ▲
		expect(_result).to.equal(true)
		-- ▼ ReadonlyArray.some ▼
		local _result_1 = false
		local _callback_1 = function(v)
			return v == 4
		end
		for _k, _v in a do
			if _callback_1(_v, _k - 1, a) then
				_result_1 = true
				break
			end
		end
		-- ▲ ReadonlyArray.some ▲
		expect(_result_1).to.equal(false)
	end)
	it("should support forEach", function()
		local bin = { 1, 2, 3 }
		local str = ""
		-- ▼ ReadonlyArray.forEach ▼
		local _callback = function(v)
			str ..= tostring(v)
			return str
		end
		for _k, _v in bin do
			_callback(_v, _k - 1, bin)
		end
		-- ▲ ReadonlyArray.forEach ▲
		expect(str).to.equal("123")
	end)
	it("should support map", function()
		local a = { 1, 2, 3 }
		-- ▼ ReadonlyArray.map ▼
		local _newValue = table.create(#a)
		local _callback = function(v)
			return v + 1
		end
		for _k, _v in a do
			_newValue[_k] = _callback(_v, _k - 1, a)
		end
		-- ▲ ReadonlyArray.map ▲
		local b = _newValue
		expect(b).never.to.equal(a)
		expect(b[1]).to.equal(2)
		expect(b[2]).to.equal(3)
		expect(b[3]).to.equal(4)
	end)
	it("should support filter", function()
		local a = { 1, 2, 3, 4, 5 }
		-- ▼ ReadonlyArray.filter ▼
		local _newValue = {}
		local _callback = function(v)
			return v % 2 == 0
		end
		local _length = 0
		for _k, _v in a do
			if _callback(_v, _k - 1, a) == true then
				_length += 1
				_newValue[_length] = _v
			end
		end
		-- ▲ ReadonlyArray.filter ▲
		local b = _newValue
		expect(b).never.to.equal(a)
		expect(#b).to.equal(2)
		expect(b[1]).to.equal(2)
		expect(b[2]).to.equal(4)
	end)
	it("should support reduce", function()
		local function reducer(accum, value)
			local _array = {}
			local _length = #_array
			local _accumLength = #accum
			table.move(accum, 1, _accumLength, _length + 1, _array)
			_length += _accumLength
			table.move(value, 1, #value, _length + 1, _array)
			return _array
		end
		local _exp = { { 0, 1 }, { 2, 3 }, { 4, 5 } }
		-- ▼ ReadonlyArray.reduce ▼
		if #_exp == 0 then
			error("Attempted to call `ReadonlyArray.reduce()` on an empty array without an initialValue.")
		end
		local _result = _exp[1]
		local _callback = reducer
		for _i = 2, #_exp do
			_result = _callback(_result, _exp[_i], _i - 1, _exp)
		end
		-- ▲ ReadonlyArray.reduce ▲
		local a = _result
		expect(a[1]).to.equal(0)
		expect(a[2]).to.equal(1)
		expect(a[3]).to.equal(2)
		expect(a[4]).to.equal(3)
		expect(a[5]).to.equal(4)
		expect(a[6]).to.equal(5)
	end)
	it("should support reducing empty arrays only with a initialValue parameter", function()
		expect(function()
			local _exp = {}
			-- ▼ ReadonlyArray.reduce ▼
			if #_exp == 0 then
				error("Attempted to call `ReadonlyArray.reduce()` on an empty array without an initialValue.")
			end
			local _result = _exp[1]
			local _callback = function(previous, current, index, arr)
				return previous .. current
			end
			for _i = 2, #_exp do
				_result = _callback(_result, _exp[_i], _i - 1, _exp)
			end
			-- ▲ ReadonlyArray.reduce ▲
			return _result
		end).to:throw()
		local _exp = {}
		-- ▼ ReadonlyArray.reduce ▼
		local _result = "a"
		local _callback = function(previous, current, index, arr)
			error("This should never run! [1]")
		end
		for _i = 1, #_exp do
			_result = _callback(_result, _exp[_i], _i - 1, _exp)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result).to.equal("a")
	end)
	it("should support reducing arrays with an undefined initialValue", function()
		local _array = {}
		local _length = #_array
		for _char in string.gmatch("😂😄😃😊😉😍", utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		-- ▼ ReadonlyArray.reduce ▼
		local _result = nil
		local _callback = function(previous, current, index, arr)
			local _exp = index
			local _condition = previous
			if not (_condition ~= 0 and _condition == _condition and _condition) then
				_condition = index
			end
			return _exp + _condition
		end
		for _i = 1, #_array do
			_result = _callback(_result, _array[_i], _i - 1, _array)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result).to.equal(16)
		local _exp = {}
		-- ▼ ReadonlyArray.reduce ▼
		local _result_1 = nil
		local _callback_1 = function()
			error("Should not call the reducer function on an empty array! [1]")
		end
		for _i = 1, #_exp do
			_result_1 = _callback_1(_result_1, _exp[_i], _i - 1, _exp)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result_1).to.equal(nil)
	end)
	it("should support reducing single-element arrays without calling a reducer when no initialValue is passed in", function()
		local _exp = { 4 }
		-- ▼ ReadonlyArray.reduce ▼
		if #_exp == 0 then
			error("Attempted to call `ReadonlyArray.reduce()` on an empty array without an initialValue.")
		end
		local _result = _exp[1]
		local _callback = function()
			error("Should not call the reducer function on a single-element array with no initialValue! [1]")
		end
		for _i = 2, #_exp do
			_result = _callback(_result, _exp[_i], _i - 1, _exp)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result).to.equal(4)
	end)
	it("should support reducing forwards or backwards", function()
		local _array = {}
		local _length = #_array
		for _char in string.gmatch("abcdef", utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		-- ▼ ReadonlyArray.reduce ▼
		if #_array == 0 then
			error("Attempted to call `ReadonlyArray.reduce()` on an empty array without an initialValue.")
		end
		local _result = _array[1]
		local _callback = function(previous, current)
			return previous .. current
		end
		for _i = 2, #_array do
			_result = _callback(_result, _array[_i], _i - 1, _array)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result).to.equal("abcdef")
		local _array_1 = {}
		local _length_1 = #_array_1
		for _char in string.gmatch("abcdef", utf8.charpattern) do
			_length_1 += 1
			_array_1[_length_1] = _char
		end
		-- ▼ ReadonlyArray.reduce ▼
		local _result_1 = " "
		local _callback_1 = function(previous, current)
			return previous .. current
		end
		for _i = 1, #_array_1 do
			_result_1 = _callback_1(_result_1, _array_1[_i], _i - 1, _array_1)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result_1).to.equal(" " .. "abcdef")
	end)
	it("should support Array.find", function()
		local a = { 1, 2, 3, 4, 5 }
		-- ▼ ReadonlyArray.find ▼
		local _callback = function(v)
			return v % 2 == 0
		end
		local _result
		for _i, _v in a do
			if _callback(_v, _i - 1, a) == true then
				_result = _v
				break
			end
		end
		-- ▲ ReadonlyArray.find ▲
		local b = _result
		expect(b).to.equal(2)
		-- ▼ ReadonlyArray.find ▼
		local _callback_1 = function(v)
			return v == 6
		end
		local _result_1
		for _i, _v in a do
			if _callback_1(_v, _i - 1, a) == true then
				_result_1 = _v
				break
			end
		end
		-- ▲ ReadonlyArray.find ▲
		local c = _result_1
		expect(c).never.to.be.ok()
		-- ▼ ReadonlyArray.find ▼
		local _callback_2 = function(v)
			return v % 2 ~= 0
		end
		local _result_2
		for _i, _v in a do
			if _callback_2(_v, _i - 1, a) == true then
				_result_2 = _v
				break
			end
		end
		-- ▲ ReadonlyArray.find ▲
		local d = _result_2
		expect(d).to.equal(1)
	end)
	it("should allow spread 1", function()
		local a = { 1, 2, 3 }
		local _array = {}
		local _length = #_array
		local _aLength = #a
		table.move(a, 1, _aLength, _length + 1, _array)
		_length += _aLength
		_array[_length + 1] = 4
		_array[_length + 2] = 5
		_array[_length + 3] = 6
		local b = _array
		expect(b[1]).to.equal(1)
		expect(b[2]).to.equal(2)
		expect(b[3]).to.equal(3)
		expect(b[4]).to.equal(4)
		expect(b[5]).to.equal(5)
		expect(b[6]).to.equal(6)
		expect(#b).to.equal(6)
	end)
	it("should allow spread 2", function()
		local _array = {}
		local _length = #_array
		local _array_1 = { 1 }
		local _Length = #_array_1
		table.move(_array_1, 1, _Length, _length + 1, _array)
		_length += _Length
		local _array_2 = { 2 }
		table.move(_array_2, 1, #_array_2, _length + 1, _array)
		local c = _array
		expect(c[1]).to.equal(1)
		expect(c[2]).to.equal(2)
	end)
	it("should allow spread 3", function()
		local oldArr = { 2 }
		local counter = 0
		local _array = {}
		local _length = #_array
		counter += 1
		_array[_length + 1] = counter
		table.move(oldArr, 1, #oldArr, _length + 2, _array)
		local newArr = _array
		expect(newArr[1]).to.equal(1)
		expect(newArr[2]).to.equal(2)
	end)
	it("should allow spread 4", function()
		local counter = 0
		local _array = {}
		local _length = #_array
		counter += 1
		_array[_length + 1] = counter
		_length += 1
		for _char in string.gmatch("abc", utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		local newArr = _array
		expect(newArr[1]).to.equal(1)
		expect(newArr[2]).to.equal("a")
		expect(newArr[3]).to.equal("b")
		expect(newArr[4]).to.equal("c")
	end)
	it("should allow spread 5", function()
		local counter = 0
		local _array = {}
		local _length = #_array
		counter += 1
		_array[_length + 1] = counter
		_length += 1
		for _v in {
			[2] = true,
		} do
			_length += 1
			_array[_length] = _v
		end
		local newArr = _array
		expect(newArr[1]).to.equal(1)
		expect(newArr[2]).to.equal(2)
	end)
	it("should allow spread 6", function()
		local counter = 0
		local _array = {}
		local _length = #_array
		counter += 1
		_array[_length + 1] = { "a", counter }
		_length += 1
		for _k, _v in {
			b = 2,
		} do
			_length += 1
			_array[_length] = { _k, _v }
		end
		local newArr = _array
		expect(newArr[1][1]).to.equal("a")
		expect(newArr[1][2]).to.equal(1)
		expect(newArr[2][1]).to.equal("b")
		expect(newArr[2][2]).to.equal(2)
	end)
	it("should allow spread 7", function()
		local function foo()
			return TS.generator(function()
				coroutine.yield(2)
			end)
		end
		local counter = 0
		local _array = {}
		local _length = #_array
		counter += 1
		_array[_length + 1] = counter
		_length += 1
		for _result in foo().next do
			if _result.done then
				break
			end
			_length += 1
			_array[_length] = _result.value
		end
		local newArr = _array
		expect(newArr[1]).to.equal(1)
		expect(newArr[2]).to.equal(2)
	end)
	it("should copy on spread", function()
		local a = { 1, 2, 3 }
		local _array = {}
		local _length = #_array
		table.move(a, 1, #a, _length + 1, _array)
		local b = _array
		expect(a).never.to.equal(b)
		expect(#a).to.equal(#b)
		for i = 0, #a - 1 do
			expect(b[i + 1]).to.equal(a[i + 1])
		end
	end)
	it("should unpack spread into function calls", function()
		local function foo(...)
			expect((...)).to.equal(1)
			expect((select(2, ...))).to.equal(2)
			expect((select(3, ...))).to.equal(3)
		end
		foo(unpack({ 1, 2, 3 }))
	end)
	it("should support Array.sort", function()
		local _exp = { "March", "Jan", "Feb", "Dec" }
		table.sort(_exp)
		local months = _exp
		expect(months[1]).to.equal("Dec")
		expect(months[2]).to.equal("Feb")
		expect(months[3]).to.equal("Jan")
		expect(months[4]).to.equal("March")
		local _exp_1 = { 1, 30, 4, 21, 100000 }
		table.sort(_exp_1)
		local array1 = _exp_1
		expect(array1[1]).to.equal(1)
		expect(array1[2]).to.equal(4)
		expect(array1[3]).to.equal(21)
		expect(array1[4]).to.equal(30)
		expect(array1[5]).to.equal(100000)
	end)
	it("should support Array.isEmpty", function()
		local _ = #{} == 0
		local v = #{} == 0
		local arr = {}
		local _1 = #arr == 0
		local x = #arr == 0
		expect(v).to.equal(true)
		table.insert(arr, "Nope")
		expect(#arr == 0).to.equal(false)
	end)
	it("should support Array.unorderedRemove", function()
		local arr = { 0, 1, 2, 3, 4, 5, 6, 7 }
		local i = 2
		local value
		i *= 2
		local _i = i
		-- ▼ Array.unorderedRemove ▼
		local _index = _i + 1
		local _length = #arr
		local _value = arr[_index]
		if _value ~= nil then
			arr[_index] = arr[_length]
			arr[_length] = nil
		end
		-- ▲ Array.unorderedRemove ▲
		expect(_value).to.equal(4)
		expect(#arr).to.equal(7)
		expect(arr[5]).to.equal(7)
		expect(arr[7]).to.equal(6)
	end)
	it("should support spreading non-apparent types", function()
		local function f(arr)
			local _array = {}
			local _length = #_array
			table.move(arr, 1, #arr, _length + 1, _array)
			return _array
		end
		f({ 0, 1 })
	end)
	it("should support Array constructor", function()
		expect(#table.create(10, nil) == 0).to.equal(true)
	end)
	it("should support Array.mapFiltered", function()
		local A
		do
			A = setmetatable({}, {
				__tostring = function()
					return "A"
				end,
			})
			A.__index = A
			function A.new(...)
				local self = setmetatable({}, A)
				return self:constructor(...) or self
			end
			function A:constructor()
			end
			function A:f()
				A.i += 1
				return if A.i > 5 then {
					x = A.i,
				} else nil
			end
			A.i = 0
		end
		local arr = { A.new(), A.new(), A.new(), A.new(), A.new(), A.new(), A.new(), A.new() }
		-- ▼ ReadonlyArray.mapFiltered ▼
		local _newValue = {}
		local _callback = function(a)
			return a:f()
		end
		local _length = 0
		for _k, _v in arr do
			local _result = _callback(_v, _k - 1, arr)
			if _result ~= nil then
				_length += 1
				_newValue[_length] = _result
			end
		end
		-- ▲ ReadonlyArray.mapFiltered ▲
		-- ▼ ReadonlyArray.reduce ▼
		local _result = 0
		local _callback_1 = function(a, c)
			return a + c.x
		end
		for _i = 1, #_newValue do
			_result = _callback_1(_result, _newValue[_i], _i - 1, _newValue)
		end
		-- ▲ ReadonlyArray.reduce ▲
		expect(_result).to.equal(21)
	end)
	it("should support Array.filterUndefined", function()
		local function foo(...)
			local args = { ... }
			-- ▼ ReadonlyArray.filterUndefined ▼
			local _length = 0
			for _i in args do
				if _i > _length then
					_length = _i
				end
			end
			local _result = {}
			local _resultLength = 0
			for _i = 1, _length do
				local _v = args[_i]
				if _v ~= nil then
					_resultLength += 1
					_result[_resultLength] = _v
				end
			end
			-- ▲ ReadonlyArray.filterUndefined ▲
			local safeArgs = _result
			expect(safeArgs[1]).to.equal("A")
			expect(safeArgs[2]).to.equal("B")
			expect(safeArgs[3]).to.equal("C")
		end
		foo(nil, "A", nil, "B", nil, "C", nil)
	end)
	-- this test covers an optimization in offset()
	it("should support indexing with binary expressions", function()
		local arr = { 1, 2, 3 }
		expect(arr[#arr]).to.equal(3)
		expect(arr[#arr - 1]).to.equal(2)
		expect(arr[#arr - 2]).to.equal(1)
		expect(arr[#arr]).to.equal(3)
		expect(arr[#arr + -1]).to.equal(2)
		expect(arr[#arr + -2]).to.equal(1)
		local zero = 0
		expect(arr[zero + 1]).to.equal(1)
		expect(arr[zero + 2]).to.equal(2)
		expect(arr[zero + 3]).to.equal(3)
		expect(arr[zero - -1]).to.equal(1)
		expect(arr[zero - -2]).to.equal(2)
		expect(arr[zero - -3]).to.equal(3)
		expect(arr[zero + 0.1 - -0.9]).to.equal(1)
		expect(arr[zero + 1.1 - -0.9]).to.equal(2)
		expect(arr[zero + 2.1 - -0.9]).to.equal(3)
		expect(arr[#arr - 1.1 + 1.1]).to.equal(3)
		expect(arr[#arr - 2.1 + 1.1]).to.equal(2)
		expect(arr[#arr - 3.1 + 1.1]).to.equal(1)
		expect(arr[zero - -0.1 + 0.9]).to.equal(1)
		expect(arr[zero - -1.1 + 0.9]).to.equal(2)
		expect(arr[zero - -2.1 + 0.9]).to.equal(3)
		expect(arr[#arr + -1.1 - -1.1]).to.equal(3)
		expect(arr[#arr + -2.1 - -1.1]).to.equal(2)
		expect(arr[#arr + -3.1 - -1.1]).to.equal(1)
	end)
end
