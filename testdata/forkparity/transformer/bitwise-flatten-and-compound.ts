export function flatAnd(a: number, b: number, c: number) {
	return a & b & c;
}

export function compound(bw: number) {
	bw &= 3;
	return bw;
}
