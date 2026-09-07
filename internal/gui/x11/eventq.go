package x11

import (
	"sync"

	"github.com/pkar/pvim/internal/gui"
)

// eventq is the boundary between the X event loop and the editor goroutine:
// the loop pushes and returns, the handler pops.
//
// It is internal/gui's eventq with gui.Event in place of the local one, and it
// is copied rather than shared for the reason the two packages give everywhere
// else: they are peers, not layers, and an unexported type in one is not an API
// for the other. Thirty lines is the cheaper of the two.
//
// The reason it is an unbounded slice under a condition variable rather than a
// buffered channel is the reason it is there at all. A send on a full channel
// blocks, and the one thing the event loop must never do is block on the
// editor: a wedged handler would stop the window answering the window manager,
// which is how a window gets declared not responding. The alternative, a
// non-blocking send that drops on overflow, drops keystrokes, and an editor
// that silently loses what was typed is worse than one that falls behind.
type eventq struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []gui.Event
	closed bool
}

// newEventq returns an empty queue ready to use.
func newEventq() *eventq {
	q := &eventq{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends an event and wakes a waiting pop. It never blocks.
func (q *eventq) push(ev gui.Event) {
	q.mu.Lock()
	if !q.closed {
		q.events = append(q.events, ev)
	}
	q.mu.Unlock()
	q.cond.Signal()
}

// pop returns the oldest event, blocking until there is one. The second result
// is false once the queue is closed and drained, which is how the handler
// goroutine learns to stop.
func (q *eventq) pop() (gui.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.events) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.events) == 0 {
		return nil, false
	}
	ev := q.events[0]
	q.events = q.events[1:]
	return ev, true
}

// close stops the queue. Events already queued are still delivered, so a
// CloseEvent pushed just before this one is not lost.
func (q *eventq) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}
