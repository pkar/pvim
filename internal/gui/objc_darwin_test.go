//go:build darwin

package gui

import (
	"testing"

	"github.com/ebitengine/purego/objc"
)

// TestApplicationDefinedEventType pins the constant stop() posts its wake-up
// event with.
//
// -stop: only sets a flag that -run checks after it has finished dispatching an
// event, so quitting an idle application needs an event pushed at the head of
// the queue for it to finish dispatching, and the inert one meant for that is
// NSEventTypeApplicationDefined. AppKit numbers SystemDefined 14 and
// ApplicationDefined 15, an off-by-one that costs nothing and everything
// the moment anyone writes the obvious next line, a nextEventMatchingMask: over
// NSEventMaskApplicationDefined, which is 1<<15 and would never match an event
// posted as 14.
func TestApplicationDefinedEventType(t *testing.T) {
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	ev := objc.Send[objc.ID](objc.ID(objc.GetClass("NSEvent")), selOtherEventWithType,
		uint64(nsEventTypeApplicationDefined),
		NSPoint{},
		uint64(0),
		float64(0),
		int64(0),
		objc.ID(0),
		int16(0),
		int64(0),
		int64(0),
	)
	if ev == 0 {
		t.Fatal("NSEvent otherEventWithType: returned nil")
	}
	got := objc.Send[uint64](ev, objc.RegisterName("type"))
	const applicationDefined = 15
	if got != applicationDefined {
		t.Errorf("the wake-up event is type %d, want %d (NSEventTypeApplicationDefined)", got, applicationDefined)
	}
	const maskApplicationDefined = uint64(1) << applicationDefined
	if maskApplicationDefined&(uint64(1)<<got) == 0 {
		t.Errorf("an event of type %d does not match NSEventMaskApplicationDefined", got)
	}
}

// TestNSStringSurvivesInvalidUTF8 is a crash, not a cosmetic complaint.
//
// +[NSString stringWithUTF8String:] returns nil for bytes that are not valid
// UTF-8, and AppKit's setters assert on nil instead of ignoring it:
// -[NSWindow setTitle:] with nil raises NSInternalInconsistencyException
// ("Invalid parameter not satisfying: aString != nil"), nothing catches it and
// the process aborts. A path is bytes and not UTF-8, so `pvim $'bad\xff.txt'`
// reaches SetTitle on the first frame and takes the editor with it, with the
// buffer in it. Measured under purego v0.11.0: nil for ff 2e 74
// 78 74 and for 66 6f 6f c3 28.
//
// So nsString has to hand back a real NSString for any string at all, and the
// contents are checked too, because returning an empty string for a name that
// merely has one bad byte in it would pass a nil check and lose the title.
func TestNSStringSurvivesInvalidUTF8(t *testing.T) {
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	selIsEqualToString := objc.RegisterName("isEqualToString:")
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "foo.txt", "foo.txt"},
		{"empty", "", ""},
		{"a latin-1 byte in a name", "\xff.txt", "�.txt"},
		{"a truncated sequence", "foo\xc3(", "foo�("},
		{"a lone continuation byte", "a\x80b", "a�b"},
		{"valid multibyte is untouched", "héllo ✓.txt", "héllo ✓.txt"},
		{"a real U+FFFD is untouched", "�", "�"},
	} {
		got := nsString(tc.in)
		if got == 0 {
			t.Errorf("%s: nsString(%q) is nil, and -[NSWindow setTitle:] aborts the process on nil", tc.name, tc.in)
			continue
		}
		want := nsString(tc.want)
		if want == 0 {
			t.Fatalf("%s: nsString(%q) is nil for the expected string itself", tc.name, tc.want)
		}
		if !objc.Send[bool](got, selIsEqualToString, want) {
			t.Errorf("%s: nsString(%q) is not the NSString for %q", tc.name, tc.in, tc.want)
		}
	}
}

// TestToValidUTF8 is the coercion on its own, so that it is pinned on a
// machine with no window server as well as on this one. One replacement
// character per bad byte is what ranging over a Go string decodes to and what
// the rest of macOS shows for a name it cannot read.
func TestToValidUTF8(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"foo.txt", "foo.txt"},
		{"héllo ✓", "héllo ✓"},
		{"\xff.txt", "�.txt"},
		{"foo\xc3(", "foo�("},
		{"\xff\xfe", "��"},
		{"a\x80b", "a�b"},
	} {
		if got := toValidUTF8(tc.in); got != tc.want {
			t.Errorf("toValidUTF8(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
