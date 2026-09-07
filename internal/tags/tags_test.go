package tags

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// write puts a tags file in a fresh directory and returns the directory.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tags"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestParseReadsTheThreeFields is the format itself, one row per shape a real
// ctags file holds.
func TestParseReadsTheThreeFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		line    string
		want    Tag
		refused bool
	}{
		{
			name: "pattern",
			line: "foo\tsrc/a.c\t/^int foo(void)$/",
			want: Tag{Name: "foo", File: "src/a.c", Address: "/^int foo(void)$/"},
		},
		{
			name: "line number",
			line: "foo\tsrc/a.c\t42",
			want: Tag{Name: "foo", File: "src/a.c", Address: "42"},
		},
		{
			name: "kind field",
			line: "foo\ta.c\t/^int foo/;\"\tf",
			want: Tag{Name: "foo", File: "a.c", Address: "/^int foo/", Kind: "f"},
		},
		{
			name: "named kind field",
			line: "foo\ta.c\t/^int foo/;\"\tkind:function",
			want: Tag{Name: "foo", File: "a.c", Address: "/^int foo/", Kind: "function"},
		},
		{
			name: "static tag",
			line: "foo\ta.c\t/^int foo/;\"\tf\tfile:",
			want: Tag{Name: "foo", File: "a.c", Address: "/^int foo/", Kind: "f", Static: true},
		},
		{
			// The ";\"" inside the search command is part of the pattern and
			// not the start of the fields, or an address cuts itself in half.
			name: "semicolon in the pattern",
			line: "foo\ta.c\t/^printf(\";\\\"\")/;\"\tf",
			want: Tag{Name: "foo", File: "a.c", Address: "/^printf(\";\\\"\")/", Kind: "f"},
		},
		{name: "no tabs", line: "foo", refused: true},
		{name: "two fields", line: "foo\ta.c", refused: true},
		{name: "empty address", line: "foo\ta.c\t", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parse(tc.line)
			if ok == tc.refused {
				t.Fatalf("parse(%q) ok=%v, want %v", tc.line, ok, !tc.refused)
			}
			if tc.refused {
				return
			}
			if got != tc.want {
				t.Errorf("parse(%q) =\n %+v\nwant\n %+v", tc.line, got, tc.want)
			}
		})
	}
}

// TestPatternUndoesTheEscaping: ctags escapes the delimiter, and the "^" and
// "$" stay in the pattern because they are what pins the match to the line the
// tag was written from.
func TestPatternUndoesTheEscaping(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{"/^int foo$/", "^int foo$"},
		{"?^int foo$?", "^int foo$"},
		{`/a\/b/`, "a/b"},
		{"/no closing delimiter", "no closing delimiter"},
	} {
		got, ok := Tag{Address: tc.addr}.Pattern()
		if !ok || got != tc.want {
			t.Errorf("Pattern(%q) = %q, %v; want %q, true", tc.addr, got, ok, tc.want)
		}
	}
	if _, ok := (Tag{Address: "42"}).Pattern(); ok {
		t.Error("a line number read as a pattern")
	}
	if n, ok := (Tag{Address: "42"}).LineNumber(); !ok || n != 42 {
		t.Errorf("LineNumber(42) = %d, %v", n, ok)
	}
}

// TestFindOrdersByPriority is vim's bucket order: a tag in the file the cursor
// is in comes first, and a static tag in that file comes before an ordinary
// one.
//
// It is the order the "pri" column of :tselect prints, and getting it wrong
// means CTRL-] on a name defined in two files jumps to the wrong one.
func TestFindOrdersByPriority(t *testing.T) {
	dir := write(t, "foo\tother.c\t/^a/\n"+
		"foo\there.c\t/^b/\n"+
		"foo\there.c\t/^c/;\"\tf\tfile:\n"+
		"foo\tother.c\t/^d/;\"\tf\tfile:\n")

	got, err := Find("foo", Options{Files: []string{"tags"}, Dir: dir, CurFile: filepath.Join(dir, "here.c")})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		file string
		pri  string
	}{
		{"here.c", "FSC"},
		{"here.c", "F C"},
		{"other.c", "F  "},
		{"other.c", "FS "},
	}
	if len(got) != len(want) {
		t.Fatalf("%d matches, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].File != w.file || got[i].PriName() != w.pri {
			t.Errorf("match %d is %s %q, want %s %q", i+1, got[i].File, got[i].PriName(), w.file, w.pri)
		}
	}
}

// TestFindIgnoresCaseOnlyWhenAsked, and puts those matches last, which is
// vim's MT_IC_OFF.
func TestFindIgnoresCaseOnlyWhenAsked(t *testing.T) {
	dir := write(t, "Foo\ta.c\t/^a/\nfoo\ta.c\t/^b/\n")

	got, err := Find("foo", Options{Files: []string{"tags"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Address != "/^b/" {
		t.Fatalf("case-sensitive Find gave %d matches (%+v), want the lowercase one", len(got), got)
	}

	got, err = Find("foo", Options{Files: []string{"tags"}, Dir: dir, IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Address != "/^b/" {
		t.Fatalf("with IgnoreCase: %+v; want the exact match first and both present", got)
	}
}

// TestFindReportsNoTagsFile is E433, which is a different answer from "the
// name is not in it": vim prints both, in that order, and a caller that only
// had one of them would be guessing.
func TestFindReportsNoTagsFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Find("foo", Options{Files: []string{"tags"}, Dir: dir})
	if !errors.Is(err, ErrNoTagsFile) {
		t.Fatalf("Find with no tags file: %v, want ErrNoTagsFile", err)
	}
	if got := ErrNoTagsFile.Error(); got != "E433: No tags file" {
		t.Errorf("the message is %q", got)
	}

	dir = write(t, "bar\ta.c\t/^a/\n")
	got, err := Find("foo", Options{Files: []string{"tags"}, Dir: dir})
	if err != nil || len(got) != 0 {
		t.Fatalf("a tags file without the name: %v, %+v; want no error and no matches", err, got)
	}
	if got := NotFound("foo"); got != "E426: Tag not found: foo" {
		t.Errorf("NotFound is %q", got)
	}
}

// TestFindSkipsTheHeaderAndCountsAFileOnce.
//
// The default 'tags' names "./tags" and "tags", which are one file whenever
// the buffer is in the working directory; vim offers each match once and a
// count that walked the same file twice would jump to the same place for two
// different numbers.
func TestFindSkipsTheHeaderAndCountsAFileOnce(t *testing.T) {
	dir := write(t, "!_TAG_FILE_SORTED\t1\t//\nfoo\ta.c\t/^a/\n")

	got, err := Find("foo", Options{Files: SplitOption(DefaultOption), Dir: dir, CurDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d matches over the default 'tags', want 1: %+v", len(got), got)
	}
	if got[0].Name != "foo" {
		t.Errorf("the header line was read as a tag: %+v", got[0])
	}
}

// TestFindResolvesTheFileAgainstTheTagsFile: the "file" column is relative to
// the tags file and not to the working directory, which is what makes a tags
// file in a subdirectory usable at all.
func TestFindResolvesTheFileAgainstTheTagsFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "tags"), []byte("foo\ta.c\t/^a/\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Find("foo", Options{Files: []string{"sub/tags"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != filepath.Join(sub, "a.c") {
		t.Fatalf("Path is %q, want %q", got[0].Path, filepath.Join(sub, "a.c"))
	}
	if got[0].File != "a.c" {
		t.Errorf("File is %q; E429 quotes the name as the tags file spells it", got[0].File)
	}
}

// TestSplitOptionReadsTheCommas, with the backslash that protects one.
func TestSplitOptionReadsTheCommas(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", []string{"./tags", "tags"}},
		{"./tags,tags", []string{"./tags", "tags"}},
		{"tags,/usr/tags", []string{"tags", "/usr/tags"}},
		{`odd\,name,tags`, []string{"odd,name", "tags"}},
	} {
		got := SplitOption(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("SplitOption(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("SplitOption(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// TestIdentIsTheKeywordUnderOrAfterTheCursor, which is vim's
// find_ident_under_cursor with FIND_IDENT and no second pass over non-blanks:
// a line of punctuation is E349 and not a tag search for the punctuation.
func TestIdentIsTheKeywordUnderOrAfterTheCursor(t *testing.T) {
	for _, tc := range []struct {
		line string
		col  int
		want string
		at   int
		ok   bool
	}{
		{line: "call foo here", col: 5, want: "foo", at: 5, ok: true},
		{line: "call foo here", col: 7, want: "foo", at: 5, ok: true},
		{line: "call foo here", col: 4, want: "foo", at: 5, ok: true},
		{line: "call foo here", col: 0, want: "call", at: 0, ok: true},
		{line: "  ***  ", col: 0, ok: false},
		{line: "", col: 0, ok: false},
		{line: "one two", col: 99, ok: false},
		{line: "a_b.c", col: 0, want: "a_b", at: 0, ok: true},
	} {
		got, at, ok := Ident([]byte(tc.line), tc.col, "")
		if ok != tc.ok || got != tc.want || (ok && at != tc.at) {
			t.Errorf("Ident(%q, %d) = %q, %d, %v; want %q, %d, %v", tc.line, tc.col, got, at, ok, tc.want, tc.at, tc.ok)
		}
	}
}

// TestIdentHonoursIsKeyword, because the option decides what a tag name is:
// with "-" in 'iskeyword' a lisp identifier is one word.
func TestIdentHonoursIsKeyword(t *testing.T) {
	const line = "call foo-bar here"
	if got, _, _ := Ident([]byte(line), 5, ""); got != "foo" {
		t.Errorf("with the default 'iskeyword' the word is %q, want foo", got)
	}
	if got, _, _ := Ident([]byte(line), 5, "@,48-57,_,192-255,-"); got != "foo-bar" {
		t.Errorf("with - in 'iskeyword' the word is %q, want foo-bar", got)
	}
}
