-- Compiled with roblox-ts v3.0.0
-- for-of over Set: single binding `for x in exp do`
for tag in tags do
	print(tag)
end
-- for-of over Map: the [k, v] inline-destructure fast path
for name, score in scores do
	print(name, score)
end
-- Map plain-id fallback: `for _k, _v in` + `local pair = { _k, _v }`
for _k, _v in scores do
	local pair = { _k, _v }
	print(pair[1], pair[2])
end
-- Map assignment-form fast path: `for _binding, _binding in` + writes
local k3 = ""
local v3 = 0
for _binding, _binding_1 in scores do
	k3 = _binding
	v3 = _binding_1
	print(k3, v3)
end
-- for-of over string: string.gmatch(exp, utf8.charpattern)
local word = "héllo"
for ch in string.gmatch(word, utf8.charpattern) do
	print(ch)
end
-- $range without step
for i = 1, 5 do
	print(i)
end
-- $range with a literal step
for j = 10, 2, 2 do
	print(j)
end
-- $range with a non-literal step expression (`or 1` defaulting)
for n = 0, 10, stepValue or 1 do
	print(n)
end
-- for-of over a declared generator object: .next()/.done/.value protocol
for _result in gen.next do
	if _result.done then
		break
	end
	local g = _result.value
	print(g)
end
-- for-of over IterableFunction<T>: `for x in exp do`
for v in iter do
	print(v)
end
-- IterableFunction<LuaTuple<T>>: inline array-pattern fast path
for pk, pv in pairsIter do
	print(pk, pv)
end
-- plain-id over a labeled tuple: arity introspection names temps from labels
for _key, _value in pairsIter do
	local entry = { _key, _value }
	print(entry[1], entry[2])
end
-- plain-id over an unlabeled tuple: temps named `_element`
for _element, _element_1 in dualIter do
	local duo = { _element, _element_1 }
	print(duo[1], duo[2])
end
-- rest-element tuple: unknown arity, while-true protocol
local _restIter = restIter
while true do
	local packed = { _restIter() }
	if #packed == 0 then
		break
	end
	print(packed[1])
end
-- destructuring FROM a Map: `local _k, _v = next(map[, lastK])`
local _k, _v = next(scores)
local _binding = { _k, _v }
local ka = _binding[1]
local va = _binding[2]
local _k_1, _v_1 = next(scores, _k)
local _binding_1 = { _k_1, _v_1 }
local kb = _binding_1[1]
local vb = _binding_1[2]
print(ka, va, kb, vb)
-- destructuring FROM a Set: `next(set[, lastValue])` continuation
local _value = next(tags)
local firstTag = _value
local _value_1 = next(tags, _value)
local secondTag = _value_1
print(firstTag, secondTag)
-- destructuring FROM a string: gmatch matcher (hole still advances)
local _matcher = string.gmatch(word, utf8.charpattern)
local c1 = _matcher()
_matcher()
local c3 = _matcher()
print(c1, c3)
-- destructuring FROM an IterableFunction: `iter()` per element
local n1 = iter()
local n2 = iter()
print(n1, n2)
-- destructuring FROM a generator object: `gen.next().value`
local g1 = gen.next().value
local g2 = gen.next().value
print(g1, g2)
return nil
