function collect<T extends Array<unknown>>(
	iterator: IterableFunction<LuaTuple<[number, ...T]>>,
): void {
	for (const entry of iterator) {
		print(entry[0]);
	}
}
