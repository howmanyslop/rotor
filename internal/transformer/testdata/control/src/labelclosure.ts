let n = 0;
outer: for (const a of [1, 2]) {
	const f = () => {
		for (const b of [1, 2]) {
			n += b;
		}
	};
	f();
	for (const c of [1, 2]) {
		if (c === 2) {
			break outer;
		}
	}
}
print(n);
