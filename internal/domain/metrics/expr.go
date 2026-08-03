package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// The restricted expression language for user-defined metrics
// (docs/07-metrics-and-budgets.md §7.7).
//
// It is deliberately not a general formula language. There are no cell
// references, no loops, no I/O and nothing is ever evaluated as code: an
// expression is parsed to an AST and walked against pre-computed values. That is
// the difference between a metric a user can define and a spreadsheet nobody can
// trust.
//
// Division by zero yields nil — never an error, never infinity. A metric that
// blows up on an empty month would be useless in exactly the months worth
// looking at.

// NodeKind is the sort of an AST node.
type NodeKind string

// The node kinds.
const (
	NodeNumber NodeKind = "number"
	NodeIdent  NodeKind = "identifier"
	NodeBinary NodeKind = "binary"
	NodeUnary  NodeKind = "unary"
	NodeCall   NodeKind = "call"
)

// Node is one node of a parsed expression.
type Node struct {
	Kind NodeKind
	// Number holds a literal.
	Number float64
	// Name is the identifier or function name.
	Name string
	// Arg is the identifier's argument, e.g. spend('Food').
	Arg string
	// Op is the operator for binary and unary nodes.
	Op       string
	Children []*Node
}

// Functions is the whole function set. Anything else is rejected by name, so a
// typo is a message rather than a silent zero.
var Functions = map[string]int{
	"min":   -1, // variadic, at least one argument
	"max":   -1,
	"abs":   1,
	"mean":  -1,
	"count": -1,
}

// References are the identifiers that may take a name argument, as in
// `spend('Food')`. The set is closed: without it, any word followed by a bracket
// and a string would parse, and `exec('rm -rf /')` would be a well-formed
// expression waiting on a resolver to refuse it. It is refused here instead.
var References = map[string]bool{
	"spend":   true,
	"planned": true,
	"average": true,
	"total":   true,
	"value":   true,
}

// Resolver supplies the value of an identifier. A nil result means "not
// recorded", which propagates through the expression rather than becoming zero.
type Resolver func(name, arg string) (*float64, error)

// ParseError names what is wrong and where, so the settings screen can say so
// without the user reading a stack trace.
type ParseError struct {
	Message  string
	Position int
}

// Error implements error.
func (e *ParseError) Error() string {
	return fmt.Sprintf("%s (at character %d)", e.Message, e.Position+1)
}

// Parse builds the AST for an expression.
func Parse(expression string) (*Node, error) {
	tokens, err := tokenize(expression)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	node, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if !p.done() {
		return nil, &ParseError{Message: "unexpected " + p.peek().text, Position: p.peek().pos}
	}
	return node, nil
}

// Validate parses and checks every identifier against the registry without
// evaluating anything. It is what POST /metrics/validate answers with.
func Validate(expression string, known func(name string) bool) error {
	node, err := Parse(expression)
	if err != nil {
		return err
	}
	return walk(node, known)
}

func walk(n *Node, known func(string) bool) error {
	if n == nil {
		return nil
	}
	if n.Kind == NodeIdent && known != nil && !known(n.Name) {
		return &ParseError{Message: fmt.Sprintf("unknown identifier %q", n.Name)}
	}
	for _, child := range n.Children {
		if err := walk(child, known); err != nil {
			return err
		}
	}
	return nil
}

// Eval walks the AST against a resolver.
//
// A nil result anywhere propagates: an unrecorded input makes the whole metric
// unrecorded, which is the same rule every other figure in this system follows.
func Eval(n *Node, resolve Resolver) (*float64, error) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case NodeNumber:
		value := n.Number
		return &value, nil

	case NodeIdent:
		if resolve == nil {
			return nil, &ParseError{Message: fmt.Sprintf("no value for %q", n.Name)}
		}
		return resolve(n.Name, n.Arg)

	case NodeUnary:
		operand, err := Eval(n.Children[0], resolve)
		if err != nil || operand == nil {
			return nil, err
		}
		out := -*operand
		return &out, nil

	case NodeBinary:
		left, err := Eval(n.Children[0], resolve)
		if err != nil {
			return nil, err
		}
		right, err := Eval(n.Children[1], resolve)
		if err != nil {
			return nil, err
		}
		if left == nil || right == nil {
			return nil, nil
		}
		return applyBinary(n.Op, *left, *right)

	case NodeCall:
		return evalCall(n, resolve)
	}
	return nil, &ParseError{Message: "unsupported expression"}
}

func applyBinary(op string, left, right float64) (*float64, error) {
	var out float64
	switch op {
	case "+":
		out = left + right
	case "-":
		out = left - right
	case "*":
		out = left * right
	case "/":
		if right == 0 {
			// Not an error, not infinity: nothing is known about the ratio.
			return nil, nil
		}
		out = left / right
	case ">":
		out = boolean(left > right)
	case "<":
		out = boolean(left < right)
	case ">=":
		out = boolean(left >= right)
	case "<=":
		out = boolean(left <= right)
	case "==":
		out = boolean(left == right)
	case "!=":
		out = boolean(left != right)
	default:
		return nil, &ParseError{Message: "unsupported operator " + op}
	}
	return &out, nil
}

func evalCall(n *Node, resolve Resolver) (*float64, error) {
	values := make([]float64, 0, len(n.Children))
	for _, child := range n.Children {
		v, err := Eval(child, resolve)
		if err != nil {
			return nil, err
		}
		if v == nil {
			// count ignores absent inputs; every other function is undefined
			// without them.
			if n.Name == "count" {
				continue
			}
			return nil, nil
		}
		values = append(values, *v)
	}

	switch n.Name {
	case "abs":
		out := math.Abs(values[0])
		return &out, nil
	case "min", "max":
		if len(values) == 0 {
			return nil, nil
		}
		out := values[0]
		for _, v := range values[1:] {
			if (n.Name == "min") == (v < out) {
				out = v
			}
		}
		return &out, nil
	case "mean":
		if len(values) == 0 {
			return nil, nil
		}
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		out := sum / float64(len(values))
		return &out, nil
	case "count":
		// Counts the truthy arguments, which is what makes
		// `count(saved_percent > 0.2)` mean what it reads as.
		out := 0.0
		for _, v := range values {
			if v != 0 {
				out++
			}
		}
		return &out, nil
	}
	return nil, &ParseError{Message: "unknown function " + n.Name}
}

func boolean(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// --- lexer ---

type tokenKind int

const (
	tokenNumber tokenKind = iota
	tokenIdent
	tokenString
	tokenOperator
	tokenLParen
	tokenRParen
	tokenComma
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

func tokenize(input string) ([]token, error) {
	var out []token
	runes := []rune(input)
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case unicode.IsSpace(c):
			i++

		case unicode.IsDigit(c) || c == '.':
			start := i
			for i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
				i++
			}
			text := string(runes[start:i])
			if _, err := strconv.ParseFloat(text, 64); err != nil {
				return nil, &ParseError{Message: fmt.Sprintf("%q is not a number", text), Position: start}
			}
			out = append(out, token{kind: tokenNumber, text: text, pos: start})

		case unicode.IsLetter(c) || c == '_':
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				i++
			}
			out = append(out, token{kind: tokenIdent, text: string(runes[start:i]), pos: start})

		case c == '\'' || c == '"':
			quote := c
			start := i
			i++
			for i < len(runes) && runes[i] != quote {
				i++
			}
			if i >= len(runes) {
				return nil, &ParseError{Message: "unterminated string", Position: start}
			}
			out = append(out, token{kind: tokenString, text: string(runes[start+1 : i]), pos: start})
			i++

		case c == '(':
			out = append(out, token{kind: tokenLParen, text: "(", pos: i})
			i++
		case c == ')':
			out = append(out, token{kind: tokenRParen, text: ")", pos: i})
			i++
		case c == ',':
			out = append(out, token{kind: tokenComma, text: ",", pos: i})
			i++

		default:
			// Two-character operators first, so `>=` is not read as `>` then `=`.
			if i+1 < len(runes) {
				pair := string(runes[i : i+2])
				switch pair {
				case ">=", "<=", "==", "!=":
					out = append(out, token{kind: tokenOperator, text: pair, pos: i})
					i += 2
					continue
				}
			}
			switch c {
			case '+', '-', '*', '/', '>', '<':
				out = append(out, token{kind: tokenOperator, text: string(c), pos: i})
				i++
			case '×':
				out = append(out, token{kind: tokenOperator, text: "*", pos: i})
				i++
			case '÷':
				out = append(out, token{kind: tokenOperator, text: "/", pos: i})
				i++
			default:
				return nil, &ParseError{
					Message: fmt.Sprintf("%q is not allowed in a metric", string(c)), Position: i,
				}
			}
		}
	}
	return out, nil
}

// --- parser ---

type parser struct {
	tokens []token
	at     int
}

func (p *parser) done() bool { return p.at >= len(p.tokens) }
func (p *parser) peek() token {
	if p.done() {
		return token{text: "end of expression", pos: len(p.tokens)}
	}
	return p.tokens[p.at]
}
func (p *parser) next() token { t := p.peek(); p.at++; return t }

// parseExpression handles comparison, which binds loosest.
func (p *parser) parseExpression() (*Node, error) {
	left, err := p.parseSum()
	if err != nil {
		return nil, err
	}
	for !p.done() && p.peek().kind == tokenOperator && isComparison(p.peek().text) {
		op := p.next().text
		right, err := p.parseSum()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: NodeBinary, Op: op, Children: []*Node{left, right}}
	}
	return left, nil
}

func (p *parser) parseSum() (*Node, error) {
	left, err := p.parseProduct()
	if err != nil {
		return nil, err
	}
	for !p.done() && p.peek().kind == tokenOperator && (p.peek().text == "+" || p.peek().text == "-") {
		op := p.next().text
		right, err := p.parseProduct()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: NodeBinary, Op: op, Children: []*Node{left, right}}
	}
	return left, nil
}

func (p *parser) parseProduct() (*Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for !p.done() && p.peek().kind == tokenOperator && (p.peek().text == "*" || p.peek().text == "/") {
		op := p.next().text
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: NodeBinary, Op: op, Children: []*Node{left, right}}
	}
	return left, nil
}

func (p *parser) parseUnary() (*Node, error) {
	if !p.done() && p.peek().kind == tokenOperator && p.peek().text == "-" {
		p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Node{Kind: NodeUnary, Op: "-", Children: []*Node{operand}}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (*Node, error) {
	if p.done() {
		return nil, &ParseError{Message: "the expression ends too early", Position: p.peek().pos}
	}
	t := p.next()
	switch t.kind {
	case tokenNumber:
		value, _ := strconv.ParseFloat(t.text, 64)
		return &Node{Kind: NodeNumber, Number: value}, nil

	case tokenLParen:
		inner, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if p.done() || p.peek().kind != tokenRParen {
			return nil, &ParseError{Message: "missing )", Position: t.pos}
		}
		p.next()
		return inner, nil

	case tokenIdent:
		if p.done() || p.peek().kind != tokenLParen {
			return &Node{Kind: NodeIdent, Name: t.text}, nil
		}
		return p.parseCallOrRef(t)

	case tokenString:
		return nil, &ParseError{Message: "a bare string is not a value", Position: t.pos}
	}
	return nil, &ParseError{Message: "unexpected " + t.text, Position: t.pos}
}

// parseCallOrRef handles both `min(a, b)` and `spend('Food')`. The second is an
// identifier with an argument, not a function call — which is why an arbitrary
// name followed by a bracket is rejected rather than treated as a call.
func (p *parser) parseCallOrRef(name token) (*Node, error) {
	p.next() // consume '('

	// spend('Food'): exactly one string argument, and the name must be a
	// registered reference rather than any identifier.
	if !p.done() && p.peek().kind == tokenString {
		if !References[name.text] {
			return nil, &ParseError{
				Message: fmt.Sprintf(
					"%q does not take a name; only %s do", name.text, strings.Join(referenceNames(), ", ")),
				Position: name.pos,
			}
		}
		arg := p.next()
		if p.done() || p.peek().kind != tokenRParen {
			return nil, &ParseError{Message: "missing ) after " + name.text, Position: name.pos}
		}
		p.next()
		return &Node{Kind: NodeIdent, Name: name.text, Arg: arg.text}, nil
	}

	arity, ok := Functions[name.text]
	if !ok {
		return nil, &ParseError{
			Message: fmt.Sprintf("%q is not one of the allowed functions (%s)",
				name.text, strings.Join(functionNames(), ", ")),
			Position: name.pos,
		}
	}

	node := &Node{Kind: NodeCall, Name: name.text}
	if !p.done() && p.peek().kind == tokenRParen {
		p.next()
	} else {
		for {
			arg, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, arg)
			if p.done() {
				return nil, &ParseError{Message: "missing ) after " + name.text, Position: name.pos}
			}
			if p.peek().kind == tokenComma {
				p.next()
				continue
			}
			if p.peek().kind == tokenRParen {
				p.next()
				break
			}
			return nil, &ParseError{Message: "expected , or ) in " + name.text, Position: p.peek().pos}
		}
	}

	if arity >= 0 && len(node.Children) != arity {
		return nil, &ParseError{
			Message:  fmt.Sprintf("%s takes %d argument(s), got %d", name.text, arity, len(node.Children)),
			Position: name.pos,
		}
	}
	if arity < 0 && len(node.Children) == 0 {
		return nil, &ParseError{Message: name.text + " needs at least one argument", Position: name.pos}
	}
	return node, nil
}

func isComparison(op string) bool {
	switch op {
	case ">", "<", ">=", "<=", "==", "!=":
		return true
	}
	return false
}

func referenceNames() []string {
	out := make([]string, 0, len(References))
	for name := range References {
		out = append(out, name)
	}
	return sorted(out)
}

func functionNames() []string {
	out := make([]string, 0, len(Functions))
	for name := range Functions {
		out = append(out, name)
	}
	return sorted(out)
}

// sorted keeps an error message stable: one that reorders itself between runs is
// a diff nobody wants to read.
func sorted(in []string) []string {
	for i := 0; i < len(in); i++ {
		for j := i + 1; j < len(in); j++ {
			if in[j] < in[i] {
				in[i], in[j] = in[j], in[i]
			}
		}
	}
	return in
}
