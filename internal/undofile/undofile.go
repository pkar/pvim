// Package undofile is everything pvim keeps on disk between runs: the undo
// tree, the swap file a crash is recovered from, and the command line, search,
// mark and jump histories.
//
// Three formats, all pvim's own and all versioned. Vim's .un~ and .viminfo are
// deliberately neither read nor written: compatibility with files MacVim wrote
// is worth nothing the day after cutover, and both formats are annoying. What
// is kept from vim is the shape of the guarantees, not the bytes -- an undo
// file that belongs to a file changed behind the editor's back is refused
// rather than applied, a swap file names the process holding it so a second
// editor can tell a crash from a colleague, and the swap file's name is the
// one 'directory' with a trailing "//" asks for.
//
// # This is the first time persistent undo will actually work here
//
// The survey behind this package found `set undofile` with
// `undodir=~/.cache/vim` in the vimrc and no such directory on the disk. Vim
// will not create undodir itself: it writes nothing, says nothing, and
// 'undofile' has therefore been on and silently doing nothing on this machine
// for years. cmd/pvim/main.go's cacheDir makes the directory on every launch
// that is not --oracle (main.go:144), so pvim is the first editor on this box
// for which the option means anything. The vimrc has since grown its own mkdir
// and now points undodir at <vimrc dir>/state/undo// and 'directory' at
// <vimrc dir>/state/swap//, which is why nothing here hard-codes ~/.cache/vim:
// the paths come from the options, and ~/.cache/vim is only the fallback for a
// run with no vimrc behind it.
//
// # Names
//
// Swap files are named exactly as vim names them, ".swp" and all, because a
// swap file is a lock other editors have to see: leave MacVim and pvim
// disagreeing about the name and two editors sit on one file with neither
// warning about the other. Reading one it cannot parse is not a failure, it is
// "[does not look like a Vim swap file]" and the E325 prompt anyway, which is
// what vim says about a foreign swap file too.
//
// Undo files are named with a ".pvundo" suffix vim never writes. The opposite
// reasoning: undo history is private, the formats are incompatible, and vim's
// undo file for a given source file has no suffix at all when undodir ends in
// "//" -- so sharing the name would mean each editor stamping on the other's
// history and vim answering E824 for the rest of the cutover. Two suffixes cost
// nothing and both editors keep their own.
//
// # Framing
//
// Every file is a magic string, a version, a body of length-prefixed fields and
// a CRC32 of everything before it. Integers are varints, byte strings and text
// are a length and then the bytes, and there is no padding and no alignment
// anywhere: the format is read back by exactly one program and the only thing
// it has to survive is a version bump and a machine that stops mid-write. Both
// have a test.
package undofile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Version is the format version stamped into every file this package writes.
//
// One number for all three formats. They land in the same release and are read
// by the same binary, and a reader that finds a version it does not know
// refuses the file rather than guessing: an undo file misread is a buffer full
// of somebody else's text, which is worse in every way than losing the history.
const Version = 1

// The magic strings. Eight bytes each so that the kind of a file is decidable
// from its first read, and readable in a hex dump because the first thing
// anybody does with a format they do not trust is look at it.
const (
	magicUndo = "pvimundo"
	magicSwap = "pvimswap"
	magicHist = "pvimhist"
)

// The errors a reader answers with. Every one of them means "do not use this
// file", and the caller's response differs: a stale undo file is deleted and
// rewritten, a foreign swap file still raises E325.
var (
	// ErrMagic is a file that is not the kind asked for: a vim .swp, a
	// truncated file, or something else entirely.
	ErrMagic = errors.New("not a pvim file of this kind")

	// ErrVersion is a file from a pvim that wrote a format this one does not
	// know. Newer or older; both are refused.
	ErrVersion = errors.New("unknown format version")

	// ErrCorrupt is a file whose CRC does not match or whose body ran out
	// early. A kill in the middle of a write produces this, which is why the
	// swap file's records carry their own CRC and are read one at a time.
	ErrCorrupt = errors.New("corrupt file")

	// ErrStale is an undo file whose header does not describe the file on the
	// disk any more, which is the one case that matters most: apply it and the
	// buffer fills with text that was never in this file. Vim invalidates on
	// the same evidence.
	ErrStale = errors.New("file changed since the history was written")
)

// enc builds one file. Append-only, no seeking, no error return on any writer:
// a bytes.Buffer does not fail and pretending otherwise would put an error
// check on every field of every record.
type enc struct{ b []byte }

func (e *enc) raw(p []byte)  { e.b = append(e.b, p...) }
func (e *enc) str(s string)  { e.uint(uint64(len(s))); e.b = append(e.b, s...) }
func (e *enc) blob(p []byte) { e.uint(uint64(len(p))); e.b = append(e.b, p...) }

func (e *enc) uint(v uint64) { e.b = binary.AppendUvarint(e.b, v) }
func (e *enc) int(v int)     { e.b = binary.AppendVarint(e.b, int64(v)) }
func (e *enc) int64(v int64) { e.b = binary.AppendVarint(e.b, v) }

func (e *enc) bool(v bool) {
	if v {
		e.b = append(e.b, 1)
		return
	}
	e.b = append(e.b, 0)
}

// lines writes a run of buffer lines. The count first so a reader can size its
// slice once, which for a 40k-line log is the difference between one allocation
// and forty thousand.
func (e *enc) lines(ls [][]byte) {
	e.uint(uint64(len(ls)))
	for _, l := range ls {
		e.blob(l)
	}
}

// seal appends the CRC32 of everything written so far and returns the file.
func (e *enc) seal() []byte {
	return binary.BigEndian.AppendUint32(e.b, crc32.ChecksumIEEE(e.b))
}

// dec reads one file back.
//
// The first error is kept and every later read is a no-op returning a zero
// value, so a decoder is written as a straight run of reads with one error
// check at the end. That is the same trade bufio.Scanner makes and for the same
// reason: a length prefix that has been corrupted produces a hundred failures
// downstream and only the first one says anything useful.
type dec struct {
	b   []byte
	err error
}

func (d *dec) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// take returns the next n bytes, or nil having failed.
func (d *dec) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.b) {
		d.fail(fmt.Errorf("%w: wanted %d bytes, %d left", ErrCorrupt, n, len(d.b)))
		return nil
	}
	p := d.b[:n]
	d.b = d.b[n:]
	return p
}

func (d *dec) uint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.fail(fmt.Errorf("%w: bad unsigned varint", ErrCorrupt))
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *dec) int64() int64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Varint(d.b)
	if n <= 0 {
		d.fail(fmt.Errorf("%w: bad varint", ErrCorrupt))
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *dec) int() int { return int(d.int64()) }

func (d *dec) bool() bool {
	p := d.take(1)
	return len(p) == 1 && p[0] != 0
}

func (d *dec) str() string {
	n := d.uint()
	if n > uint64(len(d.b)) {
		d.fail(fmt.Errorf("%w: string of %d bytes, %d left", ErrCorrupt, n, len(d.b)))
		return ""
	}
	return string(d.take(int(n)))
}

func (d *dec) blob() []byte {
	n := d.uint()
	if n > uint64(len(d.b)) {
		d.fail(fmt.Errorf("%w: blob of %d bytes, %d left", ErrCorrupt, n, len(d.b)))
		return nil
	}
	// Copied, not aliased: the caller holds these for the life of a buffer and
	// the backing array is the whole file.
	p := d.take(int(n))
	out := make([]byte, len(p))
	copy(out, p)
	return out
}

// lines reads a run of buffer lines back.
//
// The count is checked against what is left before the slice is made, because
// a corrupted length prefix says four billion and make() is happy to try.
func (d *dec) lines() [][]byte {
	n := d.uint()
	if n > uint64(len(d.b)) {
		d.fail(fmt.Errorf("%w: %d lines, %d bytes left", ErrCorrupt, n, len(d.b)))
		return nil
	}
	out := make([][]byte, n)
	for i := range out {
		out[i] = d.blob()
	}
	if d.err != nil {
		return nil
	}
	return out
}

// open checks the magic, the version and the CRC and returns a decoder over the
// body. The undo file and the history file are read through it.
func open(p []byte, magic string) (*dec, error) {
	if len(p) < len(magic)+4 || string(p[:len(magic)]) != magic {
		return nil, ErrMagic
	}
	body, sum := p[:len(p)-4], binary.BigEndian.Uint32(p[len(p)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrCorrupt)
	}
	return openHeader(body, magic)
}

// openHeader is open without the whole-file checksum, for the one format that
// does not have one: a swap file is appended to while it is being read by
// nobody and its records carry a CRC each, so the four bytes at the end of it
// are a record's checksum and not the file's.
func openHeader(p []byte, magic string) (*dec, error) {
	if len(p) < len(magic) || string(p[:len(magic)]) != magic {
		return nil, ErrMagic
	}
	d := &dec{b: p[len(magic):]}
	if v := d.uint(); v != Version {
		if d.err != nil {
			return nil, d.err
		}
		return nil, fmt.Errorf("%w: %d, this pvim writes %d", ErrVersion, v, Version)
	}
	return d, nil
}

// Pos is a cursor position: a 1-based line and a 0-based byte column, which is
// internal/text's Pos and vim's getpos(). This package keeps its own so that it
// imports nothing in this module at all -- it is a leaf, like internal/text and
// internal/options, and cmd/pvim converts.
type Pos struct {
	Line int
	Col  int
}
