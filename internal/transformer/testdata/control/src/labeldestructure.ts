let n = 0;
const map = new Map<string, number>();
outer: for (const [k, v] of map) {
	for (const w of [1, 2]) {
		if (w === 2) {
			break outer;
		}
		n += v;
	}
}
print(n);
