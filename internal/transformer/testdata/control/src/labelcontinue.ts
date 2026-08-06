let n = 0;
outer: for (const a of [1, 2]) {
	for (const b of [1, 2]) {
		if (b === 2) {
			continue outer;
		}
		n += 1;
	}
	n += 100;
}
print(n);
