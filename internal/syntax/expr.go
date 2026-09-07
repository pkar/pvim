package syntax

import (
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/regex"
)

// The expression half of the vimscript subset.
//
// a vimscript interpreter is deliberately out of scope
// one. It is what a syntax file needs to decide which of its rules exist, which
// is a much smaller language: `if exists("b:current_syntax") | finish | endif`,
// `if s:HighlightFunctions()`, `get(g:, 'go_fold_enable', ['block'])`,
// `if has('conceal') && (!exists("g:vim_json_conceal") || g:vim_json_conceal==1)`.
// Every one of those is a condition over variables nobody set and functions
// that return a default, and the whole of it is below.
//
// An expression this cannot evaluate is an error, and an `if` whose condition
// errored takes the else branch and records a ScriptError. That is the safe
// direction: the else branch of every one of these is the plain rule and the
// then branch is the optional extra, so an unreadable condition costs colour
// and never invents it.

// evaluator is one syntax file's variables and functions.
type evaluator struct {
	s     *Syntax
	vars  map[string]value
	funcs map[string]*function
	depth int
}

// function is a user-defined function, kept as its lines so that calling it is
// running them.
type function struct {
	name   string
	params []string
	body   []string
}

// parser is a cursor over one expression.
type parser struct {
	ev  *evaluator
	src string
	i   int
	err error

	// dead counts how deep the cursor is inside a branch that short-circuiting
	// has already decided. Vim never evaluates the right of a || whose left
	// was true, and json.vim depends on it:
	//
	//	if (!exists("g:vim_json_warnings") || g:vim_json_warnings==1)
	//
	// reads a variable that does not exist, and would be an error here rather
	// than the 1 vim answers, if the right side were evaluated at all. The
	// text is still parsed, because that is how the cursor gets past it.
	dead int
}

// eval evaluates a whole expression and reports the first thing it could not
// read.
func (ev *evaluator) eval(src string) (value, error) {
	p := &parser{ev: ev, src: src}
	v := p.expr()
	p.space()
	if p.err == nil && p.i < len(p.src) {
		p.fail("trailing " + strconv.Quote(p.src[p.i:]))
	}
	return v, p.err
}

func (p *parser) fail(why string) {
	if p.err == nil && p.dead == 0 {
		p.err = &exprError{src: p.src, at: p.i, why: why}
	}
}

// exprError is an expression this package cannot read. It is not a Refused:
// the caller turns it into one, because where it happened is the caller's to
// know.
type exprError struct {
	src string
	at  int
	why string
}

func (e *exprError) Error() string {
	return "cannot evaluate " + strconv.Quote(e.src) + " at " + strconv.Itoa(e.at) + ": " + e.why
}

func (p *parser) space() {
	for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) peek(s string) bool {
	p.space()
	return strings.HasPrefix(p.src[p.i:], s)
}

func (p *parser) take(s string) bool {
	if p.peek(s) {
		p.i += len(s)
		return true
	}
	return false
}

// expr is the ternary, vim's expr1.
func (p *parser) expr() value {
	cond := p.or()
	if p.err != nil || !p.take("?") {
		return cond
	}
	if !cond.truthy() {
		p.dead++
	}
	yes := p.expr()
	if !cond.truthy() {
		p.dead--
	}
	if !p.take(":") {
		p.fail("? without :")
		return num(0)
	}
	if cond.truthy() {
		p.dead++
	}
	no := p.expr()
	if cond.truthy() {
		p.dead--
	}
	if cond.truthy() {
		return yes
	}
	return no
}

func (p *parser) or() value {
	v := p.and()
	for p.err == nil && p.take("||") {
		if v.truthy() {
			p.dead++
			p.and()
			p.dead--
			v = boolean(true)
			continue
		}
		v = boolean(p.and().truthy())
	}
	return v
}

func (p *parser) and() value {
	v := p.compare()
	for p.err == nil && p.take("&&") {
		if !v.truthy() {
			p.dead++
			p.compare()
			p.dead--
			v = boolean(false)
			continue
		}
		v = boolean(p.compare().truthy())
	}
	return v
}

// comparators are vim's expr4 operators, longest first so that ">=" is not
// read as ">" and "=~#" is not read as "=~".
var comparators = []string{
	"==#", "==?", "!=#", "!=?", ">=#", ">=?", "<=#", "<=?",
	"=~#", "=~?", "!~#", "!~?", ">#", ">?", "<#", "<?",
	"==", "!=", ">=", "<=", "=~", "!~", ">", "<",
}

func (p *parser) compare() value {
	v := p.concat()
	if p.err != nil {
		return v
	}
	p.space()
	if p.peek("is#") || p.peek("isnot") || p.peek("is ") {
		// `is` and `isnot` compare identity, which for the values this
		// evaluator has is equality.
		neg := p.take("isnot")
		if !neg {
			p.take("is")
		}
		r := p.concat()
		eq := v.text() == r.text() && v.kind == r.kind
		return boolean(eq != neg)
	}
	for _, op := range comparators {
		if !p.peek(op) {
			continue
		}
		p.i += len(op)
		r := p.concat()
		return p.applyCompare(strings.TrimRight(op, "#?"), v, r)
	}
	return v
}

func (p *parser) applyCompare(op string, a, b value) value {
	if op == "=~" || op == "!~" {
		re, err := regex.Compile(b.text(), regex.Options{})
		if err != nil {
			p.fail(err.Error())
			return num(0)
		}
		return boolean(re.MatchString(a.text()) == (op == "=~"))
	}
	if a.kind == vString || b.kind == vString {
		x, y := a.text(), b.text()
		switch op {
		case "==":
			return boolean(x == y)
		case "!=":
			return boolean(x != y)
		case ">":
			return boolean(x > y)
		case ">=":
			return boolean(x >= y)
		case "<":
			return boolean(x < y)
		case "<=":
			return boolean(x <= y)
		}
	}
	x, y := a.number(), b.number()
	switch op {
	case "==":
		return boolean(x == y)
	case "!=":
		return boolean(x != y)
	case ">":
		return boolean(x > y)
	case ">=":
		return boolean(x >= y)
	case "<":
		return boolean(x < y)
	case "<=":
		return boolean(x <= y)
	}
	p.fail("unknown comparison " + op)
	return num(0)
}

// concat is vim's expr5: +, - and the two string concatenations.
func (p *parser) concat() value {
	v := p.term()
	for p.err == nil {
		p.space()
		switch {
		case p.take(".."):
			v = str(v.text() + p.term().text())
		case p.peek("."):
			// A dot is concatenation unless it starts a float or a key, and
			// this evaluator has neither, so it is always concatenation.
			p.i++
			v = str(v.text() + p.term().text())
		case p.take("+"):
			v = num(v.number() + p.term().number())
		case p.peek("-") && !p.peek("->"):
			p.i++
			v = num(v.number() - p.term().number())
		default:
			return v
		}
	}
	return v
}

func (p *parser) term() value {
	v := p.unary()
	for p.err == nil {
		p.space()
		switch {
		case p.take("*"):
			v = num(v.number() * p.unary().number())
		case p.take("/"):
			d := p.unary().number()
			if d == 0 {
				v = num(0)
				continue
			}
			v = num(v.number() / d)
		case p.take("%"):
			d := p.unary().number()
			if d == 0 {
				v = num(0)
				continue
			}
			v = num(v.number() % d)
		default:
			return v
		}
	}
	return v
}

func (p *parser) unary() value {
	p.space()
	switch {
	case p.take("!"):
		return boolean(!p.unary().truthy())
	case p.take("-"):
		return num(-p.unary().number())
	case p.take("+"):
		return p.unary()
	}
	return p.postfix()
}

func (p *parser) postfix() value {
	v := p.primary()
	for p.err == nil {
		if p.peek("->") {
			// A method call: x->join(sep) is join(x, sep). html.vim writes its
			// attribute lists this way.
			p.i += 2
			name := p.name()
			args := []value{v}
			if !p.take("(") {
				p.fail("-> without a call")
				return v
			}
			for p.err == nil && !p.take(")") {
				args = append(args, p.expr())
				if p.take(",") {
					continue
				}
				if p.take(")") {
					break
				}
				p.fail("unclosed (")
			}
			v = p.ev.call(name, args, p)
			continue
		}
		if !p.peek("[") {
			return v
		}
		p.i++
		if p.take(":") {
			// A slice with no start: x[:n].
			hi := p.expr()
			if !p.take("]") {
				p.fail("unclosed [")
				return v
			}
			v = slice(v, 0, hi.number()+1)
			continue
		}
		lo := p.expr()
		if p.take(":") {
			hi := len(v.list)
			if v.kind == vString {
				hi = len(v.str)
			}
			if !p.peek("]") {
				hi = p.expr().number() + 1
			}
			if !p.take("]") {
				p.fail("unclosed [")
				return v
			}
			v = slice(v, lo.number(), hi)
			continue
		}
		if !p.take("]") {
			p.fail("unclosed [")
			return v
		}
		v = index(v, lo.number())
	}
	return v
}

// slice is vim's x[a:b], with b inclusive and out-of-range ends clamped.
func slice(v value, lo, hi int) value {
	switch v.kind {
	case vString:
		lo, hi = clamp(lo, len(v.str)), clamp(hi, len(v.str))
		if lo > hi {
			return str("")
		}
		return str(v.str[lo:hi])
	case vList:
		lo, hi = clamp(lo, len(v.list)), clamp(hi, len(v.list))
		if lo > hi {
			return value{kind: vList}
		}
		return value{kind: vList, list: v.list[lo:hi]}
	}
	return v
}

func index(v value, i int) value {
	switch v.kind {
	case vString:
		if i < 0 || i >= len(v.str) {
			return str("")
		}
		return str(v.str[i : i+1])
	case vList:
		if i < 0 {
			i += len(v.list)
		}
		if i < 0 || i >= len(v.list) {
			return str("")
		}
		return v.list[i]
	}
	return v
}

func clamp(n, hi int) int {
	if n < 0 {
		n = 0
	}
	if n > hi {
		n = hi
	}
	return n
}

func (p *parser) primary() value {
	p.space()
	if p.i >= len(p.src) {
		p.fail("expression ended early")
		return num(0)
	}
	c := p.src[p.i]
	switch {
	case c == '(':
		p.i++
		v := p.expr()
		if !p.take(")") {
			p.fail("unclosed (")
		}
		return v
	case c == '[':
		p.i++
		var list []value
		for p.err == nil {
			if p.take("]") {
				return value{kind: vList, list: list}
			}
			list = append(list, p.expr())
			if p.take(",") {
				continue
			}
			if p.take("]") {
				return value{kind: vList, list: list}
			}
			p.fail("unclosed [")
		}
		return value{kind: vList, list: list}
	case c == '{':
		// A dictionary literal. Nothing in a syntax file reads one back, so
		// it is consumed and answered as an empty list rather than modelled.
		depth := 0
		for p.i < len(p.src) {
			if p.src[p.i] == '{' {
				depth++
			}
			if p.src[p.i] == '}' {
				depth--
				p.i++
				if depth == 0 {
					return value{kind: vList}
				}
				continue
			}
			p.i++
		}
		p.fail("unclosed {")
		return value{kind: vList}
	case c == '\'':
		return p.singleQuoted()
	case c == '"':
		return p.doubleQuoted()
	case c >= '0' && c <= '9':
		j := p.i
		for j < len(p.src) && p.src[j] >= '0' && p.src[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(p.src[p.i:j])
		p.i = j
		return num(n)
	case c == '&':
		p.i++
		p.take("l:")
		p.take("g:")
		name := p.name()
		return p.ev.option(name)
	case c == '$':
		p.i++
		return str("")
	case c == '@':
		p.i += 2
		return str("")
	case isNameByte(c):
		name := p.name()
		if p.peek("(") {
			p.i++
			var args []value
			for p.err == nil && !p.take(")") {
				args = append(args, p.expr())
				if p.take(",") {
					continue
				}
				if p.take(")") {
					break
				}
				p.fail("unclosed (")
			}
			return p.ev.call(name, args, p)
		}
		return p.ev.lookup(name, p)
	}
	p.fail("cannot read " + strconv.Quote(string(c)))
	return num(0)
}

// name reads a variable or function name, scope prefix and all.
func (p *parser) name() string {
	j := p.i
	for j < len(p.src) && (isNameByte(p.src[j]) || p.src[j] == ':' || p.src[j] == '#') {
		// A colon is part of a scope prefix and not of the name that follows
		// it, so g:x is one name and a ternary's colon is not.
		if p.src[j] == ':' && !(j == p.i+1 && isScope(p.src[p.i])) {
			break
		}
		j++
	}
	out := p.src[p.i:j]
	p.i = j
	return out
}

func isNameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func isScope(b byte) bool {
	switch b {
	case 'g', 'b', 's', 'w', 't', 'v', 'l', 'a':
		return true
	}
	return false
}

// singleQuoted reads vim's literal string, where the only escape is ” for a
// quote.
func (p *parser) singleQuoted() value {
	p.i++
	var b strings.Builder
	for p.i < len(p.src) {
		if p.src[p.i] == '\'' {
			if p.i+1 < len(p.src) && p.src[p.i+1] == '\'' {
				b.WriteByte('\'')
				p.i += 2
				continue
			}
			p.i++
			return str(b.String())
		}
		b.WriteByte(p.src[p.i])
		p.i++
	}
	p.fail("unclosed '")
	return str(b.String())
}

// doubleQuoted reads vim's escaped string.
func (p *parser) doubleQuoted() value {
	p.i++
	var b strings.Builder
	for p.i < len(p.src) {
		c := p.src[p.i]
		if c == '"' {
			p.i++
			return str(b.String())
		}
		if c != '\\' || p.i+1 >= len(p.src) {
			b.WriteByte(c)
			p.i++
			continue
		}
		p.i++
		switch e := p.src[p.i]; e {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case '\\', '"':
			b.WriteByte(e)
		default:
			b.WriteByte('\\')
			b.WriteByte(e)
		}
		p.i++
	}
	p.fail("unclosed \"")
	return str(b.String())
}
