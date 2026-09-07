package textobj

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
)

// TestTypes: ip and ap are the only linewise text objects there are.
// Everything else is charwise, and an object that reports linewise by accident
// deletes whole lines for dib.
func TestTypes(t *testing.T) {
	for _, o := range All() {
		want := register.TypeChar
		if o.Key == 'p' {
			want = register.TypeLine
		}
		if o.Type != want {
			t.Errorf("%q (%s) is %v, want %v", string(rune(o.Key)), o.Name, o.Type, want)
		}
	}
}

// TestAliases: b and B are vim's spellings of ( and {, and the two halves of
// each bracket pair name the same object. All four have to be in the table or
// dib and di( disagree.
func TestAliases(t *testing.T) {
	for _, group := range [][]byte{
		{'(', ')', 'b'},
		{'{', '}', 'B'},
		{'[', ']'},
		{'<', '>'},
	} {
		var name string
		for i, c := range group {
			o, ok := ByKey(c)
			if !ok {
				t.Errorf("%q is not in the table", string(rune(c)))
				continue
			}
			if i == 0 {
				name = o.Name
			} else if o.Name != name {
				t.Errorf("%q is %q, want %q like the rest of its group", string(rune(c)), o.Name, name)
			}
		}
	}
}

// TestEveryObjectHasABody catches a table row added with no function behind it,
// which would be a nil call on that keystroke.
func TestEveryObjectHasABody(t *testing.T) {
	for _, o := range All() {
		if o.Find == nil {
			t.Errorf("%q has no Find", string(rune(o.Key)))
		}
	}
}

// TestCount1 is vim's default of one.
func TestCount1(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 1}, {-1, 1}, {4, 4}} {
		if got := (Request{Count: tc.in}).Count1(); got != tc.want {
			t.Errorf("Request{Count: %d}.Count1() = %d, want %d", tc.in, got, tc.want)
		}
	}
}
