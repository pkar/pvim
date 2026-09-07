package main

import (
	"errors"
	"strconv"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/keymap"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/tags"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// session is one editor and the window it is looked at through.
//
// It exists because the scrolling commands are the one family of keys that
// belongs to neither side on its own. internal/mode cannot run CTRL-U: the
// only window it has is motion.Window, which is four numbers describing what
// is on screen and has no way to move it. internal/window cannot run CTRL-U
// either: it has the arithmetic but not the count the user typed and not the
// mode the editor is in. So the frontend layer owns the join, and this is it,
// used by the headless --oracle path and by the terminal loop beside it.
//
// That split is why 211 of the 270 differences in the last fuzz run were
// scrolling and window keys. Every one of them reached the mode machine, which
// refused it with "key not implemented" and exited 1, and the harness reported
// a difference it could not describe. With a window and this router they are
// ordinary keys with measured answers.
type session struct {
	ed *mode.Editor
	// w0 is the window this session was built over, and it is a fallback and
	// not the answer. See session.win: internal/ex moves the current window
	// out from under this struct on every split, every tab and every ":copen",
	// so a session that measured the window it was constructed with answered
	// H, M, L, CTRL-D and every z command against a window that is no longer
	// on screen. Measured on 60 lines: ":sp" then L then ":only" leaves vim on
	// line 14 and this on 34, because 34 is where L lands in the 39-row window
	// the split halved.
	w0  *window.Window
	ctx *ex.Context
	// relayout re-shares the screen between the windows of the current tab,
	// or nil in a session nobody has given a screen size to. It is a hook and
	// not a call into the editor because this struct deliberately holds no
	// editor: it joins a mode machine to a window, and the screen the windows
	// are laid out on belongs to the thing that owns both.
	relayout func()
	// so is the resolved 'scrolloff' for the window: 5 under a plain
	// "vim --clean", 3 under this vimrc. The window needs it on every scroll
	// and every cursor correction, and reading it once at startup is right
	// only until ":set scrolloff" exists, which is the ex layer's to do.
	so int
	// lastSync is where the cursor was when this session last put the window
	// under it, which is how a keystroke that moved nothing is told from one
	// that moved something. See sync.
	lastSync text.Pos
	// zCount is the count typed between a z and its command, which is the
	// window height for "z{count}<CR>" and nothing this editor answers for
	// anything else.
	zCount int
	// pendingZ says a "z" is held back waiting for the key that says whether
	// it was zt or zf, and pendingW the same for CTRL-W. pendingG is the same
	// again for the two g commands that need the tab list: gt and gT.
	pendingZ bool
	pendingW bool
	pendingG bool
	// pendingWG says the CTRL-W being held is the g prefix: CTRL-W g t, g T,
	// g f, g ] and the rest are three keys and not two, and the third one has
	// its own little table. See session.windowGCommand.
	pendingWG bool
	// wCount is the count typed between the CTRL-W and the command after it.
	// vim multiplies it by the one typed in front of the CTRL-W, so
	// "2 CTRL-W 3 i" is the sixth match. Only the identifier commands read
	// it; every other CTRL-W command this editor answers is a no-op with one
	// window.
	wCount int
	// pendingZZ says an uppercase Z has gone past, which is half of ZZ or ZQ.
	// Unlike the other two it holds nothing back: both keys go to the mode
	// machine as they always did and this only watches, because internal/mode
	// answers ZZ and ZQ identically and the frontend is what has to know which
	// one writes.
	pendingZZ bool
	// writeOnQuit says the quit that is coming was a ZZ and not a ZQ.
	writeOnQuit bool
	// line is the ":" prompt while one is open, nil the rest of the time.
	line *ex.Line

	// feed is the keys that have arrived and not yet been dispatched. It is a
	// queue and not a loop variable because a command may ask for the next
	// key itself: ":4,2d" prompts "Backwards range given, OK to swap (y/n)?"
	// and reads the answer out of the same stream the command came from,
	// which is what vim does under "-s" and what makes such a case gradeable.
	feed  []key.Key
	getch func() (key.Key, bool)

	// keys is the map table the vimrc filled in, or nil when nothing is
	// mapped. It lives on the session and not on the editor because the
	// session is what turns a keystroke into a command, and because the
	// resolver in front of that dispatch is the only thing that reads it.
	keys *keymap.Table
	// maps is the mapping resolver, or nil when nothing is mapped. Nil is the
	// state every --oracle run is in, because a graded run reads no vimrc, and
	// it is the state this file was in before mappings existed: Key hands the
	// key straight to keyRaw and nothing else happens. See cmd/pvim/keymap.go.
	maps *keymap.Machine
	// mapBuf answers which buffer a <buffer> mapping would belong to, and is
	// nil alongside a nil maps.
	mapBuf func() int

	// tagStacks is one tag stack per window, made on first use. Vim keeps the
	// stack on the window and hands a split a copy of it, so this is a map and
	// not a field: see cmd/pvim/tags.go, which owns every entry in it.
	tagStacks map[*window.Window]*tags.Stack

	// spell is the spell checker and its added-word lists, nil until a window
	// with 'spell' set asks for one. See cmd/pvim/spell.go.
	spell *spellState
}

// newSession joins an editor to a window. The caller has already sized the
// window and set its options.
func newSession(ed *mode.Editor, win *window.Window, ctx *ex.Context, scrollOff int) *session {
	s := &session{ed: ed, w0: win, ctx: ctx, so: scrollOff}
	s.sync()
	return s
}

// win is the window every key in this file measures against: the current tab's
// current window, asked for freshly each time.
//
// Asked for and not remembered, which is the whole point. internal/ex replaces
// the current window in exSplit, in openScratch for the ":!" output and in
// ":copen", and it changes the current tab in ":tabnew" and ":tabclose"; a
// pointer taken at construction survives all of that and points at a window
// that may not be on the screen any more. The fallback is the window the
// session was built with, which is what a Context with no tab pages -- the
// vimrc loader builds one -- has to fall back to.
func (s *session) win() *window.Window {
	if s.ctx != nil {
		if w := s.ctx.Window(); w != nil {
			return w
		}
	}
	return s.w0
}

// Run feeds a whole script through the session, one key at a time.
//
// One at a time and not through Editor.Keys because the window has to follow:
// vim scrolls when a motion leaves the visible lines, and H, M and L are then
// measured against where it scrolled to. A run that set the window once at the
// start would answer H correctly on a file that fits on the screen and wrongly
// on every other.
func (s *session) Run(keys []key.Key) error {
	s.feed = append(s.feed, keys...)
	for {
		k, ok := s.next()
		if !ok {
			// The input has run out. A mapping half typed at this point is
			// held by the resolver and would never fire; see flushMaps for
			// what vim does instead and why pvim cannot.
			return s.flushMaps()
		}
		if err := s.Key(k); err != nil {
			return err
		}
	}
}

// next takes the next key off the queue, or asks getch for one when the queue
// is empty and something is there to ask.
//
// getch is how the terminal frontend answers a prompt: the queue is empty
// there, because keys arrive one event at a time, so the callback reads the
// next event off the same channel the loop reads. Under --oracle it is nil and
// an empty queue is the end of the script, which is what vim does when a "-s"
// file runs out in the middle of a prompt.
func (s *session) next() (key.Key, bool) {
	if len(s.feed) > 0 {
		k := s.feed[0]
		s.feed = s.feed[1:]
		return k, true
	}
	if s.getch != nil {
		return s.getch()
	}
	return key.Key{}, false
}

// prompt is ex.Context's Prompt: a question on the command line answered by
// the next keystroke.
//
// It answers with the first key that is one of the choices, ignoring anything
// else, which is vim's own behaviour at "Backwards range given, OK to swap
// (y/n)?" and at the 'confirm' prompt. Running out of input answers with the
// last choice, which for both of those is the one that does nothing.
func (s *session) prompt(question, choices string) (byte, error) {
	s.ed.SayPrompt(question)
	for {
		k, ok := s.next()
		if !ok {
			if choices == "" {
				return 0, nil
			}
			return choices[len(choices)-1], nil
		}
		if k.Special == key.KeyEsc {
			if choices == "" {
				return 0, nil
			}
			// Escape is the last choice, which is "n" on the swap prompt and
			// "C" on the 'confirm' one, and vim echoes it as if it had been
			// typed: the redirect holds "(y/n)?n".
			c := choices[len(choices)-1]
			s.ed.SayAnswer(c)
			return c, nil
		}
		if !k.IsRune() || k.Rune > 0x7f {
			continue
		}
		c := byte(k.Rune)
		for i := 0; i < len(choices); i++ {
			if choices[i] == c {
				// vim echoes the answer at the end of the prompt, so the
				// message log holds "(y/n)?y" on one line.
				s.ed.SayAnswer(c)
				return c, nil
			}
		}
	}
}

// promptQuiet is the ":s" "c" flag's prompt.
//
// It differs from prompt in the two ways vim's do_sub differs from
// ask_yesno(): the key that answers is not echoed, and the prompt is wiped off
// the line afterwards, which the redirect sees as a bare newline. It also does
// not set msg_scroll, so a substitute behind it still keeps its count message
// across the redraw -- ":%s/aaa/X/gc" answered "a" says "4 substitutions on 4
// lines" twice.
func (s *session) promptQuiet(question, choices string) (byte, error) {
	s.ed.Say(question)
	answer := byte(0)
	for answer == 0 {
		k, ok := s.next()
		switch {
		case !ok:
			if choices == "" {
				s.ed.NewMessageLine()
				return 0, nil
			}
			answer = choices[len(choices)-1]
		case k.Special == key.KeyEsc:
			if choices == "" {
				s.ed.NewMessageLine()
				return 0, nil
			}
			answer = choices[len(choices)-1]
		case !k.IsRune() || k.Rune > 0x7f:
		default:
			c := byte(k.Rune)
			for i := 0; i < len(choices); i++ {
				if choices[i] == c {
					answer = c
				}
			}
		}
	}
	s.ed.NewMessageLine()
	return answer, nil
}

// Key feeds one keystroke to whichever half of the session owns it.
//
// Four families never reach the mode machine, because it has no answer for
// them and says so with an error: the scrolling keys, the z commands that
// redraw, CTRL-W, and ":". Everything else goes straight through, and after it
// the window follows the cursor so that the next H, M or L measures against
// where vim would have scrolled to.
//
// The order matters. An open command line takes every key, then the half-typed
// prefixes, then the keys that are only commands in normal and visual mode,
// then the mode machine. A key tested after the mode machine has seen it is a
// key the mode machine has already refused.
func (s *session) Key(k key.Key) error {
	if s.maps == nil {
		return s.keyRaw(k)
	}
	// The resolver sees the key before the editor does, which is where vim
	// applies a mapping too: mappings are read out of the typeahead and the
	// command that runs never learns which keys were typed and which a
	// right-hand side produced. Everything below keyRaw -- the count, the
	// operator, the register, the macro recorder -- therefore works on the
	// keys a mapping produced, and "map { gT" needs nothing from any of them.
	s.maps.Push(k)
	return s.drainMaps()
}

// keyRaw is Key with the mapping layer already behind it: one keystroke, as
// the editor is going to execute it.
func (s *session) keyRaw(k key.Key) error {
	if s.line != nil {
		return s.cmdlineKey(k)
	}
	if s.pendingW {
		s.pendingW = false
		return s.windowCommand(k)
	}
	if s.pendingZ {
		if k.IsRune() && k.Rune >= '0' && k.Rune <= '9' {
			// "z{count}<CR>" sets the window height, so the digits after a z
			// belong to the z and not to the next command. vim's nv_zet reads
			// them itself, which is why "z2N" beeps at the N rather than
			// running N with a count of two.
			s.zCount = s.zCount*10 + int(k.Rune-'0')
			return nil
		}
		n := s.zCount
		s.pendingZ, s.zCount = false, 0
		if n > 0 {
			if k.Special == key.KeyCR {
				// z{height}<CR>: make the window that many rows and redraw
				// with the cursor line at the top, which is what the rest of
				// z<CR> does.
				s.win().SetHeight(n)
				s.scroll(window.RedrawTopFirst)
				return nil
			}
			// A count on any other z command is not one this editor answers,
			// and vim beeps at most of them too.
			return s.ed.Beep()
		}
		if cmd, ok := zScroll(k); ok {
			s.scroll(cmd)
			return nil
		}
		if cmd, ok := zSide(k); ok {
			s.sideScroll(cmd)
			return nil
		}
		// Not one of the z scrolling commands: zf and the rest are the mode
		// machine's, and it has to see the z as well as the key after it or
		// it sees an operator with no name.
		//
		// The count in front of the z is read before the mode machine is
		// handed the key, because handing it the key is what consumes it:
		// "2zg" is vim's second entry of 'spellfile' and by the time the z
		// family's fallback below sees the g there is no count left to read.
		pre := showcmdCount(s.ed.Showcmd())
		if err := s.ed.Key(key.Rune('z')); err != nil {
			return err
		}
		if err := s.pass(k); err != nil {
			return s.zFallback(k, err, pre)
		}
		return nil
	}
	if s.pendingG {
		s.pendingG = false
		if k == key.Rune('t') || k == key.Rune('T') {
			return s.tabStep(k == key.Rune('t'))
		}
		if (k == key.Rune('f') || k == key.Rune('F')) && s.ed.Mode() == mode.Normal {
			// gf and gF: the file under the cursor, opened in this window.
			// They are here beside gt for the same reason -- the mode machine
			// has no buffer list and no window to open a file in -- and their
			// count is vim's count'th match in 'path'. See
			// cmd/pvim/windowcmd.go, which owns all six file-under-cursor
			// keys.
			//
			// Normal mode only. v_gf and v_gF are a different command: they
			// take the name from the highlighted text and not from under the
			// cursor, so answering them with this one would be a wrong answer
			// rather than a missing one, and the mode machine goes on getting
			// the g and the f exactly as it did before this existed.
			count := showcmdCount(s.ed.Showcmd())
			s.clearCount()
			return s.gotoFile(fileHere, k == key.Rune('F'), count)
		}
		// Not one of the four: the mode machine owns the rest of the g family
		// and has to see the g as well as the key behind it, exactly as it
		// does for a z that turned out to be zf.
		if err := s.ed.Key(key.Rune('g')); err != nil {
			return err
		}
		return s.pass(k)
	}
	if cmd, ok := plainScroll(k); ok && s.atCommand() {
		s.scroll(cmd)
		return nil
	}
	if s.pendingZZ {
		s.pendingZZ = false
		if k != key.Rune('Z') && k != key.Rune('Q') {
			// ZZ and ZQ are the whole of the Z family. vim's nv_Zet beeps at
			// anything else, so "Z(" leaves the buffer and the message line
			// exactly as they were.
			return s.ed.Beep()
		}
		s.writeOnQuit = k == key.Rune('Z')
		return s.pass(k)
	}
	if gitKey(s, k) {
		// The status buffer's "-" and "cc", the blame window's <CR>, and ]c,
		// [c, do and dp in a window in diff mode. It is asked on every key
		// and not only on those, because the blame window's scroll lock and
		// the pickup of a finished "git commit" are polls: this editor has no
		// CursorMoved autocommand and a goroutine has no way to wake the
		// loop. See cmd/pvim/git.go, which owns all of it and answers false
		// for every key that is not its own.
		return nil
	}
	if k == key.Rune('Z') && s.atCommand() {
		s.pendingZZ = true
		return s.pass(k)
	}
	if k == key.Ctrl('w') && s.atCommand() {
		// Held for the same reason a z is: CTRL-W is a prefix and the key
		// after it decides everything.
		s.pendingW = true
		return nil
	}
	if k == key.Rune(':') && s.atCommand() {
		return s.startCmdline()
	}
	if (k == key.Ctrl(']') || k == key.Ctrl('t')) && s.ed.Mode() == mode.Normal && s.atCommand() {
		// The tag keys. They are here beside gf and CTRL-W for the same
		// reason: a tag jump opens a file and may split a window, and the mode
		// machine has neither a buffer list nor a window. Normal mode only,
		// because v_CTRL-] takes its name from the highlighted text rather
		// than from under the cursor and answering it with this one would be a
		// wrong answer rather than a missing one.
		count := showcmdCount(s.ed.Showcmd())
		s.clearCount()
		if k == key.Ctrl('t') {
			return s.tagPop(count)
		}
		return s.tagCommand(count, pickCount, tagHere)
	}
	if k == key.Rune('z') && s.atCommand() {
		// Held rather than passed on, because the key that decides whether
		// this was zt or zf has not arrived yet. A z at the very end of a
		// script is dropped, which is what vim does with a half-typed command
		// when the input runs out.
		s.pendingZ = true
		return nil
	}
	if k == key.Rune('g') && s.atCommand() {
		// Held for gt and gT, and for nothing else: every other g command is
		// the mode machine's and is handed straight back on the next key. The
		// two that are not are the two the vimrc maps { and } to, and they
		// need the tab list, which the mode machine does not have and is not
		// going to get -- the same reason z, CTRL-W and the scrolling keys are
		// in this file.
		s.pendingG = true
		return nil
	}
	return s.pass(k)
}

// clearPrefixes throws away the half-typed commands this file is holding: the
// z of a z command, the CTRL-W of a window command, the Z of a ZZ and the g of
// a gt.
//
// It exists for the mouse. A click throws away a half-typed command --
// measured: "3" then a click then "x" deletes one character and not three --
// and internal/mode does that for the prefixes it holds itself. The four above
// are held here instead, because each of them needs a window or a tab list,
// and a click that cleared only the mode machine's would leave "g" armed and
// turn the next "x" into a "gx". cmd/pvim/mouse.go owns the click and calls
// this; nothing else should, because every other way out of a half-typed
// prefix is the key that completes it.
func (s *session) clearPrefixes() {
	s.pendingZ, s.zCount = false, 0
	s.pendingW, s.pendingWG, s.wCount = false, false, 0
	s.pendingZZ = false
	s.pendingG = false
}

// pass hands a key to the mode machine and then puts the window back under the
// cursor.
func (s *session) pass(k key.Key) error {
	if err := s.ed.Key(k); err != nil {
		var kw *mode.KeywordError
		if errors.As(err, &kw) {
			return s.keywordPrg(kw)
		}
		return err
	}
	s.sync()
	return nil
}

// keywordPrg finishes a K.
//
// The mode machine found the word under the cursor and built the command line
// 'keywordprg' wants; running it needs a shell, which is the ex layer's. No
// NewMessageLine in front of it, and that is the difference between this and a
// ":!" the person typed: vim's K never opened a command line, so the newline
// one leaves behind is not in the redirect. Measured, "K" over "foo" leaves
// "\n:!man 'foo'\r\n\nshell returned 1\n" where ":!man 'foo'" typed by hand
// leaves the same with one newline more in front.
func (s *session) keywordPrg(kw *mode.KeywordError) error {
	if s.ctx == nil {
		return kw
	}
	if err := s.ctx.RunLine(kw.Cmd); err != nil {
		if msg := errorMessage(err, kw.Cmd); msg != "" {
			s.ed.Say(msg)
		}
	}
	s.sync()
	s.ed.Redisplay()
	return nil
}

// atCommand reports whether the editor is where a scrolling key, a CTRL-W or a
// ":" means what this file thinks it means: in normal or visual mode, with at
// most a count half typed.
//
// Two things make it not. In insert and replace mode CTRL-E, CTRL-Y, CTRL-D
// and CTRL-U are different commands entirely -- the character below, the
// character above, and the two indent keys -- and they belong to the mode
// machine, which implements them. And with an operator or a register half
// typed, vim cancels rather than scrolls, which is what handing the key to the
// mode machine does.
func (s *session) atCommand() bool {
	switch s.ed.Mode() {
	case mode.Normal, mode.VisualChar, mode.VisualLine, mode.VisualBlock:
	default:
		return false
	}
	// Not while the mode machine is holding the next key for something it has
	// already started. A register name, an f target and a mark name are all
	// read that way, and any of them may be a z or a CTRL-W.
	return !s.ed.Waiting() && isCountOrRegister(s.ed.Showcmd())
}

// scroll runs one scrolling command with whatever count was typed in front of
// it, and leaves the editor's cursor where the window put it.
//
// publish and not sync on the way out, and that is the whole point of having
// two. A scrolling command is allowed to leave the window past the end of the
// buffer, where update_topline never would: CTRL-F on line 1 of a 60-line file
// in a 39-row window gives a top line of 38 and three rows of "~", measured,
// while G on the same file stops at 22 so that the last line sits on the last
// row. Running ScrollToCursor after a scroll applies the second rule to the
// first and drags the top line from 38 back to 22, which is H off by sixteen
// lines and every case here failing.
func (s *session) scroll(cmd window.ScrollCmd) {
	count := showcmdCount(s.ed.Showcmd())
	s.clearCount()
	s.win().View.Cursor = s.ed.Cursor()
	before := s.win().View.Cursor.Line
	s.win().Scroll(cmd, count, s.so)
	// 'startofline', which is on by default and which the vimrc leaves on:
	// CTRL-D, CTRL-U, CTRL-B and CTRL-F land on the first non-blank of the
	// line they reach. Only when they reached a different line -- a CTRL-U
	// with the window already at the top beeps before vim's beginline() and
	// leaves the column alone. Measured on " alpha one" and friends with the
	// cursor in column 6: CTRL-D and CTRL-F give column 3, CTRL-U and CTRL-B
	// at the top of the file give 6, and CTRL-E and CTRL-Y, which the option
	// does not cover, give 6 wherever they are.
	if solScroll(cmd) && s.startOfLine() && s.win().View.Cursor.Line != before {
		s.win().FirstNonBlank()
	}
	s.ed.SetCursor(s.win().View.Cursor)
	s.publish()
}

// sideScroll runs one of the six horizontal z commands.
//
// It is the sideways twin of scroll above and it is separate for the reason
// the two families are separate in vim: zh and zl move the window and let the
// cursor follow it back into view, where zs and ze move the window to where
// the cursor already is and leave the cursor alone. internal/window.SideScroll
// owns both rules and the measurements behind them; what is here is the count,
// the option values and putting the cursor the window moved back into the mode
// machine.
//
// A command that moved nothing does not beep, which is measured and not an
// omission: `zh` with the window already at column 0 leaves the message line
// empty in vim, where CTRL-B at the top of the file does the same. The bell is
// for a key vim does not have, and vim has all six of these.
func (s *session) sideScroll(cmd window.SideCmd) {
	count := showcmdCount(s.ed.Showcmd())
	s.clearCount()
	w := s.win()
	w.View.Cursor = s.ed.Cursor()
	w.SideScroll(cmd, count, s.side())
	s.ed.SetCursor(w.View.Cursor)
	s.publish()
}

// side is the three option values the horizontal arithmetic reads.
//
// 'sidescrolloff' is the global-local one and is 5 under this vimrc and 0 under
// a plain "vim --clean"; 'sidescroll' is 0 by default, which means a scroll
// that has to move puts the cursor in the middle rather than sliding by a
// column; 'tabstop' is what turns a byte column into a display one, and it is
// the current buffer's because that is the line being measured.
//
// Read on every call rather than once at construction, unlike 'scrolloff'
// beside it, because all three can move under a ":set".
func (s *session) side() window.Side {
	if s.ctx == nil || s.ctx.Opt == nil {
		return window.Side{TabStop: 8}
	}
	o := s.ctx.Opt
	return window.Side{
		ScrollOff: o.SideScrollOffValue(),
		Scroll:    o.G.SideScroll,
		TabStop:   o.B.TabStop,
	}
}

// solScroll is the four scrolling commands 'startofline' names. CTRL-E and
// CTRL-Y are not on the option's list and the z family puts the cursor where
// each of its members puts it, which internal/window already knows.
func solScroll(cmd window.ScrollCmd) bool {
	switch cmd {
	case window.HalfUp, window.HalfDown, window.PageBack, window.PageForward:
		return true
	}
	return false
}

// startOfLine is 'startofline'. It defaults to on, which is what vim does and
// what a session built without an option bag has to answer.
func (s *session) startOfLine() bool {
	if s.ctx == nil || s.ctx.Opt == nil {
		return true
	}
	return s.ctx.Opt.G.StartOfLine
}

// clearCount throws away a count the mode machine is holding for a command it
// is never going to see. Escape is how, because the count lives in the mode
// machine's own pending state and Escape is the key that empties it; it says
// nothing on the message line in normal mode, which is what makes it usable
// here.
func (s *session) clearCount() {
	if s.ed.Showcmd() == "" {
		return
	}
	// The error is dropped deliberately: Escape in normal mode cannot fail,
	// and a session that refused to scroll because clearing a count reported
	// something would be a worse bug than the one it reported.
	_ = s.ed.Key(key.Key{Special: key.KeyEsc})
}

// sync points the window at the cursor and tells the editor what is on screen.
//
// Both halves every time, and in that order. vim's update_topline runs after
// every command that moved the cursor, and H, M and L are then measured
// against the window it left; a session that scrolled only when a key asked it
// to would answer H correctly on a file that fits on the screen and wrongly on
// every other.
// It scrolls only when the cursor is outside the margin, which is vim's
// update_topline and not a shortcut: a window left showing past the end of the
// buffer by CTRL-F or by zt stays there while the cursor moves about inside
// it, and ScrollToCursor unasked would pull the top line back to where the
// last buffer line sits on the last row. Measured, CTRL-F then H then x then M
// on a 60-line file in a 39-row window: vim reports w0 38 throughout and M
// lands on 49; a sync that scrolled every time reports 22 and lands on 41.
func (s *session) sync() {
	// The layout first, because everything below measures a window: ":sp",
	// ":tabnew", ":copen" and ":only" all change how many windows are sharing
	// the screen, and every one of them lands here through the ex layer
	// without having said what the new heights are.
	wasHigh := s.win().View.Height
	if s.relayout != nil {
		s.relayout()
	}
	cur := s.ed.Cursor()
	// Whether this sync is a window that changed size rather than a cursor
	// that moved. ":sp" is both at once from here -- the layout halves the
	// height and the cursor is where it was -- and the two want different
	// scroll rules, so they are told apart before either is applied.
	resized := s.win().View.Height != wasHigh && cur == s.lastSync
	half := s.ed.Showcmd() != "" && cur == s.lastSync
	s.win().View.Cursor = cur
	// Not while a command is half typed and the cursor has not moved. vim
	// reaches update_topline only with VALID_TOPLINE cleared, and typing the
	// "3" of "3H" clears nothing: the window stays exactly where the scroll
	// before it put it. Correcting on every key instead undid that scroll one
	// keystroke later. Measured on five lines in a 39-row window at
	// 'scrolloff' 5: CTRL-F then `Hx` empties line 5 in both editors, and
	// CTRL-F then `3Hx` empties vim's line 5 and, without this, line 3 here --
	// the digit was the whole of the difference. It shows only where a scroll
	// has left the window somewhere update_topline would not have put it,
	// which on a five-line file is any CTRL-F at all and on a sixty-line file
	// is nothing.
	if !half && s.outOfView() {
		// ScrollToCursor centres, which is what a cursor that JUMPED wants and
		// not what a window that shrank under a stationary cursor wants: vim
		// runs update_topline there, which moves the top line as little as it
		// can. Measured on 100 lines with the cursor on 50: ":sp" then "H"
		// lands on 42 in vim and 46 with the centring rule, because vim's
		// topline is 37 and the centring one picks 41.
		if resized {
			s.win().ScrollToCursorMinimal(s.so)
		} else {
			s.win().ScrollToCursor(s.so)
		}
	}
	// The sideways half, which is vim's curs_columns and which has no
	// out-of-view test of its own: SideScrollToCursor does nothing when the
	// cursor is already between the margins, and under 'wrap' it does nothing
	// at all. Without it 'leftcol' is 0 for the life of the session, "200|"
	// leaves the cursor drawn off the right-hand edge of a 120-column window,
	// and zH and ze scroll from a place the cursor is not.
	s.win().SideScrollToCursor(s.side())
	s.publish()
}

// outOfView reports whether the cursor is outside the window's 'scrolloff'
// margin, which is the condition vim's update_topline scrolls on.
//
// The margin is suspended at each end of the buffer, the same exemption
// window.AtTop and window.AtBottom make for H and L: with the window against
// the top, line 1 is inside the margin, and with the last line on screen, so
// is the last line.
func (s *session) outOfView() bool {
	v := s.win().Visible()
	line := s.win().View.Cursor.Line
	if !v.AtTop && line < v.Top+s.so {
		return true
	}
	if !v.AtBottom && line > v.Bottom-s.so {
		return true
	}
	return line < v.Top || line > v.Bottom
}

// publish tells the editor what is on screen and moves nothing.
//
// It is the half of sync a scrolling command wants: the window has just been
// put exactly where the command said, and the only thing left is for H, M and
// L to be able to see it.
func (s *session) publish() {
	// Where the cursor was when the window was last put under it, which is
	// what sync compares against to tell a keystroke that moved nothing from
	// one that moved something. It is recorded here and not in sync because
	// scroll ends here too, and the scroll is exactly the thing whose result
	// the next keystroke must not undo.
	s.lastSync = s.ed.Cursor()
	v := s.win().Visible()
	s.ed.SetWindow(motion.Window{
		Top:      v.Top,
		Bottom:   v.Bottom,
		AtTop:    v.AtTop,
		AtBottom: v.AtBottom,
		LeftCol:  v.LeftCol,
		Height:   v.Height,
	})
}

// plainScroll maps the six control keys onto the scrolling commands.
func plainScroll(k key.Key) (window.ScrollCmd, bool) {
	switch k {
	case key.Ctrl('u'):
		return window.HalfUp, true
	case key.Ctrl('d'):
		return window.HalfDown, true
	case key.Ctrl('b'):
		return window.PageBack, true
	case key.Ctrl('f'):
		return window.PageForward, true
	case key.Ctrl('y'):
		return window.LineUp, true
	case key.Ctrl('e'):
		return window.LineDown, true
	}
	return 0, false
}

// tabStep is gt and gT: the two normal-mode commands that move between tab
// pages, and the two the vimrc's "map { gT" and "map } gt" exist to reach.
//
// The count rule is vim being vim and is measured rather than assumed. Over
// four tab pages, sitting on the fourth: "gt" wraps to 1, "2gt" goes to tab 2,
// "9gt" clamps to 4 and "0gt" is a bare gt; "gT" goes to 3, "5gT" with four
// pages goes to 3 as well. So a count on gt is an ABSOLUTE tab number and a
// count on gT is a repeat. internal/window.Tabs.Next and .Prev already carry
// both rules and their own measurements; this is the key binding they never
// had.
//
// Visual mode ends here, because it does in vim: "v l gt" over four tabs
// answers tabpagenr() 1 and mode() "n". Vim's reason is that visual mode
// belongs to a window and the tab page it switched to is showing a different
// one; pvim has one mode machine for the whole editor, so the Escape is how it
// arrives at the same answer.
func (s *session) tabStep(forward bool) error {
	count := showcmdCount(s.ed.Showcmd())
	s.clearCount()
	if s.ctx == nil || s.ctx.Tabs == nil {
		return s.ed.Beep()
	}
	switch s.ed.Mode() {
	case mode.VisualChar, mode.VisualLine, mode.VisualBlock:
		if err := s.ed.Key(key.Key{Special: key.KeyEsc}); err != nil {
			return err
		}
	}
	// The count goes in front, because internal/ex's table marks both
	// commands RangeCount: ":3tabnext" is the count form and ":tabnext 3" is
	// an argument neither takes.
	cmd := "tabprevious"
	if forward {
		cmd = "tabnext"
	}
	if count > 0 {
		cmd = strconv.Itoa(count) + cmd
	}
	if err := s.ctx.RunLine(cmd); err != nil {
		if msg := errorMessage(err, cmd); msg != "" {
			s.ed.Say(msg)
		}
		return nil
	}
	if s.relayout != nil {
		s.relayout()
	}
	s.sync()
	return nil
}

// zFallback answers a z command the mode machine refused.
//
// The folds are later work's and until they exist vim's answer to the keys that
// reach them is a message this editor can give correctly: there is no fold
// here. The four word-list keys go to cmd/pvim/spell.go, which answers all of
// them -- including vim's own E764 when there is no 'spellfile' to write to,
// which is every graded run. Everything else in the z family that is not a
// command at all -- zN, zy, z, and the rest -- is vim's clearopbeep, which is
// why the default is a beep and not a refusal. Measured, key by key, over the
// whole printable range: these messages and silence cover all of it except
// z=, which is the suggestion list this editor does not build.
//
// count is the count typed in front of the z, which only the word-list keys
// read: it picks which entry of 'spellfile' is written.
func (s *session) zFallback(k key.Key, err error, count int) error {
	if !errors.Is(err, mode.ErrNotImplemented) || !k.IsRune() {
		return err
	}
	switch k.Rune {
	case 'a', 'c', 'd', 'o', 'A', 'C', 'D', 'O':
		s.ed.Say("E490: No fold found")
		return nil
	case 'g', 'w', 'G', 'W':
		// The four word-list keys. See cmd/pvim/spell.go: 'g' and 'w' write
		// the file 'spellfile' names, which under --oracle there is none of
		// and the answer is still vim's E764; 'G' and 'W' write the list that
		// lasts the session.
		return s.spellAdd(k.Rune, count)
	case '=':
		// The one real command this editor has not written. z= is the
		// spelling suggestion list, and it is deliberately out of scope: a
		// wordlist underline is later work and suggestion ranking is a separate
		// two weeks nobody has asked for. Refusing it loudly keeps the gap
		// visible; beeping would make it look like a key vim also does nothing
		// with.
		//
		// zG and zW left this case when internal/spell was written: they are
		// word-list commands and not suggestions, and the list they write is
		// a real file in a temp directory, exactly as vim's is. No oracle
		// case can grade the message either way, because the file name in it
		// is different on every run in both editors.
		// zy is not here any more: it is an operator, the mode machine has
		// it, and a bare "zy" at the end of a script is a motion that never
		// arrived and does nothing at all, in both editors. zp and zP left
		// for the same reason: internal/mode puts a block without its
		// trailing white space now, so they never reach this.
		return err
	}
	return s.ed.Beep()
}

// zScroll maps the second key of the z family onto a vertical scrolling
// command.
//
// The eight that redraw are here and the rest of z is not: zf, za, zo, zc, zg
// and z= are folds, spelling and the mode machine's business, and the sideways
// six are zSide below. z<CR>, z., z-, z+ and z^ move the cursor to the first
// non-blank of its line as well as scrolling, which is the whole difference
// between them and zt, zz and zb.
func zScroll(k key.Key) (window.ScrollCmd, bool) {
	if k.Special == key.KeyCR {
		return window.RedrawTopFirst, true
	}
	switch k {
	case key.Rune('t'):
		return window.RedrawTop, true
	case key.Rune('z'):
		return window.RedrawMiddle, true
	case key.Rune('b'):
		return window.RedrawBottom, true
	case key.Rune('.'):
		return window.RedrawMiddleFirst, true
	case key.Rune('-'):
		return window.RedrawBottomFirst, true
	case key.Rune('+'):
		// The next screenful with its first line at the top, which is not
		// CTRL-F: there is no two-line overlap. internal/window.screen has the
		// measurements.
		return window.ScreenDown, true
	case key.Rune('^'):
		return window.ScreenUp, true
	}
	return 0, false
}

// zSide maps the second key of the z family onto a horizontal scrolling
// command.
//
// All six only do anything under 'nowrap', which is why no case in
// testdata/keys caught them being missing: under 'wrap', which both option
// profiles have unless a case turns it off, vim does not scroll sideways
// either and pvim's doing nothing was the right answer by accident.
// The vimrc sets 'nowrap', so they are live in the editor this exists to serve.
func zSide(k key.Key) (window.SideCmd, bool) {
	switch k.Special {
	case key.KeyLeft:
		return window.SideLeft, true
	case key.KeyRight:
		return window.SideRight, true
	}
	switch k {
	case key.Rune('h'):
		return window.SideLeft, true
	case key.Rune('l'):
		return window.SideRight, true
	case key.Rune('H'):
		return window.SideHalfLeft, true
	case key.Rune('L'):
		return window.SideHalfRight, true
	case key.Rune('s'):
		return window.SideStart, true
	case key.Rune('e'):
		return window.SideEnd, true
	}
	return 0, false
}

// isCountOrRegister reports whether the mode machine's pending state is a
// count, a register prefix, or both, which is the pending state a scrolling
// key may be typed after.
//
// The register is allowed because vim allows it: `"a CTRL-D` scrolls, measured
// on a four-line file where it left the cursor on line 4. An operator is not,
// and it is the only thing showcmd holds that this refuses -- `d CTRL-U` does
// nothing at all in vim, which is what handing the key to the mode machine
// does here.
func isCountOrRegister(showcmd string) bool {
	for i := 0; i < len(showcmd); i++ {
		c := showcmd[i]
		switch {
		case c >= '0' && c <= '9':
		case c == '"' && i+1 < len(showcmd):
			// The register name is whatever follows the quote, and it is not
			// checked here: the mode machine refused an invalid one before
			// this could see it.
			i++
		default:
			return false
		}
	}
	return true
}

// showcmdCount reads the count out of the mode machine's pending state, 0 for
// none. isCount has already said there is nothing else in there.
func showcmdCount(showcmd string) int {
	n := 0
	for i := 0; i < len(showcmd); i++ {
		n = n*10 + int(showcmd[i]-'0')
	}
	return n
}
