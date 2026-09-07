package substitute

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
)

// session is a buffer, an option state and a substitute state, which is
// everything a ":s" needs and nothing else. It exists so that a table case
// reads like the ex command it came from.
type session struct {
	buf *text.Buffer
	opt options.Options
	st  State
	cur text.Pos
	msg []string
}

func newSession(lines ...string) *session {
	s := &session{
		buf: text.Read([]byte(strings.Join(lines, "\n") + "\n")),
		opt: options.Defaults(),
		cur: text.Pos{Line: 1, Col: 0},
	}
	return s
}

// set applies a ":set" line, so a case can say "report=0" the way it was typed
// at the vim that answered it.
func (s *session) set(t *testing.T, args string) {
	t.Helper()
	if _, err := s.opt.ApplyLine(args, options.Both); err != nil {
		t.Fatalf("set %s: %v", args, err)
	}
}

// sub runs one substitute. spec is the range and the command, split at the
// first space or at the point the range stops looking like one: "%" and
// "/a/X/g" arrive as "%", "/a/X/g".
func (s *session) sub(t *testing.T, kind Kind, rng, args string) error {
	t.Helper()
	first, last := s.resolve(t, rng)
	c, err := Parse(kind, args, &s.st, &s.opt)
	if err != nil {
		return err
	}
	res, err := Do(Request{
		Buf: s.buf, First: first, Last: last,
		Cmd: c, Cursor: s.cur, Opt: &s.opt, State: &s.st,
	})
	if res.Cursor.Line > 0 {
		s.cur = res.Cursor
	}
	if res.Message != "" {
		s.msg = append(s.msg, res.Message)
	}
	if res.Print != "" {
		s.msg = append(s.msg, res.Print)
	}
	return err
}

// resolve turns the small subset of ranges these tests use into two line
// numbers. internal/ex owns the real one; this is "%", "N", "N,M" and "", and
// "" is the cursor line, the way a bare ":s" means it.
func (s *session) resolve(t *testing.T, rng string) (int, int) {
	t.Helper()
	switch {
	case rng == "":
		return s.cur.Line, s.cur.Line
	case rng == "%":
		return 1, s.buf.LineCount()
	case strings.Contains(rng, ","):
		a, b, _ := strings.Cut(rng, ",")
		return atoi(t, a), atoi(t, b)
	default:
		n := atoi(t, rng)
		return n, n
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("bad range %q: %v", s, err)
	}
	return n
}

// dump is the buffer as one string with the lines separated by "|", which is
// how every expectation in these tables is written: a case that is wrong shows
// the whole buffer in the failure and not a line number.
func (s *session) dump() string {
	var out []string
	for n := 1; n <= s.buf.LineCount(); n++ {
		out = append(out, string(s.buf.Line(n)))
	}
	return strings.Join(out, "|")
}
