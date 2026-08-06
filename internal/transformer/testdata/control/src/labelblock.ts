let n = 0;
a: {
	n += 1;
	if (n === 1) {
		break a;
	}
	n += 100;
}
print(n);
