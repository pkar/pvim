package filetype

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vimCase is one file and the answer vim gives for it.
type vimCase struct {
	// name is the file's name, relative to the directory the case is written
	// into. A name with a "/" in it puts the file in a subdirectory, which is
	// how the .git/config case exercises the whole-path half of the pattern
	// rule.
	name string
	// body is the file's contents. Most cases do not care and say "x\n".
	body string
	// ft is what /opt/homebrew/bin/vim 9.2.0321 answers for &filetype.
	ft string
}

// vimAnswers is the gate.
//
// Every row was measured by writing the file and asking
//
//	vim --clean -i NONE --not-a-term -es -c 'echo &filetype' -c 'qa!' FILE
//
// and TestVimStillSaysSo below runs exactly that again, so the table is a
// recording and not a claim. --clean is the right vim to grade against for the
// same reason cmd/oracle uses it: it sources defaults.vim, which contains
// "filetype plugin indent on", so detection is on in the reference the way it
// is on in the editor.
//
// The rows are the types the vimrc names, the list the brief adds, and the
// shapes each detection route has to get right: a name, a pattern, an
// extension, a shebang, a first line, and the two content-dependent
// extensions.
var vimAnswers = []vimCase{
	// The extension table, dist#ft#ft_from_ext.
	{name: "a.go", body: "x\n", ft: "go"},
	{name: "a.js", body: "x\n", ft: "javascript"},
	{name: "a.json", body: "x\n", ft: "json"},
	{name: "a.yaml", body: "x\n", ft: "yaml"},
	{name: "a.yml", body: "x\n", ft: "yaml"},
	{name: "a.hcl", body: "x\n", ft: "hcl"},
	{name: "a.css", body: "x\n", ft: "css"},
	{name: "a.java", body: "x\n", ft: "java"},
	{name: "a.rb", body: "x\n", ft: "ruby"},
	{name: "a.rs", body: "x\n", ft: "rust"},
	{name: "a.tmpl", body: "x\n", ft: "template"},
	{name: "a.ini", body: "x\n", ft: "dosini"},
	{name: "a.tsv", body: "x\n", ft: "tsv"},
	{name: "a.csv", body: "x\n", ft: "csv"},
	{name: "a.kt", body: "x\n", ft: "kotlin"},
	{name: "a.proto", body: "x\n", ft: "proto"},
	{name: "a.swift", body: "x\n", ft: "swift"},
	{name: "a.tsx", body: "x\n", ft: "typescriptreact"},
	{name: "a.diff", body: "x\n", ft: "diff"},
	{name: "a.patch", body: "x\n", ft: "diff"},
	{name: "a.conf", body: "x\n", ft: "conf"},
	{name: "a.cfg", body: "x\n", ft: "cfg"},
	{name: "a.jsonc", body: "x\n", ft: "jsonc"},

	// The explicit patterns, filetype.vim.
	{name: "a.py", body: "x\n", ft: "python"},
	{name: "a.md", body: "x\n", ft: "markdown"},
	{name: "a.markdown", body: "x\n", ft: "markdown"},
	{name: "a.sh", body: "x\n", ft: "sh"},
	{name: "a.bash", body: "x\n", ft: "sh"},
	{name: "a.zsh", body: "x\n", ft: "zsh"},
	{name: "a.html", body: "x\n", ft: "html"},
	{name: "a.htm", body: "x\n", ft: "html"},
	{name: "a.c", body: "x\n", ft: "c"},
	{name: "a.h", body: "x\n", ft: "c"},
	{name: "a.cc", body: "x\n", ft: "cpp"},
	{name: "a.cpp", body: "x\n", ft: "cpp"},
	{name: "a.mk", body: "x\n", ft: "make"},
	{name: "a.mak", body: "x\n", ft: "make"},
	{name: "a.bzl", body: "x\n", ft: "bzl"},
	{name: "a.bazel", body: "x\n", ft: "bzl"},
	{name: "a.ps1", body: "x\n", ft: "ps1"},
	{name: "a.lua", body: "x\n", ft: "lua"},
	{name: "a.pl", body: "x\n", ft: "perl"},
	{name: "a.php", body: "x\n", ft: "php"},
	{name: "a.sql", body: "x\n", ft: "sql"},
	{name: "a.toml", body: "x\n", ft: "toml"},
	{name: "a.vim", body: "x\n", ft: "vim"},
	{name: "a.gradle", body: "x\n", ft: "groovy"},
	{name: "a.text", body: "x\n", ft: "text"},
	{name: "a.txt", body: "x\n", ft: "text"},
	{name: "Makefile", body: "x\n", ft: "make"},
	{name: "makefile", body: "x\n", ft: "make"},
	{name: "GNUmakefile", body: "x\n", ft: "make"},
	{name: "Dockerfile", body: "x\n", ft: "dockerfile"},
	{name: "Jenkinsfile", body: "x\n", ft: "groovy"},
	{name: "BUILD", body: "x\n", ft: "bzl"},
	{name: "WORKSPACE", body: "x\n", ft: "bzl"},
	{name: "SConstruct", body: "x\n", ft: "python"},
	{name: "README", body: "x\n", ft: "text"},
	{name: "LICENSE", body: "x\n", ft: "text"},
	{name: "COPYING", body: "x\n", ft: "text"},
	{name: "AUTHORS", body: "x\n", ft: "text"},
	{name: ".bashrc", body: "x\n", ft: "sh"},
	{name: ".bash_profile", body: "x\n", ft: "sh"},
	{name: ".profile", body: "x\n", ft: "sh"},
	{name: ".gitignore", body: "x\n", ft: "gitignore"},
	{name: ".gitconfig", body: "x\n", ft: "gitconfig"},
	{name: ".gitattributes", body: "x\n", ft: "gitattributes"},
	{name: "git-rebase-todo", body: "x\n", ft: "gitrebase"},
	// A "/" in the pattern means the whole path is matched, which is the
	// half of the rule a tail-only matcher gets wrong in silence.
	{name: ".git/config", body: "x\n", ft: "gitconfig"},

	// The name table, dist#ft#ft_from_name.
	{name: "COMMIT_EDITMSG", body: "x\n", ft: "gitcommit"},
	{name: "MERGE_MSG", body: "x\n", ft: "gitcommit"},
	{name: "Gemfile", body: "x\n", ft: "ruby"},
	{name: "Vagrantfile", body: "x\n", ft: "ruby"},
	{name: "Brewfile", body: "x\n", ft: "ruby"},
	{name: ".editorconfig", body: "x\n", ft: "editorconfig"},
	// Neither of these is in ft_from_name: ".zshrc" is filetype.vim line 1057
	// and ".vimrc" is the "*vimrc*" s:StarSetf() at line 1578, which runs
	// after the content sniffing. They are here because they are the two
	// dotfiles on this machine that get opened most.
	{name: ".zshrc", body: "x\n", ft: "zsh"},
	{name: ".vimrc", body: "x\n", ft: "vim"},

	// The two extensions whose answer depends on the contents.
	{name: "real.tf", body: "resource \"a\" \"b\" {}\n", ft: "terraform"},
	{name: "mud.tf", body: ";comment\n/x\n", ft: "tf"},
	{name: "empty.tf", body: "", ft: "tf"},
	{name: "qt.ts", body: "<?xml version=\"1.0\"?>\n", ft: "xml"},
	{name: "a.ts", body: "x\n", ft: "typescript"},
	{name: "xh.html", body: "<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Strict//EN\">\n", ft: "xhtml"},
	// A *.sh whose shebang names another shell is that other shell.
	{name: "z.sh", body: "#!/bin/zsh\necho\n", ft: "zsh"},
	{name: "c.sh", body: "#!/bin/csh\n", ft: "csh"},

	// Shebangs on files with no extension and no known name, which is the
	// only route left: scripts.vim.
	{name: "sb_bash", body: "#!/bin/bash\n", ft: "sh"},
	{name: "sb_zsh", body: "#!/bin/zsh\n", ft: "zsh"},
	{name: "sb_csh", body: "#!/bin/csh\n", ft: "csh"},
	{name: "sb_tcsh", body: "#!/bin/tcsh\n", ft: "tcsh"},
	{name: "sb_perl", body: "#!/usr/bin/perl\n", ft: "perl"},
	{name: "sb_ruby", body: "#!/usr/bin/ruby\n", ft: "ruby"},
	{name: "sb_env_py", body: "#!/usr/bin/env python3\n", ft: "python"},
	{name: "sb_envS", body: "#!/usr/bin/env -S python3 -u\n", ft: "python"},
	{name: "sb_lua", body: "#!/usr/bin/env lua\n", ft: "lua"},
	{name: "sb_node", body: "#!/usr/bin/env node\n", ft: "javascript"},
	{name: "sb_make", body: "#!/usr/bin/gnumake -f\n", ft: "make"},

	// The first-line checks in scripts.vim that do not start with "#!".
	{name: "x_colon", body: ":\n", ft: "sh"},
	{name: "x_compdef", body: "#compdef foo\n", ft: "zsh"},
	{name: "x_xmldecl", body: "<?xml version=\"1.0\"?>\n", ft: "xml"},
	{name: "x_doctype", body: "<!DOCTYPE html>\n<html>\n", ft: "html"},
	{name: "x_vimhdr", body: "\" vim\n", ft: "vim"},

	// The rows a sweep of 1,113 real files on this machine added, each one a
	// difference that sweep found. The .h cluster was 45 of them.
	{name: "cpp.h", body: "namespace v8 {\n", ft: "cpp"},
	{name: "objc.h", body: "@interface Foo\n", ft: "objcpp"},
	{name: "plain.h", body: "int x;\n", ft: "c"},
	{name: "go.mod", body: "module github.com/x\n", ft: "gomod"},
	{name: "go.sum", body: "x\n", ft: "gosum"},
	{name: "a.mod", body: "x\n", ft: "modsim3"},
	{name: ".git/HEAD", body: "ref: refs/heads/master\n", ft: "git"},
	{name: ".zprofile", body: "x\n", ft: "zsh"},
	{name: ".dockerignore", body: "x\n", ft: "gitignore"},
	{name: ".env", body: "x\n", ft: "env"},
	{name: "prod.env", body: "x\n", ft: "env"},
	{name: "a.envrc", body: "x\n", ft: "sh"},
	{name: "f.hw", body: "x\n", ft: "virata"},
	{name: "gse.patch", body: "From abcdef0123456789abcdef0123456789abcdef01 Mon Sep 17 00:00:00 2001\n", ft: "gitsendemail"},
	// ".bashrc*" is one of the s:StarSetf() rules, which run after the
	// content sniffing and not with the rest of the bash names.
	{name: ".bashrc.bak", body: "x\n", ft: "sh"},
	// The generic conf guess, filetype.vim's last autocommand: a file nothing
	// recognises whose first five lines have a "#" in column one.
	{name: "mystery", body: "#comment\n", ft: "conf"},
}

// TestDetectMatchesVim is the gate itself: Detect over every recorded case.
//
// It shells out to nothing, so it runs under -short and in check-fast, and a
// failure names the file, what vim said and what this package said.
func TestDetectMatchesVim(t *testing.T) {
	for _, c := range vimAnswers {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(c.name, []byte(c.body))
			if got != c.ft {
				t.Errorf("Detect(%q) = %q; vim says %q", c.name, got, c.ft)
			}
		})
	}
}

// TestVimStillSaysSo re-measures the table against the installed vim.
//
// Without it vimAnswers is a hundred numbers somebody typed, and the day a
// brew upgrade changes one of them the gate above goes on passing against the
// old answer. Skipped under -short with the rest of the suite that shells out.
func TestVimStillSaysSo(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to vim")
	}
	const bin = "/opt/homebrew/bin/vim"
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("no vim at %s: %v", bin, err)
	}

	dir := t.TempDir()
	for _, c := range vimAnswers {
		path := filepath.Join(dir, c.name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("making the directory for %s: %v", c.name, err)
		}
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", c.name, err)
		}
	}

	for _, c := range vimAnswers {
		t.Run(c.name, func(t *testing.T) {
			got := askVim(t, bin, dir, c.name)
			if got != c.ft {
				t.Errorf("vim says &filetype is %q for %s; the table says %q", got, c.name, c.ft)
			}
		})
	}
}

// askVim runs one file through the installed vim and returns its &filetype.
//
// -es is ex mode with no terminal, --not-a-term stops the "output is not to a
// terminal" wait, and -i NONE keeps it away from a viminfo file. The redir
// pair is how a headless vim is made to print anything at all.
func askVim(t *testing.T, bin, dir, name string) string {
	t.Helper()
	cmd := exec.Command(bin,
		"--clean", "-i", "NONE", "--not-a-term", "-es",
		"-c", "redir! >>/dev/stdout",
		"-c", "echo &filetype",
		"-c", "redir END",
		"-c", "qa!",
		name,
	)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("vim on %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// TestExtOf holds the ':e' rule, which is the one piece of name arithmetic
// here that Go's own filepath disagrees with.
func TestExtOf(t *testing.T) {
	for _, c := range []struct{ base, want string }{
		{"a.go", "go"},
		{"Makefile", ""},
		{".bashrc", "bashrc"},
		{"x.tar.gz", "gz"},
		{".foo.bar", "bar"},
		{"a.", ""},
		{".", ""},
	} {
		if got := extOf(c.base); got != c.want {
			t.Errorf("extOf(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// TestGlobMatch covers the matcher's own corners, which the detection table
// exercises only by accident: "*" across a separator, "?" for one byte, and
// the three character classes the pattern table needs.
func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		pat, name string
		want      bool
	}{
		{"*.go", "a.go", true},
		{"*.go", "a.god", false},
		{"*.git/config", "/home/x/.git/config", true},
		{"*.git/config", "/home/x/git/config", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"*.[dD]ockerfile", "web.Dockerfile", true},
		{"*.[dD]ockerfile", "web.dockerfile", true},
		{"*.[dD]ockerfile", "web.Xockerfile", false},
		{"[jt]sconfig*.json", "tsconfig.app.json", true},
		{"[jt]sconfig*.json", "xsconfig.json", false},
		{".bash[_-]profile", ".bash_profile", true},
		{".bash[_-]profile", ".bash-profile", true},
		{".bash[_-]profile", ".bashXprofile", false},
		{"[a-z].txt", "q.txt", true},
		{"[a-z].txt", "Q.txt", false},
		{"[^a-z].txt", "Q.txt", true},
		// An unclosed bracket is a literal one, which is what vim does and
		// what stops a typo in the table from matching everything.
		{"[abc", "[abc", true},
		{"*", "anything/at/all", true},
		{"", "", true},
		{"", "x", false},
	} {
		if got := globMatch(c.pat, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

// TestDetectSaysNothingWhenItKnowsNothing is the other half of the contract.
// A caller sets 'filetype' to what this returns, and an empty answer has to
// mean "leave it alone" rather than "set it to empty", so a file nobody
// recognises must not come back with a guess on it.
func TestDetectSaysNothingWhenItKnowsNothing(t *testing.T) {
	for _, name := range []string{"", ".", "noextension", "a.qqqqzz", "a.xyzzy"} {
		if got := Detect(name, []byte("nothing recognisable\n")); got != "" {
			t.Errorf("Detect(%q) = %q, want the empty answer", name, got)
		}
	}
}

// TestDetectOnAnEmptyBuffer is the BufNewFile case: ":e new.py" on a file that
// does not exist yet still has to answer python, because every FileType
// autocommand in the vimrc has to fire on a new file as well as a read one.
func TestDetectOnAnEmptyBuffer(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"new.py", "python"},
		{"new.go", "go"},
		{"new.md", "markdown"},
		{"new.yaml", "yaml"},
		{"Makefile", "make"},
	} {
		if got := Detect(c.name, nil); got != c.want {
			t.Errorf("Detect(%q, nil) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestKnownDifferencesFromVim is the register for this package: the answers
// vim gives that this one deliberately does not, each with the reason.
//
// It is a test and not a comment so that a change which accidentally closes
// one of these goes red and gets read, rather than quietly making the list
// wrong. Two rows after a sweep of 1,113 real files on this machine.
func TestKnownDifferencesFromVim(t *testing.T) {
	for _, c := range []struct {
		name, body, want, vim, why string
	}{
		{
			name: "pack/*/start/nerdtree/doc/NERDTree.txt",
			body: "*NERDTree.txt*\n",
			want: "text",
			vim:  "help",
			// filetype.vim line 59 reads getline('$') for a "vim:ft=help"
			// modeline, and the last line of the file is the one thing Detect
			// is never handed: it takes the first bytes so that opening a
			// 40k-line log costs one read. 'modeline' is off in this editor
			// by the list, so there is no second route to help
			// either. Every *.txt in a plugin's doc directory is this.
			why: "the help modeline is on the last line and Detect reads the first bytes",
		},
		{
			name: "gosnippets/UltiSnips/go.snippets",
			body: "snippet main\n",
			want: "",
			vim:  "snippets",
			// Not vim's answer at all: it comes from vim-go's own
			// ftdetect/snippets.vim, one of the 43,360 lines of plugin
			// vimscript this editor does not run. Every plugin ftdetect file
			// is this row.
			why: "a plugin's own ftdetect, and pvim loads no plugins",
		},
	} {
		if got := Detect(c.name, []byte(c.body)); got != c.want {
			t.Errorf("Detect(%q) = %q, want %q (vim says %q: %s)", c.name, got, c.want, c.vim, c.why)
		}
	}
}
