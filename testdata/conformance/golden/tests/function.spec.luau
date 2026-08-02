-- Compiled with roblox-ts v3.0.0
local TS = require(script.Parent.Parent.include.RuntimeLib)
local N = {}
do
	local _container = N
	local a = function(self, n)
		return n
	end
	_container.a = a
	local b = function(n)
		return n
	end
	_container.b = b
	local c = function(n)
		return n
	end
	_container.c = c
	local d = function(n)
		return n
	end
	_container.d = d
	local function e(self, n)
		return n
	end
	_container.e = e
	local function f(n)
		return n
	end
	_container.f = f
	local function g(n)
		return n
	end
	_container.g = g
end
return function()
	it("should support function declarations", function()
		local function foo()
			return true
		end
		expect(foo()).to.be.ok()
	end)
	it("should support function arguments", function()
		local function add(a, b)
			return a + b
		end
		expect(add(123, 456)).to.equal(579)
	end)
	it("should support no return value", function()
		local function doStuff(doIHaveToMom)
			if doIHaveToMom then
				return "cleans room"
			else
				return nil
			end
		end
		expect(doStuff(true)).to.equal("cleans room")
		expect(doStuff(false)).never.to.be.ok()
	end)
	it("should support destructuring in assignment", function()
		local function addAndMultiply(a, b)
			local sum = a + b
			local product = a * b
			return { sum, product }
		end
		local _binding = addAndMultiply(5, 6)
		local x = _binding[1]
		local y = _binding[2]
		expect(x).to.equal(11)
		expect(y).to.equal(30)
	end)
	it("should support variadic arguments", function()
		local function addAll(...)
			local sum = 0
			for _i = 1, select("#", ...) do
				local value = (select(_i, ...))
				sum += value
			end
			return sum
		end
		expect(addAll()).to.equal(0)
		expect(addAll(1)).to.equal(1)
		expect(addAll(1, 2, 3)).to.equal(6)
	end)
	it("should support default arguments", function()
		local function addSeven(n)
			if n == nil then
				n = 5
			end
			return n + 7
		end
		expect(addSeven()).to.equal(12)
		expect(addSeven(7)).to.equal(14)
	end)
	it("should support function expressions", function()
		expect((function()
			return 123
		end)()).to.equal(123)
	end)
	it("should support arrow functions", function()
		expect((function()
			return 456
		end)()).to.equal(456)
	end)
	it("should support binding patterns", function()
		local function foo(_param)
			local a = _param.a
			return a * 2
		end
		expect(foo({
			a = 4,
		})).to.equal(8)
		local function bar(_param)
			local a = _param[1]
			local b = _param[2]
			return a * b
		end
		expect(bar({ 4, 7 })).to.equal(28)
	end)
	it("should support this parameter", function()
		local function foo(x, y, z)
			return `{x}, {y}, {z}`
		end
		expect(foo(1, 2, 3)).to.equal("1, 2, 3")
	end)
	it("should properly define and call methods vs callbacks", function()
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
				self.e = function(n)
					expect(n).to.equal(5)
				end
				self.f = function(self, n)
					expect(TS.instanceof(self, A)).to.equal(true)
					expect(n).to.equal(5)
				end
				self.g = function(n)
					expect(n).to.equal(5)
				end
			end
			function A.a(n)
				expect(self).to.equal(nil)
				expect(n).to.equal(5)
			end
			function A:b(n)
				expect(self).to.equal(A)
				expect(n).to.equal(5)
			end
			function A:c(n)
				expect(n).to.equal(5)
			end
			function A.d(n)
				expect(self).to.equal(nil)
				expect(n).to.equal(5)
			end
			function A:h(n)
				expect(TS.instanceof(self, A)).to.equal(true)
				expect(n).to.equal(5)
			end
		end
		local o
		o = {
			a = function(n)
				expect(self).to.equal(nil)
				expect(n).to.equal(5)
			end,
			b = function(self, n)
				expect(self).to.equal(o)
				expect(n).to.equal(5)
			end,
			c = function(n)
				expect(self).to.equal(nil)
				expect(n).to.equal(5)
			end,
			d = function(self, n)
				expect(self).to.equal(o)
				expect(n).to.equal(5)
			end,
			e = function(n)
				expect(n).to.equal(5)
			end,
			f = function(self, n)
				expect(self).to.equal(o)
				expect(n).to.equal(5)
			end,
			g = function(self, n)
				expect(self).to.equal(o)
				expect(n).to.equal(5)
			end,
		}
		local function f(n)
			-- expect(this).to.equal(undefined);
			expect(n).to.equal(5)
		end
		local g = function(n)
			-- expect(this).to.equal(undefined);
			expect(n).to.equal(5)
		end
		A.a(5)
		A:b(5)
		A:c(5)
		local a = A.new()
		a.d(5)
		a.e(5)
		a:f(5)
		a.g(5)
		a:h(5)
		o.a(5)
		o:b(5)
		o.c(5)
		o:d(5)
		o.e(5)
		o:f(5)
		o:g(5)
		expect(N:a(5)).to.equal(5)
		expect(N.b(5)).to.equal(5)
		expect(N.c(5)).to.equal(5)
		expect(N.d(5)).to.equal(5)
		expect(N:e(5)).to.equal(5)
		expect(N.f(5)).to.equal(5)
		expect(N.g(5)).to.equal(5)
		f(5)
		g(5);
		(function(n)
			expect(n).to.equal(5)
		end)(5);
		({
			x = function(self, n)
				expect(n).to.equal(5)
			end,
		}):x(5);
		({
			x = function(n)
				expect(n).to.equal(5)
			end,
		}).x(5);
		({
			x = function(self, n)
				expect(n).to.equal(5)
			end,
		}):x(5);
		(function(n)
			expect(n).to.equal(5)
		end)(5)
		local obj = {
			saferNum = function(n)
				return if n == n and n > 0 then n else 0
			end,
		}
		local saferNum = obj.saferNum
		expect(saferNum(5)).to.equal(5)
	end)
	it("should optimize array spread paramters", function()
		local function foo(a, b, c)
			return { a, b, c }
		end
		local result = foo(123, "abc", true)
		expect(result[1]).to.equal(123)
		expect(result[2]).to.equal("abc")
		expect(result[3]).to.equal(true)
	end)
	it("should protect against void values passed into built-in functions", function()
		local function foo()
		end
		expect(tonumber((foo()))).to.equal(nil)
	end)
	it("should not wrap argument spread expressions in parentheses", function()
		local args = { "123", "456" }
		expect((select(2, select(1, unpack(args))))).to.equal("456")
	end)
	it("should support spreading into a macro with optional arguments", function()
		local array = { 1, 2, 3, 4, 5 }
		local args = { function(value, accum)
			return value + accum
		end, 10 }
		local _spread0, _spread1 = unpack(args)
		-- ▼ ReadonlyArray.reduce ▼
		local _result = _spread1
		local _callback = _spread0
		for _i = 1, #array do
			_result = _callback(_result, array[_i], _i - 1, array)
		end
		-- ▲ ReadonlyArray.reduce ▲
		local sum = _result
		expect(sum).to.equal(25)
	end)
end
