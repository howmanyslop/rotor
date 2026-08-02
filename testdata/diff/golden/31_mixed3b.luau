-- Compiled with roblox-ts v3.0.0
-- Phase 3b adversarial mix: macro tables, optional chaining, and the full
-- iteration builders interacting with each other — not in isolation.
-- ?? joining an optional Map with a Map-returning call, used as a for-of source
local source = registry or getFallback()
-- for-of over a Map with [k, v] destructuring; optional-chained macros + ?? in body
local lines = {}
for name, entry in source do
	local _result = entry.weights
	if _result ~= nil then
		_result = _result[name]
	end
	local _condition = _result
	if _condition == nil then
		-- ▼ ReadonlySet.size ▼
		local _size = 0
		for _ in entry.tags do
			_size += 1
		end
		-- ▲ ReadonlySet.size ▲
		_condition = _size
	end
	local weight = _condition
	-- string macro on a template-literal result, pushed via an Array macro
	local _arg0 = string.upper(`{name}:{weight}`)
	table.insert(lines, _arg0)
end
-- String/Array macro chain: join -> split -> map -> filter -> join
local _exp = string.split(table.concat(lines, ","), ",")
-- ▼ ReadonlyArray.map ▼
local _newValue = table.create(#_exp)
local _callback = function(part)
	return string.lower(part)
end
for _k, _v in _exp do
	_newValue[_k] = _callback(_v, _k - 1, _exp)
end
-- ▲ ReadonlyArray.map ▲
-- ▼ ReadonlyArray.filter ▼
local _newValue_1 = {}
local _callback_1 = function(part)
	return #part > 3
end
local _length = 0
for _k, _v in _newValue do
	if _callback_1(_v, _k - 1, _newValue) == true then
		_length += 1
		_newValue_1[_length] = _v
	end
end
-- ▲ ReadonlyArray.filter ▲
local slug = table.concat(_newValue_1, "-")
print(slug)
-- Map macro inside an optional chain, its result optional-accessed into a Set macro
local _probed = registry
if _probed ~= nil then
	_probed = _probed.alpha
	if _probed ~= nil then
		_probed = _probed.tags.hot ~= nil
	end
end
local probed = _probed
print(probed)
-- optional chain on a macro RESULT (pop -> string | undefined -> string macro)
-- ▼ Array.pop ▼
local _length_1 = #lines
local _result = lines[_length_1]
lines[_length_1] = nil
-- ▲ Array.pop ▲
local _popped = _result
if _popped ~= nil then
	_popped = string.sub(_popped, 1, 4)
end
local popped = _popped
print(popped)
-- assert narrows away undefined; the follow-up macro call is non-optional
assert(registry, "registry missing")
-- ▼ ReadonlyMap.size ▼
local _size = 0
for _ in registry do
	_size += 1
end
-- ▲ ReadonlyMap.size ▲
print(_size)
-- typeIs guard unlocking string macros on an unknown, chained into ArrayLike.size
if type(payload) == "string" then
	print(#string.split(payload, " "))
end
-- destructuring FROM a Set feeding $tuple, consumed inside a Map for-of with
-- an omitted key binding
local function firstTwo(tags)
	local _value = next(tags)
	local a = _value
	local _value_1 = next(tags, _value)
	local b = _value_1
	return a, b
end
for _, entry in source do
	local first, second = firstTwo(entry.tags)
	local _condition = second
	if _condition == nil then
		_condition = "none"
	end
	print(first, _condition)
end
-- $range with a macro-heavy body: chained Map.set, template literal + rep
local counts = {}
for i = 1, 5, 2 do
	local _arg1 = string.rep(`#{i}`, i)
	counts[i] = _arg1
	local _counts = counts
	local _arg0 = -i
	_counts[_arg0] = "neg"
end
-- ▼ ReadonlyMap.forEach ▼
local _callback_2 = function(v, k)
	return print(k, #v)
end
for _k, _v in counts do
	_callback_2(_v, _k, counts)
end
-- ▲ ReadonlyMap.forEach ▲
-- nested binding pattern (default + hole) over an IterableFunction of LuaTuples,
-- with macros on every piece
for key, _binding in pairsIter do
	local head = _binding[1]
	if head == nil then
		head = 0
	end
	local third = _binding[3]
	local _exp_1 = string.upper(key)
	local _condition = third
	if _condition == nil then
		_condition = head
	end
	print(_exp_1, head, _condition)
end
-- generator object iteration; LuaTuple-returning string macro in the body
for _result_1 in wordStream().next do
	if _result_1.done then
		break
	end
	local word = _result_1.value
	local start = string.find(word, "o")
	local _condition = start
	if _condition == nil then
		_condition = -1
	end
	print(_condition)
end
return nil
