package filetype

import "strings"

// The explicit autocommand patterns from runtime/filetype.vim, vim 9.2.0321,
// read.
//
// This is the one table here that is a selection rather than a port, because
// the file has 1,264 of these lines and most of them are for a config format
// nothing on this machine has ever opened. What is taken:
//
// - every type the vimrc names, which is what this whole piece of work is
// for: bash, sh, ps1, template, bzl, bazel, html, yaml, json, tf,
// terraform, hcl, tmpl, python, go, javascript, markdown, css, java and
// the txt/md pair the spell autocommands want;
// - the rest of the list in the brief: make and gitcommit;
// - the languages a Go machine has open beside them: c, cpp, typescript,
// lua, php, sql, toml, vim, zsh, groovy, dockerfile, and git's own
// config/ignore/attributes files.
//
// Each row carries the line of filetype.vim it came from. A row with a fn
// instead of an ft is one of the entries vim implements as a call into
// dist#ft#, and the function beside it says which one and how much of it is
// here.
//
// Order is filetype.vim's order, ascending by line number, because that is the
// order vim registers the autocommands in and "setf" is first-match-wins. Two
// rows can match one name -- ".bash_history" is both the bash rule at 1037 and
// nothing else, but "makefile.am" is byName's automake and would have been the
// make rule at 687 -- and the earlier one has to win.
type rule struct {
	// pats is the pattern list of one autocommand, as written.
	pats []string
	// ft is the filetype it sets, empty when fn decides.
	ft string
	// fn reads the first bytes of the file and answers, which is what vim's
	// dist#ft# functions do. An empty answer means no match, so detection
	// carries on down the list -- vim has no such case in the rows taken
	// here, every one of these functions ends in a setf, but the shape costs
	// nothing and makes a half-ported function say so rather than lie.
	fn func(base string, head []byte) string
	// guard says this row refuses a name matching g:ft_ignore_pat, which is
	// what filetype.vim's s:StarSetf() wrapper is for and what the generic
	// conf fallback at the end of the file tests for itself. See ignored.
	guard bool
	// line is the line of runtime/filetype.vim this came from.
	line int
}

var patterns = []rule{
	{pats: []string{"*.bzl", "*.bxl", "*.bazel", "WORKSPACE", "WORKSPACE.bzlmod"}, ft: "bzl", line: 176},
	{pats: []string{"*.BUILD", "BUILD", "BUCK"}, ft: "bzl", line: 179},
	// FTlpc, which is "setf c" unless g:lpc_syntax_for_c is set. Nothing sets
	// it and pvim has no way to, so the branch is not here.
	{pats: []string{"*.c"}, ft: "c", line: 186},
	// Both are "setf cpp" unless cynlib_syntax_for_cc / _cpp exists. Same
	// reasoning as *.c.
	{pats: []string{"*.cc", "*.cpp"}, ft: "cpp", line: 209},
	// FTheader. A .h is C, C++, Ch or Objective-C, and vim reads a hundred
	// lines to decide; so does ftHeader. It earns its place: a sweep over
	// node's bundled headers found 45 files this got wrong when it was a flat
	// "c", which was every difference in that run bar thirteen.
	{pats: []string{"*.h"}, fn: ftHeader, line: 221},
	{pats: []string{"Containerfile", "Dockerfile", "dockerfile", "*.[dD]ockerfile"}, ft: "dockerfile", line: 281},
	// FTcfg, minus the g:filetype_cfg branch nothing can set.
	{pats: []string{"*.cfg", "*.Cfg", "*.CFG"}, fn: ftCfg, line: 336},
	{pats: []string{"*.patch"}, fn: ftPatch, line: 379},
	{pats: []string{"*.git/config", ".gitconfig", "*/etc/gitconfig"}, ft: "gitconfig", line: 465},
	{pats: []string{".gitmodules", "*.git/modules/*/config"}, ft: "gitconfig", line: 469},
	{pats: []string{".gitattributes", "*.git/info/attributes"}, ft: "gitattributes", line: 475},
	{pats: []string{".gitignore", "*.git/info/exclude"}, ft: "gitignore", line: 478},
	{pats: []string{"*/.config/git/ignore", "*.prettierignore"}, ft: "gitignore", line: 479},
	{pats: []string{"*/.config/fd/ignore", ".fdignore", ".ignore"}, ft: "gitignore", line: 480},
	{pats: []string{".rgignore", ".dockerignore", ".containerignore"}, ft: "gitignore", line: 481},
	{pats: []string{".npmignore", ".vscodeignore"}, ft: "gitignore", line: 482},
	{pats: []string{"git-rebase-todo"}, ft: "gitrebase", line: 483},
	// A file inside a .git directory whose first line is an object name or a
	// symbolic ref: HEAD, ORIG_HEAD, the files under refs/. The guard is
	// vim's own and it is what stops this from claiming every hook script and
	// every packed-refs file in there.
	{pats: []string{"*.git/*"}, fn: ftGit, line: 485},
	{pats: []string{"*.gradle", "*.groovy", "Jenkinsfile"}, ft: "groovy", line: 520},
	{pats: []string{"*.html", "*.htm", "*.shtml", "*.stm"}, fn: ftHTML, line: 540},
	{pats: []string{"*.jsonc", ".babelrc", ".eslintrc", ".jsfmtrc", "bun.lock"}, ft: "jsonc", line: 594},
	{pats: []string{".jshintrc", ".jscsrc", ".vsconfig", ".hintrc", ".swrc", "[jt]sconfig*.json"}, ft: "jsonc", line: 595},
	{pats: []string{"*.lua", "*.tlu", ".lua_history"}, ft: "lua", line: 667},
	// FTmake, which decides a b:make_flavor from the contents and then always
	// says "setf make". Only the answer is here; the flavour is a syntax-file
	// variable and syntax is not built.
	{pats: []string{"*[mM]akefile", "*.mk", "*.mak"}, ft: "make", line: 687},
	{pats: []string{"*.markdown", "*.mdown", "*.mkd", "*.mkdn", "*.mdwn", "*.md"}, ft: "markdown", line: 702},
	// FTmod, whose comment reads "Determine if *.mod is ABB RAPID,
	// LambdaProlog, Modula-2, Modsim III or go.mod".
	{pats: []string{"*.mod"}, fn: ftMod, line: 728},
	// FTpl: perl, or prolog when the first non-blank line says so.
	{pats: []string{"*.pl", "*.PL"}, fn: ftPl, line: 858},
	{pats: []string{"*.plx", "*.al", "*.psgi"}, ft: "perl", line: 862},
	{pats: []string{"*.php", "*.phtml", "*.ctp", "*.phpt", "*.theme"}, ft: "php", line: 879},
	{pats: []string{"*.ps1", "*.psd1", "*.psm1", "*.pssc"}, ft: "ps1", line: 902},
	{pats: []string{"*.py", "*.pyw", ".pythonstartup", ".pythonrc", ".python_history", ".jline-jython.history"}, ft: "python", line: 932},
	{pats: []string{"*.ipy", "*.ptl", "*.pyi", "SConstruct"}, ft: "python", line: 933},
	// SetFileTypeSH("bash"): the name says bash, which in vim sets b:is_bash
	// and the filetype "sh". Measured: ":e sample.bash" answers &filetype "sh".
	{pats: []string{
		".bashrc", "bashrc", "bash.bashrc", ".bash[_-]profile", ".bash[_-]logout",
		".bash[_-]aliases", ".bash[_-]history", "*.ebuild", "*.bash", "*.eclass",
		"PKGBUILD", "*.bats", "*.cygport",
	}, ft: "sh", line: 1037},
	// SetFileTypeSH(getline(1)): here the first line decides, because a *.sh
	// whose shebang says zsh is a zsh file. See shellFromName.
	{pats: []string{"*/etc/profile", ".profile", "*.sh", "*.envrc", ".envrc.*"}, fn: ftShell, line: 1039},
	{pats: []string{".zprofile", "*/etc/zprofile", ".zfbfmarks"}, ft: "zsh", line: 1056},
	{pats: []string{".zshrc", ".zshenv", ".zlogin", ".zlogout", ".zcompdump", ".zsh_history"}, ft: "zsh", line: 1057},
	{pats: []string{"*.zsh", "*.zsh-theme", "*.zunit"}, ft: "zsh", line: 1058},
	// SQL(), which is "setf sql" unless g:filetype_sql names something else.
	{pats: []string{"*.sql"}, ft: "sql", line: 1096},
	{pats: []string{".sqlite_history"}, ft: "sql", line: 1097},
	// FTtf: terraform, or the TinyFugue mud client's "tf" when every line in
	// the file is blank or starts with ";" or "/".
	{pats: []string{"*.tf"}, fn: ftTf, line: 1180},
	{pats: []string{"*.toml", "uv.lock"}, ft: "toml", line: 1186},
	// "TypeScript or Qt translation file (which is XML)".
	{pats: []string{"*.ts"}, fn: ftTs, line: 1191},
	{pats: []string{".ts_node_repl_history"}, ft: "typescript", line: 1195},
	{pats: []string{"*.hw", "*.module", "*.pkg"}, fn: ftVirata, line: 1224},
	{pats: []string{"*.vim", ".exrc", "_exrc", ".netrwhist"}, ft: "vim", line: 1221},
	// ".env{.*,}" is vim's brace expansion, which this matcher does not have:
	// the two alternatives are written out instead. Same for the two
	// bash-completion patterns in the late table below.
	{pats: []string{"*.env", ".env", ".env.*"}, ft: "env", line: 1423},
}

// late is the rules filetype.vim puts after the content sniffing, each with
// vim's own comment for why.
//
// Line 1353: "Plain text files, needs to be far down to not override others.
// This avoids the 'conf' type being used if there is a line starting with '#'."
//
// Lines 1578 to 1611 are s:StarSetf() calls: "Extra checks for when no
// filetype has been detected now. Mostly used for patterns that end in '*'.
// E.g., 'zsh*' matches 'zsh.vim', but that's a Vim script file."
//
// Line 1614: "Help files match *.txt but should have a last line that is a
// modeline." The modeline test is not here and cannot be: 'modeline' is off in
// this editor and reading one is out of scope, and vim's check reads
// getline('$'), the last line of the file, which this package is never handed.
// A *.txt whose last line says "vim:ft=help" therefore gets "text" here and
// "help" in vim, which is the one deliberate difference in this file. The
// vimrc's own interest in *.txt is the spell autocommand, which fires on the
// name and not the type, so nothing in the config notices.
//
// Line 1636 is the generic conf guess, and vim's own comment on it is "Use
// FALLBACK, it's just guessing!". It is last for a reason: it claims any file
// whose name ends in ".conf" or whose first five lines have a "#" in column
// one, which is most shell scripts, most yaml and most of /etc.
var late = []rule{
	{pats: []string{"*.text", "README", "LICENSE", "COPYING", "AUTHORS"}, ft: "text", line: 1353},
	{pats: []string{"*/doc/bash_completion*", "*/doc/.bash_completion*",
		"*/doc/bash-completion*", "*/doc/.bash-completion*"}, ft: "text", line: 1546},
	{pats: []string{
		".bashrc*", ".bash[_-]profile*", ".bash[_-]logout*", ".bash[_-]aliases*",
		"bash-fc[-.]*", "PKGBUILD*", "APKBUILD*",
		"*/bash[_-]completion*", "*/.bash[_-]completion*",
	}, ft: "sh", line: 1549},
	{pats: []string{".kshrc*"}, ft: "sh", line: 1550},
	{pats: []string{".profile*"}, fn: ftShell, line: 1551},
	{pats: []string{"*vimrc*"}, ft: "vim", guard: true, line: 1578},
	{pats: []string{".zsh*", ".zlog*", ".zcompdump*"}, ft: "zsh", guard: true, line: 1610},
	{pats: []string{"zsh*", "zlog*"}, ft: "zsh", guard: true, line: 1611},
	{pats: []string{"*.txt"}, ft: "text", line: 1614},
	{pats: []string{"*"}, fn: ftConfFallback, guard: true, line: 1636},
}

// ignoredSuffixes is g:ft_ignore_pat, '\.\(Z\|gz\|bz2\|zip\|tgz\)$': the
// compressed-file extensions s:StarSetf() refuses, so that "zsh.tgz" is not a
// zsh script.
var ignoredSuffixes = []string{".Z", ".gz", ".bz2", ".zip", ".tgz"}

func ignored(name string) bool {
	for _, s := range ignoredSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// matchPatterns walks the table in order and returns the first answer.
func matchPatterns(path, base string, head []byte) string {
	return match(patterns, path, base, head)
}

// matchLate walks the rules vim runs after the content sniffing.
func matchLate(path, base string, head []byte) string {
	return match(late, path, base, head)
}

func match(rules []rule, path, base string, head []byte) string {
	for _, r := range rules {
		if r.guard && ignored(base) {
			continue
		}
		for _, pat := range r.pats {
			// Vim's autocommand rule, the same one internal/vimrc's
			// Config.Match uses: a pattern with a "/" in it is matched
			// against the whole name and one without against the tail. It is
			// what makes "*.git/config" mean a path and ".gitconfig" mean a
			// file name wherever it sits.
			target := base
			if strings.Contains(pat, "/") {
				target = path
			}
			if !globMatch(pat, target) {
				continue
			}
			if r.fn != nil {
				if ft := r.fn(base, head); ft != "" {
					return ft
				}
				continue
			}
			return r.ft
		}
	}
	return ""
}

// globMatch is vim's autocommand pattern match: "*" is any run of bytes
// including a path separator, "?" is one byte, "[abc]" and "[a-z]" are a class
// with "^" or "!" negating, and everything else is itself.
//
// Not filepath.Match, whose "*" stops at a separator: "*.git/config" has to
// match "/path/to/pvim/.git/config" and filepath.Match says it does
// not. This is internal/vimrc's globMatch with character classes added, which
// three patterns in the table above need -- "*.[dD]ockerfile",
// "[jt]sconfig*.json" and the ".bash[_-]profile" family. The two copies are
// deliberate: internal/filetype imports nothing in this module, which is what
// keeps it a leaf, and exporting a matcher from internal/vimrc to save
// thirty lines would put an arrow from the leaf to the config loader.
func globMatch(pat, name string) bool {
	// px and nx are the current positions; star and mark remember the last
	// "*" so a mismatch can back up to it, which is the standard two-pointer
	// glob and is linear.
	px, nx, star, mark := 0, 0, -1, 0
	for nx < len(name) {
		matched := false
		if px < len(pat) {
			switch pat[px] {
			case '?':
				px++
				nx++
				matched = true
			case '*':
				star, mark = px, nx
				px++
				matched = true
			case '[':
				if end, ok := classMatch(pat[px:], name[nx]); ok {
					px += end
					nx++
					matched = true
				} else if end > 0 {
					// A well-formed class that did not match this byte.
					matched = false
				} else {
					// Not a class at all -- an unclosed "[" is a literal.
					matched = pat[px] == name[nx]
					if matched {
						px++
						nx++
					}
				}
			default:
				if pat[px] == name[nx] {
					px++
					nx++
					matched = true
				}
			}
		}
		if matched {
			continue
		}
		if star < 0 {
			return false
		}
		px = star + 1
		mark++
		nx = mark
	}
	for px < len(pat) && pat[px] == '*' {
		px++
	}
	return px == len(pat)
}

// classMatch tests one "[...]" against a byte.
//
// It returns the length of the class in the pattern and whether the byte is in
// it. A zero length means the pattern did not open a well-formed class at all,
// which the caller treats as a literal "[" -- vim does the same, and it is why
// a pattern like "[incomplete" matches a file called that.
func classMatch(pat string, b byte) (int, bool) {
	if len(pat) < 2 || pat[0] != '[' {
		return 0, false
	}
	i := 1
	negate := false
	if i < len(pat) && (pat[i] == '^' || pat[i] == '!') {
		negate = true
		i++
	}
	// A "]" straight after the bracket is a literal "]", which is how every
	// glob in every shell spells that class.
	first := true
	in := false
	for i < len(pat) {
		if pat[i] == ']' && !first {
			i++
			return i, in != negate
		}
		first = false
		lo := pat[i]
		i++
		if i+1 < len(pat) && pat[i] == '-' && pat[i+1] != ']' {
			hi := pat[i+1]
			i += 2
			if b >= lo && b <= hi {
				in = true
			}
			continue
		}
		if b == lo {
			in = true
		}
	}
	return 0, false // unterminated: not a class
}
