-- Compiled with roblox-ts v3.0.0
local arr = { 1, 2, 3, 4 }
local first = arr[1]
local second = arr[2]
local third = arr[3]
local head = 10
local obj = {
	x = 1,
	y = 2,
	nested = {
		z = 3,
	},
}
local x = obj.x
local renamed = obj.y
local _binding = obj.nested
local z = _binding.z
local _binding_1 = {
	x = 5,
}
local dx = _binding_1.x
if dx == nil then
	dx = 99
end
local missing = _binding_1.missing
if missing == nil then
	missing = 7
end
local a = 0
local b = 0
a, b = b, a
local _binding_2 = obj
a = _binding_2.x
print(first, second, third, head, x, renamed, z, dx, missing, a, b)
return nil
