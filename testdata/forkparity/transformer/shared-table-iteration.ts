export function iterateTable(t: SharedTable) {
	let sum = 0;
	for (const [k, v] of t) {
		sum += v as number;
	}
	return sum;
}
