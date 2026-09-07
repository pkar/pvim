package clip

import (
	"reflect"
	"testing"

	"github.com/pkar/pvim/internal/register"
)

// The X11 selection wire formats, tested where they can be: over bytes, with no
// server and no other vim on the far end. What these cannot say is whether the
// layouts are the ones vim actually writes, because there is no Linux box here
// to ask. See the note at the top of x11sel.go and the manual checks for
// internal/gui/x11.

func TestVimTextRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   register.Value
	}{
		{"charwise one line", register.Char([]byte("alpha"))},
		{"charwise two lines", register.Char([]byte("alpha"), []byte("beta"))},
		{"linewise", register.LineValue([]byte("alpha"), []byte("beta"))},
		{"blockwise", register.BlockValue(4, []byte("abcd"), []byte("ef"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enc := range []struct {
				name   string
				encode func(register.Value) []byte
				decode func([]byte) (register.Value, bool)
			}{
				{"_VIM_TEXT", encodeVimText, decodeVimText},
				{"_VIMENC_TEXT", encodeVimEncText, decodeVimEncText},
			} {
				got, ok := enc.decode(enc.encode(tc.in))
				if !ok {
					t.Fatalf("%s: what this package wrote, it refused to read", enc.name)
				}
				if got.Type != tc.in.Type {
					t.Errorf("%s: type came back %s, want %s", enc.name, got.Type, tc.in.Type)
				}
				if !reflect.DeepEqual(got.Lines, tc.in.Lines) {
					t.Errorf("%s: lines came back %q, want %q", enc.name, got.Lines, tc.in.Lines)
				}
			}
		})
	}
}

// TestVimTextOfAnEmptyValueIsEmpty follows the darwin half rather than vim.
// vim answers V and one blank line for an empty string carrying MLINE; this
// package answers an empty register, for the reason valueOfMotion gives: a
// selection holding nothing is what "nothing has been copied" looks like, and
// "+p on a fresh login should do nothing rather than insert a blank line.
func TestVimTextOfAnEmptyValueIsEmpty(t *testing.T) {
	for _, in := range [][]byte{{0}, {1}} {
		v, ok := decodeVimText(in)
		if !ok {
			t.Fatalf("%v was refused", in)
		}
		if !v.Empty() {
			t.Errorf("%v decoded to %v, want an empty register", in, v)
		}
	}
}

// TestVimEncTextLayout is the byte layout written out, because a round trip
// through this package's own encoder would pass whatever the layout was. This
// is the shape vim's clipboard code writes, and it is the assertion that would
// have to change if the manual check says it is wrong.
func TestVimEncTextLayout(t *testing.T) {
	got := encodeVimEncText(register.LineValue([]byte("hi")))
	want := append([]byte{1}, append([]byte("utf-8\x00"), "hi\n"...)...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("encoded to %q, want %q: motion byte, encoding, NUL, text", got, want)
	}
	if got := encodeVimText(register.Char([]byte("hi"))); !reflect.DeepEqual(got, []byte("\x00hi")) {
		t.Errorf("_VIM_TEXT encoded to %q, want a motion byte then the text", got)
	}
}

// TestVimTextRefusesWhatItCannotBeSure is the half that matters more than the
// round trip: a decoder that guessed would put an encoding name or a control
// byte into the buffer.
func TestVimTextRefusesWhatItCannotBeSure(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"a motion vim does not define", []byte{7, 'h', 'i'}},
		{"MAUTO, which is not a motion here", []byte{255, 'h', 'i'}},
		{"a UTF-8 continuation byte first", []byte{0x80, 'h', 'i'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if v, ok := decodeVimText(tc.in); ok {
				t.Errorf("accepted %q as %v", tc.in, v)
			}
		})
	}
}

func TestVimEncTextRefusesWhatItCannotBeSure(t *testing.T) {
	long := append([]byte{0}, make([]byte, 64)...)
	for i := 1; i < len(long); i++ {
		long[i] = 'x'
	}
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"a motion byte alone", []byte{0}},
		{"a motion vim does not define", append([]byte{9}, "utf-8\x00hi"...)},
		{"an encoding this package cannot convert", append([]byte{0}, "latin1\x00hi"...)},
		{"no NUL at all", []byte{0, 'u', 't', 'f', '-', '8'}},
		{"an empty encoding name", []byte{0, 0, 'h', 'i'}},
		{"an encoding name longer than any vim writes", long},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if v, ok := decodeVimEncText(tc.in); ok {
				t.Errorf("accepted %q as %v", tc.in, v)
			}
		})
	}
}

// TestVimEncTextTakesEitherCase, because vim writes 'encoding' verbatim and a
// vimrc may have set it to UTF-8 with capitals.
func TestVimEncTextTakesEitherCase(t *testing.T) {
	in := append([]byte{1}, append([]byte("UTF-8\x00"), "hi\n"...)...)
	v, ok := decodeVimEncText(in)
	if !ok {
		t.Fatal("UTF-8 in capitals was refused")
	}
	if v.Type != register.TypeLine || string(v.Lines[0]) != "hi" {
		t.Errorf("decoded to %v", v)
	}
}

// TestChunkSizesAlwaysEndsEmpty pins the property an INCR transfer's receiver
// depends on: the last write is always the zero-length one that says the
// transfer is over, including when the data divided evenly.
func TestChunkSizesAlwaysEndsEmpty(t *testing.T) {
	for _, tc := range []struct {
		total, max int
		want       []int
	}{
		{0, 100, []int{0}},
		{50, 100, []int{50, 0}},
		{100, 100, []int{100, 0}},
		{250, 100, []int{100, 100, 50, 0}},
		{300, 100, []int{100, 100, 100, 0}},
	} {
		got := chunkSizes(tc.total, tc.max)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("chunkSizes(%d, %d) = %v, want %v", tc.total, tc.max, got, tc.want)
		}
		sum := 0
		for _, n := range got {
			sum += n
		}
		if sum != tc.total {
			t.Errorf("chunkSizes(%d, %d) covers %d bytes", tc.total, tc.max, sum)
		}
		if got[len(got)-1] != 0 {
			t.Errorf("chunkSizes(%d, %d) does not end with the terminator: %v", tc.total, tc.max, got)
		}
	}
	if got := chunkSizes(100, 0); got != nil {
		t.Errorf("chunkSizes with no room gave %v, want nothing", got)
	}
}

// TestChunkSizesOverAWholeBuffer is the large paste by name:
// a megabyte through a server whose maximum request length is the smallest the
// protocol allows.
func TestChunkSizesOverAWholeBuffer(t *testing.T) {
	const total, max = 1 << 20, 16360
	sizes := chunkSizes(total, max)
	sum := 0
	for i, n := range sizes {
		if n > max {
			t.Fatalf("chunk %d is %d bytes, over the %d-byte maximum", i, n, max)
		}
		sum += n
	}
	if sum != total {
		t.Errorf("the chunks cover %d bytes of %d", sum, total)
	}
	if sizes[len(sizes)-1] != 0 {
		t.Error("the transfer does not end with a zero-length write")
	}
}

func TestLatin1ToUTF8(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want string
	}{
		{[]byte("plain ascii"), "plain ascii"},
		{[]byte{'c', 'a', 'f', 0xe9}, "café"},
		{[]byte{0xff}, "ÿ"},
		{nil, ""},
	} {
		if got := string(latin1ToUTF8(tc.in)); got != tc.want {
			t.Errorf("latin1ToUTF8(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUTF8ToLatin1(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []byte
		ok   bool
	}{
		{"plain ascii", []byte("plain ascii"), true},
		{"café", []byte{'c', 'a', 'f', 0xe9}, true},
		{"ÿ", []byte{0xff}, true},
		{"Ā", nil, false}, // U+0100, the first code point Latin-1 cannot hold
		{"☺", nil, false},
		{"", []byte{}, true},
	} {
		got, ok := utf8ToLatin1([]byte(tc.in))
		if ok != tc.ok {
			t.Errorf("utf8ToLatin1(%q) said %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("utf8ToLatin1(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestLatin1RoundTrips over every byte, because the two functions are inverses
// over the whole range or they are not worth having.
func TestLatin1RoundTrips(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	back, ok := utf8ToLatin1(latin1ToUTF8(all))
	if !ok {
		t.Fatal("a Latin-1 byte string did not survive the trip to UTF-8 and back")
	}
	if !reflect.DeepEqual(back, all) {
		t.Errorf("the round trip changed the bytes")
	}
}
