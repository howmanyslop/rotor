let n = 0;
function a() {
	n += 1;
	return n < 5;
}
outer: do {
	for (const x of [1, 2]) {
		if (x === 2) {
			continue outer;
		}
		n += 1;
	}
} while (a() && n++ < 20);
print(n);
