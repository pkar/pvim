package vimrc

import (
	"reflect"
	"testing"
)

// TestLogicalLinesJoinContinuations is the vimrc's dict literal, which is one
// of the three lines as hard.
//
// The rule that makes it work is that vim throws away everything up to and
// including the backslash and puts nothing in its place, so the spaces in the
// joined line are the ones that were written after the backslash and no
// others.
func TestLogicalLinesJoinContinuations(t *testing.T) {
	src := []byte(`let g:ctrlp_custom_ignore = {
  \ 'dir':  '\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$',
  \ 'file': '\.so$\|\.dat$|\.DS_Store|\.pyc$'
  \ }
set ts=2
`)
	got := logicalLines(src, "vimrc")
	want := []logicalLine{
		{
			text: `let g:ctrlp_custom_ignore = { 'dir':  '\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$', 'file': '\.so$\|\.dat$|\.DS_Store|\.pyc$' }`,
			pos:  Pos{File: "vimrc", Line: 1},
		},
		{text: "set ts=2", pos: Pos{File: "vimrc", Line: 5}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("logicalLines:\n got %#v\nwant %#v", got, want)
	}
}

// TestLogicalLinesDropCommentsAndBlanks. A statement's position is the line it
// started on, which is what vim reports: sourcing a file whose line 2 is a
// stray continuation prints "line 1:" over the error. Measured.
func TestLogicalLinesDropCommentsAndBlanks(t *testing.T) {
	src := []byte("\" a comment\n\nset ts=2 \" trailing\n   \" indented comment\nset sw=4\n")
	got := logicalLines(src, "vimrc")
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %#v", len(got), got)
	}
	if got[0].pos.Line != 3 || got[1].pos.Line != 5 {
		t.Errorf("positions are %d and %d, want 3 and 5", got[0].pos.Line, got[1].pos.Line)
	}
}

func TestSplitBar(t *testing.T) {
	for _, tc := range []struct {
		in         string
		head, rest string
		found      bool
	}{
		{in: "set ts=2", head: "set ts=2"},
		{in: "so $MYVIMRC | if 1", head: "so $MYVIMRC ", rest: " if 1", found: true},
		// A bar inside a string is not a separator, which is what keeps a
		// log-file path in one piece.
		{in: `let x = "a|b"`, head: `let x = "a|b"`},
		{in: `let x = 'a|b'`, head: `let x = 'a|b'`},
		// An escaped bar is not a separator, which is how an option value
		// carries one.
		{in: `set ef=%f\|%l | set ts=2`, head: `set ef=%f\|%l `, rest: " set ts=2", found: true},
		// A doubled quote inside a single-quoted string does not end it.
		{in: `let x = 'it''s|fine'`, head: `let x = 'it''s|fine'`},
	} {
		head, rest, found := splitBar(tc.in)
		if head != tc.head || rest != tc.rest || found != tc.found {
			t.Errorf("splitBar(%q) = %q, %q, %v; want %q, %q, %v", tc.in, head, rest, found, tc.head, tc.rest, tc.found)
		}
	}
}

func TestStripComment(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// Line 76 of the vimrc, which is three options and a comment and not
		// four options.
		{`noerrorbells visualbell t_vb= " Disable ALL bells"`, "noerrorbells visualbell t_vb="},
		{"nocompatible \" cp: turns off strict vi compatibility", "nocompatible"},
		{`foo="bar"`, `foo="bar"`},
		{`ts=2`, "ts=2"},
		{`\" not a comment`, `\" not a comment`},
	} {
		if got := stripComment(tc.in); got != tc.want {
			t.Errorf("stripComment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCommandAbbreviations holds the command table to vim's own answers.
//
// Every row was measured against /opt/homebrew/bin/vim 9.2.0321
// with fullcommand(), one call per abbreviation, and not read out of map.txt.
// That is how ":sm" got caught: it is :smagic and not :smap, so :smap's
// shortest form is ":sma" and a table that let ":sm" through would have
// defined a select-mode mapping where the user asked for a substitute
// setting.
func TestCommandAbbreviations(t *testing.T) {
	for typed, want := range map[string]string{
		"map": "map", "nm": "nmap", "vm": "vmap", "xm": "xmap", "sma": "smap",
		"om": "omap", "im": "imap", "cm": "cmap", "no": "noremap",
		"nn": "nnoremap", "vn": "vnoremap", "xn": "xnoremap", "snor": "snoremap",
		"ono": "onoremap", "ino": "inoremap", "cno": "cnoremap",
		"unm": "unmap", "nun": "nunmap", "vu": "vunmap", "xu": "xunmap",
		"sunm": "sunmap", "ou": "ounmap", "iu": "iunmap", "cu": "cunmap",
		"mapc": "mapclear", "nmapc": "nmapclear",
		"se": "set", "setl": "setlocal", "setg": "setglobal",
		"let": "let", "unl": "unlet", "au": "autocmd", "aug": "augroup",
		"com": "command", "delc": "delcommand", "if": "if", "elsei": "elseif",
		"el": "else", "en": "endif", "end": "endif", "endf": "endfunction",
		"colo": "colorscheme", "hi": "highlight", "so": "source",
		"sy": "syntax", "filet": "filetype", "fini": "finish", "fu": "function",
		"cal": "call", "noh": "nohlsearch", "norm": "normal",
	} {
		spec, ok := lookupCmd(typed)
		if !ok {
			t.Errorf("lookupCmd(%q) found nothing; vim resolves it to :%s", typed, want)
			continue
		}
		if spec.name != want {
			t.Errorf("lookupCmd(%q) = :%s; vim says :%s", typed, spec.name, want)
		}
	}

	// ":sm" is :smagic, which this package does not have. Resolving it to
	// :smap would be worse than not resolving it at all.
	if spec, ok := lookupCmd("sm"); ok {
		t.Errorf("lookupCmd(\"sm\") = :%s; vim says :smagic and this table has no row for it", spec.name)
	}
}

// TestParseHeaderRespectsLiteralCommands is the second of the three hard
// lines. ":autocmd" swallows the rest of the line, bars and all, so the
// vimrc's BufWritePost line registers one autocommand whose command has three
// bars in it. Measured with :execute('autocmd BufWritePost').
func TestParseHeaderRespectsLiteralCommands(t *testing.T) {
	const line = `au BufWritePost .vimrc so $MYVIMRC | if has('gui_running') | so $MYGVIMRC | endif`
	h := parseHeader(line)
	if !h.ok || h.spec.name != "autocmd" {
		t.Fatalf("parseHeader did not find :autocmd: %+v", h)
	}
	if h.rest != "" {
		t.Errorf("the line was split at a bar; rest = %q", h.rest)
	}
	const wantArgs = `BufWritePost .vimrc so $MYVIMRC | if has('gui_running') | so $MYGVIMRC | endif`
	if h.args != wantArgs {
		t.Errorf("args = %q, want %q", h.args, wantArgs)
	}

	// A command that is not literal is cut at its first bar.
	h = parseHeader("set ts=2 | set sw=4")
	if h.args != "ts=2 " || h.rest != " set sw=4" {
		t.Errorf("set: args = %q, rest = %q; want %q and %q", h.args, h.rest, "ts=2 ", " set sw=4")
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pat, name string
		want      bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "main.gox", false},
		{"*", "~/x.md", true}, // vim's "*" crosses a separator
		{".vimrc", ".vimrc", true},
		{"*.vba.gz", "x.vba.gz", true},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"*.txt", "notes.txt", true},
		{"*.md", "notes.txt", false},
	} {
		if got := globMatch(tc.pat, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pat, tc.name, got, tc.want)
		}
	}
}
