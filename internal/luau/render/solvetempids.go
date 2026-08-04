package render

import (
	"strconv"

	"rotor/internal/luau"
)

// tempScope mirrors upstream solveTempIds.ts Scope.
type tempScope struct {
	ids     map[string]struct{}
	lastTry map[string]int
	parent  *tempScope
}

// newTempScope leaves both maps nil; they are allocated on first write
// (reads of nil maps are safe), since most scopes never register an id.
func newTempScope(parent *tempScope) *tempScope {
	return &tempScope{parent: parent}
}

func (s *tempScope) addID(id string) {
	if s.ids == nil {
		s.ids = make(map[string]struct{})
	}
	s.ids[id] = struct{}{}
}

func (s *tempScope) setLastTry(input string, i int) {
	if s.lastTry == nil {
		s.lastTry = make(map[string]int)
	}
	s.lastTry[input] = i
}

// has mirrors upstream scopeHasId: checks this scope and all ancestors.
func (s *tempScope) has(id string) bool {
	if _, ok := s.ids[id]; ok {
		return true
	}
	if s.parent != nil {
		return s.parent.has(id)
	}
	return false
}

func isFullyScopedNode(node luau.Node) bool {
	switch node.Kind() {
	case luau.KindForStatement, luau.KindNumericForStatement:
		return true
	}
	return luau.IsFunctionLike(node)
}

func identifierListContains(identifiers *luau.List[luau.AnyIdentifier], target *luau.Identifier) bool {
	if identifiers == nil {
		return false
	}
	for current := identifiers.Head; current != nil; current = current.Next {
		if current.Value == target {
			return true
		}
	}
	return false
}

func isIdentifierDeclaration(identifier *luau.Identifier) bool {
	switch parent := identifier.Parent().(type) {
	case *luau.VariableDeclaration:
		switch left := parent.Left.(type) {
		case *luau.Identifier:
			return left == identifier
		case *luau.List[luau.AnyIdentifier]:
			return identifierListContains(left, identifier)
		}
	case *luau.FunctionDeclaration:
		return parent.Localize && parent.Name == identifier ||
			identifierListContains(parent.Parameters, identifier)
	case *luau.FunctionExpression:
		return identifierListContains(parent.Parameters, identifier)
	case *luau.MethodDeclaration:
		return identifierListContains(parent.Parameters, identifier)
	case *luau.ForStatement:
		return identifierListContains(parent.IDs, identifier)
	case *luau.NumericForStatement:
		return parent.ID == identifier
	}
	return false
}

// isScopeEdge mirrors upstream: node is the head (head=true) or tail
// (head=false) statement of its parent's statements list, or of an
// IfStatement's else-list.
func isScopeEdge(node luau.Node, head bool) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	edgeOf := func(l *luau.List[luau.Statement]) luau.Statement {
		if head {
			if l.Head != nil {
				return l.Head.Value
			}
		} else {
			if l.Tail != nil {
				return l.Tail.Value
			}
		}
		return nil
	}
	if luau.HasStatements(parent) {
		if stmts := luau.StatementsOf(parent); stmts != nil {
			if edge := edgeOf(stmts); edge != nil && luau.Node(edge) == node {
				return true
			}
		}
	}
	// non-list elseBody would have the elseBody itself as a parent,
	// which would be a luau.IfStatement and handled above
	if ifStmt, ok := parent.(*luau.IfStatement); ok {
		if elseList, ok := ifStmt.ElseBody.(*luau.List[luau.Statement]); ok {
			if edge := edgeOf(elseList); edge != nil && luau.Node(edge) == node {
				return true
			}
		}
	}
	return false
}

// solveTempIDs ports reference/luau-ast/src/LuauRenderer/solveTempIds.ts:
// assigns rendered names to TemporaryIdentifier nodes, scope-aware so they
// never collide with source identifiers or other temps.
func solveTempIDs(state *RenderState, ast luau.NodeOrList) {
	var tempIDsToProcess []*luau.TemporaryIdentifier
	nodesToScopes := map[*luau.TemporaryIdentifier]*tempScope{}

	scopeStack := []*tempScope{newTempScope(nil)}
	peek := func() *tempScope { return scopeStack[len(scopeStack)-1] }
	push := func() { scopeStack = append(scopeStack, newTempScope(peek())) }
	pop := func() { scopeStack = scopeStack[:len(scopeStack)-1] }
	registerID := func(name string) { peek().addID(name) }
	reserveIdentifierUse := func(name string) {
		for scope := peek(); scope != nil; scope = scope.parent {
			scope.addID(name)
		}
	}

	vis := &visitor{
		before: func(node luau.Node) {
			if declaration, ok := node.(*luau.FunctionDeclaration); ok && declaration.Localize {
				if identifier, ok := declaration.Name.(*luau.Identifier); ok {
					registerID(identifier.Name)
				}
			}
			if isFullyScopedNode(node) {
				push()
			}
			if isScopeEdge(node, true) {
				push()
			}
			switch n := node.(type) {
			case *luau.TemporaryIdentifier:
				nodesToScopes[n] = peek()
				tempIDsToProcess = append(tempIDsToProcess, n)
			case *luau.Identifier:
				if isIdentifierDeclaration(n) {
					registerID(n.Name)
				} else {
					reserveIdentifierUse(n.Name)
				}
			case *luau.VariableDeclaration:
				switch l := n.Left.(type) {
				case *luau.List[luau.AnyIdentifier]:
					l.ForEach(func(id luau.AnyIdentifier) {
						if ident, ok := id.(*luau.Identifier); ok {
							registerID(ident.Name)
						}
					})
				case *luau.Identifier:
					registerID(l.Name)
				}
			default:
				if luau.IsFunctionLike(node) {
					params, _ := node.(luau.HasParameters).ParamData()
					params.ForEach(func(id luau.AnyIdentifier) {
						if ident, ok := id.(*luau.Identifier); ok {
							registerID(ident.Name)
						}
					})
				}
			}
		},
		after: func(node luau.Node) {
			if isFullyScopedNode(node) {
				pop()
			}
			if isScopeEdge(node, false) {
				pop()
			}
		},
	}
	visitNodeOrList(ast, vis)

	for _, tempID := range tempIDsToProcess {
		if _, done := state.seenTempNodes[tempID.ID]; done {
			continue
		}
		scope := nodesToScopes[tempID]
		if tempID.ExactName {
			input := tempID.Name
			i, ok := scope.lastTry[input]
			if !ok {
				i = 1
			}
			for scope.has(input) {
				input = tempID.Name + "_" + strconv.Itoa(i)
				i++
			}
			scope.setLastTry(input, i)
			scope.addID(input)
			state.seenTempNodes[tempID.ID] = input
			continue
		}
		separator := "_"
		if tempID.Name == "" {
			separator = ""
		}
		// NOTE (upstream parity): lastTry is read with the pre-loop input
		// ("_" + name) but written with the post-loop (final) input and the
		// post-loop value of i — replicating upstream exactly.
		input := "_" + tempID.Name
		i, ok := scope.lastTry[input]
		if !ok {
			i = 1
		}
		for scope.has(input) {
			input = "_" + tempID.Name + separator + strconv.Itoa(i)
			i++
		}
		scope.setLastTry(input, i)
		scope.addID(input)
		state.seenTempNodes[tempID.ID] = input
	}
}
