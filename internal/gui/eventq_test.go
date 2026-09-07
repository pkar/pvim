package gui

import (
	"sync"
	"testing"
)

// TestEventqOrder is the whole contract the Handler doc relies on: events
// arrive one at a time and in the order they happened, so an editor written
// against it needs no locking.
func TestEventqOrder(t *testing.T) {
	q := newEventq()
	want := []Event{
		ResizeEvent{Rows: 28, Cols: 80},
		KeyEvent{},
		MouseEvent{Row: 3, Col: 4},
		CloseEvent{},
	}
	for _, ev := range want {
		q.push(ev)
	}
	q.close()

	for i, w := range want {
		got, ok := q.pop()
		if !ok {
			t.Fatalf("pop %d: queue closed early", i)
		}
		if got != w {
			t.Fatalf("pop %d = %#v, want %#v", i, got, w)
		}
	}
	if _, ok := q.pop(); ok {
		t.Error("pop after the last event returned one")
	}
}

// TestEventqCloseDrains checks that closing does not throw away what is already
// queued. A CloseEvent pushed by the Quit menu item and a close racing behind it
// must not lose the CloseEvent, or Cmd-Q would sometimes do nothing.
func TestEventqCloseDrains(t *testing.T) {
	q := newEventq()
	q.push(CloseEvent{})
	q.close()
	if _, ok := q.pop(); !ok {
		t.Fatal("the event queued before close was dropped")
	}
}

// TestEventqPushNeverBlocks is the reason this is a slice and not a channel.
// The main thread pushes from a callback and must return to AppKit immediately
// whatever the editor is doing, so a producer far ahead of its consumer has to
// keep running.
func TestEventqPushNeverBlocks(t *testing.T) {
	q := newEventq()
	const n = 100000
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			q.push(KeyEvent{})
		}
		close(done)
	}()
	<-done // would deadlock on a bounded queue with no reader

	q.close()
	count := 0
	for {
		if _, ok := q.pop(); !ok {
			break
		}
		count++
	}
	if count != n {
		t.Errorf("popped %d events, pushed %d", count, n)
	}
}

// TestEventqConcurrent runs a producer and a consumer against each other, which
// is the arrangement in the running editor, under the race detector.
func TestEventqConcurrent(t *testing.T) {
	q := newEventq()
	const n = 10000

	var wg sync.WaitGroup
	wg.Add(1)
	got := 0
	go func() {
		defer wg.Done()
		for {
			if _, ok := q.pop(); !ok {
				return
			}
			got++
		}
	}()

	for i := 0; i < n; i++ {
		q.push(KeyEvent{})
	}
	q.close()
	wg.Wait()

	if got != n {
		t.Errorf("consumer saw %d of %d events", got, n)
	}
}
