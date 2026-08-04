package luau

import (
	"math"
	"sync/atomic"
)

// adopt mirrors upstream create(): set child's parent; clone first if it
// already has one. Callers must not pass typed-nil interfaces (use nil checks
// for optional fields before calling).
func adopt[T Node](parent Node, child T) T {
	if any(child) == nil {
		return child
	}
	if child.Parent() != nil {
		child = child.shallowClone().(T)
	}
	child.setParent(parent)
	return child
}

func adoptList[T Node](parent Node, list *List[T]) *List[T] {
	if list == nil {
		return nil
	}
	list.ForEachNode(func(ln *ListNode[T]) {
		if ln.Value.Parent() != nil {
			ln.Value = ln.Value.shallowClone().(T)
		}
		ln.Value.setParent(parent)
	})
	return list
}

// adoptNodeOrList handles union fields (WritableExpression | List, etc.).
func adoptNodeOrList(parent Node, v NodeOrList) NodeOrList {
	switch x := v.(type) {
	case nil:
		return nil
	case *List[Expression]:
		return adoptList(parent, x)
	case *List[WritableExpression]:
		return adoptList(parent, x)
	case *List[AnyIdentifier]:
		return adoptList(parent, x)
	case *List[Statement]:
		return adoptList(parent, x)
	case Node:
		return adopt(parent, x)
	default:
		panic("adoptNodeOrList: unsupported value")
	}
}

// ---- constructors (one per kind; each adopts its children) ----

func NewIdentifier(name string) *Identifier { return &Identifier{Name: name} }

var lastTempID atomic.Int64

func TempID(name string) *TemporaryIdentifier {
	return &TemporaryIdentifier{Name: name, ID: int(lastTempID.Add(1))}
}

func ExactTempID(name string) *TemporaryIdentifier {
	return &TemporaryIdentifier{Name: name, ID: int(lastTempID.Add(1)), ExactName: true}
}

func NewComputedIndex(expression IndexableExpression, index Expression) *ComputedIndexExpression {
	n := &ComputedIndexExpression{}
	n.Expression = adopt[IndexableExpression](n, expression)
	n.Index = adopt[Expression](n, index)
	return n
}

func NewPropertyAccess(expression IndexableExpression, name string) *PropertyAccessExpression {
	n := &PropertyAccessExpression{Name: name}
	n.Expression = adopt[IndexableExpression](n, expression)
	return n
}

func NewCall(expression IndexableExpression, args *List[Expression]) *CallExpression {
	n := &CallExpression{}
	n.Expression = adopt[IndexableExpression](n, expression)
	n.Args = adoptList(n, args)
	return n
}

func NewMethodCall(name string, expression IndexableExpression, args *List[Expression]) *MethodCallExpression {
	n := &MethodCallExpression{Name: name}
	n.Expression = adopt[IndexableExpression](n, expression)
	n.Args = adoptList(n, args)
	return n
}

func NewParenthesized(expression Expression) *ParenthesizedExpression {
	n := &ParenthesizedExpression{}
	n.Expression = adopt[Expression](n, expression)
	return n
}

func NewNone() *None              { return &None{} }
func Nil() *NilLiteral            { return &NilLiteral{} }
func NewVarArgs() *VarArgsLiteral { return &VarArgsLiteral{} }

func Bool(value bool) Expression {
	if value {
		return &TrueLiteral{}
	}
	return &FalseLiteral{}
}

func NewNumberLiteral(value string) *NumberLiteral { return &NumberLiteral{Value: value} }

// Num mirrors upstream luau.number(): negatives become unary minus.
func Num(value float64) Expression {
	if value >= 0 {
		return NewNumberLiteral(JSNumberString(value))
	}
	return NewUnary("-", Num(math.Abs(value)))
}

func Str(value string) *StringLiteral { return &StringLiteral{Value: value} }
func ID(name string) *Identifier      { return NewIdentifier(name) }

func NewComment(text string) *Comment { return &Comment{Text: text} }

func NewFunctionExpression(params *List[AnyIdentifier], hasDotDotDot bool, statements *List[Statement]) *FunctionExpression {
	n := &FunctionExpression{HasDotDotDot: hasDotDotDot}
	n.Parameters = adoptList(n, params)
	n.Statements = adoptList(n, statements)
	return n
}

func NewBinary(left Expression, op BinaryOperator, right Expression) *BinaryExpression {
	n := &BinaryExpression{Operator: op}
	n.Left = adopt[Expression](n, left)
	n.Right = adopt[Expression](n, right)
	return n
}

func NewUnary(op UnaryOperator, expression Expression) *UnaryExpression {
	n := &UnaryExpression{Operator: op}
	n.Expression = adopt[Expression](n, expression)
	return n
}

func NewIfExpression(condition, expression, alternative Expression) *IfExpression {
	n := &IfExpression{}
	n.Condition = adopt[Expression](n, condition)
	n.Expression = adopt[Expression](n, expression)
	n.Alternative = adopt[Expression](n, alternative)
	return n
}

func NewInterpolatedString(parts *List[Node]) *InterpolatedString {
	n := &InterpolatedString{}
	n.Parts = adoptList(n, parts)
	return n
}

func NewInterpolatedStringPart(text string) *InterpolatedStringPart {
	return &InterpolatedStringPart{Text: text}
}

func NewArray(members *List[Expression]) *Array {
	n := &Array{}
	n.Members = adoptList(n, members)
	return n
}

func NewSet(members *List[Expression]) *Set {
	n := &Set{}
	n.Members = adoptList(n, members)
	return n
}

func NewMapField(index, value Expression) *MapField {
	n := &MapField{}
	n.Index = adopt[Expression](n, index)
	n.Value = adopt[Expression](n, value)
	return n
}

func NewMap(fields *List[*MapField]) *Map {
	n := &Map{}
	n.Fields = adoptList(n, fields)
	return n
}

func NewMixedTable(fields *List[Node]) *MixedTable {
	n := &MixedTable{}
	n.Fields = adoptList(n, fields)
	return n
}

func NewAssignment(left NodeOrList, op AssignmentOperator, right NodeOrList) *Assignment {
	n := &Assignment{Operator: op}
	n.Left = adoptNodeOrList(n, left)
	n.Right = adoptNodeOrList(n, right)
	return n
}

func NewBreak() *BreakStatement       { return &BreakStatement{} }
func NewContinue() *ContinueStatement { return &ContinueStatement{} }

func NewCallStatement(expression Expression) *CallStatement {
	n := &CallStatement{}
	n.Expression = adopt[Expression](n, expression)
	return n
}

func NewDo(statements *List[Statement]) *DoStatement {
	n := &DoStatement{}
	n.Statements = adoptList(n, statements)
	return n
}

func NewWhile(condition Expression, statements *List[Statement]) *WhileStatement {
	n := &WhileStatement{}
	n.Condition = adopt[Expression](n, condition)
	n.Statements = adoptList(n, statements)
	return n
}

func NewRepeat(condition Expression, statements *List[Statement]) *RepeatStatement {
	n := &RepeatStatement{}
	n.Statements = adoptList(n, statements)
	n.Condition = adopt[Expression](n, condition)
	return n
}

func NewIf(condition Expression, statements *List[Statement], elseBody NodeOrList) *IfStatement {
	n := &IfStatement{}
	n.Condition = adopt[Expression](n, condition)
	n.Statements = adoptList(n, statements)
	n.ElseBody = adoptNodeOrList(n, normalizeElseBody(elseBody))
	return n
}

// normalizeElseBody: nil means "no else" — store an empty statement list,
// matching how upstream transform code constructs IfStatements.
func normalizeElseBody(v NodeOrList) NodeOrList {
	if v == nil {
		return NewList[Statement]()
	}
	return v
}

func NewNumericFor(id AnyIdentifier, start, end, step Expression, statements *List[Statement]) *NumericForStatement {
	n := &NumericForStatement{}
	n.ID = adopt[AnyIdentifier](n, id)
	n.Start = adopt[Expression](n, start)
	n.End = adopt[Expression](n, end)
	if step != nil {
		n.Step = adopt[Expression](n, step)
	}
	n.Statements = adoptList(n, statements)
	return n
}

func NewFor(ids *List[AnyIdentifier], expression Expression, statements *List[Statement]) *ForStatement {
	n := &ForStatement{}
	n.IDs = adoptList(n, ids)
	n.Expression = adopt[Expression](n, expression)
	n.Statements = adoptList(n, statements)
	return n
}

func NewFunctionDeclaration(localize bool, name Expression, params *List[AnyIdentifier], hasDotDotDot bool, statements *List[Statement]) *FunctionDeclaration {
	n := &FunctionDeclaration{Localize: localize, HasDotDotDot: hasDotDotDot}
	n.Name = adopt[Expression](n, name)
	n.Parameters = adoptList(n, params)
	n.Statements = adoptList(n, statements)
	return n
}

func NewMethodDeclaration(expression IndexableExpression, name string, params *List[AnyIdentifier], hasDotDotDot bool, statements *List[Statement]) *MethodDeclaration {
	n := &MethodDeclaration{Name: name, HasDotDotDot: hasDotDotDot}
	n.Expression = adopt[IndexableExpression](n, expression)
	n.Parameters = adoptList(n, params)
	n.Statements = adoptList(n, statements)
	return n
}

func NewVariableDeclaration(left NodeOrList, right NodeOrList) *VariableDeclaration {
	n := &VariableDeclaration{}
	n.Left = adoptNodeOrList(n, left)
	if right != nil {
		n.Right = adoptNodeOrList(n, right)
	}
	return n
}

func NewReturn(expression NodeOrList) *ReturnStatement {
	n := &ReturnStatement{}
	n.Expression = adoptNodeOrList(n, expression)
	return n
}
