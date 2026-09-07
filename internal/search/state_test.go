package search

import (
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// smartOptions is 'ignorecase' and 'smartcase' together, which is the only
// combination 'smartcase' does anything in and the one the real vimrc sets.
func smartOptions() Options {
	opt := DefaultOptions()
	opt.IgnoreCase, opt.SmartCase = true, true
	return opt
}

// TestStarRemembersNoSmartCase: * turns 'smartcase' off for its own search and
// vim keeps that decision beside the pattern, in spat_T's no_scs, so every n,
// N and bare / that reuses the pattern stays case-insensitive. Without it the
// n after a * compiles \<Foo\> under 'smartcase', the capital makes it
// case-sensitive, and it walks past the two lowercase matches to wrap round
// onto the line the * started from.
func TestStarRemembersNoSmartCase(t *testing.T) {
	b := text.Read([]byte(bStarCase))

	s := &State{}
	if _, err := s.Word(b, text.Pos{Line: 1}, Forward, true, 1, smartOptions()); err != nil {
		t.Fatalf("star: %v", err)
	}
	if !s.NoSmartCase {
		t.Error("star did not remember that 'smartcase' was off for it")
	}

	res, err := s.Next(b, text.Pos{Line: 2}, false, 1, smartOptions())
	if err != nil {
		t.Fatalf("n after a star: %v", err)
	}
	if res.Pos.Line != 3 || res.Wrapped {
		t.Errorf("n after a star = %v wrapped=%v, want line 3 and no wrap", res.Pos, res.Wrapped)
	}

	// A bare / reuses the pattern, and vim restores no_scs every time it does.
	res, err = s.Do(b, text.Pos{Line: 2}, Forward, "", 1, smartOptions())
	if err != nil {
		t.Fatalf("bare / after a star: %v", err)
	}
	if res.Pos.Line != 3 || res.Wrapped {
		t.Errorf("bare / after a star = %v wrapped=%v, want line 3 and no wrap", res.Pos, res.Wrapped)
	}
	if !s.NoSmartCase {
		t.Error("a bare / dropped the star's no-smartcase")
	}
}

// TestTypedPatternBringsSmartCaseBack is the other half of the rule, and the
// reason the flag lives on the state rather than in the options: a pattern off
// the keyboard is compiled under whatever 'smartcase' says now, so the capital
// in /Foo makes it case-sensitive again even though the * before it did not.
func TestTypedPatternBringsSmartCaseBack(t *testing.T) {
	b := text.Read([]byte(bStarCase))
	s := &State{}
	if _, err := s.Word(b, text.Pos{Line: 1}, Forward, true, 1, smartOptions()); err != nil {
		t.Fatalf("star: %v", err)
	}

	res, err := s.Do(b, text.Pos{Line: 2}, Forward, "Foo", 1, smartOptions())
	if err != nil {
		t.Fatalf("/Foo after a star: %v", err)
	}
	if s.NoSmartCase {
		t.Error("a typed pattern kept the star's no-smartcase")
	}
	if res.Pos.Line != 1 || !res.Wrapped {
		t.Errorf("/Foo after a star = %v wrapped=%v, want line 1 and a wrap", res.Pos, res.Wrapped)
	}
}

// TestUncompilablePatternIsRemembered: vim stores the pattern before it tries
// to compile it, so "/ holds a pattern nobody can compile and the n after it
// raises the same error again and does not move. Keeping the pattern from
// before instead makes n run the old search and land somewhere plausible and
// wrong.
func TestUncompilablePatternIsRemembered(t *testing.T) {
	b := text.Read([]byte("foo bar\nbaz foo\nfoo x\n"))
	s := &State{}
	if _, err := s.Do(b, text.Pos{Line: 1}, Forward, "foo", 1, DefaultOptions()); err != nil {
		t.Fatalf("/foo: %v", err)
	}

	const bad = `\(bar`
	if _, err := s.Do(b, text.Pos{Line: 2, Col: 4}, Forward, bad, 1, DefaultOptions()); err == nil {
		t.Fatalf("/%s compiled", bad)
	}
	if s.Pattern != bad {
		t.Errorf("@/ = %q, want %q", s.Pattern, bad)
	}
	if s.Re != nil {
		t.Error("a pattern that would not compile left a compiled pattern behind")
	}

	res, err := s.Next(b, text.Pos{Line: 2, Col: 4}, false, 1, DefaultOptions())
	if err == nil {
		t.Fatal("n after a pattern that would not compile ran a search")
	}
	if res.Pos.Line != 0 {
		t.Errorf("n moved the cursor to %v", res.Pos)
	}
}
