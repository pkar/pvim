package undofile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func history() History {
	when := time.Date(2026, 9, 6, 5, 44, 4, 0, time.UTC)
	return History{
		Written:    when,
		Command:    []string{"w", "JsonPretty", "g/aaa/d"},
		Search:     []string{`\s\+$`, "func "},
		Expr:       []string{"&tabstop"},
		Pattern:    "func ",
		Substitute: "X",
		Marks: []Mark{
			{Name: 'A', Place: Place{File: "/tmp/a.go", Pos: Pos{Line: 12, Col: 3}}},
			{Name: '0', Place: Place{File: "/tmp/b.go", Pos: Pos{Line: 1}}},
		},
		Jumps: []Place{
			{File: "/tmp/a.go", Pos: Pos{Line: 40}},
			{File: "/tmp/b.go", Pos: Pos{Line: 7, Col: 2}},
		},
		Files: []FileMark{
			{Place: Place{File: "/tmp/a.go", Pos: Pos{Line: 12, Col: 3}}, When: when},
		},
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	want := history()
	got, err := DecodeHistory(EncodeHistory(want))
	if err != nil {
		t.Fatalf("DecodeHistory: %v", err)
	}
	if !got.Written.Equal(want.Written) {
		t.Errorf("written %v", got.Written)
	}
	got.Written, want.Written = time.Time{}, time.Time{}
	for i := range got.Files {
		if !got.Files[i].When.Equal(want.Files[i].When) {
			t.Errorf("file mark %d time %v", i, got.Files[i].When)
		}
		got.Files[i].When, want.Files[i].When = time.Time{}, time.Time{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("history came back as\n%+v\nwant\n%+v", got, want)
	}
}

func TestHistoryOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	if _, err := ReadHistory(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a first launch with no history file gave %v", err)
	}
	if err := WriteHistory(path, history()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Written.IsZero() {
		t.Error("WriteHistory did not stamp the clock")
	}
	if len(got.Command) != 3 || got.Pattern != "func " {
		t.Errorf("read back %+v", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600: this file holds every command anybody typed", st.Mode().Perm())
	}
}

// 'history' is 200 and the vimrc leaves it there. The oldest go, which is what
// makes the up arrow walk backwards through the recent ones.
func TestHistoryTrim(t *testing.T) {
	var h History
	for i := 0; i < 500; i++ {
		h.Command = append(h.Command, strconv.Itoa(i))
		h.Search = append(h.Search, strconv.Itoa(i))
		h.Jumps = append(h.Jumps, Place{File: "/tmp/f", Pos: Pos{Line: i}})
		h.Files = append(h.Files, FileMark{Place: Place{File: "/tmp/" + strconv.Itoa(i)}})
	}
	h.Trim(DefaultHistory)
	if len(h.Command) != DefaultHistory || h.Command[0] != "300" {
		t.Errorf("command history is %d entries starting at %q", len(h.Command), h.Command[0])
	}
	if len(h.Search) != DefaultHistory {
		t.Errorf("search history is %d entries", len(h.Search))
	}
	if len(h.Jumps) != maxJumps || h.Jumps[0].Pos.Line != 400 {
		t.Errorf("jumplist is %d entries starting at line %d", len(h.Jumps), h.Jumps[0].Pos.Line)
	}
	if len(h.Files) != DefaultFileMark {
		t.Errorf("file marks are %d entries", len(h.Files))
	}
	// A zero limit is the option unset, not a request to forget everything.
	h2 := history()
	h2.Trim(0)
	if len(h2.Command) != 3 {
		t.Errorf("Trim(0) dropped history down to %d entries", len(h2.Command))
	}
}

func TestHistoryRefusesRubbish(t *testing.T) {
	good := EncodeHistory(history())
	if _, err := DecodeHistory([]byte("# vim viminfo file")); !errors.Is(err, ErrMagic) {
		t.Errorf("a viminfo file gave %v, want ErrMagic", err)
	}
	bad := append([]byte(nil), good...)
	bad[len(bad)-2] ^= 0xff
	if _, err := DecodeHistory(bad); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a flipped byte gave %v, want ErrCorrupt", err)
	}
	for n := range good {
		if _, err := DecodeHistory(good[:n]); err == nil {
			t.Fatalf("a history file truncated to %d bytes was accepted", n)
		}
	}
}

// An empty history is a valid file and not a missing one: it is what a session
// that typed no commands writes, and reading it back has to give an empty
// history rather than an error the caller then treats as corruption.
func TestHistoryEmpty(t *testing.T) {
	got, err := DecodeHistory(EncodeHistory(History{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Command) != 0 || len(got.Marks) != 0 || len(got.Jumps) != 0 {
		t.Errorf("an empty history came back as %+v", got)
	}
}
