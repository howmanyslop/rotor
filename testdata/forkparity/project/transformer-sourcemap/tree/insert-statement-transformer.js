"use strict";
module.exports = function () {
  return function (context) {
    return function (sourceFile) {
      const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
        context.factory.createVariableDeclaration("__INJECTED__", undefined, undefined, context.factory.createStringLiteral("transformer-was-here"))
      ], 1));
      return context.factory.updateSourceFile(sourceFile, [marker].concat(sourceFile.statements));
    };
  };
};
