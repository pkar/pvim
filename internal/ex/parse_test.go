package ex

import (
	"strings"
	"testing"
)

// The parser has no editor in it, so every case here is a string in and a
// struct out. That is the whole reason Parse and Resolve are two functions: a
// range parses the same whatever the buffer holds.

func TestParseNameAndBang(t *testing.T) {
	for _, tc := range []struct {
		line string
		name string
		bang bool
		args string
	}{
		{"w", "write", false, ""},
		{"w!", "write", true, ""},
		{"e foo.txt", "edit", false, "foo.txt"},
		{"s/a/b/g", "substitute", false, "/a/b/g"},
		{"sg/a/b/", "substitute", false, "g/a/b/"},
		{"ka", "k", false, "a"},
		{"kee", "keepmarks", false, ""},
		{"normal dw", "normal", false, "dw"},
		{"   :::d", "delete", false, ""},
		{"", cmdNop, false, ""},
		{"5", cmdGoto, false, ""},
		{"%", cmdGoto, false, ""},
	} {
		got, err := Parse(tc.line)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.line, err)
			continue
		}
		if got.Name != tc.name {
			t.Errorf("Parse(%q).Name = %q, want %q", tc.line, got.Name, tc.name)
		}
		if got.Bang != tc.bang {
			t.Errorf("Parse(%q).Bang = %v, want %v", tc.line, got.Bang, tc.bang)
		}
		if got.Args != tc.args {
			t.Errorf("Parse(%q).Args = %q, want %q", tc.line, got.Args, tc.args)
		}
	}
}

// TestParseRangeShapes covers the address grammar. The Given count is checked
// as well as the addresses because ":d", ":.d" and ":.,.d" do the same thing
// while ":w" and ":.w" do not.
func TestParseRangeShapes(t *testing.T) {
	for _, tc := range []struct {
		line  string
		given int
		from  Addr
		to    Addr
		semi  bool
	}{
		{"d", 0, Addr{}, Addr{}, false},
		{".d", 1, Addr{Kind: AddrCurrent}, Addr{Kind: AddrCurrent}, false},
		{"5d", 1, Addr{Kind: AddrLine, Line: 5}, Addr{Kind: AddrLine, Line: 5}, false},
		{"$d", 1, Addr{Kind: AddrLast}, Addr{Kind: AddrLast}, false},
		{"1,5d", 2, Addr{Kind: AddrLine, Line: 1}, Addr{Kind: AddrLine, Line: 5}, false},
		{"1;5d", 2, Addr{Kind: AddrLine, Line: 1}, Addr{Kind: AddrLine, Line: 5}, true},
		{"%d", 2, Addr{Kind: AddrLine, Line: 1}, Addr{Kind: AddrLast}, false},
		{"+d", 1, Addr{Kind: AddrCurrent, Offset: 1}, Addr{Kind: AddrCurrent, Offset: 1}, false},
		{"-2d", 1, Addr{Kind: AddrCurrent, Offset: -2}, Addr{Kind: AddrCurrent, Offset: -2}, false},
		{".+3+2d", 1, Addr{Kind: AddrCurrent, Offset: 5}, Addr{Kind: AddrCurrent, Offset: 5}, false},
		{"'a,'bd", 2, Addr{Kind: AddrMark, Mark: 'a'}, Addr{Kind: AddrMark, Mark: 'b'}, false},
		{"/foo/d", 1, Addr{Kind: AddrSearchFwd, Pattern: "foo"}, Addr{Kind: AddrSearchFwd, Pattern: "foo"}, false},
		{"?bar?-1d", 1,
			Addr{Kind: AddrSearchBack, Pattern: "bar", Offset: -1},
			Addr{Kind: AddrSearchBack, Pattern: "bar", Offset: -1}, false},
		{`\/d`, 1, Addr{Kind: AddrNextMatch}, Addr{Kind: AddrNextMatch}, false},
		// Only the last two addresses survive, which is why ":1,2,3d" is 2 to
		// 3 and not 1 to 3.
		{"1,2,3d", 2, Addr{Kind: AddrLine, Line: 2}, Addr{Kind: AddrLine, Line: 3}, false},
		{",5d", 2, Addr{Kind: AddrCurrent}, Addr{Kind: AddrLine, Line: 5}, false},
		// Three addresses: the last two decide the lines and the ";" in front
		// of them still has to survive, because it moves the cursor. Semicolon
		// here is the separator between the surviving pair, which is the ",".
		{"3;.,.+1d", 2, Addr{Kind: AddrCurrent}, Addr{Kind: AddrCurrent, Offset: 1}, false},
	} {
		got, err := Parse(tc.line)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.line, err)
			continue
		}
		if got.Range.Given != tc.given {
			t.Errorf("Parse(%q).Range.Given = %d, want %d", tc.line, got.Range.Given, tc.given)
		}
		if got.Range.From != tc.from {
			t.Errorf("Parse(%q).Range.From = %+v, want %+v", tc.line, got.Range.From, tc.from)
		}
		if got.Range.To != tc.to {
			t.Errorf("Parse(%q).Range.To = %+v, want %+v", tc.line, got.Range.To, tc.to)
		}
		if got.Range.Semicolon != tc.semi {
			t.Errorf("Parse(%q).Range.Semicolon = %v, want %v", tc.line, got.Range.Semicolon, tc.semi)
		}
	}
}

// TestParseRegisterAndCount is vim's rule for the two arguments that come
// before the free text, measured: ":1,2 d extra" answers
// "E488: Trailing characters: xtra", with the e taken as the register.
// TestParseRangeKeepsEarlierSeparators: only the last two addresses decide the
// lines, and every ";" moves the cursor as get_address walks past it, so the
// ones in front of the surviving pair are kept rather than dropped. Measured:
// ":3;.,.+1d" on a1..a5 deletes a3 and a4 and ends on line 3, where dropping
// the ";" deletes a1 and a2.
func TestParseRangeKeepsEarlierSeparators(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []Sep
	}{
		{"1,5d", nil},
		{"1;5d", nil},
		{"3;.,.+1d", []Sep{{Addr: Addr{Kind: AddrLine, Line: 3}, Semicolon: true}}},
		{"1,2,3d", []Sep{{Addr: Addr{Kind: AddrLine, Line: 1}, Semicolon: false}}},
		{"1;2;3d", []Sep{{Addr: Addr{Kind: AddrLine, Line: 1}, Semicolon: true}}},
		{"1,2;3d", []Sep{{Addr: Addr{Kind: AddrLine, Line: 1}, Semicolon: false}}},
	} {
		got, err := Parse(tc.line)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.line, err)
			continue
		}
		if len(got.Range.Earlier) != len(tc.want) {
			t.Errorf("Parse(%q).Range.Earlier = %+v, want %+v", tc.line, got.Range.Earlier, tc.want)
			continue
		}
		for i := range tc.want {
			if got.Range.Earlier[i] != tc.want[i] {
				t.Errorf("Parse(%q).Range.Earlier[%d] = %+v, want %+v",
					tc.line, i, got.Range.Earlier[i], tc.want[i])
			}
		}
	}
}

func TestParseRegisterAndCount(t *testing.T) {
	for _, tc := range []struct {
		line  string
		reg   byte
		count int
		args  string
	}{
		{"d", 0, 0, ""},
		{"d a", 'a', 0, ""},
		{"d a 3", 'a', 3, ""},
		{"d 3", 0, 3, ""},
		{"y A", 'A', 0, ""},
		{"d extra", 'e', 0, "xtra"},
		{"j 3", 0, 3, ""},
	} {
		got, err := Parse(tc.line)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.line, err)
			continue
		}
		if got.Reg != tc.reg {
			t.Errorf("Parse(%q).Reg = %q, want %q", tc.line, got.Reg, tc.reg)
		}
		if got.Count != tc.count {
			t.Errorf("Parse(%q).Count = %d, want %d", tc.line, got.Count, tc.count)
		}
		if got.Args != tc.args {
			t.Errorf("Parse(%q).Args = %q, want %q", tc.line, got.Args, tc.args)
		}
	}
}

// TestParseModifiers keeps ":s" a substitute and ":sil" a modifier, which is
// the one thing a naive modifier loop gets wrong.
func TestParseModifiers(t *testing.T) {
	got, err := Parse("silent! vertical sp foo")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !got.Mods.Silent || !got.Mods.SilentBang || !got.Mods.Vertical {
		t.Errorf("mods = %+v, want silent, silent-bang and vertical", got.Mods)
	}
	if got.Name != "split" || got.Args != "foo" {
		t.Errorf("got %q %q, want split foo", got.Name, got.Args)
	}

	if c, err := Parse("s/silent/x/"); err != nil || c.Name != "substitute" {
		t.Errorf(`":s/silent/x/" parsed as %q (%v), want substitute`, c.Name, err)
	}
	// A modifier with nothing after it is the command itself, not a modifier
	// that ate the line.
	if c, err := Parse("vertical"); err != nil || c.Name != "vertical" || c.Mods.Vertical {
		t.Errorf(`":vertical" parsed as %q mods %+v (%v)`, c.Name, c.Mods, err)
	}
}

// TestParseErrors is the four the parser can raise on its own. Each code was
// measured: ":1,2set nu" is E481, ":d!" is E477 and ":foo" is E492 with the
// name after it.
func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		{"foo", "E492: Not an editor command: foo"},
		{"1,2set nu", "E481: No range allowed"},
		{"d!", "E477: No ! allowed"},
	} {
		_, err := Parse(tc.line)
		if err == nil {
			t.Errorf("Parse(%q) succeeded, want %q", tc.line, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.line, err, tc.want)
		}
	}
}

// TestUppercaseIsAUserCommand: the parser accepts any uppercase name because
// it cannot see the definitions, and dispatch is what answers E492 for one
// nobody defined. Measured: ":W" says "E492: Not an editor command: W", which
// is what catches a shifted colon.
func TestUppercaseIsAUserCommand(t *testing.T) {
	c, err := Parse("W")
	if err != nil {
		t.Fatalf("Parse(W): %v", err)
	}
	if c.Name != "W" || c.Typed != "W" {
		t.Errorf("Parse(W) = %+v, want the user command W", c)
	}
	ctx := &Context{Cmds: UserCommands{}}
	if err := ctx.RunCmd(c); err == nil || err.Error() != "E492: Not an editor command: W" {
		t.Errorf("running it answered %v, want E492", err)
	}
}

// TestShiftRepeats is the one command spelled by repeating a character, which
// Lookup folds rather than putting five entries in the table for.
func TestShiftRepeats(t *testing.T) {
	for _, typed := range []string{">", ">>", ">>>", "<", "<<"} {
		c, ok := Lookup(typed)
		if !ok {
			t.Fatalf("Lookup(%q) found nothing", typed)
		}
		if c.Name != typed[:1] {
			t.Errorf("Lookup(%q) = %q, want %q", typed, c.Name, typed[:1])
		}
	}
	got, err := Parse("2,3>>")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Typed != ">>" {
		t.Errorf("Typed = %q, want >>; exShift counts the brackets", got.Typed)
	}
}

// TestSplitBars is the vimrc's nastiest line and the rules it needs: a bar is
// a separator, unless the command in front of it swallows one or reads the
// text the bar is in.
func TestSplitBars(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"w", []string{"w"}},
		{"w | q", []string{"w ", " q"}},
		{
			"so $MYVIMRC | if has('gui_running') | so $MYGVIMRC | endif",
			[]string{"so $MYVIMRC ", " if has('gui_running') ", " so $MYGVIMRC ", " endif"},
		},
		// ":normal" replays the bar as a keystroke and ":g" hands it to the
		// command it runs, so neither is split.
		{"normal A|x", []string{"normal A|x"}},
		{"g/x/s/a/b/ | echo", []string{"g/x/s/a/b/ | echo"}},
		{`echo "a|b"`, []string{`echo "a|b"`}},
		{`s/a/b/ | w`, []string{`s/a/b/ `, ` w`}},
		// ":s" has no EX_TRLBAR either: do_sub scans the delimiter, the
		// pattern, the replacement and the flags itself and only looks for a
		// bar after them, so a bar in either half belongs to the command.
		// Splitting at the first one wrote "|b" into the file and then
		// answered E86 for the other half.
		{`s/a|b/X/`, []string{`s/a|b/X/`}},
		{`s/a/X|Y/`, []string{`s/a/X|Y/`}},
		{`%s/\vb|$/-/g`, []string{`%s/\vb|$/-/g`}},
		{`s/a/b/g | w`, []string{`s/a/b/g `, ` w`}},
		{`s/a\/b/c/ | w`, []string{`s/a\/b/c/ `, ` w`}},
		{`s/a|b`, []string{`s/a|b`}},
		// The repeat forms carry flags and no pattern, so the first bar ends
		// them. ":&" never takes a delimiter at all, which is what makes the
		// "&" of ":&&" a flag.
		{`s|w`, []string{`s`, `w`}},
		{`sg|w`, []string{`sg`, `w`}},
		{`&&|w`, []string{`&&`, `w`}},
		// An address may hold one too, and get_address scans it the same way.
		{`/a|b/d|w`, []string{`/a|b/d`, `w`}},
	} {
		got := SplitBars(tc.line)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("SplitBars(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
