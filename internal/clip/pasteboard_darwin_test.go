//go:build darwin

package clip

import (
	"errors"
	"os"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/register"
)

// direct is the Runner a test uses: the work runs on whatever goroutine asked
// for it, right now.
//
// That is correct here and would be wrong in the editor. The rule the real
// Runner has to keep is that the call must not be made from the run loop thread
// itself, because gui.OnMain blocks until the run loop has run the work and the
// run loop cannot run it while it is inside the call. A test has no run loop to
// be on the wrong side of, which is also why every autorelease in this package
// is inside a pool of its own: with no run loop there is no AppKit pool.
func direct(fn func()) error { fn(); return nil }

// writesAllowed reports whether this run may put things on the desktop's
// clipboard.
//
// Off by default, and the reason is not caution about the code. A test that
// writes the general pasteboard destroys whatever the person running `make
// check` had copied, which may be a file in the Finder or a password, and
// putting it back afterwards is not possible: only the string can be restored,
// not the image, the file URL or another application's private type that was
// beside it. So the write half runs when it is asked for by name:
//
//	PVIM_CLIP_PASTEBOARD=1 go test ./internal/clip/...
//
// The read half needs no permission and runs always, because it is the half
// that proves the whole purego path -- the frameworks load, the classes and
// selectors resolve, generalPasteboard answers, changeCount answers, a string
// comes back out of an NSString into Go memory -- and it changes nothing.
func writesAllowed(t *testing.T) bool {
	t.Helper()
	if os.Getenv("PVIM_CLIP_PASTEBOARD") == "" {
		t.Skip("set PVIM_CLIP_PASTEBOARD=1 to let this test replace the desktop clipboard")
		return false
	}
	return true
}

// TestReadTheRealPasteboard is the smoke test for the whole binding wall. It
// asserts almost nothing about the contents, because the contents are whatever
// the person at the machine last copied; what it asserts is that reading them
// works and costs one trip.
func TestReadTheRealPasteboard(t *testing.T) {
	trips := 0
	c, err := New(func(fn func()) error { trips++; return direct(fn) })
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	v, err := c.Read()
	if errors.Is(err, ErrNoPasteboard) {
		// No login session behind this run: an ssh into a Mac nobody is
		// logged in at, or a build box. There is no pasteboard to read and
		// that is the machine, not the code.
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if trips != 1 {
		t.Errorf("the first read took %d trips to the run loop, want 1", trips)
	}
	t.Logf("the clipboard holds %d lines, %s", len(v.Lines), v.Type)

	if _, err := c.Read(); err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if trips != 2 {
		t.Errorf("two reads took %d trips, want 2; every call is exactly one", trips)
	}
}

// TestNewLoadsTheFrameworks: a Pasteboard that came back without an error has
// AppKit mapped and every class and selector resolved, because New does that
// work rather than leaving it for the first yank.
func TestNewLoadsTheFrameworks(t *testing.T) {
	if _, err := New(direct); err != nil {
		t.Fatalf("New: %v", err)
	}
	if classPasteboard == 0 || selChangeCount == 0 || nameVim == 0 {
		t.Errorf("New returned with class %d, selector %d, type name %d", classPasteboard, selChangeCount, nameVim)
	}
}

// TestRunnerErrorsTravel: when the run loop is gone the Runner says so, and the
// caller gets an error rather than a silently empty register. Losing a yank
// quietly is the one thing this package must not do.
func TestRunnerErrorsTravel(t *testing.T) {
	boom := os.ErrClosed
	c, err := New(func(func()) error { return boom })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Read(); err == nil {
		t.Error("Read with a dead run loop returned no error")
	}
	if err := c.Write(register.Char([]byte("alpha"))); err == nil {
		t.Error("Write with a dead run loop returned no error")
	}
}

// TestWriteRefusesTextThatIsNotUTF8, without going near the run loop.
//
// The refusal is in Go and before the trip on purpose: an NSString cannot be
// made from bytes it cannot decode, initWithBytes: answers nil, and setString:
// with a nil raises an Objective-C exception that no Go process catches. So the
// Runner must not be called at all, and the board must be left as it was --
// a yank that cannot reach the clipboard must not also destroy what was on it.
func TestWriteRefusesTextThatIsNotUTF8(t *testing.T) {
	c, err := New(func(func()) error {
		t.Error("Write went to the run loop with text it cannot put on the board")
		return nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 0xe9 alone is latin-1 for e-acute and is not a UTF-8 sequence. A pvim
	// buffer can hold it; vim's cannot, because vim converts a latin-1 file on
	// the way in and this editor does not.
	if err := c.Write(register.Char([]byte("caf\xe9"))); !errors.Is(err, ErrNotUTF8) {
		t.Errorf("Write of latin-1 bytes gave %v, want %v", err, ErrNotUTF8)
	}
}

// TestRoundTripThroughTheRealPasteboard is the one that needs permission. It
// covers the case the changeCount cache exists for and the case it does not:
// a value written and read straight back keeps its blockwise width, and the
// same value read after another process has cleared the board comes back typed
// by the plist, which for a block means the width is recomputed.
func TestRoundTripThroughTheRealPasteboard(t *testing.T) {
	if !writesAllowed(t) {
		return
	}
	c, err := New(direct)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, tc := range []struct {
		name  string
		in    register.Value
		width int // what a read that missed the cache should answer
	}{
		{"charwise", register.Char([]byte("alpha")), 0},
		{"charwise over two lines", register.Char([]byte("alpha"), []byte("beta")), 0},
		{"linewise", register.LineValue([]byte("alpha")), 0},
		{"linewise over two lines", register.LineValue([]byte("alpha"), []byte("beta")), 0},
		{"blockwise", register.BlockValue(4, []byte("abcd"), []byte("ef")), 4},
		{"a $ block, which loses its ToEOL", register.Value{
			Lines: [][]byte{[]byte("alpha"), []byte("be")},
			Type:  register.TypeBlock, Width: 5, ToEOL: true,
		}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.Write(tc.in); err != nil {
				t.Fatalf("Write: %v", err)
			}

			// Cached: the changeCount has not moved, so this is the value that
			// went in, width, ToEOL and all.
			got, err := c.Read()
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if got.Type != tc.in.Type || got.String() != tc.in.String() || got.Width != tc.in.Width {
				t.Errorf("cached read gave %s %q width %d, want %s %q width %d",
					got.Type, got.String(), got.Width, tc.in.Type, tc.in.String(), tc.in.Width)
			}

			// Not cached: a second Pasteboard has never written this board, so
			// it reads the string and the plist, exactly as pvim would after
			// somebody copied in another application.
			other, err := New(direct)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err = other.Read()
			if err != nil {
				t.Fatalf("Read from a fresh Pasteboard: %v", err)
			}
			if got.Type != tc.in.Type {
				t.Errorf("a fresh read gave %s, want %s", got.Type, tc.in.Type)
			}
			if got.String() != tc.in.String() {
				t.Errorf("a fresh read gave %q, want %q", got.String(), tc.in.String())
			}
			if got.Width != tc.width {
				t.Errorf("a fresh read gave width %d, want %d", got.Width, tc.width)
			}
			if got.ToEOL {
				t.Error("a fresh read kept ToEOL; the pasteboard cannot carry it and neither does vim")
			}
		})
	}
}

// TestTextFromAnotherApplication is the case with no VimPboardType on the
// board: the trailing newline is the only thing that says linewise, which is
// vim's rule and the reason pbcopy of a whole line pastes as a line.
func TestTextFromAnotherApplication(t *testing.T) {
	if !writesAllowed(t) {
		return
	}
	c, err := New(direct)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A charwise write puts no newline on the board, so reading it back on a
	// Pasteboard that did not write it types it charwise; a linewise one does,
	// and types linewise. That is the whole of the rule and it needs no second
	// application to demonstrate.
	for _, tc := range []struct {
		in   register.Value
		want register.Type
	}{
		{register.Char([]byte("alpha")), register.TypeChar},
		{register.LineValue([]byte("alpha")), register.TypeLine},
	} {
		if err := c.Write(tc.in); err != nil {
			t.Fatalf("Write: %v", err)
		}
		other, err := New(direct)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got, err := other.Read()
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Type != tc.want {
			t.Errorf("%q came back %s, want %s", tc.in.String(), got.Type, tc.want)
		}
	}
}

// BenchmarkPasteboardReadCached is a read that finds its own last write: one
// changeCount and nothing else. It is the number the doc comment quotes for
// what a put costs under 'clipboard'.
//
// Every benchmark here replaces the desktop clipboard, so they all need
// PVIM_CLIP_PASTEBOARD=1. Benchmarks do not run without -bench anyway, so this
// is belt and braces.
func BenchmarkPasteboardReadCached(b *testing.B) {
	c := benchClip(b)
	if err := c.Write(register.Char([]byte("alpha"))); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := c.Read(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPasteboardReadFresh is a read that has to fetch the string and the
// plist, which is what happens after any other application has copied.
func BenchmarkPasteboardReadFresh(b *testing.B) {
	c := benchClip(b)
	if err := c.Write(register.Char([]byte("alpha"))); err != nil {
		b.Fatal(err)
	}
	p := c.(*Pasteboard)
	for b.Loop() {
		p.known = false // as if somebody else had written since
		if _, err := c.Read(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPasteboardWrite is a yank under 'clipboard': clearContents, the
// string, the plist. clearContents is most of it, because it is what tells
// every other process on the desktop that the board has changed.
func BenchmarkPasteboardWrite(b *testing.B) {
	c := benchClip(b)
	v := register.LineValue([]byte("alpha"))
	for b.Loop() {
		if err := c.Write(v); err != nil {
			b.Fatal(err)
		}
	}
}

func benchClip(b *testing.B) Clipboard {
	b.Helper()
	if os.Getenv("PVIM_CLIP_PASTEBOARD") == "" {
		b.Skip("set PVIM_CLIP_PASTEBOARD=1 to let this benchmark replace the desktop clipboard")
	}
	c, err := New(direct)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

// BenchmarkPasteboardWriteSteps is what a write is made of, measured
// cumulatively: the clear on its own, the clear plus the string, and the clear
// plus the string plus vim's private type, which is the whole of put.
//
// Cumulative and not one call each, because one call each answers the wrong
// question. Measured both ways: a setString:forType: repeated on a board this
// process already owns costs 10 microseconds, and the same call made after a
// clearContents costs the better part of a hundred, because what is expensive
// is handing the text to the pasteboard server as a new owner and not the
// message send. Three separate benchmarks sum to 75 microseconds where a real
// write costs 230, and the difference is not overhead, it is the step being
// measured in a state a real write is never in. So each step here pays for the
// ones before it and the doc comment quotes the differences.
//
// Every step runs inside one withPool on one locked thread, so a pool push is
// not in the number.
func BenchmarkPasteboardWriteSteps(b *testing.B) {
	benchClip(b) // the permission gate, and it loads the frameworks

	b.Run("clear", func(b *testing.B) {
		withPool(func() {
			pb := generalPasteboard()
			for b.Loop() {
				pb.Send(selClearContents)
			}
		})
	})

	b.Run("clear+setString", func(b *testing.B) {
		withPool(func() {
			pb := generalPasteboard()
			s := newNSString([]byte("alpha\n"))
			defer s.Send(selRelease)
			for b.Loop() {
				pb.Send(selClearContents)
				if !objc.Send[bool](pb, selSetStringForType, s, nameString) {
					b.Fatal("setString:forType: refused")
				}
			}
		})
	})

	b.Run("clear+setString+setPropertyList", func(b *testing.B) {
		withPool(func() {
			pb := generalPasteboard()
			s := newNSString([]byte("alpha\n"))
			defer s.Send(selRelease)
			num := objc.ID(classNumber).Send(selAlloc).Send(selInitWithInt, motionLine)
			defer num.Send(selRelease)
			items := [2]objc.ID{num, s}
			arr := objc.ID(classArray).Send(selAlloc).Send(selInitWithObjects, unsafe.Pointer(&items[0]), 2)
			defer arr.Send(selRelease)
			for b.Loop() {
				pb.Send(selClearContents)
				if !objc.Send[bool](pb, selSetStringForType, s, nameString) {
					b.Fatal("setString:forType: refused")
				}
				objc.Send[bool](pb, selSetPropListForTyp, arr, nameVim)
			}
		})
	})
}
