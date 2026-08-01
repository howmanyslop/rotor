declare global {
	// Fork-parity: SharedTable must be iterable for for-of destructuring.
	interface SharedTable extends Iterable<[string | number, SharedTableValue]> {}
}
export function iterateTable(t: SharedTable) {
	let sum = 0;
	for (const [k, v] of t) {
		sum += v as number;
	}
	return sum;
}
