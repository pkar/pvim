package undofile

import (
	"fmt"
	"os"
	"time"
)

// The history file: the fifth of viminfo anybody notices.
//
// Vim's .viminfo holds registers, buffer lists, file marks, the jump list, the
// three command histories, the last search pattern, the last substitute string,
// options set with ":set", and a hat-tip to the 1990s in the shape of a
// "*encoding=" line. the format under "Do not build" and it is
// right to: what a person misses after a restart is the up arrow on ":" and
// "/", '0 and '. landing where they landed, and CTRL-O walking back
// through the files they were in. That is what is here. Registers are
// deliberately not: a yank that outlives the process it was made in is a
// surprise more often than a convenience, and MacVim's own default for
// 'viminfo' keeps fifty lines of them for exactly nobody.
//
// One file, ~/.cache/vim/history by default, rewritten atomically at exit. Not
// merged with what another instance wrote in the meantime, which vim does and
// which needs a lock, a read-modify-write and a rule for whose search is more
// recent. The socket in internal/server means there is normally one pvim on
// this machine, and the last one out writes the file.

// Place is a position in a file: what a mark and a jump-list entry both are.
type Place struct {
	File string
	Pos  Pos
}

// Mark is a named mark. Vim keeps the uppercase ones and the numbered ones
// across sessions and the lowercase ones inside a buffer, and so does this: 'a
// through 'z belong to a buffer that may not be open, and vim drops them.
type Mark struct {
	Name byte
	Place
}

// FileMark is where the cursor was when a file was last closed, which is vim's
// '" mark and the reason ":e" on a file you edited opens where you
// left it. When is what orders the list and what trims it.
type FileMark struct {
	Place
	When time.Time
}

// History is everything the history file holds.
type History struct {
	Written time.Time

	// The three command lines vim keeps and internal/ex has a HistKind for:
	// ":", "/" and "=". Oldest first, which is the order internal/ex.History
	// stores them in and the order the up arrow walks backwards through.
	Command []string
	Search  []string
	Expr    []string

	// Search is also a pattern: the one "n" repeats. It is not the last line
	// of Search, because a search that failed to compile is in the history and
	// is not the pattern, and because "*" sets the pattern without putting
	// anything in the history at all.
	Pattern string

	// Substitute is the replacement ":s//~/" reuses.
	Substitute string

	Marks []Mark
	Jumps []Place
	Files []FileMark
}

// The limits. Vim's 'history' is 200 and the vimrc leaves it there; the file
// mark list is vim's viminfo "'" count, which defaults to 100.
const (
	DefaultHistory  = 200
	DefaultFileMark = 100
	maxJumps        = 100
)

// Trim holds each list to its limit, dropping the oldest, which is what vim's
// 'history' does on the way into the file rather than on the way out of it.
func (h *History) Trim(history int) {
	if history <= 0 {
		history = DefaultHistory
	}
	h.Command = tail(h.Command, history)
	h.Search = tail(h.Search, history)
	h.Expr = tail(h.Expr, history)
	if len(h.Jumps) > maxJumps {
		h.Jumps = h.Jumps[len(h.Jumps)-maxJumps:]
	}
	if len(h.Files) > DefaultFileMark {
		h.Files = h.Files[len(h.Files)-DefaultFileMark:]
	}
}

func tail(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// EncodeHistory returns the bytes of a history file.
func EncodeHistory(h History) []byte {
	var e enc
	e.raw([]byte(magicHist))
	e.uint(Version)
	e.int64(nanos(h.Written))
	strs(&e, h.Command)
	strs(&e, h.Search)
	strs(&e, h.Expr)
	e.str(h.Pattern)
	e.str(h.Substitute)
	e.uint(uint64(len(h.Marks)))
	for _, m := range h.Marks {
		e.raw([]byte{m.Name})
		e.str(m.File)
		e.int(m.Pos.Line)
		e.int(m.Pos.Col)
	}
	e.uint(uint64(len(h.Jumps)))
	for _, j := range h.Jumps {
		e.str(j.File)
		e.int(j.Pos.Line)
		e.int(j.Pos.Col)
	}
	e.uint(uint64(len(h.Files)))
	for _, f := range h.Files {
		e.str(f.File)
		e.int(f.Pos.Line)
		e.int(f.Pos.Col)
		e.int64(nanos(f.When))
	}
	return e.seal()
}

func strs(e *enc, ss []string) {
	e.uint(uint64(len(ss)))
	for _, s := range ss {
		e.str(s)
	}
}

// DecodeHistory reads a history file back.
func DecodeHistory(p []byte) (History, error) {
	d, err := open(p, magicHist)
	if err != nil {
		return History{}, err
	}
	var h History
	h.Written = unixNano(d.int64())
	h.Command = readStrs(d)
	h.Search = readStrs(d)
	h.Expr = readStrs(d)
	h.Pattern = d.str()
	h.Substitute = d.str()
	if n, ok := count(d); ok {
		h.Marks = make([]Mark, n)
		for i := range h.Marks {
			if b := d.take(1); len(b) == 1 {
				h.Marks[i].Name = b[0]
			}
			h.Marks[i].File = d.str()
			h.Marks[i].Pos.Line = d.int()
			h.Marks[i].Pos.Col = d.int()
		}
	}
	if n, ok := count(d); ok {
		h.Jumps = make([]Place, n)
		for i := range h.Jumps {
			h.Jumps[i].File = d.str()
			h.Jumps[i].Pos.Line = d.int()
			h.Jumps[i].Pos.Col = d.int()
		}
	}
	if n, ok := count(d); ok {
		h.Files = make([]FileMark, n)
		for i := range h.Files {
			h.Files[i].File = d.str()
			h.Files[i].Pos.Line = d.int()
			h.Files[i].Pos.Col = d.int()
			h.Files[i].When = unixNano(d.int64())
		}
	}
	if d.err != nil {
		return History{}, d.err
	}
	if len(d.b) != 0 {
		return History{}, fmt.Errorf("%w: %d bytes after the history", ErrCorrupt, len(d.b))
	}
	return h, nil
}

// count reads a list length and refuses one longer than the bytes left, which
// is the check that stops a corrupt varint from asking for a four-billion entry
// slice.
func count(d *dec) (int, bool) {
	n := d.uint()
	if d.err != nil {
		return 0, false
	}
	if n > uint64(len(d.b)) {
		d.fail(fmt.Errorf("%w: %d entries, %d bytes left", ErrCorrupt, n, len(d.b)))
		return 0, false
	}
	return int(n), true
}

func readStrs(d *dec) []string {
	n, ok := count(d)
	if !ok {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = d.str()
	}
	return out
}

// WriteHistory writes the history file atomically, so that a crash during the
// write leaves's history rather than half of's.
func WriteHistory(path string, h History) error {
	h.Written = time.Now()
	return writeFileAtomic(path, EncodeHistory(h))
}

// ReadHistory reads the history file. A missing file is not an error worth
// anything to the caller -- the first launch has no history -- so it comes back
// as an empty History and os.ErrNotExist for a caller that wants to know.
func ReadHistory(path string) (History, error) {
	p, err := os.ReadFile(path)
	if err != nil {
		return History{}, err
	}
	return DecodeHistory(p)
}
