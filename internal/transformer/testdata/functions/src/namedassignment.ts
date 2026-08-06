declare function useEffect(callback: () => void, dependencies: Array<unknown>): void;
declare function warn(message: string): void;
declare const cache: Array<unknown>;
declare const name: string;

let effect: (() => void) | undefined;
let dependencies: Array<unknown> | undefined;
if (cache[0] !== name) {
	dependencies = [name];
	effect = function namedEffect(): void {
		warn(name);
	};
	cache[0] = name;
	cache[1] = effect;
} else {
	effect = cache[1] as () => void;
	dependencies = cache[2] as Array<unknown>;
}

useEffect(effect, dependencies);
