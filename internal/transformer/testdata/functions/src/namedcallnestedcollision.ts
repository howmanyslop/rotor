declare function consume(callback: () => number): void;
declare function _collide(): number;

consume(function collide(): number {
	return collide();
});

function later() {
	return _collide();
}
