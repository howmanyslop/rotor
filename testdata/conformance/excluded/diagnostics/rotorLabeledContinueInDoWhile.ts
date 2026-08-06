let n = 0;
function step() {
	n += 1;
	return n < 5;
}
outer: do {
	for (const x of [1, 2]) {
		if (x === 2) {
			continue outer;
		}
	}
} while (step() && n++ < 20);
