declare function consume(first: number, callback: (value: number) => number, last: number): void;

const collide = 5;
consume(
	collide,
	function collide(value: number): number {
		return value === 0 ? 0 : collide(value - 1);
	},
	collide,
);
print(collide);
