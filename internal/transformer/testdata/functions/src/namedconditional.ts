declare const flag: boolean;

const short = flag && function shortNamed(): number {
	return 1;
};

const ternary = flag
	? function thenNamed(): number {
			return 1;
		}
	: function elseNamed(): number {
			return 2;
		};

print(short, ternary);
