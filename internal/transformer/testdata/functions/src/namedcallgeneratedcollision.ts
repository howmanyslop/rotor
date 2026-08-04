function _collide(callback: () => number) {
	return callback();
}

_collide(function collide(): number {
	return collide();
});
