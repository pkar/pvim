//go:build darwin

package gui

import (
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/raster"
)

// Every way out of this window, driven through the real delegate.
//
// There are four, and all four have to arrive at the editor as the same
// CloseEvent rather than as a dead process: the red button, Cmd-Q through the
// menu, Quit from the Dock menu, and a logout. The last two are -terminate:,
// which is on the do-not-send list and which without an answer ends the
// process with a modified buffer still in it.

// delegateInstance returns an instance of the registered delegate class with a
// window behind it.
func delegateInstance(t *testing.T) (*window, objc.ID) {
	t.Helper()
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	cls, err := registerDelegateClass()
	if err != nil {
		t.Fatalf("registering pvimDelegate: %v", err)
	}
	obj := objc.ID(cls).Send(selAlloc).Send(selInit)
	if obj == 0 {
		t.Fatal("pvimDelegate init returned nil")
	}
	face, err := raster.DefaultFace(1)
	if err != nil {
		t.Skipf("no Monaco: %v", err)
	}
	w := &window{q: newEventq(), face: face, scale: 1, rows: 26, cols: 80}
	withWindow(t, w)
	return w, obj
}

// TestEveryQuitPathIsACloseEvent drives the three selectors and checks that
// each pushes one CloseEvent and nothing else.
func TestEveryQuitPathIsACloseEvent(t *testing.T) {
	w, delegate := delegateInstance(t)

	// The red button. NO keeps the window up: the editor decides, and under
	// 'confirm' it has a prompt to put on the command line first.
	if objc.Send[bool](delegate, objc.RegisterName("windowShouldClose:"), objc.ID(0)) {
		t.Error("windowShouldClose: returned YES; AppKit would close the window before the editor answered")
	}
	// Cmd-Q, through the one item in the application menu.
	delegate.Send(objc.RegisterName("pvimQuit:"), objc.ID(0))
	// Quit from the Dock menu, and a logout.
	if reply := objc.Send[uint64](delegate, objc.RegisterName("applicationShouldTerminate:"), objc.ID(0)); reply != nsTerminateCancel {
		t.Errorf("applicationShouldTerminate: returned %d, want NSTerminateCancel (%d): the process would stop with the buffer unsaved",
			reply, nsTerminateCancel)
	}

	got := drain(w.q)
	if len(got) != 3 {
		t.Fatalf("three ways out pushed %d events, want 3: %v", len(got), got)
	}
	for i, ev := range got {
		if _, ok := ev.(CloseEvent); !ok {
			t.Errorf("event %d is %T, want a CloseEvent", i, ev)
		}
	}
}

// TestFocusNotificationsReachTheQueue is TestFocusEventsAreDelivered from the
// other side: that one calls onFocus, which is Go, and this one sends the two
// notification selectors, which is the half a typo would break. AppKit calls a
// window delegate method by name and silently calls nothing if the name is
// wrong, so the window would simply never learn it had lost focus and the
// caret would stay solid behind another application forever.
func TestFocusNotificationsReachTheQueue(t *testing.T) {
	w, delegate := delegateInstance(t)

	delegate.Send(objc.RegisterName("windowDidBecomeKey:"), objc.ID(0))
	delegate.Send(objc.RegisterName("windowDidResignKey:"), objc.ID(0))

	got := drain(w.q)
	if len(got) != 2 {
		t.Fatalf("two notifications pushed %d events: %v", len(got), got)
	}
	if ev, ok := got[0].(FocusEvent); !ok || !ev.Focused {
		t.Errorf("becoming key gave %#v, want FocusEvent{Focused: true}", got[0])
	}
	if ev, ok := got[1].(FocusEvent); !ok || ev.Focused {
		t.Errorf("resigning key gave %#v, want FocusEvent{Focused: false}", got[1])
	}
}
