package substitute

import (
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// TestParseFlagsToggle. The letters toggle rather than set, which is vim and
// which nobody expects: ":s/a/b/gg" is not global and ":s/a/b/ggg" is.
func TestParseFlagsToggle(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"", false}, {"g", true}, {"gg", false}, {"ggg", true},
	} {
		f, err := ParseFlags(c.in, Flags{}, false)
		if err != nil {
			t.Fatalf("ParseFlags(%q): %v", c.in, err)
		}
		if f.All != c.want {
			t.Errorf("ParseFlags(%q).All = %v, want %v", c.in, f.All, c.want)
		}
	}
}

// TestParseFlagsStartsAtGdefault. 'gdefault' inverts what a bare ":s" means,
// which is why All is a field and not a letter count.
func TestParseFlagsStartsAtGdefault(t *testing.T) {
	f, _ := ParseFlags("", Flags{}, true)
	if !f.All {
		t.Error("with 'gdefault' a bare :s is global and this one is not")
	}
	f, _ = ParseFlags("g", Flags{}, true)
	if f.All {
		t.Error("with 'gdefault' the g flag turns global off and this one did not")
	}
}

// TestParseFlagsAmpersandInherits. The "&" flag takes the previous flags and
// the letters after it toggle from there, so ":s/a/b/&g" after a global one is
// not global. The "n" flag is the one thing it never inherits, because vim
// resets it on every command.
func TestParseFlagsAmpersandInherits(t *testing.T) {
	prev := Flags{All: true, Confirm: true, CountOnly: true, Count: 4}
	f, err := ParseFlags("&", prev, false)
	if err != nil {
		t.Fatal(err)
	}
	if !f.All || !f.Confirm {
		t.Errorf("the & flag dropped the previous flags: %+v", f)
	}
	if f.CountOnly {
		t.Error("the & flag inherited the n flag, which vim resets")
	}
	if f.Count != 0 {
		t.Error("the & flag inherited the previous count")
	}
	f, _ = ParseFlags("&g", prev, false)
	if f.All {
		t.Error("the g after an & set global instead of toggling it off")
	}
}

// TestParseFlagsTrailing is E488, with the text vim prints after it.
func TestParseFlagsTrailing(t *testing.T) {
	for _, c := range []struct{ in, rest string }{
		{"zz", "zz"},
		{"3g", "g"}, // a flag after the count
		{"g&", "&"}, // an & that is not first
		{"q", "q"},
	} {
		_, err := ParseFlags(c.in, Flags{}, false)
		te, ok := err.(TrailingError)
		if !ok {
			t.Fatalf("ParseFlags(%q) gave %v, want E488", c.in, err)
		}
		if te.Rest != c.rest {
			t.Errorf("ParseFlags(%q) says %q, want %q", c.in, te.Rest, c.rest)
		}
	}
	if got := (TrailingError{Rest: "zz"}).Error(); got != "E488: Trailing characters: zz" {
		t.Errorf("E488 reads %q", got)
	}
}

// TestParseFlagsCount reads the trailing count, with or without the space.
func TestParseFlagsCount(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{"", 0}, {"3", 3}, {" 3", 3}, {"g 12", 12}, {"g3", 3}, {"3 ", 3},
	} {
		f, err := ParseFlags(c.in, Flags{}, false)
		if err != nil {
			t.Fatalf("ParseFlags(%q): %v", c.in, err)
		}
		if f.Count != c.want {
			t.Errorf("ParseFlags(%q).Count = %d, want %d", c.in, f.Count, c.want)
		}
	}
}

// TestParseDelimiters. Anything that is not a letter, a digit, a double quote
// or a bar delimits a ":s" pattern, and "&" and "#" are the two that look like
// they should not.
func TestParseDelimiters(t *testing.T) {
	st := &State{}
	o := options.Defaults()
	for _, in := range []string{"/a/X/", "#a#X#", ",a,X,", "&a&X&", ":a:X:", "+a+X+"} {
		c, err := Parse(KindSubstitute, in, st, &o)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if c.Pattern != "a" || c.Replacement != "X" || !c.HavePattern {
			t.Errorf("Parse(%q) = %+v", in, c)
		}
	}
}

// TestParseKeepsEscapes. The backslash before a delimiter stays in the text:
// internal/regex wants the pattern as typed and Expand wants the replacement
// the same way.
func TestParseKeepsEscapes(t *testing.T) {
	c, err := Parse(KindSubstitute, `/a\/b/X\/Y/g`, &State{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Pattern != `a\/b` || c.Replacement != `X\/Y` || !c.Flags.All {
		t.Errorf("parsed as %+v", c)
	}
}

// TestParseRepeatFormsTakeNoPattern.
//
// ":&" and ":~" are repeats by definition, so ":&/a/Y/" is not a substitution
// with a new pattern, it is E488 with "/a/Y/" in it. Measured, because both
// read like abbreviations of ":s" and are not.
func TestParseRepeatFormsTakeNoPattern(t *testing.T) {
	st := &State{Replacement: "X", HaveReplacement: true, SubPattern: "a"}
	for _, kind := range []Kind{KindAmpersand, KindTilde} {
		_, err := Parse(kind, "/a/Y/", st, nil)
		te, ok := err.(TrailingError)
		if !ok || te.Rest != "/a/Y/" {
			t.Errorf("kind %d gave %v, want E488 on /a/Y/", kind, err)
		}
	}
}

// TestParseRepeatFormFailsBeforeItsFlags.
//
// ":scacXc" with no previous ":s" is E33 and the same command after one is
// E488: vim decides it is a repeat, finds nothing to repeat, and stops before
// it looks at the letters. Measured both ways round.
func TestParseRepeatFormFailsBeforeItsFlags(t *testing.T) {
	if _, err := Parse(KindSubstitute, "cacXc", &State{}, nil); err != ErrNoPrevSub {
		t.Errorf("with no previous :s the answer is %v, want E33", err)
	}
	st := &State{Replacement: "X", HaveReplacement: true, SubPattern: "a"}
	_, err := Parse(KindSubstitute, "cacXc", st, nil)
	if te, ok := err.(TrailingError); !ok || te.Rest != "acXc" {
		t.Errorf("with a previous :s the answer is %v, want E488 on acXc", err)
	}
}

// TestParseNormal keeps every byte of its argument except the leading
// whitespace: a bar does not end the command and a double quote does not start
// a comment, so ":normal ia|ib" inserts "a|ib". ":normal ix" inserts "x" and
// not " x", which is the one thing about this command worth a test of its own.
func TestParseNormal(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ia|ib", "ia|ib"},
		{"  ix", "ix"},
		{`i"x`, `i"x`},
		{"\tdd", "dd"},
	} {
		n, err := ParseNormal(c.in, false)
		if err != nil {
			t.Fatalf("ParseNormal(%q): %v", c.in, err)
		}
		if n.Keys != c.want {
			t.Errorf("ParseNormal(%q).Keys = %q, want %q", c.in, n.Keys, c.want)
		}
		if n.NoRemap {
			t.Error("NoRemap set without a bang")
		}
	}
	if n, err := ParseNormal("dd", true); err != nil || !n.NoRemap {
		t.Errorf("ParseNormal with a bang: %+v %v", n, err)
	}
	if _, err := ParseNormal("   ", false); err != ErrArgRequired {
		t.Errorf("an empty :normal gave %v, want E471", err)
	}
	if got := ErrArgRequired.Error(); got != "E471: Argument required" {
		t.Errorf("E471 reads %q", got)
	}
	if Terminator != 0x1b {
		t.Errorf("the :normal terminator is %#x, want Escape", Terminator)
	}
}
