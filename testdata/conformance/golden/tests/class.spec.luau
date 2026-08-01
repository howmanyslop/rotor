-- Compiled with roblox-ts v3.0.0
local TS = require(script.Parent.Parent.include.RuntimeLib)
return function()
	it("should properly initialize static properties and use `this` in a static context correctly", function()
		local X
		do
			X = setmetatable({}, {
				__tostring = function()
					return "X"
				end,
			})
			X.__index = X
			function X.new(...)
				local self = setmetatable({}, X)
				return self:constructor(...) or self
			end
			function X:constructor()
			end
			X.value1 = "a"
			do
				X.value1 ..= "b"
			end
			X.value2 = X.value1 .. "c"
		end
		expect(X.value1).to.equal("ab")
		expect(X.value2).to.equal("abc")
	end)
	it("should create a class with a constructor", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor(bar)
				self.bar = bar
			end
		end
		local foo = Foo.new("baz!")
		expect(foo.bar).to.equal("baz!")
	end)
	it("should construct with default parameters and accessors", function()
		local Vector
		do
			Vector = setmetatable({}, {
				__tostring = function()
					return "Vector"
				end,
			})
			Vector.__index = Vector
			function Vector.new(...)
				local self = setmetatable({}, Vector)
				return self:constructor(...) or self
			end
			function Vector:constructor(x, y, z)
				if x == nil then
					x = 0
				end
				if y == nil then
					y = 0
				end
				if z == nil then
					z = 0
				end
				self.x = x
				self.y = y
				self.z = z
			end
		end
		local v0 = Vector.new(1, 2, 3)
		expect(v0.x).to.equal(1)
		expect(v0.y).to.equal(2)
		expect(v0.z).to.equal(3)
		local v1 = Vector.new()
		expect(v1.x).to.equal(0)
		expect(v1.y).to.equal(0)
		expect(v1.z).to.equal(0)
	end)
	it("should initialize properties after super call", function()
		local order = {}
		local Super
		do
			Super = setmetatable({}, {
				__tostring = function()
					return "Super"
				end,
			})
			Super.__index = Super
			function Super.new(...)
				local self = setmetatable({}, Super)
				return self:constructor(...) or self
			end
			function Super:constructor()
				table.insert(order, 1)
			end
		end
		local Class
		do
			local super = Super
			Class = setmetatable({}, {
				__tostring = function()
					return "Class"
				end,
				__index = super,
			})
			Class.__index = Class
			function Class.new(...)
				local self = setmetatable({}, Class)
				return self:constructor(...) or self
			end
			function Class:constructor()
				table.insert(order, 0)
				super.constructor(self)
				table.insert(order, 2)
				self.parameter = #order
				table.insert(order, 3)
			end
		end
		Class.new()
		for i = 0, #order - 1 do
			expect(order[i + 1]).to.equal(i)
		end
	end)
	it("should expose a public method", function()
		local Greeter
		do
			Greeter = setmetatable({}, {
				__tostring = function()
					return "Greeter"
				end,
			})
			Greeter.__index = Greeter
			function Greeter.new(...)
				local self = setmetatable({}, Greeter)
				return self:constructor(...) or self
			end
			function Greeter:constructor(message)
				self.greeting = message
			end
			function Greeter:greet()
				return "Hello, " .. self.greeting
			end
			Greeter.f = TS.async(function(self) end)
		end
		local artemis = Greeter.new("artemis")
		expect(artemis.greeting).to.equal("artemis")
		expect(artemis:greet()).to.equal("Hello, artemis")
	end)
	it("should inhereit methods", function()
		local Animal
		do
			Animal = setmetatable({}, {
				__tostring = function()
					return "Animal"
				end,
			})
			Animal.__index = Animal
			function Animal.new(...)
				local self = setmetatable({}, Animal)
				return self:constructor(...) or self
			end
			function Animal:constructor()
			end
			function Animal:move(distanceInMeters)
				if distanceInMeters == nil then
					distanceInMeters = 0
				end
				return `Animal moved {distanceInMeters}m.`
			end
		end
		local Dog
		do
			local super = Animal
			Dog = setmetatable({}, {
				__tostring = function()
					return "Dog"
				end,
				__index = super,
			})
			Dog.__index = Dog
			function Dog.new(...)
				local self = setmetatable({}, Dog)
				return self:constructor(...) or self
			end
			function Dog:constructor(name)
				super.constructor(self)
				self.name = name
			end
			function Dog:bark()
				return self.name .. " barks"
			end
		end
		local apollo = Dog.new("apollo")
		expect(apollo:move()).to.equal("Animal moved 0m.")
		expect(apollo:move(5)).to.equal("Animal moved 5m.")
		expect(apollo:bark()).to.equal("apollo barks")
	end)
	it("should support static methods", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			function Foo:bar()
				return "bar"
			end
			Foo.barf = TS.async(function(self)
				return "barf"
			end)
		end
		expect(Foo:bar()).to.equal("bar")
	end)
	it("should inherit static methods", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			function Foo:bar()
				return "bar"
			end
		end
		local Bar
		do
			local super = Foo
			Bar = setmetatable({}, {
				__tostring = function()
					return "Bar"
				end,
				__index = super,
			})
			Bar.__index = Bar
			function Bar.new(...)
				local self = setmetatable({}, Bar)
				return self:constructor(...) or self
			end
			function Bar:constructor(...)
				super.constructor(self, ...)
			end
		end
		expect(Bar:bar()).to.equal("bar")
	end)
	it("should support parameter initializers", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
				self.bar = "baz"
			end
		end
		local foo = Foo.new()
		expect(foo.bar).to.equal("baz")
	end)
	it("should support toString", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			function Foo:toString()
				return "Foo"
			end
			function Foo:__tostring()
				return self:toString()
			end
		end
		expect(tostring(Foo.new())).to.equal("Foo")
	end)
	it("should support toString inheritance", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			function Foo:toString()
				return "Foo"
			end
			function Foo:__tostring()
				return self:toString()
			end
		end
		local Bar
		do
			local super = Foo
			Bar = setmetatable({}, {
				__tostring = function()
					return "Bar"
				end,
				__index = super,
			})
			Bar.__index = Bar
			function Bar.new(...)
				local self = setmetatable({}, Bar)
				return self:constructor(...) or self
			end
			function Bar:constructor(...)
				super.constructor(self, ...)
			end
			function Bar:__tostring()
				return self:toString()
			end
		end
		expect(tostring(Bar.new())).to.equal("Foo")
	end)
	it("should support toString inheritance with ClassExpression", function()
		local A
		do
			local _class
			do
				local B = setmetatable({}, {
					__tostring = function()
						return "B"
					end,
				})
				B.__index = B
				function B.new(...)
					local self = setmetatable({}, B)
					return self:constructor(...) or self
				end
				function B:constructor()
				end
				function B:toString()
					return "B"
				end
				function B:__tostring()
					return self:toString()
				end
				_class = B
			end
			local super = _class
			A = setmetatable({}, {
				__tostring = function()
					return "A"
				end,
				__index = super,
			})
			A.__index = A
			function A.new(...)
				local self = setmetatable({}, A)
				return self:constructor(...) or self
			end
			function A:constructor(...)
				super.constructor(self, ...)
			end
			function A:__tostring()
				return self:toString()
			end
		end
		expect(tostring(A.new())).to.equal("B")
	end)
	it("should support multiple constructors", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor(name)
				if name ~= "" and name then
					self.name = name
				end
			end
		end
		local foo1 = Foo.new()
		expect(foo1.name).never.to.be.ok()
		local foo2 = Foo.new("bar")
		expect(foo2.name).to.equal("bar")
	end)
	it("should support constructor parameter destructuring", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor(_param)
				local a = _param.a
				self.bar = a
			end
		end
		expect(Foo.new({
			a = 123,
		}).bar).to.equal(123)
	end)
	it("should support passing the rest parameter from constructor to super", function()
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
			function A:constructor(...)
				expect((...)).to.equal(10)
				expect((select(2, ...))).to.equal(20)
				expect((select(3, ...))).to.equal(30)
			end
		end
		local B
		do
			local super = A
			B = setmetatable({}, {
				__tostring = function()
					return "B"
				end,
				__index = super,
			})
			B.__index = B
			function B.new(...)
				local self = setmetatable({}, B)
				return self:constructor(...) or self
			end
			function B:constructor(...)
				super.constructor(self, ...)
			end
		end
		expect(function()
			return B.new(10, 20, 30)
		end).never:throw()
	end)
	it("should support class expressions", function()
		local _class
		do
			_class = setmetatable({}, {
				__tostring = function()
					return "Anonymous"
				end,
			})
			_class.__index = _class
			function _class.new(...)
				local self = setmetatable({}, _class)
				return self:constructor(...) or self
			end
			function _class:constructor()
			end
			function _class:bar()
				return "A"
			end
		end
		local Foo = _class
		local _class_1
		do
			local super = Foo
			_class_1 = setmetatable({}, {
				__tostring = function()
					return "Anonymous"
				end,
				__index = super,
			})
			_class_1.__index = _class_1
			function _class_1.new(...)
				local self = setmetatable({}, _class_1)
				return self:constructor(...) or self
			end
			function _class_1:constructor(...)
				super.constructor(self, ...)
			end
		end
		local Bar = _class_1
		local bar = Bar.new()
		expect(bar:bar()).to.equal("A")
	end)
	it("should support built-in classes", function()
		expect(TS.Promise.new(function() end)).to.be.ok()
	end)
	it("should support numeric members", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
				self[1] = "bar"
			end
		end
		expect(Foo.new()[1]).to.equal("bar")
	end)
	it("should support computed members", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
				self.bar = "baz"
			end
		end
		expect(Foo.new().bar).to.equal("baz")
	end)
	it("should support numeric statics", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			Foo[1] = "bar"
		end
		expect(Foo[1]).to.equal("bar")
	end)
	it("should support computed statics", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			Foo.bar = "baz"
		end
		expect(Foo.bar).to.equal("baz")
	end)
	it("should support new expressions without parentheses", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
				self.bar = 1
			end
		end
		-- prettier-ignore
		local foo = Foo.new()
		expect(foo).to.be.ok()
		expect(TS.instanceof(foo, Foo)).to.equal(true)
		expect(foo.bar).to.equal(1)
	end)
	it("should compile static fields last", function()
		local Foo
		do
			Foo = setmetatable({}, {
				__tostring = function()
					return "Foo"
				end,
			})
			Foo.__index = Foo
			function Foo.new(...)
				local self = setmetatable({}, Foo)
				return self:constructor(...) or self
			end
			function Foo:constructor()
			end
			function Foo:bar()
				return "baz"
			end
			Foo.x = Foo.new():bar()
		end
		expect(Foo.x).to.equal("baz")
	end)
	it("should support invalid indexing", function()
		local _class
		do
			local A = setmetatable({}, {
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
				self["$v"] = 2
			end
			A["$"] = function(self)
				self["$v"] += 1
				return self["$v"]
			end
			_class = A
		end
		local _ = _class.new()
		expect(_["$"](_)).to.equal(3)
		local _class_1
		do
			_class_1 = setmetatable({}, {
				__tostring = function()
					return "Anonymous"
				end,
			})
			_class_1.__index = _class_1
			function _class_1.new(...)
				local self = setmetatable({}, _class_1)
				return self:constructor(...) or self
			end
			function _class_1:constructor()
			end
			_class_1["$"] = function(self)
				self["$v"] += 1
				return self["$v"]
			end
			_class_1["$v"] = 2
		end
		expect(_class_1["$"](_class_1)).to.equal(3)
		local _class_2
		do
			local _class_3
			do
				_class_3 = setmetatable({}, {
					__tostring = function()
						return "Anonymous"
					end,
				})
				_class_3.__index = _class_3
				function _class_3.new(...)
					local self = setmetatable({}, _class_3)
					return self:constructor(...) or self
				end
				function _class_3:constructor()
					self["$v"] = -1
				end
				_class_3["$"] = function(self)
					self["$v"] += 1
					return self["$v"]
				end
			end
			local super = _class_3
			_class_2 = setmetatable({}, {
				__tostring = function()
					return "Anonymous"
				end,
				__index = super,
			})
			_class_2.__index = _class_2
			function _class_2.new(...)
				local self = setmetatable({}, _class_2)
				return self:constructor(...) or self
			end
			function _class_2:constructor(...)
				super.constructor(self, ...)
			end
			_class_2["$"] = function(self)
				return super["$"](self)
			end
		end
		local _1 = _class_2.new()
		expect(_1["$"](_1)).to.equal(0)
		local _class_3
		do
			local _class_4
			do
				_class_4 = setmetatable({}, {
					__tostring = function()
						return "Anonymous"
					end,
				})
				_class_4.__index = _class_4
				function _class_4.new(...)
					local self = setmetatable({}, _class_4)
					return self:constructor(...) or self
				end
				function _class_4:constructor()
				end
				_class_4["$"] = function(self)
					self["$v"] += 1
					return self["$v"]
				end
				_class_4["$v"] = -1
			end
			local super = _class_4
			_class_3 = setmetatable({}, {
				__tostring = function()
					return "Anonymous"
				end,
				__index = super,
			})
			_class_3.__index = _class_3
			function _class_3.new(...)
				local self = setmetatable({}, _class_3)
				return self:constructor(...) or self
			end
			function _class_3:constructor(...)
				super.constructor(self, ...)
			end
			_class_3["$"] = function(self)
				return super["$"](self)
			end
		end
		expect(_class_3["$"](_class_3)).to.equal(0)
	end)
	it("should support nested classes which refer to the outer class", function()
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
			local _class
			do
				local B = setmetatable({}, {
					__tostring = function()
						return "B"
					end,
				})
				B.__index = B
				function B.new(...)
					local self = setmetatable({}, B)
					return self:constructor(...) or self
				end
				function B:constructor()
				end
				function B:method()
					return A.new()
				end
				_class = B
			end
			A.member = _class
		end
		expect(TS.instanceof(A.member.new():method(), A)).to.equal(true)
	end)
	it("should support methods keys that emit prereqs", function()
		local i = 0
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
			i += 1
			A[i] = function(self)
				return "first"
			end
			i += 1
			A[i] = function(self)
				return "second"
			end
		end
		local a = A.new()
		expect(a[1](a)).to.equal("first")
		expect(a[2](a)).to.equal("second")
	end)
end
