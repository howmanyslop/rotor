let n = 0;
function a() {
	n += 1;
	return n < 5;
}
do {
	if (n === 2) {
		continue;
	}
	n += 1;
} while (a() && n++ < 20);
print(n);
