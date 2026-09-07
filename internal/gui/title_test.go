package gui

import "testing"

// TestTitle is the four shapes measured out of vim 9.2, with the
// last word changed. The measurement is in Title's doc comment; this is it as
// an exit code.
func TestTitle(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dir      string
		modified bool
		want     string
	}{
		{"foo.txt", "/tmp/titletest", false, "foo.txt (/tmp/titletest) - pvim"},
		{"foo.txt", "/tmp/titletest", true, "foo.txt + (/tmp/titletest) - pvim"},
		{"", "", false, "[No Name] - pvim"},
		{"", "", true, "[No Name] + - pvim"},
	} {
		if got := Title(tc.name, tc.dir, tc.modified); got != tc.want {
			t.Errorf("Title(%q, %q, %v) = %q, want %q", tc.name, tc.dir, tc.modified, got, tc.want)
		}
	}
}
