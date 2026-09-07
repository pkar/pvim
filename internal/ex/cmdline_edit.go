package ex

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/options"
)

// The command line as a mode of its own: editing, history and 'wildmenu'
// completion.
//
// It holds bytes and a byte position for the same reason internal/text does:
// vim addresses the command line by byte column, and every place a rune index
// meets a byte column is a place a multi-byte character goes missing.

// Source is where the command line gets text it did not type: CTRL-R reads a
// register and CTRL-R CTRL-W reads the word under the cursor. It is an
// interface because internal/ex must not need a whole editor to test one
// keystroke, and because the terminal frontend and the window frontend supply
// the same two things by different routes.
type Source interface {
	// Register returns a register's contents, with a newline between its
	// lines, or nothing for one that is empty.
	Register(name byte) []byte
	// WordUnderCursor is what CTRL-R CTRL-W inserts.
	WordUnderCursor() []byte
}

// EditorSource is the Source a live editor supplies.
type EditorSource struct{ Ctx *Context }

// Register reads one register through the register file.
func (s EditorSource) Register(name byte) []byte {
	r := s.Ctx.regs()
	if r == nil {
		return nil
	}
	v, err := r.Get(name)
	if err != nil {
		return nil
	}
	return v.Bytes()
}

// WordUnderCursor is the keyword the cursor is on, by 'iskeyword'. It uses the
// same rule the search word commands do so that CTRL-R CTRL-W and "*" agree
// about where a word ends.
func (s EditorSource) WordUnderCursor() []byte {
	b := s.Ctx.buffer()
	p := s.Ctx.Ed.Cursor()
	line := b.Line(p.Line)
	if p.Col >= len(line) {
		return nil
	}
	start := p.Col
	for start > 0 && isWordByte(line[start-1]) {
		start--
	}
	end := p.Col
	for end < len(line) && isWordByte(line[end]) {
		end++
	}
	return line[start:end]
}

// isWordByte is the default 'iskeyword' set, which is what CTRL-R CTRL-W uses
// when nothing has changed the option.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 0xc0
}

// NewLine returns a command line opened with the given prefix character.
func NewLine(prefix byte) *Line { return &Line{Prefix: prefix, HistIdx: -1} }

// Add appends a line to a history.
//
// A repeated line is moved to the end rather than stored twice, which is what
// stops the up arrow repeating itself, and the oldest goes when the history is
// full. 'history' is 200 by default and the vimrc leaves it there.
func (h *History) Add(line string) {
	if line == "" {
		return
	}
	if h.Max <= 0 {
		h.Max = options.Defaults().G.History
	}
	for i, l := range h.Lines {
		if l == line {
			h.Lines = append(h.Lines[:i], h.Lines[i+1:]...)
			break
		}
	}
	h.Lines = append(h.Lines, line)
	if len(h.Lines) > h.Max {
		h.Lines = h.Lines[len(h.Lines)-h.Max:]
	}
}

// Kind is which history a command line belongs to, from the character that
// opened it.
func (l *Line) Kind() HistKind {
	switch l.Prefix {
	case '/', '?':
		return HistSearch
	case '=':
		return HistExpr
	default:
		return HistCommand
	}
}

// Key feeds one keystroke to the command line and reports whether it is
// finished.
//
// done says the line is over and accepted says it was accepted rather than
// abandoned. <CR> accepts, <Esc> abandons, and a <BS> on an empty line
// abandons it too, which is the one that surprises people and the reason it is
// written down here.
func (l *Line) Key(k key.Key) (done bool, accepted bool) {
	if l.pending != 0 {
		l.insertPending(k)
		return false, false
	}
	l.comp.matches = nil

	switch k.Special {
	case key.KeyCR, key.KeyNL:
		return true, true
	case key.KeyEsc:
		return true, false
	case key.KeyBS:
		if len(l.Text) == 0 {
			return true, false
		}
		l.backspace()
		return false, false
	case key.KeyDel:
		if l.Pos < len(l.Text) {
			l.Text = append(l.Text[:l.Pos], l.Text[l.Pos+1:]...)
		}
		return false, false
	case key.KeyLeft:
		l.moveLeft()
		return false, false
	case key.KeyRight:
		l.moveRight()
		return false, false
	case key.KeyHome:
		l.Pos = 0
		return false, false
	case key.KeyEnd:
		l.Pos = len(l.Text)
		return false, false
	case key.KeyUp:
		l.walkHistory(-1)
		return false, false
	case key.KeyDown:
		l.walkHistory(1)
		return false, false
	}

	switch k {
	case key.Ctrl('h'):
		if len(l.Text) == 0 {
			return true, false
		}
		l.backspace()
	case key.Ctrl('u'):
		// vim deletes to the start of the line, keeping what is after the
		// cursor, which is not the same as clearing it.
		l.Text = append([]byte{}, l.Text[l.Pos:]...)
		l.Pos = 0
	case key.Ctrl('w'):
		l.deleteWord()
	case key.Ctrl('b'):
		l.Pos = 0
	case key.Ctrl('e'):
		l.Pos = len(l.Text)
	case key.Ctrl('p'):
		l.walkHistory(-1)
	case key.Ctrl('n'):
		l.walkHistory(1)
	case key.Ctrl('r'):
		l.pending = 'r'
	case key.Ctrl('c'):
		return true, false
	default:
		if k.IsRune() && k.Rune >= 0x20 {
			l.insert([]byte(string(k.Rune)))
		}
	}
	return false, false
}

// insertPending handles the key after CTRL-R.
//
// CTRL-R CTRL-W is the word under the cursor and CTRL-R CTRL-O x is the
// literal form of a register, which the vimrc uses on line 44 as its
// insert-mode paste of the "+" register. Both are one more key, so the pending
// state is one byte and not a state machine.
func (l *Line) insertPending(k key.Key) {
	l.pending = 0
	if l.Src == nil {
		return
	}
	switch {
	case k == key.Ctrl('w'):
		l.insert(l.Src.WordUnderCursor())
	case k == key.Ctrl('o'), k == key.Ctrl('r'):
		// The literal and the "insert as typed" forms. Neither difference is
		// visible on a command line, which cannot hold a line break anyway.
		l.pending = 'r'
	case k.IsRune():
		text := l.Src.Register(byte(k.Rune))
		// A register with line breaks in it becomes carriage returns on the
		// command line, which is how vim shows one and what makes a yanked
		// line usable as an argument.
		l.insert([]byte(strings.ReplaceAll(string(text), "\n", "\r")))
	}
}

// insert puts bytes in at the cursor.
func (l *Line) insert(b []byte) {
	if len(b) == 0 {
		return
	}
	out := make([]byte, 0, len(l.Text)+len(b))
	out = append(out, l.Text[:l.Pos]...)
	out = append(out, b...)
	out = append(out, l.Text[l.Pos:]...)
	l.Text = out
	l.Pos += len(b)
}

// backspace deletes the character before the cursor, which is a rune and not a
// byte.
func (l *Line) backspace() {
	if l.Pos == 0 {
		return
	}
	start := l.Pos - 1
	for start > 0 && l.Text[start]&0xc0 == 0x80 {
		start--
	}
	l.Text = append(l.Text[:start], l.Text[l.Pos:]...)
	l.Pos = start
}

// moveLeft and moveRight step by a rune.
func (l *Line) moveLeft() {
	if l.Pos == 0 {
		return
	}
	l.Pos--
	for l.Pos > 0 && l.Text[l.Pos]&0xc0 == 0x80 {
		l.Pos--
	}
}

func (l *Line) moveRight() {
	if l.Pos >= len(l.Text) {
		return
	}
	l.Pos++
	for l.Pos < len(l.Text) && l.Text[l.Pos]&0xc0 == 0x80 {
		l.Pos++
	}
}

// deleteWord is CTRL-W: back over white space, then back over one word.
func (l *Line) deleteWord() {
	end := l.Pos
	i := l.Pos
	for i > 0 && (l.Text[i-1] == ' ' || l.Text[i-1] == '\t') {
		i--
	}
	if i > 0 && isWordByte(l.Text[i-1]) {
		for i > 0 && isWordByte(l.Text[i-1]) {
			i--
		}
	} else {
		for i > 0 && !isWordByte(l.Text[i-1]) && l.Text[i-1] != ' ' && l.Text[i-1] != '\t' {
			i--
		}
	}
	l.Text = append(l.Text[:i], l.Text[end:]...)
	l.Pos = i
}

// walkHistory moves through the history by n entries, -1 being one older.
//
// The line being typed is saved on the way in and comes back when the walk
// returns past the newest entry, which is what makes the down arrow undo an up
// arrow rather than leaving a recalled line behind.
func (l *Line) walkHistory(n int) {
	if l.Hist == nil || len(l.Hist.Lines) == 0 {
		return
	}
	if l.HistIdx < 0 {
		l.Saved = append([]byte{}, l.Text...)
		l.HistIdx = len(l.Hist.Lines)
	}
	idx := l.HistIdx + n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(l.Hist.Lines) {
		l.HistIdx = -1
		l.Text = append([]byte{}, l.Saved...)
		l.Pos = len(l.Text)
		return
	}
	l.HistIdx = idx
	l.Text = []byte(l.Hist.Lines[idx])
	l.Pos = len(l.Text)
}

// Complete advances the command line by one 'wildmenu' step, which is what
// <Tab> does and, with back set, what <S-Tab> does.
//
// 'wildmode' decides what a step means. The vimrc leaves it at vim's default,
// "full", which cycles through the matches one at a time and comes back to
// what was typed after the last one. That last step is not decoration: it is
// how you get out of a completion you did not want without deleting the word.
//
// 'wildignore' removes matches, and the vimrc's list is thirteen patterns
// long. A pattern there is a file glob in which "*" crosses directory
// separators, which is why "*/tmp/*" hides everything under a tmp directory
// however deep it is.
func (l *Line) Complete(c Completer, wildmode, wildignore string, back bool) error {
	if c == nil {
		return nil
	}
	if l.comp.matches == nil {
		kind, start := l.completionAt()
		prefix := string(l.Text[start:l.Pos])
		matches := filterIgnored(c.Complete(kind, prefix), wildignore, kind)
		if len(matches) == 0 {
			return nil
		}
		l.comp.matches = matches
		l.comp.start = start
		l.comp.typed = prefix
		// The selection starts on the typed text, which is the slot one past
		// the last match. A forward step from there is the first match and a
		// backward one is the last, which is what makes Shift-Tab useful
		// before Tab has been pressed at all.
		l.comp.idx = len(matches)
	}
	step := 1
	if back {
		step = -1
	}
	n := len(l.comp.matches) + 1
	l.comp.idx = ((l.comp.idx+step)%n + n) % n
	replacement := l.comp.typed
	if l.comp.idx < len(l.comp.matches) {
		replacement = l.comp.matches[l.comp.idx]
	}
	l.replaceFrom(l.comp.start, replacement)
	return nil
}

// Matches is the candidate list a wildmenu draws on the line above, and
// MatchIndex is which of them is selected, or -1 for the typed text.
func (l *Line) Matches() []string { return l.comp.matches }
func (l *Line) MatchIndex() int {
	if l.comp.idx >= len(l.comp.matches) {
		return -1
	}
	return l.comp.idx
}

// replaceFrom swaps everything between start and the cursor for s.
func (l *Line) replaceFrom(start int, s string) {
	out := make([]byte, 0, start+len(s)+len(l.Text)-l.Pos)
	out = append(out, l.Text[:start]...)
	out = append(out, s...)
	rest := l.Text[l.Pos:]
	l.Pos = len(out)
	out = append(out, rest...)
	l.Text = out
}

// completionAt says what should be completed at the cursor and where the word
// being completed starts.
//
// The command name is completed at the front of the line; after it the kind
// comes from the command's own entry, which is what makes ":set " offer option
// names and ":b " buffer names. A search line completes nothing, because vim
// has no pattern completion and offering file names after a "/" would be
// actively wrong.
func (l *Line) completionAt() (CompKind, int) {
	if l.Prefix != ':' {
		return CompNone, l.Pos
	}
	text := string(l.Text[:l.Pos])
	p := &parser{s: text}
	var c Cmd
	_ = parseMods(p, &c)
	if _, err := parseRange(p); err != nil {
		return CompNone, l.Pos
	}
	p.skipWhite()
	nameStart := p.i
	name := scanName(p)
	if p.i >= len(text) {
		// Still typing the name.
		return CompCommand, nameStart
	}
	cmd, ok := Lookup(name)
	if !ok {
		return CompNone, l.Pos
	}
	if p.peek() == '!' {
		p.i++
	}
	p.skipWhite()
	// The word being completed starts after the last unescaped space.
	start := p.i
	for i := p.i; i < len(text); i++ {
		if text[i] == ' ' && (i == 0 || text[i-1] != '\\') {
			start = i + 1
		}
	}
	return kindFor(cmd.Args), start
}

// kindFor maps a command's argument kind onto a completion kind. They are two
// enums rather than one because a command's arguments are a fact about the
// command and a completion is a fact about the cursor: ":e" takes a file and
// completes a file, and ":normal" takes text and completes nothing.
func kindFor(a ArgKind) CompKind {
	switch a {
	case ArgFile:
		return CompFile
	case ArgDir:
		return CompDir
	case ArgBuffer:
		return CompBuffer
	case ArgOption:
		return CompOption
	case ArgCommand:
		return CompCommand
	case ArgHighlight:
		return CompHighlight
	case ArgTag:
		return CompTag
	case ArgColorscheme:
		return CompColorscheme
	}
	return CompNone
}

// filterIgnored removes the candidates 'wildignore' hides. It applies to file
// and directory completion only, which is vim's rule: ":b" still offers a
// buffer whose name matches a wildignore pattern.
func filterIgnored(names []string, wildignore string, kind CompKind) []string {
	if wildignore == "" || (kind != CompFile && kind != CompDir) {
		return names
	}
	patterns := strings.Split(wildignore, ",")
	out := names[:0:0]
	for _, n := range names {
		hidden := false
		for _, p := range patterns {
			if p != "" && globMatch(p, n) {
				hidden = true
				break
			}
		}
		if !hidden {
			out = append(out, n)
		}
	}
	return out
}

// globMatch is 'wildignore' matching: "*" matches anything including a "/",
// "?" matches one character, and everything else is literal.
//
// It is not path.Match, and the difference is the whole point: path.Match's
// "*" stops at a separator, so "*/tmp/*" would match nothing and the vimrc's
// most useful ignore pattern would be inert. A pattern with no "/" in it is
// matched against the base name as well, which is how "*.o" hides
// "build/x.o".
func globMatch(pattern, name string) bool {
	if globHere(pattern, name) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		return globHere(pattern, filepath.Base(name))
	}
	return false
}

// globHere is the matcher itself, iterative with one backtrack point, so a
// pattern of many stars cannot make it exponential.
func globHere(pattern, name string) bool {
	px, nx := 0, 0
	star, mark := -1, 0
	for nx < len(name) {
		switch {
		case px < len(pattern) && (pattern[px] == '?' || pattern[px] == name[nx]):
			px++
			nx++
		case px < len(pattern) && pattern[px] == '*':
			star, mark = px, nx
			px++
		case star >= 0:
			px = star + 1
			mark++
			nx = mark
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// Candidates is the Completer a live editor supplies: the command table, the
// option table, the buffer list, the help tags and the file system.
type Candidates struct{ Ctx *Context }

// Complete returns the sorted candidates for one prefix.
func (c Candidates) Complete(kind CompKind, prefix string) []string {
	switch kind {
	case CompCommand:
		// The punctuation commands are in the table so that ":!" and ":>"
		// parse, and they are not offered here: nobody completes a ":" into a
		// "!", and vim does not offer them either.
		var names []string
		for _, n := range Names() {
			if isAlpha(n[0]) {
				names = append(names, n)
			}
		}
		return withPrefix(names, prefix)
	case CompOption:
		return withPrefix(options.Names(), prefix)
	case CompBuffer:
		var names []string
		for _, b := range c.Ctx.bufs().Bufs {
			if b.Listed && b.Name != "" {
				names = append(names, b.Name)
			}
		}
		return withPrefix(names, prefix)
	case CompTag:
		return HelpTagNames(prefix)
	case CompFile, CompDir:
		return fileCandidates(prefix, kind == CompDir)
	}
	return nil
}

// withPrefix keeps the names that start with prefix, sorted.
func withPrefix(names []string, prefix string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// fileCandidates lists the entries of the directory the prefix names, keeping
// the prefix's own directory part so that the completed text replaces the
// whole word.
func fileCandidates(prefix string, dirsOnly bool) []string {
	dir, base := filepath.Split(prefix)
	lookIn := dir
	if lookIn == "" {
		lookIn = "."
	}
	entries, err := os.ReadDir(Abs(lookIn))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), base) {
			continue
		}
		if dirsOnly && !e.IsDir() {
			continue
		}
		name := dir + e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
