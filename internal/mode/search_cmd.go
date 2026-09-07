package mode

import (
	"errors"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/search"
)

// The search half of the command line: /, ?, n, N, * and #.
//
// These are not in internal/motion's table and cannot be: / and ? need a
// command line to read a pattern from, * and # need the word under the cursor,
// and n and N need the last pattern, none of which fits behind a Func that is
// handed a buffer and a position. So the mode machine asks internal/search for
// a destination and turns the answer into a motion.Result of its own, which
// then goes through exactly the same operator path a w does. That is what
// makes d/foo and dw one code path and not two.
//
// The `:` command line belongs to internal/ex. This file is the search prompt
// and nothing else.

// The two directions, spelled here so that this file reads without a search.
const (
	searchForward  = search.Forward
	searchBackward = search.Backward
)

// cmdline is the / or ? prompt in progress.
type cmdline struct {
	dir  search.Direction
	text []byte
}

// Cmdline is what the search prompt currently holds, for a frontend to draw:
// the leading / or ? and the pattern typed so far.
func (e *Editor) Cmdline() string {
	if e.mode != Cmdline {
		return ""
	}
	return e.cmd.dir.String() + string(e.cmd.text)
}

// startSearch opens the search prompt. The pending operator, count and
// register are left exactly as they are: d/foo<CR> is a delete over whatever
// the search lands on, so the half-typed command has to survive the prompt.
func (e *Editor) startSearch(dir search.Direction) error {
	e.cmd = cmdline{dir: dir}
	e.pend.keys = nil
	e.mode = Cmdline
	return nil
}

// cmdlineKey feeds one key to the search prompt.
func (e *Editor) cmdlineKey(k key.Key) error {
	if e.wait != nil {
		f := e.wait
		e.wait = nil
		return f(k)
	}
	e.cmdKeys = append(e.cmdKeys, k)
	switch {
	case k == keyEsc:
		e.cmd = cmdline{}
		e.mode = Normal
		return e.abandon()
	case k == keyCR || k == keyNL:
		return e.runSearch()
	case k == keyBS || k == key.Ctrl('h'):
		if len(e.cmd.text) == 0 {
			e.cmd = cmdline{}
			e.mode = Normal
			return e.abandon()
		}
		e.cmd.text = e.cmd.text[:prevRune(e.cmd.text, len(e.cmd.text))]
		return nil
	case k == key.Ctrl('u'):
		e.cmd.text = nil
		return nil
	case k == key.Ctrl('w'):
		e.cmd.text = e.cmd.text[:cmdWordStart(e.cmd.text)]
		return nil
	case k == key.Ctrl('r'):
		e.wait = func(k key.Key) error {
			e.cmdKeys = append(e.cmdKeys, k)
			if !k.IsRune() || k.Rune > 0x7f {
				return nil
			}
			if v, err := e.regs.Get(byte(k.Rune)); err == nil {
				e.cmd.text = append(e.cmd.text, v.Bytes()...)
			}
			return nil
		}
		return nil
	case k.IsRune() && k.Mod == 0:
		e.cmd.text = append(e.cmd.text, string(k.Rune)...)
		return nil
	}
	return nil
}

// cmdWordStart is where CTRL-W deletes back to at the search prompt: vim's
// getcmdline() case Ctrl_W, which skips white space and then takes either a
// run of keyword bytes or a run of punctuation, whichever the byte in front of
// it is. Measured: "/abc de" then CTRL-W leaves "abc " with the space, and
// CTRL-W on an empty prompt leaves it empty rather than cancelling.
//
// The same rule is in internal/ex's Line.deleteWord for the ":" prompt, and it
// is written twice rather than shared because internal/mode cannot import
// internal/ex: the ex layer is built on top of this one.
func cmdWordStart(text []byte) int {
	white := func(c byte) bool { return c == ' ' || c == '\t' }
	word := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '_' || c >= 0xc0
	}
	i := len(text)
	for i > 0 && white(text[i-1]) {
		i--
	}
	if i > 0 && word(text[i-1]) {
		for i > 0 && word(text[i-1]) {
			i--
		}
		return i
	}
	for i > 0 && !word(text[i-1]) && !white(text[i-1]) {
		i--
	}
	return i
}

// runSearch is Enter at the search prompt.
func (e *Editor) runSearch() error {
	line := string(e.cmd.text)
	dir := e.cmd.dir
	e.cmd = cmdline{}
	e.mode = Normal
	if e.pend.op != operator.OpNone {
		e.mode = OperatorPending
	}

	res, err := e.search.Do(e.buf, e.cur, dir, line, e.searchCount(), e.opt.Search())
	e.regs.SetLastSearch(e.search.Pattern)
	return e.searchResult(res, err, dir, true)
}

// searchCount is the count typed before the search, with the two counts of an
// operator command multiplied as everywhere else.
func (e *Editor) searchCount() int {
	if n := e.pend.count(); n > 0 {
		return n
	}
	return 1
}

// searchNext is n and N: the last pattern again, in its own direction or the
// other one.
func (e *Editor) searchNext(same bool, count int) error {
	dir := e.search.Dir
	if !same {
		dir = dir.Reverse()
	}
	res, err := e.search.Next(e.buf, e.cur, !same, count, e.opt.Search())
	return e.searchResult(res, err, dir, true)
}

// searchWord is *, #, g* and g#: the keyword under or after the cursor,
// searched for whole with the first two and anywhere with the second two.
func (e *Editor) searchWord(s string, count int) error {
	whole := s == "*" || s == "#"
	dir := searchForward
	if s == "#" || s == "g#" {
		dir = searchBackward
	}
	res, err := e.search.Word(e.buf, e.cur, dir, whole, count, e.opt.Search())
	e.regs.SetLastSearch(e.search.Pattern)
	// E348 is * or # with nothing under the cursor to search for. vim never
	// gets as far as a search command, so nothing is echoed and the pattern
	// from the last search is still sitting in the state to be echoed wrongly
	// if this asked for one.
	var noString search.NoStringError
	return e.searchResult(res, err, dir, !errors.As(err, &noString))
}

// searchResult turns what internal/search answered into a cursor move or an
// operator, and puts vim's own messages on the message line in vim's order:
// the search command echoed back, then the wrap notice, then whatever went
// wrong. echo is false for the errors vim raises before it has a search
// command to print at all.
func (e *Editor) searchResult(res search.Result, err error, dir search.Direction, echo bool) error {
	// vim echoes the whole search command back onto the message line and then
	// leaves it, which the redirect catches as the command, a trailing space
	// and a newline. n and N echo too, in the direction they are going, which
	// is why N after /foo prints "?foo". Nothing is echoed when there was no
	// pattern to search for: * on an empty line is E348 and no echo, because
	// vim gives up before it has a search command to print.
	if echo && e.search.Pattern != "" {
		e.msg.say(e.search.Command(dir) + " ")
		e.msg.empty()
	}

	// Once per wrap, and a count wraps more than once: give_warning() runs
	// inside the search loop, so "3*" on a one-match file says it three times
	// and the redraw after the command puts the last one back for a fourth.
	// give_warning() sets keep_msg, which is what that fourth is -- unless the
	// E486 below overwrites it first, which is why a failed search prints one
	// fewer.
	for i := 0; i < max(res.Wraps, boolToInt(res.Wrapped)); i++ {
		e.msg.sayKeep(search.WrapMessage(dir))
	}
	if err != nil {
		e.Say(err.Error())
		return e.beep()
	}

	kind := motion.KindCharExclusive
	switch {
	case res.Linewise:
		kind = motion.KindLine
	case res.Inclusive:
		kind = motion.KindCharInclusive
	}
	m := motion.Result{To: res.Pos, Kind: kind, Ok: true, Jump: true, Count: 1}
	// Ahead of the operator, because vim's do_search() calls setpcmark() on
	// its way to the match and do_pending_operator only runs after the motion
	// has answered. Measured on 200 lines: "100Gy/line150<CR>" leaves line 100
	// in the jumplist even though the yank puts the cursor back on it.
	e.setPCMark(e.cur)
	if e.pend.op != operator.OpNone {
		return e.operateOverMotion(m, byte(dir.String()[0]))
	}
	e.cur = e.buf.Clamp(res.Pos)
	e.setCurswant(m)
	e.finish(false)
	return nil
}

// boolToInt is the fallback for a Result that says a wrap happened but does
// not say how many times, which is every path that builds one by hand.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
