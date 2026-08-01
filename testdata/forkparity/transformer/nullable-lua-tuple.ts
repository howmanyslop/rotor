export function foo(): LuaTuple<[number, string]> | undefined {
	return $tuple(1, "a");
}

export function bar(): LuaTuple<[number, string]> | undefined {
	return undefined;
}

export function indexNullable() {
	const a = foo()?.[1];
	const b = bar()?.[1];
	return a;
}
