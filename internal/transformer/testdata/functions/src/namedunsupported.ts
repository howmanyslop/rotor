// The two forms that still keep the diagnostic. Their name binds to the
// TS.async / TS.generator wrapper, not to the closure a lift would declare,
// so a self-reference inside the body would reach the wrong function.
const asyncNamed = async function asyncNamed() {};
const generatorNamed = function* generatorNamed() {};

print(asyncNamed, generatorNamed);
