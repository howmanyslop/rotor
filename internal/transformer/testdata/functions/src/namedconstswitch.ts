declare const selector: number;

switch (selector) {
	case 0:
		const namedFunction = function namedFunction() {
			return namedFunction;
		};
		break;
	case 1:
		// @ts-expect-error exercising cross-clause predeclaration lowering
		print(namedFunction);
		break;
}
