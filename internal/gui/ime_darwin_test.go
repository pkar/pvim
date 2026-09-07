//go:build darwin

package gui

import (
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/key"
)

// The input-method path, tested against real AppKit with no window server.
//
// This is further than it looks. -interpretKeyEvents: needs an input context,
// an input context needs a class that conforms to NSTextInputClient, and the
// whole round trip -- NSEvent in, the text system in the middle, an
// insertText: callback out into Go -- runs headless, so "basic IME" here is a
// test rather than a claim. What is NOT tested is
// composition itself: a synthesised NSEvent carries characters that have
// already been through the keyboard layout, so the dead-key state machine
// never runs, and Option-e-then-e can only be checked by a person at a
// keyboard, which is what the manual checks are for.

// withWindow installs w as the package's current window for the duration of a
// test, because every callback in this package reaches the window through that
// one variable.
func withWindow(t *testing.T, w *window) {
	t.Helper()
	prev := current
	current = w
	t.Cleanup(func() { current = prev })
}

// testView returns an instance of the real registered view class.
func testView(t *testing.T) objc.ID {
	t.Helper()
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	cls, err := registerViewClass()
	if err != nil {
		t.Fatalf("registering pvimView: %v", err)
	}
	view := objc.ID(cls).Send(selAlloc)
	view = objc.Send[objc.ID](view, selInitWithFrame, NSRect{Size: NSSize{Width: 640, Height: 480}})
	if view == 0 {
		t.Fatal("pvimView initWithFrame: returned nil")
	}
	return view
}

// keyEvent builds an NSEvent of type NSEventTypeKeyDown.
func keyEvent(t *testing.T, chars string, code uint16, flags uint64) objc.ID {
	t.Helper()
	const nsEventTypeKeyDown = 10
	ev := objc.Send[objc.ID](objc.ID(objc.GetClass("NSEvent")),
		objc.RegisterName("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:"),
		uint64(nsEventTypeKeyDown), NSPoint{}, flags, float64(0), int64(0), objc.ID(0),
		nsString(chars), nsString(chars), false, code)
	if ev == 0 {
		t.Fatalf("NSEvent keyEventWithType: returned nil for %q", chars)
	}
	return ev
}

// drain empties an event queue into a slice.
func drain(q *eventq) []Event {
	var out []Event
	for {
		q.mu.Lock()
		n := len(q.events)
		q.mu.Unlock()
		if n == 0 {
			return out
		}
		ev, ok := q.pop()
		if !ok {
			return out
		}
		out = append(out, ev)
	}
}

// keysOf is the notation of every KeyEvent in a batch, which is what a failure
// message wants to print.
func keysOf(evs []Event) string {
	var keys []key.Key
	for _, ev := range evs {
		if k, ok := ev.(KeyEvent); ok {
			keys = append(keys, k.Key)
		}
	}
	return key.Format(keys)
}

// TestViewIsATextInputClient is the conformance that everything else here
// rests on. Without it -inputContext is nil, -interpretKeyEvents: does
// nothing, and dead keys silently stop working with no error anywhere.
func TestViewIsATextInputClient(t *testing.T) {
	view := testView(t)
	if !imeEnabled {
		t.Fatal("imeEnabled is false: the NSTextInputClient protocol was not found, so there are no dead keys")
	}
	proto := objc.GetProtocol("NSTextInputClient")
	if proto == nil {
		t.Fatal("NSTextInputClient is not a protocol this runtime knows")
	}
	if !objc.Send[bool](view, objc.RegisterName("conformsToProtocol:"), proto) {
		t.Error("pvimView does not conform to NSTextInputClient")
	}
	if view.Send(selInputContext) == 0 {
		t.Error("the view has no input context, so interpretKeyEvents: would do nothing")
	}
}

// TestMarkedRangeStructReturn is the ABI check with teeth.
//
// -markedRange returns an NSRange by value, which on arm64 is two integer
// registers filled by a Go callback going out through purego. Nothing else in
// this package returns a struct to Objective-C, and a purego that lost the
// ability would fail here rather than as a garbage popup position.
func TestMarkedRangeStructReturn(t *testing.T) {
	view := testView(t)
	w := &window{q: newEventq()}
	withWindow(t, w)

	got := objc.Send[NSRange](view, objc.RegisterName("markedRange"))
	if got != notFoundRange {
		t.Errorf("markedRange with nothing composing is %+v, want %+v", got, notFoundRange)
	}

	w.ime.marked = []rune("ab")
	got = objc.Send[NSRange](view, objc.RegisterName("markedRange"))
	if got.Location != 0 || got.Length != 2 {
		t.Errorf("markedRange while composing two characters is %+v, want {0 2}", got)
	}
	if !objc.Send[bool](view, objc.RegisterName("hasMarkedText")) {
		t.Error("hasMarkedText is NO while text is marked")
	}
	view.Send(objc.RegisterName("unmarkText"))
	if len(w.ime.marked) != 0 {
		t.Error("unmarkText left the composition in place")
	}
}

// TestKeyDownDeliversTextExactlyOnce is the rule the whole file exists for.
//
// The key goes to the input method first and falls through to the ordinary
// key mapping if the method produced nothing. Get the bookkeeping wrong in one
// direction and every letter is typed twice; get it wrong in the other and
// nothing is typed at all.
func TestKeyDownDeliversTextExactlyOnce(t *testing.T) {
	view := testView(t)
	w := &window{q: newEventq()}
	withWindow(t, w)

	w.keyDown(view, keyEvent(t, "a", 0, 0))
	evs := drain(w.q)
	if got := keysOf(evs); got != "a" {
		t.Fatalf("typing 'a' delivered %q (%d events), want exactly one 'a'", got, len(evs))
	}
}

// TestSpecialKeysBypassTheInputMethod. Escape, Return, Tab and Backspace are
// vim's, and an input method that got hold of one would turn it into a
// doCommandBySelector: this editor deliberately answers with silence -- so a
// key that reached the input method by mistake would vanish rather than
// arriving as the wrong key, which is a bug with no symptom but a missing
// keystroke.
func TestSpecialKeysBypassTheInputMethod(t *testing.T) {
	view := testView(t)
	w := &window{q: newEventq()}
	withWindow(t, w)

	for _, tc := range []struct {
		chars string
		code  uint16
		want  string
	}{
		{"\x1b", 53, "<Esc>"},
		{"\r", 36, "<CR>"},
		{"\t", 48, "<Tab>"},
		{"\x7f", 51, "<BS>"},
	} {
		w.keyDown(view, keyEvent(t, tc.chars, tc.code, 0))
		if got := keysOf(drain(w.q)); got != tc.want {
			t.Errorf("keyCode %d delivered %q, want %q", tc.code, got, tc.want)
		}
	}
}

// TestControlChordsBypassTheInputMethod. CTRL-A is a vim command and not text,
// and the input method would call it moveToBeginningOfParagraph:.
func TestControlChordsBypassTheInputMethod(t *testing.T) {
	view := testView(t)
	w := &window{q: newEventq()}
	withWindow(t, w)

	w.keyDown(view, keyEvent(t, "a", 0, modFlagControl))
	if got := keysOf(drain(w.q)); got != "<C-A>" {
		t.Errorf("Control-a delivered %q, want <C-A>", got)
	}
}

// TestInsertTextPushesEveryRune. An input method commits a whole string at
// once, and a Chinese or Japanese one commits several characters for one
// keystroke.
func TestInsertTextPushesEveryRune(t *testing.T) {
	view := testView(t)
	w := &window{q: newEventq()}
	withWindow(t, w)

	view.Send(objc.RegisterName("insertText:replacementRange:"), nsString("héllo"), notFoundRange)
	if got := keysOf(drain(w.q)); got != "héllo" {
		t.Errorf("insertText delivered %q, want héllo", got)
	}
	if !w.ime.handled {
		t.Error("insertText did not mark the key handled, so keyDown would deliver it a second time")
	}
}

// TestMarkedTextIsRecordedAndNotTyped. A composition in progress is not text
// yet. Typing it would put the input method's scratch work into the buffer,
// which is what an editor with no marked-text drawing must not do.
func TestMarkedTextIsRecordedAndNotTyped(t *testing.T) {
	view := testView(t)
	w := &window{q: newEventq()}
	withWindow(t, w)

	view.Send(objc.RegisterName("setMarkedText:selectedRange:replacementRange:"),
		nsString("´"), NSRange{Location: 1}, notFoundRange)
	if string(w.ime.marked) != "´" {
		t.Errorf("marked text is %q, want the acute accent", string(w.ime.marked))
	}
	if evs := drain(w.q); len(evs) != 0 {
		t.Errorf("a composition in progress delivered %d events (%q); it must deliver none", len(evs), keysOf(evs))
	}
}

// TestCaretRect is where the accent popup goes.
//
// The metrics are device pixels and AppKit wants points, so the scale divides:
// a popup positioned in pixels on a retina display sits twice as far across
// the window as the caret it belongs to, which is the whole of the bug this
// pins.
func TestCaretRect(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		row, col             int
		cellW, rowH, scale   int
		wantX, wantY         float64
		wantWidth, wantHight float64
	}{
		{"scale 1, origin", 0, 0, 8, 18, 1, 0, 0, 8, 18},
		{"scale 1, row 3 column 10", 3, 10, 8, 18, 1, 80, 54, 8, 18},
		{"scale 2 halves everything", 3, 10, 16, 36, 2, 80, 54, 8, 18},
		{"a nonsense scale is treated as 1", 1, 1, 8, 18, 0, 8, 18, 8, 18},
	} {
		got := caretRect(tc.row, tc.col, tc.cellW, tc.rowH, tc.scale)
		if got.Origin.X != tc.wantX || got.Origin.Y != tc.wantY ||
			got.Size.Width != tc.wantWidth || got.Size.Height != tc.wantHight {
			t.Errorf("%s: caretRect = %+v, want origin (%v,%v) size %vx%v",
				tc.name, got, tc.wantX, tc.wantY, tc.wantWidth, tc.wantHight)
		}
	}
}

// TestUTF16Runes is the decoder insertText: reads its argument with. The
// interesting case is an emoji, which an emoji picker really can deliver and
// which arrives as a surrogate pair.
func TestUTF16Runes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		units []uint16
		want  string
	}{
		{"ascii", []uint16{'a', 'b'}, "ab"},
		{"latin-1 supplement", []uint16{0x00e9}, "é"},
		{"a surrogate pair is one rune", []uint16{0xD83D, 0xDE00}, "\U0001F600"},
		{"an unpaired surrogate is dropped", []uint16{0xD83D, 'a'}, "a"},
		{"a trailing lone surrogate is dropped", []uint16{'a', 0xDE00}, "a"},
	} {
		if got := string(utf16Runes(tc.units)); got != tc.want {
			t.Errorf("%s: utf16Runes = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestKeyEquivalentLeavesTheMenusKeysAlone is the routing that keeps Cmd-Q
// quitting. See menuOwnsKeyEquivalent: the view is asked before the menu is,
// so a view that says yes to everything disables the menu bar.
func TestKeyEquivalentLeavesTheMenusKeysAlone(t *testing.T) {
	testView(t) // for the skip when AppKit is unavailable
	w := &window{q: newEventq()}
	withWindow(t, w)

	const cmd = uint64(modFlagCommand)
	if w.onKeyEquivalent(keyEvent(t, "c", 8, cmd)) {
		t.Error("the view claimed Cmd-C, so the Copy menu item would never fire")
	}
	if evs := drain(w.q); len(evs) != 0 {
		t.Errorf("the view delivered Cmd-C to the editor as well: %q", keysOf(evs))
	}

	if !w.onKeyEquivalent(keyEvent(t, "p", 35, cmd)) {
		t.Error("the view refused Cmd-P, which no menu item claims, so macOS beeps")
	}
	if got := keysOf(drain(w.q)); got != "<D-p>" {
		t.Errorf("Cmd-P reached the editor as %q, want <D-p>", got)
	}
}

// TestMenuActionTypesInTheModeOfTheLastFrame is the Edit menu doing its job:
// one item, two different key sequences, chosen by what the last painted frame
// showed.
func TestMenuActionTypesInTheModeOfTheLastFrame(t *testing.T) {
	w := &window{q: newEventq()}
	withWindow(t, w)

	w.lastMode = modeNormal
	w.onMenuAction(editPaste)
	if got := keysOf(drain(w.q)); got != `"+gP` {
		t.Errorf("Cmd-V in normal mode typed %q, want \"+gP", got)
	}

	w.lastMode = modeInsert
	w.onMenuAction(editPaste)
	if got := keysOf(drain(w.q)); got != "<C-R><C-O>+" {
		t.Errorf("Cmd-V in insert mode typed %q, want <C-R><C-O>+", got)
	}

	// An item with no binding in this mode types nothing at all, which is what
	// vim's own menu does when the mode has no mapping.
	w.lastMode = modeInsert
	w.onMenuAction(editCut)
	if evs := drain(w.q); len(evs) != 0 {
		t.Errorf("Cmd-X in insert mode typed %q, want nothing", keysOf(evs))
	}
}

// TestFocusEventsAreDelivered. The hollow caret is the only thing on screen
// that says which of two editors a keystroke would go to, and the editor can
// only draw it if the window says so.
func TestFocusEventsAreDelivered(t *testing.T) {
	w := &window{q: newEventq()}
	w.onFocus(true)
	w.onFocus(false)
	evs := drain(w.q)
	if len(evs) != 2 {
		t.Fatalf("two focus changes produced %d events", len(evs))
	}
	if ev, ok := evs[0].(FocusEvent); !ok || !ev.Focused {
		t.Errorf("first event is %#v, want FocusEvent{true}", evs[0])
	}
	if ev, ok := evs[1].(FocusEvent); !ok || ev.Focused {
		t.Errorf("second event is %#v, want FocusEvent{false}", evs[1])
	}
}
