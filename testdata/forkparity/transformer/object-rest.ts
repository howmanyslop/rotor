export function objectRest() {
	const obj = { a: 1, b: 2, c: 3 };
	const { b: bb, ...rest } = obj;
	return rest;
}
