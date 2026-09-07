package tui

import (
	"bytes"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/key"
)

// feedAll runs the whole of b through a fresh decoder in one go and returns
// the events, with flush off. It is the shape of a paste or of a keystroke
// file: no timing at all.
func feedAll(b string) []Event {
	var in input
	evs, _ := in.feed([]byte(b), false)
	return evs
}

// keyNames returns the vim notation for a run of events, which is what makes a
// failure message readable: "<Up> <C-A>" and not a slice of structs.
func keyNames(evs []Event) []string {
	var out []string
	for _, ev := range evs {
		k, ok := ev.(KeyEvent)
		if !ok {
			out = append(out, "<not a key>")
			continue
		}
		out = append(out, k.Key.String())
	}
	return out
}

func joined(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

// TestXtermSequencesDecode is the round trip: the bytes a
// real xterm sends, in, and the keys vim names, out.
//
// Every sequence here was taken from a terminal's own output or from vim's
// termcap for xterm, not invented: the CSI and SS3 cursor keys, the ";5" and
// ";2" modifier forms, the tilde forms for the editing keypad and the function
// keys, CSI-u for a combination the legacy tables cannot say, and the two C0
// bytes that are not what their name suggests -- 0x7f is <BS> and 0x08 is
// <C-H>.
func TestXtermSequencesDecode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"a", "a"},
		{"\x01", "<C-A>"},
		{"\x7f", "<BS>"},
		{"\x08", "<C-H>"},
		{"\r", "<CR>"},
		{"\t", "<Tab>"},
		{"\x1b[A", "<Up>"},
		{"\x1b[B", "<Down>"},
		{"\x1b[C", "<Right>"},
		{"\x1b[D", "<Left>"},
		{"\x1bOA", "<Up>"},
		{"\x1b[1;5A", "<C-Up>"},
		{"\x1b[1;2A", "<S-Up>"},
		{"\x1b[H", "<Home>"},
		{"\x1b[F", "<End>"},
		{"\x1b[3~", "<Del>"},
		{"\x1b[5~", "<PageUp>"},
		{"\x1b[6~", "<PageDown>"},
		{"\x1bOP", "<F1>"},
		{"\x1b[15~", "<F5>"},
		{"\x1b[Z", "<S-Tab>"},
		{"\x1b[97;5u", "<C-A>"},
		{"é", "é"},
		{"\x1b[A\x1b[B", "<Up> <Down>"},
		{"ihello\x1b", "i h e l l o"}, // the trailing Escape needs a flush
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := joined(keyNames(feedAll(tc.in))); got != tc.want {
				t.Errorf("feed(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestKeyBytesRoundTrip drives internal/key's own byte writer back through the
// decoder. It is the wide net under the table above: every key that can be
// written as bytes has to come back as itself, and a table of two dozen cases
// cannot say that.
func TestKeyBytesRoundTrip(t *testing.T) {
	var keys []key.Key
	for r := 'a'; r <= 'z'; r++ {
		keys = append(keys, key.Rune(r), key.Ctrl(r))
	}
	for _, s := range []key.Special{
		key.KeyUp, key.KeyDown, key.KeyLeft, key.KeyRight,
		key.KeyHome, key.KeyEnd, key.KeyPageUp, key.KeyPageDown,
		key.KeyDel, key.KeyInsert, key.KeyBS, key.KeyCR, key.KeyTab,
		key.KeyF1, key.KeyF5, key.KeyF12,
	} {
		keys = append(keys, key.Key{Special: s})
	}

	for _, k := range keys {
		b := key.Bytes([]key.Key{k})
		if len(b) == 0 {
			t.Errorf("%s writes no bytes", k)
			continue
		}
		got := keyNames(feedAll(string(b)))
		if len(got) != 1 || got[0] != k.String() {
			t.Errorf("%s -> %q -> %q", k, b, joined(got))
		}
	}
}

// TestEscapeNeedsTheTimeout is the ambiguity this whole package is arranged
// around. A bare Escape and the first byte of an arrow key are the same byte
// and only time tells them apart, which is what the vimrc's notimeout,
// ttimeout and ttimeoutlen=10 are about.
func TestEscapeNeedsTheTimeout(t *testing.T) {
	var in input

	// A lone Escape decodes to nothing and consumes nothing: it could still
	// become <Up>.
	evs, used := in.feed([]byte("\x1b"), false)
	if len(evs) != 0 || used != 0 {
		t.Fatalf("a bare Escape decoded to %q and consumed %d bytes; it has to wait", joined(keyNames(evs)), used)
	}

	// The rest of the arrow key arrives inside the timeout and it was an
	// arrow key all along.
	evs, used = in.feed([]byte("\x1b[A"), false)
	if got := joined(keyNames(evs)); got != "<Up>" || used != 3 {
		t.Fatalf("\\x1b[A = %q, %d bytes, want <Up>, 3", got, used)
	}

	// Nothing else arrives, the timeout expires, and it was an Escape.
	evs, used = in.feed([]byte("\x1b"), true)
	if got := joined(keyNames(evs)); got != "<Esc>" || used != 1 {
		t.Fatalf("flushed \\x1b = %q, %d bytes, want <Esc>, 1", got, used)
	}
}

// TestSequenceSplitAcrossReads: ssh, a slow terminal and a busy machine all
// deliver an escape sequence in pieces, and each piece is its own read. The
// decoder keeps the tail and the caller keeps calling.
func TestSequenceSplitAcrossReads(t *testing.T) {
	var in input
	pending := []byte(nil)
	var all []Event

	for _, chunk := range []string{"\x1b", "[1", ";5", "A"} {
		pending = append(pending, chunk...)
		evs, used := in.feed(pending, false)
		pending = consume(pending, used)
		all = append(all, evs...)
	}
	if got := joined(keyNames(all)); got != "<C-Up>" {
		t.Errorf("a sequence in four reads decoded to %q, want <C-Up>", got)
	}
	if len(pending) != 0 {
		t.Errorf("%q left over", pending)
	}
}

// TestBracketedPaste. Without it a pasted "jj" leaves insert mode, a pasted
// tab starts completion and a pasted comment leader gets auto-indented on
// every line. The markers are taken out of the stream before the key decoder
// sees them, because "\x1b[200~" is a well-formed CSI with no key behind it
// and would otherwise arrive as <Ignore>.
func TestBracketedPaste(t *testing.T) {
	evs := feedAll("a\x1b[200~jj\ttext\x1b[201~b")
	if len(evs) != 3 {
		t.Fatalf("got %d events, want key, paste, key: %#v", len(evs), evs)
	}
	if k, ok := evs[0].(KeyEvent); !ok || k.Key.String() != "a" {
		t.Errorf("first event is %#v, want the key a", evs[0])
	}
	p, ok := evs[1].(PasteEvent)
	if !ok {
		t.Fatalf("second event is %#v, want a PasteEvent", evs[1])
	}
	if string(p.Text) != "jj\ttext" {
		t.Errorf("pasted %q, want %q", p.Text, "jj\ttext")
	}
	if k, ok := evs[2].(KeyEvent); !ok || k.Key.String() != "b" {
		t.Errorf("third event is %#v, want the key b", evs[2])
	}
}

// TestPasteSplitAcrossReads, including a read that ends in the middle of the
// closing marker. That is the case where a naive scanner delivers "\x1b[20" as
// pasted text and then loses the paste.
func TestPasteSplitAcrossReads(t *testing.T) {
	var in input
	pending := []byte(nil)
	var all []Event

	for _, chunk := range []string{"\x1b[200~one ", "two\x1b[2", "01~"} {
		pending = append(pending, chunk...)
		evs, used := in.feed(pending, false)
		pending = consume(pending, used)
		all = append(all, evs...)
	}
	if len(all) != 1 {
		t.Fatalf("got %d events, want one paste: %#v", len(all), all)
	}
	p, ok := all[0].(PasteEvent)
	if !ok {
		t.Fatalf("got %#v, want a PasteEvent", all[0])
	}
	if string(p.Text) != "one two" {
		t.Errorf("pasted %q, want %q", p.Text, "one two")
	}
}

// TestPartialSuffix is the rule the paste scanner holds back on, on its own,
// because it is the part that is wrong in every hand-rolled version of this.
func TestPartialSuffix(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"abc", 0},
		{"ab\x1b", 1},
		{"ab\x1b[", 2},
		{"\x1b[201", 5},
		{"\x1b[20", 4},
		{"\x1b[2", 3},
		{"", 0},
	} {
		if got := partialSuffix([]byte(tc.in), pasteEnd); got != tc.want {
			t.Errorf("partialSuffix(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestUnknownReportsAreSwallowed. A terminal answers a device-attribute or
// cursor-position query whenever it feels like it, including at startup and
// after a resize, and an editor that let one through would insert "[?62;c"
// into the buffer.
func TestUnknownReportsAreSwallowed(t *testing.T) {
	for _, in := range []string{
		"\x1b[?62;1;6cx",  // a device attribute report
		"\x1b[?2004;1$yx", // a DECRPM mode report, intermediate byte and all
		"\x1b[24;80Rx",    // a cursor position report
	} {
		if got := joined(keyNames(feedAll(in))); got != "x" {
			t.Errorf("%q decoded to %q, want just x", in, got)
		}
	}
}

// TestAPartialReportWaitsAndThenGivesUp. A report split across two reads has
// to be held; an Escape that only looks like the start of one has to become an
// Escape when the key-code timeout says so, or every "\x1b" typed in front of
// a "[" would hang the editor.
func TestAPartialReportWaitsAndThenGivesUp(t *testing.T) {
	var in input
	if evs, used := in.feed([]byte("\x1b[?62;1"), false); len(evs) != 0 || used != 0 {
		t.Fatalf("half a report gave %d events and used %d bytes", len(evs), used)
	}
	if evs, _ := in.feed([]byte("\x1b[?62;1;6c"), false); len(evs) != 0 {
		t.Fatalf("a whole report gave %#v, want nothing", evs)
	}
	// The timeout fires on half a report: the Escape was an Escape and the
	// two bytes behind it are the ordinary characters they look like, which
	// is what vim does with any escape sequence that times out.
	evs, used := in.feed([]byte("\x1b[?"), true)
	if got := joined(keyNames(evs)); got != "<Esc> [ ?" || used != 3 {
		t.Errorf("a flushed \\x1b[? gave %q using %d bytes, want \"<Esc> [ ?\" using 3", got, used)
	}
}

// TestMouseReports is SGR mouse, which is the only encoding that can report a
// column past 223 and the reason Start sends \x1b[?1006h.
//
// The coordinate change is the assertion that matters: a report is 1-based and
// a grid is 0-based, so a click on the top-left cell is 1;1 on the wire and
// 0,0 here.
func TestMouseReports(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want MouseEvent
	}{
		{"left press", "\x1b[<0;1;1M", MouseEvent{Button: MouseLeft, Action: MousePress}},
		{"left release", "\x1b[<0;10;5m", MouseEvent{Button: MouseLeft, Action: MouseRelease, Row: 4, Col: 9}},
		{"left drag", "\x1b[<32;11;5M", MouseEvent{Button: MouseLeft, Action: MouseDrag, Row: 4, Col: 10}},
		{"middle press", "\x1b[<1;3;4M", MouseEvent{Button: MouseMiddle, Action: MousePress, Row: 3, Col: 2}},
		{"right press", "\x1b[<2;3;4M", MouseEvent{Button: MouseRight, Action: MousePress, Row: 3, Col: 2}},
		{"middle drag", "\x1b[<33;3;4M", MouseEvent{Button: MouseMiddle, Action: MouseDrag, Row: 3, Col: 2}},
		{"middle release", "\x1b[<1;3;4m", MouseEvent{Button: MouseMiddle, Action: MouseRelease, Row: 3, Col: 2}},
		{"right drag", "\x1b[<34;3;4M", MouseEvent{Button: MouseRight, Action: MouseDrag, Row: 3, Col: 2}},
		{"right release", "\x1b[<2;3;4m", MouseEvent{Button: MouseRight, Action: MouseRelease, Row: 3, Col: 2}},
		{"a click past column 223", "\x1b[<0;400;9M", MouseEvent{Button: MouseLeft, Action: MousePress, Row: 8, Col: 399}},
		{"wheel up", "\x1b[<64;1;1M", MouseEvent{Button: MouseNone, Action: MouseWheelUp}},
		{"wheel down", "\x1b[<65;1;1M", MouseEvent{Button: MouseNone, Action: MouseWheelDown}},
		{"wheel left", "\x1b[<66;1;1M", MouseEvent{Button: MouseNone, Action: MouseWheelLeft}},
		{"wheel right", "\x1b[<67;1;1M", MouseEvent{Button: MouseNone, Action: MouseWheelRight}},
		{"shift wheel up", "\x1b[<68;1;1M", MouseEvent{Button: MouseNone, Action: MouseWheelUp, Mod: key.ModShift}},
		{"ctrl wheel down", "\x1b[<81;1;1M", MouseEvent{Button: MouseNone, Action: MouseWheelDown, Mod: key.ModCtrl}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evs := feedAll(tc.in)
			if len(evs) != 1 {
				t.Fatalf("got %d events, want one: %#v", len(evs), evs)
			}
			got, ok := evs[0].(MouseEvent)
			if !ok {
				t.Fatalf("got %#v, want a MouseEvent", evs[0])
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestShiftWheelKeepsItsModifier says out loud what this frontend refuses to
// decide. Vim's scroll.txt gives the wheel four keys and six default actions:
//
//	<ScrollWheelUp> scroll N lines up
//	<S-ScrollWheelUp> scroll one page up
//	<ScrollWheelLeft> scroll N columns left
//
// with N three lines vertically and six columns horizontally. All of that is
// the editor's, so one notch is one event and the modifier arrives with it. A
// frontend that turned a shifted wheel into a horizontal one, or into three
// events, would make "scroll one page" impossible to write anywhere.
func TestShiftWheelKeepsItsModifier(t *testing.T) {
	evs := feedAll("\x1b[<68;1;1M")
	if len(evs) != 1 {
		t.Fatalf("a shifted wheel notch gave %d events, want one", len(evs))
	}
	got := evs[0].(MouseEvent)
	if got.Action != MouseWheelUp || got.Mod != key.ModShift {
		t.Errorf("got %+v, want a wheel-up carrying Shift", got)
	}
}

// TestMouseReportsThisVocabularyHasNoEventFor. The two extra buttons and the
// bare motion report have no MouseEvent, and dropping them is better than
// inventing one: internal/gui cannot deliver them either, and the two
// frontends have to agree.
func TestMouseReportsThisVocabularyHasNoEventFor(t *testing.T) {
	for _, in := range []string{"\x1b[<128;1;1M", "\x1b[<35;1;1M"} {
		if evs := feedAll(in); len(evs) != 0 {
			t.Errorf("%q gave %#v, want nothing", in, evs)
		}
	}
}

// TestKeyCodeTimeout is vim's own two tables, from options.txt under
// 'ttimeout' and 'ttimeoutlen', turned into cases. The last row is the vimrc.
func TestKeyCodeTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    Options
		want time.Duration
		on   bool
	}{
		{"both off", Options{TimeoutLen: 1000, TTimeoutLen: 100}, 0, false},
		{"timeout on, ttimeoutlen -1", Options{Timeout: true, TimeoutLen: 1000, TTimeoutLen: -1}, time.Second, true},
		{"timeout on, ttimeoutlen 100", Options{Timeout: true, TimeoutLen: 1000, TTimeoutLen: 100}, 100 * time.Millisecond, true},
		{"vim --clean", Options{Timeout: true, TTimeout: true, TimeoutLen: 1000, TTimeoutLen: 100}, 100 * time.Millisecond, true},
		{"the vimrc", Options{Timeout: false, TTimeout: true, TimeoutLen: 1000, TTimeoutLen: 10}, 10 * time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, on := tc.o.keyCodeTimeout()
			if on != tc.on || (on && got != tc.want) {
				t.Errorf("keyCodeTimeout() = %v, %v; want %v, %v", got, on, tc.want, tc.on)
			}
		})
	}
}

// TestConsumeKeepsTheTailAtTheFront. A "b = b[used:]" would walk the read
// buffer forward one keystroke at a time and reallocate it once per key over a
// long session.
func TestConsumeKeepsTheTailAtTheFront(t *testing.T) {
	b := make([]byte, 0, 16)
	b = append(b, "abcdef"...)
	b = consume(b, 2)
	if !bytes.Equal(b, []byte("cdef")) {
		t.Fatalf("consume = %q, want %q", b, "cdef")
	}
	if cap(b) != 16 {
		t.Errorf("consume reallocated: cap %d, want 16", cap(b))
	}
	if b = consume(b, 99); len(b) != 0 {
		t.Errorf("consume past the end left %q", b)
	}
}

// TestTheVimrcsTerminalClipboardKeysArrive.
//
// The vimrc has this, behind if has('clipboard') && !has('gui_running'):
//
//	vnoremap <C-c> "+y
//	vnoremap <C-x> "+d
//	vnoremap <C-v> "+p
//	inoremap <C-v> <C-r><C-o>+
//
// Two of those four bytes never reach a program from a terminal in cooked
// mode: 0x03 raises SIGINT under ISIG, and 0x16 is IEXTEN's literal-next, as
// 0x0f is its discard on macOS. Raw mode turns ISIG and IEXTEN off, which is
// what makes them arrive as keystrokes, and this asserts the decoding half of
// that. The mapping half belongs to the vimrc loader and the mode machine, and
// the has('gui_running') that gates it has to answer false wherever this
// frontend is the one running, which is what tui.GUIRunning is for.
func TestTheVimrcsTerminalClipboardKeysArrive(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"\x03", "<C-C>"},
		{"\x18", "<C-X>"},
		{"\x16", "<C-V>"},
		{"\x12", "<C-R>"}, // the <C-r><C-o>+ the insert-mode map expands to
		{"\x0f", "<C-O>"},
	} {
		if got := joined(keyNames(feedAll(tc.in))); got != tc.want {
			t.Errorf("%q decoded to %q, want %q", tc.in, got, tc.want)
		}
	}
}
