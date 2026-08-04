package luau

// ---- indexable expressions ----

type Identifier struct {
	base
	Name string
}

type TemporaryIdentifier struct {
	base
	Name      string
	ID        int
	ExactName bool
}

type ComputedIndexExpression struct {
	base
	Expression IndexableExpression
	Index      Expression
}

type PropertyAccessExpression struct {
	base
	Expression IndexableExpression
	Name       string
}

type CallExpression struct {
	base
	Expression IndexableExpression
	Args       *List[Expression]
}

type MethodCallExpression struct {
	base
	Name       string
	Expression IndexableExpression
	Args       *List[Expression]
}

type ParenthesizedExpression struct {
	base
	Expression Expression
}

// ---- expressions ----

type None struct{ base }
type NilLiteral struct{ base }
type FalseLiteral struct{ base }
type TrueLiteral struct{ base }

type NumberLiteral struct {
	base
	Value string
}

type StringLiteral struct {
	base
	Value string
}

type VarArgsLiteral struct{ base }

type FunctionExpression struct {
	base
	Parameters   *List[AnyIdentifier]
	HasDotDotDot bool
	Statements   *List[Statement]
}

type BinaryExpression struct {
	base
	Left     Expression
	Operator BinaryOperator
	Right    Expression
}

type UnaryExpression struct {
	base
	Operator   UnaryOperator
	Expression Expression
}

type IfExpression struct {
	base
	Condition   Expression
	Expression  Expression
	Alternative Expression
}

type InterpolatedString struct {
	base
	Parts *List[Node] // InterpolatedStringPart | Expression
}

type Array struct {
	base
	Members *List[Expression]
}

type Map struct {
	base
	Fields *List[*MapField]
}

type Set struct {
	base
	Members *List[Expression]
}

type MixedTable struct {
	base
	Fields *List[Node] // MapField | Expression
}

// ---- statements ----

type Assignment struct {
	base
	Left     NodeOrList // WritableExpression | *List[WritableExpression]
	Operator AssignmentOperator
	Right    NodeOrList // Expression | *List[Expression]
}

type BreakStatement struct{ base }

type CallStatement struct {
	base
	Expression Expression // CallExpression | MethodCallExpression
}

type ContinueStatement struct{ base }

type DoStatement struct {
	base
	Statements *List[Statement]
}

type WhileStatement struct {
	base
	Condition  Expression
	Statements *List[Statement]
}

type RepeatStatement struct {
	base
	Condition  Expression
	Statements *List[Statement]
}

type IfStatement struct {
	base
	Condition  Expression
	Statements *List[Statement]
	ElseBody   NodeOrList // *IfStatement | *List[Statement]
}

type NumericForStatement struct {
	base
	ID         AnyIdentifier
	Start      Expression
	End        Expression
	Step       Expression // may be nil
	Statements *List[Statement]
}

type ForStatement struct {
	base
	IDs        *List[AnyIdentifier]
	Expression Expression
	Statements *List[Statement]
}

type FunctionDeclaration struct {
	base
	Localize     bool
	Name         Expression // AnyIdentifier | PropertyAccessExpression
	Parameters   *List[AnyIdentifier]
	HasDotDotDot bool
	Statements   *List[Statement]
}

type MethodDeclaration struct {
	base
	Expression   IndexableExpression
	Name         string
	Parameters   *List[AnyIdentifier]
	HasDotDotDot bool
	Statements   *List[Statement]
}

type VariableDeclaration struct {
	base
	Left  NodeOrList // AnyIdentifier | *List[AnyIdentifier]
	Right NodeOrList // Expression | *List[Expression] | nil
}

type ReturnStatement struct {
	base
	Expression NodeOrList // Expression | *List[Expression]
}

type Comment struct {
	base
	Text string
}

// ---- fields ----

type MapField struct {
	base
	Index Expression
	Value Expression
}

type InterpolatedStringPart struct {
	base
	Text string
}

// ---- Kind ----

func (*Identifier) Kind() SyntaxKind               { return KindIdentifier }
func (*TemporaryIdentifier) Kind() SyntaxKind      { return KindTemporaryIdentifier }
func (*ComputedIndexExpression) Kind() SyntaxKind  { return KindComputedIndexExpression }
func (*PropertyAccessExpression) Kind() SyntaxKind { return KindPropertyAccessExpression }
func (*CallExpression) Kind() SyntaxKind           { return KindCallExpression }
func (*MethodCallExpression) Kind() SyntaxKind     { return KindMethodCallExpression }
func (*ParenthesizedExpression) Kind() SyntaxKind  { return KindParenthesizedExpression }
func (*None) Kind() SyntaxKind                     { return KindNone }
func (*NilLiteral) Kind() SyntaxKind               { return KindNilLiteral }
func (*FalseLiteral) Kind() SyntaxKind             { return KindFalseLiteral }
func (*TrueLiteral) Kind() SyntaxKind              { return KindTrueLiteral }
func (*NumberLiteral) Kind() SyntaxKind            { return KindNumberLiteral }
func (*StringLiteral) Kind() SyntaxKind            { return KindStringLiteral }
func (*VarArgsLiteral) Kind() SyntaxKind           { return KindVarArgsLiteral }
func (*FunctionExpression) Kind() SyntaxKind       { return KindFunctionExpression }
func (*BinaryExpression) Kind() SyntaxKind         { return KindBinaryExpression }
func (*UnaryExpression) Kind() SyntaxKind          { return KindUnaryExpression }
func (*IfExpression) Kind() SyntaxKind             { return KindIfExpression }
func (*InterpolatedString) Kind() SyntaxKind       { return KindInterpolatedString }
func (*Array) Kind() SyntaxKind                    { return KindArray }
func (*Map) Kind() SyntaxKind                      { return KindMap }
func (*Set) Kind() SyntaxKind                      { return KindSet }
func (*MixedTable) Kind() SyntaxKind               { return KindMixedTable }
func (*Assignment) Kind() SyntaxKind               { return KindAssignment }
func (*BreakStatement) Kind() SyntaxKind           { return KindBreakStatement }
func (*CallStatement) Kind() SyntaxKind            { return KindCallStatement }
func (*ContinueStatement) Kind() SyntaxKind        { return KindContinueStatement }
func (*DoStatement) Kind() SyntaxKind              { return KindDoStatement }
func (*WhileStatement) Kind() SyntaxKind           { return KindWhileStatement }
func (*RepeatStatement) Kind() SyntaxKind          { return KindRepeatStatement }
func (*IfStatement) Kind() SyntaxKind              { return KindIfStatement }
func (*NumericForStatement) Kind() SyntaxKind      { return KindNumericForStatement }
func (*ForStatement) Kind() SyntaxKind             { return KindForStatement }
func (*FunctionDeclaration) Kind() SyntaxKind      { return KindFunctionDeclaration }
func (*MethodDeclaration) Kind() SyntaxKind        { return KindMethodDeclaration }
func (*VariableDeclaration) Kind() SyntaxKind      { return KindVariableDeclaration }
func (*ReturnStatement) Kind() SyntaxKind          { return KindReturnStatement }
func (*Comment) Kind() SyntaxKind                  { return KindComment }
func (*MapField) Kind() SyntaxKind                 { return KindMapField }
func (*InterpolatedStringPart) Kind() SyntaxKind   { return KindInterpolatedStringPart }

// ---- shallowClone ----

func (n *Identifier) shallowClone() Node               { c := *n; return &c }
func (n *TemporaryIdentifier) shallowClone() Node      { c := *n; return &c }
func (n *ComputedIndexExpression) shallowClone() Node  { c := *n; return &c }
func (n *PropertyAccessExpression) shallowClone() Node { c := *n; return &c }
func (n *CallExpression) shallowClone() Node           { c := *n; return &c }
func (n *MethodCallExpression) shallowClone() Node     { c := *n; return &c }
func (n *ParenthesizedExpression) shallowClone() Node  { c := *n; return &c }
func (n *None) shallowClone() Node                     { c := *n; return &c }
func (n *NilLiteral) shallowClone() Node               { c := *n; return &c }
func (n *FalseLiteral) shallowClone() Node             { c := *n; return &c }
func (n *TrueLiteral) shallowClone() Node              { c := *n; return &c }
func (n *NumberLiteral) shallowClone() Node            { c := *n; return &c }
func (n *StringLiteral) shallowClone() Node            { c := *n; return &c }
func (n *VarArgsLiteral) shallowClone() Node           { c := *n; return &c }
func (n *FunctionExpression) shallowClone() Node       { c := *n; return &c }
func (n *BinaryExpression) shallowClone() Node         { c := *n; return &c }
func (n *UnaryExpression) shallowClone() Node          { c := *n; return &c }
func (n *IfExpression) shallowClone() Node             { c := *n; return &c }
func (n *InterpolatedString) shallowClone() Node       { c := *n; return &c }
func (n *Array) shallowClone() Node                    { c := *n; return &c }
func (n *Map) shallowClone() Node                      { c := *n; return &c }
func (n *Set) shallowClone() Node                      { c := *n; return &c }
func (n *MixedTable) shallowClone() Node               { c := *n; return &c }
func (n *Assignment) shallowClone() Node               { c := *n; return &c }
func (n *BreakStatement) shallowClone() Node           { c := *n; return &c }
func (n *CallStatement) shallowClone() Node            { c := *n; return &c }
func (n *ContinueStatement) shallowClone() Node        { c := *n; return &c }
func (n *DoStatement) shallowClone() Node              { c := *n; return &c }
func (n *WhileStatement) shallowClone() Node           { c := *n; return &c }
func (n *RepeatStatement) shallowClone() Node          { c := *n; return &c }
func (n *IfStatement) shallowClone() Node              { c := *n; return &c }
func (n *NumericForStatement) shallowClone() Node      { c := *n; return &c }
func (n *ForStatement) shallowClone() Node             { c := *n; return &c }
func (n *FunctionDeclaration) shallowClone() Node      { c := *n; return &c }
func (n *MethodDeclaration) shallowClone() Node        { c := *n; return &c }
func (n *VariableDeclaration) shallowClone() Node      { c := *n; return &c }
func (n *ReturnStatement) shallowClone() Node          { c := *n; return &c }
func (n *Comment) shallowClone() Node                  { c := *n; return &c }
func (n *MapField) shallowClone() Node                 { c := *n; return &c }
func (n *InterpolatedStringPart) shallowClone() Node   { c := *n; return &c }

// ---- category markers ----

func (*Identifier) expressionNode()               {}
func (*TemporaryIdentifier) expressionNode()      {}
func (*ComputedIndexExpression) expressionNode()  {}
func (*PropertyAccessExpression) expressionNode() {}
func (*CallExpression) expressionNode()           {}
func (*MethodCallExpression) expressionNode()     {}
func (*ParenthesizedExpression) expressionNode()  {}
func (*None) expressionNode()                     {}
func (*NilLiteral) expressionNode()               {}
func (*FalseLiteral) expressionNode()             {}
func (*TrueLiteral) expressionNode()              {}
func (*NumberLiteral) expressionNode()            {}
func (*StringLiteral) expressionNode()            {}
func (*VarArgsLiteral) expressionNode()           {}
func (*FunctionExpression) expressionNode()       {}
func (*BinaryExpression) expressionNode()         {}
func (*UnaryExpression) expressionNode()          {}
func (*IfExpression) expressionNode()             {}
func (*InterpolatedString) expressionNode()       {}
func (*Array) expressionNode()                    {}
func (*Map) expressionNode()                      {}
func (*Set) expressionNode()                      {}
func (*MixedTable) expressionNode()               {}

func (*Identifier) indexableNode()               {}
func (*TemporaryIdentifier) indexableNode()      {}
func (*ComputedIndexExpression) indexableNode()  {}
func (*PropertyAccessExpression) indexableNode() {}
func (*CallExpression) indexableNode()           {}
func (*MethodCallExpression) indexableNode()     {}
func (*ParenthesizedExpression) indexableNode()  {}

func (*Assignment) statementNode()          {}
func (*BreakStatement) statementNode()      {}
func (*CallStatement) statementNode()       {}
func (*ContinueStatement) statementNode()   {}
func (*DoStatement) statementNode()         {}
func (*WhileStatement) statementNode()      {}
func (*RepeatStatement) statementNode()     {}
func (*IfStatement) statementNode()         {}
func (*NumericForStatement) statementNode() {}
func (*ForStatement) statementNode()        {}
func (*FunctionDeclaration) statementNode() {}
func (*MethodDeclaration) statementNode()   {}
func (*VariableDeclaration) statementNode() {}
func (*ReturnStatement) statementNode()     {}
func (*Comment) statementNode()             {}

func (*MapField) fieldNode()               {}
func (*InterpolatedStringPart) fieldNode() {}

func (*Identifier) anyIdentifierNode()          {}
func (*TemporaryIdentifier) anyIdentifierNode() {}

func (*Identifier) writableNode()               {}
func (*TemporaryIdentifier) writableNode()      {}
func (*PropertyAccessExpression) writableNode() {}
func (*ComputedIndexExpression) writableNode()  {}

// ---- HasParameters ----

func (n *FunctionExpression) ParamData() (*List[AnyIdentifier], bool) {
	return n.Parameters, n.HasDotDotDot
}

func (n *FunctionDeclaration) ParamData() (*List[AnyIdentifier], bool) {
	return n.Parameters, n.HasDotDotDot
}

func (n *MethodDeclaration) ParamData() (*List[AnyIdentifier], bool) {
	return n.Parameters, n.HasDotDotDot
}
