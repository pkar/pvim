package text

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// corpusFiles are the shapes that break a naive buffer. Every one of them was a
// bug in some editor: the file with no final newline that grows one, the CRLF
// file that gets normalised on save, the file whose last line is blank and
// disappears.
//
// Seven of the eight shapes are here and bigCorpus is the eighth, the
// 40,000-line log; unicode.txt is a ninth nothing asked for and the
// display-column tests needed anyway. gosource.txt is a snapshot of
// internal/regex/translate.go and vimrc.txt a snapshot of the ~/.vimrc this
// editor exists to run: both are bytes here rather than read from the originals
// so that a round trip which starts failing is failing on the same input it
// passed on last year.
var corpusFiles = []string{
	"empty.txt",
	"noeol.txt",
	"crlf.txt",
	"wide.txt",
	"lastblank.txt",
	"unicode.txt",
	"gosource.txt",
	"vimrc.txt",
}

// readCorpus returns the bytes of a testdata file.
func readCorpus(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading corpus: %v", err)
	}
	return data
}

// bigCorpus builds the 40,000-line file in memory. It is generated rather than
// committed because 40k lines of filler in the repo is 40k lines nobody will
// ever read and one more thing for a grep to wade through.
func bigCorpus() []byte {
	var buf bytes.Buffer
	for i := 1; i <= 40000; i++ {
		buf.WriteString("line ")
		buf.WriteString(itoa(i))
		if i%7 == 0 {
			buf.WriteString("\twith a tab")
		}
		if i%101 == 0 {
			buf.WriteString(" 世界")
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// A buffer that cannot give back the bytes it was handed cannot be trusted with
// a file, and every corpus shape here is one that some editor has silently
// rewritten on save.
func TestReadBytesRoundTrip(t *testing.T) {
	for _, name := range corpusFiles {
		t.Run(name, func(t *testing.T) {
			in := readCorpus(t, name)
			out := Read(in).Bytes()
			if !bytes.Equal(in, out) {
				t.Errorf("round trip changed the file:\n in %q\nout %q", in, out)
			}
		})
	}
	t.Run("40k", func(t *testing.T) {
		in := bigCorpus()
		if got := Read(in).Bytes(); !bytes.Equal(in, got) {
			t.Errorf("40k-line round trip differs (%d bytes in, %d out)", len(in), len(got))
		}
	})
}

// The three flags Read has to infer, spelled out. These are the cases where a
// one-character mistake in the detector produces a file that looks fine until
// something else reads it.
func TestReadDetection(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		lines  []string
		format Format
		noEOL  bool
	}{
		// An empty file is vim's ML_EMPTY and not a line without an ending:
		// measured, typing into one and writing it gives "abc\n" even under
		// 'nofixendofline', so 'endofline' is on and Bytes is short because
		// the buffer is empty and not because a separator is missing.
		{"empty file is one empty line", "", []string{""}, Unix, false},
		{"a lone newline is one empty line with an ending", "\n", []string{""}, Unix, false},
		{"no final newline", "abc", []string{"abc"}, Unix, true},
		{"plain unix", "a\nb\n", []string{"a", "b"}, Unix, false},
		{"all crlf is dos", "a\r\nb\r\n", []string{"a", "b"}, DOS, false},
		{"dos without a final ending", "a\r\nb", []string{"a", "b"}, DOS, true},
		{"one stray unix ending makes it unix", "a\nb\r\nc\n", []string{"a", "b\r", "c"}, Unix, false},
		{"a carriage return in the middle is text", "a\n\rb\n", []string{"a", "\rb"}, Unix, false},
		{"blank last line", "a\n\n", []string{"a", ""}, Unix, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read([]byte(c.in))
			if b.LineCount() != len(c.lines) {
				t.Fatalf("LineCount() = %d, want %d", b.LineCount(), len(c.lines))
			}
			for i, want := range c.lines {
				if got := string(b.Line(i + 1)); got != want {
					t.Errorf("Line(%d) = %q, want %q", i+1, got, want)
				}
			}
			if b.Format() != c.format {
				t.Errorf("Format() = %v, want %v", b.Format(), c.format)
			}
			if b.NoEOL() != c.noEOL {
				t.Errorf("NoEOL() = %v, want %v", b.NoEOL(), c.noEOL)
			}
		})
	}
}

// Replace is the only mutation, so every shape of edit has to come out of it
// correctly or the wrappers built on top are all wrong in the same way.
func TestReplace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		r    Range
		with string
		want string
		end  Pos
	}{
		{"insert inside a line", "hello\n", Range{Pos{1, 5}, Pos{1, 5}}, " there", "hello there\n", Pos{1, 11}},
		{"delete inside a line", "hello\n", Range{Pos{1, 1}, Pos{1, 4}}, "", "ho\n", Pos{1, 1}},
		{"replace across two lines", "one\ntwo\n", Range{Pos{1, 1}, Pos{2, 1}}, "X", "oXwo\n", Pos{1, 2}},
		{"split a line", "onetwo\n", Range{Pos{1, 3}, Pos{1, 3}}, "\n", "one\ntwo\n", Pos{2, 0}},
		{"join two lines", "one\ntwo\n", Range{Pos{1, 3}, Pos{2, 0}}, "", "onetwo\n", Pos{1, 3}},
		{"delete a whole line", "a\nb\nc\n", Range{Pos{2, 0}, Pos{3, 0}}, "", "a\nc\n", Pos{2, 0}},
		{"insert two lines", "a\nb\n", Range{Pos{2, 0}, Pos{2, 0}}, "x\ny\n", "a\nx\ny\nb\n", Pos{4, 0}},
		{"delete everything", "a\nb\n", Range{Pos{1, 0}, Pos{2, 1}}, "", "\n", Pos{1, 0}},
		{"reversed range does nothing", "abc\n", Range{Pos{1, 2}, Pos{1, 0}}, "", "abc\n", Pos{1, 2}},
		{"range past the end is clamped", "abc\n", Range{Pos{1, 2}, Pos{9, 9}}, "Z", "abZ\n", Pos{1, 3}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read([]byte(c.in))
			end := b.Replace(c.r, []byte(c.with))
			if got := string(b.Bytes()); got != c.want {
				t.Errorf("buffer = %q, want %q", got, c.want)
			}
			if end != c.end {
				t.Errorf("end = %v, want %v", end, c.end)
			}
		})
	}
}

// The wrappers exist so call sites do not do line arithmetic. If they get it
// wrong the call sites are worse off than if they had done it themselves.
func TestLineHelpers(t *testing.T) {
	t.Run("InsertLines before the first", func(t *testing.T) {
		b := Read([]byte("a\nb\n"))
		b.InsertLines(1, [][]byte{[]byte("x")})
		if got := string(b.Bytes()); got != "x\na\nb\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("InsertLines in the middle", func(t *testing.T) {
		b := Read([]byte("a\nb\n"))
		b.InsertLines(2, [][]byte{[]byte("x"), []byte("y")})
		if got := string(b.Bytes()); got != "a\nx\ny\nb\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("InsertLines past the end", func(t *testing.T) {
		b := Read([]byte("a\nb\n"))
		b.InsertLines(3, [][]byte{[]byte("x")})
		if got := string(b.Bytes()); got != "a\nb\nx\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("InsertLines past the end of a file with no final newline", func(t *testing.T) {
		b := Read([]byte("a"))
		b.InsertLines(2, [][]byte{[]byte("x")})
		if got := string(b.Bytes()); got != "a\nx" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("DeleteLines in the middle", func(t *testing.T) {
		b := Read([]byte("a\nb\nc\n"))
		b.DeleteLines(2, 2)
		if got := string(b.Bytes()); got != "a\nc\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("DeleteLines to the end", func(t *testing.T) {
		b := Read([]byte("a\nb\nc\n"))
		b.DeleteLines(2, 3)
		if got := string(b.Bytes()); got != "a\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("DeleteLines everything leaves one empty line", func(t *testing.T) {
		b := Read([]byte("a\nb\nc\n"))
		b.DeleteLines(1, 3)
		if b.LineCount() != 1 || len(b.Line(1)) != 0 {
			t.Errorf("LineCount() = %d, Line(1) = %q; want one empty line", b.LineCount(), b.Line(1))
		}
	})
	t.Run("SetLine", func(t *testing.T) {
		b := Read([]byte("a\nb\n"))
		b.SetLine(2, []byte("zzz"))
		if got := string(b.Bytes()); got != "a\nzzz\n" {
			t.Errorf("got %q", got)
		}
	})
}

// Text is what a register gets filled from, and a register holds LF between its
// lines whatever the file on disk uses.
func TestText(t *testing.T) {
	b := Read([]byte("one\r\ntwo\r\nthree\r\n"))
	if b.Format() != DOS {
		t.Fatalf("expected a DOS buffer")
	}
	cases := []struct {
		r    Range
		want string
	}{
		{Range{Pos{1, 0}, Pos{1, 3}}, "one"},
		{Range{Pos{1, 1}, Pos{2, 2}}, "ne\ntw"},
		{Range{Pos{1, 0}, Pos{3, 0}}, "one\ntwo\n"},
		{Range{Pos{2, 1}, Pos{2, 1}}, ""},
	}
	for _, c := range cases {
		if got := string(b.Text(c.r)); got != c.want {
			t.Errorf("Text(%v) = %q, want %q", c.r, got, c.want)
		}
	}
}

// Line hands out the buffer's own slice for speed. The contract is that it
// stays valid until the next mutation; what must never happen is history
// aliasing live text, because then an undo hands back a slice something else is
// still writing through.
func TestUndoDoesNotAliasLiveLines(t *testing.T) {
	b := Read([]byte("hello\nworld\n"))
	b.OpenUndoBlock(Pos{1, 0})
	b.SetLine(1, []byte("HELLO"))
	b.CloseUndoBlock()

	live := b.Line(1)
	if string(live) != "HELLO" {
		t.Fatalf("Line(1) = %q", live)
	}
	live[0] = 'X' // the thing callers are told not to do
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() reported nothing to undo")
	}
	if got := string(b.Line(1)); got != "hello" {
		t.Errorf("after undo Line(1) = %q, want %q; history aliased a live line", got, "hello")
	}
}
