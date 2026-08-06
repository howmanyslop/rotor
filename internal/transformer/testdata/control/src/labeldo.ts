let n = 0;
outer: do {
	for (const a of [1, 2]) {
		if (a === 2) {
			break outer;
		}
		n += 1;
	}
} while (n < 10);
print(n);
