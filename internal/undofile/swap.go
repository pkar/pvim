package undofile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"
)

// The swap file: what is on the disk while a buffer is being edited, so that a
// kill -9, a panic or a power cut costs the time since the last sync and not
// the day's work.
//
// Vim's swap file is a paged block file with a free list, written that way in
// 1991 because the buffer itself lived on the disk and the memory was 640k.
// This one is a header and an append-only log of line-range replacements: open
// the file, append the change, never seek, never rewrite. That is the property
// that matters after a kill -- a file only ever appended to is either complete
// or complete plus a torn record at the end, and a torn record is detected by
// its own length and CRC and dropped. There is no state on the disk that a
// half-finished write can make inconsistent, which is exactly the failure a
// block file with a free list has to work to avoid.
//
// The cost is that the file grows with the editing rather than with the buffer.
// Two bounds hold it: a change touching more than half the buffer is written as
// a whole snapshot rather than a delta, and a log that has grown past
// swapGrowth times the buffer starts again with a snapshot. Neither is subtle
// and both have a test.

// The record kinds.
const (
	recFull  = 1 // the whole buffer
	recLines = 2 // one line-range replacement
	recSaved = 3 // ":w" happened; the file on the disk is now this
)

// swapGrowth is how many times the buffer's own size the log may reach before
// Sync rewrites it. Three is a guess with a reason: at two, a file edited in
// one place all afternoon rewrites constantly; past four, the recovery read of
// a long session starts to be measurable on a 40k-line file.
const swapGrowth = 3

// SwapMeta is what a swap file records about the file it is protecting.
//
// The digest as well as the size and the modification time, because the point
// of all three is to answer "is what is on the disk what I started from" after
// a crash, and mtime alone is answered wrongly by any tool that preserves it.
type SwapMeta struct {
	File  string
	Size  int64
	Mtime time.Time
	SHA   [32]byte
}

// Swap is an open swap file, being written as the editing happens.
type Swap struct {
	f    *os.File
	path string

	// last is the buffer as the log currently describes it. Sync diffs against
	// this to find the run of lines to append, which is what makes a
	// keystroke's worth of change a keystroke's worth of write.
	last  [][]byte
	noEOL bool

	// bytes written and the size of the buffer at the last full snapshot,
	// which is what the growth check above compares.
	written int64
	full    int64

	// saved is the digest of the contents as of the last ":w", and content is
	// the digest of what the log currently describes. The two are what
	// "modified: YES" is, and keeping them here means the header never has to
	// be rewritten to flip a dirty flag.
	saved   [32]byte
	content [32]byte

	meta SwapMeta
}

// swapNames is the sequence of names vim tries when the first is taken, which
// is what "(E)dit anyway" needs: the second editor of one file gets its own
// swap file rather than writing into the first one's.
//
// Vim walks the last letter down from 'p' through 'a' and then the middle one,
// giving .swp, .swo, .swn and so on. This walks the same first sixteen and
// stops, because a seventeenth simultaneous edit of one file is not a thing
// that happens to a person and E303 is a better answer than a search.
func swapNames(path string) []string {
	if len(path) < 4 || path[len(path)-4:] != swapSuffix {
		return []string{path}
	}
	out := []string{path}
	base := path[:len(path)-1]
	for c := byte('o'); c >= 'a'; c-- {
		out = append(out, base+string(c))
	}
	return out
}

// CreateSwap opens a swap file for a buffer and writes the first snapshot.
//
// The name is the one SwapName produced; if that one is taken, the next of
// swapNames is used, which is how vim's "(E)dit anyway" ends up on .swo. The
// name actually used is Swap.Path.
//
// O_EXCL, always: an editor that opens an existing swap file has just thrown
// away whatever the crashed session left in it, and the whole point of the E325
// prompt is that the decision belongs to a person.
func CreateSwap(path string, meta SwapMeta, lines [][]byte, noEOL bool, cursor Pos) (*Swap, error) {
	var firstErr error
	for _, name := range swapNames(path) {
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if errors.Is(err, os.ErrExist) {
				continue
			}
			// A directory that does not exist or cannot be written is not
			// going to be fixed by trying fifteen more names in it.
			return nil, err
		}
		s := &Swap{f: f, path: name, meta: meta, saved: meta.SHA}
		if err := s.header(); err != nil {
			f.Close()
			os.Remove(name)
			return nil, err
		}
		if err := s.snapshot(lines, noEOL, cursor); err != nil {
			f.Close()
			os.Remove(name)
			return nil, err
		}
		return s, nil
	}
	return nil, fmt.Errorf("E303: Unable to open swap file for %q, recovery impossible: %w", meta.File, firstErr)
}

// Path is the file this swap is being written to, which is not always the name
// CreateSwap was asked for. See swapNames.
func (s *Swap) Path() string { return s.path }

func (s *Swap) header() error {
	var e enc
	e.raw([]byte(magicSwap))
	e.uint(Version)
	e.int64(nanos(time.Now()))
	e.int(os.Getpid())
	e.str(hostName())
	e.str(userName())
	e.str(s.meta.File)
	e.int64(s.meta.Size)
	e.int64(nanos(s.meta.Mtime))
	e.raw(s.meta.SHA[:])
	// No CRC on the header: it is followed by records that each carry their
	// own, and a checksum over a prefix that is about to be appended to would
	// have to be rewritten on every sync. The header is fixed at creation and
	// its own fields are checked when they are used.
	return s.write(e.b)
}

func (s *Swap) write(p []byte) error {
	n, err := s.f.Write(p)
	s.written += int64(n)
	return err
}

// record appends one record: its length, its payload, and a CRC of the
// payload. Length first so a truncated tail is recognised without parsing it,
// CRC after so a torn write inside the payload is caught too.
func (s *Swap) record(payload []byte) error {
	var head []byte
	head = binary.AppendUvarint(head, uint64(len(payload)))
	if err := s.write(head); err != nil {
		return err
	}
	if err := s.write(payload); err != nil {
		return err
	}
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.ChecksumIEEE(payload))
	if err := s.write(sum[:]); err != nil {
		return err
	}
	// Sync, every record. A swap file that is only in the page cache protects
	// against a crash of pvim and not against a crash of the machine, and the
	// second is the one the option exists for. It is one fsync per sync
	// interval and not one per keystroke, because Sync only writes when
	// something changed.
	return s.f.Sync()
}

func (s *Swap) snapshot(lines [][]byte, noEOL bool, cursor Pos) error {
	var e enc
	e.raw([]byte{recFull})
	e.int(cursor.Line)
	e.int(cursor.Col)
	e.bool(noEOL)
	e.lines(lines)
	if err := s.record(e.b); err != nil {
		return err
	}
	s.remember(lines, noEOL)
	s.full = s.written
	return nil
}

// remember copies the buffer this swap now describes. Copied and not aliased:
// the caller owns those slices and internal/text mutates them in place.
func (s *Swap) remember(lines [][]byte, noEOL bool) {
	s.last = make([][]byte, len(lines))
	for i, l := range lines {
		s.last[i] = append([]byte(nil), l...)
	}
	s.noEOL = noEOL
	s.content = digestLines(lines, noEOL)
}

// Sync appends whatever has changed since the last one, and writes nothing at
// all when nothing has.
//
// Nothing at all is the common case and it is what makes this callable from the
// key loop: a cursor motion, a mode change, a failed search and a ":set" all
// leave the buffer alone and cost one length compare and a memcmp of the lines
// that are equal. Vim reaches the same place from the other side, syncing every
// 'updatecount' characters and after 'updatetime' of idleness, and gets a
// window in which work can be lost; there is none here.
func (s *Swap) Sync(lines [][]byte, noEOL bool, cursor Pos) error {
	if noEOL == s.noEOL && sameLines(s.last, lines) {
		return nil
	}
	lead, trail := commonEnds(s.last, lines)
	changed := len(lines) - lead - trail
	replaced := len(s.last) - lead - trail
	// A change that touches most of the buffer, or a log that has grown past
	// a few copies of it, is written as a snapshot instead: the replay is
	// shorter and the file stops growing without bound in a ":%s" loop.
	if changed*2 >= len(lines) || s.written > s.full+int64(swapGrowth)*bufferBytes(lines) {
		return s.snapshot(lines, noEOL, cursor)
	}
	var e enc
	e.raw([]byte{recLines})
	e.int(cursor.Line)
	e.int(cursor.Col)
	e.bool(noEOL)
	e.int(lead + 1)
	e.int(replaced)
	e.lines(lines[lead : lead+changed])
	if err := s.record(e.b); err != nil {
		return err
	}
	s.remember(lines, noEOL)
	return nil
}

// Saved records a ":w": the file on the disk is now what the buffer holds, so
// the swap file stops saying "modified: YES" without being rewritten.
func (s *Swap) Saved(meta SwapMeta) error {
	var e enc
	e.raw([]byte{recSaved})
	e.int64(meta.Size)
	e.int64(nanos(meta.Mtime))
	e.raw(meta.SHA[:])
	if err := s.record(e.b); err != nil {
		return err
	}
	s.saved = meta.SHA
	return nil
}

// Modified reports whether the swap file holds changes the file on the disk
// does not, which is the "modified: YES" line of the E325 message.
func (s *Swap) Modified() bool { return s.content != s.saved }

// Close closes the file and leaves it on the disk. Use Remove on a clean exit;
// this is for a caller that means to keep it.
func (s *Swap) Close() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// Remove closes the swap file and deletes it, which is what a clean quit does.
// A swap file left behind is an E325 on the next open, and the second-worst
// thing an editor can do to a person is cry wolf about a crash that did not
// happen.
func (s *Swap) Remove() error {
	err := s.Close()
	if rmErr := os.Remove(s.path); err == nil && !errors.Is(rmErr, os.ErrNotExist) {
		err = rmErr
	}
	return err
}

// SwapInfo is a swap file's header, plus what can be worked out about it from
// the outside: whether the process that made it is still running, and whether
// what it holds differs from the file on the disk.
type SwapInfo struct {
	Path     string
	Created  time.Time
	Mtime    time.Time // the swap file's own, which is vim's "dated:" line
	PID      int
	Host     string
	User     string
	File     string
	Size     int64
	FileTime time.Time
	SHA      [32]byte

	// Modified is whether the recovered contents differ from the file as the
	// last ":w" left it.
	Modified bool

	// Running is whether a process with this PID exists on this host. False
	// when the swap came from another machine, where the question cannot be
	// answered and vim does not ask it either.
	Running bool

	// Foreign is a swap file this pvim cannot read: vim's own, or one written
	// by a version with a format this one does not know. It is still a swap
	// file and it still raises E325 -- the whole point of the prompt is that
	// somebody else may be editing this file -- and Unreadable is the clause
	// vim puts in the message in its place.
	Foreign    bool
	Unreadable string
}

// Recovery is what a swap file's records add up to: the buffer as of the last
// sync before the process died.
type Recovery struct {
	Lines   [][]byte
	NoEOL   bool
	Cursor  Pos
	Records int

	// Truncated is a swap file whose last record was cut short, which is what
	// a kill in the middle of a write leaves. The records before it are
	// good and are what Lines holds; this says one sync's worth of typing was
	// lost, which is the honest thing to tell somebody who has just recovered
	// a file.
	Truncated bool
}

// ReadSwap reads a swap file: the header, and the records replayed into a
// buffer.
func ReadSwap(path string) (SwapInfo, Recovery, error) {
	p, err := os.ReadFile(path)
	if err != nil {
		return SwapInfo{}, Recovery{}, err
	}
	d, err := openHeader(p, magicSwap)
	if err != nil {
		return SwapInfo{}, Recovery{}, err
	}
	var i SwapInfo
	i.Path = path
	i.Created = unixNano(d.int64())
	i.PID = d.int()
	i.Host = d.str()
	i.User = d.str()
	i.File = d.str()
	i.Size = d.int64()
	i.FileTime = unixNano(d.int64())
	if sha := d.take(32); len(sha) == 32 {
		copy(i.SHA[:], sha)
	}
	if d.err != nil {
		return SwapInfo{}, Recovery{}, d.err
	}
	r, saved, err := replay(d)
	if err != nil {
		return SwapInfo{}, Recovery{}, err
	}
	i.Modified = digestLines(r.Lines, r.NoEOL) != saved
	i.Running = processRunning(i.Host, i.PID)
	if st, err := os.Stat(path); err == nil {
		i.Mtime = st.ModTime()
	}
	return i, r, nil
}

// replay walks a swap file's records. It stops at the first one that is short or whose
// CRC does not match and reports what it had, because that is the shape of a
// file whose writer was killed: everything before the last record is exactly
// what was synced.
func replay(d *dec) (Recovery, [32]byte, error) {
	var r Recovery
	var saved [32]byte
	first := true
	for len(d.b) > 0 {
		n, adv := binary.Uvarint(d.b)
		if adv <= 0 || uint64(len(d.b)-adv) < n+4 {
			r.Truncated = true
			break
		}
		payload := d.b[adv : adv+int(n)]
		sum := binary.BigEndian.Uint32(d.b[adv+int(n) : adv+int(n)+4])
		if crc32.ChecksumIEEE(payload) != sum {
			r.Truncated = true
			break
		}
		d.b = d.b[adv+int(n)+4:]
		rd := &dec{b: payload}
		kind := rd.take(1)
		if len(kind) != 1 {
			r.Truncated = true
			break
		}
		switch kind[0] {
		case recFull:
			r.Cursor.Line, r.Cursor.Col = rd.int(), rd.int()
			r.NoEOL = rd.bool()
			r.Lines = rd.lines()
			if first {
				saved = digestLines(r.Lines, r.NoEOL)
			}
		case recLines:
			r.Cursor.Line, r.Cursor.Col = rd.int(), rd.int()
			r.NoEOL = rd.bool()
			at, replaced := rd.int(), rd.int()
			put := rd.lines()
			if rd.err == nil {
				var err error
				if r.Lines, err = splice(r.Lines, at, replaced, put); err != nil {
					return Recovery{}, saved, err
				}
			}
		case recSaved:
			rd.int64()
			rd.int64()
			if sha := rd.take(32); len(sha) == 32 {
				copy(saved[:], sha)
			}
		default:
			return Recovery{}, saved, fmt.Errorf("%w: swap record kind %d", ErrCorrupt, kind[0])
		}
		if rd.err != nil {
			return Recovery{}, saved, rd.err
		}
		first = false
		r.Records++
	}
	if r.Records == 0 {
		return r, saved, fmt.Errorf("%w: swap file holds no complete record", ErrCorrupt)
	}
	return r, saved, nil
}

// splice applies one line-range replacement, refusing one that does not fit
// rather than growing the buffer to make it.
func splice(lines [][]byte, at, replaced int, put [][]byte) ([][]byte, error) {
	if at < 1 || replaced < 0 || at-1+replaced > len(lines) {
		return nil, fmt.Errorf("%w: swap record replaces %d lines at %d of %d",
			ErrCorrupt, replaced, at, len(lines))
	}
	out := make([][]byte, 0, len(lines)-replaced+len(put))
	out = append(out, lines[:at-1]...)
	out = append(out, put...)
	return append(out, lines[at-1+replaced:]...), nil
}

// Inspect answers what can be said about a swap file without trusting it,
// which is what the E325 prompt needs: a vim swap file, a truncated one and a
// file from a pvim that writes a newer format all have to produce a message
// rather than an error, because all three mean somebody may be editing this
// file right now.
func Inspect(path string) (SwapInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return SwapInfo{}, err
	}
	i, _, err := ReadSwap(path)
	if err == nil {
		return i, nil
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrCorrupt) ||
		errors.Is(err, ErrMagic) || errors.Is(err, ErrVersion) {
		out := SwapInfo{Path: path, Mtime: st.ModTime(), Foreign: true}
		out.Unreadable = "[does not look like a Vim swap file]"
		if errors.Is(err, ErrCorrupt) {
			out.Unreadable = "[cannot be read]"
		}
		out.User = statOwner(st)
		return out, nil
	}
	return SwapInfo{}, err
}

// processRunning reports whether the process that made a swap file is still
// there, which is what turns "an edit session crashed" into "another program is
// editing this file" and takes "(D)elete it" off the prompt.
//
// Only on this machine: a PID from another host names a process here that has
// nothing to do with the swap file, and answering with it would be worse than
// not answering. Vim's swapfile_process_running() makes the same check for the
// same reason.
func processRunning(host string, pid int) bool {
	if pid <= 0 || host == "" || host != hostName() {
		return false
	}
	err := syscall.Kill(pid, 0)
	// EPERM is a process that exists and belongs to somebody else, which is
	// still a process editing this file.
	return err == nil || errors.Is(err, syscall.EPERM)
}

// statOwner is the owning user of a file, as a name where one can be had and a
// uid where one cannot.
//
// os/user is not asked, deliberately. With CGO_ENABLED=0 on macOS it cannot
// resolve a uid at all -- users live in Directory Services and not in
// /etc/passwd -- so it would answer the same number after a lookup that reads a
// file for nothing. pvim's own swap files carry the user name in the header,
// which is where the common case gets a name from.
func statOwner(st os.FileInfo) string {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	if uint32(os.Getuid()) == sys.Uid {
		if u := userName(); u != "" {
			return u
		}
	}
	return strconv.FormatUint(uint64(sys.Uid), 10)
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// userName is $USER, or the numeric uid. See statOwner for why os/user is not
// in the way.
func userName() string {
	for _, name := range []string{"USER", "LOGNAME"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return strconv.Itoa(os.Getuid())
}

func digestLines(lines [][]byte, noEOL bool) [32]byte {
	return Digest(Join(lines, noEOL))
}

// Join is a run of lines as the bytes of a file: newline-terminated, unless the
// file had no final newline, which is the one thing every editor gets wrong
// once.
func Join(lines [][]byte, noEOL bool) []byte {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	out := make([]byte, 0, n)
	for i, l := range lines {
		out = append(out, l...)
		if i < len(lines)-1 || !noEOL {
			out = append(out, '\n')
		}
	}
	return out
}

func sameLines(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// commonEnds is how many lines two versions of a buffer share at each end,
// which is the whole of the diff a swap file needs: the middle is written out
// and the ends are already in the log.
func commonEnds(was, now [][]byte) (lead, trail int) {
	for lead < len(was) && lead < len(now) && bytes.Equal(was[lead], now[lead]) {
		lead++
	}
	for trail < len(was)-lead && trail < len(now)-lead &&
		bytes.Equal(was[len(was)-1-trail], now[len(now)-1-trail]) {
		trail++
	}
	return lead, trail
}

func bufferBytes(lines [][]byte) int64 {
	var n int64
	for _, l := range lines {
		n += int64(len(l)) + 1
	}
	return n
}
