"use strict";
module.exports = function (_, config) {
  return function (context) {
    return function (sourceFile) {
      const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
        context.factory.createVariableDeclaration("__ORDER_" + config.label.toUpperCase() + "__", undefined, undefined, context.factory.createStringLiteral(config.label))
      ], 1));
      return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
    };
  };
};
