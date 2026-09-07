package vimrc

import (
	"strings"
	"testing"
)

// TestMissingEndifIsReportedPastTheLastLine.
//
// Vim blames a missing :endif on the line after the last one in the file, not
// on the :if that was left open and not on nothing at all. Measured
// with /opt/homebrew/bin/vim 9.2.0321: a two-line file of "if 1"
// and "set tw=3" prints "line 3:" over "E171: Missing :endif", written
// with and without a trailing newline, and a five-line file prints
// "line 6:".
//
// Line 0 was what this reported before, which is the one number a reader
// cannot act on: it points at no line of any file.
func TestMissingEndifIsReportedPastTheLastLine(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want int
	}{
		{"if 1\nset tw=3\n", 3},
		{"if 1\nset tw=3", 3},
		{"set ts=4\nif 1\nset tw=3\nset sw=2\nset et\n", 6},
	} {
		cfg := NewConfig()
		res := Run([]byte(tc.src), "vimrc", cfg, &DefaultEnv{Opts: cfg.Opts})
		if len(res.Errors) != 1 {
			t.Fatalf("%q: errors = %v, want one E171", tc.src, res.Log())
		}
		d := res.Errors[0]
		if d.Line != tc.want {
			t.Errorf("%q: E171 is on line %d, want %d, which is what vim says", tc.src, d.Line, tc.want)
		}
	}
}

// TestFinishInsideAnIfIsSilent. A file that stops at a ":finish" has not left
// a block open, it has chosen to stop, and vim says nothing about the :if
// above it. Measured on a file of "if 1" and "finish": no message
// at all.
func TestFinishInsideAnIfIsSilent(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("if 1\nfinish\n"), "vimrc", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if !res.Finished {
		t.Error("Finished is false after a :finish")
	}
}

// TestAFileThatEndsInsideAFunctionSaysSo.
//
// A ":function" block swallows every line after it, so a file that never
// closes one loses everything below it. Being silent about that is the third
// of the three failure modes this package's doc comment names, and it was
// what happened: Result.Errors came back empty.
//
// Vim says "E126: Missing :endfunction" and blames the ":function" line
// itself, not the end of the file. Measured on a two-line file of
// "function! Foo()" and " set tw=3": "line 1:".
func TestAFileThatEndsInsideAFunctionSaysSo(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("function! Foo()\n  set tw=3\n"), "vimrc", cfg, &DefaultEnv{Opts: cfg.Opts})

	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one E126", res.Log())
	}
	d := res.Errors[0]
	if d.Line != 1 {
		t.Errorf("E126 is on line %d, want 1, which is the :function line vim blames", d.Line)
	}
	if !strings.Contains(d.Error(), "E126: Missing :endfunction") {
		t.Errorf("message is %q, want an E126", d.Error())
	}
}

// TestBothOpenBlocksAreReportedInVimsOrder. Measured on "if 1",
// "function! Foo()", " set tw=3": vim prints E126 against line 2 and then
// E171. Only the order and the codes are asserted, because vim's line number
// for that E171 is 5 on a three-line file: its function reader ran off the
// end and carried the sourcing counter with it. pvim says 4, which is one
// past the last line, and reproducing the 5 would mean reproducing an
// artefact of how vim reads a function body.
func TestBothOpenBlocksAreReportedInVimsOrder(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("if 1\nfunction! Foo()\n  set tw=3\n"), "vimrc", cfg, &DefaultEnv{Opts: cfg.Opts})

	if len(res.Errors) != 2 {
		t.Fatalf("errors = %v, want an E126 and an E171", res.Log())
	}
	if got := res.Errors[0].Error(); !strings.Contains(got, "E126") {
		t.Errorf("first error is %q, want the E126 vim prints first", got)
	}
	if got := res.Errors[1].Error(); !strings.Contains(got, "E171") {
		t.Errorf("second error is %q, want the E171", got)
	}
	if res.Errors[0].Line != 2 {
		t.Errorf("E126 is on line %d, want 2", res.Errors[0].Line)
	}
}

// TestLineDoesNotStayInsideAFunction.
//
// Loader.Line is the entry point an autocommand's command goes through, and
// it reset three of the four pieces of per-file state. A ":function" left the
// fourth set, and a loader in that state swallowed every command it was asked
// to run afterwards without a word: the second Line below set nothing and
// reported nothing.
func TestLineDoesNotStayInsideAFunction(t *testing.T) {
	cfg := NewConfig()
	ld := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts})

	first := ld.Line("function! Foo()", Pos{File: "au", Line: 1})
	if len(first.Errors) != 1 || !strings.Contains(first.Errors[0].Error(), "E126") {
		t.Errorf("a :function with no :endfunction reported %v, want an E126", first.Log())
	}

	res := ld.Line("set sw=9", Pos{File: "au", Line: 2})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	v, err := cfg.Opts.Get("shiftwidth")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "=9" {
		t.Errorf("&shiftwidth = %q, want =9: the loader was still swallowing a function body", v.String())
	}
}
