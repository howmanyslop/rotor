"use strict";

let declarationEmitCount = 0;

module.exports = function () {
  return function (context) {
    return function (sourceFile) {
      declarationEmitCount += 1;
      const marker = context.factory.createVariableStatement(
        undefined,
        context.factory.createVariableDeclarationList([
          context.factory.createVariableDeclaration(
            `__DECLARATION_EMIT_${declarationEmitCount}__`,
            undefined,
            undefined,
            context.factory.createStringLiteral(sourceFile.fileName),
          ),
        ], 1),
      );
      return context.factory.updateSourceFile(sourceFile, sourceFile.statements.concat([marker]));
    };
  };
};
