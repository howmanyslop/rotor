let n = 0;
outer: for (const x of [1, 2]) {
	try {
		if (x === 1) {
			continue outer;
		}
	} catch (e) {
		n += 1;
	}
}
print(n);
