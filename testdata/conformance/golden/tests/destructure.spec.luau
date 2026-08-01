-- Compiled with roblox-ts v3.0.0
local TS = require(script.Parent.Parent.include.RuntimeLib)
-- eslint-disable prefer-const 
return function()
	it("should destructure simple arrays", function()
		local a, b = 1, 2
		expect(a).to.equal(1)
		expect(b).to.equal(2)
	end)
	it("should destructure nested arrays", function()
		local _binding, _binding_1 = { 7, 2 }, { 8, 9 }
		local a = _binding[1]
		local b = _binding[2]
		local c = _binding_1[1]
		local d = _binding_1[2]
		expect(a).to.equal(7)
		expect(b).to.equal(2)
		expect(c).to.equal(8)
		expect(d).to.equal(9)
	end)
	it("should destructure simple objects", function()
		local a = {
			b = 1,
			c = 2,
			d = 3,
		}
		local b = a.b
		local c = a.c
		local d = a.d
		expect(b).to.equal(1)
		expect(c).to.equal(2)
		expect(d).to.equal(3)
	end)
	it("should destructure nested objects", function()
		local a = {
			b = {
				c = {
					d = 123,
				},
			},
		}
		local _binding = a.b
		local _binding_1 = _binding.c
		local d = _binding_1.d
		expect(d).to.equal(123)
	end)
	it("should destructure mixed objects", function()
		local a = {
			b = {
				c = { 8, 1, 4 },
			},
		}
		local _binding = a.b
		local _binding_1 = _binding.c
		local eight = _binding_1[1]
		local one = _binding_1[2]
		local four = _binding_1[3]
		expect(eight).to.equal(8)
		expect(one).to.equal(1)
		expect(four).to.equal(4)
	end)
	it("should support default values", function()
		local foo = {
			a = {
				b = 1,
			},
		}
		local bar = {
			a = {
				b = nil,
			},
		}
		do
			local _binding = foo.a
			local b = _binding.b
			if b == nil then
				b = 5
			end
			expect(b).to.equal(1)
		end
		do
			local _binding = bar.a
			local b = _binding.b
			if b == nil then
				b = 5
			end
			expect(b).to.equal(5)
		end
	end)
	it("should support aliases", function()
		local foo = {
			a = {
				b = 123,
			},
		}
		local _binding = foo.a
		local z = _binding.b
		expect(z).to.equal(123)
	end)
	it("should not optimize array destructuring", function()
		local function a()
			return { 1, 2, 3 }
		end
		local _binding = a()
		local d = _binding[1]
		local e = _binding[2]
		local f = _binding[3]
		expect(d).to.equal(1)
		expect(e).to.equal(2)
		expect(f).to.equal(3)
	end)
	it("should optimize tuple destructuring", function()
		local function a()
			return { 1, 2, 3 }
		end
		local _binding = a()
		local d = _binding[1]
		local e = _binding[2]
		local f = _binding[3]
		expect(d).to.equal(1)
		expect(e).to.equal(2)
		expect(f).to.equal(3)
	end)
	it("should optimize tuple destructuring with omitted expressions", function()
		local function a()
			return { 1, 2, 3, 4 }
		end
		local _binding = a()
		local b = _binding[2]
		local c = _binding[4]
		expect(b).to.equal(2)
		expect(c).to.equal(4)
	end)
	it("should support destructure assignment", function()
		local x
		local y
		local z
		local _binding
		x, y, _binding = 1, 2, { 3 }
		z = _binding[1]
		expect(x).to.equal(1)
		expect(y).to.equal(2)
		expect(z).to.equal(3)
	end)
	it("should support destructure assignment with identifier", function()
		local x
		local y
		local z
		local obj = { 1, 2, { 3 } }
		local _binding = obj
		x = _binding[1]
		y = _binding[2]
		local _binding_1 = _binding[3]
		z = _binding_1[1]
		expect(x).to.equal(1)
		expect(y).to.equal(2)
		expect(z).to.equal(3)
	end)
	it("should support destructure assignment as expression", function()
		local function test(obj)
			expect(obj[1]).to.equal(1)
			expect(obj[2]).to.equal(2)
			expect(obj[3][1]).to.equal(3)
		end
		local x
		local y
		local z
		local _binding = { 1, 2, { 3 } }
		x = _binding[1]
		y = _binding[2]
		local _binding_1 = _binding[3]
		z = _binding_1[1]
		test(_binding)
		expect(x).to.equal(1)
		expect(y).to.equal(2)
		expect(z).to.equal(3)
	end)
	it("should support destructure assignment with property access", function()
		local obj = {
			x = 0,
			y = 0,
			z = 0,
		}
		local _binding
		obj.x, obj.y, _binding = 1, 2, { 3 }
		obj.z = _binding[1]
		expect(obj.x).to.equal(1)
		expect(obj.y).to.equal(2)
		expect(obj.z).to.equal(3)
	end)
	it("should support indexing a return value from a function", function()
		local function foo()
			return { 1, 2, 3 }
		end
		local function bar()
			return { 4, 5, 6 }
		end
		expect(foo()[1]).to.equal(1)
		expect(foo()[2]).to.equal(2)
		expect(foo()[3]).to.equal(3)
		expect(bar()[1]).to.equal(4)
		expect(bar()[2]).to.equal(5)
		expect(bar()[3]).to.equal(6)
	end)
	it("should support accessing the size method", function()
		local _ = #{ 1, 2, 3 }
		expect(#{ 1, 2, 3 }).to.equal(3)
	end)
	it("should destructure properly into already declared variables", function()
		local a
		local _binding = {
			[4] = true,
		}
		local _value = next(_binding)
		a = _value
		expect(a).to.equal(4)
		local len
		local _binding_1 = { 1, 2, 3 }
		len = _binding_1[3]
		expect(len).to.equal(3)
		local y = 0
		local _binding_2 = {
			x = 1,
		}
		y = _binding_2.x
		expect(y).to.equal(1)
	end)
	it("should destructure computed property types as well (number-only)", function()
		local array = {}
		-- ▼ Array.push ▼
		table.insert(array, 1)
		table.insert(array, 2)
		table.insert(array, 3)
		table.insert(array, 4)
		-- ▲ Array.push ▲
		local function f(i)
			local num
			local _binding = array
			num = _binding[i + 1]
			return num
		end
		expect(f(0)).to.equal(1)
		expect(f(1)).to.equal(2)
		expect(f(2)).to.equal(3)
		expect(f(3)).to.equal(4)
	end)
	it("should destructure into objects", function()
		local o = {
			a = 100,
			b = 100,
			c = 100,
			d = 100,
			[1] = 100,
			e = 100,
		}
		local f = function()
			return "6"
		end
		-- prettier-ignore
		local _binding = {
			a = 1,
			b = 2,
			c = 3,
			["9"] = 4,
			[5] = 5,
			["6"] = 6,
		}
		o.a = _binding.a
		o.b = _binding.b
		o.c = _binding["9"]
		o.d = _binding[5]
		o.e = _binding[f()]
		o[1] = _binding.c
		local i = 0
		local _to = expect(o.a).to
		i += 1
		_to.equal(i)
		local _to_1 = expect(o.b).to
		i += 1
		_to_1.equal(i)
		local _to_2 = expect(o[1]).to
		i += 1
		_to_2.equal(i)
		local _to_3 = expect(o.c).to
		i += 1
		_to_3.equal(i)
		local _to_4 = expect(o.d).to
		i += 1
		_to_4.equal(i)
		local _to_5 = expect(o.e).to
		i += 1
		_to_5.equal(i)
	end)
	it("should properly destructure optimized strings", function()
		local truth = { "a", "b", "c", "d", "e", "f", "g" }
		local _array = {}
		local _length = #_array
		for _char in string.gmatch("abcdefg", utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		-- ▼ ReadonlyArray.every ▼
		local _result = true
		local _callback = function(x, i)
			return truth[i + 1] == x
		end
		for _k, _v in _array do
			if not _callback(_v, _k - 1, _array) then
				_result = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result).to.equal(true)
	end)
	it("should properly destructure optimized strings with a ' as quotes", function()
		local truth = { "a", "b", "c", '"', "d", "e", "f", "g" }
		local _array = {}
		local _length = #_array
		for _char in string.gmatch('abc"defg', utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		-- ▼ ReadonlyArray.every ▼
		local _result = true
		local _callback = function(x, i)
			return truth[i + 1] == x
		end
		for _k, _v in _array do
			if not _callback(_v, _k - 1, _array) then
				_result = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result).to.equal(true)
	end)
	it("should properly destructure optimized strings with backslashes", function()
		local truth = { "a", "\n", "b", "c", '"', "d", "e", "\t", "f", "\\", "g" }
		local _array = {}
		local _length = #_array
		for _char in string.gmatch('a\nbc"de\tf\\g', utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		-- ▼ ReadonlyArray.every ▼
		local _result = true
		local _callback = function(x, i)
			return truth[i + 1] == x
		end
		for _k, _v in _array do
			if not _callback(_v, _k - 1, _array) then
				_result = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result).to.equal(true)
	end)
	it("should properly destructure strings with a ` as quotes", function()
		local truth = { "'", "a", "b", "c", '"', "d", "e", "f", "g", "'" }
		local _array = {}
		local _length = #_array
		for _char in string.gmatch(`'abc"defg'`, utf8.charpattern) do
			_length += 1
			_array[_length] = _char
		end
		-- ▼ ReadonlyArray.every ▼
		local _result = true
		local _callback = function(x, i)
			return truth[i + 1] == x
		end
		for _k, _v in _array do
			if not _callback(_v, _k - 1, _array) then
				_result = false
				break
			end
		end
		-- ▲ ReadonlyArray.every ▲
		expect(_result).to.equal(true)
	end)
	it("should properly destruct gmatch #1", function()
		local function catchLetters(...)
			local letterPairs = { ... }
			local i = 97
			for _, _binding in letterPairs do
				local a = _binding[1]
				local b = _binding[2]
				local _to = expect(a).to
				local _original = i
				i += 1
				_to.equal(string.char(_original))
				local _to_1 = expect(b).to
				local _original_1 = i
				i += 1
				_to_1.equal(string.char(_original_1))
			end
		end
		local _array = {}
		local _length = 0
		local _iterFunc = string.gmatch("abcdefghijklmnopqrstuvwxyz", "(%l)(%l)")
		while true do
			local _results = { _iterFunc() }
			if #_results == 0 then
				break
			end
			_length += 1
			_array[_length] = _results
		end
		catchLetters(unpack(_array))
	end)
	it("should properly destruct gmatch #2", function()
		local _binding = string.gmatch("a,b,c", "[^,]+")
		local _binding_1 = { _binding() }
		local a = _binding_1[1]
		local _binding_2 = { _binding() }
		local b = _binding_2[1]
		local _binding_3 = { _binding() }
		local c = _binding_3[1]
		expect(a).to.equal("a")
		expect(b).to.equal("b")
		expect(c).to.equal("c")
	end)
	it("should properly destruct gmatch #3", function()
		local _binding = string.gmatch("z,a,b,c", "[^,]+")
		_binding()
		local _binding_1 = { _binding() }
		local a = _binding_1[1]
		local _binding_2 = { _binding() }
		local b = _binding_2[1]
		local _binding_3 = { _binding() }
		local c = _binding_3[1]
		expect(a).to.equal("a")
		expect(b).to.equal("b")
		expect(c).to.equal("c")
	end)
	it("should properly destruct sets", function()
		local expected = {
			[1] = true,
			[2] = true,
			[3] = true,
		}
		local _binding = {
			[1] = true,
			[2] = true,
			[3] = true,
		}
		local _value = next(_binding)
		local a = _value
		local _value_1 = next(_binding, _value)
		local b = _value_1
		local _value_2 = next(_binding, _value_1)
		local c = _value_2
		-- ▼ Set.delete ▼
		local _valueExisted = expected[a] ~= nil
		expected[a] = nil
		-- ▲ Set.delete ▲
		expect(_valueExisted).to.equal(true)
		-- ▼ Set.delete ▼
		local _valueExisted_1 = expected[b] ~= nil
		expected[b] = nil
		-- ▲ Set.delete ▲
		expect(_valueExisted_1).to.equal(true)
		-- ▼ Set.delete ▼
		local _valueExisted_2 = expected[c] ~= nil
		expected[c] = nil
		-- ▲ Set.delete ▲
		expect(_valueExisted_2).to.equal(true)
	end)
	it("should properly destruct maps", function()
		local expected = {
			a = 1,
			b = 2,
			c = 3,
		}
		local _binding = {
			a = 1,
			b = 2,
			c = 3,
		}
		local _k, _v = next(_binding)
		local a = { _k, _v }
		local _k_1, _v_1 = next(_binding, _k)
		local b = { _k_1, _v_1 }
		local _k_2, _v_2 = next(_binding, _k_1)
		local c = { _k_2, _v_2 }
		local _arg0 = a[1]
		expect(expected[_arg0]).to.equal(a[2])
		local _arg0_1 = a[1]
		-- ▼ Map.delete ▼
		local _valueExisted = expected[_arg0_1] ~= nil
		expected[_arg0_1] = nil
		-- ▲ Map.delete ▲
		expect(_valueExisted).to.equal(true)
		local _arg0_2 = b[1]
		expect(expected[_arg0_2]).to.equal(b[2])
		local _arg0_3 = b[1]
		-- ▼ Map.delete ▼
		local _valueExisted_1 = expected[_arg0_3] ~= nil
		expected[_arg0_3] = nil
		-- ▲ Map.delete ▲
		expect(_valueExisted_1).to.equal(true)
		local _arg0_4 = c[1]
		expect(expected[_arg0_4]).to.equal(c[2])
		local _arg0_5 = c[1]
		-- ▼ Map.delete ▼
		local _valueExisted_2 = expected[_arg0_5] ~= nil
		expected[_arg0_5] = nil
		-- ▲ Map.delete ▲
		expect(_valueExisted_2).to.equal(true)
	end)
	it("should properly destruct with element access", function()
		local array = { 1, 2 }
		array[1], array[2] = array[2], array[1]
		expect(array[1]).to.equal(2)
		expect(array[2]).to.equal(1)
	end)
	it("should properly destruct with var element access", function()
		local array = { 1, 2 }
		local a = 0
		local b = 1
		local _index = a + 1
		local _index_1 = b + 1
		array[_index], array[_index_1] = array[b + 1], array[a + 1]
		expect(array[a + 1]).to.equal(2)
		expect(array[b + 1]).to.equal(1)
	end)
	it("should support initializers in object destructuring", function()
		local o = {
			a = 1,
			b = 2,
			c = 3,
		}
		local function f(x)
			local _binding = o
			local a = _binding[x]
			if a == nil then
				a = 0
			end
			return a
		end
		local function g(x)
			local _binding = o
			local b = _binding.x
			if b == nil then
				b = 2
			end
			return b
		end
		expect(f("a")).to.equal(1)
		expect(f("b")).to.equal(2)
		expect(f("c")).to.equal(3)
		expect(f("d")).to.equal(0)
		expect(g("a")).to.equal(2)
		expect(g("d")).to.equal(2)
	end)
	it("should support arrays with object destructure", function()
		local array = { 3, 4 }
		local a = 0
		local b = 1
		local _binding = array
		array[b + 1] = _binding[a + 1]
		array[a + 1] = _binding[b + 1]
		expect(array[a + 1]).to.equal(3)
		expect(array[b + 1]).to.equal(3)
	end)
	it("should support object destructuring with context", function()
		local y = 0
		local _binding = {
			x = nil,
		}
		local x = _binding.x
		if x == nil then
			local _original = y
			y += 1
			x = _original
		end
		expect(x).to.equal(0)
		expect(y).to.equal(1)
	end)
	it("should support object destructing from object with shorthand syntax", function()
		local x = 123
		local _binding = {
			x = x,
		}
		local y = _binding.x
		expect(y).to.equal(123)
	end)
	it("should support object destructing from object with numeric literal key", function()
		local x = 456
		local _binding = {
			[1] = x,
		}
		local y = _binding[1]
		expect(y).to.equal(456)
	end)
	it("should support object destructing from object with string literal key", function()
		local x = 789
		-- prettier-ignore
		local _binding = {
			str = x,
		}
		local y = _binding.str
		expect(y).to.equal(789)
	end)
	it("should support destructuring from strings", function()
		local str = "xyz"
		local _matcher = string.gmatch(str, utf8.charpattern)
		local a = _matcher()
		local b = _matcher()
		local c = _matcher()
		expect(a).to.equal("x")
		expect(b).to.equal("y")
		expect(c).to.equal("z")
	end)
	it("should support destructuring from string literals", function()
		local _binding = "xyz"
		local _matcher = string.gmatch(_binding, utf8.charpattern)
		local a = _matcher()
		local b = _matcher()
		local c = _matcher()
		expect(a).to.equal("x")
		expect(b).to.equal("y")
		expect(c).to.equal("z")
	end)
	it("should support object assignment destructuring", function()
		local a = 0
		local b = 0
		local c = 0
		local _binding = {
			a = 4,
			b = 5,
			c = 6,
		}
		a = _binding.a
		b = _binding.b
		c = _binding.c
		expect(a).to.equal(4)
		expect(b).to.equal(5)
		expect(c).to.equal(6)
	end)
	it("should support object assignment destructuring with aliases", function()
		local a = 0
		local b = 0
		local c = 0
		local _binding = {
			x = 4,
			y = 5,
			z = 6,
		}
		a = _binding.x
		b = _binding.y
		c = _binding.z
		expect(a).to.equal(4)
		expect(b).to.equal(5)
		expect(c).to.equal(6)
	end)
	it("should support nested object assignment destructuring", function()
		local obj = {
			a = {
				b = {
					c = {
						d = "NEST!",
					},
				},
			},
		}
		local d = ""
		local _binding = obj
		local _binding_1 = _binding.a
		local _binding_2 = _binding_1.b
		local _binding_3 = _binding_2.c
		d = _binding_3.d
		expect(d).to.equal("NEST!")
	end)
	it("should support nested object assignment destructuring with alias", function()
		local obj = {
			a = {
				b = {
					c = {
						d = "NEST!",
					},
				},
			},
		}
		local str = ""
		local _binding = obj
		local _binding_1 = _binding.a
		local _binding_2 = _binding_1.b
		local _binding_3 = _binding_2.c
		str = _binding_3.d
		expect(str).to.equal("NEST!")
	end)
	it("should support array binding pattern with initializer", function()
		local _binding = {}
		local x = _binding[1]
		if x == nil then
			x = 5
		end
		expect(x).to.equal(5)
	end)
	it("should support array binding literal with omitted expression", function()
		local a = 0
		local b = 0
		a, _, b = 1, 2, 3
		expect(a).to.equal(1)
		expect(b).to.equal(3)
	end)
	it("should support array binding literal with initializer", function()
		local a = 0
		local b = 0
		local _binding = {}
		a = _binding[1]
		if a == nil then
			a = 5
		end
		b = _binding[2]
		if b == nil then
			b = 6
		end
		expect(a).to.equal(5)
		expect(b).to.equal(6)
	end)
	it("should support object binding literal with initializer", function()
		local a = 0
		local b = 0
		local _binding = {}
		a = _binding.a
		if a == nil then
			a = 5
		end
		b = _binding.b
		if b == nil then
			b = 6
		end
		expect(a).to.equal(5)
		expect(b).to.equal(6)
	end)
	it("should support object binding literal with alias and initializer", function()
		local y = 0
		local _binding = {}
		y = _binding.x
		if y == nil then
			y = 5
		end
		expect(y).to.equal(5)
	end)
	it("should destructure assign with nested sets", function()
		local a = ""
		local obj = {
			x = {
				["heck"] = true,
			},
		}
		local _binding = obj
		local _binding_1 = _binding.x
		local _value = next(_binding_1)
		a = _value
		expect(a).to.equal("heck")
	end)
	it("should destructure assign with nested maps", function()
		local a
		local obj = {
			x = {
				heck = 123,
			},
		}
		local _binding = obj
		local _binding_1 = _binding.x
		local _k, _v = next(_binding_1)
		a = { _k, _v }
		expect(a[1]).to.equal("heck")
		expect(a[2]).to.equal(123)
	end)
	it("should destructure assign with nested maps keys and values", function()
		local a
		local b
		local obj = {
			x = {
				heck = 123,
			},
		}
		local _binding = obj
		local _binding_1 = _binding.x
		local _k, _v = next(_binding_1)
		local _binding_2 = { _k, _v }
		a = _binding_2[1]
		b = _binding_2[2]
		expect(a).to.equal("heck")
		expect(b).to.equal(123)
	end)
	it("should destructure assign with double nested maps keys and values", function()
		local a
		local b
		local obj = {
			x = { {
				heck = 123,
			} },
		}
		local _binding = obj
		local _binding_1 = _binding.x
		local _binding_2 = _binding_1[1]
		local _k, _v = next(_binding_2)
		local _binding_3 = { _k, _v }
		a = _binding_3[1]
		b = _binding_3[2]
		expect(a).to.equal("heck")
		expect(b).to.equal(123)
	end)
	it("should destructure assign with double nested sets", function()
		local a
		local obj = {
			x = { {
				["heck"] = true,
			} },
		}
		local _binding = obj
		local _binding_1 = _binding.x
		local _binding_2 = _binding_1[1]
		local _value = next(_binding_2)
		a = _value
		expect(a).to.equal("heck")
	end)
	it("should destructure assign with triple nested sets", function()
		local a
		local obj = {
			x = { {
				["heck"] = true,
			} },
		}
		local _binding = obj
		local _binding_1 = _binding.x
		local _binding_2 = _binding_1[1]
		local _value = next(_binding_2)
		local _binding_3 = _value
		local _matcher = string.gmatch(_binding_3, utf8.charpattern)
		a = _matcher()
		expect(a).to.equal("h")
	end)
	it("should destructure nested generators", function()
		local function foo()
			return TS.generator(function()
				coroutine.yield(1)
				coroutine.yield(2)
				coroutine.yield(3)
			end)
		end
		local obj = {
			x = foo(),
		}
		local a = 0
		local b = 0
		local c = 0
		local _binding = obj
		local _binding_1 = _binding.x
		a = _binding_1.next().value
		b = _binding_1.next().value
		c = _binding_1.next().value
		expect(a).to.equal(1)
		expect(b).to.equal(2)
		expect(c).to.equal(3)
	end)
	it("should destructure double nested generators", function()
		local function foo()
			return TS.generator(function()
				coroutine.yield(1)
				coroutine.yield(2)
				coroutine.yield(3)
			end)
		end
		local obj = {
			x = { foo() },
		}
		local a = 0
		local b = 0
		local c = 0
		local _binding = obj
		local _binding_1 = _binding.x
		local _binding_2 = _binding_1[1]
		a = _binding_2.next().value
		b = _binding_2.next().value
		c = _binding_2.next().value
		expect(a).to.equal(1)
		expect(b).to.equal(2)
		expect(c).to.equal(3)
	end)
	it("should destructure nested strings", function()
		local obj = {
			x = "abc",
		}
		local a = ""
		local b = ""
		local c = ""
		local _binding = obj
		local _binding_1 = _binding.x
		local _matcher = string.gmatch(_binding_1, utf8.charpattern)
		a = _matcher()
		b = _matcher()
		c = _matcher()
		expect(a).to.equal("a")
		expect(b).to.equal("b")
		expect(c).to.equal("c")
	end)
	it("should destructure nested strings 2", function()
		local obj = {
			x = {
				foo = 1,
			},
		}
		local a = ""
		local _binding = obj
		local _binding_1 = _binding.x
		local _k, _v = next(_binding_1)
		local _binding_2 = { _k, _v }
		local _binding_3 = _binding_2[1]
		local _matcher = string.gmatch(_binding_3, utf8.charpattern)
		local _binding_4 = _matcher()
		local _matcher_1 = string.gmatch(_binding_4, utf8.charpattern)
		local _binding_5 = _matcher_1()
		local _matcher_2 = string.gmatch(_binding_5, utf8.charpattern)
		a = _matcher_2()
		expect(a).to.equal("f")
	end)
	it("should get sub type of iterable iterator", function()
		local function foo()
			return TS.generator(function()
				coroutine.yield("abc")
			end)
		end
		local obj = {
			x = foo(),
		}
		local a = ""
		local b = ""
		local c = ""
		local _binding = obj
		local _binding_1 = _binding.x
		local _binding_2 = _binding_1.next().value
		local _matcher = string.gmatch(_binding_2, utf8.charpattern)
		a = _matcher()
		b = _matcher()
		c = _matcher()
		expect(a).to.equal("a")
		expect(b).to.equal("b")
		expect(c).to.equal("c")
	end)
	it("should support empty destructure", function()
		local x = 0
		pcall(function()
			x = 123
			return x
		end)
		expect(x).to.equal(123)
	end)
	it("should support empty destructure assignment", function()
		local x = 0
		pcall(function()
			x = 123
			return x
		end)
		expect(x).to.equal(123)
	end)
	it("should support function destructuring if not a method", function()
		local x = 0
		local a = {
			b = function(param)
				x = param
			end,
		}
		local b = a.b
		b(123)
		expect(x).to.equal(123)
	end)
end
