export function restSet(s: Set<number>) {
	const [x, ...rest] = s;
	return rest;
}

export function restMap(m: Map<string, number>) {
	const [x, ...rest] = m;
	return rest;
}

export function* gen() {
	yield 1;
	yield 2;
	yield 3;
}

export function restGenerator() {
	const [x, ...rest] = gen();
	return rest;
}
