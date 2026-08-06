// ROTOR-OWNED behavioral spec: loop labels are a rotor extension (rbxtsc 3.0.0
// rejects every program in this file with `noLabeledStatement`), so this spec
// has no byte-parity golden and is NOT part of the shared conformance corpus.
// It is staged into the Lune suite by stageRuntimeSuiteProject.
//
// Ported from roblox-ts PR #2928's tests/src/tests/loop.spec.ts additions, plus
// the cases that PR gets wrong (unlabeled enclosing loop, `break` through a
// switch, labeled block, two labels on one loop).

export = () => {
	it("should support a labeled break out of nested loops", () => {
		const seen = new Array<string>();
		outer: for (const a of [1, 2, 3]) {
			for (const b of [1, 2, 3]) {
				if (a === 2 && b === 2) {
					break outer;
				}
				seen.push(`${a}${b}`);
			}
		}
		expect(seen.join(",")).to.equal("11,12,13,21");
	});

	it("should support a labeled continue out of nested loops", () => {
		const seen = new Array<string>();
		let tails = 0;
		outer: for (const a of [1, 2, 3]) {
			for (const b of [1, 2, 3]) {
				if (b === 2) {
					continue outer;
				}
				seen.push(`${a}${b}`);
			}
			tails++;
		}
		expect(seen.join(",")).to.equal("11,21,31");
		expect(tails).to.equal(0);
	});

	it("should reset the label flag between iterations", () => {
		let hits = 0;
		let tails = 0;
		outer: for (const a of [1, 2, 3]) {
			for (const b of [1, 2]) {
				if (a === 2 && b === 1) {
					continue outer;
				}
				hits++;
			}
			tails++;
		}
		// a=1: 2 hits + tail, a=2: continue, a=3: 2 hits + tail
		expect(hits).to.equal(4);
		expect(tails).to.equal(2);
	});

	it("should support a labeled break targeting the innermost loop", () => {
		let n = 0;
		only: for (const a of [1, 2, 3]) {
			if (a === 2) {
				break only;
			}
			n += a;
		}
		expect(n).to.equal(1);
	});

	it("should support a labeled continue targeting the innermost loop", () => {
		let n = 0;
		only: for (const a of [1, 2, 3]) {
			if (a === 2) {
				continue only;
			}
			n += a;
		}
		expect(n).to.equal(4);
	});

	it("should not break an unlabeled enclosing loop", () => {
		// PR #2928 indexes the label stack by loop depth and emits a second
		// check after the `a` loop, which kills this outer loop too.
		const seen = new Array<number>();
		for (const x of [1, 2, 3]) {
			a: for (const y of [1, 2]) {
				for (const z of [1, 2]) {
					if (z === 2) {
						break a;
					}
				}
			}
			seen.push(x);
		}
		expect(seen.join(",")).to.equal("1,2,3");
	});

	it("should support a labeled break through a switch", () => {
		// A switch lowers to `repeat ... until true`, so a bare `break` here
		// would only leave the switch.
		const seen = new Array<number>();
		outer: for (const a of [1, 2, 3]) {
			switch (a) {
				case 2:
					break outer;
			}
			seen.push(a);
		}
		expect(seen.join(",")).to.equal("1");
	});

	it("should support a labeled continue through a switch", () => {
		const seen = new Array<number>();
		let tails = 0;
		outer: for (const a of [1, 2, 3]) {
			switch (a) {
				case 2:
					continue outer;
			}
			seen.push(a);
			tails++;
		}
		expect(seen.join(",")).to.equal("1,3");
		expect(tails).to.equal(2);
	});

	it("should support a labeled block", () => {
		let n = 0;
		blk: {
			n += 1;
			if (n === 1) {
				break blk;
			}
			n += 100;
		}
		expect(n).to.equal(1);
	});

	it("should support a labeled if statement", () => {
		let n = 0;
		cond: if (n === 0) {
			n += 1;
			if (n === 1) {
				break cond;
			}
			n += 100;
		}
		expect(n).to.equal(1);
	});

	it("should support a labeled block broken from a nested loop", () => {
		const seen = new Array<number>();
		blk: {
			for (const a of [1, 2, 3]) {
				if (a === 3) {
					break blk;
				}
				seen.push(a);
			}
			seen.push(100);
		}
		expect(seen.join(",")).to.equal("1,2");
	});

	it("should support two labels on one loop", () => {
		const seen = new Array<string>();
		let tails = 0;
		a: b: for (const x of [1, 2, 3]) {
			for (const y of [1, 2]) {
				if (x === 1) {
					continue a;
				}
				if (x === 2) {
					continue b;
				}
				if (y === 2) {
					break a;
				}
				seen.push(`${x}${y}`);
			}
			tails++;
		}
		expect(seen.join(",")).to.equal("31");
		expect(tails).to.equal(0);
	});

	it("should support labels on while loops", () => {
		let n = 0;
		let tails = 0;
		outer: while (n < 6) {
			for (const a of [1, 2]) {
				n += 1;
				if (a === 1) {
					continue outer;
				}
			}
			tails++;
		}
		expect(n).to.equal(6);
		expect(tails).to.equal(0);
	});

	it("should support labels on do/while loops", () => {
		let n = 0;
		outer: do {
			for (const a of [1, 2, 3]) {
				n += 1;
				if (a === 2) {
					break outer;
				}
			}
		} while (n < 100);
		expect(n).to.equal(2);
	});

	it("should support labels on optimized numeric for loops", () => {
		let n = 0;
		let tails = 0;
		outer: for (let i = 0; i < 3; i++) {
			for (let j = 0; j < 3; j++) {
				if (j === 1) {
					continue outer;
				}
				n += 1;
			}
			tails++;
		}
		expect(n).to.equal(3);
		expect(tails).to.equal(0);
	});

	it("should support labels on fallback for loops with captured variables", () => {
		const fns = new Array<() => number>();
		outer: for (let i = 0; i !== 3; i++) {
			fns.push(() => i);
			for (const a of [1, 2]) {
				if (a === 2) {
					continue outer;
				}
			}
		}
		expect(fns.size()).to.equal(3);
		expect(fns[0]()).to.equal(0);
		expect(fns[1]()).to.equal(1);
		expect(fns[2]()).to.equal(2);
	});

	it("should support labels on $range loops", () => {
		let n = 0;
		outer: for (const i of $range(1, 3)) {
			for (const j of $range(1, 3)) {
				if (j === 2) {
					break outer;
				}
				n += i;
			}
		}
		expect(n).to.equal(1);
	});

	it("should support labels on for-of loops over maps", () => {
		const map = new Map<string, number>([
			["a", 1],
			["b", 2],
		]);
		let entries = 0;
		outer: for (const [, v] of map) {
			for (const w of [1, 2]) {
				if (w === 2) {
					break outer;
				}
				entries += v;
			}
		}
		expect(entries > 0).to.equal(true);
	});

	it("should not leak label state into a closure", () => {
		let inner = 0;
		let n = 0;
		outer: for (const a of [1, 2, 3]) {
			const f = () => {
				for (const b of [1, 2]) {
					inner += b;
				}
			};
			f();
			for (const c of [1, 2]) {
				if (a === 2) {
					break outer;
				}
				n += c;
			}
		}
		expect(inner).to.equal(6);
		expect(n).to.equal(3);
	});

	it("should support a label declared inside a try block", () => {
		const seen = new Array<number>();
		for (const x of [1, 2]) {
			try {
				inner: for (const y of [1, 2, 3]) {
					for (const z of [1, 2]) {
						if (z === 2) {
							break inner;
						}
						seen.push(y);
					}
				}
			} catch (e) {}
		}
		expect(seen.join(",")).to.equal("1,1");
	});

	it("should support deeply nested unwinding", () => {
		const seen = new Array<string>();
		let tails = 0;
		a: for (const x of [1, 2]) {
			for (const y of [1, 2]) {
				for (const z of [1, 2]) {
					if (z === 2) {
						continue a;
					}
					seen.push(`${x}${y}${z}`);
				}
			}
			tails++;
		}
		expect(seen.join(",")).to.equal("111,211");
		expect(tails).to.equal(0);
	});
};
