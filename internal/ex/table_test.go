package ex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMeasuredAbbreviations is the table's specification.
//
// Vim resolves an abbreviated command by taking the first entry in its table
// that the typed text is a prefix of, so the table's order is the whole rule
// and it is not derivable from anything. Every pair below was read out of vim
// 9.2 patches 1-321 with fullcommand(), through
// "vim --clean -i NONE --not-a-term -s". A command added to the table in the
// wrong place fails here rather than quietly stealing ":c" from ":change".
func TestMeasuredAbbreviations(t *testing.T) {
	for typed, want := range map[string]string{
		"e": "edit", "s": "substitute", "w": "write", "q": "quit",
		"b": "buffer", "m": "move", "co": "copy", "c": "change",
		"cl": "clist", "cn": "cnext", "cp": "cprevious", "cw": "cwindow",
		"n": "next", "d": "delete", "y": "yank", "p": "print",
		"r": "read", "f": "file", "h": "help", "g": "global",
		"v": "vglobal", "a": "append", "i": "insert", "o": "open",
		"j": "join", "l": "list", "x": "xit", "u": "undo",
		"sp": "split", "vs": "vsplit", "ta": "tag", "on": "only",
		"clo": "close", "bd": "bdelete", "ls": "ls", "se": "set",
		"let": "let", "ma": "mark", "no": "noremap", "au": "autocmd",
		"so": "source", "hi": "highlight", "norm": "normal",
		"wq": "wq", "qa": "qall", "ch": "change", "cc": "cc",
		"ene": "enew", "bn": "bnext", "bp": "bprevious", "bf": "bfirst",
		"bl": "blast", "ba": "ball", "cq": "cquit", "cd": "cd",
		"pw": "pwd", "le": "left", "ce": "center", "fo": "fold",
		"ju": "jumps", "nu": "number", "pr": "print", "ru": "runtime",
		"sor": "sort", "ve": "version", "wa": "wall", "xa": "xall",
		"ya": "yank", "tabe": "tabedit", "tabn": "tabnext",
		"tabc": "tabclose", "cf": "cfile", "cg": "cgetfile",
		"sil": "silent", "vert": "vertical", "bel": "belowright",
		"abo": "aboveleft", "top": "topleft", "bo": "botright",
		"tab": "tab", "noa": "noautocmd", "conf": "confirm",
		"bro": "browse", "en": "endif",
		// The map family, measured with fullcommand(). The two
		// that decide where the rows go are "vn", which is vnoremap and not
		// vnew, and "map", which has to lose "m" to move and "ma" to mark and
		// win everything else.
		"map": "map", "mapc": "mapclear", "nm": "nmap", "nn": "nnoremap",
		"nun": "nunmap", "nor": "noremap", "unm": "unmap", "im": "imap",
		"ino": "inoremap", "iun": "iunmap", "in": "insert", "vm": "vmap",
		"vn": "vnoremap", "vu": "vunmap", "xm": "xmap", "xn": "xnoremap",
		"xu": "xunmap", "om": "omap", "ono": "onoremap", "ou": "ounmap",
		"cm": "cmap", "cno": "cnoremap", "cu": "cunmap", "lm": "lmap",
		"ln": "lnoremap", "lu": "lunmap", "nmapc": "nmapclear",
		"imapc": "imapclear", "vmapc": "vmapclear", "xmapc": "xmapclear",
		"omapc": "omapclear", "cmapc": "cmapclear",
	} {
		got, ok := Lookup(typed)
		if !ok {
			t.Errorf("%q resolved to nothing; vim answers %q", typed, want)
			continue
		}
		if got.Name != want {
			t.Errorf("%q resolved to %q; vim answers %q", typed, got.Name, want)
		}
	}
}

// TestSubstituteSwallowsItsFlags covers the rule that is not prefix matching:
// ":sg", ":sr" and ":si" are all :substitute with the flags written against
// the name, which fullcommand() confirms.
func TestSubstituteSwallowsItsFlags(t *testing.T) {
	for _, typed := range []string{"sg", "sr", "si", "sgc"} {
		c, ok := Lookup(typed)
		if !ok || c.Name != "substitute" {
			t.Errorf("%q did not resolve to substitute", typed)
		}
	}
	// The guards. Every one of these is a real command that the flag rule
	// would otherwise swallow, and every one was checked with fullcommand().
	for typed, want := range map[string]string{
		"sp": "split", "se": "set", "so": "source", "sor": "sort",
		"sil": "silent", "sy": "syntax",
	} {
		if c, ok := Lookup(typed); !ok || c.Name != want {
			t.Errorf("%q did not resolve to %q", typed, want)
		}
	}

	// ":k" takes the next character as an argument, except when that
	// character is an 'e'.
	if c, ok := Lookup("ka"); !ok || c.Name != "k" {
		t.Error("ka did not resolve to :k; it sets mark a")
	}
	if c, ok := Lookup("kee"); !ok || c.Name != "keepmarks" {
		t.Error("kee did not resolve to keepmarks; vim guards :k with p[1] != 'e'")
	}
}

// TestUserCommandsAreNotBuiltins keeps ":JsonPretty" out of the built-in
// table. Vim's rule is the first letter: a user command starts with an
// uppercase one and a built-in never does.
func TestUserCommandsAreNotBuiltins(t *testing.T) {
	if _, ok := Lookup("JsonPretty"); ok {
		t.Error("JsonPretty resolved to a built-in; a name starting uppercase is always a user command")
	}
	if _, ok := Lookup("W"); ok {
		t.Error(`":W" resolved to a built-in; vim answers E492 for it, which is what catches a shifted colon`)
	}
}

// TestUnknownIsNotFound: the table answers no rather than guessing, and the
// caller turns that into E492 with the name in it.
func TestUnknownIsNotFound(t *testing.T) {
	for _, typed := range []string{"zzz", "notacommand", ""} {
		if _, ok := Lookup(typed); ok {
			t.Errorf("%q resolved to a command", typed)
		}
	}
}

// TestEveryCommandNameIsUnique catches the copy-and-paste that would otherwise
// make one entry permanently unreachable behind another.
func TestEveryCommandNameIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := range commands {
		if seen[commands[i].Name] {
			t.Errorf("%q is in the table twice", commands[i].Name)
		}
		seen[commands[i].Name] = true
	}
}

// TestEveryPrefixMatchesVim is the exhaustive version of the table above, and
// it is the one that would catch a command inserted in the wrong place.
//
// testdata/ex/fullcommand-vim9.2.0321.txt was generated by taking every prefix
// of every command name in this table, feeding each to fullcommand() in
// "vim --clean -i NONE --not-a-term -s", and writing the pairs out. All 714 of
// them agree with Lookup. A prefix whose answer is a command this editor
// does not implement is skipped, because pvim's table being smaller than vim's
// is the whole design and not a failure.
//
// Regenerated against the same vim, because the map family was
// added to the table and 141 prefixes of it had no row here to be checked
// against. Nothing that was already in the file moved: the diff is 141 added
// lines and no changed ones, which is the evidence that the new rows went in
// the right places rather than merely somewhere that passes.
//
// The patch level is in the file name for the same reason it is in
// testdata/regex/vim92.tsv: a brew upgrade that reorders vim's command table
// should regenerate this file in its own commit rather than quietly change
// what ":c" means.
func TestEveryPrefixMatchesVim(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ex", "fullcommand-vim9.2.0321.txt"))
	if err != nil {
		t.Fatalf("reading the vim dump: %v", err)
	}
	known := map[string]bool{}
	for _, n := range Names() {
		known[n] = true
	}
	var checked int
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		typed, want := parts[0], parts[1]
		if !known[want] {
			continue // a vim command pvim does not implement
		}
		checked++
		got, ok := Lookup(typed)
		switch {
		case !ok:
			t.Errorf("%q resolved to nothing; vim answers %q", typed, want)
		case got.Name != want:
			t.Errorf("%q resolved to %q; vim answers %q", typed, got.Name, want)
		}
	}
	if checked < 400 {
		t.Fatalf("only %d prefixes were checked; the dump is not being read", checked)
	}
}
