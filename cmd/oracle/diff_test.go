package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestFirstDifference(t *testing.T) {
	for _, tc := range []struct {
		name      string
		a, b      string
		off, line int
	}{
		{"identical", "abc\ndef\n", "abc\ndef\n", 8, 3},
		{"first byte", "x", "y", 0, 1},
		{"third line", "a\nb\nc\n", "a\nb\nd\n", 4, 3},
		{"candidate is short", "a\nb\nc\n", "a\nb\n", 4, 3},
		{"candidate is long", "a\nb\n", "a\nb\nc\n", 4, 3},
		{"both empty", "", "", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			off, line := firstDifference([]byte(tc.a), []byte(tc.b))
			if off != tc.off || line != tc.line {
				t.Errorf("firstDifference = %d, line %d; want %d, line %d", off, line, tc.off, tc.line)
			}
		})
	}
}

// TestWindowIsThreeLinesWithControlBytesShown covers the two things the window
// is for: enough context to see what moved, and no raw tabs or newlines, which
// in a register dump are the whole story and are invisible printed as
// themselves.
func TestWindowIsThreeLinesWithControlBytesShown(t *testing.T) {
	b := []byte("one\ntwo\tthree\nfour\nfive\n")

	got := window(b, 2)
	if len(got) != 3 {
		t.Fatalf("%d lines, want 3:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[1], `two\tthree`) {
		t.Errorf("the tab is not escaped: %q", got[1])
	}
	if !strings.Contains(got[0], "1") || !strings.Contains(got[2], "3") {
		t.Errorf("lines are not numbered: %v", got)
	}

	// At the top of the file there is no line above, and asking for one must
	// not produce an empty row or a panic.
	if got := window(b, 1); len(got) != 2 {
		t.Errorf("window at line 1 has %d lines, want 2: %v", len(got), got)
	}
}

func TestCapAtFortyLines(t *testing.T) {
	var long []string
	for i := range 200 {
		long = append(long, fmt.Sprintf("line %d", i))
	}
	got := cap40(long)
	if len(got) != maxDiffLines {
		t.Fatalf("%d lines, want %d", len(got), maxDiffLines)
	}
	if !strings.Contains(got[len(got)-1], "more lines") {
		t.Errorf("the last line does not say what was cut: %q", got[len(got)-1])
	}

	short := []string{"a", "b"}
	if got := cap40(short); len(got) != 2 {
		t.Errorf("cap40 padded a short report to %d lines", len(got))
	}
}

// TestCompareOrdersBufferFirst pins the order a failing case reads in. The
// buffer explains the state and the state explains the messages, so a report
// that leads with the message line is a report you read backwards.
func TestCompareOrdersBufferFirst(t *testing.T) {
	ref := artifacts{buffer: []byte("a"), state: []byte("s"), messages: []byte("m")}
	cand := artifacts{buffer: []byte("b"), state: []byte("t"), messages: []byte("n")}

	got := compare(ref, cand)
	if len(got) != 3 {
		t.Fatalf("%d diffs, want 3", len(got))
	}
	for i, want := range []string{bufferName, stateName, messagesName} {
		if got[i].name != want {
			t.Errorf("diff %d is %s, want %s", i, got[i].name, want)
		}
	}

	if n := len(compare(ref, ref)); n != 0 {
		t.Errorf("%d diffs comparing a run against itself", n)
	}
}

// TestSeededRegister checks the table the harness ships with.
//
// The count is checked rather than the list, so that adding a row is a
// deliberate edit here as well as there, and so that the day a tenth arrives
// this fails: fewer than ten, or the register is doing work the code should be.
func TestSeededRegister(t *testing.T) {
	r := loadRegister()
	if len(r.all) < 4 || len(r.all) > 9 {
		t.Fatalf("%d entries, want between four and nine", len(r.all))
	}
	seen := map[string]bool{}
	for _, d := range r.all {
		if d.why == "" {
			t.Errorf("%s has no reason", d.id)
		}
		if d.what == "" {
			t.Errorf("%s does not say what the difference is", d.id)
		}
		if seen[d.id] {
			t.Errorf("%s is registered twice", d.id)
		}
		seen[d.id] = true
	}
}

// TestNormaliseElapsed pins the one comparison in this harness that is not
// byte for byte.
//
// The point of the table is the second half of it: everything that is not a
// count of seconds in front of "ago" has to come back untouched, because a
// normalisation that is one character too greedy is a difference the oracle
// stops being able to see, and it would stop seeing it silently.
func TestNormaliseElapsed(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"one second", "6 changes; before #1  1 second ago\n", "6 changes; before #1  N seconds ago\n"},
		{"zero seconds", "6 changes; before #1  0 seconds ago\n", "6 changes; before #1  N seconds ago\n"},
		{"many seconds", "1 change; after #3  17 seconds ago\n", "1 change; after #3  N seconds ago\n"},
		{"two on two lines", "1 change; before #1  1 second ago\n1 change; after #1  2 seconds ago\n",
			"1 change; before #1  N seconds ago\n1 change; after #1  N seconds ago\n"},

		{"no time at the root", "0 changes; before #0  \n", "0 changes; before #0  \n"},
		{"nothing to do", "E486: Pattern not found: zzz\n", "E486: Pattern not found: zzz\n"},
		{"a word ending in ago", "1 line less; before #1 chicago\n", "1 line less; before #1 chicago\n"},
		{"no count in front", "seconds ago\n", "seconds ago\n"},
		{"minutes are left alone", "1 change; before #1  2 minutes ago\n", "1 change; before #1  2 minutes ago\n"},
		{"a buffer line that says seconds", "he waited 3 seconds ago and left\n", "he waited N seconds ago and left\n"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(normaliseElapsed([]byte(tc.in))); got != tc.want {
				t.Errorf("normaliseElapsed(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormaliseElapsedDoesNotHideARealDifference: two messages that differ
// anywhere but in the count of seconds still differ afterwards.
func TestNormaliseElapsedDoesNotHideARealDifference(t *testing.T) {
	ref := []byte("6 changes; before #1  1 second ago\n")
	for _, cand := range []string{
		"6 changes; before #2  1 second ago\n", // a different sequence number
		"5 changes; before #1  1 second ago\n", // a different count of changes
		"6 changes; after #1  1 second ago\n",  // the other direction
		"6 changes; before #1  1 second ago",   // a missing newline
		"6 changes; before #1  \n",             // no elapsed clause at all
	} {
		a, b := normaliseElapsed(ref), normaliseElapsed([]byte(cand))
		if bytes.Equal(a, b) {
			t.Errorf("%q and %q normalise to the same thing", ref, cand)
		}
	}
}
