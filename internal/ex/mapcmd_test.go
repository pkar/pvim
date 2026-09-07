package ex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The map family, and 'readonly' being a lock rather than a label.

// TestMapCommandsReachTheHook is the whole of what internal/ex does for a
// mapping: work out that the command IS one, resolve the name and hand the
// line over. Every pair below is the abbreviation on the left and what the
// frontend has to be handed on the right, with the name spelled out.
func TestMapCommandsReachTheHook(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"nn <silent> gh :nohl<CR>", "nnoremap <silent> gh :nohl<CR>"},
		{"nm zz x", "nmap zz x"},
		{"map q dd", "map q dd"},
		{"map! jk <Esc>", "map! jk <Esc>"},
		{"no { gT", "noremap { gT"},
		{"vn <leader>y \"+y", `vnoremap <leader>y "+y`},
		{"ino jj <Esc>", "inoremap jj <Esc>"},
		{"unm q", "unmap q"},
		{"nun zz", "nunmap zz"},
		{"mapc", "mapclear"},
		{"nmapc <buffer>", "nmapclear <buffer>"},
		{"om ir i(", "omap ir i("},
		{"cno <C-a> <Home>", "cnoremap <C-a> <Home>"},
		{"xn p pgvy", "xnoremap p pgvy"},
		{"lm a b", "lmap a b"},
	} {
		h := newHarness(t, tenLines()...)
		var got string
		h.ctx.Map = func(line string) error { got = line; return nil }
		if err := h.ctx.RunLine(tc.typed); err != nil {
			t.Errorf(":%s: %v", tc.typed, err)
			continue
		}
		if got != tc.want {
			t.Errorf(":%s handed over %q, want %q", tc.typed, got, tc.want)
		}
	}
}

// TestMapKeepsTheWholeLine is the bar rule. vim's map commands have no
// EX_TRLBAR, so the bar in a right-hand side is part of the mapping and not
// the start of a second command; splitting there mapped half of it and then
// tried to run ":echo hi" as its own line.
func TestMapKeepsTheWholeLine(t *testing.T) {
	h := newHarness(t, tenLines()...)
	var got []string
	h.ctx.Map = func(line string) error { got = append(got, line); return nil }
	if err := h.ctx.RunLine("nnoremap gh :nohl<CR>|echo hi"); err != nil {
		t.Fatalf("nnoremap: %v", err)
	}
	if len(got) != 1 || got[0] != "nnoremap gh :nohl<CR>|echo hi" {
		t.Errorf("the bar split the line: %q", got)
	}
}

// TestMapWithNoHookIsLoud is the other half of item 4's complaint. A map
// command used to be accepted and do nothing; with no frontend to hand it to
// it now says so, which is the same E319 every other unwritten command gives.
func TestMapWithNoHookIsLoud(t *testing.T) {
	h := newHarness(t, tenLines()...)
	err := h.err("nnoremap zz x")
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf(":nnoremap with no Map hook answered %v, want E319", err)
	}
	if err == nil || !contains(err.Error(), "nnoremap") {
		t.Errorf("the message does not name the command: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestReadOnlyRefusesTheWrite is item 3.
//
// Measured with ":set ro" on a three-line buffer: ":w" says
// "E45: 'readonly' option is set (add ! to override)" and writes nothing --
// not even the progress line -- while ":w!" and ":w somewhere-else" both go
// through. vim's check_readonly() protects the file this buffer came from and
// nothing else.
func TestReadOnlyRefusesTheWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		line    string
		wantErr bool
		// target is the file that must exist afterwards, relative to the
		// temporary directory, or empty when nothing should be written.
		target string
	}{
		{name: "plain", line: "w", wantErr: true},
		{name: "bang", line: "w!", target: "f.txt"},
		{name: "elsewhere", line: "w other.txt", target: "other.txt"},
		{name: "write and quit", line: "wq", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			h := newHarness(t, tenLines()...)
			h.ctx.Quit = func(bool, bool) error { return nil }
			buf := h.ctx.current()
			buf.Name = filepath.Join(dir, "f.txt")
			chdir(t, dir)

			h.run(t, "set readonly")
			err := h.err(tc.line)
			switch {
			case tc.wantErr && !errors.Is(err, ErrReadOnly):
				t.Fatalf(":%s answered %v, want E45", tc.line, err)
			case !tc.wantErr && err != nil:
				t.Fatalf(":%s: %v", tc.line, err)
			}
			if tc.target == "" {
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Errorf(":%s wrote %d files; E45 means it wrote none", tc.line, len(entries))
				}
				for _, m := range h.msgs {
					if contains(m, "written") {
						t.Errorf(":%s said %q; vim prints no progress line either", tc.line, m)
					}
				}
				return
			}
			if _, err := os.Stat(filepath.Join(dir, tc.target)); err != nil {
				t.Errorf(":%s did not write %s: %v", tc.line, tc.target, err)
			}
		})
	}
}

// TestReadOnlyBufferFlagAlsoRefuses is the other 'readonly': the per-buffer
// flag ":help" and the crash-recovery prompt's [O]pen Read-Only set, which
// is not the same field as the option and which nothing enforced.
func TestReadOnlyBufferFlagAlsoRefuses(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, tenLines()...)
	buf := h.ctx.current()
	buf.Name = filepath.Join(dir, "f.txt")
	buf.ReadOnly = true
	chdir(t, dir)

	if err := h.err("w"); !errors.Is(err, ErrReadOnly) {
		t.Errorf(":w over a read-only buffer answered %v, want E45", err)
	}
	if err := h.err("w!"); err != nil {
		t.Errorf(":w! over a read-only buffer: %v", err)
	}
}

// chdir moves into dir for the length of the test, which ":w" needs because
// the name it prints is shortened against the working directory.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
