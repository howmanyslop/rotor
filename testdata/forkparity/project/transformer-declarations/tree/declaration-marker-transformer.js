"use strict";
module.exports = function () {
  return function (context) {
    return function (sourceFile) {
      const marker = context.factory.createVariableStatement(undefined, context.factory.createVariableDeclarationList([
        context.factory.createVariableDeclaration("__DECLARATION_MARKER__", undefined, undefined, context.factory.createStringLiteral("after-declarations"))
      ], 1));
      return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
    };
  };
};
