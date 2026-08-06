let n = 0;
for (const x of [1, 2]) {
	try {
		inner: for (const y of [1, 2]) {
			for (const z of [1, 2]) {
				if (z === 2) {
					break inner;
				}
				n += 1;
			}
		}
	} catch (e) {
		n += 1;
	}
}
print(n);
