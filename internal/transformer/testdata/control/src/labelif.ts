let n = 0;
a: if (n === 0) {
	n += 1;
	if (n === 1) {
		break a;
	}
	n += 100;
}
print(n);
