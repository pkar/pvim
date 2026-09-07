//go:build linux

package clip

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/pkar/pvim/internal/register"
)

// The X11 clipboard's tests, and a warning about them.
//
// None of this has ever been executed. It is written on a machine with no
// Linux, no X server and no way to run a linux/amd64 binary, so what has
// happened to this file is `GOOS=linux CGO_ENABLED=0 go vet ./internal/clip/`
// and nothing else. The parts of this package that are actually exercised are
// in x11sel_test.go, which carries no build tag on purpose: the wire formats,
// the INCR chunking and the Latin-1 conversion are pure functions and they run
// in every `go test ./...` on any platform.
//
// What is below is the part that needs a connection, or that needs one field of
// a struct that has one. It is here so that the first `go test ./...` on a
// Linux box has something to say.

// fakeAtoms is an x11Clipboard with atoms filled in and no connection, which is
// enough for every method that only decides what to offer or how to read
// something. Distinct numbers so a mix-up between two targets is a failure and
// not a coincidence.
func fakeAtoms() *x11Clipboard {
	return &x11Clipboard{a: atoms{
		clipboard:  100,
		targets:    101,
		timestamp:  102,
		incr:       103,
		utf8String: 104,
		textPlain:  105,
		text:       106,
		vimText:    107,
		vimEncText: 108,
		prop:       109,
		timeProp:   110,
	}}
}

// TestNewRefusesWithNoDisplay is the case a build box and an ssh session hit,
// and the one that has to stay an ErrUnsupported rather than a hang: cmd/pvim
// installs no clipboard then and "* and "+ are ordinary registers.
func TestNewRefusesWithNoDisplay(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if _, err := New(nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("New with no DISPLAY gave %v, want %v", err, ErrUnsupported)
	}
	if _, _, err := NewSelections(nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("NewSelections with no DISPLAY gave %v, want %v", err, ErrUnsupported)
	}
}

// TestNewIgnoresTheRunner pins the difference from darwin. There is no thread
// to borrow here, so a Runner is accepted and unused, and passing one must not
// be an error: cmd/pvim hands the same thing to both platforms.
func TestNewIgnoresTheRunner(t *testing.T) {
	t.Setenv("DISPLAY", "")
	run := Runner(func(fn func()) error { fn(); return nil })
	if _, err := New(run); !errors.Is(err, ErrUnsupported) {
		t.Errorf("New with a runner gave %v, want the same refusal as without one: %v", err, ErrUnsupported)
	}
	if _, err := New(nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("New with no runner gave %v, want %v", err, ErrUnsupported)
	}
}

func TestMaxPropBytes(t *testing.T) {
	for _, tc := range []struct {
		max  uint16
		want int
	}{
		{65535, 262116},
		{4096, 16360},
		{4, 0},
		{0, 0},
	} {
		if got := maxPropBytes(tc.max); got != tc.want {
			t.Errorf("maxPropBytes(%d) = %d, want %d", tc.max, got, tc.want)
		}
	}
}

// TestOfferHasEveryTargetItPromises keeps the TARGETS list and the table a
// request is answered from in step. A target advertised and not answerable is a
// requestor that asks and is refused, which is legal and is also the shape of
// bug nobody finds.
func TestOfferHasEveryTargetItPromises(t *testing.T) {
	c := fakeAtoms()
	v := register.Char([]byte("alpha"))
	data := c.offer(v, bytesFor(v))

	for _, target := range c.targetList() {
		switch target {
		case c.a.targets, c.a.timestamp:
			continue // answered by convert, not out of the table
		}
		if _, ok := data[target]; !ok {
			t.Errorf("target %d is in the TARGETS list and not in the table", target)
		}
	}
}

// TestOfferRefusesSTRINGForTextLatin1CannotHold is the one conditional target,
// and the reason it is conditional: STRING is Latin-1 by the ICCCM and handing
// UTF-8 over it is what makes an accented character arrive elsewhere as two.
func TestOfferRefusesSTRINGForTextLatin1CannotHold(t *testing.T) {
	c := fakeAtoms()
	fits := register.Char([]byte("café"))
	if _, ok := c.offer(fits, bytesFor(fits))[xproto.AtomString]; !ok {
		t.Error("Latin-1 text is not offered on STRING")
	}
	wide := register.Char([]byte("snowman ☃"))
	if _, ok := c.offer(wide, bytesFor(wide))[xproto.AtomString]; ok {
		t.Error("text Latin-1 cannot hold is offered on STRING")
	}
}

func TestDecodePerTarget(t *testing.T) {
	c := fakeAtoms()
	for _, tc := range []struct {
		name   string
		target xproto.Atom
		data   []byte
		want   register.Value
		ok     bool
	}{
		{"vimenc keeps linewise", c.a.vimEncText, append([]byte{1}, "utf-8\x00alpha\n"...),
			register.LineValue([]byte("alpha")), true},
		{"vim text keeps linewise", c.a.vimText, []byte{1, 'a', '\n'},
			register.LineValue([]byte("a")), true},
		{"utf8 falls back to the newline rule", c.a.utf8String, []byte("alpha\n"),
			register.LineValue([]byte("alpha")), true},
		{"text/plain is the same", c.a.textPlain, []byte("alpha"),
			register.Char([]byte("alpha")), true},
		{"string is latin-1", xproto.AtomString, []byte{'c', 'a', 'f', 0xe9},
			register.Char([]byte("café")), true},
		{"a utf8 target that is not utf8 is refused", c.a.utf8String, []byte{0xff, 0xfe}, register.Value{}, false},
		{"an unknown target is refused", 999, []byte("alpha"), register.Value{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := c.decode(tc.target, tc.data)
			if ok != tc.ok {
				t.Fatalf("decode said %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.Type != tc.want.Type || !reflect.DeepEqual(got.Lines, tc.want.Lines) {
				t.Errorf("decoded to %v %q, want %v %q", got.Type, got.Lines, tc.want.Type, tc.want.Lines)
			}
		})
	}
}

// TestReadTargetsAskVimFirst is the order that decides whether a linewise yank
// pasted between two pvims stays linewise.
func TestReadTargetsAskVimFirst(t *testing.T) {
	c := fakeAtoms()
	want := []xproto.Atom{c.a.vimEncText, c.a.vimText, c.a.utf8String, xproto.AtomString}
	if got := c.readTargets(); !reflect.DeepEqual(got, want) {
		t.Errorf("read targets are %v, want %v", got, want)
	}
}

// TestLiveSelections is the only test here that talks to a server, and it skips
// itself when there is not one. On a Linux desktop it is the round trip that
// says the whole file works: claim a selection, read it back, and get the same
// value with the same type.
//
// It reads back through the same process, which is not the interesting case --
// startRead short-circuits an owned selection and answers from memory -- so it
// checks what that path is for and no more. The cross-application checks are in
// the manual checks for internal/gui/x11 and need a person and a browser.
func TestLiveSelections(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no DISPLAY: this needs a running X server")
	}
	c, err := newX11()
	if err != nil {
		t.Skipf("no usable X connection: %v", err)
	}
	defer c.close()

	star := oneSelection{c: c, sel: xproto.AtomPrimary}
	plus := oneSelection{c: c, sel: c.a.clipboard}
	line := register.LineValue([]byte("alpha"), []byte("beta"))
	if err := plus.Write(line); err != nil {
		t.Fatalf("writing CLIPBOARD: %v", err)
	}
	got, err := plus.Read()
	if err != nil {
		t.Fatalf("reading CLIPBOARD: %v", err)
	}
	if got.Type != register.TypeLine || len(got.Lines) != 2 {
		t.Errorf("CLIPBOARD came back %v %q, want two linewise lines", got.Type, got.Lines)
	}
	block := register.BlockValue(2, []byte("al"), []byte("be"))
	if err := star.Write(block); err != nil {
		t.Fatalf("writing PRIMARY: %v", err)
	}
	if got, err := star.Read(); err != nil || got.Type != register.TypeBlock {
		t.Errorf("PRIMARY came back %v (%v), want a block", got, err)
	}
}
