let n = 0;
outer: for (const a of [1, 2, 3]) {
	switch (a) {
		case 2:
			break outer;
	}
	n += a;
}
print(n);
