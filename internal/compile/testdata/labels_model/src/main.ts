let n = 0;

outer: for (let i = 0; i < 3; i++) {
	for (let j = 0; j < 3; j++) {
		if (j === 1) {
			continue outer;
		}
		n += 1;
	}
	n += 100;
}

search: for (let i = 0; i < 3; i++) {
	switch (i) {
		case 2:
			break search;
	}
	n += i;
}

block: {
	n += 1;
	if (n > 0) {
		break block;
	}
	n += 100;
}

print(n);
