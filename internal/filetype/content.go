package filetype

import "strings"

// Detection from the file's contents.
//
// Two different things live here and vim keeps them apart too. The functions
// named ft* are the dist#ft# calls a *name* pattern reaches -- a *.tf is
// terraform or tf depending on what is in it, a *.sh with a zsh shebang is a
// zsh file -- and they run as part of the pattern table. fromContent is
// scripts.vim, which vim runs only when the name has told it nothing at all,
// and which is where a file called "install" turns out to be a shell script.
//
// Ported from runtime/autoload/dist/script.vim and
// runtime/autoload/dist/ft.vim, vim 9.2.0321.

// ftTf is dist#ft#FTtf, whole:
//
//	for i in range(1, line('$'))
//	 var currentLine = trim(getline(i))
//	 var firstCharacter = currentLine[0]
//	 if firstCharacter !=? ";" && firstCharacter !=? "/" && firstCharacter !=? ""
//	 setf terraform
//	 return
//	 endif
//	endfor
//	setf tf
//
// The mud client wins only on a file whose every line is blank or starts with
// ";" or "/", which no terraform file is and which an empty new file is. That
// last one matters: ":e new.tf" on a file that does not exist is a BufNewFile
// with an empty buffer, and vim answers "tf" for it, not "terraform". Measured
// on this machine, and reproduced here rather than fixed, because the vimrc's
// FileType list names both and the difference costs nothing.
//
// Vim reads the whole file; this reads the sniffed head. A terraform file
// whose first 8 KB are all comments and blank lines is the only input the two
// disagree on.
func ftTf(_ string, head []byte) string {
	for _, l := range firstLines(head, 1<<20) {
		l = strings.Trim(l, " \t\r")
		if l == "" || l[0] == ';' || l[0] == '/' {
			continue
		}
		return "terraform"
	}
	return "tf"
}

// ftTs is filetype.vim line 1191, which is written inline rather than as a
// function: a *.ts is a Qt translation file when its first line has "<?xml" in
// it and TypeScript otherwise.
func ftTs(_ string, head []byte) string {
	if strings.Contains(line(head, 1), "<?xml") {
		return "xml"
	}
	return "typescript"
}

// ftHTML is dist#ft#FThtml, reduced to the XHTML branch and the fallback.
//
// Vim reads forty lines looking for four things: Angular control flow, an
// XHTML DTD, Django template tags and SuperHTML. Three of those turn an .html
// into a filetype no ftplugin on this machine has an opinion about and no line
// of the vimrc names, so they would be three tables of syntax markers bought
// for nothing. The XHTML one is here because it is a single unambiguous
// string, and because "html" and "xhtml" are the pair a person actually
// notices getting wrong.
func ftHTML(_ string, head []byte) string {
	for _, l := range firstLines(head, 40) {
		// Vim's pattern is '\<DTD\s\+XHTML\s'. The word boundary and the
		// run of white space are the whole of it, and a DOCTYPE line has one
		// space, so a Contains of "DTD XHTML" is the same test on every real
		// input and is not a regexp.
		if strings.Contains(l, "DTD XHTML") {
			return "xhtml"
		}
	}
	return "html"
}

// ftShell is dist#ft#SetFileTypeSH(getline(1)), which filetype.vim line 1039
// hands the file's own first line so that a *.sh with a zsh shebang is a zsh
// file.
func ftShell(_ string, head []byte) string {
	return shellFromName(line(head, 1))
}

// shellFromName is the dialect half of dist#ft#SetFileTypeSH.
//
// Vim's function takes either a bare shell name ("bash", from the *.bash
// pattern) or a whole "#!" line, and the patterns it tests are pairs:
//
//	if name =~ '^csh$' || name =~ '^#!.\{-2,}\<csh\>'
//
// The answer is a filetype for csh, tcsh and zsh, and "sh" for everything else
// -- ksh, bash, dash and sh all set b:is_kornshell / b:is_bash / b:is_sh and
// then fall through to SetFileTypeShell("sh"). Measured: ":e x.bash" answers
// &filetype "sh", not "bash". Those three buffer variables are syntax-file
// input and syntax is not built, so they are not here.
func shellFromName(name string) string {
	name = strings.TrimRight(name, "\r")
	switch {
	case name == "csh" || hasShellWord(name, "csh"):
		return "csh"
	case name == "tcsh" || hasShellWord(name, "tcsh"):
		return "tcsh"
	case name == "zsh" || hasShellWord(name, "zsh"):
		return "zsh"
	}
	return "sh"
}

// hasShellWord is vim's '^#!.\{-2,}\<NAME\>' applied to a first line: the line
// starts with "#!" and NAME appears in it as a whole word.
//
// "\{-2,}" is at least two characters between the "#!" and the word, which is
// why "#!csh" does not match this half and is caught by the bare-name half
// instead. The word boundary is what stops "#!/bin/tcsh" from answering csh,
// and getting that wrong is the reason this is a function and not a Contains
// at each call site.
func hasShellWord(line, word string) bool {
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	rest := line[2:]
	for i := 0; i+len(word) <= len(rest); i++ {
		if rest[i:i+len(word)] != word {
			continue
		}
		if i < 2 {
			continue // the "\{-2,}" in vim's pattern
		}
		if isWordByte(rest[i-1]) {
			continue
		}
		if i+len(word) < len(rest) && isWordByte(rest[i+len(word)]) {
			continue
		}
		return true
	}
	return false
}

// isWordByte is vim's \w for the purposes of \< and \>: a letter, a digit or
// an underscore. 'iskeyword' does not come into it, because vim's own \< in
// this pattern is not iskeyword-aware either.
func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// fromContent is runtime/scripts.vim, which vim sources only when nothing has
// set a filetype from the name. It is dist#script#DetectFiletype: a "#!" line
// goes through the interpreter table, anything else through a handful of
// first-line tests.
func fromContent(head []byte) string {
	first := strings.TrimRight(line(head, 1), "\r")
	if strings.HasPrefix(first, "#!") {
		return fromHashBang(first)
	}
	return fromText(head, first)
}

// fromHashBang is dist#script#DetectFromHashBang: work out the interpreter's
// name, then look it up.
//
// Vim's name extraction is four substitute() calls over three shapes, and this
// is the same three:
//
//	"#!c:/program files/perl" a DOS path, take the last \i+ run
//	"#!/usr/bin/env perl" the word after "env"
//	"#!perl args" no path at all, the first word
//	"#!/usr/bin/perl args" the last component of the path
//
// The "env" case first strips VAR=value assignments, -i/-S flags and
// --split-string, which is what makes "#!/usr/bin/env -S python3 -u" resolve.
func fromHashBang(first string) string {
	s := strings.TrimSpace(first[2:])

	// Cut the arguments off: the interpreter is the first field. Vim does
	// this with \i\+ and \f\+ patterns; a field split is the same thing on a
	// line that is by definition one path and then arguments.
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]

	if base := lastPathElement(name); base == "env" {
		// Skip the options vim recognises and the VAR=value assignments,
		// then take the first word that is left.
		name = ""
		for _, f := range fields[1:] {
			switch {
			case f == "--split-string" || f == "-S" || f == "-i" || f == "-iS" ||
				f == "--ignore-environment" || strings.HasPrefix(f, "--split-string="):
				continue
			case strings.Contains(f, "=") && !strings.HasPrefix(f, "-"):
				continue // VAR=value
			}
			name = f
			break
		}
		if name == "" {
			return ""
		}
	}
	return exe2filetype(lastPathElement(name), first)
}

// lastPathElement is the "\(\i\+\)" at the end of vim's path patterns: the
// component after the last "/" or "\".
func lastPathElement(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		return s[i+1:]
	}
	return s
}

// exe2filetype is dist#script#Exe2filetype, in its own order.
//
// Vim's is a chain of forty elseifs over the interpreter's name and the order
// matters in two places: "make" is tested with a trailing \> only, so
// "gnumake" answers make, and the javascript row matches "node", "nodejs" or
// anything ending in "js", which would swallow names a later row wants. Kept
// as a slice of tests rather than a map for exactly that reason -- a map would
// lose the order and the prefix and suffix rules with it.
//
// Cut from vim's list: nothing. It is forty rows of string comparison and the
// day one of them is the answer is the day it would otherwise have been a
// silent nothing.
func exe2filetype(name, first string) string {
	switch {
	// Bourne-like shells: bash bash2 dash ksh ksh93 sh. Vim hands the whole
	// "#!" line back to SetFileTypeSH, which is what makes "#!/bin/sh" inside
	// a file whose shebang also says csh come out right.
	case wordPrefix(name, "bash"), wordPrefix(name, "dash"),
		wordPrefix(name, "ksh"), wordPrefix(name, "sh"):
		return shellFromName(first)
	case wordPrefix(name, "csh"):
		return "csh"
	case wordPrefix(name, "tcsh"):
		return "tcsh"
	case wordPrefix(name, "zsh"):
		return "zsh"
	case wordPrefix(name, "tclsh"), wordPrefix(name, "wish"),
		wordPrefix(name, "expectk"), wordPrefix(name, "itclsh"), wordPrefix(name, "itkwish"):
		return "tcl"
	case wordPrefix(name, "expect"):
		return "expect"
	case wordPrefix(name, "gnuplot"):
		return "gnuplot"
	case wordSuffix(name, "make"):
		return "make"
	case wordPrefix(name, "pike"):
		return "pike"
	case strings.Contains(name, "lua"):
		return "lua"
	case strings.Contains(name, "perl"):
		return "perl"
	case strings.Contains(name, "php"):
		return "php"
	case strings.Contains(name, "python"):
		return "python"
	case wordPrefix(name, "groovy"):
		return "groovy"
	case strings.Contains(name, "raku"):
		return "raku"
	case strings.Contains(name, "ruby"):
		return "ruby"
	case wordSuffix(name, "node"), wordSuffix(name, "nodejs"),
		wordSuffix(name, "js"), wordSuffix(name, "rhino"):
		return "javascript"
	case strings.Contains(name, "just"):
		return "just"
	case wordPrefix(name, "bc"):
		return "bc"
	case wordSuffix(name, "sed"):
		return "sed"
	case strings.Contains(name, "ocaml"):
		return "ocaml"
	case wordSuffix(name, "awk"):
		return "awk"
	case strings.Contains(name, "wml"):
		return "wml"
	case strings.Contains(name, "scheme"), strings.Contains(name, "guile"):
		return "scheme"
	case strings.Contains(name, "cfengine"):
		return "cfengine"
	case strings.Contains(name, "escript"):
		return "erlang"
	case strings.Contains(name, "haskell"):
		return "haskell"
	case wordSuffix(name, "scala"):
		return "scala"
	case strings.Contains(name, "clojure"):
		return "clojure"
	case wordPrefix(name, "instantfpc"):
		return "pascal"
	case wordPrefix(name, "fennel"):
		return "fennel"
	case wordSuffix(name, "rsc"):
		return "routeros"
	case wordSuffix(name, "fish"):
		return "fish"
	case wordSuffix(name, "gforth"):
		return "forth"
	case wordSuffix(name, "icon"):
		return "icon"
	case strings.Contains(name, "nix-shell"):
		return "nix"
	case wordPrefix(name, "crystal"):
		return "crystal"
	case wordPrefix(name, "rexx"), wordPrefix(name, "regina"):
		return "rexx"
	case wordPrefix(name, "janet"):
		return "janet"
	case wordPrefix(name, "dart"):
		return "dart"
	case wordPrefix(name, "execlineb"):
		return "execline"
	case wordPrefix(name, "bpftrace"):
		return "bpftrace"
	case wordPrefix(name, "vim"):
		return "vim"
	}
	return ""
}

// wordPrefix is vim's '^NAME\>': the name starts with the word and what
// follows it, if anything, is not a word character. "bash2" matches "bash"
// under this rule and vim's own row spells that out with '^bash2\=$'.
func wordPrefix(name, word string) bool {
	if !strings.HasPrefix(name, word) {
		return false
	}
	rest := name[len(word):]
	if rest == "" {
		return true
	}
	// Vim's \> after a name is followed in every row of this table by either
	// nothing or a version digit run: bash2, ksh93, php8, pike7.
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

// wordSuffix is vim's 'NAME\>' with no anchor: the word appears in the name
// and ends it or is followed by a non-word byte. "gnumake" answers make and
// "makefile" does not.
func wordSuffix(name, word string) bool {
	for i := 0; i+len(word) <= len(name); i++ {
		if name[i:i+len(word)] != word {
			continue
		}
		if i+len(word) < len(name) && isWordByte(name[i+len(word)]) {
			continue
		}
		return true
	}
	return false
}

// fromText is dist#script#DetectFromText, reduced to the checks that answer
// with a type this editor or this vimrc has an opinion about.
//
// Vim's version is about sixty branches long and most of it is mail formats,
// diff output, PostScript, MOO databases and a dozen assemblers. What is here
// is the shell and zsh openings, the two markup declarations, and the vim
// script one, each with the vim pattern it came from beside it. Everything
// else is left to answer nothing, which is the same thing this package does
// for a file it has never heard of.
func fromText(head []byte, first string) string {
	switch {
	// '^:$' -- an old Bourne script whose first line is a bare colon.
	case first == ":":
		return "sh"
	// '^#compdef\>' and '^#autoload\>'.
	case wordAfter(first, "#compdef"), wordAfter(first, "#autoload"):
		return "zsh"
	// '<?\s*xml.*?>'.
	case xmlDecl(first):
		return "xml"
	// '<!DOCTYPE\s\+html\>', case-insensitive, with the XHTML DTD taking
	// precedence the way FThtml has it.
	case docTypeHTML(first):
		if strings.Contains(first, "DTD XHTML") {
			return "xhtml"
		}
		return "html"
	// '^" *[vV]im$' -- a vim script that says so on its first line.
	case isVimHeader(first):
		return "vim"
	}
	// The zsh "emulate sh" check reads five lines rather than one:
	// '\n\s*emulate\s\+\%(-[LR]\s\+\)\=[ckz]\=sh\>' over lines one to five.
	for _, l := range firstLines(head, 5) {
		if isZshEmulate(strings.TrimRight(l, "\r")) {
			return "zsh"
		}
	}
	return ""
}

// wordAfter is '^WORD\>' on a line.
func wordAfter(line, word string) bool {
	if !strings.HasPrefix(line, word) {
		return false
	}
	return len(line) == len(word) || !isWordByte(line[len(word)])
}

// xmlDecl is '<?\s*xml.*?>'.
func xmlDecl(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "<?")
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, "xml") {
		return false
	}
	return strings.Contains(rest, "?>")
}

// docTypeHTML is '<!DOCTYPE\s\+html\>', matched without regard to case as
// vim's =? does.
func docTypeHTML(line string) bool {
	low := strings.ToLower(strings.TrimLeft(line, " \t"))
	rest, ok := strings.CutPrefix(low, "<!doctype")
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	return wordAfter(rest, "html")
}

// isVimHeader is '^" *[vV]im$'.
func isVimHeader(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, " \t\r"), `"`)
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, " ")
	return rest == "vim" || rest == "Vim"
}

// isZshEmulate is '\s*emulate\s\+\%(-[LR]\s\+\)\=[ckz]\=sh\>' anchored at the
// start of a line, which is what the "\n" in vim's pattern amounts to.
func isZshEmulate(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "emulate")
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) >= 2 && rest[0] == '-' && (rest[1] == 'L' || rest[1] == 'R') {
		rest = strings.TrimLeft(rest[2:], " \t")
		if rest == "" {
			return false
		}
	}
	if len(rest) > 0 && strings.IndexByte("ckz", rest[0]) >= 0 {
		rest = rest[1:]
	}
	return wordAfter(rest, "sh")
}

// ftCfg is dist#ft#FTcfg minus the g:filetype_cfg branch: "cfg", unless
// IsRapid("cfg") says the file opens with an ABB robot controller section
// header, whose pattern is '\v^%(EIO|MMC|MOC|PROC|SIO|SYS):CFG' matched
// without regard to case.
func ftCfg(_ string, head []byte) string {
	first := strings.ToUpper(strings.TrimRight(line(head, 1), "\r"))
	for _, section := range []string{"EIO", "MMC", "MOC", "PROC", "SIO", "SYS"} {
		if strings.HasPrefix(first, section+":CFG") {
			return "rapid"
		}
	}
	return "cfg"
}

// ftPatch is filetype.vim line 379: a patch produced by "git format-patch" has
// git's fake From line at the top and is a gitsendemail buffer; anything else
// is a diff.
//
//	if getline(1) =~# '^From [0-9a-f]\{40,\} Mon Sep 17 00:00:00 2001$'
func ftPatch(_ string, head []byte) string {
	first := strings.TrimRight(line(head, 1), "\r")
	rest, ok := strings.CutPrefix(first, "From ")
	if !ok {
		return "diff"
	}
	n := 0
	for n < len(rest) && isHexByte(rest[n]) {
		n++
	}
	if n >= 40 && rest[n:] == " Mon Sep 17 00:00:00 2001" {
		return "gitsendemail"
	}
	return "diff"
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}

// ftPl is dist#ft#FTpl minus the g:filetype_pl branch: perl, unless the first
// non-blank line names prolog.
//
// Vim's test is '\<prolog\>' or its prolog_pattern, a longer alternation of
// Prolog directives. Only the first half is here: a .pl on this machine is
// perl, the second half is nine directive names, and a wrong answer costs a
// syntax file that is not built. Named rather than silently dropped.
func ftPl(_ string, head []byte) string {
	for _, l := range firstLines(head, 200) {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if wordSuffix(l, "prolog") {
			return "prolog"
		}
		break
	}
	return "perl"
}

// ftConfFallback is filetype.vim line 1636, the last autocommand in the file
// and the one whose own comment is "Use FALLBACK, it's just guessing!":
//
//	if !did_filetype() && expand("<amatch>") !~ g:ft_ignore_pat
//	 && (expand("<amatch>") =~# '\.conf$'
//	 || getline(1) =~ '^#' || getline(2) =~ '^#'
//	 || getline(3) =~ '^#' || getline(4) =~ '^#'
//	 || getline(5) =~ '^#')
//	 setf FALLBACK conf
//
// It is why an unrecognised file with a comment at the top comes back "conf"
// and not empty, and leaving it out would have made this package quietly
// disagree with vim on every dotfile in /etc.
func ftConfFallback(base string, head []byte) string {
	if strings.HasSuffix(base, ".conf") {
		return "conf"
	}
	for _, l := range firstLines(head, 5) {
		if strings.HasPrefix(l, "#") {
			return "conf"
		}
	}
	return ""
}

// ftGit is filetype.vim line 485:
//
//	au BufNewFile,BufRead *.git/*
//	 \ if getline(1) =~# '^\x\{40,\}\>\|^ref: ' |
//	 \ setf git |
//	 \ endif
//
// A file under a .git directory is a git file when its first line is an object
// name or a symbolic ref, which is HEAD, ORIG_HEAD, FETCH_HEAD and everything
// under refs/. It is the one rule in this package that fires on files a person
// does not open on purpose and a fugitive-shaped :Gstatus will, and it answers
// nothing for the rest of .git, which is why the whole of a repository's
// internals does not come back "git".
func ftGit(_ string, head []byte) string {
	first := strings.TrimRight(line(head, 1), "\r")
	if strings.HasPrefix(first, "ref: ") {
		return "git"
	}
	n := 0
	for n < len(first) && isHexByte(first[n]) {
		n++
	}
	if n >= 40 && (n == len(first) || !isWordByte(first[n])) {
		return "git"
	}
	return ""
}

// ftMod is dist#ft#FTmod, in vim's own order: Modula-2 first, then go.mod,
// then LambdaProlog, then ABB RAPID, then modsim3 as the "nothing recognized"
// answer.
//
// The two middle checks are not ported. IsLProlog skips leading "%" comment
// lines and then matches 'module\s\+\w\+\s*\.\s*\(%\|$\)'; IsRapid matches
// '^\s*%%%\|module\s\+\k\+\s*[(]\?$'. Both are one language nothing on this
// machine writes, and a *.mod that is one of them comes back "modsim3" here,
// which is the answer vim gives an unrecognised *.mod anyway. go.mod is the
// row that had to exist: a Go repository has one and it was the first
// difference the sweep over this repository found.
func ftMod(base string, head []byte) string {
	if isModula2(head) {
		return "modula2"
	}
	if base == "go.mod" || strings.HasSuffix(base, "/go.mod") {
		return "gomod"
	}
	return "modsim3"
}

// isModula2 is dist#ft#IsModula2, whose whole body is
//
//	getline(nextnonblank(1)) =~ '\<MODULE\s\+\w\+\s*\%(\[.*]\s*\)\=;\|^\s*(\*'
//
// a MODULE declaration or an opening comment bracket on the first non-blank
// line.
func isModula2(head []byte) bool {
	for _, l := range firstLines(head, 200) {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), "(*") {
			return true
		}
		return moduleDecl(l)
	}
	return false
}

// moduleDecl is '\<MODULE\s\+\w\+\s*\%(\[.*]\s*\)\=;': the word MODULE, a
// name, an optional bracketed priority, and a semicolon.
func moduleDecl(l string) bool {
	i := strings.Index(l, "MODULE")
	if i < 0 || (i > 0 && isWordByte(l[i-1])) {
		return false
	}
	rest := l[i+len("MODULE"):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	n := 0
	for n < len(rest) && isWordByte(rest[n]) {
		n++
	}
	if n == 0 {
		return false
	}
	rest = strings.TrimLeft(rest[n:], " \t")
	if strings.HasPrefix(rest, "[") {
		if j := strings.LastIndex(rest, "]"); j >= 0 {
			rest = strings.TrimLeft(rest[j+1:], " \t")
		}
	}
	return strings.HasPrefix(rest, ";")
}

// ftHeader is dist#ft#FTheader minus the two g: variables nothing can set: a
// *.h is Objective-C++, C++ or C, decided by dist#ft#CheckObjCOrCpp over the
// first hundred lines.
//
//	if line =~ '\v^\s*\@%(class|interface|end)>'
//	 return 'objcpp'
//	elseif line =~ '\v^\s*%(class|namespace|template|using)>'
//	 return 'cpp'
//
// and C when neither shows up.
func ftHeader(_ string, head []byte) string {
	for _, l := range firstLines(head, 100) {
		l = strings.TrimLeft(strings.TrimRight(l, "\r"), " \t")
		if rest, ok := strings.CutPrefix(l, "@"); ok {
			for _, kw := range []string{"class", "interface", "end"} {
				if wordAfter(rest, kw) {
					return "objcpp"
				}
			}
			continue
		}
		for _, kw := range []string{"class", "namespace", "template", "using"} {
			if wordAfter(l, kw) {
				return "cpp"
			}
		}
	}
	return "c"
}

// ftVirata is filetype.vim line 1224: a *.hw, *.module or *.pkg is PHP when
// its first line has "<?php" in it and a Virata config file otherwise.
func ftVirata(_ string, head []byte) string {
	if strings.Contains(line(head, 1), "<?php") {
		return "php"
	}
	return "virata"
}
