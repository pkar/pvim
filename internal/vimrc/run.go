package vimrc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pkar/pvim/internal/ex"
)

// Loader runs a vimscript file against a Sink.
//
// It is a struct and not a function because three things outlive one file.
// 'mapleader' is read at the moment a :map command runs, so a file that sets
// it and a file it sources have to agree on the value. The variables a script
// has set are what exists() answers about, and the four nofrils files each
// ask exists() about a variable they then set. And the names of the functions
// a load stepped over decide whether a later :call is a logged refusal or an
// E117.
type Loader struct {
	// Sink is where statements go. Required.
	Sink Sink
	// Env is what the expressions ask questions of. Required.
	Env Env

	// Leader and LocalLeader are 'mapleader' and 'maplocalleader'. Vim's
	// default for both is a backslash, which is what <Leader> means until a
	// script sets one.
	Leader, LocalLeader string

	// Vars is every variable a load has assigned, under its scope-qualified
	// name: a bare "NERDTreeWinSize" in a vimrc is "g:NERDTreeWinSize", which
	// is how vim stores it and what exists() has to find.
	Vars map[string]Val

	// funcs is the functions a load stepped over.
	funcs map[string]bool

	// group is the augroup a following :autocmd belongs to.
	group string

	// file is the file being run, which is what expand('<sfile>') answers and
	// what a Pos carries. It is per-file and Run sets it.
	file string

	// The per-file state, reset by Run.
	res      *Result
	ifs      []ifState
	inFunc   bool
	finished bool

	// funcPos and funcText are the ":function" that opened the block inFunc
	// is inside, kept because vim reports "E126: Missing :endfunction"
	// against the ":function" line itself and not against the end of the
	// file. Measured a file whose line 1 is "function! Foo()"
	// and whose line 2 is "set tw=3" prints "line 1:" over the E126.
	funcPos  Pos
	funcText string
}

// ifState is one level of ":if" nesting.
type ifState struct {
	// running says the statements at this level execute.
	running bool
	// taken says a branch at this level has already run, so no :elseif and no
	// :else may.
	taken bool
	// outer says the enclosing level was running. A nested :if inside a
	// branch that is being skipped never runs, whatever its condition says,
	// and its condition is never evaluated: the vimrc's line 193 sits inside
	// "if has('autocmd')" and would otherwise call filereadable() on a path
	// that is not there.
	outer bool
}

// NewLoader returns a loader with vim's defaults for the two leaders.
func NewLoader(sink Sink, env Env) *Loader {
	return &Loader{
		Sink:        sink,
		Env:         env,
		Leader:      `\`,
		LocalLeader: `\`,
		Vars:        map[string]Val{},
		funcs:       map[string]bool{},
	}
}

// takeScriptScope removes the "s:" variables from the loader and returns them,
// so that the file about to run starts with an empty script scope.
func (l *Loader) takeScriptScope() map[string]Val {
	saved := map[string]Val{}
	for name, v := range l.Vars {
		if strings.HasPrefix(name, "s:") {
			saved[name] = v
			delete(l.Vars, name)
		}
	}
	return saved
}

// restoreScriptScope puts a saved script scope back, dropping whatever the
// file that has just ended left behind.
func (l *Loader) restoreScriptScope(saved map[string]Val) {
	for name := range l.Vars {
		if strings.HasPrefix(name, "s:") {
			delete(l.Vars, name)
		}
	}
	for name, v := range saved {
		l.Vars[name] = v
	}
}

// Run parses src and feeds every statement to the sink.
//
// file is the name that goes in each Pos and in each error message. Errors do
// not stop it: vim prints each one, with the file and the line, and carries on
// to the next statement, and a loader that stopped at the first one would turn
// a plugin call that slipped into the vimrc into an editor with no
// configuration at all. The gate is that the real ~/.vimrc comes back
// with Result.Errors empty.
func (l *Loader) Run(src []byte, file string) *Result {
	l.res = &Result{}
	l.ifs = nil
	l.inFunc = false
	l.finished = false

	outerFile := l.file
	l.file = file
	// A file gets its own "s:" scope and gives it back when it ends, which is
	// what script-local MEANS: the live vimrc's s:vim_home has to be invisible
	// to the colourscheme it loads on line 9, and a colourscheme that used the
	// same name would otherwise overwrite the path the next six lines build
	// 'undodir' out of. Vim keeps one scope dictionary per sourced script;
	// this keeps one at a time because nothing here reads a scope it is not
	// inside.
	outerScope := l.takeScriptScope()
	defer func() {
		l.file = outerFile
		l.restoreScriptScope(outerScope)
	}()

	for _, ll := range logicalLines(src, file) {
		l.runLine(ll)
		if l.finished {
			l.res.Finished = true
			break
		}
	}
	l.endOfFile(file, lineCount(src)+1)
	return l.res
}

// endOfFile reports the blocks the file left open.
//
// Both of them, and in this order, because vim reports both and in this
// order. Measured on a file of "if 1", "function! Foo()",
// " set tw=3": vim prints E126 against the ":function" line and E171 after
// it. A file that just runs off the end inside a ":function" is the third of
// the three failure modes this package's doc comment names -- being silent
// about refusing something -- because everything after the ":function" is
// swallowed and, before this, nothing said so.
//
// eof is the line vim blames a missing :endif on, which is one past the last
// physical line of the file and not the line of the ":if". Measured
// a two-line file says "line 3" and a five-line file says
// "line 6".
func (l *Loader) endOfFile(file string, eof int) {
	if l.finished {
		// A ":finish" inside an open ":if" is silent in vim, measured
		// on "if 1" / "finish": no E171, no message at all. It
		// is not an unclosed block, it is a file that chose to stop.
		return
	}
	if l.inFunc {
		l.fail(l.funcPos, l.funcText, errors.New("E126: Missing :endfunction"))
		l.inFunc = false
	}
	if len(l.ifs) > 0 {
		l.fail(Pos{File: file, Line: eof}, "", errors.New("E171: Missing :endif"))
		l.ifs = nil
	}
}

// Line runs one command line, which is what an autocommand's command is.
//
// The vimscript subset only. An autocommand whose command is an ordinary ex
// command -- the ":%s/\s\+$//e" the nested autocommand registers is the one
// in this vimrc -- belongs to internal/ex and comes back here as an E492. The
// caller that fires autocommands is the one that knows both, and it tries the
// ex layer first.
//
// It is the other entry point and it is here for the vimrc's line 193:
//
//	au BufWritePost .vimrc,... so $MYVIMRC | if has('gui_running') && filereadable($MYGVIMRC) | so $MYGVIMRC | endif
//
// The :autocmd command swallows that whole string, bars and all, so nothing
// splits it at registration time. It gets split when the autocommand fires and
// the string is run as a command line, and that is this method: four commands
// with an :if across two of the bars.
func (l *Loader) Line(text string, pos Pos) *Result {
	l.res = &Result{}
	l.ifs = nil
	l.inFunc = false
	l.finished = false
	l.file = pos.File

	l.runLine(logicalLine{text: text, pos: pos})
	if l.inFunc {
		l.fail(pos, text, errors.New("E126: Missing :endfunction"))
		l.inFunc = false
	}
	if len(l.ifs) > 0 {
		l.fail(pos, text, errors.New("E171: Missing :endif"))
		l.ifs = nil
	}
	return l.res
}

// Run parses src and feeds every statement to sink. It is the one-shot form
// of Loader for a caller with one file and no state to carry.
func Run(src []byte, file string, sink Sink, env Env) *Result {
	return NewLoader(sink, env).Run(src, file)
}

// running reports whether statements execute right now.
func (l *Loader) running() bool {
	return len(l.ifs) == 0 || l.ifs[len(l.ifs)-1].running
}

// fail records a diagnostic and tells the sink, which is what puts an E-code
// on the message line.
func (l *Loader) fail(pos Pos, text string, err error) {
	l.res.Errors = append(l.res.Errors, Diag{Pos: pos, Text: text, Err: err})
	_ = l.Sink.Unknown(Unknown{Pos: pos, Text: text, Err: err})
}

// drop records a statement that was logged and not run.
func (l *Loader) drop(pos Pos, name, text string, why IgnoreReason) {
	i := Ignored{Pos: pos, Name: name, Text: text, Reason: why}
	l.res.Ignored = append(l.res.Ignored, i)
	if err := l.Sink.Ignored(i); err != nil {
		l.fail(pos, text, err)
	}
}

// apply hands a statement to the sink and records whatever it says about it.
func (l *Loader) apply(pos Pos, text string, err error) {
	if err != nil {
		l.fail(pos, text, err)
	}
}

// runLine runs every command on one logical line.
func (l *Loader) runLine(ll logicalLine) {
	text := ll.text
	for {
		h := parseHeader(text)
		if !h.ok {
			if l.inFunc || strings.TrimSpace(text) == "" {
				return
			}
			if !l.running() {
				// A command nobody recognises inside a branch that is being
				// skipped is not an error, and the :endif after it still has
				// to be found: "if has('nvim') | lua ... | endif" has to close
				// its own :if on a build with no lua in it.
				_, rest, found := splitBar(text)
				if !found {
					return
				}
				text = rest
				continue
			}
			l.fail(ll.pos, strings.TrimSpace(text), fmt.Errorf("%w: %s", ex.ErrNotAnEditorCommand, strings.TrimSpace(text)))
			return
		}
		rest := l.exec(h, ll.pos, text)
		if l.finished || rest == "" {
			return
		}
		text = rest
	}
}

// skippedRest is what a command gives up when nothing on this line is
// running.
//
// A barExpr command finds its own bar by parsing an expression, and inside a
// branch that is not running there is no parse: doIf never evaluates a
// condition whose enclosing level is skipped, on purpose, so that the vimrc's
// line 193 does not call filereadable() on a path that is not there. The
// :endif behind that line's two bars still has to be found, which is what
// splitBar is for here.
func skippedRest(h header) string {
	if h.spec.bar != barExpr {
		return h.rest
	}
	_, rest, found := splitBar(h.args)
	if !found {
		return ""
	}
	return rest
}

// exec runs one command.
//
// The order of the three gates is the whole of the control flow: a
// ":function" body swallows everything but its ":endfunction", the ":if"
// family is read even while a branch is being skipped so that the nesting
// stays right, and everything else runs only when no branch is skipping it.
func (l *Loader) exec(h header, pos Pos, line string) string {
	if l.inFunc {
		if h.spec.kind == kindEndFunction {
			l.inFunc = false
		}
		return h.rest
	}

	switch h.spec.kind {
	case kindIf:
		return l.doIf(h, pos, line)
	case kindElseIf:
		return l.doElseIf(h, pos, line)
	case kindElse:
		l.doElse(pos, line)
		return h.rest
	case kindEndIf:
		if len(l.ifs) == 0 {
			l.fail(pos, line, errors.New("E580: :endif without :if"))
			return h.rest
		}
		l.ifs = l.ifs[:len(l.ifs)-1]
		return h.rest
	case kindFunction:
		if l.running() {
			l.startFunction(h, pos, line)
		} else {
			l.inFunc = true
			l.funcPos, l.funcText = pos, strings.TrimSpace(line)
		}
		return h.rest
	case kindEndFunction:
		l.fail(pos, line, errors.New("E193: :endfunction not inside a function"))
		return h.rest
	}

	if !l.running() {
		return skippedRest(h)
	}
	return l.run(h, pos, line)
}

// doIf pushes a level, evaluating the condition only when the enclosing level
// is running.
func (l *Loader) doIf(h header, pos Pos, line string) string {
	outer := l.running()
	if !outer {
		l.ifs = append(l.ifs, ifState{})
		return skippedRest(h)
	}
	v, rest, err := l.condition(h.args)
	run := false
	if err != nil {
		l.fail(pos, line, err)
		// A condition that did not evaluate is a branch that does not run,
		// and the :endif still has to find its :if.
	} else {
		run = v
	}
	l.ifs = append(l.ifs, ifState{running: run, taken: run, outer: outer})
	return rest
}

func (l *Loader) doElseIf(h header, pos Pos, line string) string {
	if len(l.ifs) == 0 {
		l.fail(pos, line, errors.New("E581: :elseif without :if"))
		return skippedRest(h)
	}
	st := &l.ifs[len(l.ifs)-1]
	if !st.outer || st.taken {
		st.running = false
		return skippedRest(h)
	}
	v, rest, err := l.condition(h.args)
	if err != nil {
		l.fail(pos, line, err)
		st.running = false
		return rest
	}
	st.running, st.taken = v, v
	return rest
}

func (l *Loader) doElse(pos Pos, line string) {
	if len(l.ifs) == 0 {
		l.fail(pos, line, errors.New("E581: :else without :if"))
		return
	}
	st := &l.ifs[len(l.ifs)-1]
	st.running = st.outer && !st.taken
	st.taken = true
}

// condition evaluates an ":if" argument and checks what follows it, which is
// nothing or a comment.
func (l *Loader) condition(args string) (bool, string, error) {
	v, n, err := eval(args, l.Env, l.Vars, l.file)
	if err != nil {
		// The expression did not parse, so it cannot say where it stopped.
		// Guess, so that a bad condition still lets its :endif be found.
		_, rest, _ := splitBar(args)
		return false, rest, err
	}
	rest, err := after(args[n:])
	if err != nil {
		return false, "", err
	}
	if v.Kind == ValueList || v.Kind == ValueDict {
		return false, rest, fmt.Errorf("E745: Using a %s as a Number", v.Kind)
	}
	return v.Truthy(), rest, nil
}

// after says what follows an expression, and gives back the next command on
// the line when there is one.
//
// Vim's ends_excmd2 ends a command at the end of the line, at a bar or at a
// double quote, and its check_nextcmd takes what is after a bar and nothing
// else. That pair is the whole rule, and both halves of it matter here.
//
// The comment half is where a ":let" finds its comment. `let
// g:go_metalinter_enabled = ['all'] " ['vet', 'revive', ...]` sets a
// one-element list, measured, and it does so because the
// expression parser stops after the "]" and what follows is a comment. A
// comment stripper run first would have cut `let g:vim_ai_debug_log_file =
// "/tmp/vim_ai_debug.log"` in half.
//
// The bar half is why the comment swallows it. Measured
// `let g:x = 1 " a " | set sts=9` leaves 'softtabstop' alone, because the
// bar is inside the comment, while `let g:x = "a|b" | set et` sets both,
// because that bar is not.
func after(rest string) (string, error) {
	rest = strings.TrimLeft(rest, " \t")
	switch {
	case rest == "", strings.HasPrefix(rest, "\""):
		return "", nil
	case strings.HasPrefix(rest, "|"):
		return rest[1:], nil
	}
	return "", fmt.Errorf("E488: Trailing characters: %s", rest)
}

// trailing checks what is left after an expression and throws away the next
// command. It is after() for a caller that only wants to know whether the
// remainder was legal.
func trailing(rest string) error {
	_, err := after(rest)
	return err
}

// startFunction steps over a ":function" definition and logs it.
func (l *Loader) startFunction(h header, pos Pos, line string) {
	l.inFunc = true
	l.funcPos, l.funcText = pos, strings.TrimSpace(line)
	name, _, _ := strings.Cut(h.args, "(")
	name = strings.TrimSpace(name)
	if name != "" {
		l.funcs[name] = true
	}
	l.drop(pos, name, strings.TrimSpace(line), IgnoreFunc)
}

// run is the statement dispatch, reached only when nothing is skipping.
func (l *Loader) run(h header, pos Pos, line string) string {
	text := strings.TrimSpace(line)

	switch h.spec.kind {
	case kindSet:
		args := stripComment(h.args)
		if args == "" {
			return h.rest // ":set" on its own lists every option
		}
		l.apply(pos, text, l.Sink.SetOption(SetOption{Pos: pos, Scope: h.spec.scope, Args: args}))

	case kindLet:
		return l.doLet(h, pos, text)

	case kindUnlet:
		for _, name := range strings.Fields(stripComment(h.args)) {
			name = strings.TrimPrefix(name, "!")
			full := qualify(name)
			delete(l.Vars, full)
			l.apply(pos, text, l.Sink.Let(Let{Pos: pos, Name: full, Unlet: true}))
		}

	case kindMap, kindUnmap, kindMapClear:
		m, err := parseMap(h.spec, h.args, h.bang, l.Leader, l.LocalLeader, pos)
		if err != nil {
			l.fail(pos, text, err)
			return h.rest
		}
		l.apply(pos, text, l.Sink.Map(m))

	case kindAutoCmd:
		a, err := parseAutoCmd(h.args, h.bang, l.group, pos)
		if err != nil {
			l.fail(pos, text, err)
			return h.rest
		}
		l.apply(pos, text, l.Sink.AutoCmd(a))

	case kindAugroup:
		name := stripComment(h.args)
		if strings.EqualFold(name, "END") {
			l.group = ""
			return h.rest
		}
		l.group = name

	case kindCommand:
		c, err := parseCommandDef(h.args, h.bang, pos)
		if err != nil {
			l.fail(pos, text, err)
			return h.rest
		}
		if c.Name == "" {
			return h.rest // ":command" on its own lists
		}
		l.apply(pos, text, l.Sink.Command(c))

	case kindDelCommand:
		name := stripComment(h.args)
		l.apply(pos, text, l.Sink.Command(Command{Pos: pos, Name: name, Delete: true}))

	case kindColorscheme:
		name := stripComment(h.args)
		if name == "" {
			return h.rest // ":colorscheme" on its own prints the current one
		}
		l.apply(pos, text, l.Sink.Colorscheme(Colorscheme{Pos: pos, Name: name}))

	case kindHighlight:
		hl, err := parseHighlight(stripComment(h.args), pos)
		if err != nil {
			l.fail(pos, text, err)
			return h.rest
		}
		l.apply(pos, text, l.Sink.Highlight(hl))

	case kindSource:
		if h.bang {
			l.fail(pos, text, errors.New("E477: :source! reads a file as normal-mode keys, which pvim does not do"))
			return h.rest
		}
		raw := stripComment(h.args)
		l.apply(pos, text, l.Sink.Source(Source{Pos: pos, Path: l.Env.Expand(raw), Raw: raw}))

	case kindFinish:
		l.finished = true

	case kindCall:
		l.doCall(h, pos, text)

	case kindExecute:
		return l.doExecute(h, pos, text)

	case kindPack:
		l.apply(pos, text, l.Sink.Pack(Pack{Pos: pos, Bang: h.bang}))

	case kindFileType:
		ft, err := parseFileType(pos, stripComment(h.args))
		if err != nil {
			l.fail(pos, text, err)
			return h.rest
		}
		l.apply(pos, text, l.Sink.FileType(ft))

	case kindSyntax:
		arg := strings.ToLower(strings.TrimSpace(stripComment(h.args)))
		if i := strings.IndexAny(arg, " \t"); i >= 0 {
			arg = arg[:i]
		}
		l.apply(pos, text, l.Sink.Syntax(Syntax{Pos: pos, Arg: arg, Args: h.args}))

	case kindInert:
		l.drop(pos, "", text, IgnoreInert)

	case kindEx:
		cmd := ex.Cmd{Name: h.spec.name, Typed: h.typed, Bang: h.bang, Args: h.args, Line: text}
		l.apply(pos, text, l.Sink.Ex(Ex{Pos: pos, Cmd: cmd, Line: text}))
	}
	return h.rest
}

// doCall runs a ":call".
//
// Three answers and they are all different events. A call of one of the ten
// builtins runs, because mkdir() is the same function whether it is reached
// from an expression or from a statement and the live vimrc reaches it from
// here three times. A call of a function this load stepped over is logged and
// dropped, which is what the four nofrils colourschemes end with. Anything
// else is E117, naming the function.
func (l *Loader) doCall(h header, pos Pos, text string) {
	args := strings.TrimSpace(h.args)
	name, _, _ := strings.Cut(args, "(")
	name = strings.TrimSpace(name)

	if _, ok := builtins[name]; ok {
		// The return value is thrown away, which is what a ":call" is for.
		_, n, err := eval(args, l.Env, l.Vars, l.file)
		if err != nil {
			l.fail(pos, text, err)
			return
		}
		if err := trailing(args[n:]); err != nil {
			l.fail(pos, text, err)
		}
		return
	}
	if l.funcs[name] {
		l.drop(pos, name, text, IgnoreCall)
		return
	}
	l.fail(pos, text, fmt.Errorf("%w: %s", ErrNoFunc, name))
}

// ErrExecuteScope is what a ":execute" that resolved to something outside the
// closed set says.
//
// It is pvim's refusal and not one of vim's codes, because vim has no such
// refusal: its ":execute" runs whatever it built, control flow and nested
// ":execute" included, and that is exactly the door this package closes by
// ruling out execute-with-string-building. E15 rather than a code of its own
// so that the number means what it means everywhere else here: an expression
// this layer will not evaluate.
var ErrExecuteScope = errors.New("E15: Invalid expression: :execute may build a command this loader already has, not control flow and not another :execute")

// doExecute runs pvim's restricted ":execute".
//
// The two halves of "restricted" are both here. The argument goes through the
// same evaluator every other expression in this package goes through, so it is
// string literals, variables, the ten builtins and concatenation and nothing
// else; and the string that comes out has to be a command in the table above,
// so it can reach no more than a typed line can.
//
// Every failure carries the RESOLVED text and not the typed line, which is the
// whole reason to have the feature rather than a no-op: the live vimrc's line
// 6 is
//
//	execute 'set runtimepath^=' . fnameescape(s:vim_home)
//
// and being told that "set runtimepath^=~/work/agents/bash/vim" was
// refused says which option is missing, where being told the typed line was
// refused says nothing at all.
//
// This is deliberately NOT ":execute" the vimscript feature. See the package
// doc comment.
func (l *Loader) doExecute(h header, pos Pos, text string) string {
	v, n, err := eval(h.args, l.Env, l.Vars, l.file)
	if err != nil {
		l.fail(pos, text, err)
		_, rest, _ := splitBar(h.args)
		return rest
	}
	next, err := after(h.args[n:])
	if err != nil {
		// Vim would join a second space-separated expression onto the first.
		// pvim refuses instead: joining is the half of ":execute" that builds
		// a command out of parts nobody wrote down, and no line in either
		// vimrc uses it.
		l.fail(pos, text, err)
		return ""
	}
	if v.Kind == ValueList || v.Kind == ValueDict {
		l.fail(pos, text, fmt.Errorf("%w: :execute wants a string, not a %s", ErrNoExpr, v.Kind))
		return next
	}
	l.runResolved(v.String(), pos)
	return next
}

// runResolved runs the command line a ":execute" built.
//
// It is runLine's shape minus the two things that would make ":execute" a
// language: a command the table does not have is E492 rather than anything
// else, and the control-flow commands and a nested ":execute" are refused
// outright. Refusing them is what keeps the ":if" nesting a property of the
// file as written: an "execute 'endif'" closing a block that opened three
// lines up is legal vimscript and unreadable, and pvim will not be the thing
// that runs it.
func (l *Loader) runResolved(line string, pos Pos) {
	text := line
	for {
		h := parseHeader(text)
		if !h.ok {
			l.fail(pos, line, fmt.Errorf("%w: %s", ex.ErrNotAnEditorCommand, strings.TrimSpace(text)))
			return
		}
		switch h.spec.kind {
		case kindExecute, kindIf, kindElseIf, kindElse, kindEndIf,
			kindFunction, kindEndFunction, kindFinish:
			l.fail(pos, line, fmt.Errorf("%w: %s", ErrExecuteScope, strings.TrimSpace(text)))
			return
		}
		rest := l.run(h, pos, text)
		if rest == "" {
			return
		}
		text = rest
	}
}

// doLet runs a ":let".
//
// The variable lands in Loader.Vars whatever happens, because exists() has to
// answer about it and vim would have set it. Whether the SINK hears about it
// is the dividing rule: a name Read knows is applied and everything else is
// logged and dropped, which is what makes the g:vim_ai_* lines and
// g:go_metalinter_enabled inert without noise.
func (l *Loader) doLet(h header, pos Pos, text string) string {
	args := strings.TrimLeft(h.args, " \t")

	switch {
	case args == "":
		return "" // ":let" on its own lists every variable
	case strings.HasPrefix(args, "&"), strings.HasPrefix(args, "$"),
		strings.HasPrefix(args, "@"), strings.HasPrefix(args, "["):
		l.fail(pos, text, fmt.Errorf("E15: Invalid expression: :let can assign a variable and nothing else: %s", args))
		return ""
	}

	i := 0
	for i < len(args) && isNameByte(args[i]) {
		i++
	}
	name := args[:i]
	if name == "" {
		l.fail(pos, text, fmt.Errorf("%w: %s", ErrNoExpr, args))
		return ""
	}
	rest := strings.TrimLeft(args[i:], " \t")

	op := ""
	for _, cand := range []string{"..=", ".=", "+=", "-=", "*=", "/=", "%=", "="} {
		if strings.HasPrefix(rest, cand) {
			op, rest = cand, rest[len(cand):]
			break
		}
	}
	if op == "" {
		// ":let x" displays the variable. Nothing in a vimrc wants that.
		return ""
	}

	v, n, err := eval(rest, l.Env, l.Vars, l.file)
	if err != nil {
		l.fail(pos, text, err)
		return ""
	}
	next, err := after(rest[n:])
	if err != nil {
		l.fail(pos, text, err)
		return ""
	}

	full := qualify(name)
	if op != "=" {
		prev, ok := l.Vars[full]
		if !ok {
			l.fail(pos, text, fmt.Errorf("%w: %s", ErrNoVar, full))
			return next
		}
		v, err = combine(prev, v, op)
		if err != nil {
			l.fail(pos, text, err)
			return next
		}
	}
	l.Vars[full] = v

	switch full {
	case "g:mapleader":
		l.Leader = v.String()
	case "g:maplocalleader":
		l.LocalLeader = v.String()
	}

	if !strings.HasPrefix(full, "g:") {
		// A script-local variable is this loader's own machinery and not a
		// setting: the live vimrc's s:vim_home and s:vim_state hold the paths
		// the next fifty lines are built out of, they are already in
		// Loader.Vars where the expressions that need them look, and nothing
		// above this package has any use for them. So they are neither applied
		// nor logged. Logging them would put two entries in the list the gate
		// reads as "things pvim declined to do", which is the opposite
		// of what happened.
		return next
	}
	if !Read(full) {
		l.drop(pos, full, text, IgnoreVar)
		return next
	}
	l.apply(pos, text, l.Sink.Let(Let{
		Pos:   pos,
		Name:  full,
		Op:    op,
		Value: strings.TrimSpace(rest[:n]),
		Val:   v,
	}))
	return next
}

// combine applies one of the compound assignment operators.
func combine(prev, v Val, op string) (Val, error) {
	switch op {
	case ".=", "..=":
		return Str(prev.String() + v.String()), nil
	case "+=":
		if prev.Kind == ValueList && v.Kind == ValueList {
			return Val{Kind: ValueList, List: append(append([]Val{}, prev.List...), v.List...)}, nil
		}
		return Number(prev.number() + v.number()), nil
	case "-=":
		return Number(prev.number() - v.number()), nil
	case "*=":
		return Number(prev.number() * v.number()), nil
	case "/=", "%=":
		if v.number() == 0 {
			return Number(0), nil
		}
		if op == "/=" {
			return Number(prev.number() / v.number()), nil
		}
		return Number(prev.number() % v.number()), nil
	}
	return Val{}, fmt.Errorf("%w: %s", ErrNoExpr, op)
}

// scopes is the variable scope prefixes vim has. A name with none of them is
// a global, which is how the vimrc writes NERDTreeShowHidden, NERDTreeIgnore,
// NERDTreeWinSize and mapleader.
var scopes = []string{"g:", "b:", "w:", "t:", "s:", "l:", "a:", "v:"}

// qualify puts a bare name in the global scope, where vim puts it.
func qualify(name string) string {
	for _, s := range scopes {
		if strings.HasPrefix(name, s) {
			return name
		}
	}
	return "g:" + name
}

// ErrBadFileType is E475, which is what vim says about an argument to
// ":filetype" that is not one of plugin, indent, on, off or detect. Measured
// ":filetype nonsense" prints
// "E475: Invalid argument: nonsense".
var ErrBadFileType = errors.New("E475: Invalid argument")

// parseFileType reads the argument list of ":filetype".
//
// Vim's own parser is ex_filetype() in ex_cmds2.c and this is the same shape:
//
//	while (STRNCMP(arg, "plugin", 6) == 0) { plugin = TRUE; arg = skipwhite(arg + 6); }
//	while (STRNCMP(arg, "indent", 6) == 0) { indent = TRUE; arg = skipwhite(arg + 6); }
//	if (STRCMP(arg, "on") == 0 || STRCMP(arg, "detect") == 0) { ... }
//	else if (STRCMP(arg, "off") == 0) { ... }
//	else semsg(_(e_invalid_argument_str), arg);
//
// Two things fall out of it that a hand-written parser gets wrong. "detect"
// turns detection on as well as running it, so ":filetype detect" on a fresh
// vim is ":filetype on" plus a re-scan. And "plugin" and "indent" imply "on"
// but do not say it: ":filetype plugin indent on" is one "on" for all three,
// which is why the switches are separate fields and not one enum.
//
// Both orders of the two switches are taken. Measured against vim
// 9.2.0321: ":filetype off" then ":filetype indent plugin on" reports
// "detection:ON plugin:ON indent:ON", the same as the documented spelling, so
// the order vim's source appears to insist on is not one it enforces.
//
// One place this is STRICTER than the vim on this machine. ":filetype plugin",
// ":filetype indent" and ":filetype on off" change nothing there and say
// nothing either -- vim reaches its own semsg with an empty argument and no
// message comes out -- and here each is an E475 naming the word that was
// wrong. A line that names a switch and never says on or off is a typo, and a
// typo that changes nothing in silence is the kind of thing somebody finds six
// months later wondering why their python files have no expandtab.
//
// What is not reproduced is the report a bare ":filetype" prints. Vim's
// "filetype detection:ON plugin:ON indent:(on)" reads whether each runtime
// file was actually sourced and has a third state for "on but not by name";
// pvim sources none of them, so the three switches here are what the file
// asked for and not what happened, and a Sink that wants to print something is
// printing a different fact. Nothing in this vimrc asks.
func parseFileType(pos Pos, args string) (FileType, error) {
	ft := FileType{Pos: pos, Args: args}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		ft.Report = true
		return ft, nil
	}

	// Every field but the last names a switch, and the last says on or off.
	for _, f := range fields[:len(fields)-1] {
		switch f {
		case "plugin":
			ft.Plugin = true
		case "indent":
			ft.Indent = true
		default:
			return ft, fmt.Errorf("%w: %s", ErrBadFileType, f)
		}
	}
	switch last := fields[len(fields)-1]; last {
	case "on":
		ft.On = true
	case "off":
		// The zero value. ":filetype off" with no switch named turns all
		// three off; ":filetype plugin off" turns off the one it names.
	case "detect":
		ft.On = true
		ft.Redetect = true
	default:
		return ft, fmt.Errorf("%w: %s", ErrBadFileType, last)
	}

	// A line that names neither switch is about all three, which is what makes
	// ":filetype off" turn detection off as well.
	if !ft.Plugin && !ft.Indent {
		ft.Detect, ft.Plugin, ft.Indent = true, true, true
		return ft, nil
	}
	// Naming one of them turns detection ON with it, because sourcing a script
	// per filetype means nothing without a filetype -- but turning one OFF
	// leaves detection alone. Measured ":filetype plugin indent on"
	// then ":filetype plugin off" reports "detection:ON plugin:OFF
	// indent:ON".
	ft.Detect = ft.On
	return ft, nil
}
