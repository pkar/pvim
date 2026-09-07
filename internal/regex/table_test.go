package regex

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// tablePath is the oracle table, relative to this package.
const tablePath = "../../testdata/regex/vim92.tsv"

// tableOptions are the options the table was generated under. The generator
// runs :s/x/bc/ before the first case so that `~` has a value, and sets
// 'noignorecase' 'nosmartcase' 'magic' explicitly; the header says so.
var tableOptions = Options{LastSubstitute: "bc"}

// row is one line of the table.
type row struct {
	line    int
	pattern string
	input   string
	start   int
	match   string
	refused bool
}

// TestVim92Table runs every row of the oracle table.
//
// This is the test the whole package exists to pass. A row that fails is either
// a translation bug or vim changing under a brew upgrade, and the two are told
// apart by regenerating the table and reading the diff.
func TestVim92Table(t *testing.T) {
	rows := readTable(t)
	if len(rows) < 500 {
		t.Fatalf("the table has %d rows, which is too few to be the table", len(rows))
	}

	for _, r := range rows {
		name := fmt.Sprintf("line%d/%s", r.line, r.pattern)
		t.Run(name, func(t *testing.T) {
			if r.refused {
				checkRefusedRow(t, r)
				return
			}
			re, err := Compile(r.pattern, tableOptions)
			if err != nil {
				t.Fatalf("Compile(%q): %v", r.pattern, err)
			}
			loc := re.FindStringIndex(r.input)
			start, match := -1, ""
			if loc != nil {
				start, match = loc[0], r.input[loc[0]:loc[1]]
			}
			if start != r.start || match != r.match {
				t.Errorf("%q over %q\n got start %d match %q\nwant start %d match %q\nsource %s",
					r.pattern, r.input, start, match, r.start, r.match, re.Source())
			}
		})
	}
}

// checkRefusedRow asserts that a refused pattern comes back with the error type
// its atom is meant to produce, and that the refusal table in refuse_test.go
// knows about it. The second half is what stops a row being quietly downgraded
// to "some error came back".
func checkRefusedRow(t *testing.T, r row) {
	t.Helper()
	want, ok := refusalFor(r.pattern)
	if !ok {
		t.Fatalf("%q is tagged refused in the table and is not in the refusals list in refuse_test.go", r.pattern)
	}
	_, err := Compile(r.pattern, tableOptions)
	if err == nil {
		t.Fatalf("Compile(%q) succeeded; it must refuse with %T", r.pattern, want)
	}
	if fmt.Sprintf("%T", err) != fmt.Sprintf("%T", want) {
		t.Fatalf("Compile(%q) returned %T (%v); want %T", r.pattern, err, err, want)
	}
}

// readTable parses the table, skipping the comment header.
func readTable(t *testing.T) []row {
	t.Helper()
	f, err := os.Open(tablePath)
	if err != nil {
		t.Fatalf("the oracle table is missing: %v", err)
	}
	defer f.Close()

	var out []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			t.Fatalf("%s:%d: %d columns, want 5", tablePath, n, len(f))
		}
		start, err := strconv.Atoi(f[2])
		if err != nil {
			t.Fatalf("%s:%d: bad match-start %q", tablePath, n, f[2])
		}
		out = append(out, row{
			line:    n,
			pattern: unescapeTable(f[0]),
			input:   unescapeTable(f[1]),
			start:   start,
			match:   unescapeTable(f[3]),
			refused: f[4] == "refused",
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", tablePath, err)
	}
	return out
}

// unescapeTable reverses the escaping the generator applies to columns 1, 2 and
// 4. It is a copy of the generator's own unescapeTSV, and it is a copy on
// purpose: the generator lives under testdata, which the go tool will not let
// anything import, and a shared copy in the package would be code the editor
// ships to read a test fixture.
func unescapeTable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
