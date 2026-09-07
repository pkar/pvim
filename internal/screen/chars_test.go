package screen

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

func TestCharsSpellsWhatVimSpells(t *testing.T) {
	cases := []struct {
		name string
		line string
		ts   int
		want []string // one entry per character: its Text, or its rune
	}{
		{"plain", "abc", 8, []string{"a", "b", "c"}},
		{"tab", "a\tb", 4, []string{"a", "\t", "b"}},
		{"control", "a\x01b", 8, []string{"a", "^A", "b"}},
		{"del", "a\x7f", 8, []string{"a", "^?"}},
		{"c1 control", "a\u0085", 8, []string{"a", "<85>"}},
		{"format control", "a\u200bb", 8, []string{"a", "<200b>", "b"}},
		{"wide", "a\u4e00", 8, []string{"a", "\u4e00"}},
		{"combining", "e\u0301x", 8, []string{"e", "x"}},
		{"invalid byte", "a\xffb", 8, []string{"a", "<ff>", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Chars([]byte(c.line), c.ts)
			if len(got) != len(c.want) {
				t.Fatalf("%d characters, want %d: %+v", len(got), len(c.want), got)
			}
			for i, ch := range got {
				s := ch.Text
				if s == "" {
					s = string(ch.Rune)
				}
				if s != c.want[i] {
					t.Errorf("character %d is %q, want %q", i, s, c.want[i])
				}
			}
		})
	}
}

func TestCharsWidths(t *testing.T) {
	cases := []struct {
		line string
		ts   int
		cols []int // the display column each character starts at
		last int   // the width of the whole line
	}{
		{"ab\tc", 4, []int{0, 1, 2, 4}, 5},
		{"\t\t", 4, []int{0, 4}, 8},
		{"a\tb", 8, []int{0, 1, 8}, 9},
		{"\u4e00\u4e8c", 8, []int{0, 2}, 4},
		{"\x01\x01", 8, []int{0, 2}, 4},
		{"\u200b", 8, []int{0}, 6},
	}
	for _, c := range cases {
		got := Chars([]byte(c.line), c.ts)
		if len(got) != len(c.cols) {
			t.Fatalf("%q: %d characters, want %d", c.line, len(got), len(c.cols))
		}
		for i, ch := range got {
			if ch.Col != c.cols[i] {
				t.Errorf("%q: character %d starts at column %d, want %d", c.line, i, ch.Col, c.cols[i])
			}
		}
		if n := LineWidth([]byte(c.line), c.ts); n != c.last {
			t.Errorf("%q: LineWidth = %d, want %d", c.line, n, c.last)
		}
	}
}

// The one rule this package and internal/text both carry, held to agree.
//
// internal/text computes display columns so a cursor lands where vim's does;
// this package computes them so a cell holds what vim's holds. They are the
// same arithmetic written twice, and the day they disagree is the day a motion
// puts the caret one cell away from the character it is on. The corpus below
// is every shape that has gone wrong here: tabs at every offset, wide runes,
// combining marks, controls and the format controls.
//
// Not invalid bytes. The two packages disagree there and this test is not the
// place to settle it: vim's utf_ptr2cells() returns 4 for a byte that starts
// no valid sequence, because it draws one as <ff>, and that is what Chars
// does; internal/text gives it one cell. It could not be measured against vim
// either way, because vim reads a file with a stray 0xff in it as latin-1 and
// converts, and nr2char() will not put a raw byte in a buffer, so no buffer
// vim will hold has one. Left as a difference on the record rather than
// asserted in either direction.
func TestWidthAgreesWithText(t *testing.T) {
	corpus := []string{
		"", "a", "abc", "\t", "a\tb", "\ta", "ab\tcd", "\t\t\t",
		"\u4e00\u4e8c\u4e09", "a\u4e00b\u4e8cc", "e\u0301", "a\u0301\u0302b",
		"\x01\x02\x7f", "\u0080\u009f", "\u200b\ufeff",
		"    indented", "\tmixed \ttabs\t", strings.Repeat("\u4e00", 20),
		"\u65e5\u672c\u8a9e", "\U0001f642 emoji \U0001f642",
	}
	for _, ts := range []int{1, 2, 4, 8} {
		for _, line := range corpus {
			b := []byte(line)
			if got, wantW := LineWidth(b, ts), text.DisplayWidth(b, ts); got != wantW {
				t.Errorf("ts=%d %q: screen says %d cells, internal/text says %d", ts, line, got, wantW)
			}
			for _, ch := range Chars(b, ts) {
				if got := text.DisplayCol(b, ch.Byte, ts); got != ch.Col {
					t.Errorf("ts=%d %q byte %d: screen column %d, internal/text column %d",
						ts, line, ch.Byte, ch.Col, got)
				}
			}
		}
	}
}

// The same cross-check over random bytes, because the corpus above is only the
// shapes somebody thought of.
func TestWidthAgreesWithTextOnRandomBytes(t *testing.T) {
	rng := rand.New(rand.NewSource(20260904))
	for i := 0; i < 2000; i++ {
		n := rng.Intn(24)
		var b []byte
		for j := 0; j < n; j++ {
			switch rng.Intn(5) {
			case 0:
				b = append(b, '\t')
			case 1:
				b = utf8.AppendRune(b, rune(0x4e00+rng.Intn(200)))
			case 2:
				b = utf8.AppendRune(b, rune(0x300+rng.Intn(0x40)))
			case 3:
				b = append(b, byte(rng.Intn(0x20)))
			default:
				b = append(b, byte(0x20+rng.Intn(0x60)))
			}
		}
		ts := 1 << rng.Intn(4)
		if got, wantW := LineWidth(b, ts), text.DisplayWidth(b, ts); got != wantW {
			t.Fatalf("ts=%d %q: screen says %d cells, internal/text says %d", ts, b, got, wantW)
		}
	}
}
