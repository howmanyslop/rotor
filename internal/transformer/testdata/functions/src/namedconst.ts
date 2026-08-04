const namedFunction = function namedFunction(value: number): number {
	return value === 0 ? 0 : namedFunction(value - 1);
};

print(namedFunction(2));
