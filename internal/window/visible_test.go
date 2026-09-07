package window

import (
	"strconv"
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// TestVisibleFeedsHML checks that what Visible reports is enough to put H, M
// and L where vim puts them.
//
// The three motions are implemented in internal/motion, which may not import
// this package: it is handed a window.Visible and answers from that alone. The
// rule below is a second copy of motion's, deliberately, because the thing
// under test is the Visible and not the rule. A window whose Bottom is one line
// out, or whose AtBottom flag is wrong on the last screenful, moves every one
// of these answers, and it is the sort of wrongness that reads as correct in
// the window package and shows up as a fuzz difference in the mode package a
// week later.
func TestVisibleFeedsHML(t *testing.T) {
	for _, tc := range readMeasured(t, "../../testdata/window/hml-vim9.2.0321.txt") {
		name := tc.keys + " h" + strconv.Itoa(tc.height) + " so" + strconv.Itoa(tc.so) +
			" from " + strconv.Itoa(tc.top) + "/" + strconv.Itoa(tc.cursor)
		t.Run(name, func(t *testing.T) {
			w := at(tc.height, tc.top, tc.cursor)
			v := w.Visible()

			count := 0
			keys := tc.keys
			for len(keys) > 0 && keys[0] >= '0' && keys[0] <= '9' {
				count = count*10 + int(keys[0]-'0')
				keys = keys[1:]
			}
			if count < 1 {
				count = 1
			}

			var lnum int
			switch keys {
			case "H":
				lnum = v.Top + count - 1
			case "L":
				lnum = v.Bottom - (count - 1)
				if count-1 >= v.Bottom {
					lnum = 1
				}
			case "M":
				lnum = v.Top + (v.Bottom-v.Top)/2
			default:
				t.Fatalf("unknown motion %q", keys)
			}

			height := v.Bottom - v.Top + 1
			above, below := tc.so, tc.so
			if v.AtTop {
				above = 0
				if maxOff := height / 2; below > maxOff {
					below = maxOff
				}
			}
			if v.AtBottom {
				below = 0
				if maxOff := (height - 1) / 2; above > maxOff {
					above = maxOff
				}
			}
			lnum = clamp(lnum, v.Top+above, v.Bottom-below)
			lnum = clamp(lnum, 1, w.Buf.LineCount())

			if lnum != tc.wantCur {
				t.Errorf("%s landed on line %d, vim says %d (testdata line %d)", tc.keys, lnum, tc.wantCur, tc.line)
			}
			if w.View.TopLine != tc.wantTop {
				t.Errorf("%s scrolled the window to %d, vim says %d: H, M and L never scroll", tc.keys, w.View.TopLine, tc.wantTop)
			}
		})
	}
}

// TestVisibleReportsTheEnds is the pair of flags on their own, because they are
// what suspends 'scrolloff' and they are two booleans a refactor can invert
// without any test noticing.
func TestVisibleReportsTheEnds(t *testing.T) {
	for _, tc := range []struct {
		name             string
		lineCount, top   int
		wantTop, wantBot bool
	}{
		{"a window at the top of a long file", 400, 1, true, false},
		{"a window in the middle", 400, 100, false, false},
		{"a window on the last screenful", 400, 378, false, true},
		{"a file shorter than the window", 4, 1, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New(1, lines(tc.lineCount), options.Defaults().GW)
			w.SetHeight(23)
			w.View.TopLine = tc.top
			v := w.Visible()
			if v.AtTop != tc.wantTop || v.AtBottom != tc.wantBot {
				t.Errorf("AtTop %v AtBottom %v, want %v and %v", v.AtTop, v.AtBottom, tc.wantTop, tc.wantBot)
			}
		})
	}
}
