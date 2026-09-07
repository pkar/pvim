package syntax

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSideBySide prints vim's answer and pvim's for one file, a line at a
// time, which is the only readable way to look at a disagreement. It is a tool
// and not a gate: it asserts nothing and does nothing unless it is asked.
//
//	SYNTAX_FILE=sample.md.txt SYNTAX_FT=markdown go test -run TestSideBySide -v ./internal/syntax/
func TestSideBySide(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to vim")
	}
	file := os.Getenv("SYNTAX_FILE")
	ft := os.Getenv("SYNTAX_FT")
	if file == "" {
		t.Skip("set SYNTAX_FILE and SYNTAX_FT")
	}
	body, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	dir := t.TempDir()
	path := filepath.Join(dir, strings.TrimSuffix(file, ".txt"))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	want := askVim(t, path)
	got := ours(t, ft, src)
	for i, line := range src {
		bad := false
		for c := 1; c <= len(line); c++ {
			if want[[2]int{i + 1, c}] != got[[2]int{i + 1, c}] {
				bad = true
			}
		}
		if !bad {
			continue
		}
		t.Logf("line %d: %q", i+1, line)
		for c := 1; c <= len(line); c++ {
			w, g := want[[2]int{i + 1, c}], got[[2]int{i + 1, c}]
			mark := " "
			if w != g {
				mark = "!"
			}
			t.Logf("  %s %3d %q vim=%-24s pvim=%s", mark, c, line[c-1:c], w, g)
		}
	}
}
