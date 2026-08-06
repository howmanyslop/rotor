let n = 0;
a: b: for (const x of [1, 2, 3]) {
	for (const y of [1, 2]) {
		if (y === 1) {
			continue a;
		}
		if (x === 3) {
			continue b;
		}
		n += 1;
	}
	n += 100;
}
print(n);
