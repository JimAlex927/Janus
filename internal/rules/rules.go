// Package rules parses and evaluates the small request-matching language used
// by the router. Rules are compiled when a configuration generation is built;
// request handling only evaluates the immutable expression tree.
package rules

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const (
	MaxExpressionBytes = 16 << 10
	MaxExpressionDepth = 64
	MaxRuleArguments   = 32
)

// Facts contains the request attributes that a rule may inspect. The router
// creates one value per request and normalizes the expensive pieces once.
type Facts struct {
	Host     string
	Path     string
	Method   string
	Protocol string
	Header   http.Header
	Query    url.Values
}

// Predicate is a leaf rule. Custom rules can be added to a Registry without
// changing the parser or the router.
type Predicate interface {
	Match(*Facts) bool
}

// IndexedPredicate is optional. A custom predicate that can prove a Host or
// PathPrefix candidate may expose a hint; predicates without one remain safe
// in the fallback candidate set.
type IndexedPredicate interface {
	Predicate
	IndexHints() IndexHints
}

type Compiler func(args []string) (Predicate, error)

type Registry struct {
	compilers map[string]Compiler
}

func NewRegistry() *Registry {
	r := &Registry{compilers: make(map[string]Compiler)}
	r.compilers["Host"] = compileHost
	r.compilers["Path"] = compilePath
	r.compilers["PathPrefix"] = compilePathPrefix
	r.compilers["Method"] = compileMethod
	r.compilers["Header"] = compileHeader
	r.compilers["Query"] = compileQuery
	r.compilers["Protocol"] = compileProtocol
	return r
}

func (r *Registry) Register(name string, compiler Compiler) error {
	if r == nil || compiler == nil || !validName(name) {
		return fmt.Errorf("invalid rule registration %q", name)
	}
	if r.compilers == nil {
		r.compilers = make(map[string]Compiler)
	}
	if _, exists := r.compilers[name]; exists {
		return fmt.Errorf("rule %q is already registered", name)
	}
	r.compilers[name] = compiler
	return nil
}

// Matcher is safe to share between concurrent requests.
type Matcher struct {
	root node
}

// IndexHints describes conditions that are safe to use as a candidate index.
// A false Safe value means that the expression contains OR/NOT and must stay
// in the fallback set unless a more advanced matcher planner is added.
type IndexHints struct {
	Safe       bool
	Host       string
	PathPrefix string
}

func (r *Registry) Compile(expression string) (*Matcher, error) {
	if r == nil {
		r = DefaultRegistry()
	}
	if strings.TrimSpace(expression) == "" {
		return nil, fmt.Errorf("match expression is empty")
	}
	if len(expression) > MaxExpressionBytes {
		return nil, fmt.Errorf("match expression exceeds %d bytes", MaxExpressionBytes)
	}
	p := parser{lexer: newLexer(expression), registry: r}
	root, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if token := p.take(); token.kind != tokenEOF {
		return nil, fmt.Errorf("unexpected token %q at position %d", token.text, token.pos)
	}
	return &Matcher{root: root}, nil
}

func DefaultRegistry() *Registry { return NewRegistry() }

func (m *Matcher) Match(facts *Facts) bool {
	return m != nil && m.root != nil && facts != nil && m.root.match(facts)
}

func (m *Matcher) IndexHints() IndexHints {
	if m == nil || m.root == nil {
		return IndexHints{}
	}
	return indexHints(m.root)
}

type node interface {
	match(*Facts) bool
}

type predicateNode struct{ predicate Predicate }

func (n predicateNode) match(f *Facts) bool { return n.predicate.Match(f) }

type andNode struct{ left, right node }

func (n andNode) match(f *Facts) bool { return n.left.match(f) && n.right.match(f) }

type orNode struct{ left, right node }

func (n orNode) match(f *Facts) bool { return n.left.match(f) || n.right.match(f) }

type notNode struct{ child node }

func (n notNode) match(f *Facts) bool { return !n.child.match(f) }

func indexHints(n node) IndexHints {
	switch value := n.(type) {
	case predicateNode:
		if indexed, ok := value.predicate.(IndexedPredicate); ok {
			return indexed.IndexHints()
		}
		switch predicate := value.predicate.(type) {
		case hostPredicate:
			if len(predicate) == 1 {
				return IndexHints{Safe: true, Host: predicate[0]}
			}
		case pathPredicate:
			return IndexHints{Safe: true, PathPrefix: string(predicate)}
		case pathPrefixPredicate:
			return IndexHints{Safe: true, PathPrefix: string(predicate)}
		default:
			return IndexHints{Safe: true}
		}
	case andNode:
		left, right := indexHints(value.left), indexHints(value.right)
		if !left.Safe || !right.Safe {
			return IndexHints{}
		}
		if left.Host != "" && right.Host != "" && left.Host != right.Host {
			return IndexHints{}
		}
		if left.PathPrefix != "" && right.PathPrefix != "" && left.PathPrefix != right.PathPrefix {
			return IndexHints{}
		}
		if left.Host == "" {
			left.Host = right.Host
		}
		if left.PathPrefix == "" {
			left.PathPrefix = right.PathPrefix
		}
		return left
	case orNode, notNode:
		return IndexHints{}
	}
	return IndexHints{}
}

type hostPredicate []string

func (p hostPredicate) Match(f *Facts) bool {
	for _, host := range p {
		if f.Host == host {
			return true
		}
	}
	return false
}

type pathPredicate string

func (p pathPredicate) Match(f *Facts) bool { return f.Path == string(p) }

type pathPrefixPredicate string

func (p pathPrefixPredicate) Match(f *Facts) bool {
	prefix := string(p)
	return prefix == "/" || f.Path == prefix || strings.HasPrefix(f.Path, prefix+"/")
}

type methodPredicate map[string]struct{}

func (p methodPredicate) Match(f *Facts) bool {
	_, ok := p[strings.ToUpper(f.Method)]
	return ok
}

type headerPredicate struct {
	name, value string
	presentOnly bool
}

func (p headerPredicate) Match(f *Facts) bool {
	values, ok := f.Header[http.CanonicalHeaderKey(p.name)]
	if !ok {
		return false
	}
	if p.presentOnly {
		return true
	}
	for _, value := range values {
		if value == p.value {
			return true
		}
	}
	return false
}

type queryPredicate struct {
	name, value string
	presentOnly bool
}

func (p queryPredicate) Match(f *Facts) bool {
	values, ok := f.Query[p.name]
	if !ok {
		return false
	}
	if p.presentOnly {
		return true
	}
	for _, value := range values {
		if value == p.value {
			return true
		}
	}
	return false
}

type protocolPredicate map[string]struct{}

func (p protocolPredicate) Match(f *Facts) bool {
	_, ok := p[f.Protocol]
	return ok
}

func compileHost(args []string) (Predicate, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("Host requires at least one argument")
	}
	result := make(hostPredicate, len(args))
	for i, arg := range args {
		host := strings.ToLower(strings.TrimSuffix(arg, "."))
		if host == "" || strings.ContainsAny(host, ":/*?#@\\ \t\r\n") {
			return nil, fmt.Errorf("Host argument %q is not an exact hostname", arg)
		}
		result[i] = host
	}
	return result, nil
}

func compilePath(args []string) (Predicate, error) {
	arg, err := onePathArg("Path", args)
	if err != nil {
		return nil, err
	}
	return pathPredicate(arg), nil
}

func compilePathPrefix(args []string) (Predicate, error) {
	arg, err := onePathArg("PathPrefix", args)
	if err != nil {
		return nil, err
	}
	return pathPrefixPredicate(arg), nil
}

func onePathArg(name string, args []string) (string, error) {
	if len(args) != 1 || args[0] == "" || !strings.HasPrefix(args[0], "/") || strings.ContainsAny(args[0], "?#%\\ \t\r\n") {
		return "", fmt.Errorf("%s requires one unescaped absolute path argument", name)
	}
	if args[0] != "/" && strings.HasSuffix(args[0], "/") {
		return "", fmt.Errorf("%s path must omit the trailing slash", name)
	}
	return args[0], nil
}

func compileMethod(args []string) (Predicate, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("Method requires at least one argument")
	}
	result := make(methodPredicate, len(args))
	for _, arg := range args {
		if arg == "" {
			return nil, fmt.Errorf("Method arguments cannot be empty")
		}
		result[strings.ToUpper(arg)] = struct{}{}
	}
	return result, nil
}

func compileHeader(args []string) (Predicate, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("Header requires a name and optional value")
	}
	if args[0] == "" {
		return nil, fmt.Errorf("Header name cannot be empty")
	}
	return headerPredicate{name: args[0], value: valueAt(args, 1), presentOnly: len(args) == 1}, nil
}

func compileQuery(args []string) (Predicate, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("Query requires a name and optional value")
	}
	if args[0] == "" {
		return nil, fmt.Errorf("Query name cannot be empty")
	}
	return queryPredicate{name: args[0], value: valueAt(args, 1), presentOnly: len(args) == 1}, nil
}

func compileProtocol(args []string) (Predicate, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("Protocol requires at least one argument")
	}
	result := make(protocolPredicate, len(args))
	for _, arg := range args {
		switch arg {
		case "http", "sse", "websocket":
			result[arg] = struct{}{}
		default:
			return nil, fmt.Errorf("unsupported route protocol %q", arg)
		}
	}
	return result, nil
}

func valueAt(args []string, index int) string {
	if len(args) <= index {
		return ""
	}
	return args[index]
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenInvalid
	tokenName
	tokenLiteral
	tokenOpen
	tokenClose
	tokenComma
	tokenAnd
	tokenOr
	tokenNot
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

type lexer struct {
	input  string
	offset int
}

func newLexer(input string) *lexer { return &lexer{input: input} }

func (l *lexer) next() token {
	for l.offset < len(l.input) && unicode.IsSpace(rune(l.input[l.offset])) {
		l.offset++
	}
	start := l.offset
	if l.offset >= len(l.input) {
		return token{kind: tokenEOF, pos: start}
	}
	if strings.HasPrefix(l.input[l.offset:], "&&") {
		l.offset += 2
		return token{kind: tokenAnd, text: "&&", pos: start}
	}
	if strings.HasPrefix(l.input[l.offset:], "||") {
		l.offset += 2
		return token{kind: tokenOr, text: "||", pos: start}
	}
	switch l.input[l.offset] {
	case '(':
		l.offset++
		return token{kind: tokenOpen, text: "(", pos: start}
	case ')':
		l.offset++
		return token{kind: tokenClose, text: ")", pos: start}
	case ',':
		l.offset++
		return token{kind: tokenComma, text: ",", pos: start}
	case '!':
		l.offset++
		return token{kind: tokenNot, text: "!", pos: start}
	case '`', '\'', '"':
		quote := l.input[l.offset]
		l.offset++
		var b strings.Builder
		for l.offset < len(l.input) {
			ch := l.input[l.offset]
			l.offset++
			if ch == quote {
				return token{kind: tokenLiteral, text: b.String(), pos: start}
			}
			if ch == '\\' && quote != '`' && l.offset < len(l.input) {
				b.WriteByte(l.input[l.offset])
				l.offset++
				continue
			}
			b.WriteByte(ch)
		}
		return token{kind: tokenInvalid, text: "unterminated literal", pos: start}
	}
	for l.offset < len(l.input) {
		ch := l.input[l.offset]
		if unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == '_' || ch == '-' {
			l.offset++
			continue
		}
		break
	}
	if l.offset == start {
		l.offset++
		return token{kind: tokenInvalid, text: string(l.input[start]), pos: start}
	}
	return token{kind: tokenName, text: l.input[start:l.offset], pos: start}
}

type parser struct {
	lexer      *lexer
	registry   *Registry
	current    token
	hasCurrent bool
	depth      int
}

func (p *parser) take() token {
	if !p.hasCurrent {
		p.current = p.lexer.next()
		p.hasCurrent = true
	}
	t := p.current
	p.hasCurrent = false
	return t
}

func (p *parser) peek() token {
	if !p.hasCurrent {
		p.current = p.lexer.next()
		p.hasCurrent = true
	}
	return p.current
}

func (p *parser) parseExpression() (node, error) { return p.parseOr() }

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokenOr {
		p.take()
		right, e := p.parseAnd()
		if e != nil {
			return nil, e
		}
		left = orNode{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokenAnd {
		p.take()
		right, e := p.parseUnary()
		if e != nil {
			return nil, e
		}
		left = andNode{left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (node, error) {
	if p.depth >= MaxExpressionDepth {
		return nil, fmt.Errorf("match expression exceeds maximum nesting depth %d", MaxExpressionDepth)
	}
	p.depth++
	defer func() { p.depth-- }()
	if p.peek().kind == tokenNot {
		p.take()
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return notNode{child: child}, nil
	}
	if p.peek().kind == tokenOpen {
		p.take()
		child, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if got := p.take(); got.kind != tokenClose {
			return nil, fmt.Errorf("expected ')' at position %d", got.pos)
		}
		return child, nil
	}
	return p.parseCall()
}

func (p *parser) parseCall() (node, error) {
	name := p.take()
	if name.kind != tokenName {
		return nil, fmt.Errorf("expected rule name at position %d", name.pos)
	}
	if got := p.take(); got.kind != tokenOpen {
		return nil, fmt.Errorf("rule %q must be followed by '('", name.text)
	}
	args := make([]string, 0, 2)
	if p.peek().kind != tokenClose {
		for {
			arg := p.take()
			if arg.kind != tokenLiteral {
				return nil, fmt.Errorf("rule %q arguments must be quoted at position %d", name.text, arg.pos)
			}
			args = append(args, arg.text)
			if len(args) > MaxRuleArguments {
				return nil, fmt.Errorf("rule %q has more than %d arguments", name.text, MaxRuleArguments)
			}
			if p.peek().kind != tokenComma {
				break
			}
			p.take()
		}
	}
	if got := p.take(); got.kind != tokenClose {
		return nil, fmt.Errorf("rule %q is missing ')'", name.text)
	}
	compiler, ok := p.registry.compilers[name.text]
	if !ok {
		return nil, fmt.Errorf("unknown rule %q", name.text)
	}
	predicate, err := compiler(args)
	if err != nil {
		return nil, fmt.Errorf("rule %q: %w", name.text, err)
	}
	if predicate == nil {
		return nil, fmt.Errorf("rule %q compiler returned a nil predicate", name.text)
	}
	return predicateNode{predicate: predicate}, nil
}
