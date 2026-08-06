declare function consume(value: unknown): void;

function withDefault(callback = function defaultNamed(): void {}): void {
	consume(callback);
}

for (let index = 0; index < 3; index += 1) {
	consume(function loopNamed(): number {
		return index;
	});
}

const factory = (): (() => number) =>
	function returned(): number {
		return 4;
	};

withDefault();
consume(factory);
