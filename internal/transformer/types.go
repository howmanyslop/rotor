package transformer

import (
	"strings"

	"rotor/tsgo/ast"
	"rotor/tsgo/checker"
	"rotor/tsgo/jsnum"
)

// This file ports TSTransformer/util/types.ts: the isDefinitelyType /
// isPossiblyType combinators and the leaf predicates the first transform wave
// needs (truthiness, binary `+`, relational operators, logical chains).
//
// ts.TypeFlags -> tsgo mapping is 1:1 by name (checker.TypeFlags*); upstream
// helper methods on ts.Type map as:
//   type.getConstraint()          -> Checker.GetBaseConstraintOfType (TS public
//                                    Type.getConstraint is exactly that call)
//   type.isClassOrInterface()     -> getObjectFlags gate + ObjectFlagsClassOrInterface
//   type.getBaseTypes()           -> Checker.GetBaseTypes
//   type.isNumberLiteral()/.value -> Type.IsNumberLiteral + AsLiteralType().Value()
//   typeChecker.getTrueType()     -> identity = BooleanLiteral flag + value +
//                                    freshness (see IsBooleanLiteralType)

// TypeCheck ports `type TypeCheck = (type: ts.Type) => boolean` (types.ts L8).
// Go cannot compare func values, but isPossiblyTypeInner must reproduce
// upstream's `callbacks.length === 1 && callbacks[0] === isUndefinedType`
// identity test, so the predicate carries an explicit marker instead.
type TypeCheck struct {
	check          func(t *checker.Type) bool
	undefinedCheck bool
}

// Check runs the predicate on t.
func (tc TypeCheck) Check(t *checker.Type) bool { return tc.check(t) }

// isClassOrInterface ports ts.Type.isClassOrInterface():
// `getObjectFlags(this) & ObjectFlags.ClassOrInterface`, where getObjectFlags
// yields 0 unless `type.flags & TypeFlags.ObjectFlagsType`.
func isClassOrInterface(t *checker.Type) bool {
	return t.Flags()&checker.TypeFlagsObjectFlagsType != 0 &&
		t.ObjectFlags()&checker.ObjectFlagsClassOrInterface != 0
}

// getRecursiveBaseTypes ports types.ts getRecursiveBaseTypes (L10-23): all
// base types of a class/interface, transitively.
func getRecursiveBaseTypes(s *State, t *checker.Type) []*checker.Type {
	var result []*checker.Type
	var inner func(t *checker.Type)
	inner = func(t *checker.Type) {
		for _, baseType := range s.Checker.GetBaseTypes(t) {
			result = append(result, baseType)
			if isClassOrInterface(baseType) {
				inner(baseType)
			}
		}
	}
	inner(t)
	return result
}

// constraintOrSelf ports `type.getConstraint() ?? type` (types.ts L39/L69).
func constraintOrSelf(s *State, t *checker.Type) *checker.Type {
	if constraint := s.Checker.GetBaseConstraintOfType(t); constraint != nil {
		return constraint
	}
	return t
}

// isDefinitelyTypeInner ports types.ts L25-36: union -> EVERY member must
// match, intersection -> SOME member, class/interface -> base types may match,
// leaf -> SOME callback matches.
func isDefinitelyTypeInner(s *State, t *checker.Type, callbacks []TypeCheck) bool {
	if t.IsUnion() {
		for _, member := range t.Types() {
			if !isDefinitelyTypeInner(s, member, callbacks) {
				return false
			}
		}
		return true
	} else if t.IsIntersection() {
		for _, member := range t.Types() {
			if isDefinitelyTypeInner(s, member, callbacks) {
				return true
			}
		}
		return false
	}
	if isClassOrInterface(t) {
		for _, baseType := range getRecursiveBaseTypes(s, t) {
			if isDefinitelyTypeInner(s, baseType, callbacks) {
				return true
			}
		}
	}
	for _, cb := range callbacks {
		if cb.Check(t) {
			return true
		}
	}
	return false
}

// IsDefinitelyType ports types.ts isDefinitelyType (L38-40): does the type
// (or its constraint) certainly satisfy one of the predicates?
func IsDefinitelyType(s *State, t *checker.Type, callbacks ...TypeCheck) bool {
	return isDefinitelyTypeInner(s, constraintOrSelf(s, t), callbacks)
}

// isPossiblyTypeInner ports types.ts L42-66: union AND intersection -> SOME
// member; unconstrained type variables / any / unknown are possibly anything;
// the rbxts `defined` type is possibly everything except `undefined` when
// that is the sole query.
func isPossiblyTypeInner(s *State, t *checker.Type, callbacks []TypeCheck) bool {
	if t.Flags()&checker.TypeFlagsUnionOrIntersection != 0 {
		for _, member := range t.Types() {
			if isPossiblyTypeInner(s, member, callbacks) {
				return true
			}
		}
		return false
	}
	if isClassOrInterface(t) {
		for _, baseType := range getRecursiveBaseTypes(s, t) {
			if isPossiblyTypeInner(s, baseType, callbacks) {
				return true
			}
		}
	}

	// type variable without constraint, any, or unknown
	if t.Flags()&(checker.TypeFlagsTypeVariable|checker.TypeFlagsAnyOrUnknown) != 0 {
		return true
	}

	// defined type
	if isDefinedType(s, t) {
		if len(callbacks) == 1 && callbacks[0].undefinedCheck {
			// if only matching undefined, then defined means not possible
			return false
		}
		return true
	}

	for _, cb := range callbacks {
		if cb.Check(t) {
			return true
		}
	}
	return false
}

// IsPossiblyType ports types.ts isPossiblyType (L68-70): could the type (or
// its constraint) satisfy one of the predicates?
func IsPossiblyType(s *State, t *checker.Type, callbacks ...TypeCheck) bool {
	return isPossiblyTypeInner(s, constraintOrSelf(s, t), callbacks)
}

// isDefinedType ports types.ts isDefinedType (L72-81): detects the rbxts
// `defined` type — flags EXACTLY equal to Object with no properties, call or
// construct signatures, and no number/string index types.
func isDefinedType(s *State, t *checker.Type) bool {
	return t.Flags() == checker.TypeFlagsObject &&
		len(s.Checker.GetPropertiesOfType(t)) == 0 &&
		len(s.Checker.GetCallSignatures(t)) == 0 &&
		len(s.Checker.GetConstructSignatures(t)) == 0 &&
		s.Checker.GetNumberIndexType(t) == nil &&
		s.Checker.GetStringIndexType(t) == nil
}

// ---------------------------------------------------------------------------
// Leaf predicates (types.ts L83-206)
// ---------------------------------------------------------------------------

// IsAnyType ports isAnyType (L83-85): identity with the checker's intrinsic
// `any` type.
func IsAnyType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		return t == s.Checker.GetAnyType()
	}}
}

func isBooleanTypeCheck(t *checker.Type) bool {
	return t.Flags()&(checker.TypeFlagsBoolean|checker.TypeFlagsBooleanLiteral) != 0
}

// IsBooleanType ports isBooleanType (L87-89).
var IsBooleanType = TypeCheck{check: isBooleanTypeCheck}

// IsBooleanLiteralType ports isBooleanLiteralType (L91-99). Upstream compares
// identity against typeChecker.getTrueType()/getFalseType(), and TS implements
// `getFalseType: (fresh) => fresh ? falseType : regularFalseType` — the
// NO-ARGUMENT upstream call yields the REGULAR true/false types (the `boolean`
// union's members), NOT the fresh literal-expression variants. The checker
// creates exactly four boolean literal types (fresh/regular x true/false), so
// identity is equivalent to value + regularness (the s parameter mirrors the
// upstream signature; tsgo needs no checker access here). Non-literals fall
// back to isBooleanType, so plain non-literal boolean-ish types count too.
// Verified against rbxtsc 3.0.0: `(boolean | undefined) ?? x` refuses the
// inline `or` form, which requires the regular `false` member to match.
func IsBooleanLiteralType(s *State, value bool) TypeCheck {
	_ = s
	return TypeCheck{check: func(t *checker.Type) bool {
		if t.Flags()&checker.TypeFlagsBooleanLiteral != 0 {
			lt := t.AsLiteralType()
			return lt.Value() == value && lt.RegularType() == t
		}
		return isBooleanTypeCheck(t)
	}}
}

func isNumberTypeCheck(t *checker.Type) bool {
	return t.Flags()&(checker.TypeFlagsNumber|checker.TypeFlagsNumberLike|checker.TypeFlagsNumberLiteral) != 0
}

// IsNumberType ports isNumberType (L101-103).
var IsNumberType = TypeCheck{check: isNumberTypeCheck}

// IsNumberLiteralType ports isNumberLiteralType (L105-112): a number literal
// type matches by value; any other number-ish type matches too (non-literal
// fallback).
func IsNumberLiteralType(value jsnum.Number) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		if t.IsNumberLiteral() {
			return t.AsLiteralType().Value() == value
		}
		return isNumberTypeCheck(t)
	}}
}

func isNaNTypeCheck(t *checker.Type) bool {
	return isNumberTypeCheck(t) && !t.IsNumberLiteral()
}

// IsNaNType ports isNaNType (L114-116): number-ish AND not a number literal
// (a literal has a known value, which is never NaN; there is no distinct NaN
// type in the checker).
var IsNaNType = TypeCheck{check: isNaNTypeCheck}

func isStringTypeCheck(t *checker.Type) bool {
	return t.Flags()&(checker.TypeFlagsString|checker.TypeFlagsStringLike|checker.TypeFlagsStringLiteral) != 0
}

// IsStringType ports isStringType (L118-120).
var IsStringType = TypeCheck{check: isStringTypeCheck}

// IsObjectType ports isObjectType (L181-183).
var IsObjectType = TypeCheck{check: func(t *checker.Type) bool {
	return t.Flags()&checker.TypeFlagsObject != 0
}}

func isUndefinedTypeCheck(t *checker.Type) bool {
	return t.Flags()&(checker.TypeFlagsUndefined|checker.TypeFlagsVoid) != 0
}

// IsUndefinedType ports isUndefinedType (L185-187). Its undefinedCheck marker
// drives the `defined`-type special case in isPossiblyTypeInner.
var IsUndefinedType = TypeCheck{check: isUndefinedTypeCheck, undefinedCheck: true}

// isTemplateLiteralType ports typeGuards.ts isTemplateLiteralType (L19-21:
// `"texts" in type && "types" in type && flags & TemplateLiteral`); in tsgo
// the flag alone identifies *TemplateLiteralType.
func isTemplateLiteralType(t *checker.Type) bool {
	return t.Flags()&checker.TypeFlagsTemplateLiteral != 0
}

func isEmptyStringTypeCheck(t *checker.Type) bool {
	if t.IsStringLiteral() {
		return t.AsLiteralType().Value() == ""
	}
	if isTemplateLiteralType(t) {
		texts := t.AsTemplateLiteralType().Texts()
		for _, text := range texts {
			if len(text) != 0 {
				return false
			}
		}
		return true
	}
	return isStringTypeCheck(t)
}

// IsEmptyStringType ports isEmptyStringType (L189-197): string literal "",
// template literal type with all-empty texts (`texts.length === 0 ||
// texts.every(v => v.length === 0)`), or any non-literal string-ish type.
var IsEmptyStringType = TypeCheck{check: isEmptyStringTypeCheck}

// IsArrayType ports isArrayType (L122-137): tuple types, array-like types
// (the checker's isArrayLikeType returns true for `any`, hence the explicit
// Any exclusion), or a type whose symbol is one of the ambient @rbxts array
// interfaces. Upstream compares against macroManager.getSymbolOrThrow(...);
// rotor resolves the same global names through the checker (State.AmbientSymbol).
func IsArrayType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		// typeChecker.isArrayLikeType() will return true for `any`, so rule it out here
		if t.Flags()&checker.TypeFlagsAny != 0 {
			return false
		}
		if t.IsTupleType() || s.Checker.IsArrayLikeType(t) {
			return true
		}
		symbol := t.Symbol()
		if symbol == nil {
			return false
		}
		return symbol == s.AmbientSymbol("ReadonlyArray") ||
			symbol == s.AmbientSymbol("Array") ||
			symbol == s.AmbientSymbol("ReadVoxelsArray") ||
			symbol == s.AmbientSymbol("TemplateStringsArray")
	}}
}

// IsLuaTupleType ports isLuaTupleType (L161-165): the type carries the
// nominal `_nominal_LuaTuple` property, identity-compared against the symbol
// resolved from the ambient LuaTuple<T> type alias (upstream MacroManager
// registers it the same way). Projects without @rbxts/compiler-types have no
// nominal symbol and nothing is ever a LuaTuple.
func IsLuaTupleType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		nominal := s.LuaTupleNominalSymbol()
		return nominal != nil && s.Checker.GetPropertyOfType(t, NominalLuaTupleName) == nominal
	}}
}

// symbolIsAmbient reports whether t's symbol is one of the named global
// ambient symbols (upstream `type.symbol === macroManager.getSymbolOrThrow(...)`
// chains; rotor resolves the same names through State.AmbientSymbol). The nil
// guard matters: AmbientSymbol returns nil for projects without
// @rbxts/compiler-types, and nil == nil must not match.
func symbolIsAmbient(s *State, t *checker.Type, names ...string) bool {
	symbol := t.Symbol()
	if symbol == nil {
		return false
	}
	for _, name := range names {
		if symbol == s.AmbientSymbol(name) {
			return true
		}
	}
	return false
}

// IsSetType ports isSetType (L139-144).
func IsSetType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		return symbolIsAmbient(s, t, "Set", "ReadonlySet", "WeakSet")
	}}
}

// IsMapType ports isMapType (L146-151).
func IsMapType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		return symbolIsAmbient(s, t, "Map", "ReadonlyMap", "WeakMap")
	}}
}

// IsSharedTableType reports whether t is the ambient SharedTable type. SharedTable
// uses Map-shaped iteration, but is not a native Map constructor or macro type.
func IsSharedTableType(s *State, t *checker.Type) bool {
	return symbolIsAmbient(s, t, "SharedTable")
}

// IsGeneratorType ports isGeneratorType (L153-155).
func IsGeneratorType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		return symbolIsAmbient(s, t, "Generator")
	}}
}

// IsIterableFunctionType ports isIterableFunctionType (L157-159).
func IsIterableFunctionType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		return symbolIsAmbient(s, t, "IterableFunction")
	}}
}

// IsIterableType ports isIterableType (L177-179).
func IsIterableType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		return symbolIsAmbient(s, t, "Iterable")
	}}
}

// getTypeArguments ports util/types.ts getTypeArguments (L256-258):
// `typeChecker.getTypeArguments(type as ts.TypeReference) ?? []`. tsgo's
// getTypeArguments asserts the type IS a reference, so the `?? []` fallback
// becomes an explicit reference-flag guard.
func getTypeArguments(s *State, t *checker.Type) []*checker.Type {
	if t.Flags()&checker.TypeFlagsObjectFlagsType == 0 ||
		t.ObjectFlags()&checker.ObjectFlagsReference == 0 {
		return nil
	}
	return s.Checker.GetTypeArguments(t)
}

// IsIterableFunctionLuaTupleType ports isIterableFunctionLuaTupleType
// (L167-175): an IterableFunction whose first type argument is a LuaTuple.
func IsIterableFunctionLuaTupleType(s *State) TypeCheck {
	return TypeCheck{check: func(t *checker.Type) bool {
		if !IsIterableFunctionType(s).Check(t) {
			return false
		}
		typeArguments := getTypeArguments(s, t)
		return len(typeArguments) > 0 && IsLuaTupleType(s).Check(typeArguments[0])
	}}
}

// IsRobloxType ports isRobloxType (L199-206): the type's symbol has a
// declaration inside the @rbxts/types package. Upstream resolves
// `<nodeModulesPath>/@rbxts/types` and tests path descendance; rotor matches
// the normalized path fragment.
func IsRobloxType(s *State) TypeCheck {
	_ = s // upstream signature; the path stand-in needs no state
	return TypeCheck{check: func(t *checker.Type) bool {
		symbol := t.Symbol()
		if symbol == nil {
			return false
		}
		for _, declaration := range symbol.Declarations {
			if sf := ast.GetSourceFileOfNode(declaration); sf != nil &&
				strings.Contains(sf.FileName(), "node_modules/@rbxts/types/") {
				return true
			}
		}
		return false
	}}
}

// ---------------------------------------------------------------------------
// type utilities (types.ts L210-258)
// ---------------------------------------------------------------------------

// WalkTypes ports walkTypes (L210-224): recurse into union/intersection
// members; non-union types follow their constraint when it exists AND differs
// from the type itself ("in template literal types, constraint === type and
// this causes infinite recursion"); leaves invoke the callback.
func WalkTypes(s *State, t *checker.Type, callback func(t *checker.Type)) {
	if t.Flags()&checker.TypeFlagsUnionOrIntersection != 0 {
		for _, member := range t.Types() {
			WalkTypes(s, member, callback)
		}
		return
	}
	// type.getConstraint() == Checker.GetBaseConstraintOfType (see header note)
	if constraint := s.Checker.GetBaseConstraintOfType(t); constraint != nil && constraint != t {
		WalkTypes(s, constraint, callback)
	} else {
		callback(t)
	}
}

// getFirstConstructSymbol ports getFirstConstructSymbol (L226-242): the
// symbol of the first construct-signature member found among the interface
// declarations of the expression type's symbol, or nil. transformNewExpression
// looks this up to dispatch constructor macros (the MacroManager registers
// the construct-signature symbols of the GLOBAL interfaces, so user-defined
// shadowing types won't collide).
func getFirstConstructSymbol(s *State, expression *ast.Node) *ast.Symbol {
	symbol := s.GetType(expression).Symbol()
	if symbol == nil {
		return nil
	}
	for _, declaration := range symbol.Declarations {
		if ast.IsInterfaceDeclaration(declaration) {
			for _, member := range declaration.AsInterfaceDeclaration().Members.Nodes {
				if ast.IsConstructSignatureDeclaration(member) {
					return member.Symbol()
				}
			}
		}
	}
	return nil
}

// GetFirstDefinedSymbol ports getFirstDefinedSymbol (L244-254):
// union/intersection -> first member with a non-undefined symbol; otherwise
// the type's own symbol (which may be nil).
func GetFirstDefinedSymbol(s *State, t *checker.Type) *ast.Symbol {
	if t.Flags()&checker.TypeFlagsUnionOrIntersection != 0 {
		for _, member := range t.Types() {
			if symbol := member.Symbol(); symbol != nil && !s.Checker.IsUndefinedSymbol(symbol) {
				return symbol
			}
		}
		return nil
	}
	return t.Symbol()
}
