declare function consume(first: number, callback: (value: number) => number, last: number): void;

function before() {
	return 1;
}

function after() {
	return 2;
}

consume(
	before(),
	function recurse(value: number): number {
		return value === 0 ? 0 : recurse(value - 1);
	},
	after(),
);
