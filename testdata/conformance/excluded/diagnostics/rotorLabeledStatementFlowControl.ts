for (const x of [1, 2, 3]) {
	blk: {
		if (x === 1) {
			break blk;
		}
		break;
	}
}
