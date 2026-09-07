package vimrc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/options"
)

// ValueKind is the shape of a value.
type ValueKind uint8

// The value kinds. Dict is there for one line in the vimrc:
// g:ctrlp_custom_ignore, which is a dictionary literal spread over three lines
// with backslash continuations, and which the finder reads.
const (
	ValueNumber ValueKind = iota
	ValueString
	ValueList
	ValueDict
	// ValueExpr is the zero-ish case nothing produces: a value the evaluator
	// could not reduce. It exists so that a Val handed around before it was
	// filled in cannot pass for the number zero.
	ValueExpr
)

// String names the kind for an error message.
func (k ValueKind) String() string {
	switch k {
	case ValueNumber:
		return "number"
	case ValueString:
		return "string"
	case ValueList:
		return "list"
	case ValueDict:
		return "dict"
	default:
		return "expression"
	}
}

// Val is one vimscript value.
//
// Four kinds and no more: a number, a string, a list of them and a dictionary
// of them. No funcref, no float, no blob, no job, no channel. The vimrc and
// the four colourschemes between them use a number, a string, a list of six
// strings and a dictionary of two, and a fifth kind here would be a feature
// nobody asked for.
type Val struct {
	Kind ValueKind
	Num  int
	Str  string
	List []Val
	Dict map[string]Val
}

// Number and Str build the two scalar kinds.
func Number(n int) Val { return Val{Kind: ValueNumber, Num: n} }
func Str(s string) Val { return Val{Kind: ValueString, Str: s} }
func Bool(b bool) Val  { return Number(btoi(b)) }
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Truthy is what ":if" asks of a value.
//
// Vim's rule is that a condition is a number and everything else is converted
// to one, which for a string means "the leading digits, or zero". So
// `if "abc"` is false, `if "0x"` is false and `if "3x"` is true, and a list or
// a dictionary in a condition is E745, which is an error and not a false.
func (v Val) Truthy() bool {
	switch v.Kind {
	case ValueNumber:
		return v.Num != 0
	case ValueString:
		return leadingNumber(v.Str) != 0
	default:
		return false
	}
}

// String is vim's string conversion, which is what a "." concatenation and a
// ":let" of a string variable want.
func (v Val) String() string {
	switch v.Kind {
	case ValueNumber:
		return strconv.Itoa(v.Num)
	case ValueString:
		return v.Str
	case ValueList:
		parts := make([]string, len(v.List))
		for i, e := range v.List {
			parts[i] = e.quoted()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case ValueDict:
		keys := make([]string, 0, len(v.Dict))
		for k := range v.Dict {
			keys = append(keys, k)
		}
		sortStrings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = "'" + k + "': " + v.Dict[k].quoted()
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return ""
}

// quoted is String with a string wrapped in quotes, which is how vim's
// string() renders a value nested inside a list or a dictionary.
func (v Val) quoted() string {
	if v.Kind == ValueString {
		return "'" + strings.ReplaceAll(v.Str, "'", "''") + "'"
	}
	return v.String()
}

// number is vim's number conversion.
func (v Val) number() int {
	switch v.Kind {
	case ValueNumber:
		return v.Num
	case ValueString:
		return leadingNumber(v.Str)
	}
	return 0
}

// sortStrings is an insertion sort, which is enough for a dictionary of two
// and keeps this file free of an import that would only ever be used here.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// leadingNumber reads the decimal number at the front of s, which is what vim
// does when it needs a number and has a string.
func leadingNumber(s string) int {
	i, neg := 0, false
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	n, any := 0, false
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
		any = true
	}
	if !any {
		return 0
	}
	if neg {
		return -n
	}
	return n
}

// The refusals. Every one names the atom and says why, because the whole point
// of drawing the line at all is that it is visible from the message line.
var (
	// ErrNoExpr is E15, which is vim's own code for an expression it could
	// not parse.
	ErrNoExpr = errors.New("E15: Invalid expression")
	// ErrNoFunc is E117, vim's code for a call of a function that is not
	// defined. pvim raises it for every function outside the four in
	// funcNames, because from the caller's side "pvim has no such function"
	// and "vim has no such function" are the same event.
	ErrNoFunc = errors.New("E117: Unknown function")
	// ErrNoRegex is pvim's own: "=~" and "!~" would need the vim-dialect
	// translator, and internal/regex sits below internal/ex, not below this
	// package. Neither the vimrc nor the four colourschemes uses either
	// operator, so the refusal is a fence and not a loss.
	ErrNoRegex = errors.New("E15: Invalid expression: =~ and !~ need a regex engine this layer cannot reach")
	// ErrNoVar is E121, an undefined variable.
	ErrNoVar = errors.New("E121: Undefined variable")
)

// evaluator walks one expression.
//
// It carries the loader's variables as well as the Env so that a script can
// see what it set itself: the four nofrils files open with
// `if !exists("g:nofrils_strbackgrounds")` and then set the variable, and an
// evaluator that could only ask the editor would take the first branch twice.
type evaluator struct {
	src  string
	i    int
	env  Env
	vars map[string]Val
	// script is the file being run, absolute, which is what expand('<sfile>')
	// answers. Empty when an expression is evaluated outside a file, and then
	// '<sfile>' is E15 rather than the empty string: a path built out of a
	// silently empty <sfile> is a path rooted at "/", and the live vimrc
	// builds its 'undodir' out of exactly this.
	script string
}

// eval parses and evaluates one expression from the front of src and returns
// the value with the number of bytes it consumed.
//
// The caller checks the tail itself, because what may follow depends on the
// command: nothing, a "|" and another command, or white space and a comment.
func eval(src string, env Env, vars map[string]Val, script string) (Val, int, error) {
	e := &evaluator{src: src, env: env, vars: vars, script: script}
	v, err := e.expr1()
	if err != nil {
		return Val{Kind: ValueExpr}, e.i, err
	}
	return v, e.i, nil
}

func (e *evaluator) ws() {
	for e.i < len(e.src) && (e.src[e.i] == ' ' || e.src[e.i] == '\t') {
		e.i++
	}
}

func (e *evaluator) peek(s string) bool {
	e.ws()
	return strings.HasPrefix(e.src[e.i:], s)
}

func (e *evaluator) take(s string) bool {
	if e.peek(s) {
		e.i += len(s)
		return true
	}
	return false
}

// expr1 is the ternary. The vimrc has none; it is four lines and leaving it
// out would be the one hole a colourscheme walks into.
func (e *evaluator) expr1() (Val, error) {
	cond, err := e.expr2()
	if err != nil || !e.take("?") {
		return cond, err
	}
	yes, err := e.expr1()
	if err != nil {
		return yes, err
	}
	if !e.take(":") {
		return Val{}, fmt.Errorf("%w: expected \":\" after \"?\"", ErrNoExpr)
	}
	no, err := e.expr1()
	if err != nil {
		return no, err
	}
	if cond.Truthy() {
		return yes, nil
	}
	return no, nil
}

// expr2 is "||", which short-circuits in vim as it does here.
func (e *evaluator) expr2() (Val, error) {
	v, err := e.expr3()
	if err != nil {
		return v, err
	}
	for e.take("||") {
		got := v.Truthy()
		rhs, err := e.expr3()
		if err != nil {
			return rhs, err
		}
		v = Bool(got || rhs.Truthy())
	}
	return v, nil
}

// expr3 is "&&".
func (e *evaluator) expr3() (Val, error) {
	v, err := e.expr4()
	if err != nil {
		return v, err
	}
	for e.take("&&") {
		got := v.Truthy()
		rhs, err := e.expr4()
		if err != nil {
			return rhs, err
		}
		v = Bool(got && rhs.Truthy())
	}
	return v, nil
}

// comparisons, longest first so that "==#" is not read as "==" followed by a
// stray "#" and "!~" is not read as "!".
var comparisons = []string{
	"==#", "==?", "!=#", "!=?", ">=#", ">=?", "<=#", "<=?", ">#", ">?", "<#", "<?",
	"=~#", "=~?", "!~#", "!~?",
	"==", "!=", ">=", "<=", "=~", "!~", ">", "<",
}

// expr4 is one comparison, which vim does not chain and neither does this.
func (e *evaluator) expr4() (Val, error) {
	lhs, err := e.expr5()
	if err != nil {
		return lhs, err
	}
	e.ws()
	for _, op := range comparisons {
		if !strings.HasPrefix(e.src[e.i:], op) {
			continue
		}
		e.i += len(op)
		rhs, err := e.expr5()
		if err != nil {
			return rhs, err
		}
		return compare(lhs, rhs, op)
	}
	return lhs, nil
}

// compare is vim's comparison, minus the two operators that need a regex.
//
// Case: "#" is case-sensitive and "?" ignores case. A bare "==" follows the
// 'ignorecase' option in vim and does NOT here -- it is always
// case-sensitive. Nothing in the vimrc or the four colourschemes compares two
// strings at all, so the difference is unreachable from the config this
// package exists to read; it is written down rather than hidden because the
// day something does compare, that is where it will go wrong.
func compare(lhs, rhs Val, op string) (Val, error) {
	if strings.HasPrefix(op, "=~") || strings.HasPrefix(op, "!~") {
		return Val{}, ErrNoRegex
	}
	fold := strings.HasSuffix(op, "?")
	op = strings.TrimRight(op, "#?")

	if lhs.Kind == ValueNumber && rhs.Kind == ValueNumber {
		return Bool(cmpInt(lhs.Num, rhs.Num, op)), nil
	}
	if lhs.Kind == ValueList || rhs.Kind == ValueList || lhs.Kind == ValueDict || rhs.Kind == ValueDict {
		// Vim compares these element by element. Nothing in scope does, and a
		// wrong answer here would be silent.
		return Val{}, fmt.Errorf("%w: cannot compare a %s with a %s", ErrNoExpr, lhs.Kind, rhs.Kind)
	}
	a, b := lhs.String(), rhs.String()
	if fold {
		a, b = strings.ToLower(a), strings.ToLower(b)
	}
	return Bool(cmpStr(a, b, op)), nil
}

func cmpInt(a, b int, op string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	default:
		return a <= b
	}
}

func cmpStr(a, b, op string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	default:
		return a <= b
	}
}

// expr5 is "+", "-" and the two concatenations. Vim 9 spells concatenation
// ".." and legacy vimscript spells it "."; both are here because the vimrc is
// legacy and a colourscheme might not be.
func (e *evaluator) expr5() (Val, error) {
	v, err := e.expr6()
	if err != nil {
		return v, err
	}
	for {
		e.ws()
		switch {
		case e.take(".."):
			rhs, err := e.expr6()
			if err != nil {
				return rhs, err
			}
			v = Str(v.String() + rhs.String())
		case e.take("+"):
			rhs, err := e.expr6()
			if err != nil {
				return rhs, err
			}
			v = Number(v.number() + rhs.number())
		case e.take("-"):
			rhs, err := e.expr6()
			if err != nil {
				return rhs, err
			}
			v = Number(v.number() - rhs.number())
		case e.dotConcat():
			rhs, err := e.expr6()
			if err != nil {
				return rhs, err
			}
			v = Str(v.String() + rhs.String())
		default:
			return v, nil
		}
	}
}

// dotConcat consumes a legacy "." concatenation, and only when it is one: a
// dot followed by a digit is part of a float this package does not have, and
// a dot with no space around it after a variable name is a dictionary index,
// which nothing in scope writes. Requiring the dot to be followed by
// something that can start a value keeps "a.b" out of here.
func (e *evaluator) dotConcat() bool {
	e.ws()
	if e.i >= len(e.src) || e.src[e.i] != '.' {
		return false
	}
	rest := e.src[e.i+1:]
	if rest == "" {
		return false
	}
	switch c := rest[0]; {
	case c >= '0' && c <= '9':
		return false
	default:
		e.i++
		return true
	}
}

// expr6 is "*", "/" and "%".
func (e *evaluator) expr6() (Val, error) {
	v, err := e.expr7()
	if err != nil {
		return v, err
	}
	for {
		e.ws()
		var op byte
		switch {
		case e.take("*"):
			op = '*'
		case e.take("/"):
			op = '/'
		case e.take("%"):
			op = '%'
		default:
			return v, nil
		}
		rhs, err := e.expr7()
		if err != nil {
			return rhs, err
		}
		a, b := v.number(), rhs.number()
		switch {
		case op == '*':
			v = Number(a * b)
		case b == 0:
			// Vim's integer divide by zero is not an error; it saturates.
			v = Number(0)
		case op == '/':
			v = Number(a / b)
		default:
			v = Number(a % b)
		}
	}
}

// expr7 is the unary operators.
func (e *evaluator) expr7() (Val, error) {
	e.ws()
	switch {
	case e.take("!"):
		v, err := e.expr7()
		if err != nil {
			return v, err
		}
		return Bool(!v.Truthy()), nil
	case e.take("-"):
		v, err := e.expr7()
		if err != nil {
			return v, err
		}
		return Number(-v.number()), nil
	case e.take("+"):
		return e.expr7()
	}
	return e.expr8()
}

// expr8 is a value with the postfix operators vim allows on one. Indexing and
// slicing are not here: nothing in scope indexes, and a wrong answer from a
// half-written index would be silent.
func (e *evaluator) expr8() (Val, error) { return e.expr9() }

// expr9 is one value.
func (e *evaluator) expr9() (Val, error) {
	e.ws()
	if e.i >= len(e.src) {
		return Val{}, fmt.Errorf("%w: \"\"", ErrNoExpr)
	}

	switch c := e.src[e.i]; {
	case c == '(':
		e.i++
		v, err := e.expr1()
		if err != nil {
			return v, err
		}
		if !e.take(")") {
			return Val{}, fmt.Errorf("%w: expected \")\"", ErrNoExpr)
		}
		return v, nil
	case c == '\'':
		return e.singleQuoted()
	case c == '"':
		return e.doubleQuoted()
	case c == '[':
		return e.list()
	case c == '{':
		return e.dict()
	case c == '&':
		return e.option()
	case c == '$':
		return e.envVar()
	case c >= '0' && c <= '9':
		return e.number()
	case isNameByte(c):
		return e.nameOrCall()
	}
	return Val{}, fmt.Errorf("%w: %q", ErrNoExpr, e.src[e.i:])
}

// singleQuoted reads '...', where the only escape is a doubled quote and a
// backslash is a backslash. That is what lets the ctrlp ignore patterns carry
// `\.git$\|\.yardoc` through to internal/regex unmangled.
func (e *evaluator) singleQuoted() (Val, error) {
	e.i++ // the opening quote
	var b strings.Builder
	for e.i < len(e.src) {
		c := e.src[e.i]
		if c == '\'' {
			if e.i+1 < len(e.src) && e.src[e.i+1] == '\'' {
				b.WriteByte('\'')
				e.i += 2
				continue
			}
			e.i++
			return Str(b.String()), nil
		}
		b.WriteByte(c)
		e.i++
	}
	return Val{}, fmt.Errorf("%w: unterminated string", ErrNoExpr)
}

// doubleQuoted reads "...", where a backslash escapes.
func (e *evaluator) doubleQuoted() (Val, error) {
	e.i++
	var b strings.Builder
	for e.i < len(e.src) {
		c := e.src[e.i]
		switch c {
		case '"':
			e.i++
			return Str(b.String()), nil
		case '\\':
			e.i++
			if e.i >= len(e.src) {
				return Val{}, fmt.Errorf("%w: unterminated string", ErrNoExpr)
			}
			switch esc := e.src[e.i]; esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'e':
				b.WriteByte(0x1b)
			case '0':
				b.WriteByte(0)
			default:
				b.WriteByte(esc)
			}
			e.i++
		default:
			b.WriteByte(c)
			e.i++
		}
	}
	return Val{}, fmt.Errorf("%w: unterminated string", ErrNoExpr)
}

// list reads [a, b, c], with a trailing comma allowed as vim allows one.
func (e *evaluator) list() (Val, error) {
	e.i++ // "["
	out := Val{Kind: ValueList, List: []Val{}}
	for {
		if e.take("]") {
			return out, nil
		}
		v, err := e.expr1()
		if err != nil {
			return v, err
		}
		out.List = append(out.List, v)
		if e.take(",") {
			continue
		}
		if e.take("]") {
			return out, nil
		}
		return Val{}, fmt.Errorf("%w: expected \",\" or \"]\" in a list", ErrNoExpr)
	}
}

// dict reads {'k': v, ...}.
//
// This is the vimrc's g:ctrlp_custom_ignore and the only dictionary this
// package allows: a literal, assigned to a variable, read by the finder. The keys are
// converted to strings the way vim does, so {1: 'x'} has the key "1".
func (e *evaluator) dict() (Val, error) {
	e.i++ // "{"
	out := Val{Kind: ValueDict, Dict: map[string]Val{}}
	for {
		if e.take("}") {
			return out, nil
		}
		k, err := e.expr1()
		if err != nil {
			return k, err
		}
		if !e.take(":") {
			return Val{}, fmt.Errorf("%w: expected \":\" in a dictionary", ErrNoExpr)
		}
		v, err := e.expr1()
		if err != nil {
			return v, err
		}
		out.Dict[k.String()] = v
		if e.take(",") {
			continue
		}
		if e.take("}") {
			return out, nil
		}
		return Val{}, fmt.Errorf("%w: expected \",\" or \"}\" in a dictionary", ErrNoExpr)
	}
}

// option reads &name, &l:name and &g:name, which the vimrc uses twice:
// "if !&scrolloff" and "if !&sidescrolloff".
func (e *evaluator) option() (Val, error) {
	e.i++ // "&"
	name := e.name()
	if strings.HasPrefix(name, "l:") || strings.HasPrefix(name, "g:") {
		name = name[2:]
	}
	v, err := e.env.Option(name)
	if err != nil {
		return Val{}, err
	}
	switch v.Kind {
	case options.Number:
		return Number(v.Num), nil
	case options.Bool:
		return Bool(v.Bool), nil
	default:
		return Str(v.Str), nil
	}
}

// envVar reads $NAME, which goes through Env.Expand so that $MYVIMRC and
// $MYGVIMRC mean what the editor says they mean and not what the process
// environment happens to hold.
func (e *evaluator) envVar() (Val, error) {
	start := e.i
	e.i++
	e.name()
	return Str(e.env.Expand(e.src[start:e.i])), nil
}

// number reads a number literal in the four bases vim has.
//
// The octal case is not decoration. The live vimrc's three mkdir() calls pass
// 0700 as the permission, and vim reads that as octal 448 -- measured,
// ":echo 0700" prints 448 -- so a reader that took it for decimal
// 700 would ask for a mode of 0o1274 and make the state directory
// world-writable. Vim's own rule, from vim_str2nr: a leading "0" is octal only
// when every digit after it is 0 to 7, so 08 is eight and 0700 is 448.
func (e *evaluator) number() (Val, error) {
	start := e.i
	rest := e.src[e.i:]

	base, prefix := 10, 0
	switch {
	case strings.HasPrefix(rest, "0x"), strings.HasPrefix(rest, "0X"):
		base, prefix = 16, 2
	case strings.HasPrefix(rest, "0b"), strings.HasPrefix(rest, "0B"):
		base, prefix = 2, 2
	case len(rest) > 1 && rest[0] == '0' && isOctalRun(rest[1:]):
		base, prefix = 8, 1
	}

	e.i += prefix
	for e.i < len(e.src) && isDigitInBase(e.src[e.i], base) {
		e.i++
	}
	digits := e.src[start+prefix : e.i]
	if digits == "" {
		// "0x" with no hex digit after it is the number zero and an "x",
		// which is what vim reads it as.
		e.i = start + 1
		return Number(0), nil
	}
	n, err := strconv.ParseInt(digits, base, 64)
	if err != nil {
		return Val{}, fmt.Errorf("%w: %q", ErrNoExpr, e.src[start:e.i])
	}
	return Number(int(n)), nil
}

// isOctalRun reports whether s opens with at least one digit and every digit
// in that opening run is 0 to 7, which is vim's test for an octal literal.
func isOctalRun(s string) bool {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n == 0 {
		return false
	}
	for i := 0; i < n; i++ {
		if s[i] > '7' {
			return false
		}
	}
	return true
}

func isDigitInBase(c byte, base int) bool {
	switch base {
	case 16:
		return isHexByte(c)
	case 8:
		return c >= '0' && c <= '7'
	case 2:
		return c == '0' || c == '1'
	}
	return c >= '0' && c <= '9'
}

// name reads an identifier, scope prefix and all: "g:go_bin_path", "v:true",
// "mapleader".
func (e *evaluator) name() string {
	start := e.i
	for e.i < len(e.src) && isNameByte(e.src[e.i]) {
		e.i++
	}
	return e.src[start:e.i]
}

// nameOrCall reads a variable or a function call.
func (e *evaluator) nameOrCall() (Val, error) {
	name := e.name()
	if e.i < len(e.src) && e.src[e.i] == '(' {
		return e.call(name)
	}
	switch name {
	case "v:true":
		return Number(1), nil
	case "v:false":
		return Number(0), nil
	case "v:none", "v:null":
		return Number(0), nil
	}
	if v, ok := e.lookup(name); ok {
		return v, nil
	}
	return Val{}, fmt.Errorf("%w: %s", ErrNoVar, name)
}

// lookup finds a variable in the loader's own table, trying the bare name as a
// global as well, because a bare name in a vimrc IS a global and the vimrc
// writes four of them that way.
func (e *evaluator) lookup(name string) (Val, bool) {
	if v, ok := e.vars[name]; ok {
		return v, true
	}
	if !strings.Contains(name, ":") {
		if v, ok := e.vars["g:"+name]; ok {
			return v, true
		}
	}
	return Val{}, false
}

// call runs one of the ten builtins in funcs.go.
//
// Anything else is E117 with the name in it, which is deliberately the same
// answer vim gives for a function that does not exist: a vimrc that calls
// something pvim declined to implement and a vimrc that calls something
// misspelled are the same problem to whoever is reading the message line.
func (e *evaluator) call(name string) (Val, error) {
	e.i++ // "("
	var args []Val
	for {
		if e.take(")") {
			break
		}
		v, err := e.expr1()
		if err != nil {
			return v, err
		}
		args = append(args, v)
		if e.take(",") {
			continue
		}
		if e.take(")") {
			break
		}
		return Val{}, fmt.Errorf("%w: expected \",\" or \")\" in a function call", ErrNoExpr)
	}

	spec, ok := builtins[name]
	if !ok {
		return Val{}, fmt.Errorf("%w: %s", ErrNoFunc, name)
	}
	switch {
	case len(args) < spec.minArgs:
		return Val{}, fmt.Errorf("E119: Not enough arguments for function: %s", name)
	case len(args) > spec.maxArgs:
		return Val{}, fmt.Errorf("E118: Too many arguments for function: %s", name)
	}
	arg := args[0].String()

	switch name {
	case "has":
		return Bool(e.env.Has(arg)), nil
	case "filereadable":
		return Bool(e.env.FileReadable(e.env.Expand(arg))), nil
	case "expand":
		return e.expand(arg)
	case "exists":
		return Bool(e.exists(arg)), nil
	case "executable":
		// Expanded here and not in Env, so that every builtin that takes a
		// path takes it the same way: one Expand at the call site, none
		// inside. executable('~/bin/x') has to mean what filereadable of the
		// same string means.
		return Bool(e.env.Executable(e.env.Expand(arg))), nil
	case "isdirectory":
		return Bool(e.env.IsDirectory(e.env.Expand(arg))), nil
	case "resolve":
		return Str(e.env.Resolve(e.env.Expand(arg))), nil
	case "fnameescape":
		return Str(fnameescape(arg)), nil
	case "fnamemodify":
		got, err := fnamemodify(arg, args[1].String(), e.env.Cwd())
		if err != nil {
			return Val{}, err
		}
		return Str(got), nil
	default: // mkdir
		return e.mkdir(args)
	}
}

// expand answers expand().
//
// Three forms, which are the three the two vimrcs use: "<sfile>" with the
// filename modifiers glued on the end, a leading "~", and a "$NAME". The first
// is this evaluator's because it is the only one that depends on WHICH file is
// being read, and the other two are the world's and go to Env.
//
// Every other "<...>" is refused rather than passed through. Vim leaves an
// unknown one in the string, which for "<afile>" or "<cword>" means a path
// with a literal "<afile>" in it lands in an option and the failure surfaces
// somewhere else entirely.
func (e *evaluator) expand(arg string) (Val, error) {
	if !strings.HasPrefix(arg, "<") {
		return Str(e.env.Expand(arg)), nil
	}
	name, mods := splitFileMods(arg)
	if name != "<sfile>" {
		return Val{}, fmt.Errorf("%w: expand(%q): pvim answers <sfile> and nothing else", ErrNoExpr, arg)
	}
	if e.script == "" {
		return Val{}, fmt.Errorf("%w: expand(%q): no file is being sourced", ErrNoExpr, arg)
	}
	got, err := fnamemodify(e.script, mods, e.env.Cwd())
	if err != nil {
		return Val{}, err
	}
	return Str(got), nil
}

// mkdir answers mkdir(), which is the one builtin here that changes the world.
//
// It is in the set because the live vimrc calls it three times and because
// what it makes is the directory 'undodir', 'directory' and 'viminfofile' are
// about to be pointed at. The older vimrc had this bug: "set undofile" with
// "undodir=~/.cache/vim" and that directory does not exist, so persistent undo
// never persisted. The current file fixed it by calling mkdir, and an editor
// that reads the file and skips the call reintroduces the bug it was written
// to close.
//
// Vim returns 0 and prints E739 on failure; this raises the error, so the
// loader records it against the line and the message line says which directory
// and why.
func (e *evaluator) mkdir(args []Val) (Val, error) {
	path := e.env.Expand(args[0].String())
	flags := ""
	if len(args) > 1 {
		flags = args[1].String()
	}
	// Vim's default is 0755 when the third argument is left out.
	mode := 0o755
	if len(args) > 2 {
		mode = args[2].number()
	}
	if err := e.env.MkDir(path, flags, mode); err != nil {
		return Val{}, fmt.Errorf("%w: %s: %v", ErrMkdir, path, err)
	}
	return Number(1), nil
}

// exists answers exists().
//
// The loader's own variables come first, so that the four nofrils files, which
// each open with three rounds of
//
//	if !exists("g:nofrils_strbackgrounds")
//	 let g:nofrils_strbackgrounds = 0
//	endif
//
// take the branch once and not twice when one file sources another.
func (e *evaluator) exists(name string) bool {
	switch {
	case strings.HasPrefix(name, "&"):
		_, err := e.env.Option(strings.TrimPrefix(strings.TrimPrefix(name[1:], "l:"), "g:"))
		return err == nil
	case strings.HasPrefix(name, "*"), strings.HasPrefix(name, ":"):
		// A function or a command. pvim defines no functions, and the
		// commands it does define are the editor's business.
		return e.env.Exists(name)
	}
	if _, ok := e.lookup(name); ok {
		return true
	}
	return e.env.Exists(name)
}

func isNameByte(c byte) bool {
	return c == '_' || c == ':' || c == '#' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
