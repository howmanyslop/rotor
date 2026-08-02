export function safeRead(index: number, ...args: Array<number>) {
	const a = args[0];
	const b = args[1];
	const c = args[index];
	return a + b + c;
}

export function safeSize(...args: Array<number>) {
	return args.size();
}

export function safeForOf(...args: Array<number>) {
	let sum = 0;
	for (const x of args) {
		sum += x;
	}
	return sum;
}

export function unsafeNested(...args: Array<number>) {
	return function () {
		return args[0];
	};
}
