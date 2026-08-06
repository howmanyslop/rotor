let n = 0;
for (const x of [1, 2]) {
	a: for (const y of [1, 2]) {
		for (const z of [1, 2]) {
			if (z === 2) {
				break a;
			}
			n += 1;
		}
	}
	n += 100;
}
print(n);
