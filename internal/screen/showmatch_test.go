package screen

import (
	"testing"
	"time"

	"github.com/pkar/pvim/internal/text"
)

const pairs = "(:),{:},[:]"

func TestFindMatch(t *testing.T) {
	b := text.Read([]byte("if (a(b)c) {\n  x[1]\n}\n"))
	cases := []struct {
		name     string
		from     text.Pos
		want     text.Pos
		wantFind bool
	}{
		{"open to close, nested", text.Pos{Line: 1, Col: 3}, text.Pos{Line: 1, Col: 9}, true},
		{"close to open, nested", text.Pos{Line: 1, Col: 9}, text.Pos{Line: 1, Col: 3}, true},
		{"inner pair", text.Pos{Line: 1, Col: 5}, text.Pos{Line: 1, Col: 7}, true},
		{"across lines", text.Pos{Line: 1, Col: 11}, text.Pos{Line: 3, Col: 0}, true},
		{"back across lines", text.Pos{Line: 3, Col: 0}, text.Pos{Line: 1, Col: 11}, true},
		{"bracket", text.Pos{Line: 2, Col: 3}, text.Pos{Line: 2, Col: 5}, true},
		{"not a bracket", text.Pos{Line: 1, Col: 0}, text.Pos{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := FindMatch(b, c.from, pairs)
			if ok != c.wantFind {
				t.Fatalf("found = %v, want %v", ok, c.wantFind)
			}
			if ok && got != c.want {
				t.Errorf("match at %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestFindMatchUnbalanced(t *testing.T) {
	b := text.Read([]byte("a)b\n"))
	if _, ok := FindMatch(b, text.Pos{Line: 1, Col: 1}, pairs); ok {
		t.Error("a closing bracket with no opener found a match")
	}
}

// 'showmatch' hands back a position and a duration and does not sleep. The
// duration is 'matchtime' tenths of a second, which vim leaves at 5 and so
// does the vimrc.
func TestShowMatch(t *testing.T) {
	b := text.Read([]byte("call(a, b)\n"))
	hop, ok := ShowMatch(b, text.Pos{Line: 1, Col: 9}, pairs, 1, 1)
	if !ok {
		t.Fatal("typing ')' produced no hop")
	}
	if want := (text.Pos{Line: 1, Col: 4}); hop.Pos != want {
		t.Errorf("hop to %+v, want %+v", hop.Pos, want)
	}
	if hop.For != 500*time.Millisecond {
		t.Errorf("hop lasts %v, want 500ms", hop.For)
	}
}

// Vim does not hop to a line it is not showing, and neither does this.
func TestShowMatchOffScreen(t *testing.T) {
	b := text.Read([]byte("call(\n\n\n\n)\n"))
	if _, ok := ShowMatch(b, text.Pos{Line: 5, Col: 0}, pairs, 3, 5); ok {
		t.Error("hopped to a line above the top of the window")
	}
	if _, ok := ShowMatch(b, text.Pos{Line: 5, Col: 0}, pairs, 1, 5); !ok {
		t.Error("refused a hop to a line that is on screen")
	}
}

// Typing an opening bracket is not a showmatch: only a closer is.
func TestShowMatchIgnoresOpeners(t *testing.T) {
	b := text.Read([]byte("call(\n"))
	if _, ok := ShowMatch(b, text.Pos{Line: 1, Col: 4}, pairs, 1, 1); ok {
		t.Error("typing '(' produced a hop")
	}
}

// A multi-byte pair in 'matchpairs' is refused rather than half handled.
func TestMatchPairsRefusesMultiByte(t *testing.T) {
	b := text.Read([]byte("x\n"))
	if _, ok := FindMatch(b, text.Pos{Line: 1, Col: 0}, "«:»"); ok {
		t.Error("a multi-byte 'matchpairs' item was accepted")
	}
}
