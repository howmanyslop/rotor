declare function consume(value: unknown): void;

const mismatch = function different() {};
let letNamed = function letNamed() {};
export const exportedNamed = function exportedNamed() {};
const wrapped = (function wrapped() {});
const asserted = function asserted() {} as () => void;
const objectValue = { value: function objectNamed() {} };
const first = function first() {},
	second = function second() {};
const separatelyExported = function separatelyExported() {};
export { separatelyExported };

(function callee() {})();
consume(function wrappedArgument() {});
consume(function assertedArgument() {} as () => void);
consume(mismatch);
consume(letNamed);
consume(objectValue);
consume(first);
consume(second);
