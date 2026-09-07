package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// The CTRL-W family.
//
// It is here for the same reason the scrolling keys are: internal/mode has no
// window to move between and internal/window has no key dispatch, so the
// frontend joins them. CTRL-W was 36 of the 270 differences the last fuzz run
// reported, all of them the mode machine refusing a key it has never had a
// window for.
//
// Everything below was measured against vim 9.2.0321 through cmd/oracle on the
// harness's 40-row 120-column pty, most of it by writing winnr(), winheight()
// and line('.') into line 1 of the buffer with :call setline() and reading the
// file the trailer wrote. The cases that survived are in testdata/keys.
//
// What the measurements settled, in the order they cost time:
//
//	CTRL-W s splits, the NEW window is on top and is current, and it
//	 inherits the cursor. "5 CTRL-W s" makes it five rows.
//	CTRL-W v the same sideways: new window on the left, full height.
//	CTRL-W T moves this window to a tab page of its own, after this one,
//	 and says "Already only one window" when there is one window.
//	CTRL-W z closes the preview window, and says nothing when there is
//	 none, which is what made it a working command before there
//	 could be one.
//	CTRL-W P goes to the preview window, or "E441: There is no preview
//	 window".
//	CTRL-W ^ splits and edits the alternate file, or "E23: No alternate
//	 file" when nothing has set one.
//	CTRL-W g<Tab> the tab page that was current before this one, and nothing
//	 at all -- no move, no message -- when there is one tab.
//	 Measured on three: ":tabnew:tabnew:tabfirst" then
//	 g<Tab> lands on tab 3 and not on tab 2.
//	CTRL-W H J K L
//	 move this window to an edge of the screen, full height or
//	 full width, restructuring the tree rather than making a
//	 window. A count is read and thrown away: "20 CTRL-W H"
//	 leaves the same layout a bare one does. See
//	 window.TabPage.MoveTo.
//	CTRL-W X is not a vim command at all. It is in no help file, and on
//	 one, two and three windows it leaves the layout, the sizes
//	 and the cursor exactly as CTRL-W with an unknown key does,
//	 which is a beep. CTRL-W x, the lowercase one, IS the exchange
//	 and swaps the windows with their sizes: measured with a
//	 5-row window over a 32-row one, CTRL-W x gives 32 over 5 with
//	 the cursor in the window that landed on top.
//
// The last one took three hours and none of it was vim's fault: the probe
// directory is on APFS, "case.keys" and "CASE.keys" are one file there, and
// every measurement of CTRL-W x had been overwritten by the CTRL-W X beside it.
//
// Nothing in the CTRL-W family is refused loudly any more. The last five --
// CTRL-W ], CTRL-W }, CTRL-W g], CTRL-W g} and CTRL-W g CTRL-] -- were waiting
// on a tag stack, which internal/tags is; they are four lines each in the
// tables below and the whole of what they do is in cmd/pvim/tags.go. The five
// map onto vim's ":stag", ":ptag", ":stselect", ":ptjump" and ":stjump", and
// the count means the new window's height for the first two and nothing at all
// for the three that prompt.
//
// CTRL-W f, CTRL-W F, CTRL-W gf and CTRL-W gF used to be on that list too,
// waiting on a 'path' internal/options did not carry and on a plain "gf"
// nobody had written. Both exist now and all six keys are one function; see
// gotoFile below for what was measured.
func (s *session) windowCommand(k key.Key) error {
	// The count typed between the CTRL-W and this key, taken whatever the key
	// turns out to be so that it cannot leak into the next command.
	n := s.wCount
	s.wCount = 0

	if s.pendingWG {
		// The second half of CTRL-W g, which is its own small table.
		s.pendingWG = false
		return s.windowGCommand(k, n)
	}
	if k.Special == key.KeyEsc {
		return nil
	}
	// The control form of a letter is the LOWERCASE command: vim's nv_window
	// folds CTRL-W CTRL-H onto CTRL-W h and CTRL-W CTRL-P onto CTRL-W p, and
	// the case matters because CTRL-W r and CTRL-W R are two different
	// commands and CTRL-W CTRL-R is the first of them. internal/key canon
	// leaves a control letter uppercased, so without the ToLower here
	// CTRL-W CTRL-P answered "E441: There is no preview window" where vim
	// says nothing at all, and CTRL-W CTRL-D reached no command at all.
	// Measured for every CTRL-W CTRL-x on this machine.
	if k.Mod == key.ModCtrl && k.IsRune() {
		k = key.Rune(unicode.ToLower(k.Rune))
	}
	if !k.IsRune() {
		switch k.Special {
		case key.KeyUp:
			k = key.Rune('k')
		case key.KeyDown:
			k = key.Rune('j')
		case key.KeyLeft:
			k = key.Rune('h')
		case key.KeyRight:
			k = key.Rune('l')
		case key.KeyTab:
			// CTRL-W CTRL-I, which internal/key canonicalises to Tab because
			// vim does. It is CTRL-W i, the identifier search.
			k = key.Rune('i')
		case key.KeyNL:
			// CTRL-W CTRL-J, canonicalised to NL for the same reason. It is
			// CTRL-W j.
			k = key.Rune('j')
		default:
			// Everything else -- CTRL-W then Enter, Backspace, an F key -- is
			// no command at all and vim beeps.
			return s.ed.Beep()
		}
	}
	if k.Rune >= '0' && k.Rune <= '9' {
		// A count between the CTRL-W and the command, which is still to come.
		// The digit is not the command and must not be read as one: CTRL-W 5 j
		// is one command and CTRL-W 5 alone is half of one, which the Escape
		// after it cancels.
		s.wCount = s.wCount*10 + int(k.Rune-'0')
		s.pendingW = true
		return nil
	}

	// The count in front of the CTRL-W, which vim multiplies by the one
	// between: "2 CTRL-W 3 i" is the sixth match. It is taken here and not in
	// each branch so that a command which ignores it cannot leave it behind
	// for the next key, which is what "3 CTRL-W w x" did while every one of
	// these was a no-op -- vim deleted one character and this deleted three.
	if k.Rune != 'i' && k.Rune != 'd' {
		if pre := showcmdCount(s.ed.Showcmd()); pre > 0 {
			s.clearCount()
			n = pre * max(n, 1)
		}
	}

	tab := s.tab()
	switch k.Rune {
	case 'i', 'd':
		// The identifier and the define search, each in a new window. The
		// three ways they refuse -- no identifier under the cursor, a match on
		// the line the cursor is already on, and nothing found at all -- are
		// answered before vim makes any window, so they are answered here too.
		//
		// The count picks the Nth match and is the only CTRL-W count read
		// through showcmd rather than above, because these two are the only
		// commands that multiply the two counts rather than folding them.
		// "2 CTRL-W CTRL-I" over a file whose only match is the cursor's own
		// line is "E389: Couldn't find pattern" and not "E387: Match is on
		// current line", which is a fuzz script's way of noticing that the
		// count was being thrown away.
		count := showcmdCount(s.ed.Showcmd())
		s.clearCount()
		return s.identSplit(k.Rune == 'd', count*max(n, 1))

	case ']':
		// CTRL-W ]: ":stag" on the identifier under the cursor. The count is
		// the new window's height and not the match, measured -- "2 CTRL-W ]"
		// over two matches lands on the first in a two-row window.
		return s.tagCommand(n, pickCount, tagSplit)

	case '}':
		// CTRL-W }: ":ptag". The preview window is made if there is none, the
		// count is its height and 'previewheight' is the default, and the
		// cursor stays in the window the key was typed in.
		return s.tagCommand(n, pickCount, tagPreview)

	case 's', 'S':
		return s.split("split", n)
	case 'v':
		return s.split("vsplit", n)
	case 'n':
		return s.split("new", n)

	case 'f', 'F':
		// CTRL-W f and CTRL-W F: the file under the cursor in a split. The
		// window is made only when the file was found, which is what the help
		// means by "window isn't split if the file does not exist".
		return s.gotoFile(fileSplit, k.Rune == 'F', n)

	case '^':
		return s.altSplit(n)

	case 'T':
		return s.toNewTab()

	case 'z':
		// Close the preview window. Closing one that is not there says nothing
		// at all, which is what made this a working command back when no
		// preview window could exist; now that CTRL-W } makes one it closes
		// that. Measured both ways.
		return s.closePreview()

	case 'g':
		s.pendingWG, s.pendingW, s.wCount = true, true, n
		return nil

	case 'w', 'W':
		if tab == nil {
			return nil
		}
		tab.Cycle(k.Rune == 'w', n)
		s.enter(tab.Cur)
		return nil

	case 'h', 'j', 'k', 'l':
		if tab == nil {
			return nil
		}
		dirs := map[rune]window.Dir4{'h': window.Left, 'j': window.Down, 'k': window.Up, 'l': window.Right}
		if tab.Move(dirs[k.Rune], n) {
			s.enter(tab.Cur)
		}
		return nil

	case 't', 'b':
		if tab == nil {
			return nil
		}
		if k.Rune == 't' {
			tab.First()
		} else {
			tab.Last()
		}
		s.enter(tab.Cur)
		return nil

	case 'p':
		// The window left last. With one window there is nothing to go back
		// to and vim says nothing, which is what testdata/keys'
		// ctrl_w_ctrl_p_says_nothing pins.
		if tab == nil || !tab.Back() {
			return nil
		}
		s.enter(tab.Cur)
		return nil

	case 'x':
		if tab == nil {
			return nil
		}
		if tab.Exchange(n) {
			s.enter(tab.Cur)
		}
		return nil

	case 'r', 'R':
		if tab == nil {
			return nil
		}
		if tab.Rotate(k.Rune == 'r', n) {
			s.enter(tab.Cur)
		}
		return nil

	case 'H', 'J', 'K', 'L':
		// Move this window to an edge of the screen, full width or full
		// height. The window that moves stays current, so this syncs rather
		// than entering: enter would read the cursor back out of the window
		// and the editor's is the newer of the two.
		//
		// The count is already taken and is deliberately dropped: measured,
		// "5 CTRL-W K" and "20 CTRL-W H" leave vim's layout exactly where a
		// bare one does.
		if tab == nil {
			return nil
		}
		edges := map[rune]window.Dir4{'H': window.Left, 'J': window.Down, 'K': window.Up, 'L': window.Right}
		if tab.MoveTo(edges[k.Rune]) {
			s.sync()
		}
		return nil

	case '+', '-', '<', '>':
		if tab == nil {
			return nil
		}
		delta, dir := max(n, 1), window.Horizontal
		switch k.Rune {
		case '-':
			delta = -delta
		case '<':
			delta, dir = -delta, window.Vertical
		case '>':
			dir = window.Vertical
		}
		tab.Resize(dir, delta)
		s.sync()
		return nil

	case '_', '|':
		if tab == nil {
			return nil
		}
		dir := window.Horizontal
		if k.Rune == '|' {
			dir = window.Vertical
		}
		tab.Maximise(dir, n)
		s.sync()
		return nil

	case '=':
		if tab == nil {
			return nil
		}
		tab.Equalise()
		s.sync()
		return nil

	case 'P':
		// The preview window, which CTRL-W } makes.
		w := s.previewWindow()
		if w == nil {
			s.ed.Say(errNoPreview.Error())
			return nil
		}
		s.enter(w)
		return nil

	case 'o':
		if s.windowCount() < 2 {
			s.ed.Say(window.ErrOnlyOneWindow.Error())
			return nil
		}
		if err := s.exRun("only"); err != nil {
			return err
		}
		s.sync()
		return nil

	case 'c', 'q':
		if k.Rune == 'c' && s.windowCount() < 2 {
			s.ed.Say(window.ErrLastWindow.Error())
			return nil
		}
		if s.ctx == nil {
			return nil
		}
		saved := s.cursors()
		line := "close"
		if k.Rune == 'q' {
			// ":q" and not ":close", because with one window left it is a
			// quit and internal/ex is the thing that knows which: exQuit
			// closes the window when the tab has two, closes the tab when the
			// list has two, and stops the editor otherwise.
			line = "quit"
		}
		if err := s.exRun(line); err != nil {
			return err
		}
		s.enterSaved(saved)
		return nil
	}

	if !windowCommands(k.Rune) {
		// Not a CTRL-W command at all. vim's nv_window ends in clearopbeep()
		// for these -- CTRL-W followed by \\, *, ", ., ?, X or CTRL-U does
		// nothing and says nothing -- so beeping is the behaviour and not a
		// hole being covered over. The loud refusal below is kept for the
		// keys that ARE commands and are not written yet.
		return s.ed.Beep()
	}
	return fmt.Errorf("%w: CTRL-W %s", errWindowCommand, k.String())
}

// windowGCommand is the second key of CTRL-W g.
//
// vim's table for it is gt, gT, g<Tab>, gf, gF, g], g} and g CTRL-]. gt and gT
// are the two the mode machine already answers, gf and gF open the file under
// the cursor in a tab page, and the three that want a tag stack are refused
// for the reason at the top of this file.
func (s *session) windowGCommand(k key.Key, n int) error {
	if k.Special == key.KeyEsc {
		return nil
	}
	if k.Special == key.KeyTab {
		// CTRL-W g <Tab>: the last accessed tab page, which internal/window's
		// Tabs remembers now. With one tab page there is nowhere to go back
		// to and vim says nothing at all, measured.
		if s.ctx == nil || s.ctx.Tabs == nil || !s.ctx.Tabs.Back() {
			return nil
		}
		if tab := s.tab(); tab != nil {
			s.enter(tab.Cur)
		}
		return nil
	}
	if !k.IsRune() {
		return s.ed.Beep()
	}
	if k.Mod == key.ModCtrl && k.Rune == ']' {
		// CTRL-W g CTRL-]: ":stjump". Like CTRL-W g ] except that one match
		// is jumped to rather than offered, which is the whole difference
		// between :tjump and :tselect.
		return s.tagCommand(n, pickJump, tagSplit)
	}
	if k.Mod == key.ModCtrl {
		return s.ed.Beep()
	}
	switch k.Rune {
	case 't', 'T':
		if s.ctx == nil || s.ctx.Tabs == nil {
			return nil
		}
		if k.Rune == 't' {
			s.ctx.Tabs.Next(n)
		} else {
			s.ctx.Tabs.Prev(n)
		}
		if tab := s.tab(); tab != nil {
			s.enter(tab.Cur)
		}
		return nil
	case 'f', 'F':
		// CTRL-W gf and CTRL-W gF: the file under the cursor in a tab page of
		// its own, and no tab page at all when it is not found.
		return s.gotoFile(fileTab, k.Rune == 'F', n)
	case ']':
		// CTRL-W g ]: ":stselect". The list and the prompt come first and the
		// window is made only for a match that was chosen, so a cancelled
		// prompt leaves the screen exactly as it was.
		return s.tagCommand(n, pickSelect, tagSplit)
	case '}':
		// CTRL-W g }: ":ptjump". The preview window, and the prompt only when
		// there is more than one match.
		return s.tagCommand(n, pickJump, tagPreview)
	}
	return s.ed.Beep()
}

// split is CTRL-W s, S, v and n: one ex command with the count in front of it.
//
// It goes through internal/ex rather than calling TabPage.Split directly
// because everything around the tree insert belongs to the ex layer -- which
// buffer the new window shows, the buffer list, 'splitbelow' and 'splitright'
// -- and ":split" is already all of that. The count is the new window's height
// or width: measured, "5 CTRL-W s" on a 40-row screen leaves the new window
// five rows where a bare CTRL-W s leaves it nineteen.
//
// The top line is carried over by hand. internal/ex's swapBuffer resets it to
// 1 on the window it lands in, which is right for a ":e" and wrong for a
// split: vim shows the same lines in both halves, and without this a split
// halfway down a long file scrolled the new window back to the top and H and L
// answered against lines that were not on the screen.
func (s *session) split(cmd string, n int) error {
	if s.ctx == nil {
		return fmt.Errorf("%w: CTRL-W %s", errWindowCommand, cmd)
	}
	top := 0
	if w := s.win(); w != nil {
		top = w.View.TopLine
	}
	line := cmd
	if n > 0 {
		line = strconv.Itoa(n) + cmd
	}
	if err := s.exRun(line); err != nil {
		return err
	}
	if w := s.win(); w != nil && top > 0 && cmd != "new" {
		w.View.TopLine = top
	}
	s.sync()
	return nil
}

// altSplit is CTRL-W ^: split, and show the alternate file in the new window.
//
// vim's own wording for the failure, measured on a file opened alone:
// "E23: No alternate file", and no window is made. With a count it is buffer
// N rather than the alternate, which is why the ":buffer" argument is built
// rather than always being "#".
func (s *session) altSplit(n int) error {
	if s.ctx == nil || s.ctx.Bufs == nil {
		return fmt.Errorf("%w: CTRL-W ^", errWindowCommand)
	}
	arg := "#"
	if n > 0 {
		arg = strconv.Itoa(n)
	}
	// Asked before the split, because vim complains without opening a window
	// and a split that had to be undone afterwards would leave the layout
	// jittering on every mistyped CTRL-W ^.
	if _, err := s.ctx.Bufs.Match(arg); err != nil {
		if msg := errorMessage(err, arg); msg != "" {
			s.ed.Say(msg)
		}
		return nil
	}
	if err := s.split("split", 0); err != nil {
		return err
	}
	if err := s.exRun("buffer " + arg); err != nil {
		return err
	}
	s.sync()
	return nil
}

// toNewTab is CTRL-W T: this window, alone, in a tab page of its own.
func (s *session) toNewTab() error {
	if s.ctx == nil || s.ctx.Tabs == nil {
		return fmt.Errorf("%w: CTRL-W T", errWindowCommand)
	}
	if err := s.ctx.Tabs.ToNewTab(); err != nil {
		if msg := errorMessage(err, "CTRL-W T"); msg != "" {
			s.ed.Say(msg)
		}
		return nil
	}
	if tab := s.tab(); tab != nil {
		s.enter(tab.Cur)
	}
	return nil
}

// tab is the current tab page, or nil in a session nobody gave one to, which
// is what the vimrc loader and most of the unit tests build.
func (s *session) tab() *window.TabPage {
	if s.ctx == nil || s.ctx.Tabs == nil {
		return nil
	}
	return s.ctx.Tabs.Current()
}

// windowCount is how many windows the current tab holds, 1 when there is no
// tab page at all.
func (s *session) windowCount() int {
	tab := s.tab()
	if tab == nil {
		return 1
	}
	return len(tab.Windows())
}

// enter makes w the window every key measures against.
//
// The cursor is the whole of it. internal/mode owns one cursor and a tab page
// owns one per window, so moving between windows means writing the editor's
// cursor into the window being left and reading it back out of the window
// being entered. Without that, ":sp" then a motion then ":q" left vim on the
// line the surviving window had been on and this on the line the closed one
// ended on; measured on eight lines, vim 3 and this 4.
func (s *session) enter(w *window.Window) {
	tab := s.tab()
	if tab == nil || w == nil {
		return
	}
	if cur := tab.Cur; cur != nil && cur != w && cur.Buf == s.ed.Buffer() {
		cur.View.Cursor = s.ed.Cursor()
	}
	tab.Goto(w)
	if s.ed.Buffer() != w.Buf {
		s.followBuffer(w)
	}
	s.ed.SetCursor(w.View.Cursor)
	s.sync()
}

// followBuffer points the editor and the buffer list at what w is showing.
//
// It is the same three steps internal/ex's swapBuffer takes -- remember where
// the buffer being left was, make the new one current and alternate, hand it
// to the frontend's Open hook -- minus the two that are wrong for a window
// switch: swapBuffer also assigns the window's buffer and resets its top line
// to 1, which is right for ":e" in place and would throw away the scroll
// position of a window that has been sitting there all along.
func (s *session) followBuffer(w *window.Window) {
	l := s.ctx.Bufs
	if l == nil || s.ctx.Open == nil {
		return
	}
	var nb *ex.Buf
	for _, b := range l.Bufs {
		if b.Text == w.Buf {
			nb = b
			break
		}
	}
	if nb == nil || nb == l.Cur {
		return
	}
	if cur := l.Cur; cur != nil {
		cur.Cursor = s.ed.Cursor()
		l.Alt = cur
	}
	l.Cur = nb
	s.ctx.Open(nb)
	// "% and "# follow the buffer list, which is what makes "%" and "#"
	// expand in a ":w" argument after a window switch.
	alt := ""
	if l.Alt != nil {
		alt = l.Alt.Name
	}
	s.ed.Registers().SetFilename(nb.Name, alt)
}

// cursors is where every window of the current tab is looking, taken before an
// ex command that is going to move between them.
//
// internal/ex's Sync writes the editor's cursor into whichever window it
// leaves current, so ":close" hands the window that survives the cursor of the
// window that died. That is right for ":e", which is what Sync was written
// for, and wrong for every command that changes which window is current;
// snapshotting is the frontend undoing it without reaching into internal/ex.
// Measured on eight lines: "3G CTRL-W s 5G x CTRL-W c" leaves vim on line 3
// and left this on 5.
func (s *session) cursors() map[*window.Window]text.Pos {
	tab := s.tab()
	if tab == nil {
		return nil
	}
	out := make(map[*window.Window]text.Pos, 4)
	for _, w := range tab.Windows() {
		out[w] = w.View.Cursor
	}
	return out
}

// enterSaved makes the tab's current window current here too, with the cursor
// it had before the ex command ran.
func (s *session) enterSaved(saved map[*window.Window]text.Pos) {
	tab := s.tab()
	if tab == nil || tab.Cur == nil {
		return
	}
	if p, ok := saved[tab.Cur]; ok {
		tab.Cur.View.Cursor = p
	}
	s.enter(tab.Cur)
}

// exRun runs one ex command on behalf of a CTRL-W key.
//
// It is not runLine: runLine writes the newline vim leaves behind after an
// accepted ":" command line, and CTRL-W s never opened one. Measured, a
// CTRL-W s that went through runLine cost the message log a line against
// vim in every case that splits.
func (s *session) exRun(line string) error {
	err := s.ctx.Run(line)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ex.ErrQuit):
		// The editor is going away and the loop above has to see it. Only a
		// session with no Quit hook gets here; with one, ":q" reports the
		// decision through the hook and returns nil.
		return err
	}
	if msg := errorMessage(err, line); msg != "" {
		s.ed.Say(msg)
	}
	return nil
}

// windowCommands reports whether c is a second key vim's CTRL-W recognises at
// all, from :help CTRL-W. Everything in here that is not in the switch above
// is a command this editor has still to write; everything outside it is a key
// vim beeps at.
//
// X is deliberately not in it. vim's help has no CTRL-W X and vim's own
// behaviour on one, two and three windows is the beep it gives any other
// unknown key; it was in this list, and refused loudly, on the strength of
// looking like a cousin of CTRL-W x.
func windowCommands(c rune) bool {
	const known = "sSvn^qcowWjkhltbpPrRxd+-<>=_|]}gfFizTHJKL"
	for _, k := range known {
		if k == c {
			return true
		}
	}
	return false
}

// The file under the cursor: gf, gF, CTRL-W f, CTRL-W F, CTRL-W gf and
// CTRL-W gF.
//
// Six keys and one command. They differ in three bits -- which window the file
// lands in, whether a number after the name is a line to jump to, and nothing
// else -- so they are one function with two arguments rather than six that
// drift apart. The plain g pair lives here beside the CTRL-W four for the same
// reason: writing the CTRL-W half without gf leaves the pair inconsistent, and
// the whole of what they share is the name and the search.
//
// Measured against vim 9.2.0321 through a 40-row 120-column pty, on a buffer
// holding a name and a directory holding the file or not:
//
//	gf on a name that is not there E447: Can't find file "x" in path
//	gf on an empty line E446: No file name under cursor
//	2gf on a name found once E347: No more file "x" found in path
//	gf on a modified buffer E37: No write since last change
//	gf on a name that is there the same two lines ":e" prints
//	CTRL-W f on a name that is not E447 and NO window: vim splits only on
//	 a file it has already found
//	CTRL-W gf a new tab page, and none when not found
//	CTRL-W F and CTRL-W gF the same, on the line after the name
//
// What the search is: 'path', taken apart by options.SplitPath, walked in
// order, each entry stat-ed for the name. Duplicate directories are one entry
// -- the default 'path' names the working directory twice on a session whose
// file is in it, and "2gf" there is E347 in vim and not a second hit on the
// same file. A name that is absolute or begins with "./" or "../" does not use
// 'path' at all, which is vim.
//
// What is not here, named rather than left to be discovered:
//
// - 'suffixesadd' and 'includeexpr', vim's two second chances at a name that
// did not resolve. Both are empty under --clean and under this vimrc, so
// both are inert, and neither is in internal/options.
// - A "type://machine/path" hypertext link, of which vim uses only the
// "/path". No case has one and the rule is one line the day one does.
// - The 'path' items options.SplitPath refuses: "**" and anything holding a
// wildcard or a "$". Those stop the key loudly with the item quoted rather
// than searching a shorter list and reporting the file missing.

// identSplit is CTRL-W i and CTRL-W d: the identifier under the cursor, or the
// macro definition of it, in a new window.
//
// internal/mode answers the three ways it refuses before any window is made --
// no identifier, a match on the line the cursor is already on, nothing found
// at all -- and reports whether there is a match to split for, without moving.
// The window is made here and the jump is the same command's goto form
// replayed into it: "[<Tab>" for the identifier and "[<C-D>" for the define,
// with the count in front. Replaying rather than reaching for the position
// means there is one search in the tree and not two, and the new window has
// the same buffer and the same cursor as the old one, so the match it lands on
// is the match IdentSplit found.
func (s *session) identSplit(define bool, count int) error {
	found, err := s.ed.IdentSplit(define, count)
	if err != nil || !found {
		return err
	}
	if err := s.split("split", 0); err != nil {
		return err
	}
	notation := "[<Tab>"
	if define {
		notation = "[<C-D>"
	}
	if count > 1 {
		notation = strconv.Itoa(count) + notation
	}
	ks, err := key.Parse(notation, `\`)
	if err != nil {
		return err
	}
	if err := s.ed.Keys(ks); err != nil {
		return err
	}
	s.sync()
	return nil
}

// fileTarget is which window the file under the cursor opens in.
type fileTarget int

const (
	// fileHere is gf and gF: this window, through ":e".
	fileHere fileTarget = iota
	// fileSplit is CTRL-W f and CTRL-W F.
	fileSplit
	// fileTab is CTRL-W gf and CTRL-W gF.
	fileTab
)

// gotoFile opens the file whose name is under or after the cursor.
//
// withLine is the capital-F half of each pair: a number after the name is the
// line to put the cursor on. count is vim's [count], which picks the count'th
// directory of 'path' that holds the name and is not a repeat.
func (s *session) gotoFile(target fileTarget, withLine bool, count int) error {
	if s.ctx == nil {
		return fmt.Errorf("%w: gf", errWindowCommand)
	}
	line := ""
	if b := s.ed.Buffer(); b != nil {
		cur := s.ed.Cursor()
		if cur.Line >= 1 && cur.Line <= b.LineCount() {
			line = string(b.Line(cur.Line))
		}
	}
	name, lnum := fileNameAtCursor(line, s.ed.Cursor().Col)
	if name == "" {
		s.ed.Say("E446: No file name under cursor")
		return nil
	}

	found, seen, err := s.findInPath(name, count)
	if err != nil {
		return err
	}
	if found == "" {
		if seen > 0 {
			// The name resolves, just not that many times. Vim tells the two
			// apart and so does this: E347 is "you asked for the third and
			// there are two", E447 is "there are none".
			s.ed.Say(`E347: No more file "` + name + `" found in path`)
		} else {
			s.ed.Say(`E447: Can't find file "` + name + `" in path`)
		}
		return nil
	}

	cmd := "edit"
	switch target {
	case fileSplit:
		cmd = "split"
	case fileTab:
		cmd = "tabedit"
	}
	if cmd == "edit" && s.cannotAbandon() {
		// gf and gF leave the current buffer behind and vim refuses to when it
		// has unwritten changes. The message is its own and shorter than the
		// one ":e" prints: measured on a modified buffer, gf says
		// "E37: No write since last change" and ":e other.txt" says the same
		// with " (add ! to override)" behind it. That is why the check is
		// here and not left to the ex layer, which would print the longer one.
		//
		// Only with 'confirm' off. With it on -- and a user's vimrc sets
		// it -- vim asks rather than refusing, and the asking is internal/ex's
		// prompt, so the command runs and answers for itself.
		s.ed.Say(ex.ErrNoWriteSince.Error())
		return nil
	}
	if err := s.exRun(cmd + " " + escapeFileArg(found)); err != nil {
		return err
	}
	if withLine && lnum > 0 {
		if b := s.ed.Buffer(); b != nil {
			s.ed.SetCursor(text.Pos{Line: min(lnum, b.LineCount())})
		}
	}
	s.sync()
	return nil
}

// findInPath walks 'path' for name and returns the count'th directory that
// holds it, together with how many were found at all.
//
// The count of what was seen is what tells E347 from E447, so the walk does
// not stop at the first hit when a count asked for a later one.
func (s *session) findInPath(name string, count int) (found string, seen int, err error) {
	want := max(1, count)

	// An absolute name, or one written relative to a directory on purpose,
	// does not go through 'path': vim stats it where it says and stops.
	if rooted(name) {
		// The expanded name and not the one that was typed: internal/ex's
		// ":e" does not expand a "~" and would try to read a directory
		// literally called "~". ShortName puts the "~" back when the message
		// line prints it.
		p := expandHome(name)
		if isFile(p) {
			return p, 1, nil
		}
		return "", 0, nil
	}

	entries, refused := options.SplitPath(s.ctx.Opt.PathValue())
	if len(refused) > 0 {
		// Loud, for the same reason every other unwritten key in this file is
		// loud: a search that quietly walked the entries it understood and
		// skipped the rest would report the file missing, and the missing
		// piece would be the option and not the file.
		return "", 0, fmt.Errorf("%w: %q", errPathItem, refused[0])
	}

	here := ""
	if b := s.ctx.Bufs; b != nil && b.Cur != nil && b.Cur.Name != "" {
		here = filepath.Dir(b.Cur.Name)
	}

	tried := map[string]bool{}
	for _, e := range entries {
		dir := e.Dir
		switch {
		case e.Here:
			if here == "" {
				continue
			}
			dir = here
		default:
			dir = expandHome(dir)
		}
		p := filepath.Join(dir, name)
		// One entry per directory that actually resolves to the same file.
		// The default 'path' holds "." and the working directory, which are
		// one directory in most sessions, and vim counts them once.
		key := p
		if abs, e := filepath.Abs(p); e == nil {
			key = filepath.Clean(abs)
		}
		if tried[key] {
			continue
		}
		tried[key] = true
		if !isFile(p) {
			continue
		}
		seen++
		if seen == want {
			found = p
		}
	}
	if seen < want {
		found = ""
	}
	return found, seen, nil
}

// cannotAbandon reports whether leaving the current buffer for another one
// would lose unwritten work, which is vim's can_abandon with the two options
// that decide it: 'hidden' keeps the buffer loaded and 'confirm' turns the
// refusal into a question.
func (s *session) cannotAbandon() bool {
	if s.ctx == nil || s.ctx.Opt == nil {
		return false
	}
	if s.ctx.Opt.G.Hidden || s.ctx.Opt.G.Confirm {
		return false
	}
	b := s.ctx.Bufs
	return b != nil && b.Cur != nil && b.Cur.Modified()
}

// rooted reports whether a file name says where it is on its own, which is
// vim's rule for skipping 'path' entirely: an absolute name, a "~" name, and
// the two that are relative on purpose.
func rooted(name string) bool {
	return strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, "~") ||
		strings.HasPrefix(name, "./") ||
		strings.HasPrefix(name, "../")
}

// expandHome turns a leading "~" into the home directory, which is the one
// piece of 'path' expansion internal/options cannot do: that package may not
// ask the operating system anything.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
}

// isFile reports whether p is there and is not a directory.
func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// escapeFileArg protects the three characters internal/ex's expandName reads
// as something other than themselves in a file-name argument. 'isfname' lets
// all three into a name and a backslash is what ":e" takes to mean the
// character itself.
func escapeFileArg(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' || name[i] == '%' || name[i] == '#' {
			b.WriteByte('\\')
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

// fileNameAtCursor is vim's file_name_at_cursor: the run of 'isfname'
// characters under the cursor, or the next one on the line when the cursor is
// not on one, plus the number after it that gF reads as a line.
//
// col is a byte offset into line, 0-based, which is what mode.Editor.Cursor
// carries.
//
// The rules that are not obvious and were measured rather than assumed:
//
// - The run UNDER the cursor wins over any name later on the line. On
// "see other.txt now" with the cursor at the start, vim says
// `E447: Can't find file "see" in path` and does not skip ahead to the
// name that would have worked.
// - Trailing ".,:;!" comes off. Only "." and "," can be in the run at all,
// the other three not being 'isfname' characters, so this is the sentence
// at the end of a line of prose and nothing else.
// - The number gF reads is separated from the name by characters that are
// neither 'isfname' nor digits, which is what makes "eval.c:10",
// "eval.c @ 20", "eval.c (30)" and "eval.c 40" all work. " line " is
// vim's one special case on top of that, because "line" is four 'isfname'
// characters and would otherwise stop the skip.
func fileNameAtCursor(line string, col int) (name string, lnum int) {
	if line == "" {
		return "", 0
	}
	if col < 0 {
		col = 0
	}
	if col >= len(line) {
		col = len(line) - 1
	}

	// Forward to the first filename character at or after the cursor.
	start := -1
	for i := col; i < len(line); i++ {
		if isFnameByte(line, i) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", 0
	}
	// Back to the beginning of the run the cursor landed in.
	for start > 0 && isFnameByte(line, start-1) {
		start--
	}
	end := start
	for end < len(line) && isFnameByte(line, end) {
		end++
	}
	name = strings.TrimRight(line[start:end], ".,:;!")
	if name == "" {
		return "", 0
	}
	return name, lineNumberAfter(line[end:])
}

// lineNumberAfter reads the number gF jumps to out of what follows the name.
func lineNumberAfter(rest string) int {
	for {
		trimmed := strings.TrimLeft(rest, " \t")
		// " line " between the name and the number, which vim recognises
		// because ":verbose command" prints it that way.
		if after, cut := strings.CutPrefix(trimmed, "line"); cut && strings.HasPrefix(after, " ") {
			rest = after
			continue
		}
		rest = trimmed
		break
	}
	i := 0
	for i < len(rest) && !isFnameByte(rest, i) && (rest[i] < '0' || rest[i] > '9') {
		i++
	}
	n := 0
	digits := 0
	for ; i < len(rest) && rest[i] >= '0' && rest[i] <= '9'; i++ {
		n = n*10 + int(rest[i]-'0')
		digits++
	}
	if digits == 0 {
		return 0
	}
	return n
}

// isFnameByte reports whether the byte at i is one 'isfname' takes.
//
// The default is "@,48-57,/,.,-,_,+,#,$,%,~,=", where "@" is isalpha() and
// every code point from 0xa0 up is a filename character whatever the option
// says. internal/regex/classes.go carries the same set as a character-class
// body and the same measurement behind it; this is the byte-at-a-time form,
// and a byte at 0x80 or above is part of a multi-byte rune and therefore
// inside that 0xa0-and-up range.
//
// Frozen at the default. 'isfname' is in internal/options and nothing parses
// its syntax, so a vimrc that changed it would be read here as the default;
// neither a user's vimrc nor "vim --clean" touches it.
func isFnameByte(s string, i int) bool {
	c := s[i]
	switch {
	case c >= 0x80:
		return true
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("/.-_+,#$%~=", c) >= 0
}

// errPathItem is what a 'path' this editor cannot walk stops the key with.
var errPathItem = errors.New("'path' item not implemented")

// errWindowCommand is what a CTRL-W command this editor has not written stops
// with.
//
// It stops the run rather than beeping, which is the same choice the mode
// machine makes for every key it has not implemented and for the same reason:
// a CTRL-W v that silently did nothing would pass an oracle case by looking
// like a vim that also did nothing, and the gap would be invisible until the
// day somebody typed it. Loud is cheaper.
var errWindowCommand = errors.New("key not implemented")
