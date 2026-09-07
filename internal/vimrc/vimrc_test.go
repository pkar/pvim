package vimrc

import (
	"errors"
	"testing"
)

// TestTheVimrcsOwnVariables is the dividing rule as a test.
//
// The names are the ones ~/.vimrc actually sets. A "let"
// of something in ReadVars is applied; a "let" of anything else is logged once
// and dropped, which is what makes the eleven g:go_* lines and the four
// g:vim_ai_* lines inert without noise.
func TestTheVimrcsOwnVariables(t *testing.T) {
	// Read: the finder, the tree, the formatters and the gopls path.
	for _, name := range []string{
		"mapleader",
		"g:ctrlp_max_height", "g:ctrlp_match_window", "g:ctrlp_custom_ignore",
		"g:terraform_align", "g:terraform_fmt_on_save",
		"g:go_bin_path",
		// The vimrc writes these four with no scope prefix, which in a vimrc
		// is a global. A loader that only looked for "g:NERDTreeWinSize"
		// would drop all four and open the tree at the wrong width.
		"NERDTreeShowHidden", "NERDTreeIgnore", "NERDTreeWinSize",
	} {
		if !Read(name) {
			t.Errorf("%q is a variable pvim reads and Read says otherwise", name)
		}
	}

	// Ignored: a plugin's configuration for a plugin nobody is
	// reimplementing. Every one of these is a real line in the file.
	for _, name := range []string{
		"g:go_metalinter_enabled", "g:go_addtags_transform", "g:go_list_type",
		"g:vim_ai_token_file_path", "g:vim_ai_roles_config_file",
		"g:vim_ai_debug", "g:vim_ai_debug_log_file",
		"g:codeium_enabled", "g:chat_gpt_model",
	} {
		if Read(name) {
			t.Errorf("%q is a plugin's setting and pvim claims to read it", name)
		}
	}
}

// TestStatementsAreAClosedSet is the type switch's guarantee. Every statement
// type has to carry a Pos, because every error message needs a file and a
// line, and a type that forgot one would fail to compile here rather than
// print "E492" with nothing after it.
func TestStatementsAreAClosedSet(t *testing.T) {
	stmts := []Stmt{
		SetOption{Pos: Pos{File: "vimrc", Line: 1}},
		Let{Pos: Pos{File: "vimrc", Line: 2}},
		Map{Pos: Pos{File: "vimrc", Line: 3}},
		AutoCmd{Pos: Pos{File: "vimrc", Line: 4}},
		Command{Pos: Pos{File: "vimrc", Line: 5}},
		Highlight{Pos: Pos{File: "vimrc", Line: 6}},
		Colorscheme{Pos: Pos{File: "vimrc", Line: 7}},
		Source{Pos: Pos{File: "vimrc", Line: 8}},
		Ex{Pos: Pos{File: "vimrc", Line: 9}},
		Unknown{Pos: Pos{File: "vimrc", Line: 10}},
	}
	for i, s := range stmts {
		if got := s.Where(); got.Line != i+1 || got.File != "vimrc" {
			t.Errorf("statement %T lost its position: %+v", s, got)
		}
	}
}

// TestDiagErrorSaysTheStatementOnce.
//
// The message line is one row tall, on purpose, so what goes on it has to
// earn its bytes. Half of these E-codes name the offending statement inside
// vim's own wording -- E492 and E474 do -- and Diag.Error printed Text after
// that anyway, so the line read
//
//	vimrc, line 1: E492: Not an editor command: NERDTreeToggle: NERDTreeToggle
//
// where vim says "E492: Not an editor command: NERDTreeToggle". With
// 'cmdheight' at 1 the duplicate is what gets truncated off the end.
func TestDiagErrorSaysTheStatementOnce(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"NERDTreeToggle\n", "vimrc, line 1: E492: Not an editor command: NERDTreeToggle"},
		{"nmap\n", "vimrc, line 1: E474: listing mappings is not supported: nmap"},
	} {
		cfg := NewConfig()
		res := Run([]byte(tc.src), "vimrc", cfg, &DefaultEnv{Opts: cfg.Opts})
		if len(res.Errors) != 1 {
			t.Fatalf("%q: errors = %v, want one", tc.src, res.Log())
		}
		if got := res.Errors[0].Error(); got != tc.want {
			t.Errorf("%q:\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}

	// A diagnostic whose E-code does not name the statement still gets it.
	d := Diag{Pos: Pos{File: "vimrc", Line: 4}, Text: "set frobnicate", Err: errors.New("E518: Unknown option: frobnicate")}
	if want := "vimrc, line 4: E518: Unknown option: frobnicate: set frobnicate"; d.Error() != want {
		t.Errorf("got %q, want %q", d.Error(), want)
	}
}
