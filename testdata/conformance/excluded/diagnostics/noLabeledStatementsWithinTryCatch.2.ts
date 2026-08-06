outer: for (const x of [1, 2, 3]) {
	try {
		continue outer;
	} catch (e) {}
}
