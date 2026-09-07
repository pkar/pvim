//go:build darwin

package gui

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// runQueue starts a run loop on a dedicated OS thread and returns a queue
// posting to it, plus a stop function.
//
// This is the whole wake mechanism under test with no NSApplication and no
// window server: a CFRunLoop is not an AppKit thing and any thread can have
// one, which is what makes the measurement reproducible in `go test` on a box
// with no display.
func runQueue(t *testing.T) (*mainQueue, func()) {
	t.Helper()
	if err := initObjC(); err != nil {
		t.Skipf("no CoreFoundation available: %v", err)
	}

	type result struct {
		q   *mainQueue
		err error
	}
	ready := make(chan result)
	stop := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		// The run loop belongs to one thread and CFRunLoopGetCurrent creates it
		// for the thread it is called on, so this goroutine must not migrate.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(stopped)

		q, err := newMainQueue(cfRunLoopGetCurrent())
		ready <- result{q, err}
		if err != nil {
			return
		}
		defer q.stopQueue()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// A short timeout rather than a blocking run so the stop channel is
			// checked; the real main loop is NSApp.run and never polls.
			cfRunLoopRunInMode(runLoopDefaultMode, 0.01, false)
		}
	}()

	r := <-ready
	if r.err != nil {
		close(stop)
		t.Skipf("no CoreFoundation available: %v", r.err)
	}
	return r.q, func() {
		close(stop)
		<-stopped
	}
}

// TestMainQueueRunsWorkOnTheLoopThread is the claim the whole GUI rests on:
// a goroutine that is not the main thread can hand a Go closure to the thread
// that owns the run loop, and it runs there.
func TestMainQueueRunsWorkOnTheLoopThread(t *testing.T) {
	q, stop := runQueue(t)
	defer stop()

	done := make(chan int, 1)
	var mu sync.Mutex
	var order []int

	for i := 0; i < 5; i++ {
		i := i
		q.do(func() {
			mu.Lock()
			order = append(order, i)
			n := len(order)
			mu.Unlock()
			if n == 5 {
				done <- n
			}
		})
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run loop never performed the queued work")
	}

	mu.Lock()
	defer mu.Unlock()
	for i, v := range order {
		if v != i {
			t.Fatalf("work ran out of order: %v", order)
		}
	}
}

// TestMainQueueCoalescesRedraws is the measurement that chose CFRunLoopSource
// over dispatch_async_f, kept as a test so the property cannot quietly go away.
//
// A blit of the newest frame is idempotent, so a thousand redraw requests
// issued while the main thread is busy must cost one blit and not a thousand.
// An editor whose window queued one blit per keystroke would fall further
// behind the faster it was typed at, which is the one failure a text editor
// must not have.
//
// The main thread is held busy on purpose rather than raced against: the first
// redraw blocks inside the run loop until the burst has been issued, so the
// count is a fact and not a timing accident.
func TestMainQueueCoalescesRedraws(t *testing.T) {
	q, stop := runQueue(t)
	defer stop()

	var mu sync.Mutex
	blits := 0
	held := make(chan struct{})
	release := make(chan struct{})

	q.wakeRedraw(func() {
		mu.Lock()
		blits++
		mu.Unlock()
		close(held)
		<-release
	})

	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the first redraw never ran")
	}

	const n = 1000
	for i := 0; i < n; i++ {
		q.wakeRedraw(func() {
			mu.Lock()
			blits++
			mu.Unlock()
		})
	}
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		b := blits
		mu.Unlock()
		if b >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if blits != 2 {
		t.Errorf("%d redraws ran for %d requests issued while the loop was busy; want 2", blits, n+1)
	}
}

// TestMainQueueDoIsNotCoalesced is the other half of the same design: ordinary
// work is not idempotent, so every do must run even though the wake-ups that
// carry them are folded together.
func TestMainQueueDoIsNotCoalesced(t *testing.T) {
	q, stop := runQueue(t)
	defer stop()

	var mu sync.Mutex
	ran := 0
	const n = 500
	for i := 0; i < n; i++ {
		q.do(func() {
			mu.Lock()
			ran++
			mu.Unlock()
		})
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		r := ran
		mu.Unlock()
		if r == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Errorf("%d of %d work items ran", ran, n)
}
