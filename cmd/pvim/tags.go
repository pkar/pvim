package main

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/search"
	"github.com/pkar/pvim/internal/tags"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// The tag keys: CTRL-], CTRL-T, and the five CTRL-W keys that split a window
// over a tag.
//
// internal/tags reads the tags file and owns the stack; this file is the join,
// because a tag jump needs three things that live in three places -- the
// identifier under the cursor and the mode machine's cursor, the buffer list
// and ":edit" from internal/ex, and a window to split from internal/window --
// and the frontend is the only layer that has all three.
//
// The order of operations is vim's do_tag and it is the part worth stating,
// because every one of these steps can stop the command and the window is made
// in the middle:
//
// 1. the identifier under the cursor, or "E349: No identifier under cursor";
// 2. the tags files, or "E433: No tags file" AND the E426 behind it, both,
// which is what vim prints for a tag typed in a tree nobody has run ctags
// over;
// 3. the match: the count'th for CTRL-], the one chosen at the prompt for the
// :tselect and :tjump forms;
// 4. the file, checked before anything is split, so that a tags file pointing
// at a file that has been deleted leaves the screen exactly as it was:
// E429 and no window;
// 5. the window, split or preview;
// 6. the jump, which is a search from the top of the file for the pattern the
// tags file carries, or "E434: Can't find tag pattern" with the cursor put
// back on the line it started on.
//
// The tag stack is per window, as vim's is, and a split hands the new window a
// copy: CTRL-] in a split and then CTRL-T in the window it split from is vim's
// E555 and not a jump, because the entry the split pushed was pushed onto the
// copy. The preview window is the exception and does not touch any stack at
// all: vim keeps one ptag_entry for it beside the stacks, so CTRL-W } never
// gives CTRL-T anywhere to go, measured and reproduced here by not pushing.
//
// What is not here, named rather than left to be found: vim's two second
// chances at a stale tags file, an ignore-case retry of the pattern and the
// "^tagname" guess behind "E435: Couldn't find tag, just guessing!". A tags
// file older than the source it points at lands on E434 here where vim may
// land on a nearby line and warn. Re-run ctags.

// tagDest is which window a tag command lands in.
type tagDest int

const (
	// tagHere is CTRL-] and ":tag": this window.
	tagHere tagDest = iota
	// tagSplit is CTRL-W ], CTRL-W g] and CTRL-W g CTRL-]: a new window above
	// this one, which the count sizes.
	tagSplit
	// tagPreview is CTRL-W } and CTRL-W g}: the preview window, made if there
	// is none and left holding the tag with the cursor back where it started.
	tagPreview
)

// tagPick is how the match is chosen when the name has more than one.
type tagPick int

const (
	// pickCount is ":tag" and CTRL-]: the count'th match, clamped to the last.
	pickCount tagPick = iota
	// pickSelect is ":tselect": the list and the prompt, always, even for one
	// match.
	pickSelect
	// pickJump is ":tjump": the list and the prompt only when there is more
	// than one match.
	pickJump
)

// tagStack is the current window's tag stack, made on first use.
//
// Keyed by window rather than kept on the session because vim's is per window,
// and the map is the session's rather than a field on window.Window so that
// internal/window does not have to import internal/tags to hold a pointer it
// never reads. Nothing removes the entry of a window that is closed: it is one
// pointer and a slice header per window ever opened, and the alternative is a
// hook in every path that closes one.
func (s *session) tagStack() *tags.Stack {
	w := s.win()
	if s.tagStacks == nil {
		s.tagStacks = map[*window.Window]*tags.Stack{}
	}
	st := s.tagStacks[w]
	if st == nil {
		st = &tags.Stack{}
		s.tagStacks[w] = st
	}
	return st
}

// tagOptions is what internal/tags needs to know about this session: which
// files 'tags' names and which file the cursor is in.
//
// The file list is vim's default and not the option, because internal/options
// has no 'tags' in its table: ":set tags=..." is E518 here, so the default is
// the only value this editor can ever have. One line changes when the option
// lands, and it is this one.
func (s *session) tagOptions() tags.Options {
	o := tags.Options{Files: tags.SplitOption(tags.DefaultOption)}
	if name := s.bufName(); name != "" {
		o.CurFile = name
		o.CurDir = filepath.Dir(name)
	}
	if s.ctx != nil && s.ctx.Opt != nil {
		// 'tagcase' at its default of "followic" means the tag lookup follows
		// 'ignorecase', which this vimrc sets. The option itself is not in
		// internal/options, so the default is assumed rather than read.
		o.IgnoreCase = s.ctx.Opt.G.IgnoreCase
	}
	return o
}

// bufName is the file the current buffer is showing, empty for [No Name].
func (s *session) bufName() string {
	if s.ctx == nil || s.ctx.Bufs == nil || s.ctx.Bufs.Cur == nil {
		return ""
	}
	return s.ctx.Bufs.Cur.Name
}

// tagCommand is the whole of CTRL-] and the four CTRL-W keys that look a tag
// up by the identifier under the cursor.
//
// count is vim's count: the match for CTRL-], the height of the new window for
// CTRL-W ] and CTRL-W }, and nothing at all for the two that prompt.
func (s *session) tagCommand(count int, pick tagPick, dest tagDest) error {
	word, ok := s.identUnderCursor()
	if !ok {
		s.ed.Say(tags.MsgNoIdent)
		return nil
	}

	matches, err := tags.Find(word, s.tagOptions())
	if err != nil {
		// Both messages, in vim's order. E433 alone would say the search never
		// happened and E426 alone would say it happened and found nothing;
		// vim prints both because both are true.
		s.ed.Say(err.Error())
	}
	if len(matches) == 0 {
		s.ed.Say(tags.NotFound(word))
		return nil
	}

	match := 0
	switch pick {
	case pickCount:
		// The count is the match only for the key that lands in this window.
		// CTRL-W ] and CTRL-W } read it as the height of the window they make
		// and always take the first match: measured, "2 CTRL-W ]" over two
		// matches splits a two-row window over the FIRST one.
		if dest == tagHere {
			match = min(max(count, 1), len(matches)) - 1
		}
	case pickSelect:
		if match, ok = s.tagPrompt(matches, -1); !ok {
			return nil
		}
	case pickJump:
		if len(matches) > 1 {
			if match, ok = s.tagPrompt(matches, -1); !ok {
				return nil
			}
		}
	}

	size := 0
	if dest != tagHere {
		size = count
	}
	return s.tagTo(word, matches, match, dest, size)
}

// tagTo makes the window the destination asks for and jumps to the match.
func (s *session) tagTo(name string, matches []tags.Tag, match int, dest tagDest, size int) error {
	t := matches[match]

	// The file, before any window. Vim's jumpto_tag checks it first and says
	// so without splitting, which is the same rule CTRL-W f follows for the
	// file under the cursor.
	if !s.sameAsBuffer(t.Path) && !isFile(t.Path) {
		s.ed.Say(tags.NoFile(t.File))
		return nil
	}

	// The stack entry is pushed before the window is made, onto the stack of
	// the window the command was typed in, because that is the stack a split
	// copies into the new window.
	pushed := false
	if dest != tagPreview {
		s.tagStack().Push(tags.Entry{
			Name:     name,
			From:     tags.Pos{Line: s.ed.Cursor().Line, Col: s.ed.Cursor().Col + 1},
			FromFile: s.bufName(),
			FromText: lineText(s.ed.Buffer(), s.ed.Cursor().Line),
			Matches:  matches,
			Cur:      match,
		})
		pushed = true
		// A tag jump is a jump: vim's do_tag calls setpcmark, so `` and CTRL-O
		// come back here.
		s.ed.MarkJump()
	}

	from := s.win()
	switch dest {
	case tagSplit:
		stack := s.tagStack().Clone()
		if err := s.split("split", size); err != nil {
			return err
		}
		s.tagStacks[s.win()] = stack
	case tagPreview:
		if err := s.openPreview(size); err != nil {
			return err
		}
	}

	if err := s.tagLand(t); err != nil {
		return err
	}
	if pushed {
		s.tagStack().Advance(match)
	}
	if dest == tagPreview && from != nil && from != s.win() {
		// The preview window is filled and left; the cursor goes back to the
		// window the key was typed in. Vim's do_tag ends with a win_enter of
		// the window it saved, which is what makes CTRL-W } a command that
		// shows something rather than one that goes somewhere.
		s.enter(from)
	}
	s.sync()
	return nil
}

// tagLand opens the file the tag names and puts the cursor on it.
func (s *session) tagLand(t tags.Tag) error {
	if !s.sameAsBuffer(t.Path) {
		if err := s.exRun("edit " + escapeFileArg(t.Path)); err != nil {
			return err
		}
		if !s.sameAsBuffer(t.Path) {
			// ":edit" refused -- an unwritten buffer with 'nohidden', which is
			// E37 and which it has already said. Vim does not jump either.
			return nil
		}
	}
	buf := s.ed.Buffer()
	if buf == nil {
		return nil
	}
	if n, ok := t.LineNumber(); ok {
		s.ed.SetCursor(text.Pos{Line: min(n, buf.LineCount())})
		return nil
	}
	pat, ok := t.Pattern()
	if !ok || pat == "" {
		return nil
	}

	// The search is vim's: the first match in the file, with 'ignorecase' and
	// 'smartcase' forced off whatever the options say -- jumpto_tag saves and
	// clears both, because a tags file names one exact line and finding a
	// different one that differs in case would be worse than not finding it.
	opt := search.DefaultOptions()
	re, err := search.Compile(pat, opt)
	if err != nil {
		s.ed.Say("E434: Can't find tag pattern")
		return nil
	}
	start := s.ed.Cursor()
	// Line by line from the top, and not search.Find from line 1, because
	// Find is "strictly after here" and the match a tag wants is very often
	// on line 1 itself. Vim gets the same answer by starting the search
	// before the first line -- jumpto_tag sets the cursor line to 0 for
	// exactly this -- and a tags file whose first entry pointed at line 1
	// landed on the second match without it: "/^int foo/" over a file whose
	// first two lines are "int foo" and "int foo2" went to line 2.
	for n := 1; n <= buf.LineCount(); n++ {
		if m := search.FindLine(buf, re, n); len(m) > 0 {
			s.ed.SetCursor(m[0].Start)
			return nil
		}
	}
	// The line the cursor was on, with the column at the start: vim restores
	// the line number and leaves the column where the failed search left it,
	// which is column 1. Measured on four lines with the cursor in column 6.
	s.ed.SetCursor(text.Pos{Line: start.Line})
	s.ed.Say("E434: Can't find tag pattern")
	return nil
}

// tagPrompt is vim's ":tselect" list and the number prompt under it, and
// returns the match chosen.
//
// Not a choice is not an error: an empty line, a "q", an Escape, a number that
// is not a match and the input running out all cancel the command silently,
// which is measured -- ":tselect foo" answered "9" over two matches says
// nothing at all and jumps nowhere.
func (s *session) tagPrompt(matches []tags.Tag, cur int) (int, bool) {
	for _, line := range tags.ListMatches(matches, cur) {
		s.ed.Say(line)
	}
	s.ed.SayPrompt(tags.Prompt)

	n, typed := 0, false
	for {
		k, ok := s.next()
		if !ok || k.Special == key.KeyEsc {
			return 0, false
		}
		switch {
		case k.Special == key.KeyCR || k.Special == key.KeyNL:
			if !typed || n < 1 || n > len(matches) {
				return 0, false
			}
			return n - 1, true
		case k.IsRune() && k.Rune >= '0' && k.Rune <= '9':
			// Echoed as it is typed, which is what puts the answer on the end
			// of the prompt line in the message log.
			n = n*10 + int(k.Rune-'0')
			typed = true
			s.ed.SayAnswer(byte(k.Rune))
		case k == key.Rune('q'), k == key.Ctrl('c'):
			return 0, false
		default:
			// Vim's get_number ignores everything else and keeps reading,
			// which is why a stray key in a script is swallowed by the prompt
			// rather than doing anything.
		}
	}
}

// tagPop is CTRL-T and ":pop": back to where a jump started.
func (s *session) tagPop(count int) error {
	e, msg, ok := s.tagStack().Pop(count)
	if !ok {
		s.ed.Say(msg)
		return nil
	}
	return s.tagBack(e)
}

// tagForward is ":tag" with no name: forward again after a CTRL-T.
func (s *session) tagForward(count int) error {
	e, msg, ok := s.tagStack().Forward(count)
	if !ok {
		s.ed.Say(msg)
		return nil
	}
	if len(e.Matches) == 0 {
		s.ed.Say(tags.NotFound(e.Name))
		return nil
	}
	match := min(max(e.Cur, 0), len(e.Matches)-1)
	t := e.Matches[match]
	if !s.sameAsBuffer(t.Path) && !isFile(t.Path) {
		s.ed.Say(tags.NoFile(t.File))
		return nil
	}
	if err := s.tagLand(t); err != nil {
		return err
	}
	s.sync()
	return nil
}

// tagBack puts the cursor back where a stack entry was pushed from.
func (s *session) tagBack(e tags.Entry) error {
	if e.FromFile != "" && !s.sameAsBuffer(e.FromFile) {
		if err := s.exRun("edit " + escapeFileArg(e.FromFile)); err != nil {
			return err
		}
	}
	col := e.From.Col - 1
	if col < 0 {
		col = 0
	}
	s.ed.SetCursor(text.Pos{Line: e.From.Line, Col: col})
	s.sync()
	return nil
}

// identUnderCursor is the word CTRL-] looks up.
func (s *session) identUnderCursor() (string, bool) {
	b := s.ed.Buffer()
	if b == nil {
		return "", false
	}
	cur := s.ed.Cursor()
	if cur.Line < 1 || cur.Line > b.LineCount() {
		return "", false
	}
	word, _, ok := tags.Ident(b.Line(cur.Line), cur.Col, s.ed.Options().IsKeyword)
	return word, ok
}

// sameAsBuffer reports whether a tags file's file name is the buffer already
// on the screen, which is the case a tag jump must not re-open: ":edit" on the
// current file rereads it and throws away an unwritten change.
func (s *session) sameAsBuffer(path string) bool {
	name := s.bufName()
	if name == "" || path == "" {
		return false
	}
	if name == path {
		return true
	}
	a, erra := filepath.Abs(name)
	b, errb := filepath.Abs(path)
	return erra == nil && errb == nil && filepath.Clean(a) == filepath.Clean(b)
}

// lineText is the text of one buffer line with its leading white space taken
// off, which is what ":tags" prints for an entry in the file being edited.
func lineText(b *text.Buffer, line int) string {
	if b == nil || line < 1 || line > b.LineCount() {
		return ""
	}
	return strings.TrimLeft(string(b.Line(line)), " \t")
}

// previewWindow is the tab's preview window, nil when it has none.
func (s *session) previewWindow() *window.Window {
	tab := s.tab()
	if tab == nil {
		return nil
	}
	for _, w := range tab.Windows() {
		if w.Preview {
			return w
		}
	}
	return nil
}

// openPreview makes the preview window current, splitting one off the current
// window when there is none.
//
// height is the count typed in front of CTRL-W }, and zero means
// 'previewheight'. An existing preview window is reused at whatever size it
// already has, which is vim: a second CTRL-W } does not resize it.
func (s *session) openPreview(height int) error {
	if w := s.previewWindow(); w != nil {
		s.enter(w)
		return nil
	}
	if height <= 0 {
		height = previewHeight
	}
	if err := s.split("split", height); err != nil {
		return err
	}
	if w := s.win(); w != nil {
		w.Preview = true
	}
	return nil
}

// closePreview is CTRL-W z and ":pclose": close the preview window, silently
// when there is none.
func (s *session) closePreview() error {
	w := s.previewWindow()
	if w == nil {
		return nil
	}
	tab := s.tab()
	if tab == nil {
		return nil
	}
	saved := s.cursors()
	back := tab.Cur
	if back == w {
		back = nil
	}
	if err := tab.Close(w); err != nil {
		if msg := errorMessage(err, "CTRL-W z"); msg != "" {
			s.ed.Say(msg)
		}
		return nil
	}
	if back != nil {
		tab.Goto(back)
	}
	s.enterSaved(saved)
	return nil
}

// previewHeight is 'previewheight': how tall a preview window opens.
//
// A constant and not an option, because internal/options has no field for
// 'previewheight' and adding one is the option layer's change to make. Vim's
// default is 12 and this vimrc does not set it, so the number is right for
// this editor and the day somebody types ":set previewheight=5" it is
// the option that is missing and not this.
const previewHeight = 12

// errNoPreview is what CTRL-W P says when there is no preview window.
var errNoPreview = errors.New("E441: There is no preview window")

// The ex commands: ":tag", ":tags", ":tselect", ":tjump", ":pop" and
// ":pclose".
//
// They are answered here, in front of the ex layer, for the same reason
// cmd/pvim/finder.go answers ":NERDTreeToggle" and cmd/pvim/git.go the ":G"
// family: internal/ex's table has a row for ":tag" with no handler behind it,
// and a handler there would need the tag stack, which is per window and lives
// on the session. One hook on ex.Context -- a `Tag func(cmd string, count int,
// name string, bang bool) error` filled in by cmd/pvim -- would retire this
// intercept and it is reported rather than written, because internal/ex is
// somebody else's file this run.
//
// What that costs, and it is the same cost the two hooks above carry: only a
// TYPED command line comes through here. ":tag foo" in a mapping or in the
// vimrc goes straight to internal/ex and is still E319.

// tagCommands is the ex commands this file answers, with every abbreviation
// vim accepts for each.
//
// The abbreviations are the whole of the reason this is a table and not a
// switch on the first word. ":ta" is ":tag" and ":tab" is not; ":t" is ":copy"
// and must not reach here at all; ":po" is ":pop" and ":p" is ":print".
var tagCommands = map[string]string{
	"ta": "tag", "tag": "tag",
	"tags": "tags",
	"ts":   "tselect", "tse": "tselect", "tsel": "tselect", "tsele": "tselect",
	"tselec": "tselect", "tselect": "tselect",
	"tj": "tjump", "tju": "tjump", "tjum": "tjump", "tjump": "tjump",
	"po": "pop", "pop": "pop",
	"pc": "pclose", "pcl": "pclose", "pclo": "pclose", "pclos": "pclose", "pclose": "pclose",
}

// tagCommandLine answers one command line, and reports whether it was one of
// these.
func tagCommandLine(s *session, line string) (bool, error) {
	count, name, bang, arg, ok := splitExLine(line)
	if !ok {
		return false, nil
	}
	cmd, known := tagCommands[name]
	if !known {
		return false, nil
	}
	_ = bang // ":tag!" only means "give up the buffer anyway", which ":edit" decides

	switch cmd {
	case "tags":
		for _, l := range s.tagStack().List(s.bufName()) {
			s.ed.Say(l)
		}
		return true, nil
	case "pclose":
		return true, s.closePreview()
	case "pop":
		return true, s.tagPop(count)
	}

	if arg == "" {
		switch cmd {
		case "tag":
			// ":tag" with no name walks the stack forward, which is the other
			// half of CTRL-T.
			return true, s.tagForward(count)
		default:
			// ":tselect" and ":tjump" with no name re-offer the matches of the
			// entry the stack is on.
			e, ok := s.tagStack().Current()
			if !ok || len(e.Matches) == 0 {
				s.ed.Say(tags.MsgEmpty)
				return true, nil
			}
			match, chosen := s.tagPrompt(e.Matches, e.Cur)
			if !chosen {
				return true, nil
			}
			s.tagStack().SetMatch(match)
			return true, s.tagLandAndSync(e.Matches[match])
		}
	}

	pick := pickCount
	switch cmd {
	case "tselect":
		pick = pickSelect
	case "tjump":
		pick = pickJump
	}
	return true, s.tagNamed(arg, count, pick)
}

// tagNamed is ":tag NAME" and its two prompting cousins: the same command
// CTRL-] runs, with the name given rather than read off the buffer.
func (s *session) tagNamed(name string, count int, pick tagPick) error {
	matches, err := tags.Find(name, s.tagOptions())
	if err != nil {
		s.ed.Say(err.Error())
	}
	if len(matches) == 0 {
		s.ed.Say(tags.NotFound(name))
		return nil
	}
	match, ok := 0, true
	switch pick {
	case pickCount:
		match = min(max(count, 1), len(matches)) - 1
	case pickSelect:
		if match, ok = s.tagPrompt(matches, -1); !ok {
			return nil
		}
	case pickJump:
		if len(matches) > 1 {
			if match, ok = s.tagPrompt(matches, -1); !ok {
				return nil
			}
		}
	}
	return s.tagTo(name, matches, match, tagHere, 0)
}

// tagLandAndSync is tagLand with the window put back under the cursor, which
// every caller outside tagTo needs.
func (s *session) tagLandAndSync(t tags.Tag) error {
	if err := s.tagLand(t); err != nil {
		return err
	}
	s.sync()
	return nil
}

// splitExLine takes a command line apart far enough for the table above: a
// leading count, the command word, a "!" and whatever is left.
//
// It is not internal/ex's parser and does not try to be. A line with a range
// in it -- ":1,2tag" -- is not one of these commands and is handed on, which
// is what the "not ok" return is for.
func splitExLine(line string) (count int, name string, bang bool, arg string, ok bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, ":")
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		count = count*10 + int(s[i]-'0')
		i++
	}
	s = s[i:]
	j := 0
	for j < len(s) && (s[j] >= 'a' && s[j] <= 'z') {
		j++
	}
	if j == 0 {
		return 0, "", false, "", false
	}
	name, s = s[:j], s[j:]
	if strings.HasPrefix(s, "!") {
		bang, s = true, s[1:]
	}
	return count, name, bang, strings.TrimSpace(s), true
}
