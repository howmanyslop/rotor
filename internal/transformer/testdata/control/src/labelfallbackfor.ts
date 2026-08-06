const fns = new Array<() => number>();
outer: for (let i = 0; i !== 3; i++) {
	fns.push(() => i);
	for (const a of [1, 2]) {
		if (a === 2) {
			continue outer;
		}
	}
}
print(fns.size());
