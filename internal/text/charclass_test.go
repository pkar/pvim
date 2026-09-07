package text

import "testing"

// TestUTFClass is the half of vim's utf_class() this package owns: everything
// from 0x100 up.
//
// The row that matters is the one that made it shared. "日本語" and "の" are
// two classes, so vim's "*" over the first of them searches for the three
// ideographs and stops where the Hiragana begins; a search that widened by
// "is this a keyword character" takes the whole run.
func TestUTFClass(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"CJK ideograph", '日', 0x4e00},
		{"CJK ideograph again", '語', 0x4e00},
		{"Hiragana", 'の', 0x3040},
		{"Katakana", 'テ', 0x30a0},
		{"Hangul syllable", '가', 0xac00},
		{"ideographic comma", '、', ClassPunct},
		{"accented Latin", 'é', ClassWord},
		{"Cyrillic", 'д', ClassWord},
		{"en dash", '–', ClassPunct},
		{"emoji", '🙂', ClassEmoji},
		// The ideographic space sits one codepoint below vim's punctuation
		// row, which starts at 0x3001, so vim calls it a word character. Not
		// a transcription slip: it is what the table says and what "w" over
		// it does.
		{"ideographic space", '\u3000', ClassWord},
	}
	for _, tc := range cases {
		if got := UTFClass(tc.r); got != tc.want {
			t.Errorf("%s: UTFClass(%q) = %#x, want %#x", tc.name, tc.r, got, tc.want)
		}
	}
}

// TestUTFClassSeparatesScripts is the property the two callers rely on: a
// character is in the same class as its neighbours in the same script and in a
// different one from the script next to it.
func TestUTFClassSeparatesScripts(t *testing.T) {
	if UTFClass('日') != UTFClass('本') {
		t.Error("two ideographs are in different classes")
	}
	if UTFClass('語') == UTFClass('の') {
		t.Error("an ideograph and a Hiragana are in one class")
	}
	if UTFClass('の') == UTFClass('テ') {
		t.Error("Hiragana and Katakana are in one class")
	}
}
