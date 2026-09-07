package mode

import (
	"bytes"
	"errors"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/textobj"
)

// The keys this package compares against by name. They are values rather than
// literals at the call sites because key.Key is a three-field struct and
// "k == keyEsc" is the only spelling of that comparison worth reading.
var (
	keyEsc = key.Key{Special: key.KeyEsc}
	keyCR  = key.Key{Special: key.KeyCR}
	keyNL  = key.Key{Special: key.KeyNL}
	keyBS  = key.Key{Special: key.KeyBS}
	keyDel = key.Key{Special: key.KeyDel}
	keyTab = key.Key{Special: key.KeyTab}
)

// normalKey feeds one key to normal, operator-pending or visual mode.
//
// The order is vim's own and it is the order because every part of it is
// ambiguous with the next: a digit is a count unless a count is what makes it
// a motion (0), a quote is a register prefix unless an operator is already
// waiting, and CTRL-V is a visual mode in normal and a forced motion after an
// operator. Anything that reads its own argument -- f, r, m, " -- parks a
// closure in e.wait rather than growing a state constant, because there are
// fifteen of them and each captures something different.
func (e *Editor) normalKey(k key.Key) error {
	if e.wait != nil {
		f := e.wait
		e.wait = nil
		return f(k)
	}
	e.aborted = false

	if k == keyEsc {
		return e.escape()
	}
	if e.isCount(k) {
		e.addCount(int(k.Rune - '0'))
		return nil
	}
	if k == key.Rune('"') && e.pend.op == operator.OpNone {
		e.wait = e.readRegister
		return nil
	}

	// CTRL-H and <BS> are one entry in vim's normal-mode table, and the byte a
	// keystroke file holds for either is 0x08, which internal/key decodes as
	// CTRL-H. Without this "d<BS>" is a key the motion table has never heard
	// of and beeps, which looks exactly like a d<BS> that ran and took
	// nothing.
	if k == key.Ctrl('h') {
		k = keyBS
	}
	e.pend.keys = append(e.pend.keys, k)
	e.cmdKeys = append(e.cmdKeys, k)
	s := cmdFormat(e.pend.keys)

	switch {
	case e.pend.op != operator.OpNone:
		return e.operatorPending(s)
	case e.mode != Normal:
		return e.visualCommand(s)
	default:
		return e.normalCommand(s)
	}
}

// isCount reports whether the key is a count digit here. Zero is a motion to
// the first column unless a count is already being typed, which is the whole
// of the rule and the reason d0 and d10 are different commands.
func (e *Editor) isCount(k key.Key) bool {
	if !k.IsRune() || k.Mod != 0 || k.Rune < '0' || k.Rune > '9' {
		return false
	}
	// A digit is only a count before the command starts. vim's normal_cmd
	// reads the count and then the command character, and every key after
	// that belongs to the command: "g3j" is the unknown command "g3", which
	// beeps and eats the 3, followed by a plain "j". Measured, "g3j" and
	// "[8v" both leave vim one line down and in visual mode respectively,
	// where treating the digit as a count moves three lines and swallows the
	// v.
	if len(e.pend.keys) > 0 {
		return false
	}
	if k.Rune != '0' {
		return true
	}
	if e.pend.op != operator.OpNone {
		return e.pend.count2 > 0
	}
	return e.pend.count1 > 0
}

// addCount adds a digit to whichever count is being typed. The one before the
// operator and the one after it are kept apart and multiplied, so 2d3w is six
// words; an editor with one count field deletes three and looks right until
// the day it does not.
func (e *Editor) addCount(d int) {
	if e.pend.op != operator.OpNone {
		e.pend.count2 = e.pend.count2*10 + d
		return
	}
	e.pend.count1 = e.pend.count1*10 + d
}

// readRegister takes the name after a " prefix.
func (e *Editor) readRegister(k key.Key) error {
	if k == keyEsc {
		return e.abandon()
	}
	// The name is a byte and not a rune: vim reads it with plain_vgetc, so
	// CTRL-R after a quote is 0x12 and not the letter r. Measured, "3 " CTRL-R
	// A c" appends one c in vim, because the invalid name took the count with
	// it, where reading the key as an "r" gave three.
	name, ok := atRegister(k)
	if !ok || !register.Valid(name) {
		// Silence, not E354. vim's nv_regname calls clearopbeep() for a name
		// it does not know, which beeps and says nothing at all; E354 is what
		// :let @! and setreg('!') print, and a normal-mode quote is neither.
		// Measured: "!dd leaves vim's redirect holding one bare newline where
		// the message would be, and the dd that follows the beep yanks into
		// the unnamed register. See register.Valid, whose own doc records it.
		return e.beep()
	}
	e.pend.reg = name
	return nil
}

// escape is what Escape does: leave visual mode, throw away a half-typed
// command, and do nothing at all in plain normal mode.
func (e *Editor) escape() error {
	if e.mode != Normal && e.mode != OperatorPending {
		e.endVisual()
	}
	e.mode = Normal
	e.pend = pending{}
	e.cmdKeys = nil
	return nil
}

// abandon throws the command away without a beep: an Escape typed where an
// argument was expected.
func (e *Editor) abandon() error {
	if e.mode == OperatorPending {
		e.mode = Normal
	}
	e.pend = pending{}
	e.cmdKeys = nil
	return nil
}

// beep is what vim does with a key that means nothing here: the command is
// thrown away and the editor makes a noise. It is not an error, because a
// script full of them still runs to the end, but it does set aborted, which is
// what stops a macro dead the way a failing search in a recorded macro stops
// the loop it was recorded for.
func (e *Editor) beep() error {
	e.aborted = true
	if e.mode == OperatorPending {
		e.mode = Normal
	}
	e.pend = pending{}
	e.cmdKeys = nil
	return nil
}

// finish ends the command in progress and, when it changed the buffer, makes
// it the change . repeats.
func (e *Editor) finish(changed bool) {
	if changed {
		e.recordDot(e.pend.count(), e.pend.reg)
	}
	// A yank finishes without recording anything, so the flag has to be
	// cleared here as well as in recordDot or the next change would be the one
	// that goes unrecorded.
	e.fromPos = false
	if e.mode == OperatorPending {
		e.mode = Normal
	}
	e.pend = pending{}
	e.cmdKeys = nil
}

// isPrefix reports whether more keys could still make a command out of what
// has been typed. It is what makes g wait rather than beep.
func isPrefix(s string) bool {
	for _, p := range []string{"g", "z", "Z", "[", "]"} {
		if s == p {
			return true
		}
	}
	return motion.IsPrefix(s)
}

// normalCommand dispatches a complete key sequence in normal mode.
//
// The table is a switch and not a map of closures on purpose: every entry is
// two lines, the whole of normal mode is visible in one file, and a reader
// looking for what "gJ" does finds it by searching for "gJ".
func (e *Editor) normalCommand(s string) error {
	n := e.pend.count()
	n1 := n
	if n1 < 1 {
		n1 = 1
	}

	switch s {
	// Entering insert mode.
	case "i":
		return e.enterInsert(insCmd{cmd: 'i', count: n1})
	case "I":
		return e.enterInsert(insCmd{cmd: 'I', count: n1})
	case "gI":
		return e.enterInsert(insCmd{cmd: 'g' + 'I', count: n1})
	case "gi":
		return e.enterInsert(insCmd{cmd: 'g' + 'i', count: n1})
	case "a":
		return e.enterInsert(insCmd{cmd: 'a', count: n1})
	case "A":
		return e.enterInsert(insCmd{cmd: 'A', count: n1})
	case "o":
		return e.enterInsert(insCmd{cmd: 'o', count: n1})
	case "O":
		return e.enterInsert(insCmd{cmd: 'O', count: n1})
	case "R":
		return e.enterInsert(insCmd{cmd: 'R', count: n1, replace: true})

	// The three visual modes, and the selection they bring back.
	case "v":
		return e.startVisual(VisualChar, n)
	case "V":
		return e.startVisual(VisualLine, n)
	case "<C-V>":
		return e.startVisual(VisualBlock, n)
	case "gv":
		return e.reselect()

	// The abbreviations: each is an operator with the motion filled in.
	case "x":
		return e.charDelete(n1, false)
	case "X":
		return e.charDelete(n1, true)
	case "D":
		return e.toEndOfLine(operator.OpDelete, n1)
	case "C":
		return e.toEndOfLine(operator.OpChange, n1)
	case "s":
		e.pend.op = operator.OpChange
		return e.charSpan(n1, false)
	case "S":
		e.pend.op = operator.OpChange
		return e.lineSpan(n1)
	case "Y":
		e.pend.op = operator.OpYank
		return e.lineSpan(n1)

	// The operators. Each one waits for a motion, a text object, or its own
	// key again.
	case "d", "c", "y", "<", ">", "=", "gu", "gU", "g~", "g?", "gq", "gw", "zf", "zy":
		return e.startOperator(s)

	case "J", "gJ":
		return e.join(s == "J", n)
	case "p", "P", "gp", "gP", "]p", "[p", "zp", "zP":
		return e.put(s, n1)
	case "r":
		e.wait = func(k key.Key) error { return e.replaceChars(k, n1) }
		return nil
	case "&", "g&":
		// The repeat of the last :s. vim's nv_optrans does not implement it at
		// all: it stuffs ":s\r" into the typeahead and lets the ex layer have
		// it, which is why the redirect holds the newline an ex command line
		// leaves behind before the error. There has never been a substitute to
		// repeat, because the ex layer is later work, so this is E33 every time
		// -- and E33 every time is what vim answers too until a:s has run.
		e.msg.empty()
		e.Say("E33: No previous substitute regular expression")
		return e.beep()
	case "~":
		return e.tilde(n1)
	case "<C-A>":
		return e.addToNumber(n1)
	case "<C-X>":
		return e.addToNumber(-n1)

	// Undo.
	case "u":
		return e.undo(n1)
	case "<C-R>":
		return e.redo(n1)
	case "U":
		return e.undoLine()
	case "ga":
		return e.showAscii()
	case "g-":
		return e.undoTime(n1, false)
	case "g+":
		return e.undoTime(n1, true)

	// Repeat.
	case ".":
		return e.repeatDot(n)
	case "q":
		return e.recordMacro()
	case "@":
		e.wait = func(k key.Key) error { return e.playMacro(k, n1) }
		return nil

	case "m":
		e.wait = e.setMark
		return nil

	// The jumplist. Neither key is a motion -- :help jump-motions says so and
	// vim's nv_pcmark starts with checkclearopq() -- so neither is in
	// internal/motion's table and "d<C-O>" beeps rather than deleting.
	// CTRL-I and <Tab> are one key: internal/key folds CTRL-I onto <Tab>
	// because a keystroke file holds 0x09 for either.
	case "<C-O>":
		return e.jumpOlder(n1)
	case "<Tab>":
		return e.jumpNewer(n1)

	// Search. The command line belongs to internal/ex; these two open it for a
	// pattern and nothing else.
	case "/":
		return e.startSearch(searchForward)
	case "?":
		return e.startSearch(searchBackward)
	case "*", "#", "g*", "g#":
		return e.searchWord(s, n1)
	case "n", "N":
		return e.searchNext(s == "n", n1)

	case "ZZ", "ZQ":
		e.finish(false)
		return ErrQuit

	// The identifier commands. K runs 'keywordprg' through the frontend's
	// shell and the rest search the buffer here; all of them start by asking
	// what word the cursor is on. See ident.go.
	case "K":
		return e.keyword(n1)
	}
	if _, ok := identCmds[s]; ok {
		return e.identCommand(s, n1)
	}

	if m, ok := motion.ByKeys(s); ok {
		return e.startMotion(m)
	}
	if isPrefix(s) {
		return nil
	}
	return e.unknown(s)
}

// unknown is a key sequence that is not a command. The ones that are not
// implemented yet fail loudly rather than beeping, because a `:` that silently
// does nothing is an oracle diff three keystrokes later with nothing pointing
// at the cause.
//
// The whole z family is loud and not a list of the fold keys, because a list
// is a hole waiting to be found: "zC" was not on it, vim answers it with
// "E490: No fold found", and pvim answered it with silence until a fuzz script
// went looking. Every z command that is not zf either scrolls the window or
// folds, and this editor has neither, so none of them can be right here and
// all of them have to say so.
func (e *Editor) unknown(s string) error {
	if strings.HasPrefix(s, "z") || strings.HasPrefix(s, "Z") {
		e.finish(false)
		return ErrNotImplemented
	}
	switch s {
	case ":", "<C-W>", "<C-F>", "<C-B>", "<C-D>", "<C-U>", "<C-E>", "<C-Y>":
		e.finish(false)
		return ErrNotImplemented
	}
	return e.beep()
}

// startOperator parks an operator and waits for what it acts on.
func (e *Editor) startOperator(s string) error {
	e.pend.op = opForKeys(s)
	e.pend.opKeys = s
	e.pend.trimYank = s == "zy"
	e.pend.keys = nil
	e.mode = OperatorPending
	return nil
}

// opForKeys maps an operator's keys to its Op. It is the inverse of
// operator.Op.String and it is written out rather than derived because a
// derived one would silently accept a key that is not an operator.
func opForKeys(s string) operator.Op {
	switch s {
	case "d":
		return operator.OpDelete
	case "zy":
		// vim 9's yank without trailing white space, which is the same
		// operator with pending.trimYank set beside it.
		return operator.OpYank
	case "c":
		return operator.OpChange
	case "y":
		return operator.OpYank
	case "<":
		return operator.OpShiftLeft
	case ">":
		return operator.OpShiftRight
	case "=":
		return operator.OpIndent
	case "gu":
		return operator.OpLower
	case "gU":
		return operator.OpUpper
	case "g~":
		return operator.OpToggle
	case "g?":
		return operator.OpRot13
	case "gq":
		return operator.OpFormat
	case "gw":
		return operator.OpFormatKeep
	case "zf":
		return operator.OpFold
	}
	return operator.OpNone
}

// opChar is the operator as the single character internal/motion asks about.
// Two motions read it and vim would be wrong without them: cw is ce, and h,
// <BS> and <Left> wrapping to the previous line leave their end after that
// line's last byte so that d and c take the separator with them. A
// two-character operator reports its first key, which is the 'g' that
// motion.Request.Op documents for all of them but zf, and no motion has ever
// asked about the second.
func opChar(op operator.Op) byte {
	s := op.String()
	if s == "" {
		return 0
	}
	return s[0]
}

// operatorPending dispatches the keys typed after an operator: a forced
// motion, the operator's own key again, a text object, or a motion.
func (e *Editor) operatorPending(s string) error {
	switch s {
	case "v":
		e.pend.force = motion.ForceChar
		e.pend.keys = nil
		return nil
	case "V":
		e.pend.force = motion.ForceLine
		e.pend.keys = nil
		return nil
	case "<C-V>":
		e.pend.force = motion.ForceBlock
		e.pend.keys = nil
		return nil
	}

	// The doubled operator: dd, yy, cc, >>, and for a two-key operator both
	// guu and gugu.
	if s == e.pend.opKeys || (len(e.pend.opKeys) > 1 && s == e.pend.opKeys[len(e.pend.opKeys)-1:]) {
		n := e.pend.count()
		if n < 1 {
			n = 1
		}
		return e.lineSpan(n)
	}

	// The cw exception. :help cw: when the cursor is on a non-blank, cw and cW
	// change to the end of the word rather than to the start of the next one,
	// which is e and E. It lives here and not in internal/motion because a
	// motion is not told which operator is waiting for it, and this rule is
	// about the operator.
	if (s == "w" || s == "W") && e.pend.op == operator.OpChange && !e.onBlank() {
		// vim's end_word() is called with its "stop" flag here, which means
		// the cursor does not leave a word it is already at the end of. So cw
		// on the last character of a word changes that character and nothing
		// else, where ce would run on to the end of the next word. Measured:
		// ecwXX on "hello world" gives "hellXX world".
		if e.atWordEnd(s == "W") {
			n := e.pend.count()
			if n < 2 {
				return e.charSpan(1, false)
			}
			e.pend.count1, e.pend.count2 = n-1, 0
		}
		if s == "w" {
			s = "e"
		} else {
			s = "E"
		}
	}

	// A text object. i and a are not motions, so they are taken before the
	// table is asked.
	if s == "i" || s == "a" {
		inner := s == "i"
		e.wait = func(k key.Key) error { return e.textObject(k, inner) }
		return nil
	}

	// The searches come before the motion table and not after it. n and N are
	// in the table, because their kind and their jump are motion rules, but
	// their Do is a stub: the pattern lives in internal/search and only this
	// package can reach it. Asking the table first turns "dn" into a motion
	// that fails without a word, where vim says E35.
	switch s {
	case "/":
		return e.startSearch(searchForward)
	case "?":
		return e.startSearch(searchBackward)
	case "*", "#", "g*", "g#":
		return e.searchWord(s, e.pend.count1max())
	case "n", "N":
		return e.searchNext(s == "n", e.pend.count1max())
	}
	if m, ok := motion.ByKeys(s); ok {
		return e.startMotion(m)
	}
	if isPrefix(s) {
		return nil
	}
	return e.beep()
}

// finishSimple ends a command that moved nothing and changed nothing.
func (e *Editor) finishSimple() error {
	e.finish(false)
	return nil
}

// onBlank reports whether the cursor is on a space or a tab, or on nothing at
// all because the line is empty.
func (e *Editor) onBlank() bool {
	line := e.buf.Line(e.cur.Line)
	if e.cur.Col >= len(line) {
		return true
	}
	return isSpaceByte(line[e.cur.Col])
}

// atWordEnd reports whether the cursor is on the last character of a word,
// which for a small word means the next character is in a different class and
// for a big one that it is white space or the end of the line.
func (e *Editor) atWordEnd(big bool) bool {
	line := e.buf.Line(e.cur.Line)
	if e.cur.Col >= len(line) {
		return true
	}
	next := nextRune(line, e.cur.Col)
	if next >= len(line) || isSpaceByte(line[next]) {
		return true
	}
	if big {
		return false
	}
	return isKeywordByte(line[e.cur.Col], e.opt.IsKeyword) != isKeywordByte(line[next], e.opt.IsKeyword)
}

// startMotion runs a motion, reading its argument first when it needs one.
func (e *Editor) startMotion(m motion.Motion) error {
	if !m.NeedsArg {
		return e.doMotion(m, 0)
	}
	e.wait = func(k key.Key) error {
		if k == keyEsc {
			return e.abandon()
		}
		if !k.IsRune() && k != keyCR && k != keyTab {
			return e.beep()
		}
		e.cmdKeys = append(e.cmdKeys, k)
		arg := byte(0)
		if k.IsRune() && k.Rune < 0x80 {
			arg = byte(k.Rune)
		}
		return e.doMotion(m, arg)
	}
	return nil
}

// doMotion runs one motion and either moves the cursor or hands the span it
// made to the operator that is waiting.
func (e *Editor) doMotion(m motion.Motion, arg byte) error {
	pending := e.pend.op != operator.OpNone
	from := e.cur

	res := m.Do(motion.Request{
		Buf:     e.buf,
		Ctx:     &e.mctx,
		From:    from,
		Count:   e.pend.count(),
		Arg:     arg,
		Pending: pending,
		Op:      opChar(e.pend.op),
		Opt:     e.opt.Motion(),
	})
	if res.Message != "" {
		e.Say(res.Message)
	}
	if !res.Ok {
		// A failed motion that moved anyway: ge and gE alone. The operator is
		// still abandoned and the beep still happens; the cursor stays where
		// the walk gave up. See motion.Result.Moved.
		if res.Moved {
			e.moveTo(res.To)
		}
		return e.beep()
	}
	// Before the operator, and whether or not there is one. vim's setpcmark()
	// is called by the motion's own nv_ handler, which runs ahead of
	// do_pending_operator, so an operator over a jump motion pushes the
	// jumplist exactly as a bare motion does. Measured on 200 lines: "100GyG"
	// leaves line 100 in the jumplist. What it does NOT leave behind is the '
	// mark, because the yank put the cursor back where it started and
	// checkPCMark handed the mark to whatever set it last -- so "100GyG''"
	// goes to line 1 while :jumps holds line 100. Both halves measured.
	if m.Jump || res.Jump {
		e.setPCMark(from)
	}
	if pending {
		return e.operateOverMotion(res, motionKey(m.Keys))
	}
	e.cur = e.buf.Clamp(res.To)
	e.setCurswant(res)
	e.finish(false)
	return nil
}

// moveTo puts the cursor somewhere that is not the end of a motion, and
// remembers the column j and k will aim for.
//
// Every cursor move that is not a motion has to do this. The one that showed
// it was leaving insert mode: ifoo<Esc>j left the cursor in column 0 rather
// than column 2, because j aimed at a wanted column nothing had updated since
// the file was opened, and the p after it pasted in the wrong place.
func (e *Editor) moveTo(p text.Pos) {
	e.cur = e.buf.Clamp(p)
	e.mctx.Curswant = text.DisplayCol(e.buf.Line(e.cur.Line), e.cur.Col, e.opt.TabStop)
}

// setCurswant remembers the display column j and k are aiming for. It is the
// whole mechanism behind $ sticking to the end of every line it passes.
func (e *Editor) setCurswant(res motion.Result) {
	switch res.Curswant {
	case motion.CurswantKeep:
	case motion.CurswantEOL:
		e.mctx.Curswant = motion.CurswantEOL
	case motion.CurswantHere:
		e.mctx.Curswant = text.DisplayCol(e.buf.Line(e.cur.Line), e.cur.Col, e.opt.TabStop)
	default:
		e.mctx.Curswant = res.Curswant
	}
}

// operateOverMotion resolves a motion into a span and applies the operator.
//
// key is the motion's own key, which the operator needs for one rule and one
// only: %, (, ), `, /, ?, n, N, { and } send a delete to "1 however short it
// was, where any other motion that short sends it to "-. See
// register.MotionForcesNumbered.
func (e *Editor) operateOverMotion(res motion.Result, key byte) error {
	span, err := e.spanForMotion(res)
	if err != nil {
		return err
	}
	e.numbered = register.MotionForcesNumbered(key)
	defer func() { e.numbered = false }()
	return e.runOperator(span)
}

// spanForMotion is operator.SpanForMotion with motion.Result.NoAdjust obeyed.
//
// A motion that sets NoAdjust has already put its end where the operator is to
// take it and says so: h, <BS> and <Left> wrapping to the previous line with d
// or c waiting leave the end AFTER that line's last byte, on purpose, so that
// the line separator goes with the delete. internal/operator applies the two
// :help exclusive adjustments on the way in whatever the motion asked, and the
// first of them moves that end straight back onto the byte that was meant to
// stay, which collapses the span to nothing: d<BS> at the start of a line then
// does nothing at all where vim joins the two lines. The adjustments are
// charwise rules, so a V or CTRL-V force never reaches them and is left alone.
func (e *Editor) spanForMotion(res motion.Result) (operator.Span, error) {
	if !res.NoAdjust || (e.pend.force != motion.ForceNone && e.pend.force != motion.ForceChar) {
		return operator.SpanForMotion(e.buf, e.cur, res, e.pend.force, e.opt.Operator())
	}
	start, end := e.buf.Clamp(e.cur), e.buf.Clamp(res.To)
	if end.Before(start) {
		start, end = end, start
	}
	return operator.Span{Type: register.TypeChar, Range: text.Range{Start: start, End: end}}, nil
}

// motionKey is the single character register.MotionForcesNumbered asks about.
// A motion written with more than one key is never one of the ten.
func motionKey(keys string) byte {
	if len(keys) != 1 {
		return 0
	}
	return keys[0]
}

// textObject completes an operator with iw, ap, i" and the rest.
func (e *Editor) textObject(k key.Key, inner bool) error {
	if k == keyEsc {
		return e.abandon()
	}
	if !k.IsRune() || k.Rune > 0x7f {
		return e.beep()
	}
	e.cmdKeys = append(e.cmdKeys, k)
	obj, ok := textobj.ByKey(byte(k.Rune))
	if !ok {
		return e.beep()
	}
	res := obj.Find(textobj.Request{
		Buf:   e.buf,
		At:    e.cur,
		Count: e.pend.count(),
		Inner: inner,
		Arg:   byte(k.Rune),
		Opt:   e.opt.TextObj(),
	})
	if !res.Ok {
		// A word object that ran out of buffer part way through its count
		// moved the cursor and vim does not put it back. See
		// textobj.Result.Moved.
		if res.Moved {
			e.moveTo(normalPos(e.buf, res.Range.Start))
		}
		return e.beep()
	}
	at := objectAt(res, e.cur, e.pend.op)
	if e.pend.force != motion.ForceNone {
		span, err := e.forcedObjectSpan(res)
		if err != nil {
			return err
		}
		return e.runOperatorCount(span, 1, at)
	}
	return e.runOperatorCount(spanForObject(res), 1, at)
}

// objectAt is the position an operator sees when its range came from a text
// object rather than from a motion.
//
// vim's nv_object leaves the same oap a Visual selection would have, and
// do_pending_operator then moves the cursor to oap->start before the operator
// runs, so an operator that reads "where the cursor was" reads the object's
// start and not where the object was typed from. It is the object's own start
// and not the span's: a v, V or CTRL-V force changes the span and does not
// change this. Measured on " abcdef" with the cursor in column 8, y8|yViw
// leaves the cursor in column 5 -- the "a" the object starts on -- where a
// linewise object under the same force, yVip, leaves it in column 1.
//
// Two operators read it and a third must not. "yip" and "zfip" on an indented
// paragraph both land in column one where "yj" and "zfj" keep the column they
// were typed in; "gwip" puts the cursor back on the character it was on before
// the reflow, which is vim's op_format saving the cursor for itself before
// do_pending_operator moves it, so gw is handed the real one.
func objectAt(res textobj.Result, cur text.Pos, op operator.Op) text.Pos {
	if op == operator.OpFormatKeep {
		return cur
	}
	if res.Type == register.TypeLine {
		return text.Pos{Line: res.Range.Start.Line}
	}
	return res.Range.Start
}

// forcedObjectSpan is the span a text object makes when a v, V or CTRL-V was
// typed between the operator and the object.
//
// vim does not have a second set of rules for this: nv_object leaves the same
// oap a motion would have left and do_pending_operator applies motion_force to
// it, adjustments and all. So the object is handed back as the motion it looks
// like -- a linewise object is start-of-first-line to start-of-last-line
// linewise, a charwise one is inclusive of its last character -- and
// operator.SpanForMotion does the rest. Measured on "aa/bb//cc": dvip deletes
// the first line only and reports the register linewise, because the charwise
// force made the object exclusive and the exclusive-in-the-indent rule then
// made it whole lines again. A guess that assigned the forced type would have
// deleted both.
func (e *Editor) forcedObjectSpan(res textobj.Result) (operator.Span, error) {
	start, m := res.Range.Start, motion.Result{Ok: true}
	if res.Type == register.TypeLine {
		start = text.Pos{Line: res.Range.Start.Line}
		m.To, m.Kind = text.Pos{Line: res.Range.End.Line}, motion.KindLine
	} else {
		m.To, m.Kind = e.lastBytePos(res.Range), motion.KindCharInclusive
	}
	return operator.SpanForMotion(e.buf, start, m, e.pend.force, e.opt.Operator())
}

// lastBytePos is the position of the last character in a half-open range,
// which is where vim leaves the cursor for a charwise object. A range that
// ends in column zero backs onto the last character of the line above.
func (e *Editor) lastBytePos(r text.Range) text.Pos {
	p := r.End
	if p.Col > 0 {
		line := e.buf.Line(p.Line)
		return text.Pos{Line: p.Line, Col: prevRune(line, min(p.Col, len(line)))}
	}
	if p.Line > r.Start.Line {
		p.Line--
		line := e.buf.Line(p.Line)
		if len(line) > 0 {
			p.Col = prevRune(line, len(line))
		}
	}
	return p
}

// spanForObject turns a text object's range into the span shape an operator
// reads.
//
// The two agree already: internal/textobj's linewise() names the last line the
// object covers, and operator.Span's linewise Range does too. The columns of a
// linewise object mean nothing to either, and dropping them here rather than
// passing them on is what keeps >ip from shifting the first line and leaving
// the second one alone.
func spanForObject(res textobj.Result) operator.Span {
	if res.Type != register.TypeLine {
		return operator.Span{Type: res.Type, Range: res.Range, EndAdjusted: res.EndAdjusted}
	}
	return operator.Span{
		Type: register.TypeLine,
		Range: text.Range{
			Start: text.Pos{Line: res.Range.Start.Line},
			End:   text.Pos{Line: res.Range.End.Line},
		},
		EndAdjusted: res.EndAdjusted,
	}
}

// runOperator applies the pending operator to a span, opens and closes the one
// undo step the whole command is, and switches to insert mode when the
// operator was c.
func (e *Editor) runOperator(span operator.Span) error {
	// One, because by here the count has been spent on the span: 3>> shifts
	// three lines by one shiftwidth and d3w has already taken three words.
	// The two that have not spent it are a visual operator and J, and both
	// call runOperatorCount with the number.
	return e.runOperatorCount(span, 1, e.cur)
}

// runOperatorCount is runOperator with the count the operator itself is to
// use, which is a different number from the one the motion used, and with the
// position the operator was typed at, which is not the span's start: a
// backward motion puts the span ahead of the cursor and a linewise yank keeps
// the column it was typed in. See operator.Request.At.
func (e *Editor) runOperatorCount(span operator.Span, count int, at text.Pos) error {
	op := e.pend.op
	// A named register a yank or a delete may not write is vim's beep_flush()
	// in op_yank and op_delete: the whole command does nothing, the buffer and
	// the undo tree are untouched and nothing at all is said. Measured on
	// "%dd, "~dd, "/dd, ".dd, ":dd and "/yy, every one of which leaves
	// undotree().seq_cur at 0 over an unchanged buffer. It is not an error
	// that travels up: the next keystroke in the script still runs.
	if writesRegister(op) && e.pend.reg != 0 && !register.Writable(e.pend.reg) {
		if op == operator.OpChange {
			// c is the one that carries on. op_change calls op_delete, gets
			// the same refusal back and starts insert anyway, so the text is
			// typed in front of what should have been deleted: measured,
			// "%ccZ<Esc> on "aaa bbb" leaves "Zaaa bbb" with undo saved.
			e.openUndo(e.cur)
			e.rememberLine()
			return e.enterInsert(insCmd{cmd: 'c', count: 1, keepUndo: true})
		}
		e.beep()
		e.finish(false)
		return nil
	}
	// vim's op_delete returns on ML_EMPTY before it does anything at all: no
	// bytes move, no register is written and no undo header is numbered. "dd"
	// and "x" take that path through their own guards; the charwise
	// operators reach it here, which is why "de" and "D" on a buffer that is
	// one empty line leave undotree().seq_cur at 0 where an "X" in column one
	// of a line with text on it leaves 1. A change still starts inserting,
	// because op_change calls op_delete and carries on whatever it answered.
	if op == operator.OpDelete && emptyLineDelete(e.buf, span) {
		e.beep()
		e.finish(false)
		return nil
	}
	if op.Changes() {
		// The undo block remembers where the change starts and not where the
		// cursor happens to be: dk deletes upwards, and u after it puts the
		// cursor on the first line it took rather than the last.
		e.openUndo(spanStart(span))
		e.rememberLine()
	}
	res, err := operator.Apply(operator.Request{
		Buf:              e.buf,
		Regs:             e.regs,
		Op:               op,
		Span:             span,
		At:               at,
		Register:         e.pend.reg,
		Count:            count,
		Spaces:           true,
		TrimTrailing:     e.pend.trimYank,
		NumberedRegister: e.numbered,
		Opt:              e.opt.Operator(),
	})
	if err != nil {
		e.closeUndo()
		e.beep()
		// A range the operator will not take is vim beeping and carrying on,
		// not the editor falling over: "gU(" in column one of line one hands
		// the case operator nothing and vim's next keystroke still runs. Only
		// an error that is not about the range travels up, where cmd/pvim
		// turns it into an exit code.
		if errors.Is(err, operator.ErrNoRange) {
			e.finish(false)
			return nil
		}
		return err
	}
	e.sayResult(res)
	e.moveTo(res.Cursor)
	if res.Insert {
		// c does not close the undo block: the change and the insert that
		// follows it are one press of u. A blockwise change carries the block
		// with it, because what is typed goes into every line of it.
		c := insCmd{cmd: 'c', count: 1, keepUndo: true}
		if span.Type == register.TypeBlock {
			c.block = &blockInsert{
				first: span.Block.First,
				last:  span.Block.Last,
				col:   span.Block.Left,
				home:  -1,
				// A $ block takes the text at the end of every line it
				// covers, on the change as well as on the append: without
				// this, l CTRL-V jj $ c Z leaves every line but the first
				// alone.
				toEOL: span.Block.ToEOL,
			}
		}
		return e.enterInsert(c)
	}
	e.closeUndo()
	e.finish(op.Changes())
	return nil
}

// writesRegister reports whether the operator hands its text to a register and
// therefore refuses a register it may not write. Measured one at a time: d, c
// and y refuse, while >, J and gU carry on and ignore the name entirely.
func writesRegister(op operator.Op) bool {
	switch op {
	case operator.OpDelete, operator.OpChange, operator.OpYank:
		return true
	}
	return false
}

// spanStart is the position a span begins at, which is where vim puts the
// cursor back when the change is undone.
func spanStart(s operator.Span) text.Pos {
	switch s.Type {
	case register.TypeBlock:
		return text.Pos{Line: s.Block.First}
	case register.TypeLine:
		return text.Pos{Line: s.Range.Start.Line}
	default:
		return s.Range.Start
	}
}

// lineSpan is dd, yy, cc and 3>>: the operator's own key doubled, which is
// linewise over count lines whatever the operator would otherwise take.
func (e *Editor) lineSpan(count int) error {
	// A count on a doubled operator is cursor_down() in vim's nv_lineop, and
	// cursor_down refuses to move at all from the last line of the buffer
	// rather than clamping: "3dd" and "2yy" and "3cc" on the last line beep
	// and do nothing, not even saving undo, while the same commands two lines
	// from the end take what is there. A count of one asks cursor_down for no
	// movement at all and is never refused, which is why plain "dd" works on
	// the last line.
	if count > 1 && e.cur.Line >= e.buf.LineCount() {
		return e.beep()
	}
	if e.buf.Emptied() {
		// vim's op_delete returns before it does anything at all when the
		// buffer is ML_EMPTY, so "dd" on a buffer something already emptied
		// takes no text, writes no register and says nothing -- the
		// "--No lines in buffer--" belongs to whatever emptied it. Measured
		// with dGdd, where vim says it once and an editor that runs the second
		// delete says it twice and shifts the numbered registers on the way.
		//
		// "cc" goes through the same op_delete and then starts inserting, so
		// it is the empty region and not the beep: measured, "S!" on an empty
		// file leaves "!" behind with the unnamed and numbered registers
		// exactly as empty as they were. Every other operator does its own
		// work on the empty line and none of them needs a special case.
		//
		// ML_EMPTY and not "one empty line": a file holding a single newline
		// reads as one empty line and is not ML_EMPTY, and "dd" on it does
		// take the line, says "--No lines in buffer--" and leaves a linewise
		// "\n" in the unnamed register. Measured on both files.
		switch e.pend.op {
		case operator.OpDelete:
			return e.beep()
		case operator.OpChange:
			return e.runOperator(operator.Span{
				Type:  register.TypeChar,
				Range: text.Range{Start: e.cur, End: e.cur},
			})
		}
	}
	// The v, V or CTRL-V typed between the operator and its doubled key is not
	// dropped. vim's nv_lineop makes the motion linewise and do_pending_operator
	// then applies motion_force to it like any other, so "dvd" is an empty
	// charwise region that deletes nothing and "d CTRL-V d" is a one-character
	// block. Measured: dvd on l1..l10 leaves the buffer and the unnamed
	// register untouched, where a lineSpan that ignored the force takes a line.
	if e.pend.force != motion.ForceNone {
		m := motion.Result{Ok: true, Kind: motion.KindLine, To: text.Pos{Line: e.cur.Line + count - 1}}
		span, err := operator.SpanForMotion(e.buf, e.cur, m, e.pend.force, e.opt.Operator())
		if err != nil {
			return err
		}
		return e.runOperator(span)
	}
	return e.runOperator(operator.SpanForLines(e.buf, e.cur.Line, count))
}

// emptyLineDelete is the two early returns op_delete takes before it saves
// undo, both of which leave undotree().seq_cur where it was.
//
// The first is ML_EMPTY: a buffer that is one empty line, where "de" and "D"
// number no header at all. The second is vim's own comment, "Check for trying
// to delete (e.g. \"D\") in an empty line": a charwise delete inside one empty
// line, anywhere in the buffer. Both are guarded by the operator being a
// delete, because op_change calls op_delete and carries on whatever it
// answered -- which is why "C" on the same empty line still numbers a header
// and still writes the register.
func emptyLineDelete(b *text.Buffer, span operator.Span) bool {
	if b.Emptied() {
		return true
	}
	if span.Type != register.TypeChar || span.Range.Start.Line != span.Range.End.Line {
		return false
	}
	// Only the INCLUSIVE zero-width span, which is the one $ builds and the
	// one vim's D check is about. The exclusive one -- "dl" and "x" on the
	// same empty line -- is oap->empty, and op_delete answers that one with
	// u_save_cursor(), so it numbers a header over a buffer it did not
	// change. Measured on a four-line file whose last line is blank: "4GD"
	// leaves seq_cur at 0 and "4Gdl" leaves it at 1.
	return span.EndsOnEmptyLine && len(b.Line(span.Range.Start.Line)) == 0
}

// charSpan is s and x: count characters from the cursor, clamped to the end of
// the line, which is why neither ever joins two lines the way dl with a large
// count would.
func (e *Editor) charSpan(count int, before bool) error {
	// x on a buffer that is one empty line beeps before it saves undo:
	// undotree().seq_cur is still 0 there where it is 1 after an "X" in column
	// one of a line with text on it, which is vim's op_delete returning on
	// ML_EMPTY before u_save. s is "cl" through the same code and still starts
	// inserting, so it takes the empty region below and is not this. An empty
	// line inside a buffer is not this case either.
	if e.pend.op == operator.OpDelete && e.buf.Emptied() {
		return e.beep()
	}
	line := e.buf.Line(e.cur.Line)
	start, end := e.cur, e.cur
	// A count that runs off either end of the line leaves the span empty, and
	// an empty span is not a failure: vim's nv_left and nv_right break out of
	// their loop without beeping when an operator is pending and let the
	// operator have the empty region, which saves undo and does nothing. Only
	// the count that moves nothing at all in normal mode beeps, and that is
	// not this.
	if before {
		for i := 0; i < count && start.Col > 0; i++ {
			start.Col = prevRune(line, start.Col)
		}
	} else {
		for i := 0; i < count && end.Col < len(line); i++ {
			end.Col = nextRune(line, end.Col)
		}
	}
	return e.runOperator(operator.Span{
		Type:  register.TypeChar,
		Range: text.Range{Start: start, End: end},
	})
}

// charDelete is x and X.
func (e *Editor) charDelete(count int, before bool) error {
	e.pend.op = operator.OpDelete
	return e.charSpan(count, before)
}

// toEndOfLine is D and C, which are d$ and c$ with the count meaning lines
// rather than repeats.
func (e *Editor) toEndOfLine(op operator.Op, count int) error {
	e.pend.op = op
	// D and C are "d$" and "c$" with the count restuffed, so the count is $'s
	// and $ moves down with cursor_down(), which does not clamp when it is
	// already on the last line: it fails, and the operator fails with it.
	// Measured on a three-line file, "5C" on line 1 clamps and changes all
	// three, and "2C" on line 3 beeps and changes nothing.
	if count > 1 && e.cur.Line >= e.buf.LineCount() {
		return e.beep()
	}
	last := e.cur.Line + count - 1
	if n := e.buf.LineCount(); last > n {
		last = n
	}
	if last > e.cur.Line {
		// D with a count takes the rest of this line and all of the ones
		// after it, which is a charwise span that ends at the last line's end.
		return e.runOperator(operator.Span{
			Type: register.TypeChar,
			Range: text.Range{
				Start: e.cur,
				End:   text.Pos{Line: last, Col: len(e.buf.Line(last))},
			},
		})
	}
	return e.runOperator(operator.Span{
		Type: register.TypeChar,
		Range: text.Range{
			Start: e.cur,
			End:   text.Pos{Line: e.cur.Line, Col: len(e.buf.Line(e.cur.Line))},
		},
		// D and C are "d$" and "c$", and $ is inclusive, so on an empty line
		// this is the zero-width span vim does NOT call oap->empty: the
		// delete half is skipped and the change half still writes the
		// register, charwise and empty. Measured, "S<Esc>C" on a four-line
		// file leaves the unnamed register empty where the S had made it
		// linewise.
		EndsOnEmptyLine: len(e.buf.Line(e.cur.Line)) == 0,
	})
}

// join is J and gJ. It is not an operator in normal mode: it takes a count of
// lines and no motion, so the span is built here.
func (e *Editor) join(spaces bool, count int) error {
	n := count
	if n < 2 {
		n = 2
	}
	last := e.cur.Line + n - 1
	if max := e.buf.LineCount(); last > max {
		// vim's nv_join beeps only when the count was one or two; a larger one
		// is clamped to what is left and then joined, which on the last line
		// means do_join over a single line: it saves undo, reports the line to
		// the changelist and changes nothing. Measured on a one-line file,
		// "J" leaves undotree().seq_cur at 0 and "5J" leaves it at 1.
		if n <= 2 {
			return e.beep()
		}
		last = max
	}
	if last == e.cur.Line && n <= 2 {
		return e.beep()
	}
	e.pend.op = operator.OpJoin
	e.openUndo(e.cur)
	e.rememberLine()
	if last == e.cur.Line {
		// A count clamped down to one line: vim's do_join rewrites that line
		// with itself, which saves undo and reports the line to the changelist
		// at the end of its text and does nothing else. Measured: "3G3J" on a
		// three-line file leaves undotree().seq_cur at 1 and the changelist on
		// line 3 column 3 over a buffer nothing touched. internal/operator
		// refuses a join of one line, correctly, so it is not asked.
		e.buf.ChangedAt(text.Pos{Line: e.cur.Line, Col: len(e.buf.Line(e.cur.Line))})
		// And it moves the cursor to column one, because do_join ends by
		// putting it where the last join happened and there was no join: its
		// running total of the sizes before the join is still zero. Measured,
		// "llll3J" on a one-line file and "3Gll9J" on a three-line one both
		// leave the cursor in column 1 where an ordinary "J" leaves it where
		// the seam is.
		e.cur.Col = 0
		e.closeUndo()
		e.finish(true)
		return nil
	}
	res, err := operator.Apply(operator.Request{
		Buf:  e.buf,
		Regs: e.regs,
		Op:   operator.OpJoin,
		Span: operator.Span{
			Type:  register.TypeLine,
			Range: text.Range{Start: text.Pos{Line: e.cur.Line}, End: text.Pos{Line: last}},
		},
		Register: e.pend.reg,
		Count:    count,
		Spaces:   spaces,
		Opt:      e.opt.Operator(),
	})
	e.closeUndo()
	if err != nil {
		e.beep()
		return err
	}
	e.sayResult(res)
	e.moveTo(res.Cursor)
	e.finish(true)
	return nil
}

// put is p, P, gp, gP, ]p and [p.
func (e *Editor) put(s string, count int) error {
	if e.pend.reg == register.LastInsert {
		return e.putLastInsert(s, count)
	}
	// A put with no register named follows 'clipboard': under "unnamed" it
	// reads "*, which is the pasteboard and not the unnamed register, and the
	// two are not always the same thing. See File.UnnamedName.
	name := e.pend.reg
	if name == 0 {
		name = e.regs.UnnamedName()
	}
	val, err := e.regs.Get(name)
	if err != nil {
		e.Say("E354: Invalid register name")
		e.beep()
		return nil
	}
	// "/ with nothing searched for yet is answered before undo is saved, which
	// is the difference between it and an empty "a: vim's do_put asks
	// get_spec_reg() for the pattern first and returns when there is none, so
	// "/p prints E35 and leaves undotree().seq_cur at 0 where "ap prints E353
	// and leaves it at 1. Both measured. The register is typed and holds one
	// empty line, so Value.Empty is false for it and the test has to be on the
	// text.
	if e.pend.reg == register.LastSearch && len(val.Bytes()) == 0 {
		e.Say("E35: No previous regular expression")
		return e.beep()
	}
	// The undo block opens before the register is looked at, because vim's
	// do_put calls u_save before it finds out the register is empty: p with
	// nothing yanked yet prints E353 and still leaves undotree().seq_cur at 1.
	// It saves no LINE, though -- u_save(lnum, lnum+1) is the range between
	// two lines and there is nothing in it -- so undoing that header says
	// "0 changes" and not the "1 change" an X in column one says.
	e.openUndoNoLines(e.cur)
	e.rememberLine()
	if e.pend.reg == register.BlackHole {
		// The black hole gets the other kind of header. Measured both ways:
		// "_p undone says "1 change" and leaves a changelist entry, where a
		// bare p with nothing yanked says "0 changes" and leaves none, so
		// vim reached its second u_save -- the one that saves the cursor
		// line -- before it found the register empty.
		e.buf.ReopenUndoWithLine()
		// "_p is not E353 and not a paste: the black hole answers a typed
		// charwise nothing, so vim finds no bytes to put, says nothing, and
		// leaves the buffer and the changelist alone with the undo header it
		// had already numbered. Measured, yy then "_p: seq_cur 1 and an empty
		// getchangelist().
		e.closeUndo()
		e.finish(false)
		return nil
	}
	if val.Empty() {
		// The message names the register as it was typed, so a bare p is
		// E353 on `"` however 'clipboard' redirected the read.
		said := e.pend.reg
		if said == 0 {
			said = register.Unnamed
		}
		e.Say("E353: Nothing in register " + string(rune(said)))
		return e.beep()
	}
	res, err := operator.Put(operator.PutRequest{
		Buf:    e.buf,
		Val:    val,
		At:     e.cur,
		Before: s == "P" || s == "gP" || s == "[p" || s == "zP",
		Count:  count,
		After:  s == "gp" || s == "gP",
		Indent: s == "]p" || s == "[p",
		// vim 9's put of a block without the trailing white space that makes
		// it a rectangle, and the other half of zy. Measured on a block
		// yanked with zy from "ab", "cdef" and "gh" put into three copies of
		// "0123456789": p gives "0ab 123456789" and zp gives "0ab123456789",
		// and both give "0cde123456789" for the line that filled the width.
		Trim: s == "zp" || s == "zP",
		Opt:  e.opt.Operator(),
	})
	e.closeUndo()
	if err != nil {
		e.beep()
		return err
	}
	e.sayResult(res)
	e.moveTo(res.Cursor)
	e.finish(true)
	return nil
}

// putLastInsert is p and P of the ". register, which vim does not put at all.
//
// do_put sees the "." and hands the whole thing to stuff_inserted(): it stuffs
// an "a" or an "i", the text of the last insert and an Escape into the
// typeahead and lets insert mode run. The buffer comes out the same either way
// and three other things do not. The message line says "-- INSERT --" and then
// wipes it, an empty register is E29 and not E353, and a count repeats the text
// through the insert rather than through the put. All three are diffed.
func (e *Editor) putLastInsert(s string, count int) error {
	v, err := e.regs.Get(register.LastInsert)
	// Not Value.Empty: ". is one of the registers vim reports as typed even
	// when it holds nothing, so it comes back as a single empty line and
	// Empty() is false for it. The question here is whether anything has been
	// inserted, which is a question about the text. Measured, ".p on a fresh
	// buffer: E29, the buffer untouched and undotree().seq_cur still 0.
	if err != nil || len(v.Bytes()) == 0 {
		e.Say("E29: No inserted text yet")
		return e.beep()
	}
	cmd := byte('a')
	if s == "P" || s == "gP" || s == "[p" || s == "zP" {
		cmd = 'i'
	}
	e.openUndo(e.cur)
	e.rememberLine()
	if err := e.enterInsert(insCmd{cmd: cmd, count: count, keepUndo: true}); err != nil {
		return err
	}
	e.insertBytes(v.Bytes())
	return e.endInsert()
}

// replaceChars is r: count characters replaced with one, which fails and
// changes nothing when the line is too short. r<CR> is the exception that
// splits the line instead.
func (e *Editor) replaceChars(k key.Key, count int) error {
	if k == keyEsc {
		return e.abandon()
	}
	e.cmdKeys = append(e.cmdKeys, k)

	line := e.buf.Line(e.cur.Line)
	end := e.cur.Col
	for i := 0; i < count; i++ {
		if end >= len(line) {
			return e.beep()
		}
		end = nextRune(line, end)
	}

	e.openUndo(e.cur)
	e.rememberLine()
	r := text.Range{Start: e.cur, End: text.Pos{Line: e.cur.Line, Col: end}}
	if k == keyCR || k == keyNL {
		// vim's nv_replace does not replace with a newline either: it deletes
		// the characters and calls invoke_edit(), so a real insert session
		// runs over a stuffed <CR> and Escape. The buffer is the same and the
		// message line is not, which is the whole of why this goes the long
		// way round.
		e.buf.Delete(r)
		if err := e.enterInsert(insCmd{cmd: 'r', count: 1, keepUndo: true}); err != nil {
			return err
		}
		if err := e.insertNewline(); err != nil {
			return err
		}
		return e.endInsert()
	}
	var rep []byte
	for i := 0; i < count; i++ {
		rep = append(rep, runeBytes(k)...)
	}
	e.buf.Replace(r, rep)
	e.moveTo(text.Pos{Line: e.cur.Line, Col: prevRune(e.buf.Line(e.cur.Line), end)})
	// nv_replace overwrites the characters one at a time and reports the
	// change from where it left the cursor, so 3rz says column 3 and not
	// column 1.
	e.buf.ChangedAt(e.cur)
	e.closeUndo()
	e.finish(true)
	return nil
}

// tilde is ~ without 'tildeop': toggle the case of count characters and move
// past them, stopping at the end of the line.
func (e *Editor) tilde(count int) error {
	line := e.buf.Line(e.cur.Line)
	if e.cur.Col >= len(line) {
		return e.beep()
	}
	end := e.cur.Col
	for i := 0; i < count && end < len(line); i++ {
		end = nextRune(line, end)
	}
	e.openUndo(e.cur)
	e.rememberLine()
	out := append([]byte(nil), line[:e.cur.Col]...)
	out = append(out, toggleCase(line[e.cur.Col:end])...)
	out = append(out, line[end:]...)
	// vim's n_swapchar saves undo before it looks and touches the buffer only
	// when swapchar() actually swapped something, so "~" on a digit leaves
	// undotree().seq_cur at 1 over an empty changelist. Measured on a line
	// starting with a date.
	if !bytes.Equal(out, line) {
		e.buf.SetLine(e.cur.Line, out)
		// op_tilde reports the start of the span it flipped, which is where
		// the cursor still is; rewriting the whole line here is an
		// implementation detail and would otherwise report column 0.
		e.buf.ChangedAt(e.cur)
	}
	e.closeUndo()
	col := end
	if col >= len(out) && len(out) > 0 {
		col = prevRune(out, len(out))
	}
	e.moveTo(text.Pos{Line: e.cur.Line, Col: col})
	e.finish(true)
	return nil
}

// setMark is m: the next key names a mark at the cursor.
func (e *Editor) setMark(k key.Key) error {
	if k == keyEsc || !k.IsRune() || k.Rune > 0x7f {
		return e.abandon()
	}
	name := byte(k.Rune)
	if name == '\'' || name == '`' {
		// m' and m` do not set a mark called ' at all: vim's setmark_pos()
		// calls setpcmark(), which is the same call every jump makes, so this
		// is the documented way to put the position you are standing on into
		// the jumplist by hand. It then copies w_pcmark into w_prev_pcmark --
		// "keep it even when the cursor doesn't move" in the C -- so that the
		// checkPCMark at the end of the command cannot take it back again.
		// Measured: "100Gm'" leaves line 100 in :jumps and '' on line 100.
		e.setPCMark(e.cur)
		e.prevJump = e.cur
		e.finish(false)
		return nil
	}
	if !(name >= 'a' && name <= 'z') && !(name >= 'A' && name <= 'Z') {
		// Silence, not E191. vim's nv_mark calls setmark() and, on the FAIL it
		// returns, clearopbeep(): the beep is the whole of the answer, and
		// E191 is what ":mark >" says, which is the ex command and not this
		// one. Measured: "m>" leaves the message line empty.
		return e.beep()
	}
	e.buf.SetMark(name, e.cur)
	e.finish(false)
	return nil
}

// addToNumber is CTRL-A and CTRL-X: find a number at or after the cursor on
// this line and add to it.
//
// 'nrformats' is "bin,hex" on this vim, so 007 counts up to 008 and not to
// 010, and a run of leading zeros keeps its width. A 0x prefix makes the
// number hexadecimal and the case of its digits is kept. Every one of those
// was measured rather than remembered: "x 41 y", "z -3 w", "0x0f" and "007"
// are four of the oracle's cases and each of them is a different rule.
func (e *Editor) addToNumber(delta int) error {
	line := e.buf.Line(e.cur.Line)
	start, end, base, ok := findNumber(line, e.cur.Col)
	if !ok {
		return e.beep()
	}

	digits := string(line[start:end])
	prefix := ""
	if base == 16 {
		prefix, digits = digits[:2], digits[2:]
	}
	v, err := strconv.ParseInt(digits, base, 64)
	if err != nil {
		return e.beep()
	}
	v += int64(delta)

	var out string
	switch {
	case base == 16:
		out = strconv.FormatInt(v, 16)
		if hasUpperHex([]byte(digits)) {
			out = upper(out)
		}
		out = prefix + padZero(out, len(digits))
	case leadingZero(digits):
		sign := ""
		if v < 0 {
			sign, v = "-", -v
		}
		out = sign + padZero(strconv.FormatInt(v, 10), len(trimSign(digits)))
	default:
		out = strconv.FormatInt(v, 10)
	}

	e.openUndo(e.cur)
	e.rememberLine()
	newLine := append([]byte(nil), line[:start]...)
	newLine = append(newLine, out...)
	newLine = append(newLine, line[end:]...)
	e.buf.SetLine(e.cur.Line, newLine)
	e.closeUndo()
	if col := start + len(out) - 1; col >= 0 {
		e.moveTo(text.Pos{Line: e.cur.Line, Col: col})
	}
	e.finish(true)
	return nil
}

// findNumber locates the number at or after col on a line and reports the
// bytes it spans and its base. The span includes a 0x prefix and a leading
// minus sign, because both are part of the number vim adds to.
func findNumber(line []byte, col int) (start, end, base int, ok bool) {
	for i := col; i < len(line); i++ {
		if !isDigit(line[i]) {
			continue
		}
		start, base = i, 10
		for start > 0 && isDigit(line[start-1]) {
			start--
		}
		// Hexadecimal, either because the cursor is on a digit inside one or
		// because it is on the 0 of the prefix itself.
		switch {
		case start >= 2 && (line[start-1] == 'x' || line[start-1] == 'X') && line[start-2] == '0':
			start, base = start-2, 16
		case start+2 < len(line) && line[start] == '0' &&
			(line[start+1] == 'x' || line[start+1] == 'X') && isHex(line[start+2]):
			base = 16
		}
		end = start
		if base == 16 {
			end = start + 2
			for end < len(line) && isHex(line[end]) {
				end++
			}
		} else {
			if start > 0 && line[start-1] == '-' {
				start--
			}
			end = start
			if line[end] == '-' {
				end++
			}
			for end < len(line) && isDigit(line[end]) {
				end++
			}
		}
		return start, end, base, true
	}
	return 0, 0, 0, false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hasUpperHex(b []byte) bool {
	for _, c := range b {
		if c >= 'A' && c <= 'F' {
			return true
		}
	}
	return false
}

// upper is ASCII uppercasing, which is all a hexadecimal digit needs.
func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func leadingZero(s string) bool {
	s = trimSign(s)
	return len(s) > 1 && s[0] == '0'
}

func trimSign(s string) string {
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		return s[1:]
	}
	return s
}

func padZero(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}
