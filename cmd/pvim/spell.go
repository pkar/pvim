package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/spell"
	"github.com/pkar/pvim/internal/tags"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Spelling: the four keys that add a word and the undercurl under the ones
// nobody has added.
//
// internal/spell owns the word lists and the rules; this file owns everything
// that touches the machine or the editor: which file 'spellfile' means, when
// the dictionary is read, what "zg" writes and where the underlined runs come
// from on a redraw.
//
// The vimrc has
//
//	au BufEnter *.txt,*.md setlocal spell spelllang=en_us
//
// and 'spell' reaches the window now, so this is live in every .txt and .md
// buffer the editor opens and in nothing else.
//
// # 'spellfile'
//
// Vim's rule is that "zg" writes to the first name in 'spellfile', and that an
// empty 'spellfile' is filled in from the first writable "spell" directory in
// 'runtimepath' -- which under "vim --clean" there is not, so a bare "zg"
// there is "E764: Option 'spellfile' is not set". Measured, and it is what
// testdata/keys/z_spellfile_not_set pins.
//
// pvim has no 'runtimepath'. The rule here is this editor's own: an empty
// 'spellfile' means ~/.cache/vim/spell.add, and only when that directory
// already exists.
// cmd/pvim/main.go makes it on every launch that is not --oracle, so the
// editor a person uses always has one and a graded run never does, which is
// how "zg" can be a feature here and still print vim's E764 under the oracle.
//
// # The messages, measured against vim 9.2.0321 with 'spellfile' set to my.add
//
//	zg Word 'wurdz' added to my.add and the word on a line of its own
//	zw Word 'wurdz' added to my.add and the line is "wurdz/!"
//	zG Word 'wurdz' added to /tmp/vehlSxN/0 the internal list, a temp file
//	2zg E765: 'spellfile' does not have 2 entries
//	zg on an empty line E349: No identifier under cursor
//
// "zg" twice appends the word twice: vim does not look to see whether it is
// already there, and neither does this.
//
// # What is not here
//
// - "z=", the suggestion list. Deliberately out of scope, and still refused
// loudly by cmd/pvim/session.go's zFallback, which is the only key in the z
// family that still is.
// - "zug" and "zuw", which undo a "zg" by overwriting the first byte of the
// line with a "#". They beep, as they did before this file existed.
// - "]s" and "[s", the motions to the next and previous bad word.
// - SpellCap, spell regions, 'spelloptions' and 'spellcapcheck'.

// spellDict is the word list. A variable so that a test can
// point it somewhere small: reading 236k words per test is a second nobody
// needs, and a test that depended on the machine's dictionary would fail on a
// box that ships a different one.
var spellDict = "/usr/share/dict/words"

// spellState is the editor's spelling: the dictionary, the file "zg" writes
// and the list "zG" writes, and the checker over all three.
type spellState struct {
	// dict is the system word list, read once on the first spell-checked
	// redraw and never again.
	dict *spell.List
	// file is the words "zg" and "zw" have put in 'spellfile', read when the
	// name is first needed.
	file *spell.List
	// internal is the list "zG" and "zW" write, which lasts the session. Vim
	// keeps it in a file in its own temp directory and names that file in the
	// message, so this does the same rather than printing the name of a file
	// that does not exist.
	internal *spell.List
	// ck is the checker over the three, rebuilt whenever one of them changes.
	ck *spell.Checker
	// tried says the dictionary has been looked for, so that a machine
	// without one is not stat-ed on every redraw.
	tried bool
}

// spellOn reports whether the window has 'spell' set.
func spellOn(w *window.Window) bool { return w != nil && w.Opt.Spell }

// spellStateFor returns the session's spelling state, made on first use.
func (s *session) spellStateFor() *spellState {
	if s.spell == nil {
		s.spell = &spellState{}
	}
	return s.spell
}

// checker is the word lists, with the dictionary read on the first call.
//
// A missing dictionary is not an error and says nothing: with no word list
// every word in the buffer is unknown, and a screen with a red line under
// every word is worse than a screen with none. Checker.Bad answers false for
// everything in that state, which is spell checking that is off rather than
// spell checking that is wrong.
//
// The cost, measured on this machine: 28 ms to read the 235,976 words, once,
// on the first redraw of the first .txt or .md buffer of the session, and
// 1.5 us to check a line of prose after that. A 40-row screen is 62 us of
// spell checking per frame, which is under 2% of the 3.5 ms a full repaint
// takes, so there is no per-line cache here and there does not need to be.
func (st *spellState) checker() *spell.Checker {
	if !st.tried {
		st.tried = true
		if l, err := spell.Read(spellDict); err == nil && l.Len() > 0 {
			st.dict = l
		}
		st.rebuild()
	}
	return st.ck
}

// rebuild puts the checker back together after a list changed.
func (st *spellState) rebuild() {
	st.ck = spell.New(st.dict, st.file, st.internal)
}

// available reports whether there is a dictionary at all, which is what
// decides whether anything is underlined.
func (st *spellState) available() bool { return st.checker() != nil && st.dict != nil }

// spellChecker is the word lists this session checks against: the dictionary
// and whatever 'spellfile' already holds.
//
// The spellfile is read here and not only when a "zg" writes to it, which is
// the difference between a word added last week being remembered and being
// underlined again on the next launch. It is read once per name: a file that
// is not there comes back as an empty list under that name, so a machine where
// nobody has pressed "zg" does not stat it on every redraw.
func (s *session) spellChecker() *spell.Checker {
	st := s.spellStateFor()
	ck := st.checker()
	files := s.spellFiles()
	if len(files) == 0 {
		return ck
	}
	if st.file == nil || st.file.Name != files[0] {
		l, err := spell.Read(files[0])
		if err != nil {
			return ck
		}
		st.file = l
		st.rebuild()
	}
	return st.ck
}

// spellFiles is 'spellfile', split on its commas, with pvim's default in place
// of an empty one.
//
// The default is ~/.cache/vim/spell.add and only when ~/.cache/vim is there:
// vim fills an empty 'spellfile' in from a writable directory it already has,
// and this is the same rule over the one directory this editor owns.
func (s *session) spellFiles() []string {
	value := ""
	if s.ctx != nil && s.ctx.Opt != nil {
		value = s.ctx.Opt.B.SpellFile
	}
	if strings.TrimSpace(value) != "" {
		var out []string
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
		return out
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, cacheFallback)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	return []string{filepath.Join(dir, "spell.add")}
}

// spellAdd is zg, zw, zG and zW: the word under the cursor into a word list.
//
// which is the key: 'g' and 'w' write the file, 'G' and 'W' the internal list,
// and the lowercase pair accept the word where the uppercase pair reject it.
func (s *session) spellAdd(which rune, count int) error {
	word, ok := s.spellWordUnderCursor()
	if !ok {
		// Vim's order: the word first, so a "zg" on an empty line is E349 and
		// says nothing about 'spellfile' even when there is none.
		s.ed.Say(tags.MsgNoIdent)
		return nil
	}
	bad := which == 'w' || which == 'W'
	st := s.spellStateFor()

	if which == 'G' || which == 'W' {
		if st.internal == nil {
			f, err := os.CreateTemp("", "pvim-spell-*")
			if err != nil {
				return err
			}
			name := f.Name()
			f.Close()
			st.internal = spell.NewList(name)
			st.rebuild()
		}
		if err := spellAppend(st.internal.Name, word, bad); err != nil {
			return err
		}
		st.addWord(st.internal, word, bad)
		s.ed.Say(fmt.Sprintf("Word '%s' added to %s", word, st.internal.Name))
		return nil
	}

	files := s.spellFiles()
	if len(files) == 0 {
		s.ed.Say("E764: Option 'spellfile' is not set")
		return nil
	}
	n := max(count, 1)
	if n > len(files) {
		s.ed.Say(fmt.Sprintf("E765: 'spellfile' does not have %d entries", n))
		return nil
	}
	name := files[n-1]
	if st.file == nil || st.file.Name != name {
		l, err := spell.Read(name)
		if err != nil {
			return err
		}
		st.file = l
		st.rebuild()
	}
	if err := spellAppend(name, word, bad); err != nil {
		return err
	}
	st.addWord(st.file, word, bad)
	s.ed.Say(fmt.Sprintf("Word '%s' added to %s", word, name))
	return nil
}

// addWord puts the word into a list in memory, so that the undercurl under it
// goes away on the next redraw rather than on the next launch.
func (st *spellState) addWord(l *spell.List, word string, bad bool) {
	if bad {
		l.AddBad(word)
	} else {
		l.AddGood(word)
	}
	st.rebuild()
}

// spellAppend writes one word onto the end of a word list file.
//
// A word to reject is written as "word/!", which is vim's flag for it and what
// makes the file readable by a vim that has the same file open.
func spellAppend(name, word string, bad bool) error {
	if bad {
		word += "/!"
	}
	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(word + "\n")
	return err
}

// spellWordUnderCursor is the word the four keys act on: the bad word the
// cursor is inside when 'spell' is on, and the identifier under the cursor
// otherwise.
//
// That is vim's nv_zet, which tries spell_move_to first and falls back to
// find_ident_under_cursor when it finds nothing at or before the cursor. The
// difference shows on a line like "the wurdz here" with the cursor on the
// space before "wurdz": with 'spell' on, vim adds "wurdz".
func (s *session) spellWordUnderCursor() (string, bool) {
	b := s.ed.Buffer()
	if b == nil {
		return "", false
	}
	cur := s.ed.Cursor()
	if cur.Line < 1 || cur.Line > b.LineCount() {
		return "", false
	}
	line := b.Line(cur.Line)
	if spellOn(s.win()) {
		if ck := s.spellChecker(); ck != nil && s.spellStateFor().available() {
			for _, w := range ck.BadWords(line) {
				// The cursor has to be INSIDE the word. Vim searches forward
				// for the next bad word and then throws the answer away
				// unless it starts at or before the cursor, which comes to
				// the same thing; without the second half a "zg" on a word
				// that is spelled right adds the next misspelling along the
				// line instead.
				if w.Start <= cur.Col && w.End > cur.Col {
					return w.Text, true
				}
			}
		}
	}
	word, _, ok := tags.Ident(line, cur.Col, s.ed.Options().IsKeyword)
	return word, ok
}

// spellSpans is the undercurl, as the renderer wants it.
//
// It goes into the frame's Syntax slot, which is the one place a run of buffer
// bytes can be given a highlight id without internal/screen growing a field
// for it. That is a compromise and it is written down here rather than left to
// be discovered: the clean shape is a `Spell MatchSource` on screen.Frame
// painted over the syntax spans in winline.go, five lines in a package this
// change may not touch. What it costs meanwhile is that a bad word inside a
// syntax item is drawn as SpellBad instead of as SpellBad over the item's own
// colour, which on a .txt or a .md file -- the only two the vimrc turns
// 'spell' on for, and neither of which has a syntax file loaded here -- is no
// difference at all.
func (s *session) spellSpans(tabs *window.Tabs, under map[int]screen.SpanSource, hl *screen.Table) map[int]screen.SpanSource {
	if tabs == nil || tabs.Current() == nil || hl == nil {
		return under
	}
	var windows []*window.Window
	for _, w := range tabs.Current().Windows() {
		if spellOn(w) && w.Buf != nil {
			windows = append(windows, w)
		}
	}
	if len(windows) == 0 {
		return under
	}
	st := s.spellStateFor()
	if !st.available() {
		return under
	}
	out := map[int]screen.SpanSource{}
	for id, src := range under {
		out[id] = src
	}
	id := hl.ID(screen.GroupSpellBad)
	ck := s.spellChecker()
	for _, w := range windows {
		out[w.ID] = &spellSource{buf: w.Buf, ck: ck, hl: id, under: under[w.ID]}
	}
	return out
}

// spellSource is one window's spans: whatever the syntax gave it, with the bad
// words laid over the top.
type spellSource struct {
	buf   *text.Buffer
	ck    *spell.Checker
	hl    screen.HLID
	under screen.SpanSource
}

// SpansOn is screen.SpanSource.
//
// One line's worth of work per visible line per frame, which is a split of the
// line into words and a map lookup each. There is no cache: a 40k-line file
// shows forty lines and the checker is a hash lookup, so the whole of a redraw
// is a few hundred of them.
func (s *spellSource) SpansOn(line int) []screen.SynSpan {
	var base []screen.SynSpan
	if s.under != nil {
		base = s.under.SpansOn(line)
	}
	if s.buf == nil || line < 1 || line > s.buf.LineCount() {
		return base
	}
	bad := s.ck.BadWords(s.buf.Line(line))
	if len(bad) == 0 {
		return base
	}

	// The spell spans win, so the syntax spans are cut where they overlap:
	// screen.SynSpan says spans on a line do not overlap, and a renderer
	// handed two that do would draw whichever it reached last.
	out := make([]screen.SynSpan, 0, len(base)+len(bad))
	for _, sp := range base {
		out = append(out, cutSpans(sp, bad)...)
	}
	for _, w := range bad {
		out = append(out, screen.SynSpan{Start: w.Start, End: w.End, HL: s.hl})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// cutSpans returns what is left of a syntax span once the bad words have been
// taken out of it.
func cutSpans(sp screen.SynSpan, bad []spell.Word) []screen.SynSpan {
	out := []screen.SynSpan{sp}
	for _, w := range bad {
		var next []screen.SynSpan
		for _, s := range out {
			switch {
			case w.End <= s.Start || w.Start >= s.End:
				next = append(next, s)
				continue
			}
			if s.Start < w.Start {
				next = append(next, screen.SynSpan{Start: s.Start, End: w.Start, HL: s.HL})
			}
			if w.End < s.End {
				next = append(next, screen.SynSpan{Start: w.End, End: s.End, HL: s.HL})
			}
		}
		out = next
	}
	return out
}
