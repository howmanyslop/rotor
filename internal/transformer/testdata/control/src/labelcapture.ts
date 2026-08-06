let n = 0;
for (const x of [1, 2]) {
	blk: {
		if (x === 1) {
			break blk;
		}
		if (x === 2) {
			break;
		}
		n += x;
	}
}
print(n);
