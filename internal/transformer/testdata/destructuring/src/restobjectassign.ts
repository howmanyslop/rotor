let x = 0;
let restObject = {};
const object = { x: 1, y: 2 };
({ x, ...restObject } = object);
print(x, restObject);
