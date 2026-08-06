let n = 0;
outer: for (let i = 0; i < 3; i++) {
	for (let j = 0; j < 3; j++) {
		if (j === 2) {
			continue outer;
		}
		n += 1;
	}
	n += 100;
}
print(n);
