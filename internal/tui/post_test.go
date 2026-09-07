package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/key"
)

// Post, which is the seam cmd/pvim's socket, its -s script and its prompts all
// reach the terminal's event loop through.
//
// Everything here runs the real loop over the fake driver: a posted event has
// to come out of the same channel a typed one does, in the order it went in,
// or the socket and the keyboard are two different editors.

// TestPostReachesTheLoopInOrder is the whole contract in one run: two events
// injected from another goroutine arrive as events, in the order they were
// posted, after the ResizeEvent Start always sends first.
func TestPostReachesTheLoopInOrder(t *testing.T) {
	term, _, _, _ := testTerminal(t, vimrcOptions())

	errs := make(chan error, 1)
	var got []Event
	err := term.Loop(func(c Client, ev Event) error {
		got = append(got, ev)
		if len(got) == 1 {
			// Not before the loop is running: Post blocks until the loop has
			// the event, so a poster on this goroutine would wait for itself.
			go func() {
				errs <- errors.Join(
					term.Post(KeyEvent{Key: key.Rune('x')}),
					term.Post(WakeEvent{}),
				)
			}()
		}
		if len(got) >= 3 {
			return errDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDone) {
		t.Fatalf("Loop: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("Post: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("the loop saw %d events, want 3: %#v", len(got), got)
	}
	if _, ok := got[0].(ResizeEvent); !ok {
		t.Errorf("the first event is %#v, want the ResizeEvent Start sends", got[0])
	}
	k, ok := got[1].(KeyEvent)
	if !ok || k.Key != key.Rune('x') {
		t.Errorf("the second event is %#v, want a KeyEvent for x", got[1])
	}
	if _, ok := got[2].(WakeEvent); !ok {
		t.Errorf("the third event is %#v, want a WakeEvent", got[2])
	}
}

// TestPostAnswersWhenThereIsNoLoop. Every one of these is reachable in the
// editor -- a socket bound before the frontend is up, a request that lands
// during shutdown -- and the answer has to be an error rather than a caller
// parked on a channel nobody will ever read, because the caller is another
// person's shell.
func TestPostAnswersWhenThereIsNoLoop(t *testing.T) {
	t.Run("before Start", func(t *testing.T) {
		term, _, _, _ := testTerminal(t, vimrcOptions())
		if err := term.Post(WakeEvent{}); !errors.Is(err, ErrNotRunning) {
			t.Errorf("Post before Start = %v, want ErrNotRunning", err)
		}
	})

	t.Run("after Stop", func(t *testing.T) {
		term, _, _, _ := testTerminal(t, vimrcOptions())
		if err := term.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := term.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := term.Post(WakeEvent{}); !errors.Is(err, ErrNotRunning) {
			t.Errorf("Post after Stop = %v, want ErrNotRunning", err)
		}
	})

	// A hangup ends the decoding goroutine without anybody calling Stop, so
	// the "is it stopping" flag is still false and only the decoder's own exit
	// says the event has nowhere to go. Before the gone channel this blocked
	// until something else closed the terminal.
	t.Run("after a hangup", func(t *testing.T) {
		term, _, w, _ := testTerminal(t, vimrcOptions())
		if err := term.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer term.Stop()
		<-term.Events() // the ResizeEvent, so the loop is past setup
		w.Close()
		if ev, ok := <-term.Events(); !ok {
			t.Fatal("the events channel closed with no CloseEvent on it")
		} else if _, isClose := ev.(CloseEvent); !isClose {
			t.Fatalf("a hangup produced %#v, want a CloseEvent", ev)
		}

		done := make(chan error, 1)
		go func() { done <- term.Post(WakeEvent{}) }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRunning) {
				t.Errorf("Post after a hangup = %v, want ErrNotRunning", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Post after a hangup never returned")
		}
	})
}

// TestPostDoesNotWriteToTheTerminal. An injected event is an event and not
// output: nothing about it should reach the tty, which is what a test that
// looked only at the event channel would not notice.
func TestPostDoesNotWriteToTheTerminal(t *testing.T) {
	term, _, _, out := testTerminal(t, vimrcOptions())
	if err := term.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer term.Stop()
	<-term.Events()
	out.Reset()

	done := make(chan error, 1)
	go func() { done <- term.Post(KeyEvent{Key: key.Rune('q')}) }()
	if ev, ok := <-term.Events(); !ok {
		t.Fatal("the events channel closed")
	} else if _, isKey := ev.(KeyEvent); !isKey {
		t.Fatalf("got %#v, want the posted KeyEvent", ev)
	}
	if err := <-done; err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got := out.String(); got != "" {
		t.Errorf("Post wrote %q to the terminal", got)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("Post wrote an escape sequence: %q", out.String())
	}
}
