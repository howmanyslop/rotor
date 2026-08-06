let n = 0;
outer: while (n < 10) {
	for (const a of [1, 2]) {
		if (a === 2) {
			continue outer;
		}
		n += 1;
	}
	n += 100;
}
print(n);
