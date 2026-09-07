package register

import "testing"

// TestRegType is the string the oracle diffs for every register on every case.
// A yank that is right in the buffer and linewise in the register fails here
// and nowhere else.
func TestRegType(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"charwise", Char([]byte("hello")), "v"},
		{"linewise", LineValue([]byte("hello")), "V"},
		{"blockwise", BlockValue(3, []byte("abc"), []byte("def")), "\x163"},
		// A register nothing has written has no type at all, which is not
		// the same as charwise and is thirty-odd lines of every state dump.
		{"never written", Value{}, ""},
		{"written and empty", Char([]byte("")), "v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.RegType(); got != tc.want {
				t.Errorf("RegType() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBytes is getreg()'s layout: a linewise value ends with a newline and a
// charwise one does not, which is the difference the state dump shows and the
// difference p acts on.
func TestBytes(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"one charwise line", Char([]byte("hello")), "hello"},
		{"charwise across a break", Char([]byte("lo"), []byte("wor")), "lo\nwor"},
		{"one linewise line", LineValue([]byte("hello")), "hello\n"},
		{"two linewise lines", LineValue([]byte("a"), []byte("b")), "a\nb\n"},
		{"empty", Value{}, ""},
		{"one blank line, linewise", LineValue([]byte("")), "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(tc.v.Bytes()); got != tc.want {
				t.Errorf("Bytes() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEmpty: a linewise value of one empty line is a blank line p will insert,
// not an empty register. Confusing the two is how p after dd on a blank line
// does nothing.
func TestEmpty(t *testing.T) {
	if !(Value{}).Empty() {
		t.Error("the zero Value is not empty")
	}
	if LineValue([]byte("")).Empty() {
		t.Error("a linewise blank line reports empty")
	}
}

// TestUnnamedName is 'clipboard' deciding which register "" really is. It is
// two booleans and one rule, and it is the one place a yank can end up on the
// pasteboard or fail to.
func TestUnnamedName(t *testing.T) {
	cases := []struct {
		opt  Options
		want byte
	}{
		{Options{}, Unnamed},
		{Options{Unnamed: true}, ClipboardStar},
		{Options{UnnamedPlus: true}, ClipboardPlus},
		{Options{Unnamed: true, UnnamedPlus: true}, ClipboardPlus},
	}
	for _, tc := range cases {
		f := NewFile()
		f.SetOptions(tc.opt)
		if got := f.UnnamedName(); got != tc.want {
			t.Errorf("SetOptions(%+v): UnnamedName = %q, want %q", tc.opt, got, tc.want)
		}
	}
}

// TestNames is the order the oracle's state dump walks, which comes straight
// from the trailer in cmd/oracle/script.go: the unnamed register, the search
// register, 0 through 9, a through z. Thirty-eight lines, in that order, or
// state.txt differs on every case.
func TestNames(t *testing.T) {
	got := Names()
	if len(got) != 38 {
		t.Fatalf("Names() has %d entries, want 38", len(got))
	}
	if got[0] != '"' || got[1] != '/' || got[2] != '0' || got[11] != '9' || got[12] != 'a' || got[37] != 'z' {
		t.Errorf("Names() = %q, want \" / 0-9 a-z in that order", got)
	}
}

// TestRegTypeOfAToEOLBlock: a block yanked with $ still prints a width, and
// the width is the widest line it took. Measured with CTRL-V j j $ y over
// "abcdefgh", "ab", "abcde" from column 1, which vim answers CTRL-V 7 for,
// and with a line holding a tab, where the width is display columns and not
// bytes.
func TestRegTypeOfAToEOLBlock(t *testing.T) {
	v := Value{
		Lines: [][]byte{[]byte("bcdefgh"), []byte("b"), []byte("bcde")},
		Type:  TypeBlock,
		Width: 7,
		ToEOL: true,
	}
	if got := v.RegType(); got != "\x167" {
		t.Errorf("RegType() = %q, want %q", got, "\x167")
	}

	tabbed := Value{
		Lines: [][]byte{[]byte("a\tb"), []byte("ab")},
		Type:  TypeBlock,
		Width: 9,
		ToEOL: true,
	}
	if got := tabbed.RegType(); got != "\x169" {
		t.Errorf("RegType() over a tab = %q, want %q", got, "\x169")
	}
}

// TestValid is the set of names a user may type after a quote, split by what
// vim did with each: an unknown name beeps and drops the quote, so the yy that
// followed still yanked into the unnamed register, while a known read-only one
// ate the y and yanked nothing.
func TestValid(t *testing.T) {
	const known = `"-_*+#=/.%:~`
	for _, name := range []byte(known) {
		if !Valid(name) {
			t.Errorf("Valid(%q) is false; vim knows the name", name)
		}
	}
	for _, name := range []byte("azAZ09") {
		if !Valid(name) {
			t.Errorf("Valid(%q) is false", name)
		}
	}
	for _, name := range []byte("!,] ^&") {
		if Valid(name) {
			t.Errorf("Valid(%q) is true; vim beeps at it", name)
		}
	}
}

// TestWritable is Valid minus the six vim fills itself. "/ is on the writable
// side of :let and not on the writable side of a yank, and this is the yank
// answer: "/yy in vim eats the y and yanks nothing.
func TestWritable(t *testing.T) {
	for _, name := range []byte(`"-_*+#az09`) {
		if !Writable(name) {
			t.Errorf("Writable(%q) is false", name)
		}
	}
	for _, name := range []byte("/.%:~=") {
		if Writable(name) {
			t.Errorf("Writable(%q) is true; a yank into it is refused", name)
		}
	}
}

// TestMotionForcesNumbered is :help quote_number's list of ten. Eight of them
// were measured one at a time, each as a delete of less than one line that
// still filled "1 and still shifted it; { and } cannot make a delete that
// small and are here on the help's word alone.
func TestMotionForcesNumbered(t *testing.T) {
	for _, m := range []byte("%()`/?nN{}") {
		if !MotionForcesNumbered(m) {
			t.Errorf("MotionForcesNumbered(%q) is false", m)
		}
	}
	for _, m := range []byte("wWbeEjkhl0$fFtT'gG") {
		if MotionForcesNumbered(m) {
			t.Errorf("MotionForcesNumbered(%q) is true; it is not on the list", m)
		}
	}
}
