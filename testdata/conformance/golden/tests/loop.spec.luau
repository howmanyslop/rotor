-- Compiled with roblox-ts v3.0.0
local function difference(set1, set2)
	local result = {}
	for value in set1 do
		if not (set2[value] ~= nil) then
			result[value] = true
		end
	end
	for value in set2 do
		if not (set1[value] ~= nil) then
			result[value] = true
		end
	end
	return result
end
return function()
	it("should support numeric for loops", function()
		local hit = {}
		local sum = 10
		for i = 0, 9 do
			local _i = i
			hit[_i] = true
			sum -= 1
		end
		expect(sum).to.equal(0)
		expect(hit[0] ~= nil).to.equal(true)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
		expect(hit[4] ~= nil).to.equal(true)
		expect(hit[5] ~= nil).to.equal(true)
		expect(hit[6] ~= nil).to.equal(true)
		expect(hit[7] ~= nil).to.equal(true)
		expect(hit[8] ~= nil).to.equal(true)
		expect(hit[9] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #1", function()
		local hit = {}
		local n = 0
		for i = 1, 3 do
			local _i = i
			hit[_i] = true
			n += 1
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #2", function()
		local hit = {}
		local n = 0
		for i = 3, 1, -1 do
			local _i = i
			hit[_i] = true
			n += 1
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #3", function()
		local hit = {}
		local n = 0
		do
			local i = 1
			local _shouldIncrement = false
			while true do
				if _shouldIncrement then
					i = i + 1
				else
					_shouldIncrement = true
				end
				if not (i <= 3) then
					break
				end
				local _i = i
				hit[_i] = true
				n += 1
			end
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #4", function()
		local hit = {}
		local n = 0
		do
			local i = 3
			local _shouldIncrement = false
			while true do
				if _shouldIncrement then
					i = i - 1
				else
					_shouldIncrement = true
				end
				if not (i >= 1) then
					break
				end
				local _i = i
				hit[_i] = true
				n += 1
			end
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #5", function()
		local hit = {}
		local n = 0
		for i = 1, 3 do
			local _i = i
			hit[_i] = true
			n += 1
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #6", function()
		local hit = {}
		local limit = 1
		local n = 0
		for i = 3, limit, -1 do
			local _i = i
			hit[_i] = true
			n += 1
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #7", function()
		local hit = {}
		local limit = 1
		local n = 0
		do
			local i = 3
			local _shouldIncrement = false
			while true do
				if _shouldIncrement then
					i -= 1
				else
					_shouldIncrement = true
				end
				if not (i <= limit) then
					break
				end
				local _i = i
				hit[_i] = true
				n += 1
			end
		end
		expect(n).to.equal(0)
		expect(hit[1] ~= nil).to.equal(false)
		expect(hit[2] ~= nil).to.equal(false)
		expect(hit[3] ~= nil).to.equal(false)
		for i = 3, limit, -1 do
			local _i = i
			hit[_i] = true
			n += 1
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support optimized simple loops #8", function()
		local hit = {}
		local n = 0
		do
			local i = 3
			local _shouldIncrement = false
			while true do
				if _shouldIncrement then
					i -= 1
				else
					_shouldIncrement = true
				end
				if not (i <= 1) then
					break
				end
				local _i = i
				hit[_i] = true
				n += 1
			end
		end
		expect(n).to.equal(0)
		expect(hit[1] ~= nil).to.equal(false)
		expect(hit[2] ~= nil).to.equal(false)
		expect(hit[3] ~= nil).to.equal(false)
		for i = 3, 1, -1 do
			local _i = i
			hit[_i] = true
			n += 1
		end
		expect(n).to.equal(3)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
	end)
	it("should support for-of loops over arrays", function()
		local hit = {}
		local array = { "1", "2", "3", "4" }
		local n = 0
		for _, v in array do
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(4)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over array literals", function()
		local hit = {}
		local n = 0
		for _, v in { "1", "2", "3", "4" } do
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(4)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over string literals", function()
		local hit = {}
		local n = 0
		for v in string.gmatch("1234", utf8.charpattern) do
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(4)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over strings", function()
		local hit = {}
		local str = "1234"
		local n = 0
		for v in string.gmatch(str, utf8.charpattern) do
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(4)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over computed strings", function()
		local i = 0
		for letter in string.gmatch(`{i}s`, utf8.charpattern) do
		end
	end)
	it("should support for-of loops over Set literals", function()
		local hit = {}
		local n = 0
		for v in {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		} do
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(4)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over Sets", function()
		local hit = {}
		local set = {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		}
		local n = 0
		for v in set do
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(4)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over Map literals", function()
		local hit = {}
		local n = 0
		for _k, _v in {
			["1"] = "2",
			["3"] = "4",
		} do
			local v = { _k, _v }
			local _arg0 = v[1]
			hit[_arg0] = true
			local _arg0_1 = v[2]
			hit[_arg0_1] = true
			n += 1
		end
		expect(n).to.equal(2)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support for-of loops over Maps", function()
		local hit = {}
		local map = {
			["1"] = "2",
			["3"] = "4",
		}
		local n = 0
		for _k, _v in map do
			local v = { _k, _v }
			local _arg0 = v[1]
			hit[_arg0] = true
			local _arg0_1 = v[2]
			hit[_arg0_1] = true
			n += 1
		end
		expect(n).to.equal(2)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support optimized destructuring in for-of loops over Maps", function()
		local hit = {}
		local map = {
			["1"] = "2",
			["3"] = "4",
		}
		local n = 0
		for k, v in map do
			hit[k] = true
			hit[v] = true
			n += 1
		end
		expect(n).to.equal(2)
		expect(next(difference(hit, {
			["1"] = true,
			["2"] = true,
			["3"] = true,
			["4"] = true,
		})) == nil).to.equal(true)
	end)
	it("should support destructuring optimized destructuring in for-of loops over Maps", function()
		local n = 0
		for _binding, _binding_1 in {
			[{ "1", "2" }] = { "3", "4" },
		} do
			local i = _binding[1]
			local j = _binding[2]
			local k = _binding_1[1]
			local v = _binding_1[2]
			expect(i).to.equal("1")
			expect(j).to.equal("2")
			expect(k).to.equal("3")
			expect(v).to.equal("4")
			n += 1
		end
		expect(n).to.equal(1)
	end)
	it("should support break", function()
		local array = { 1, 2, 3, 4, 5 }
		local sum = 0
		for _, value in array do
			sum += value
			if value == 3 then
				break
			end
		end
		expect(sum).to.equal(6)
	end)
	it("should support continue", function()
		local array = { 1, 2, 3, 4 }
		local sum = 0
		for _, value in array do
			if value == 3 then
				continue
			end
			sum += value
		end
		expect(sum).to.equal(7)
	end)
	it("should support continue with numeric loops", function()
		local x = 0
		for i = 0, 9 do
			if i % 2 == 0 then
				continue
			end
			x += 1
		end
		expect(x).to.equal(5)
	end)
	it("should support do-while loops", function()
		local x = 0
		repeat
			do
				x += 5
			end
		until not (x < 25)
		expect(x).to.equal(25)
		local function expect0(y)
			expect(y).to.equal(0)
			return false
		end
		local y = 0
		repeat
			do
				local y = 1
				expect(y).to.equal(1)
			end
		until not expect0(y)
	end)
	it("should support while loops", function()
		local x = 0
		while x < 10 do
			x += 1
		end
		expect(x).to.equal(10)
	end)
	it("should support for-of destructuring", function()
		local arr = { {
			a = 1,
			b = 2,
			c = 3,
		} }
		for _, _binding in arr do
			local a = _binding.a
			local b = _binding.b
			local c = _binding.c
			expect(a).to.equal(1)
			expect(b).to.equal(2)
			expect(c).to.equal(3)
		end
	end)
	it("should work with gmatch", function()
		for a in string.gmatch("H", ".") do
			expect(a).to.equal("H")
		end
		for a, b, c, d in string.gmatch("Hello!", "(%l)(%l)(%l)(%l)") do
			expect(a).to.equal("e")
			expect(b).to.equal("l")
			expect(c).to.equal("l")
			expect(d).to.equal("o")
		end
	end)
	it("should support indexing tuple as array", function()
		local obj = {
			a = 1,
			b = 2,
			c = 3,
		}
		for _element, _element_1 in pairs(obj) do
			local tuple = { _element, _element_1 }
			expect(tuple[2]).to.equal(obj[tuple[1]])
		end
	end)
	it("should support iterator function with single return when indexing tuple as array", function()
		local shortIterator = (function()
			return true
		end)
		for _element in shortIterator do
			local tuple = { _element }
			expect(#tuple).to.equal(1)
			break
		end
	end)
	it("should support iterator function with multiple returns when indexing tuple as array", function()
		local longIterator = (function()
			return true, true, true, true, true, true
		end)
		for _element, _element_1, _element_2, _element_3, _element_4, _element_5 in longIterator do
			local tuple = { _element, _element_1, _element_2, _element_3, _element_4, _element_5 }
			expect(#tuple).to.equal(6)
			break
		end
	end)
	it("should support the $range macro without step", function()
		local hit = {}
		local sum = 10
		for i = 0, 9 do
			hit[i] = true
			sum -= 1
		end
		expect(sum).to.equal(0)
		expect(hit[0] ~= nil).to.equal(true)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
		expect(hit[4] ~= nil).to.equal(true)
		expect(hit[5] ~= nil).to.equal(true)
		expect(hit[6] ~= nil).to.equal(true)
		expect(hit[7] ~= nil).to.equal(true)
		expect(hit[8] ~= nil).to.equal(true)
		expect(hit[9] ~= nil).to.equal(true)
	end)
	it("should support the $range macro with a negative step", function()
		local hit = {}
		local sum = 10
		for i = 9, 0, -1 do
			hit[i] = true
			sum -= 1
		end
		expect(sum).to.equal(0)
		expect(hit[0] ~= nil).to.equal(true)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
		expect(hit[4] ~= nil).to.equal(true)
		expect(hit[5] ~= nil).to.equal(true)
		expect(hit[6] ~= nil).to.equal(true)
		expect(hit[7] ~= nil).to.equal(true)
		expect(hit[8] ~= nil).to.equal(true)
		expect(hit[9] ~= nil).to.equal(true)
	end)
	it("should support the $range macro with a decimal step", function()
		local hit = {}
		local sum = 19
		for i = 0, 9, 0.5 do
			hit[i] = true
			sum -= 1
		end
		expect(sum).to.equal(0)
		expect(hit[0] ~= nil).to.equal(true)
		expect(hit[0.5] ~= nil).to.equal(true)
		expect(hit[1] ~= nil).to.equal(true)
		expect(hit[1.5] ~= nil).to.equal(true)
		expect(hit[2] ~= nil).to.equal(true)
		expect(hit[2.5] ~= nil).to.equal(true)
		expect(hit[3] ~= nil).to.equal(true)
		expect(hit[3.5] ~= nil).to.equal(true)
		expect(hit[4] ~= nil).to.equal(true)
		expect(hit[4.5] ~= nil).to.equal(true)
		expect(hit[5] ~= nil).to.equal(true)
		expect(hit[5.5] ~= nil).to.equal(true)
		expect(hit[6] ~= nil).to.equal(true)
		expect(hit[6.5] ~= nil).to.equal(true)
		expect(hit[7] ~= nil).to.equal(true)
		expect(hit[7.5] ~= nil).to.equal(true)
		expect(hit[8] ~= nil).to.equal(true)
		expect(hit[8.5] ~= nil).to.equal(true)
		expect(hit[9] ~= nil).to.equal(true)
	end)
end
