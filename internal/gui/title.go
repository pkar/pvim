package gui

import "strings"

// Title builds the window title, in vim's shape with pvim's name on the end.
//
// Measured rather than guessed, against /opt/homebrew/bin/vim 9.2, by running
// it under `script` with 'title' set and reading the OSC 2 sequences out of
// the transcript. All four shapes:
//
//	foo.txt (/tmp/titletest) - VIM a saved file
//	foo.txt + (/tmp/titletest) - VIM the same file, modified
//	[No Name] - VIM a buffer with no file
//	[No Name] + - VIM the same, modified
//
// So the marker is a bare "+" between the name and the directory, the
// directory is dropped along with its brackets when there is no file, and the
// separator is always " - ". The only difference here is the last word.
//
// vim also truncates the middle of a long path with "..." to fit 'titlelen'
// against the terminal width. That is a terminal's problem: a window title bar
// elides on its own and at its own width, and doing it twice reads as a bug.
//
// It lives in this package rather than in the editor because the title is a
// window's business and because a frontend with no window -- internal/tui,
// which writes the same string as an escape sequence -- has its own answer.
func Title(name, dir string, modified bool) string {
	if name == "" {
		name = "[No Name]"
	}
	parts := []string{name}
	if modified {
		parts = append(parts, "+")
	}
	if dir != "" {
		parts = append(parts, "("+dir+")")
	}
	return strings.Join(parts, " ") + " - pvim"
}
