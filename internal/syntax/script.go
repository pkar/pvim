package syntax

import (
	"strings"
)

// The statement half of the vimscript subset: enough of a runner to get from
// the top of a syntax file to the bottom of it with the right rules defined.
//
// What it runs: line continuations, if/elseif/else/endif, for/endfor,
// function/endfunction and a call to one, let, unlet, finish, execute,
// runtime, source, syn, hi and try/catch as a pass-through. What it refuses,
// by name, on the line it stood: everything else, which in the shipped runtime
// is while loops, dictionary-valued lets and the two or three files that build
// a command out of a string this evaluator cannot read.

// runner executes one file.
type runner struct {
	s    *Syntax
	ev   *evaluator
	rt   Runtime
	file string
	// path is the file being sourced, on disk, which is what <sfile> expands
	// to.
	path string

	stop      bool // :finish
	returning bool // :return, inside a function
	ret       value
}

// join folds vim's line continuations into the lines they continue.
//
// A line whose first non-blank character is a backslash is glued to the one
// before it with the backslash and the blanks in front of it removed, and a
// line starting `"\ ` is a comment inside a continuation and disappears. The
// go, python and markdown syntax files all rely on this and none of them would
// parse without it.
func join(src []string) []string {
	var out []string
	for _, line := range src {
		t := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(t, `"\ `), t == `"\`:
			continue
		case strings.HasPrefix(t, `\`) && len(out) > 0:
			out[len(out)-1] += t[1:]
		default:
			out = append(out, line)
		}
	}
	return out
}

// block runs a run of lines, honouring the control flow in them.
func (r *runner) block(lines []string) {
	// conds is the if/elseif/else stack: taken says a branch has already run,
	// live says this branch is the one running.
	type cond struct{ taken, live bool }
	var conds []cond
	live := func() bool {
		for _, c := range conds {
			if !c.live {
				return false
			}
		}
		return true
	}

	for i := 0; i < len(lines) && !r.stop && !r.returning; i++ {
		cmd := strings.TrimLeft(lines[i], " \t")
		cmd = strings.TrimPrefix(cmd, ":")
		name, rest := commandName(cmd)
		name = strings.TrimSuffix(name, "!")

		switch name {
		case "if":
			if !live() {
				conds = append(conds, cond{taken: true, live: false})
				continue
			}
			v := r.condition(rest, i)
			conds = append(conds, cond{taken: v, live: v})
		case "elseif":
			if len(conds) == 0 {
				continue
			}
			top := &conds[len(conds)-1]
			if top.taken {
				top.live = false
				continue
			}
			v := r.condition(rest, i)
			top.taken, top.live = v, v
		case "else":
			if len(conds) == 0 {
				continue
			}
			top := &conds[len(conds)-1]
			top.live = !top.taken
			top.taken = true
		case "endif":
			if len(conds) > 0 {
				conds = conds[:len(conds)-1]
			}
		case "function", "func":
			end := matchingEnd(lines, i, []string{"function", "func"}, []string{"endfunction", "endfunc"})
			if live() {
				r.defineFunction(lines[i], lines[i+1:end])
			}
			i = end
		case "for":
			end := matchingEnd(lines, i, []string{"for"}, []string{"endfor"})
			if live() {
				r.loop(rest, lines[i+1:end], i)
			}
			i = end
		case "while":
			end := matchingEnd(lines, i, []string{"while"}, []string{"endwhile"})
			if live() {
				r.refuse(i, cmd, ScriptError{})
			}
			i = end
		case "try", "endtry", "finally", "endwhile", "endfor", "endfunction", "endfunc":
			// try and its friends are pass-throughs: the body runs and a
			// failure in it is already a named refusal rather than an
			// exception, so there is nothing to catch.
		case "catch":
			// Everything from a catch to the endtry is the error path, and
			// nothing here throws.
			i = matchingEnd(lines, i, []string{"try"}, []string{"endtry"})
		default:
			if end, ok := heredoc(lines, i); ok {
				if live() {
					r.assignHeredoc(lines[i], lines[i+1:end])
				}
				i = end
				continue
			}
			if live() {
				r.exec(cmd, i)
			}
		}
	}
}

// condition evaluates an if or elseif expression. An expression this package
// cannot read is false and is recorded, because the else branch of every
// condition in the shipped runtime is the plain rule and the then branch is
// the extra one.
func (r *runner) condition(expr string, line int) bool {
	v, err := r.ev.eval(expr)
	if err != nil {
		r.refuseErr(line, "if "+expr, err)
		return false
	}
	return v.truthy()
}

// loop runs a `for x in list` over the list.
func (r *runner) loop(rest string, body []string, line int) {
	name, in, ok := strings.Cut(rest, " in ")
	if !ok {
		r.refuse(line, "for "+rest, ScriptError{})
		return
	}
	name = strings.TrimSpace(name)
	v, err := r.ev.eval(strings.TrimSpace(in))
	if err != nil {
		r.refuseErr(line, "for "+rest, err)
		return
	}
	if v.kind != vList {
		r.refuse(line, "for "+rest, ScriptError{})
		return
	}
	for _, e := range v.list {
		r.ev.vars[name] = e
		r.block(body)
		if r.stop || r.returning {
			return
		}
	}
}

// defineFunction records a function so a later call can run it.
func (r *runner) defineFunction(header string, body []string) {
	_, rest := commandName(strings.TrimLeft(header, " \t"))
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "!"))
	open := strings.IndexByte(rest, '(')
	if open < 0 {
		return
	}
	name := strings.TrimSpace(rest[:open])
	close := strings.IndexByte(rest[open:], ')')
	if close < 0 {
		return
	}
	var params []string
	for _, p := range strings.Split(rest[open+1:open+close], ",") {
		if p = strings.TrimSpace(p); p != "" {
			params = append(params, p)
		}
	}
	r.ev.funcs[name] = &function{name: name, params: params, body: body}
}

// callUser runs a function the file defined and returns what it returned.
func (ev *evaluator) callUser(fn *function, args []value) (value, error) {
	if ev.depth > 16 {
		return num(0), &exprError{src: fn.name, why: "recursion"}
	}
	ev.depth++
	defer func() { ev.depth-- }()

	saved := map[string]value{}
	var keys []string
	set := func(k string, v value) {
		if old, ok := ev.vars[k]; ok {
			saved[k] = old
		}
		keys = append(keys, k)
		ev.vars[k] = v
	}
	extra := 0
	for i, p := range fn.params {
		if p == "..." {
			extra = len(args) - i
			for j := i; j < len(args); j++ {
				set("a:"+itoa(j-i+1), args[j])
			}
			break
		}
		if i < len(args) {
			set("a:"+p, args[i])
		} else {
			set("a:"+p, str(""))
		}
	}
	if extra < 0 {
		extra = 0
	}
	set("a:0", num(extra))
	defer func() {
		for _, k := range keys {
			if old, ok := saved[k]; ok {
				ev.vars[k] = old
			} else {
				delete(ev.vars, k)
			}
		}
	}()

	sub := &runner{s: ev.s, ev: ev, file: "<function " + fn.name + ">"}
	sub.block(fn.body)
	if !sub.returning {
		return num(0), nil
	}
	return sub.ret, nil
}

// exec runs one ordinary command line.
func (r *runner) exec(cmd string, line int) {
	name, rest := commandName(cmd)
	bang := strings.HasSuffix(name, "!")
	name = strings.TrimSuffix(name, "!")
	switch name {
	case "", "\"":
		return
	case "finish", "fini", "finis":
		r.stop = true
	case "return", "retu", "retur":
		r.returning = true
		if strings.TrimSpace(rest) == "" {
			r.ret = num(0)
			return
		}
		v, err := r.ev.eval(rest)
		if err != nil {
			r.refuseErr(line, cmd, err)
		}
		r.ret = v
	case "let":
		r.let(rest, line, cmd)
	case "unlet":
		for {
			var w string
			w, rest = word(strings.TrimPrefix(strings.TrimSpace(rest), "!"))
			if w == "" {
				return
			}
			delete(r.ev.vars, w)
			if !strings.Contains(w, ":") {
				delete(r.ev.vars, "g:"+w)
			}
		}
	case "syn", "sy", "synt", "synta", "syntax":
		r.s.synCommand(rest, r.at(line))
	case "hi", "highlight", "hig", "high", "highl", "highli", "highlig", "highligh":
		r.highlight(rest, line)
	case "exe", "exec", "execu", "execut", "execute":
		v, err := r.ev.eval(rest)
		if err != nil {
			r.refuseErr(line, cmd, err)
			return
		}
		r.exec(strings.TrimLeft(v.text(), " \t:"), line)
	case "runtime", "ru", "runt", "runti", "runtim":
		r.runtime(rest, bang, line)
	case "source", "so", "sou", "sour", "sourc":
		r.source(strings.TrimSpace(rest), line)
	case "set", "se", "setlocal", "setl", "setglobal", "setg",
		"delcommand", "command", "com", "comm", "comma", "comman",
		"augroup", "aug", "au", "autocmd", "doautocmd", "echo", "echom",
		"echomsg", "echoerr", "echohl", "call", "silent", "normal", "nnoremap",
		"noremap", "map", "sil", "filetype", "delfunction", "lockvar",
		"unlockvar", "scriptencoding", "language", "verbose":
		// Editor state, output and mappings. A syntax file that sets an
		// option is telling the editor how to fold or how wide a tab is, and
		// none of that reaches the highlighter. `call` is here because every
		// call in the shipped syntax files is to a function whose only effect
		// is one of the above.
	default:
		r.refuse(line, cmd, ScriptError{})
	}
}

// let assigns a variable, or refuses the shapes this package does not model.
func (r *runner) let(rest string, line int, cmd string) {
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		return // `:let` with no assignment prints, and there is nowhere to print
	}
	lhs := strings.TrimSpace(rest[:eq])
	op := ""
	for _, o := range []string{".", "+", "-", "*", "/", "%", "."} {
		if strings.HasSuffix(lhs, o) {
			op = o
			lhs = strings.TrimSpace(strings.TrimSuffix(lhs, o))
			break
		}
	}
	rhs := rest[eq+1:]

	if strings.HasPrefix(lhs, "&") || strings.HasPrefix(lhs, "@") ||
		strings.ContainsAny(lhs, "{[") {
		// An option, a register, or a name built out of an expression.
		// markdown's `let b:{matchstr(...)}_subtype` is the third, and it only
		// runs for a fenced language nobody configured.
		if strings.ContainsAny(lhs, "{[") {
			r.refuse(line, cmd, ScriptError{})
		}
		return
	}

	v, err := r.ev.eval(rhs)
	if err != nil {
		r.refuseErr(line, cmd, err)
		return
	}
	if op != "" {
		old := r.ev.vars[lhs]
		switch op {
		case ".":
			v = str(old.text() + v.text())
		case "+":
			v = num(old.number() + v.number())
		case "-":
			v = num(old.number() - v.number())
		}
	}
	r.ev.vars[lhs] = v
}

// highlight runs a `hi` line. Only the link forms mean anything here: colours
// belong to the colourscheme, and a syntax file that sets one is overruled by
// it in vim too as soon as `hi def` is what it wrote.
func (r *runner) highlight(rest string, line int) {
	rest = strings.TrimSpace(rest)
	def := false
	if w, more := word(rest); w == "default" || w == "def" {
		def, rest = true, strings.TrimSpace(more)
	}
	w, more := word(rest)
	if w != "link" && w != "lin" && w != "li" && w != "l" {
		// `hi Group guifg=...` and `hi clear`. Recorded rather than run: this
		// package resolves a group to a name and the highlight table owns what
		// that name looks like.
		at := r.at(line)
		at.Detail = "hi " + rest
		r.s.refuse(OptionError{Refusal: at})
		return
	}
	from, more := word(more)
	to, _ := word(more)
	if from == "" || to == "" {
		return
	}
	if def {
		if _, ok := r.s.links[from]; ok {
			return
		}
	}
	if to == "NONE" {
		delete(r.s.links, from)
		return
	}
	r.s.links[from] = to
}

// heredoc finds the end of a `let x =<< [trim] END` block, which is how
// html.vim writes its 150-entry list of ARIA attribute names.
func heredoc(lines []string, i int) (int, bool) {
	cmd := strings.TrimLeft(lines[i], " \t")
	name, rest := commandName(cmd)
	if strings.TrimSuffix(name, "!") != "let" || !strings.Contains(rest, "=<<") {
		return 0, false
	}
	_, after, _ := strings.Cut(rest, "=<<")
	term := strings.TrimSpace(after)
	term = strings.TrimSpace(strings.TrimPrefix(term, "trim"))
	if term == "" {
		return 0, false
	}
	for j := i + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == term {
			return j, true
		}
	}
	return len(lines) - 1, true
}

// assignHeredoc stores the block as a list of strings, trimmed the way `trim`
// asks: the indent of the terminator line is taken off every line.
func (r *runner) assignHeredoc(header string, body []string) {
	_, rest := commandName(strings.TrimLeft(header, " \t"))
	lhs, after, _ := strings.Cut(rest, "=<<")
	lhs = strings.TrimSpace(lhs)
	trim := strings.HasPrefix(strings.TrimSpace(after), "trim")
	list := make([]value, 0, len(body))
	for _, l := range body {
		if trim {
			l = strings.TrimLeft(l, " \t")
		}
		list = append(list, str(l))
	}
	r.ev.vars[lhs] = value{kind: vList, list: list}
}

// commandName splits a command line into its name and the rest.
func commandName(cmd string) (string, string) {
	cmd = strings.TrimLeft(cmd, " \t")
	if cmd == "" || strings.HasPrefix(cmd, `"`) {
		return "", ""
	}
	i := 0
	for i < len(cmd) && (cmd[i] >= 'a' && cmd[i] <= 'z' || cmd[i] >= 'A' && cmd[i] <= 'Z') {
		i++
	}
	if i < len(cmd) && cmd[i] == '!' {
		return cmd[:i+1], cmd[i+1:]
	}
	return cmd[:i], cmd[i:]
}

// matchingEnd finds the line that closes the block opened at i, allowing for
// blocks of the same kind nested inside it.
func matchingEnd(lines []string, i int, open, close []string) int {
	depth := 0
	for j := i; j < len(lines); j++ {
		name, _ := commandName(strings.TrimLeft(lines[j], " \t"))
		name = strings.TrimSuffix(name, "!")
		if contains1(open, name) {
			depth++
		}
		if contains1(close, name) {
			depth--
			if depth <= 0 {
				return j
			}
		}
	}
	return len(lines) - 1
}

func contains1(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// at builds the location every refusal from this file carries.
func (r *runner) at(line int) Refusal {
	return Refusal{File: r.file, Line: line + 1}
}

func (r *runner) refuse(line int, cmd string, _ ScriptError) {
	at := r.at(line)
	at.Detail = strings.TrimSpace(cmd)
	r.s.refuse(ScriptError{Refusal: at})
}

func (r *runner) refuseErr(line int, cmd string, err error) {
	at := r.at(line)
	at.Detail = strings.TrimSpace(cmd) + ": " + err.Error()
	r.s.refuse(ScriptError{Refusal: at})
}

// itoa is strconv.Itoa for the small non-negative numbers a:1 through a:20 are.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
