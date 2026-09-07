package search

import "testing"

// TestOffsetKinds is the rule that makes d/foo/e delete the match and d/foo
// stop in front of it: an end offset makes the search motion inclusive, a line
// offset makes it linewise, and the other two change nothing about the kind.
func TestOffsetKinds(t *testing.T) {
	cases := []struct {
		off       Offset
		inclusive bool
		linewise  bool
	}{
		{Offset{}, false, false},
		{Offset{Kind: OffsetEnd}, true, false},
		{Offset{Kind: OffsetEnd, N: -1}, true, false},
		{Offset{Kind: OffsetLine, N: 3}, false, true},
		{Offset{Kind: OffsetStart, N: 2}, false, false},
	}
	for _, tc := range cases {
		if got := tc.off.Inclusive(); got != tc.inclusive {
			t.Errorf("%+v Inclusive = %v, want %v", tc.off, got, tc.inclusive)
		}
		if got := tc.off.Linewise(); got != tc.linewise {
			t.Errorf("%+v Linewise = %v, want %v", tc.off, got, tc.linewise)
		}
	}
}

// TestDirection: N is Reverse and nothing else, and the direction prints as
// the character that opened the command line.
func TestDirection(t *testing.T) {
	if Forward.Reverse() != Backward || Backward.Reverse() != Forward {
		t.Error("Reverse does not swap the two directions")
	}
	if Forward.String() != "/" || Backward.String() != "?" {
		t.Errorf("String = %q and %q, want / and ?", Forward, Backward)
	}
}

// TestDefaultOptions: 'wrapscan' is on by default, which is the difference
// between n wrapping round the file and E385.
func TestDefaultOptions(t *testing.T) {
	if !DefaultOptions().WrapScan {
		t.Error("DefaultOptions has wrapscan off")
	}
}

// TestCompilePassesTheCaseOptions makes sure a search picks up 'ignorecase'
// and 'smartcase' rather than each caller assembling a regex.Options of its
// own. A pattern with an uppercase letter in it defeats 'ignorecase' under
// 'smartcase' and does not without, and that is visible on the compiled
// pattern.
func TestCompilePassesTheCaseOptions(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		opt     Options
		want    bool
	}{
		{"plain", "foo", Options{}, false},
		{"ignorecase", "foo", Options{IgnoreCase: true}, true},
		{"smartcase, all lower", "foo", Options{IgnoreCase: true, SmartCase: true}, true},
		{"smartcase, one upper", "Foo", Options{IgnoreCase: true, SmartCase: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re, err := Compile(tc.pattern, tc.opt)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.pattern, err)
			}
			if got := re.IgnoreCase(); got != tc.want {
				t.Errorf("IgnoreCase = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCompileRefusesWhatRE2CannotDo: \zs is one of the six atoms the
// translator refuses by name, and a search has to pass that refusal on rather
// than swallowing it, because a pattern that silently means something else is
// the worst outcome the regex plan has.
func TestCompileRefusesWhatRE2CannotDo(t *testing.T) {
	if _, err := Compile(`foo\zsbar`, DefaultOptions()); err == nil {
		t.Fatal(`Compile("foo\\zsbar") returned no error`)
	}
}

// TestParse is vim's search command line grammar, taken apart. The rows with
// a delimiter in the pattern are the ones worth having: /[a/]/e is a
// collection and an offset, not a pattern called "[a" with a nonsense offset.
func TestParse(t *testing.T) {
	cases := []struct {
		line    string
		dir     Direction
		pattern string
		off     Offset
		keep    bool
	}{
		{line: "", pattern: "", keep: true},
		{line: "foo", pattern: "foo"},
		{line: "foo/", pattern: "foo"},
		{line: "/", pattern: ""},
		{line: "foo/e", pattern: "foo", off: Offset{Kind: OffsetEnd}},
		{line: "foo/e+2", pattern: "foo", off: Offset{Kind: OffsetEnd, N: 2}},
		{line: "foo/e-1", pattern: "foo", off: Offset{Kind: OffsetEnd, N: -1}},
		{line: "foo/e-0", pattern: "foo", off: Offset{Kind: OffsetEnd}},
		{line: "foo/s+1", pattern: "foo", off: Offset{Kind: OffsetStart, N: 1}},
		{line: "foo/b-2", pattern: "foo", off: Offset{Kind: OffsetStart, N: -2}},
		{line: "foo/s", pattern: "foo"},
		{line: "foo/+3", pattern: "foo", off: Offset{Kind: OffsetLine, N: 3}},
		{line: "foo/-2", pattern: "foo", off: Offset{Kind: OffsetLine, N: -2}},
		{line: "foo/2", pattern: "foo", off: Offset{Kind: OffsetLine, N: 2}},
		{line: "foo/0", pattern: "foo", off: Offset{Kind: OffsetLine}},
		{line: "foo/+", pattern: "foo", off: Offset{Kind: OffsetLine, N: 1}},
		{line: "foo/-", pattern: "foo", off: Offset{Kind: OffsetLine, N: -1}},
		{line: "foo/exyz", pattern: "foo", off: Offset{Kind: OffsetEnd}},
		{line: `a\/b`, pattern: `a\/b`},
		{line: `a\/b/e`, pattern: `a\/b`, off: Offset{Kind: OffsetEnd}},
		{line: `[/]/e`, pattern: `[/]`, off: Offset{Kind: OffsetEnd}},
		{line: `[]/]/e`, pattern: `[]/]`, off: Offset{Kind: OffsetEnd}},
		{line: `[[:alpha:]/]/e`, pattern: `[[:alpha:]/]`, off: Offset{Kind: OffsetEnd}},
		// A backward search unescapes \? and a forward one leaves \/ alone.
		{line: `a\?b`, dir: Backward, pattern: "a?b"},
		{line: `a\?b?e`, dir: Backward, pattern: "a?b", off: Offset{Kind: OffsetEnd}},
		{line: `a\/b`, dir: Backward, pattern: `a\/b`},
	}
	for _, tc := range cases {
		t.Run(tc.dir.String()+tc.line, func(t *testing.T) {
			got, err := Parse(tc.line, tc.dir)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.line, err)
			}
			if got.Pattern != tc.pattern {
				t.Errorf("pattern = %q, want %q", got.Pattern, tc.pattern)
			}
			if got.Offset != tc.off {
				t.Errorf("offset = %+v, want %+v", got.Offset, tc.off)
			}
			if got.KeepOffset != tc.keep {
				t.Errorf("KeepOffset = %v, want %v", got.KeepOffset, tc.keep)
			}
		})
	}
}

// TestParseRefusesAChain: /pat/;/pat2 is real vim and pvim does not run it, so
// it comes back named rather than as a search for the first half.
func TestParseRefusesAChain(t *testing.T) {
	if _, err := Parse("foo/;/bar", Forward); err == nil {
		t.Fatal("Parse of a chained search returned no error")
	}
}

// TestOffsetString is what vim echoes back, which is not what was typed:
// keeping the three fields rather than the letters means /pat/1 comes back as
// /pat/+1 and /pat/b+2 as /pat/s+2. n prints this, so it is diffed.
func TestOffsetString(t *testing.T) {
	cases := []struct {
		off  Offset
		want string
	}{
		{Offset{}, ""},
		{Offset{Kind: OffsetEnd}, "e"},
		{Offset{Kind: OffsetEnd, N: 2}, "e+2"},
		{Offset{Kind: OffsetEnd, N: -1}, "e-1"},
		{Offset{Kind: OffsetStart, N: 2}, "s+2"},
		{Offset{Kind: OffsetStart, N: -1}, "s-1"},
		{Offset{Kind: OffsetLine, N: 1}, "+1"},
		{Offset{Kind: OffsetLine}, "+0"},
		{Offset{Kind: OffsetLine, N: -2}, "-2"},
	}
	for _, tc := range cases {
		if got := tc.off.String(); got != tc.want {
			t.Errorf("%+v String = %q, want %q", tc.off, got, tc.want)
		}
	}
}

// TestCommand is the whole line n echoes when it repeats a search.
func TestCommand(t *testing.T) {
	s := &State{Pattern: "foo", Off: Offset{Kind: OffsetEnd, N: 2}}
	if got, want := s.Command(Forward), "/foo/e+2"; got != want {
		t.Errorf("Command(Forward) = %q, want %q", got, want)
	}
	if got, want := s.Command(Backward), "?foo?e+2"; got != want {
		t.Errorf("Command(Backward) = %q, want %q", got, want)
	}
	s.Off = Offset{}
	if got, want := s.Command(Forward), "/foo"; got != want {
		t.Errorf("Command with no offset = %q, want %q", got, want)
	}
}
