-- Compiled with roblox-ts v3.0.0
local function add(a, b)
	return a + b
end
local function greet(name, punct)
	if punct == nil then
		punct = "!"
	end
	return "hi " .. name .. punct
end
local function sum(...)
	local total = 0
	for _i = 1, select("#", ...) do
		local n = (select(_i, ...))
		total += n
	end
	return total
end
local isOdd
local function isEven(n)
	return if n == 0 then true else isOdd(n - 1)
end
function isOdd(n)
	return if n == 0 then false else isEven(n - 1)
end
local function double(x)
	return x * 2
end
print(add(1, 2), greet("bob"), greet("ann", "?"), sum(1, 2, 3), isEven(4), double(21))
return {
	double = double,
}
