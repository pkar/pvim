package keymap

import (
	"errors"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// drain feeds a notation string through the machine one key at a time and
// returns what the editor would have executed, in notation, plus whether the
// machine ended up waiting for more input.
//
// One key at a time and through the same Push/Next pair cmd/pvim uses, because
// the thing under test is the state between two keystrokes and a helper that
// fed the whole string at once would never visit it.
func drain(t *testing.T, m *Machine, st State, typed string) (string, bool) {
	t.Helper()
	var out []key.Key
	for _, k := range keys(t, typed) {
		m.Push(k)
		for {
			r, ok, err := m.Next(st)
			if err != nil {
				t.Fatalf("resolving %q: %v", typed, err)
			}
			if !ok {
				break
			}
			out = append(out, r)
		}
	}
	return key.Format(out), m.Pending()
}

// TestAmbiguityWaitsForTheNextKey is the measurement at the top of doc.go, run
// three ways: with `ab` and `abc` both mapped, "ab" waits, "abx" resolves to
// the ab mapping and then the x, and "abc" resolves to the abc mapping.
//
// Waits, and not "times out": the machine has no clock and this test has none
// either. What ends the wait is the key that decides, which is what vim does
// when it is fed a script and what 'notimeout' makes it do at a keyboard.
func TestAmbiguityWaitsForTheNextKey(t *testing.T) {
	var tab Table
	for _, m := range []Mapping{
		mapping(t, "n", "ab", "AA", true),
		mapping(t, "n", "abc", "CCC", true),
	} {
		if err := tab.Set(m); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		typed, want string
		pending     bool
	}{
		{"ab", "", true},
		{"abx", "AAx", false},
		{"abc", "CCC", false},
		{"ab:", "AA:", false},
		{"az", "az", false},
	} {
		got, pending := drain(t, New(&tab), State{Mode: Normal}, tc.typed)
		if got != tc.want || pending != tc.pending {
			t.Errorf("%q resolves to %q pending=%v, want %q pending=%v",
				tc.typed, got, pending, tc.want, tc.pending)
		}
	}
}

// TestTimeoutEndsTheWait is the other half: the frontend's clock, arriving as
// one call, resolves the longest complete match and leaves everything else
// alone.
func TestTimeoutEndsTheWait(t *testing.T) {
	var tab Table
	for _, m := range []Mapping{
		mapping(t, "n", "ab", "AA", true),
		mapping(t, "n", "abc", "CCC", true),
	} {
		if err := tab.Set(m); err != nil {
			t.Fatal(err)
		}
	}

	// A complete match with a longer one still possible: the timeout takes the
	// complete one.
	m := New(&tab)
	if got, pending := drain(t, m, State{Mode: Normal}, "ab"); got != "" || !pending {
		t.Fatalf("ab resolves to %q pending=%v before the timeout", got, pending)
	}
	m.Timeout()
	var out []key.Key
	for {
		k, ok, err := m.Next(State{Mode: Normal})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		out = append(out, k)
	}
	if got := key.Format(out); got != "AA" {
		t.Errorf("the timeout resolves ab to %q, want %q", got, "AA")
	}

	// A prefix with nothing complete under it: the timeout gives the keys back
	// as they were typed, which is vim beeping its way through "a" and then
	// "b" would have been if the ab mapping did not exist.
	var only Table
	if err := only.Set(mapping(t, "n", "abc", "CCC", true)); err != nil {
		t.Fatal(err)
	}
	m2 := New(&only)
	if got, pending := drain(t, m2, State{Mode: Normal}, "ab"); got != "" || !pending {
		t.Fatalf("ab under abc resolves to %q pending=%v", got, pending)
	}
	m2.Timeout()
	out = out[:0]
	for {
		k, ok, err := m2.Next(State{Mode: Normal})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		out = append(out, k)
	}
	if got := key.Format(out); got != "ab" {
		t.Errorf("the timeout over a bare prefix gives %q, want %q", got, "ab")
	}
}

// TestRemapVersusNoremap: a :map right-hand side is fed back through the table
// and a :noremap one is not. Measured with `nmap ab cd` beside `nnoremap cd
// ...`, which runs the cd mapping, and the same pair with nnoremap on the
// outer one, which does not.
func TestRemapVersusNoremap(t *testing.T) {
	build := func(outerNoremap bool) *Machine {
		var tab Table
		if err := tab.Set(mapping(t, "n", "cd", "XY", true)); err != nil {
			t.Fatal(err)
		}
		if err := tab.Set(mapping(t, "n", "ab", "cd", outerNoremap)); err != nil {
			t.Fatal(err)
		}
		return New(&tab)
	}
	if got, _ := drain(t, build(false), State{Mode: Normal}, "ab"); got != "XY" {
		t.Errorf("nmap ab cd gives %q, want %q", got, "XY")
	}
	if got, _ := drain(t, build(true), State{Mode: Normal}, "ab"); got != "cd" {
		t.Errorf("nnoremap ab cd gives %q, want %q", got, "cd")
	}
}

// TestLeadingKeyIsProtected is the measurement that decides expand(): when the
// right-hand side starts with the whole left-hand side, exactly the first key
// is protected from remapping and the rest is not.
//
// The vim run behind this: `nmap l <mapping>` plus `nmap <Space>l <Space>l`,
// typing "<Space>l", moves the cursor one column and then runs the l mapping.
// Here the l mapping produces "Z", so the answer is a space and a Z: the space
// came back protected, the l did not.
func TestLeadingKeyIsProtected(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "l", "Z", true)); err != nil {
		t.Fatal(err)
	}
	if err := tab.Set(mapping(t, "n", "<Space>l", "<Space>l", false)); err != nil {
		t.Fatal(err)
	}
	if got, _ := drain(t, New(&tab), State{Mode: Normal}, "<Space>l"); got != "<Space>Z" {
		t.Errorf("<Space>l gives %q, want %q: the protection is one key, not the whole left-hand side", got, "<Space>Z")
	}

	// The test for the protection is the WHOLE left-hand side, and the effect
	// is one key. With `nmap <F2>l <F2>x` the two sides share their first key
	// and nothing else, so nothing is protected and the <F2> mapping fires:
	// measured, and it deleted a character where the protected reading would
	// have beeped.
	var tab2 Table
	if err := tab2.Set(mapping(t, "n", "<F2>", "Q", true)); err != nil {
		t.Fatal(err)
	}
	if err := tab2.Set(mapping(t, "n", "<F2>l", "<F2>x", false)); err != nil {
		t.Fatal(err)
	}
	if got, _ := drain(t, New(&tab2), State{Mode: Normal}, "<F2>l"); got != "Qx" {
		t.Errorf("<F2>l gives %q, want %q: sharing one key is not sharing the left-hand side", got, "Qx")
	}
}

// TestRecursionStops is vim's E223 at 'maxmapdepth'. Measured on `nmap a b`
// plus `nmap b a`, which says "E223: Recursive mapping" and throws the
// half-typed command away.
func TestRecursionStops(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "a", "b", false)); err != nil {
		t.Fatal(err)
	}
	if err := tab.Set(mapping(t, "n", "b", "a", false)); err != nil {
		t.Fatal(err)
	}
	m := New(&tab)
	m.Push(key.Rune('a'))
	_, _, err := m.Next(State{Mode: Normal})
	if !errors.Is(err, ErrRecursive) {
		t.Fatalf("a mutual recursion gives %v, want %v", err, ErrRecursive)
	}
	if m.Pending() {
		t.Error("the typeahead survived E223; vim clears it")
	}
}

// TestNowaitIsBufferLocalOnly is measured both ways, and it is the one rule
// here that reads like a bug and is not. A global <nowait> does nothing: with
// global `<nowait> ab` beside global `abc`, typing "abc" runs abc. A
// buffer-local one is the whole point: the same pair with ab local runs ab and
// leaves the c, and without the <nowait> the local mapping loses to the longer
// global one.
func TestNowaitIsBufferLocalOnly(t *testing.T) {
	global := func(nowait bool) *Machine {
		var tab Table
		m := mapping(t, "n", "ab", "AA", true)
		m.Nowait = nowait
		if err := tab.Set(m); err != nil {
			t.Fatal(err)
		}
		if err := tab.Set(mapping(t, "n", "abc", "CCC", true)); err != nil {
			t.Fatal(err)
		}
		return New(&tab)
	}
	if got, _ := drain(t, global(true), State{Mode: Normal}, "abc"); got != "CCC" {
		t.Errorf("a global <nowait> ab beside abc gives %q on abc, want %q", got, "CCC")
	}

	local := func(nowait bool) *Machine {
		var tab Table
		m := mapping(t, "n", "ab", "AA", true)
		m.Buffer, m.Buf, m.Nowait = true, 1, nowait
		if err := tab.Set(m); err != nil {
			t.Fatal(err)
		}
		if err := tab.Set(mapping(t, "n", "abc", "CCC", true)); err != nil {
			t.Fatal(err)
		}
		return New(&tab)
	}
	if got, _ := drain(t, local(true), State{Mode: Normal, Buf: 1}, "abc"); got != "AAc" {
		t.Errorf("a buffer-local <nowait> ab gives %q on abc, want %q", got, "AAc")
	}
	if got, _ := drain(t, local(false), State{Mode: Normal, Buf: 1}, "abc"); got != "CCC" {
		t.Errorf("a buffer-local ab with no <nowait> gives %q on abc, want %q", got, "CCC")
	}
}

// TestBufferLocalShadowsGlobal: same length, the local one wins, and it wins
// only in its own buffer.
func TestBufferLocalShadowsGlobal(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "gx", "GLOBAL", true)); err != nil {
		t.Fatal(err)
	}
	local := mapping(t, "n", "gx", "LOCAL", true)
	local.Buffer, local.Buf = true, 3
	if err := tab.Set(local); err != nil {
		t.Fatal(err)
	}
	if got, _ := drain(t, New(&tab), State{Mode: Normal, Buf: 3}, "gx"); got != "LOCAL" {
		t.Errorf("in buffer 3, gx gives %q, want LOCAL", got)
	}
	if got, _ := drain(t, New(&tab), State{Mode: Normal, Buf: 4}, "gx"); got != "GLOBAL" {
		t.Errorf("in buffer 4, gx gives %q, want GLOBAL", got)
	}
}

// TestModesAreSeparate: the same keys mean different things in different
// modes, and a mapping made in one is not found in another. This is what makes
// the vimrc's three visual clipboard mappings and its one insert mapping
// coexist on CTRL-V.
func TestModesAreSeparate(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "v", "<C-v>", `"+p`, true)); err != nil {
		t.Fatal(err)
	}
	if err := tab.Set(mapping(t, "i", "<C-v>", "<C-r><C-o>+", true)); err != nil {
		t.Fatal(err)
	}
	if got, _ := drain(t, New(&tab), State{Mode: Visual}, "<C-v>"); got != `"+p` {
		t.Errorf("visual CTRL-V gives %q, want %q", got, `"+p`)
	}
	if got, _ := drain(t, New(&tab), State{Mode: Insert}, "<C-v>"); got != "<C-R><C-O>+" {
		t.Errorf("insert CTRL-V gives %q, want %q", got, "<C-R><C-O>+")
	}
	// Normal mode has neither, so CTRL-V is CTRL-V and blockwise visual mode
	// still starts.
	if got, _ := drain(t, New(&tab), State{Mode: Normal}, "<C-v>"); got != "<C-V>" {
		t.Errorf("normal CTRL-V gives %q, want it unmapped", got)
	}
}

// TestNoMapPassesEverythingThrough: with "map { gT" in place, "f{" finds the
// brace, because vim reads the argument of an f with no mapping at all.
// Measured, and it is one bool on State because the caller is the only thing
// that knows a command is waiting.
func TestNoMapPassesEverythingThrough(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "nvo", "{", "gT", true)); err != nil {
		t.Fatal(err)
	}
	m := New(&tab)
	m.Push(key.Rune('{'))
	k, ok, err := m.Next(State{Mode: Normal, NoMap: true})
	if err != nil || !ok {
		t.Fatalf("resolving { with NoMap: %v ok=%v", err, ok)
	}
	if k != key.Rune('{') {
		t.Errorf("with NoMap the key is %v, want a literal {", k)
	}
}

// TestNilTableIsAPassthrough: the zero machine has no table and hands
// everything straight back, which is what an editor with no vimrc gets.
func TestNilTableIsAPassthrough(t *testing.T) {
	var m Machine
	m.Push(key.Rune('x'))
	k, ok, err := m.Next(State{Mode: Normal})
	if err != nil || !ok || k != key.Rune('x') {
		t.Fatalf("a machine with no table gives %v ok=%v err=%v", k, ok, err)
	}
}

// TestResetThrowsAwayTheWait is what a beep does to a half-typed command.
func TestResetThrowsAwayTheWait(t *testing.T) {
	var tab Table
	if err := tab.Set(mapping(t, "n", "abc", "CCC", true)); err != nil {
		t.Fatal(err)
	}
	m := New(&tab)
	if got, pending := drain(t, m, State{Mode: Normal}, "ab"); got != "" || !pending {
		t.Fatalf("ab gives %q pending=%v", got, pending)
	}
	m.Reset()
	if m.Pending() {
		t.Error("Reset left keys behind")
	}
}
