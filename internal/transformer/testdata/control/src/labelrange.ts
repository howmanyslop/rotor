let n = 0;
outer: for (const i of $range(1, 3)) {
	for (const j of $range(1, 3)) {
		if (j === 2) {
			break outer;
		}
		n += i;
	}
}
print(n);
