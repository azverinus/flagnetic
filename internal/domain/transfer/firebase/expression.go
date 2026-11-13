package firebase

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// alternatives is a Firebase condition expression in disjunctive normal
// form: it holds when any of its clauses holds. Each conjunction becomes one
// rule.
type alternatives []conjunction

// conjunction is terms that must all hold: the conditions of one rule, and at
// most one percent term, which makes the rule a rollout.
type conjunction struct {
	// terms are the source terms, as the template writes them.
	terms    []string
	atoms    []atom
	percents []percent
	// unsupported says why the conjunction cannot become a rule; empty when it
	// can.
	unsupported string
}

// atom is one translated comparison: a Flagnetic operator and value on a
// standard attribute or on a user property or custom signal.
type atom struct {
	attribute string // a standard attribute's key; empty for a custom one
	custom    *custom
	op        string
	value     any
}

// custom is a user property or a custom signal, and the attribute type a
// comparison on it needs.
type custom struct {
	source string // "user property" or "custom signal"
	key    string
	typ    workspace.AttributeType
}

// percent is the share of units a percent term keeps, in Firebase's
// percent and in basis points.
type percent struct {
	firebase    float64
	basisPoints int
	// from is where Firebase's range starts, above 0 for > and between:
	// a rollout always starts at bucket 0.
	from float64
}

// parseExpression reads a condition expression: terms joined by && and ||
// and grouped by parentheses, && binding tighter. A term it cannot
// translate becomes a conjunction saying why, so that only the rules that
// need it are skipped.
func parseExpression(expression string) (alternatives, error) {
	p := &expressionParser{s: expression}
	a, err := p.or()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i < len(p.s) {
		return nil, fmt.Errorf("unexpected %q at offset %d", p.s[p.i:], p.i)
	}
	return a, nil
}

type expressionParser struct {
	s string
	i int
}

func (p *expressionParser) or() (alternatives, error) {
	a, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.space(); strings.HasPrefix(p.s[p.i:], "||"); p.space() {
		p.i += 2
		b, err := p.and()
		if err != nil {
			return nil, err
		}
		if a = append(a, b...); len(a) > flags.MaxRules {
			return nil, fmt.Errorf("it makes more than %d rules", flags.MaxRules)
		}
	}
	return a, nil
}

func (p *expressionParser) and() (alternatives, error) {
	a, err := p.primary()
	if err != nil {
		return nil, err
	}
	// One term alone expands to a rule per value of contains or matches.
	if len(a) > flags.MaxRules {
		return nil, fmt.Errorf("it makes more than %d rules", flags.MaxRules)
	}
	for p.space(); strings.HasPrefix(p.s[p.i:], "&&"); p.space() {
		p.i += 2
		b, err := p.primary()
		if err != nil {
			return nil, err
		}
		if len(a)*len(b) > flags.MaxRules {
			return nil, fmt.Errorf("it makes more than %d rules", flags.MaxRules)
		}
		a = product(a, b)
	}
	return a, nil
}

// product is a && b: every conjunction of a joined with every conjunction of b.
func product(a, b alternatives) alternatives {
	out := make(alternatives, 0, len(a)*len(b))
	for _, x := range a {
		for _, y := range b {
			c := conjunction{
				terms:       append(append([]string{}, x.terms...), y.terms...),
				atoms:       append(append([]atom{}, x.atoms...), y.atoms...),
				percents:    append(append([]percent{}, x.percents...), y.percents...),
				unsupported: x.unsupported,
			}
			if c.unsupported == "" {
				c.unsupported = y.unsupported
			}
			out = append(out, c)
		}
	}
	return out
}

// primary is a parenthesized expression or one term. A parenthesis that
// opens a primary groups; any other belongs to its term, as in
// app.version.<(['1.0']).
func (p *expressionParser) primary() (alternatives, error) {
	p.space()
	if p.i < len(p.s) && p.s[p.i] == '(' {
		p.i++
		a, err := p.or()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != ')' {
			return nil, fmt.Errorf("a parenthesis opened is not closed")
		}
		p.i++
		return a, nil
	}
	start, depth := p.i, 0
	for p.i < len(p.s) {
		c := p.s[p.i]
		if depth == 0 && (strings.HasPrefix(p.s[p.i:], "&&") || strings.HasPrefix(p.s[p.i:], "||")) {
			break
		}
		switch c {
		case '\'', '"':
			end, err := stringEnd(p.s, p.i)
			if err != nil {
				return nil, err
			}
			p.i = end
			continue
		case '(', '[':
			depth++
		case ')', ']':
			if depth == 0 {
				return p.term(start)
			}
			depth--
		}
		p.i++
	}
	if depth != 0 {
		return nil, fmt.Errorf("a bracket opened is not closed")
	}
	return p.term(start)
}

func (p *expressionParser) term(start int) (alternatives, error) {
	text := strings.TrimSpace(p.s[start:p.i])
	if text == "" {
		return nil, fmt.Errorf("a term is missing at offset %d", start)
	}
	return translateTerm(text), nil
}

func (p *expressionParser) space() {
	for p.i < len(p.s) && unicode.IsSpace(rune(p.s[p.i])) {
		p.i++
	}
}

// stringEnd returns the offset just past the quoted string starting at i.
func stringEnd(s string, i int) (int, error) {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case quote:
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("a string is not closed")
}

// compareOps are Firebase's comparison operators and Flagnetic's.
var compareOps = map[string]string{"<": "lt", "<=": "lte", "==": "eq", "!=": "neq", ">": "gt", ">=": "gte"}

// standardElements are the elements that map to a standard attribute.
var standardElements = map[string]string{
	"app.id": "app_id", "device.os": "platform", "app.version": "app_version", "app.build": "build_number",
	"device.language": "locale", "device.country": "country",
}

// translateTerm translates one term, or says why it cannot.
func translateTerm(text string) alternatives {
	unsupported := func(reason string) alternatives {
		return alternatives{{terms: []string{text}, unsupported: reason}}
	}
	t, err := lexTerm(text)
	if err != nil {
		return unsupported(fmt.Sprintf("cannot read it: %v", err))
	}
	switch {
	case len(t.tokens) == 1 && t.tokens[0].is(tokenIdent, "true"):
		return alternatives{{terms: []string{text}}}
	case len(t.tokens) == 1 && t.tokens[0].is(tokenIdent, "false"):
		return alternatives{}
	}
	e, err := t.element()
	if err != nil {
		return unsupported(fmt.Sprintf("cannot read it: %v", err))
	}
	if _, standard := standardElements[e.name]; !standard && e.name != "percent" && e.name != "app.userProperty" && e.name != "app.customSignal" {
		return unsupported(e.name + " is not supported")
	}
	op, values, err := t.operator(e.name == "percent")
	if err != nil {
		return unsupported(fmt.Sprintf("cannot read it: %v", err))
	}
	a, reason := translateComparison(e, op, values)
	if reason != "" {
		return unsupported(reason)
	}
	for i := range a {
		a[i].terms = []string{text}
	}
	return a
}

// element is what a term compares: a dotted name, the key of a user
// property or custom signal, and whether version(…) wraps it.
type element struct {
	name    string
	key     string
	version bool
}

// translateComparison maps an element, a Firebase operator and its values
// onto Flagnetic conditions or a percent, or says why it cannot.
func translateComparison(e element, op string, values []token) (alternatives, string) {
	one := func(a atom) (alternatives, string) { return alternatives{{atoms: []atom{a}}}, "" }
	notSupported := fmt.Sprintf("%s.%s is not supported", e.name, op)
	if e.name == "percent" {
		return translatePercent(op, values)
	}
	if e.version && e.name != "app.customSignal" {
		return nil, "version(" + e.name + ") is not supported"
	}
	switch e.name {
	case "app.userProperty", "app.customSignal":
		return translateCustom(e, op, values)
	case "app.id", "device.os":
		attr := standardElements[e.name]
		if cmp, ok := compareOps[op]; ok && (cmp == "eq" || cmp == "neq") && len(values) == 1 {
			return one(atom{attribute: attr, op: cmp, value: values[0].text})
		}
		if op == "exactlyMatches" && e.name == "app.id" {
			return one(atom{attribute: attr, op: "in", value: texts(values)})
		}
	case "app.version":
		if cmp, ok := compareOps[op]; ok && len(values) == 1 {
			return one(atom{attribute: standardElements[e.name], op: cmp, value: values[0].text})
		}
		if op == "exactlyMatches" {
			return one(atom{attribute: standardElements[e.name], op: "in", value: texts(values)})
		}
	case "app.build":
		numbers, err := numbersOf(values)
		if err != nil {
			return nil, fmt.Sprintf("app.build compares as build_number, a number: %v", err)
		}
		if cmp, ok := compareOps[op]; ok && len(values) == 1 {
			return one(atom{attribute: standardElements[e.name], op: cmp, value: numbers[0]})
		}
		if op == "exactlyMatches" {
			list := make([]any, len(numbers))
			for i, n := range numbers {
				list[i] = n
			}
			return one(atom{attribute: standardElements[e.name], op: "in", value: list})
		}
	case "device.language":
		if op == "in" {
			return one(atom{attribute: standardElements[e.name], op: "in", value: texts(values)})
		}
	case "device.country":
		// Firebase compares country codes ignoring case; the country
		// attribute holds ISO 3166-1 alpha-2 codes in upper case.
		if op == "in" {
			codes := texts(values)
			for i, c := range codes {
				codes[i] = strings.ToUpper(c.(string))
			}
			return one(atom{attribute: standardElements[e.name], op: "in", value: codes})
		}
	}
	return nil, notSupported
}

// translateCustom maps a comparison on a user property or custom signal:
// numeric comparisons on a number attribute, comparisons under version(…)
// on a semantic version, the string operators on a string.
func translateCustom(e element, op string, values []token) (alternatives, string) {
	source := "user property"
	if e.name == "app.customSignal" {
		source = "custom signal"
	}
	on := func(typ workspace.AttributeType, fop string, value any) atom {
		return atom{custom: &custom{source: source, key: e.key, typ: typ}, op: fop, value: value}
	}
	if cmp, ok := compareOps[op]; ok {
		if len(values) != 1 {
			return nil, fmt.Sprintf("%s.%s takes one value", e.name, op)
		}
		if e.version {
			return alternatives{{atoms: []atom{on("semver", cmp, values[0].text)}}}, ""
		}
		n, err := numbersOf(values)
		if err != nil {
			if cmp == "eq" || cmp == "neq" {
				return alternatives{{atoms: []atom{on("string", cmp, values[0].text)}}}, ""
			}
			return nil, fmt.Sprintf("%s %s compares numbers, but %q is not a number", e.name, op, values[0].text)
		}
		return alternatives{{atoms: []atom{on("number", cmp, n[0])}}}, ""
	}
	if e.version {
		return nil, fmt.Sprintf("version(%s).%s is not supported", e.name, op)
	}
	switch op {
	case "exactlyMatches":
		return alternatives{{atoms: []atom{on("string", "in", texts(values))}}}, ""
	case "contains", "matches":
		// Any of the values: one rule each.
		var a alternatives
		for _, v := range values {
			a = append(a, conjunction{atoms: []atom{on("string", op, v.text)}})
		}
		return a, ""
	case "notContains":
		// None of the values: every condition in one rule.
		var c conjunction
		for _, v := range values {
			c.atoms = append(c.atoms, on("string", "not_contains", v.text))
		}
		return alternatives{c}, ""
	}
	return nil, fmt.Sprintf("%s.%s is not supported", e.name, op)
}

// translatePercent maps percent <= p, < p, > p, >= p and between a and b
// to the share of units they keep. Firebase's own buckets cannot be
// reproduced, so only the share matters.
func translatePercent(op string, values []token) (alternatives, string) {
	numbers, err := numbersOf(values)
	if err != nil {
		return nil, fmt.Sprintf("percent: %v", err)
	}
	var share, from float64
	switch {
	case (op == "<=" || op == "<") && len(numbers) == 1:
		share = numbers[0]
	case (op == ">" || op == ">=") && len(numbers) == 1:
		share, from = 100-numbers[0], numbers[0]
	case op == "between" && len(numbers) == 2:
		share, from = numbers[1]-numbers[0], numbers[0]
	default:
		return nil, "percent " + op + " is not supported"
	}
	share = math.Min(math.Max(share, 0), 100)
	return alternatives{{percents: []percent{{firebase: share, basisPoints: int(math.Round(share * 100)), from: from}}}}, ""
}

func texts(values []token) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v.text)
	}
	return out
}

func numbersOf(values []token) ([]float64, error) {
	out := make([]float64, 0, len(values))
	for _, v := range values {
		n, err := strconv.ParseFloat(v.text, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, fmt.Errorf("%q is not a number", v.text)
		}
		out = append(out, n)
	}
	return out, nil
}

// Token kinds of a term.
const (
	tokenIdent  = "identifier"
	tokenString = "string"
	tokenNumber = "number"
	tokenOp     = "operator"
	tokenPunct  = "punctuation"
)

type token struct {
	kind string
	text string // a string's contents, unquoted
}

func (t token) is(kind, text string) bool { return t.kind == kind && t.text == text }

// termTokens are a term's tokens with a read position.
type termTokens struct {
	tokens []token
	i      int
}

// lexTerm splits a term into identifiers, quoted strings, numbers,
// comparison operators and the punctuation . [ ] ( ) ,.
func lexTerm(s string) (*termTokens, error) {
	var out []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case c == '\'' || c == '"':
			end, err := stringEnd(s, i)
			if err != nil {
				return nil, err
			}
			text, err := unquote(s[i+1 : end-1])
			if err != nil {
				return nil, err
			}
			out, i = append(out, token{tokenString, text}), end
		case c == '-' || c >= '0' && c <= '9':
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			out, i = append(out, token{tokenNumber, s[i:j]}), j
		case c == '_' || unicode.IsLetter(rune(c)):
			j := i + 1
			for j < len(s) && (s[j] == '_' || unicode.IsLetter(rune(s[j])) || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			out, i = append(out, token{tokenIdent, s[i:j]}), j
		case strings.ContainsRune("<>=!", rune(c)):
			j := i + 1
			if j < len(s) && s[j] == '=' {
				j++
			}
			op := s[i:j]
			if _, ok := compareOps[op]; !ok {
				return nil, fmt.Errorf("unknown operator %q", op)
			}
			out, i = append(out, token{tokenOp, op}), j
		case strings.ContainsRune(".[](),", rune(c)):
			out, i = append(out, token{tokenPunct, string(c)}), i+1
		default:
			return nil, fmt.Errorf("unexpected %q", c)
		}
	}
	return &termTokens{tokens: out}, nil
}

// unquote resolves backslash escapes inside a quoted string.
func unquote(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 == len(s) {
				return "", fmt.Errorf("a string ends in a backslash")
			}
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

func (t *termTokens) peek(offset int) token {
	if t.i+offset < len(t.tokens) {
		return t.tokens[t.i+offset]
	}
	return token{}
}

func (t *termTokens) next() token {
	tok := t.peek(0)
	if t.i < len(t.tokens) {
		t.i++
	}
	return tok
}

func (t *termTokens) expect(kind, text string) error {
	if tok := t.next(); !tok.is(kind, text) {
		return fmt.Errorf("expected %q", text)
	}
	return nil
}

// element reads what the term compares: version(inner), percent with an
// optional seed, or a dotted name with an optional ['key'].
func (t *termTokens) element() (element, error) {
	first := t.next()
	if first.kind != tokenIdent {
		return element{}, fmt.Errorf("expected an element")
	}
	if first.text == "version" && t.peek(0).is(tokenPunct, "(") {
		t.next()
		e, err := t.element()
		if err != nil {
			return element{}, err
		}
		e.version = true
		return e, t.expect(tokenPunct, ")")
	}
	name := first.text
	if name == "percent" {
		if t.peek(0).is(tokenPunct, "(") && t.peek(1).kind == tokenString && t.peek(2).is(tokenPunct, ")") {
			t.i += 3 // the seed: a fresh salt replaces it
		}
		return element{name: name}, nil
	}
	// A dot followed by an identifier and a parenthesis, or by an
	// operator, starts a method call rather than continuing the name.
	for t.peek(0).is(tokenPunct, ".") && t.peek(1).kind == tokenIdent && !t.peek(2).is(tokenPunct, "(") {
		name += "." + t.peek(1).text
		t.i += 2
	}
	e := element{name: name}
	if t.peek(0).is(tokenPunct, "[") {
		t.next()
		key := t.next()
		if key.kind != tokenString {
			return element{}, fmt.Errorf("expected a quoted key after %s[", name)
		}
		e.key = key.text
		if err := t.expect(tokenPunct, "]"); err != nil {
			return element{}, err
		}
	}
	return e, nil
}

// operator reads the rest of a term: .op([values]), op value, in [values]
// or, for percent, between a and b. Nothing may follow.
func (t *termTokens) operator(percent bool) (string, []token, error) {
	var op string
	var values []token
	var err error
	switch tok := t.next(); {
	case tok.is(tokenPunct, ".") && (t.peek(0).kind == tokenIdent || t.peek(0).kind == tokenOp):
		op = t.next().text
		if err := t.expect(tokenPunct, "("); err != nil {
			return "", nil, err
		}
		if values, err = t.list(); err != nil {
			return "", nil, err
		}
		if err := t.expect(tokenPunct, ")"); err != nil {
			return "", nil, err
		}
	case tok.kind == tokenOp:
		op = tok.text
		if t.peek(0).is(tokenPunct, "[") {
			values, err = t.list()
		} else {
			values, err = t.scalar()
		}
		if err != nil {
			return "", nil, err
		}
	case tok.is(tokenIdent, "in"):
		op = "in"
		if values, err = t.list(); err != nil {
			return "", nil, err
		}
	case percent && tok.is(tokenIdent, "between"):
		op = "between"
		low, and, high := t.next(), t.next(), t.next()
		if low.kind != tokenNumber || !and.is(tokenIdent, "and") || high.kind != tokenNumber {
			return "", nil, fmt.Errorf("expected between <number> and <number>")
		}
		values = []token{low, high}
	default:
		return "", nil, fmt.Errorf("expected an operator")
	}
	if t.i != len(t.tokens) {
		return "", nil, fmt.Errorf("unexpected %q after the operator", t.peek(0).text)
	}
	return op, values, nil
}

func (t *termTokens) scalar() ([]token, error) {
	v := t.next()
	if v.kind != tokenString && v.kind != tokenNumber {
		return nil, fmt.Errorf("expected a value")
	}
	return []token{v}, nil
}

// list reads [v, v, …] of at least one string or number.
func (t *termTokens) list() ([]token, error) {
	if err := t.expect(tokenPunct, "["); err != nil {
		return nil, err
	}
	var out []token
	for {
		v, err := t.scalar()
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
		switch sep := t.next(); {
		case sep.is(tokenPunct, "]"):
			return out, nil
		case !sep.is(tokenPunct, ","):
			return nil, fmt.Errorf("expected , or ]")
		}
	}
}
