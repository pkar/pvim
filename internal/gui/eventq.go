package gui

import "sync"

// eventq is the boundary between the platform's main thread and the editor
// goroutine: every callback pushes here and returns, and the handler goroutine
// pops.
//
// It is an unbounded slice under a condition variable and not a buffered
// channel, for one reason. A send on a full channel blocks, and the one thing
// the main thread must never do is block on the editor: a wedged handler would
// stop the window from redrawing, resizing or quitting, and the beachball would
// be the editor's fault rather than the OS's. The alternative, a non-blocking
// send that drops on overflow, drops keystrokes, and an editor that silently
// loses what was typed is worse than one that falls behind.
//
// Unbounded is safe here because the producer is a person typing and the
// consumer is a text editor. If it ever is not, the fix is backpressure into the
// event source, not a smaller buffer.
type eventq struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []Event
	closed bool
}

// newEventq returns an empty queue ready to use.
func newEventq() *eventq {
	q := &eventq{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends an event and wakes a waiting pop. It never blocks.
func (q *eventq) push(ev Event) {
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
func (q *eventq) pop() (Event, bool) {
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
