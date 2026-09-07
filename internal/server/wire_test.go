package server

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestRoundTrip is the format as a test: what one end writes, the other end
// reads, and the version goes on whether the caller set it or not.
func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := Request{Op: OpOpen, Path: "/tmp/f.txt", Cwd: "/tmp", Wait: true}
	if err := WriteRequest(&buf, want); err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(buf.Bytes(), []byte("\n")); n != 1 || buf.Bytes()[buf.Len()-1] != '\n' {
		t.Fatalf("a request is one line ending in a newline, got %q", buf.String())
	}

	got, err := ReadRequest(NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	want.Version = Version
	if got != want {
		t.Errorf("read %+v, want %+v", got, want)
	}
}

// TestTwoRepliesOnOneConnection is the --wait exchange: opened now, closed an
// hour later, over the same reader.
func TestTwoRepliesOnOneConnection(t *testing.T) {
	var buf bytes.Buffer
	for _, k := range []Kind{ReplyOpened, ReplyClosed} {
		if err := WriteReply(&buf, Reply{Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(&buf)
	for _, want := range []Kind{ReplyOpened, ReplyClosed} {
		got, err := ReadReply(r)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != want {
			t.Fatalf("read %q, want %q", got.Kind, want)
		}
		if got.Final() != (want != ReplyOpened) {
			t.Errorf("%q reports Final %v", want, got.Final())
		}
	}
	if _, err := ReadReply(r); !errors.Is(err, io.EOF) {
		t.Errorf("after the last reply the error is %v, want EOF", err)
	}
}

// TestVersionMismatchSaysBothNumbers. A running instance from before a rebuild
// is the failure this field exists for, and "invalid character" twenty bytes
// into a JSON object is not a thing anybody can act on.
func TestVersionMismatchSaysBothNumbers(t *testing.T) {
	line := `{"version":99,"op":"open","path":"/tmp/f.txt"}` + "\n"
	_, err := ReadRequest(NewReader(strings.NewReader(line)))
	if !errors.Is(err, ErrVersion) {
		t.Fatalf("error is %v, want ErrVersion", err)
	}
	for _, want := range []string{"99", "1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestGarbageIsAnError, and not a zero-valued request that opens the empty
// path.
func TestGarbageIsAnError(t *testing.T) {
	if _, err := ReadRequest(NewReader(strings.NewReader("not json at all\n"))); err == nil {
		t.Fatal("a line that is not JSON read as a request")
	}
}

// TestLineLimitRefuses keeps an unbounded read out of a socket that sits in the
// home directory for the life of a login session.
func TestLineLimitRefuses(t *testing.T) {
	long := `{"version":1,"op":"open","path":"` + strings.Repeat("x", maxLine) + `"}` + "\n"
	_, err := ReadRequest(NewReader(strings.NewReader(long)))
	if err == nil {
		t.Fatal("a line past the limit was read")
	}
	if !strings.Contains(err.Error(), "longer than") {
		t.Errorf("error is %v, want it to say the line was too long", err)
	}
}
