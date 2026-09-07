package syntax

import (
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/regex"
)

// The builtin functions and the variable lookup: everything a syntax file's
// conditions read.
//
// Every answer here was checked against /opt/homebrew/bin/vim 9.2.0321 rather
// than guessed, because a has() that answers differently from the vim the
// oracle runs makes a whole file's worth of rules exist on one side and not the
// other, and that shows up as hundreds of positions of difference with no
// obvious cause.

// features is what has() answers 1 for. It is the list vim --clean prints for
// the features a syntax file in the shipped runtime asks about, plus the ones
// this editor genuinely has.
//
// Read off vim with
//
//	for f in [...] | echo f has(f) | endfor
var features = map[string]bool{
	"conceal": true, "folding": true, "syntax": true, "spell": true,
	"multi_byte": true, "float": true, "iconv": true, "byte_offset": true,
	"textprop": true, "patch-7.4.1142": true, "unix": true, "mac": true,
	"macunix": true, "osx": true, "eval": true, "modify_fname": true,
	"cmdline_compl": true, "autocmd": true, "menu": true, "quickfix": true,
	"user_commands": true, "vertsplit": true, "windows": true,
	"gui_running": false, "win32": false, "vms": false, "ebcdic": false,
	"nvim": false, "vim9script": true, "patch-8.2.2261": true,
}

// option answers &name for the handful a syntax file reads.
func (ev *evaluator) option(name string) value {
	switch name {
	case "iskeyword":
		return str("@,48-57,_,192-255")
	case "cpo", "cpoptions":
		return str("aABceFs")
	case "foldmethod", "fdm":
		return str("manual")
	case "foldtext", "fdt":
		return str("foldtext()")
	case "background", "bg":
		return str("dark")
	case "encoding", "enc":
		return str("utf-8")
	case "filetype", "ft":
		return str(ev.s.Filetype)
	case "syntax", "syn":
		return str(ev.s.Filetype)
	case "conceallevel", "cole":
		return num(2)
	case "compatible", "cp":
		return num(0)
	case "shiftwidth", "sw":
		return num(4)
	case "tabstop", "ts":
		return num(2)
	}
	return str("")
}

// lookup answers a bare variable reference.
func (ev *evaluator) lookup(name string, p *parser) value {
	if v, ok := ev.vars[name]; ok {
		return v
	}
	if !strings.Contains(name, ":") {
		if v, ok := ev.vars["g:"+name]; ok {
			return v
		}
	}
	switch name {
	case "v:version":
		return num(902)
	case "v:true":
		return num(1)
	case "v:false", "v:none", "v:null":
		return num(0)
	}
	if name == "g:" || name == "b:" || name == "s:" || name == "w:" {
		// The scope itself, which only ever reaches get(g:, 'x', y).
		return str("<scope:" + name + ">")
	}
	p.fail("undefined variable " + name)
	return num(0)
}

// call runs a builtin or a function the syntax file defined.
func (ev *evaluator) call(name string, args []value, p *parser) value {
	if fn, ok := ev.funcs[name]; ok {
		v, err := ev.callUser(fn, args)
		if err != nil {
			p.fail(err.Error())
		}
		return v
	}

	arg := func(i int) value {
		if i < len(args) {
			return args[i]
		}
		return str("")
	}

	switch name {
	case "has":
		if v, ok := features[arg(0).text()]; ok {
			return boolean(v)
		}
		if strings.HasPrefix(arg(0).text(), "patch-") {
			return num(1)
		}
		return num(0)
	case "exists":
		return boolean(ev.exists(arg(0).text()))
	case "exists_compiled":
		return boolean(ev.exists(arg(0).text()))
	case "get":
		// get(g:, 'name', default) and get(list, i, default). The first form
		// is every syntax file's way of asking for a setting nobody set.
		scope := arg(0)
		if scope.kind == vString && strings.HasPrefix(scope.str, "<scope:") {
			key := strings.TrimSuffix(strings.TrimPrefix(scope.str, "<scope:"), ">") + arg(1).text()
			if v, ok := ev.vars[key]; ok {
				return v
			}
			return arg(2)
		}
		if scope.kind == vList {
			i := arg(1).number()
			if i >= 0 && i < len(scope.list) {
				return scope.list[i]
			}
		}
		return arg(2)
	case "index":
		hay := arg(0)
		for i, e := range hay.list {
			if e.text() == arg(1).text() {
				return num(i)
			}
		}
		return num(-1)
	case "len", "strlen", "strwidth", "strchars":
		if arg(0).kind == vList {
			return num(len(arg(0).list))
		}
		return num(len(arg(0).text()))
	case "empty":
		v := arg(0)
		if v.kind == vList {
			return boolean(len(v.list) == 0)
		}
		if v.kind == vString {
			return boolean(v.str == "")
		}
		return boolean(v.num == 0)
	case "type":
		switch arg(0).kind {
		case vNumber:
			return num(0)
		case vString:
			return num(1)
		}
		return num(3)
	case "copy", "deepcopy":
		return arg(0)
	case "has_key":
		return num(0)
	case "toupper":
		return str(strings.ToUpper(arg(0).text()))
	case "tolower":
		return str(strings.ToLower(arg(0).text()))
	case "tr":
		from, to := arg(1).text(), arg(2).text()
		if len(from) != len(to) {
			return arg(0)
		}
		out := []byte(arg(0).text())
		for i, b := range out {
			if j := strings.IndexByte(from, b); j >= 0 {
				out[i] = to[j]
			}
		}
		return str(string(out))
	case "split":
		sep := arg(1).text()
		var parts []string
		if sep == "" {
			parts = strings.Fields(arg(0).text())
		} else {
			parts = strings.Split(arg(0).text(), sep)
		}
		list := make([]value, 0, len(parts))
		for _, s := range parts {
			list = append(list, str(s))
		}
		return value{kind: vList, list: list}
	case "join":
		sep := " "
		if len(args) > 1 {
			sep = arg(1).text()
		}
		parts := make([]string, 0, len(arg(0).list))
		for _, e := range arg(0).list {
			parts = append(parts, e.text())
		}
		return str(strings.Join(parts, sep))
	case "string":
		return str(arg(0).text())
	case "str2nr":
		n, _ := leadingNumber(arg(0).text())
		return num(n)
	case "stridx":
		return num(strings.Index(arg(0).text(), arg(1).text()))
	case "strpart":
		s := arg(0).text()
		lo := clamp(arg(1).number(), len(s))
		hi := len(s)
		if len(args) > 2 {
			hi = clamp(lo+arg(2).number(), len(s))
		}
		return str(s[lo:hi])
	case "printf":
		return str(vimPrintf(arg(0).text(), args[1:]))
	case "expand", "fnamemodify", "escape", "shellescape", "globpath", "glob":
		return str("")
	case "filereadable", "isdirectory", "executable", "did_filetype",
		"hlexists", "hlID", "bufnr", "winnr", "line", "col", "search",
		"searchpair", "foldlevel", "synID", "wincol", "winline":
		return num(0)
	case "matchstr":
		re, err := regex.Compile(arg(1).text(), regex.Options{})
		if err != nil {
			p.fail("matchstr(): " + err.Error())
			return str("")
		}
		text := arg(0).text()
		m := re.FindStringIndex(text)
		if m == nil {
			return str("")
		}
		return str(text[m[0]:m[1]])
	case "substitute":
		out, err := vimSubstitute(arg(0).text(), arg(1).text(), arg(2).text(), arg(3).text())
		if err != nil {
			p.fail("substitute(): " + err.Error())
			return str("")
		}
		return str(out)
	case "getline":
		// The buffer, which the loader does not have: one rule set is loaded
		// per filetype and shared by every buffer of that type, so there is no
		// one first line to answer with. An empty string and not a failure,
		// because a failure cascades. sh.vim opens with
		//
		//	let s:shebang = getline(1)
		//	if s:shebang =~ '^#!.\{-2,}\<ksh\>' | let b:is_kornshell = 1
		//	elseif ...
		//
		// and a getline() that errors leaves s:shebang undefined, which makes
		// every branch of that chain an error too, so the file ends up with
		// neither the dialect it asked for nor the default. An empty string
		// takes the last else, which is POSIX sh. See the package doc for what
		// that costs a bash script.
		return str("")
	case "bufname", "synIDattr", "map",
		"matchend", "matchlist", "sort", "reverse", "extend", "add",
		"remove", "keys", "values", "items", "filter", "call", "function":
		// Every one of these needs either the buffer or a funcref, and a
		// syntax file that reaches for one is doing something this package has
		// to be told about rather than guess at.
		p.fail("unimplemented function " + name + "()")
		return str("")
	}
	p.fail("unknown function " + name + "()")
	return str("")
}

// exists answers exists("..."), which is how every syntax file asks whether it
// has already run and whether a setting was set.
func (ev *evaluator) exists(what string) bool {
	switch {
	case strings.HasPrefix(what, "*"):
		_, ok := ev.funcs[what[1:]]
		return ok
	case strings.HasPrefix(what, "&"):
		return true
	case strings.HasPrefix(what, "+"):
		return features[what[1:]]
	case strings.HasPrefix(what, ":"):
		return false
	case strings.HasPrefix(what, "$"):
		return false
	}
	if _, ok := ev.vars[what]; ok {
		return true
	}
	if !strings.Contains(what, ":") {
		_, ok := ev.vars["g:"+what]
		return ok
	}
	return false
}

// vimSubstitute is substitute(), which a few syntax files build a pattern with.
//
// The pattern goes through internal/regex like every other vim pattern in this
// tree. The replacement understands & and \0 through \9 and nothing else: a
// replacement starting \= is a vimscript expression evaluated per match, which
// is a second evaluator inside the first, and yaml.vim -- the only file in the
// runtime that needs it -- wants it to build a character class out of another
// character class. That one is refused by name.
func vimSubstitute(text, pat, sub, flags string) (string, error) {
	if strings.HasPrefix(sub, `\=`) {
		return "", errSubExpr
	}
	re, err := regex.Compile(pat, regex.Options{IgnoreCase: strings.Contains(flags, "i")})
	if err != nil {
		return "", err
	}
	all := strings.Contains(flags, "g")

	var b strings.Builder
	at := 0
	for at <= len(text) {
		m := re.FindStringSubmatchIndex(text[at:])
		if m == nil {
			break
		}
		b.WriteString(text[at : at+m[0]])
		b.WriteString(expandSub(sub, text[at:], m))
		next := at + m[1]
		if m[1] == m[0] {
			// A match of nothing: copy one byte and move on, or this loops.
			if at+m[0] < len(text) {
				b.WriteByte(text[at+m[0]])
			}
			next = at + m[0] + 1
		}
		at = next
		if !all {
			break
		}
	}
	if at < len(text) {
		b.WriteString(text[at:])
	}
	return b.String(), nil
}

// errSubExpr is substitute() with a \= replacement.
var errSubExpr = &exprError{src: `\=`, why: "a substitute() replacement that is an expression"}

// expandSub writes one replacement, resolving & and \0 through \9.
func expandSub(sub, text string, m []int) string {
	var b strings.Builder
	group := func(n int) string {
		if 2*n+1 < len(m) && m[2*n] >= 0 {
			return text[m[2*n]:m[2*n+1]]
		}
		return ""
	}
	for i := 0; i < len(sub); i++ {
		switch {
		case sub[i] == '&':
			b.WriteString(group(0))
		case sub[i] == '\\' && i+1 < len(sub):
			i++
			switch c := sub[i]; {
			case c >= '0' && c <= '9':
				b.WriteString(group(int(c - '0')))
			case c == '&':
				b.WriteByte('&')
			case c == '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(c)
			}
		default:
			b.WriteByte(sub[i])
		}
	}
	return b.String()
}

// vimPrintf is printf() with the three verbs a syntax file uses. Anything else
// comes back as the format string, which is visible and wrong rather than
// invisible and wrong.
func vimPrintf(format string, args []value) string {
	var b strings.Builder
	ai := 0
	next := func() value {
		if ai < len(args) {
			ai++
			return args[ai-1]
		}
		return str("")
	}
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case 's':
			b.WriteString(next().text())
		case 'd':
			b.WriteString(strconv.Itoa(next().number()))
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	return b.String()
}
