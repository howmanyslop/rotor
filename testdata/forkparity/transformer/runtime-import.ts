export function runtimePromise() {
	return new Promise<number>(resolve => resolve(42));
}
