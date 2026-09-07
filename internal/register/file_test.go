package register

import (
	"errors"
	"strings"
	"testing"
)

// Every want in this file was copied out of a state dump from
// /opt/homebrew/bin/vim 9.2, driven by
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE
//
// on the five-line buffer named in setup below, with a trailer that appends
// getregtype(r) and getreg(r) for each register to a file. The keys column of
// each case is the script that produced it. Nothing here is what vim ought to
// do; it is what it did.

// The buffer every case below was measured against.
const (
	line1 = "alpha beta gamma"
	line2 = "second line here"
)

// dump renders registers the way the oracle's state.txt does, so a want in a
// table can be pasted straight out of a vim run.
func dump(t *testing.T, f *File, names string) string {
	t.Helper()

	var b strings.Builder
	for _, r := range []byte(names) {
		v, err := f.Get(r)
		if err != nil {
			t.Fatalf("Get(%q): %v", r, err)
		}
		b.WriteString("reg ")
		b.WriteByte(r)
		b.WriteString("\t" + v.RegType() + "\t" + escape(string(v.Bytes())) + "\n")
	}
	return b.String()
}

// escape is the trailer's quoting rule: backslash, newline, carriage return
// and tab, and nothing else.
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s)
}

// lines makes a value's line slice from strings, because a table of [][]byte
// literals is unreadable.
func chars(s ...string) Value { return Char(bytesOf(s)...) }
func lineV(s ...string) Value { return LineValue(bytesOf(s)...) }
func blockV(width int, s ...string) Value {
	return BlockValue(width, bytesOf(s)...)
}

func bytesOf(s []string) [][]byte {
	out := make([][]byte, len(s))
	for i := range s {
		out[i] = []byte(s[i])
	}
	return out
}

// TestScripts replays what the mode machine will do for a keystroke script and
// asserts the registers vim was left holding.
func TestScripts(t *testing.T) {
	cases := []struct {
		keys  string
		names string
		run   func(f *File)
		want  []string
	}{
		{
			keys:  "yy",
			names: `"-01`,
			run:   func(f *File) { f.Yank(0, lineV(line1)) },
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg -\t\t",
				"reg 0\tV\talpha beta gamma\\n",
				"reg 1\t\t",
			},
		},
		{
			// A yank into a named register leaves "0 alone, whatever :help
			// quote0 makes it sound like.
			keys:  `"ayy`,
			names: `"-01a`,
			run:   func(f *File) { f.Yank('a', lineV(line1)) },
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\t\t",
				"reg a\tV\talpha beta gamma\\n",
			},
		},
		{
			keys:  "dd",
			names: `"-012`,
			run:   func(f *File) { f.Delete(0, lineV(line1), false) },
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\tV\talpha beta gamma\\n",
				"reg 2\t\t",
			},
		},
		{
			keys:  "dd j dd",
			names: `"123`,
			run: func(f *File) {
				f.Delete(0, lineV(line1), false)
				f.Delete(0, lineV("third line here"), false)
			},
			want: []string{
				"reg \"\tV\tthird line here\\n",
				"reg 1\tV\tthird line here\\n",
				"reg 2\tV\talpha beta gamma\\n",
				"reg 3\t\t",
			},
		},
		{
			keys:  "x",
			names: `"-01`,
			run:   func(f *File) { f.Delete(0, chars("a"), false) },
			want: []string{
				"reg \"\tv\ta",
				"reg -\tv\ta",
				"reg 0\t\t",
				"reg 1\t\t",
			},
		},
		{
			// A named delete still shifts and still fills "1.
			keys:  `"add`,
			names: `"-01a`,
			run:   func(f *File) { f.Delete('a', lineV(line1), false) },
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\tV\talpha beta gamma\\n",
				"reg a\tV\talpha beta gamma\\n",
			},
		},
		{
			// ... and a named small delete does not fill "-.
			keys:  `"ax`,
			names: `"-01a`,
			run:   func(f *File) { f.Delete('a', chars("a"), false) },
			want: []string{
				"reg \"\tv\ta",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\t\t",
				"reg a\tv\ta",
			},
		},
		{
			// The exception this package exists for: a delete of less than one
			// line made with a search motion writes "1 as well as "-.
			keys:  `d/beta<CR>`,
			names: `"-012`,
			run:   func(f *File) { f.Delete(0, chars("alpha "), true) },
			want: []string{
				"reg \"\tv\talpha ",
				"reg -\tv\talpha ",
				"reg 0\t\t",
				"reg 1\tv\talpha ",
				"reg 2\t\t",
			},
		},
		{
			// ... and it shifts, so the earlier dd is still reachable in "2.
			keys:  `dd d/line<CR>`,
			names: `"-12`,
			run: func(f *File) {
				f.Delete(0, lineV(line1), false)
				f.Delete(0, chars("second "), true)
			},
			want: []string{
				"reg \"\tv\tsecond ",
				"reg -\tv\tsecond ",
				"reg 1\tv\tsecond ",
				"reg 2\tV\talpha beta gamma\\n",
			},
		},
		{
			// Naming "1 writes it and the shift then moves that same text into
			// "2, so both hold the line.
			keys:  `"1dd`,
			names: `"123`,
			run:   func(f *File) { f.Delete('1', lineV(line1), false) },
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg 1\tV\talpha beta gamma\\n",
				"reg 2\tV\talpha beta gamma\\n",
				"reg 3\t\t",
			},
		},
		{
			// The same thing over an earlier delete loses it: the named write
			// overwrites "1 before the shift copies it up.
			keys:  `dd "1dd`,
			names: `"123`,
			run: func(f *File) {
				f.Delete(0, lineV(line1), false)
				f.Delete('1', lineV(line2), false)
			},
			want: []string{
				"reg \"\tV\tsecond line here\\n",
				"reg 1\tV\tsecond line here\\n",
				"reg 2\tV\tsecond line here\\n",
				"reg 3\t\t",
			},
		},
		{
			keys:  `dd "2dd`,
			names: `"123`,
			run: func(f *File) {
				f.Delete(0, lineV(line1), false)
				f.Delete('2', lineV(line2), false)
			},
			want: []string{
				"reg \"\tV\tsecond line here\\n",
				"reg 1\tV\tsecond line here\\n",
				"reg 2\tV\talpha beta gamma\\n",
				"reg 3\tV\tsecond line here\\n",
			},
		},
		{
			keys:  `"0dd`,
			names: `"01`,
			run:   func(f *File) { f.Delete('0', lineV(line1), false) },
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg 0\tV\talpha beta gamma\\n",
				"reg 1\tV\talpha beta gamma\\n",
			},
		},
		{
			// A bare quote counts as naming a register: the character goes to
			// "0 and "- stays empty.
			keys:  `""x`,
			names: `"-01`,
			run:   func(f *File) { f.Delete(Unnamed, chars("a"), false) },
			want: []string{
				"reg \"\tv\ta",
				"reg -\t\t",
				"reg 0\tv\ta",
				"reg 1\t\t",
			},
		},
		{
			keys:  `yy "_dd`,
			names: `"-01`,
			run: func(f *File) {
				f.Yank(0, lineV(line1))
				f.Delete(BlackHole, lineV(line1), false)
			},
			want: []string{
				"reg \"\tV\talpha beta gamma\\n",
				"reg -\t\t",
				"reg 0\tV\talpha beta gamma\\n",
				"reg 1\t\t",
			},
		},
		{
			keys:  "CTRL-V j l y",
			names: `"0`,
			run:   func(f *File) { f.Yank(0, blockV(2, "al", "se")) },
			want: []string{
				"reg \"\t\x162\tal\\nse",
				"reg 0\t\x162\tal\\nse",
			},
		},
		{
			// A one-line block delete is a small delete; a two-line one is not.
			keys:  "CTRL-V l d",
			names: `"-1`,
			run:   func(f *File) { f.Delete(0, blockV(2, "al"), false) },
			want: []string{
				"reg \"\t\x162\tal",
				"reg -\t\x162\tal",
				"reg 1\t\t",
			},
		},
		{
			keys:  "CTRL-V j l d",
			names: `"-1`,
			run:   func(f *File) { f.Delete(0, blockV(2, "al", "se"), false) },
			want: []string{
				"reg \"\t\x162\tal\\nse",
				"reg -\t\t",
				"reg 1\t\x162\tal\\nse",
			},
		},
		{
			// An appending delete shifts the numbered registers like any
			// other delete, but leaves the unnamed alias on the register it
			// appended to rather than moving it to "1: vim's
			// shift_delete_registers skips its y_previous assignment when
			// y_append is set. "" is the appended pair and "1 is only the
			// second line.
			keys:  `"ayyj"Add`,
			names: `"-012a`,
			run: func(f *File) {
				f.Yank('a', lineV(line1))
				f.Delete('A', lineV(line2), false)
			},
			want: []string{
				"reg \"\tV\talpha beta gamma\\nsecond line here\\n",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\tV\tsecond line here\\n",
				"reg 2\t\t",
				"reg a\tV\talpha beta gamma\\nsecond line here\\n",
			},
		},
		{
			// The same, with a delete rather than a yank filling "a first, so
			// the shift has something to move into "2 and the alias still
			// does not follow it.
			keys:  `"addj"Add`,
			names: `"-012a`,
			run: func(f *File) {
				f.Delete('a', lineV(line1), false)
				f.Delete('A', lineV(line2), false)
			},
			want: []string{
				"reg \"\tV\talpha beta gamma\\nsecond line here\\n",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\tV\tsecond line here\\n",
				"reg 2\tV\talpha beta gamma\\n",
				"reg a\tV\talpha beta gamma\\nsecond line here\\n",
			},
		},
		{
			// An appending delete of less than a line shifts nothing, and "-
			// stays empty because a register was named.
			keys:  `"aylj"Adl`,
			names: `"-012a`,
			run: func(f *File) {
				f.Yank('a', chars("a"))
				f.Delete('A', chars("s"), false)
			},
			want: []string{
				"reg \"\tv\tas",
				"reg -\t\t",
				"reg 0\t\t",
				"reg 1\t\t",
				"reg 2\t\t",
				"reg a\tv\tas",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			f := NewFile()
			tc.run(f)
			want := strings.Join(tc.want, "\n") + "\n"
			if got := dump(t, f, tc.names); got != want {
				t.Errorf("after %s:\ngot:\n%swant:\n%s", tc.keys, got, want)
			}
		})
	}
}

// TestShiftDropsNine walks eleven deletes past a nine-slot shift, which is the
// vim run over a twelve-line file: "1 holds the last line deleted and "9 the
// ninth from last, and the two before that are gone.
func TestShiftDropsNine(t *testing.T) {
	f := NewFile()
	for i := 1; i <= 11; i++ {
		f.Delete(0, lineV("l"+string(rune('0'+i%10))), false)
	}
	want := []string{
		"reg 1\tV\tl1\\n",
		"reg 2\tV\tl0\\n",
		"reg 3\tV\tl9\\n",
		"reg 4\tV\tl8\\n",
		"reg 5\tV\tl7\\n",
		"reg 6\tV\tl6\\n",
		"reg 7\tV\tl5\\n",
		"reg 8\tV\tl4\\n",
		"reg 9\tV\tl3\\n",
	}
	if got := dump(t, f, "123456789"); got != strings.Join(want, "\n")+"\n" {
		t.Errorf("after eleven dd:\ngot:\n%swant:\n%s", got, strings.Join(want, "\n")+"\n")
	}
}

// TestUnnamedIsAnAlias is the surprise in :help quote_quote, and the reason
// the unnamed register is a pointer here and not a copy. Each case writes some
// register after the yank or delete that pointed the alias at it, and the
// write has to show through "".
func TestUnnamedIsAnAlias(t *testing.T) {
	cases := []struct {
		keys string
		run  func(f *File)
		want string
	}{
		{`"ayy then :let @a = 'XYZ'`, func(f *File) {
			f.Yank('a', lineV(line1))
			f.Set('a', chars("XYZ"))
		}, "XYZ"},
		{`dd then :let @1 = 'XYZ'`, func(f *File) {
			f.Delete(0, lineV(line1), false)
			f.Set('1', chars("XYZ"))
		}, "XYZ"},
		{`x then :let @- = 'XYZ'`, func(f *File) {
			f.Delete(0, chars("a"), false)
			f.Set(SmallDelete, chars("XYZ"))
		}, "XYZ"},
		{`yy then :let @0 = 'Z'`, func(f *File) {
			f.Yank(0, lineV(line1))
			f.Set('0', chars("Z"))
		}, "Z"},
		// A write to a register the alias does not point at is invisible
		// through it.
		{`dd then :let @b = 'BBB'`, func(f *File) {
			f.Delete(0, lineV(line1), false)
			f.Set('b', chars("BBB"))
		}, line1 + "\n"},
		// The alias is not followed when writing "": vim resolves it to "0.
		{`"ayy then :let @" = 'Q'`, func(f *File) {
			f.Yank('a', lineV(line1))
			f.Set(Unnamed, chars("Q"))
		}, "Q"},
		// A small delete after a linewise one moves the alias to "-.
		{`dd then x`, func(f *File) {
			f.Delete(0, lineV(line1), false)
			f.Delete(0, chars("s"), false)
		}, "s"},
		// A delete that fills both "1 and "- leaves the alias on "-.
		{`d/beta<CR> then :let @- = 'Z'`, func(f *File) {
			f.Delete(0, chars("alpha "), true)
			f.Set(SmallDelete, chars("Z"))
		}, "Z"},
		// ... and a named linewise delete leaves it on "1, not on the name.
		{`"add then :let @1 = 'Z'`, func(f *File) {
			f.Delete('a', lineV(line1), false)
			f.Set('1', chars("Z"))
		}, "Z"},
		// ... but an APPENDING named linewise delete leaves it on the name,
		// because vim's shift_delete_registers moves y_previous only when it
		// was not appending. Both halves measured: the :let @a shows through
		// "" and the :let @1 does not.
		{`"ayyj"Add then :let @a = 'XYZ'`, func(f *File) {
			f.Yank('a', lineV(line1))
			f.Delete('A', lineV(line2), false)
			f.Set('a', chars("XYZ"))
		}, "XYZ"},
		{`"ayyj"Add then :let @1 = 'XYZ'`, func(f *File) {
			f.Yank('a', lineV(line1))
			f.Delete('A', lineV(line2), false)
			f.Set('1', chars("XYZ"))
		}, line1 + "\n" + line2 + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			f := NewFile()
			tc.run(f)
			v, err := f.Get(Unnamed)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := string(v.Bytes()); got != tc.want {
				t.Errorf("@\" = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnnamedBeforeAnythingIsYanked: with no yank behind it the alias points
// nowhere and "" reads "0, which is what makes :let @" = x land in "0 and read
// back.
func TestUnnamedBeforeAnythingIsYanked(t *testing.T) {
	f := NewFile()
	if f.Previous() != 0 {
		t.Errorf("Previous() = %q on a new file, want 0", f.Previous())
	}
	v, err := f.Get(Unnamed)
	if err != nil || !v.Empty() {
		t.Errorf("Get(unnamed) = %q, %v, want empty and no error", v.Bytes(), err)
	}
	if err := f.Set(Unnamed, chars("ZZZ")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := dump(t, f, `"0`); got != "reg \"\tv\tZZZ\nreg 0\tv\tZZZ\n" {
		t.Errorf("after :let @\" = 'ZZZ':\n%s", got)
	}
}

// TestAppendOnYank is the "A rule, one case per combination of old and new
// type, each measured with two yanks over abcdefg / hijklmn / opqrstu.
func TestAppendOnYank(t *testing.T) {
	cases := []struct {
		keys     string
		old, new Value
		want     string
		wantType string
	}{
		{`yy "Ayy`, lineV("abcdefg"), lineV("opqrstu"), "abcdefg\\nopqrstu\\n", "V"},
		{`yl "Ayl`, chars("a"), chars("o"), "ao", "v"},
		{`yy "Ayw`, lineV("abcdefg"), chars("opq"), "abcdefg\\nopq\\n", "V"},
		{`yw "Ayy`, chars("abc"), lineV("opqrstu"), "abc\\nopqrstu\\n", "V"},
		{`CTRL-V l y "A CTRL-V l l y`, blockV(2, "ab"), blockV(3, "opq"), "ab\\nopq", "\x162"},
		{`CTRL-V l l y "A CTRL-V l y`, blockV(3, "abc"), blockV(2, "op"), "abc\\nop", "\x163"},
		{`CTRL-V l y "Ayw`, blockV(2, "ab"), chars("opqrstu"), "ab\\nopqrstu", "\x162"},
		{`yl "A CTRL-V l y`, chars("a"), blockV(2, "op"), "aop", "v"},
		{`CTRL-V l y "Ayy`, blockV(2, "ab"), lineV("opqrstu"), "ab\\nopqrstu\\n", "V"},
		{`yy "A CTRL-V l y`, lineV("abcdefg"), blockV(2, "op"), "abcdefg\\nop\\n", "V"},
		// Appending to a register nothing has written is a plain write.
		{`"Ayy`, Value{}, lineV("abcdefg"), "abcdefg\\n", "V"},
	}

	for _, tc := range cases {
		t.Run(tc.keys, func(t *testing.T) {
			got := tc.old.AppendYank(tc.new)
			if s := escape(string(got.Bytes())); s != tc.want {
				t.Errorf("content = %q, want %q", s, tc.want)
			}
			if s := got.RegType(); s != tc.wantType {
				t.Errorf("regtype = %q, want %q", s, tc.wantType)
			}
		})
	}
}

// TestAppendOnSet is the other append rule, the one :let @A and setreg('A')
// follow. The new type wins outright and the merge asks about the old type
// alone, which is why a charwise register appended with a linewise string
// comes out linewise and joined.
func TestAppendOnSet(t *testing.T) {
	cases := []struct {
		script   string
		old, new Value
		want     string
		wantType string
	}{
		{`yy then :let @A = 'APP'`, lineV("alpha beta gamma"), chars("APP"), "alpha beta gamma\\nAPP", "v"},
		{`yw then :let @A = 'APP'`, chars("alpha "), chars("APP"), "alpha APP", "v"},
		{`yw then :let @A = "L1\nL2"`, chars("alpha "), chars("L1", "L2"), "alpha L1\\nL2", "v"},
		{`yw then :let @A = "X\n"`, chars("alpha "), lineV("X"), "alpha X\\n", "V"},
		{`setreg a one l, setreg A two l`, lineV("one"), lineV("two"), "one\\ntwo\\n", "V"},
		{`setreg a one l, setreg A two c`, lineV("one"), chars("two"), "one\\ntwo", "v"},
		{`:let @A = 'APP' on an empty register`, Value{}, chars("APP"), "APP", "v"},
	}

	for _, tc := range cases {
		t.Run(tc.script, func(t *testing.T) {
			got := tc.old.AppendSet(tc.new)
			if s := escape(string(got.Bytes())); s != tc.want {
				t.Errorf("content = %q, want %q", s, tc.want)
			}
			if s := got.RegType(); s != tc.wantType {
				t.Errorf("regtype = %q, want %q", s, tc.wantType)
			}
		})
	}
}

// TestUppercaseAppendsThroughTheFile checks the same rule where a caller meets
// it, on File, and that the lowercase name is what actually holds the text.
func TestUppercaseAppendsThroughTheFile(t *testing.T) {
	f := NewFile()
	f.Yank('a', lineV(line1))
	f.Yank('A', lineV(line2))

	want := "reg \"\tV\talpha beta gamma\\nsecond line here\\n\n" +
		"reg a\tV\talpha beta gamma\\nsecond line here\\n\n"
	if got := dump(t, f, `"a`); got != want {
		t.Errorf("after \"ayy j \"Ayy:\ngot:\n%swant:\n%s", got, want)
	}
}

// TestBlackHole: writes vanish, reads are empty and typed, and nothing else
// moves. "_dd is how vim users delete without disturbing the registers and an
// implementation that shifts anyway breaks the one thing it is for.
func TestBlackHole(t *testing.T) {
	f := NewFile()
	f.Yank(0, lineV(line1))
	before := f.Previous()

	if err := f.Delete(BlackHole, lineV(line2), false); err != nil {
		t.Fatalf("Delete into the black hole: %v", err)
	}
	if err := f.Yank(BlackHole, lineV(line2)); err != nil {
		t.Fatalf("Yank into the black hole: %v", err)
	}
	if err := f.Set(BlackHole, chars("x")); err != nil {
		t.Fatalf("Set on the black hole: %v", err)
	}
	if f.Previous() != before {
		t.Errorf("the black hole moved the unnamed alias to %q", f.Previous())
	}

	v, err := f.Get(BlackHole)
	if err != nil {
		t.Fatalf("Get(black hole): %v", err)
	}
	// Measured: getregtype('_') is "v" even though the register is empty.
	if v.RegType() != "v" || len(v.Bytes()) != 0 {
		t.Errorf("Get(black hole) = %q type %q, want empty and \"v\"", v.Bytes(), v.RegType())
	}
	if got := dump(t, f, `"01`); got != "reg \"\tV\talpha beta gamma\\n\nreg 0\tV\talpha beta gamma\\n\nreg 1\t\t\n" {
		t.Errorf("the black hole disturbed the registers:\n%s", got)
	}
}

// TestTextRegisters covers the five vim fills itself. The type matters as much
// as the content: "/ prints as charwise on every case in the oracle's dump,
// whether or not anything has been searched for.
func TestTextRegisters(t *testing.T) {
	f := NewFile()
	if got := dump(t, f, "/"); got != "reg /\tv\t\n" {
		t.Errorf("a fresh search register dumps as %q, want charwise and empty", got)
	}

	f.SetLastSearch("beta")
	f.SetLastCommand("wq!")
	f.SetLastInsert([]byte("one\ntwo"))
	f.SetFilename("f.txt", "other.txt")

	for _, tc := range []struct {
		name     byte
		want     string
		wantType string
	}{
		{LastSearch, "beta", "v"},
		{LastCommand, "wq!", "v"},
		{LastInsert, "one\ntwo", "v"},
		{Filename, "f.txt", "v"},
		{AltFilename, "other.txt", "v"},
		// "~ is the one that is untyped when empty, and pvim has no drag and
		// drop to fill it with.
		{Dropped, "", ""},
	} {
		v, err := f.Get(tc.name)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.name, err)
		}
		if got := string(v.Bytes()); got != tc.want {
			t.Errorf("@%c = %q, want %q", tc.name, got, tc.want)
		}
		if got := v.RegType(); got != tc.wantType {
			t.Errorf("getregtype(%q) = %q, want %q", tc.name, got, tc.wantType)
		}
	}
}

// TestWritesThatRefuse: the names vim answers E354 for, and the one it does
// not. :let @/ = 'pat' is legal and :let @. = 'x' is not.
func TestWritesThatRefuse(t *testing.T) {
	for _, tc := range []struct {
		name byte
		want error
	}{
		{LastInsert, ErrReadOnly},
		{LastCommand, ErrReadOnly},
		{Filename, ErrReadOnly},
		{AltFilename, ErrReadOnly},
		{Dropped, ErrReadOnly},
		{Expression, ErrUnsupported},
		{'!', ErrBadName},
		{',', ErrBadName},
		{']', ErrBadName},
		{LastSearch, nil},
		{'a', nil},
		{'7', nil},
		{Unnamed, nil},
		{SmallDelete, nil},
		{BlackHole, nil},
	} {
		if err := NewFile().Set(tc.name, chars("x")); !errors.Is(err, tc.want) {
			t.Errorf("Set(%q) = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestYankAndDeleteRefuse: a yank into a read-only register is the E354 that
// eats the y in "/yy and yanks nothing at all.
func TestYankAndDeleteRefuse(t *testing.T) {
	for _, name := range []byte{LastSearch, LastInsert, Filename, LastCommand, Dropped, Expression} {
		f := NewFile()
		if err := f.Yank(name, lineV(line1)); err == nil {
			t.Errorf("Yank(%q) succeeded, want a refusal", name)
		}
		if err := f.Delete(name, lineV(line1), false); err == nil {
			t.Errorf("Delete(%q) succeeded, want a refusal", name)
		}
		if got := dump(t, f, `"01`); got != "reg \"\t\t\nreg 0\t\t\nreg 1\t\t\n" {
			t.Errorf("a refused yank into %q still wrote something:\n%s", name, got)
		}
	}
}

// TestExpressionRegisterIsRefused holds the one deliberate hole open. A
// silent empty read would look like an expression that evaluated to nothing.
func TestExpressionRegisterIsRefused(t *testing.T) {
	if _, err := NewFile().Get(Expression); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Get(=) = %v, want ErrUnsupported", err)
	}
}

// TestUnknownNames: a name vim beeps at.
func TestUnknownNames(t *testing.T) {
	f := NewFile()
	for _, name := range []byte{'!', ',', ']', ' ', '^', 0x1b} {
		if _, err := f.Get(name); !errors.Is(err, ErrBadName) {
			t.Errorf("Get(%q) = %v, want ErrBadName", name, err)
		}
	}
}

// TestHashResolvesToRegisterZero is vim's default arm of the register table,
// which is only visible through a delete: "#dd leaves the line in "0, where no
// delete ever puts anything otherwise.
func TestHashResolvesToRegisterZero(t *testing.T) {
	f := NewFile()
	f.Delete(AltFilename, lineV(line1), false)

	want := "reg \"\tV\talpha beta gamma\\n\nreg 0\tV\talpha beta gamma\\n\nreg 1\tV\talpha beta gamma\\n\n"
	if got := dump(t, f, `"01`); got != want {
		t.Errorf("after \"#dd:\ngot:\n%swant:\n%s", got, want)
	}
}

// fakeClip is a Clipboard in a struct, which is the whole reason Clipboard is
// an interface: no window server in a test.
type fakeClip struct {
	v      Value
	writes int
	err    error
}

func (c *fakeClip) Read() (Value, error) { return c.v, c.err }
func (c *fakeClip) Write(v Value) error {
	if c.err != nil {
		return c.err
	}
	c.v = v
	c.writes++
	return nil
}

// TestClipboardMirrors is 'clipboard' set the way ~/.vimrc sets it. Measured
// with clipboard=unnamed,unnamedplus: yy fills "0 and the pasteboard, dd fills
// "1 and the pasteboard, "ayy fills neither, and getreg('"') keeps answering
// the yank rather than the pasteboard.
func TestClipboardMirrors(t *testing.T) {
	clip := &fakeClip{}
	f := NewFile()
	f.SetClipboard(clip)
	f.SetOptions(Options{Unnamed: true, UnnamedPlus: true})

	f.Yank(0, lineV(line1))
	if clip.writes != 1 || string(clip.v.Bytes()) != line1+"\n" {
		t.Errorf("after yy the clipboard holds %q after %d writes, want the line after one", clip.v.Bytes(), clip.writes)
	}
	f.Delete(0, chars("a"), false)
	if string(clip.v.Bytes()) != "a" {
		t.Errorf("after x the clipboard holds %q, want %q", clip.v.Bytes(), "a")
	}

	writes := clip.writes
	f.Yank('a', lineV(line2))
	if clip.writes != writes {
		t.Error(`"ayy wrote the clipboard; a named yank leaves it alone`)
	}

	// An outside application copies something. getreg('"') does not see it and
	// p does, which is the split UnnamedName documents.
	clip.v = chars("EXTERNAL")
	v, _ := f.Get(Unnamed)
	if string(v.Bytes()) != line2+"\n" {
		t.Errorf(`@" = %q, want the last yank; getreg does not read the clipboard`, v.Bytes())
	}
	p, _ := f.ForPut(0)
	if string(p.Bytes()) != "EXTERNAL" {
		t.Errorf("p pastes %q, want the clipboard", p.Bytes())
	}
}

// TestClipboardWithoutAFrontend: headless, with 'clipboard' set, "* and "+ are
// ordinary registers. The oracle runs this way, and it is what makes yy then p
// agree with vim on a machine whose pasteboard nobody has touched.
func TestClipboardWithoutAFrontend(t *testing.T) {
	f := NewFile()
	f.SetOptions(Options{Unnamed: true, UnnamedPlus: true})
	f.Yank(0, lineV(line1))

	for _, name := range []byte{ClipboardStar, ClipboardPlus} {
		v, err := f.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if string(v.Bytes()) != line1+"\n" {
			t.Errorf("@%c = %q, want the yanked line", name, v.Bytes())
		}
	}
	p, _ := f.ForPut(0)
	if string(p.Bytes()) != line1+"\n" {
		t.Errorf("p pastes %q, want the yanked line", p.Bytes())
	}
}

// TestClipboardErrorsTravel: a pasteboard call can fail, and a put that
// silently pastes nothing because of it is worse than an error on the message
// line.
func TestClipboardErrorsTravel(t *testing.T) {
	boom := errors.New("pasteboard is busy")
	f := NewFile()
	f.SetClipboard(&fakeClip{err: boom})

	if _, err := f.Get(ClipboardStar); !errors.Is(err, boom) {
		t.Errorf("Get(*) = %v, want the clipboard's error", err)
	}
	if err := f.Yank(ClipboardPlus, lineV(line1)); !errors.Is(err, boom) {
		t.Errorf("Yank(+) = %v, want the clipboard's error", err)
	}
}

// TestValuesDoNotShare: a Value handed out by Get must not be a window into
// the file. Appending to what Get returned used to grow the register itself,
// because both sides held the same slice header.
func TestValuesDoNotShare(t *testing.T) {
	f := NewFile()
	f.Yank('a', lineV(line1))

	v, _ := f.Get('a')
	v.Lines = append(v.Lines, []byte("intruder"))
	v.Lines[0] = []byte("clobbered")

	got, _ := f.Get('a')
	if string(got.Bytes()) != line1+"\n" {
		t.Errorf("@a = %q after a caller edited what Get returned", got.Bytes())
	}
}

// TestSmallDeleteFollowsClipboard: a delete into "* or "+ still fills "- when
// 'clipboard' had already claimed that register, because typing it asks for
// the register the delete was going to write anyway.
//
// Measured on /opt/homebrew/bin/vim 9.2.0321 driven through a pty, over the
// one-line buffer "alpha beta" with the cursor at column 0. -i NONE matters:
// without it vim restores "- from ~/.viminfo and the control cases read false.
// The keys column is the script; wantAlias was read back by setting the
// candidate register with :let afterwards and asking what @" then said.
func TestSmallDeleteFollowsClipboard(t *testing.T) {
	cases := []struct {
		keys      string
		opt       Options
		name      byte
		v         Value
		useRegOne bool
		wantSmall string
		wantAlias byte
	}{
		{
			keys:      `:set clipboard=unnamed,unnamedplus,autoselect | "*x`,
			opt:       Options{Unnamed: true, UnnamedPlus: true},
			name:      ClipboardStar,
			v:         chars("a"),
			wantSmall: "a",
			wantAlias: SmallDelete,
		},
		{
			keys:      `:set clipboard=unnamed,unnamedplus,autoselect | "+x`,
			opt:       Options{Unnamed: true, UnnamedPlus: true},
			name:      ClipboardPlus,
			v:         chars("a"),
			wantSmall: "a",
			wantAlias: SmallDelete,
		},
		{
			// dw is charwise and within the line, so the same rule catches it.
			keys:      `:set clipboard=unnamed,unnamedplus,autoselect | "*dw`,
			opt:       Options{Unnamed: true, UnnamedPlus: true},
			name:      ClipboardStar,
			v:         chars("alpha "),
			wantSmall: "alpha ",
			wantAlias: SmallDelete,
		},
		{
			// A search motion shifts the numbered registers as well, and the
			// alias still ends on "-: measured, "*d/beta<CR> leaves "1 and "-
			// both holding "alpha " and :let @- = 'XYZ' afterwards shows
			// through @".
			keys:      `:set clipboard=unnamed,unnamedplus,autoselect | "*d/beta<CR>`,
			opt:       Options{Unnamed: true, UnnamedPlus: true},
			name:      ClipboardStar,
			v:         chars("alpha "),
			useRegOne: true,
			wantSmall: "alpha ",
			wantAlias: SmallDelete,
		},
		{
			// Each half of 'clipboard' only excuses its own register.
			keys:      `:set clipboard=unnamed | "+x`,
			opt:       Options{Unnamed: true},
			name:      ClipboardPlus,
			v:         chars("a"),
			wantSmall: "",
			wantAlias: ClipboardPlus,
		},
		{
			keys:      `:set clipboard=unnamedplus | "*x`,
			opt:       Options{UnnamedPlus: true},
			name:      ClipboardStar,
			v:         chars("a"),
			wantSmall: "",
			wantAlias: ClipboardStar,
		},
		{
			keys:      `:set clipboard= | "*x`,
			name:      ClipboardStar,
			v:         chars("a"),
			wantSmall: "",
			wantAlias: ClipboardStar,
		},
		{
			// An ordinary named register is never excused.
			keys:      `:set clipboard=unnamed,unnamedplus,autoselect | "ax`,
			opt:       Options{Unnamed: true, UnnamedPlus: true},
			name:      'a',
			v:         chars("a"),
			wantSmall: "",
			wantAlias: 'a',
		},
		{
			// The rule is only for a delete of less than one line, whatever
			// 'clipboard' says.
			keys:      `:set clipboard=unnamed,unnamedplus,autoselect | "*dd`,
			opt:       Options{Unnamed: true, UnnamedPlus: true},
			name:      ClipboardStar,
			v:         lineV("alpha beta"),
			wantSmall: "",
			wantAlias: '1',
		},
	}

	for _, c := range cases {
		t.Run(c.keys, func(t *testing.T) {
			f := NewFile()
			f.SetOptions(c.opt)
			if err := f.Delete(c.name, c.v, c.useRegOne); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			small, err := f.Get(SmallDelete)
			if err != nil {
				t.Fatalf("Get(-): %v", err)
			}
			if got := string(small.Bytes()); got != c.wantSmall {
				t.Errorf(`@- = %q, want %q`, got, c.wantSmall)
			}
			if got := f.Previous(); got != c.wantAlias {
				t.Errorf(`@" aliases %q, want %q`, got, c.wantAlias)
			}

			// The typed register is written either way; the clipboard rule
			// adds "- rather than moving the text somewhere else.
			named, err := f.Get(c.name)
			if err != nil {
				t.Fatalf("Get(%q): %v", c.name, err)
			}
			if string(named.Bytes()) != string(c.v.Bytes()) {
				t.Errorf("@%c = %q, want the deleted text %q", c.name, named.Bytes(), c.v.Bytes())
			}
		})
	}
}

// TestSmallDeleteIntoClipboardIsPastable is what the bug cost, stated as the
// sequence that showed it: with the vimrc's own 'clipboard', "*x then "-p
// pastes the deleted character in vim and pasted nothing here.
func TestSmallDeleteIntoClipboardIsPastable(t *testing.T) {
	f := NewFile()
	f.SetOptions(Options{Unnamed: true, UnnamedPlus: true})
	f.Delete(ClipboardStar, chars("a"), false)

	v, err := f.Get(SmallDelete)
	if err != nil {
		t.Fatalf("Get(-): %v", err)
	}
	if got := string(v.Bytes()); got != "a" {
		t.Errorf(`"*x then "-p pastes %q, want %q`, got, "a")
	}
}
