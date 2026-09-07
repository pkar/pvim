package textobj

// This file is vim's utf_class(), transcribed.
//
// Above Latin-1 vim does not sort characters into word and punctuation: it
// gives each script a class of its own, numbered by the first codepoint of the
// script, so that a run of Hiragana and the run of CJK ideographs beside it are
// two words and "iw" on either takes one of them. The numbers are arbitrary and
// only ever compared for equality, which is why they are vim's codepoints and
// not 4, 5, 6.
//
// It is a copy of the table in internal/motion and not a shared one because the
// layering in internal/deps_test.go lets internal/textobj import internal/text
// and internal/register and nothing else, and a text object may not ask a
// motion anything. Both are transcriptions of the same table in vim's
// mbyte.c: the day one of them is wrong, the other one is wrong the same way,
// and the fix goes in both.
//
// Without it "iw" on 日 in "日本語のテキスト" took the whole line where vim
// takes 日本語, and pvim disagreed with itself, since "dw" already used the
// motion package's copy of this table and stopped in the right place.

// classEmoji is what vim gives every emoji, so that a run of them is one word
// whatever scripts they sit between. The other three classes are in cursor.go
// and are vim's 0, 1 and 2.
const classEmoji = 3

// clinterval is one row of vim's utf_class() table.
type clinterval struct {
	first, last rune
	class       int
}

// utfClasses is vim 9.2's utf_class() table, in its own order, which is sorted
// and non-overlapping so that a binary search works. Anything not in it and not
// an emoji is a word character, which is why Cyrillic, Greek, Hebrew and the
// accented Latin letters need no rows.
var utfClasses = [...]clinterval{
	{0x037e, 0x037e, 1}, // Greek question mark
	{0x0387, 0x0387, 1}, // Greek ano teleia
	{0x055a, 0x055f, 1}, // Armenian punctuation
	{0x0589, 0x0589, 1}, // Armenian full stop
	{0x05be, 0x05be, 1},
	{0x05c0, 0x05c0, 1},
	{0x05c3, 0x05c3, 1},
	{0x05f3, 0x05f4, 1},
	{0x060c, 0x060c, 1},
	{0x061b, 0x061b, 1},
	{0x061f, 0x061f, 1},
	{0x066a, 0x066d, 1},
	{0x06d4, 0x06d4, 1},
	{0x0700, 0x070d, 1},
	{0x0964, 0x0965, 1},
	{0x0970, 0x0970, 1},
	{0x0df4, 0x0df4, 1},
	{0x0e4f, 0x0e4f, 1},
	{0x0e5a, 0x0e5b, 1},
	{0x0f04, 0x0f12, 1},
	{0x0f3a, 0x0f3d, 1},
	{0x0f85, 0x0f85, 1},
	{0x104a, 0x104f, 1},
	{0x10fb, 0x10fb, 1},
	{0x1361, 0x1368, 1},
	{0x166d, 0x166e, 1},
	{0x1680, 0x1680, 0},
	{0x169b, 0x169c, 1},
	{0x16eb, 0x16ed, 1},
	{0x1735, 0x1736, 1},
	{0x17d4, 0x17dc, 1},
	{0x1800, 0x180a, 1},
	{0x2000, 0x200b, 0},
	{0x2010, 0x2027, 1},
	{0x2030, 0x205e, 1},
	{0x207d, 0x207e, 1},
	{0x208d, 0x208e, 1},
	{0x2212, 0x2212, 1},
	{0x2768, 0x2775, 1},
	{0x27e6, 0x27ef, 1},
	{0x2983, 0x2998, 1},
	{0x29d8, 0x29db, 1},
	{0x29fc, 0x29fd, 1},
	{0x2e00, 0x2e7f, 1},
	{0x3001, 0x3020, 1},      // ideographic punctuation
	{0x3030, 0x3030, 1},      // wavy dash
	{0x303d, 0x303d, 1},      // part alternation mark
	{0x3040, 0x309f, 0x3040}, // Hiragana
	{0x30a0, 0x30ff, 0x30a0}, // Katakana
	{0x3300, 0x9fff, 0x4e00}, // CJK Ideographs
	{0xac00, 0xd7a3, 0xac00}, // Hangul Syllables
	{0xf900, 0xfaff, 0x4e00}, // CJK Ideographs
	{0xfd3e, 0xfd3f, 1},
	{0xfe30, 0xfe6b, 1},        // punctuation forms
	{0xff00, 0xff0f, 1},        // half/fullwidth ASCII
	{0xff1a, 0xff20, 1},        // half/fullwidth ASCII
	{0xff3b, 0xff40, 1},        // half/fullwidth ASCII
	{0xff5b, 0xff65, 1},        // half/fullwidth ASCII
	{0x20000, 0x2a6df, 0x4e00}, // CJK Ideographs
	{0x2a700, 0x2b73f, 0x4e00}, // CJK Ideographs
	{0x2b740, 0x2b81f, 0x4e00}, // CJK Ideographs
	{0x2f800, 0x2fa1f, 0x4e00}, // CJK Ideographs
}

// emojiRanges is the reduction of vim's emoji_all table this package carries:
// the blocks whose members are emoji from end to end. Vim's own table is the
// Unicode emoji data file with several hundred rows and the difference between
// the two only shows on a codepoint that is an emoji in the middle of a block
// of things that are not, where this reports a word character and vim reports
// class 3. Both are one class, so iw over a run of either agrees; only a
// boundary between such a character and a letter differs.
var emojiRanges = [...]clinterval{
	{0x1f000, 0x1f2ff, classEmoji},
	{0x1f300, 0x1f9ff, classEmoji},
	{0x1fa70, 0x1faff, classEmoji},
	{0x2600, 0x27bf, classEmoji},
	{0x2b00, 0x2bff, classEmoji},
}

// utfClass is vim's utf_class(): the class of one character.
func utfClass(r rune, kw *keywords) int {
	if r < 0x100 {
		if r == ' ' || r == '\t' || r == 0 || r == 0xa0 {
			return clsBlank
		}
		if kw.is(r) {
			return clsWord
		}
		return clsPunct
	}
	for _, e := range emojiRanges {
		if r >= e.first && r <= e.last {
			return classEmoji
		}
	}
	lo, hi := 0, len(utfClasses)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r > utfClasses[mid].last:
			lo = mid + 1
		case r < utfClasses[mid].first:
			hi = mid - 1
		default:
			return utfClasses[mid].class
		}
	}
	return clsWord
}
