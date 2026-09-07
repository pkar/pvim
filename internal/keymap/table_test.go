package keymap

import (
	"errors"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// keys parses vim notation into keys, or fails the test. The tests are written
// in notation because a mapping is written in notation and a table of Key
// literals would be unreadable next to the vimrc line it came from.
func keys(t *testing.T, s string) []key.Key {
	t.Helper()
	k, err := key.Parse(s, ",")
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return k
}

// mapping builds a Mapping the way a :map command would.
func mapping(t *testing.T, modes, lhs, rhs string, noremap bool) Mapping {
	t.Helper()
	md, err := ParseModes(modes)
	if err != nil {
		t.Fatalf("modes %q: %v", modes, err)
	}
	return Mapping{
		Modes:   md,
		LHS:     keys(t, lhs),
		RHS:     keys(t, rhs),
		LHSText: lhs,
		RHSText: rhs,
		NoRemap: noremap,
	}
}

func TestParseModes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Mode
	}{
		{"", NVO},
		{"nvo", NVO},
		{"n", Normal},
		{"v", VS},
		{"x", Visual},
		{"s", Select},
		{"o", OpPending},
		{"i", Insert},
		{"c", Cmdline},
		{"ic", IC},
		{"l", Lang},
	} {
		got, err := ParseModes(tc.in)
		if err != nil {
			t.Errorf("ParseModes(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseModes(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if _, err := ParseModes("q"); err == nil {
		t.Error("ParseModes(\"q\") succeeded; q is not a mode letter")
	}
}

// TestBareMapIsNVO is the vimrc's own first surprise: "map { gT" is a mapping
// in operator-pending as well, so "d{" means delete to the previous tab page,
// which is nothing.
func TestBareMapIsNVO(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "nvo", "{", "gT", false)); err != nil {
		t.Fatal(err)
	}
	for _, md := range []Mode{Normal, Visual, Select, OpPending} {
		if _, ok := tab.Get(md, keys(t, "{"), 0); !ok {
			t.Errorf("{ is not mapped in %v", md)
		}
	}
	for _, md := range []Mode{Insert, Cmdline, Lang} {
		if _, ok := tab.Get(md, keys(t, "{"), 0); ok {
			t.Errorf("{ is mapped in %v and a bare :map does not do that", md)
		}
	}
}

// TestUnmapTakesOneMode is measured: ":map ab foo" then ":nunmap ab" leaves ab
// mapped in visual and operator-pending, and maparg() proves it.
func TestUnmapTakesOneMode(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "nvo", "ab", "foo", false)); err != nil {
		t.Fatal(err)
	}
	if err := tab.Unmap(Normal, keys(t, "ab"), false, 0); err != nil {
		t.Fatalf("nunmap ab: %v", err)
	}
	if _, ok := tab.Get(Normal, keys(t, "ab"), 0); ok {
		t.Error("ab is still mapped in normal mode")
	}
	for _, md := range []Mode{Visual, OpPending} {
		if _, ok := tab.Get(md, keys(t, "ab"), 0); !ok {
			t.Errorf("nunmap took the %v mapping too", md)
		}
	}
	// The whole miss is E31; the partial hit above was not.
	if err := tab.Unmap(Normal, keys(t, "zz"), false, 0); !errors.Is(err, ErrNoMapping) {
		t.Errorf("unmapping zz says %v, want %v", err, ErrNoMapping)
	}
}

// TestUniqueIsAnExactTest is measured both ways: <unique> ab fails when ab is
// mapped and passes when only abc is.
func TestUniqueIsAnExactTest(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "abc", "foo", true)); err != nil {
		t.Fatal(err)
	}
	m := mapping(t, "n", "ab", "bar", true)
	m.Unique = true
	if err := tab.Set(m); err != nil {
		t.Errorf("<unique> ab beside abc: %v, want no error", err)
	}

	m2 := mapping(t, "n", "abc", "baz", true)
	m2.Unique = true
	err := tab.Set(m2)
	var ue *UniqueError
	if !errors.As(err, &ue) {
		t.Fatalf("<unique> abc over abc: %v, want E227", err)
	}
	if want := "E227: Mapping already exists for abc"; ue.Error() != want {
		t.Errorf("E227 says %q, want %q", ue, want)
	}
}

// TestExprIsRefused holds the line: there is no expression evaluator and
// there is not going to be one, so an <expr> mapping says so when it is made.
func TestExprIsRefused(t *testing.T) {
	var tab Table
	m := mapping(t, "n", "gx", "Foo()", true)
	m.Expr = true
	err := tab.Set(m)
	var ee *ExprError
	if !errors.As(err, &ee) {
		t.Fatalf("<expr> mapping: %v, want an ExprError", err)
	}
	if _, ok := tab.Get(Normal, keys(t, "gx"), 0); ok {
		t.Error("the refused mapping went into the table anyway")
	}
}

// TestSetReplaces is what makes re-sourcing a vimrc idempotent, which this
// vimrc does to itself on every write through its BufWritePost autocmd.
func TestSetReplaces(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "ab", "one", true)); err != nil {
		t.Fatal(err)
	}
	if err := tab.Set(mapping(t, "n", "ab", "two", true)); err != nil {
		t.Fatal(err)
	}
	m, ok := tab.Get(Normal, keys(t, "ab"), 0)
	if !ok {
		t.Fatal("ab is not mapped")
	}
	if m.RHSText != "two" {
		t.Errorf("ab maps to %q, want the second definition", m.RHSText)
	}
}

// TestPlugParsesAndSits: a <Plug> left-hand side is stored where no terminal
// can reach it, which is the whole point of the prefix.
func TestPlugParsesAndSits(t *testing.T) {
	var tab Table
	m := mapping(t, "n", "<Plug>Thing", "ihello", false)
	if !m.HasPlug() {
		t.Fatal("the left-hand side does not start with <Plug>")
	}
	if err := tab.Set(m); err != nil {
		t.Fatal(err)
	}
	if _, ok := tab.Get(Normal, keys(t, "<Plug>Thing"), 0); !ok {
		t.Error("the <Plug> mapping is not in the table")
	}
}

// TestSIDIsStoredAndRefusedOnUse: key.Parse leaves <SID> as its five
// characters because :map is what would have resolved it, nothing here does,
// and the refusal is at the keystroke and not at the definition so that a
// mapping nobody presses says nothing at startup.
func TestSIDIsStoredAndRefusedOnUse(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "gs", "<SID>Thing", false)); err != nil {
		t.Fatalf("defining a <SID> mapping: %v", err)
	}
	m, ok := tab.Get(Normal, keys(t, "gs"), 0)
	if !ok {
		t.Fatal("the <SID> mapping is not in the table")
	}
	if !m.SID {
		t.Fatal("the mapping is not marked as carrying a <SID>")
	}

	mach := New(&tab)
	mach.PushKeys(keys(t, "gs"))
	_, _, err := mach.Next(State{Mode: Normal})
	var se *ScriptIDError
	if !errors.As(err, &se) {
		t.Fatalf("pressing gs: %v, want a ScriptIDError", err)
	}
	if mach.Pending() {
		t.Error("the typeahead survived the refusal")
	}
}
