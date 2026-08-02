-- Compiled with roblox-ts v3.0.0
local TS = require(script.Parent.Parent.include.RuntimeLib)
local N = {}
do
	local _container = N
	_container.x = 5
	local p = {
		a = {
			x = _container.x,
		},
	}
	_container.p = p
end
return function()
	it("should support object literal brackets", function()
		-- prettier-ignore 
		local obj = {
			test = 1,
			["2"] = 2,
			[1] = 3,
		}
		expect(obj.test).to.equal(1)
		expect(obj["2"]).to.equal(2)
		expect(obj[1]).to.equal(3)
	end)
	it("should support numeric indexing", function()
		local obj = {
			[2] = 1,
		}
		local i = 2
		local a = obj[i]
		local b = obj[2]
		local c = obj[i]
		local d = obj[2]
		expect(a).to.equal(1)
		expect(b).to.equal(1)
		expect(c).to.equal(1)
		expect(d).to.equal(1)
		a = obj[i]
		b = obj[2]
		local _binding = obj
		c = _binding[i]
		local _binding_1 = obj
		d = _binding_1[2]
		expect(a).to.equal(1)
		expect(b).to.equal(1)
		expect(c).to.equal(1)
		expect(d).to.equal(1)
		local j = 0
		local function f()
			j += 1
			return 2
		end
		local o = {
			[0] = 2,
			[1] = 4,
			[2] = 5,
			[3] = 6,
			[4] = 5,
		}
		o[f()] += 1
		local _index = f()
		local _original = o[_index]
		o[_index] += 1
		local x = _original
		expect(x).to.equal(6)
		expect(o[2]).to.equal(7)
		expect(j).to.equal(2)
	end)
	it("should support bracket index definitions", function()
		local a = {
			[1] = "foo",
			[2] = "bar",
		}
		-- prettier-ignore
		local b = {
			[1] = "baz",
			[2] = "boo",
		}
		expect(a[1]).to.equal("foo")
		expect(a[2]).to.equal("bar")
		expect(b[1]).to.equal("baz")
		expect(b[2]).to.equal("boo")
	end)
	it("should support object methods", function()
		local foo = {
			baz = "baz",
			bar = function(self)
				return "baz"
			end,
		}
		expect(foo:bar()).to.equal(foo.baz)
		local i = 0
		i += 1
		local _object = {
			count = 1,
			[i] = TS.async(function(self) end),
		}
		i += 1
		local _left = i
		_object[_left] = function()
			return 1
		end
		i += 1
		_object[`{i}`] = TS.async(function(self) end)
		i += 1
		_object[i] = TS.async(function(self) end)
		i += 1
		local _left_1 = i
		_object[_left_1] = function()
			return 1
		end
		i += 1
		_object[`{i}`] = TS.async(function(self) end)
		function _object:f()
		end
		_object.g = TS.async(function(self) end)
		local qq = _object:f()
	end)
	it("should support object spread", function()
		local foo = {
			a = 1,
			b = 2,
			c = 3,
		}
		local _object = table.clone(foo)
		setmetatable(_object, nil)
		_object.d = 4
		_object.e = 5
		_object.f = 6
		local bar = _object
		expect(bar.a).to.equal(1)
		expect(bar.b).to.equal(2)
		expect(bar.c).to.equal(3)
		expect(bar.d).to.equal(4)
		expect(bar.e).to.equal(5)
		expect(bar.f).to.equal(6)
	end)
	it("should support spread with non-objects", function()
		local foo1 = {
			a = 1,
			b = 2,
			c = 3,
		}
		local foo2 = nil
		local foo3 = ""
		local foo4 = 0
		local _object = {}
		if foo1 then
			for _k, _v in foo1 do
				_object[_k] = _v
			end
		end
		if foo2 then
			for _k, _v in foo2 do
				_object[_k] = _v
			end
		end
		if foo3 ~= "" and foo3 then
			for _k, _v in foo3 do
				_object[_k] = _v
			end
		end
		if foo4 ~= 0 and foo4 == foo4 and foo4 then
			for _k, _v in foo4 do
				_object[_k] = _v
			end
		end
		_object.d = 4
		_object.e = 5
		_object.f = 6
		local bar = _object
		expect(bar.a).to.equal(1)
		expect(bar.b).to.equal(2)
		expect(bar.c).to.equal(3)
		expect(bar.d).to.equal(4)
		expect(bar.e).to.equal(5)
		expect(bar.f).to.equal(6)
	end)
	it("should overwrite with object spread", function()
		local foo = {
			a = 1,
			b = 2,
			c = 3,
		}
		local bar = {
			a = 2,
			b = 5,
			d = 2,
		}
		local _object = table.clone(foo)
		setmetatable(_object, nil)
		for _k, _v in bar do
			_object[_k] = _v
		end
		local obj0 = _object
		expect(obj0).never.to.equal(foo)
		expect(obj0).never.to.equal(bar)
		expect(obj0.a).to.equal(2)
		expect(obj0.b).to.equal(5)
		expect(obj0.c).to.equal(3)
		expect(obj0.d).to.equal(2)
		local _object_1 = table.clone(bar)
		setmetatable(_object_1, nil)
		for _k, _v in foo do
			_object_1[_k] = _v
		end
		local obj1 = _object_1
		expect(obj1).never.to.equal(foo)
		expect(obj1).never.to.equal(bar)
		expect(obj1.a).to.equal(1)
		expect(obj1.b).to.equal(2)
		expect(obj1.c).to.equal(3)
		expect(obj1.d).to.equal(2)
		do
			local k = {
				o = 1,
				b = 2,
			}
			local _object_2 = table.clone(k)
			setmetatable(_object_2, nil)
			_object_2.o = 3
			local _left = "b"
			local _original = k.o
			k.o += 1
			_object_2[_left] = _original
			local o = _object_2
			expect(o.o).to.equal(3)
			expect(o.b).to.equal(1)
		end
		do
			local k = {
				o = 1,
				b = 2,
			}
			local _object_2 = {
				o = 3,
			}
			for _k, _v in k do
				_object_2[_k] = _v
			end
			local _left = "b"
			local _original = k.o
			k.o += 1
			_object_2[_left] = _original
			local o = _object_2
			expect(o.o).to.equal(1)
			expect(o.b).to.equal(1)
		end
		do
			local k = {
				o = 1,
				b = 2,
			}
			local _object_2 = {
				o = 3,
			}
			local _left = "b"
			local _original = k.o
			k.o += 1
			_object_2[_left] = _original
			for _k, _v in k do
				_object_2[_k] = _v
			end
			local o = _object_2
			expect(o.o).to.equal(2)
			expect(o.b).to.equal(2)
		end
	end)
	it("should support numeric literals", function()
		local object1 = {
			[1] = 1,
			[2] = 1,
		}
		expect(object1[1]).to.equal(1)
		expect(object1[2]).to.equal(1)
		expect(({
			[0] = 1,
		})[0]).to.equal(1)
	end)
	it("should support computedMethodNames", function()
		do
			local o
			local function g(n)
				expect(self).to.equal(nil)
				return o:f(n)
			end
			local i = 0
			local _object = {
				g = g,
			}
			local _left = "id"
			i += 1
			_object[_left] = i
			_object.calls = 0
			i += 1
			_object[i] = function(self, n)
				return self:f(n)
			end
			function _object:f(n)
				self.calls += 1
				return self.calls
			end
			o = _object
			local b = {
				k = o,
			}
			local _k = b.k
			expect(_k[i](_k, 5)).to.equal(1)
			expect(o[i](o, 5)).to.equal(2)
			expect(o:f(5)).to.equal(3)
			expect(o.g(5)).to.equal(4)
			o.calls += 1
			expect(o.calls).to.equal(5)
		end
	end)
	it("should support invalid Lua identifier members", function()
		local i = 0
		local o = {
			["$"] = function(self)
				self["$v"] += 1
				return self["$v"]
			end,
			["$v"] = 1,
		}
		local k = {
			o = o,
		}
		local fo = function()
			i += 1
			return o
		end
		local fk = function()
			i += 1
			return k
		end
		expect(o["$"](o)).to.equal(2)
		local _original = o["$v"]
		o["$v"] += 1
		expect(_original).to.equal(2)
		o["$v"] += 1
		expect(o["$v"]).to.equal(4)
		local _original_1 = o["$v"]
		o["$v"] += 1
		local x = _original_1
		expect(x).to.equal(4)
		o["$v"] += 1
		local y = o["$v"]
		expect(y).to.equal(6)
		local _o = k.o
		expect(_o["$"](_o)).to.equal(7)
		local _exp = k.o
		local _original_2 = _exp["$v"]
		_exp["$v"] += 1
		expect(_original_2).to.equal(7)
		local _exp_1 = k.o
		_exp_1["$v"] += 1
		expect(_exp_1["$v"]).to.equal(9)
		local _exp_2 = k.o
		local _original_3 = _exp_2["$v"]
		_exp_2["$v"] += 1
		local a = _original_3
		expect(a).to.equal(9)
		local _exp_3 = k.o
		_exp_3["$v"] += 1
		local b = _exp_3["$v"]
		expect(b).to.equal(11)
		local _ = fo()
		expect(_["$"](_)).to.equal(12)
		local _exp_4 = fo()
		local _original_4 = _exp_4["$v"]
		_exp_4["$v"] += 1
		expect(_original_4).to.equal(12)
		local _exp_5 = fo()
		_exp_5["$v"] += 1
		expect(_exp_5["$v"]).to.equal(14)
		local _exp_6 = fo()
		local _original_5 = _exp_6["$v"]
		_exp_6["$v"] += 1
		local c = _original_5
		expect(c).to.equal(14)
		local _exp_7 = fo()
		_exp_7["$v"] += 1
		local d = _exp_7["$v"]
		expect(d).to.equal(16)
		expect(i).to.equal(5)
		local _o_1 = fk().o
		expect(_o_1["$"](_o_1)).to.equal(17)
		local _exp_8 = fk().o
		local _original_6 = _exp_8["$v"]
		_exp_8["$v"] += 1
		expect(_original_6).to.equal(17)
		local _exp_9 = fk().o
		_exp_9["$v"] += 1
		expect(_exp_9["$v"]).to.equal(19)
		local _exp_10 = fk().o
		local _original_7 = _exp_10["$v"]
		_exp_10["$v"] += 1
		local e = _original_7
		expect(e).to.equal(19)
		local _exp_11 = fk().o
		_exp_11["$v"] += 1
		local f = _exp_11["$v"]
		expect(f).to.equal(21)
		expect(i).to.equal(10)
	end)
	it("should support shorthand assignments", function()
		local _exp = N.p.a
		_exp.x += 1
		expect(_exp.x).to.equal(6)
	end)
	it("should support computed members", function()
		local a = 8
		local i = 0
		local _object = {}
		local _left = a
		i += 1
		local _exp = i
		a = 9
		_object[_left] = `{_exp}{a}`
		local b = _object
		local c = {
			[a] = 1,
		}
		expect(b[8]).to.equal(`19`)
		expect(c[9]).to.equal(1)
	end)
	it("should support template literal indexes without substitution", function()
		local obj = {
			data = 55,
			thing = "hello",
		}
		local a = obj[`data`]
		local b = obj[`thing`]
		expect(a).to.equal(55)
		expect(b).to.equal("hello")
	end)
	it("should support composing objects with methods and callbacks", function()
		local function add(self, n)
			self.n += n
			return self.n
		end
		local mul = function(self, n)
			self.n *= n
			return self
		end
		local function h(n)
			expect(n).to.equal(5)
		end
		local obj = {
			add = add,
			n = 10,
			mul = mul,
			h = h,
		}
		expect(obj:add(5)).to.equal(15)
		expect(obj:mul(3):add(6)).to.equal(51)
		obj.h(5)
	end)
	it("should support object member functions as implicitly being methods", function()
		-- don't ask me why, but for some reason non-method function members in objects are implicitly methods
		local o = {
			count = 1,
			getCount = function(self)
				return self.count
			end,
		}
		expect(o:getCount()).to.equal(1)
	end)
end
