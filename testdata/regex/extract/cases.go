package main

// section is one block of the table: a list of pairs and the note that goes in
// the file header saying where they came from.
type section struct {
	name  string
	note  string
	pairs []pair
}

// classProbes are the characters every character class is tried against.
//
// Twelve, not a sweep of the whole of Latin-1, because the point of the table
// is to fail loudly when a class body drifts, and twelve characters that
// straddle every boundary in the twenty-six classes do that as well as ninety
// would while leaving the file readable. The boundaries they straddle: letter
// and digit, upper and lower, underscore, space and tab, the three punctuation
// characters 'isfname' disagrees with 'isident' about, and three code points
// above ASCII that 'isident' takes, 'iskeyword' takes and neither takes.
var classProbes = []string{"a", "Z", "5", "_", " ", "\t", "/", "-", "#", "é", "中", "Ā"}

// classAtoms is every character class vim has, in the order the help lists
// them.
var classAtoms = []string{
	`\s`, `\S`, `\d`, `\D`, `\x`, `\X`, `\o`, `\O`,
	`\w`, `\W`, `\h`, `\H`, `\a`, `\A`, `\l`, `\L`, `\u`, `\U`,
	`\i`, `\I`, `\k`, `\K`, `\f`, `\F`, `\p`, `\P`,
}

// classCases is every class against every probe: 312 rows that pin the body of
// each class one character at a time.
func classCases() []pair {
	var out []pair
	for _, atom := range classAtoms {
		for _, p := range classProbes {
			out = append(out, pair{atom, p})
		}
	}
	return out
}

// atomCases covers every atom the translator claims to handle, one row per
// behaviour rather than one per atom, because the atoms that go wrong go wrong
// at their edges: `*` at the start of a pattern, `$` in the middle of one, `^`
// after `\(`.
var atomCases = []pair{
	// Magic on, the default.
	{`.`, "abc"},
	{`a.c`, "xabcy"},
	{`\.`, "xa.by"},
	{`a*`, "xaaay"},
	{`*ab`, "x*aby"},
	{`^*ab`, "*aby"},
	{`\*`, "xa*by"},
	{`[abc]`, "xby"},
	{`\[abc]`, "x[abc]y"},
	{`~`, "xbcy"},
	{`~\+`, "xbcbcy"},
	{`\~`, "x~y"},
	{`a^b`, "xa^by"},
	{`^abc`, "abcx"},
	{`^abc`, "xabc"},
	{`\^a`, "x^ay"},
	{`abc$`, "xabc"},
	{`abc$`, "abcx"},
	{`a$b`, "xa$by"},
	{`\$a`, "x$ay"},
	{`\(^a\)`, "abc"},
	{`\(^a\)`, "xabc"},
	{`x\|^a`, "abc"},
	{`\(a$\)`, "xa"},
	{`\(a$\)b`, "xab"},
	{`a$\|b`, "xab"},

	// Multis.
	{`a\+`, "xaaay"},
	{`a\+`, "xy"},
	{`a\?`, "xay"},
	{`a\=`, "xay"},
	{`ab\{2}`, "xabby"},
	{`ab\{2,}`, "xabbbby"},
	{`ab\{,2}`, "xabby"},
	{`ab\{}`, "xabbby"},
	{`ab\{2,3\}`, "xabbbby"},
	{`a\{-}b`, "aaab"},
	{`a\{-1,}b`, "aaab"},
	{`a\{-2}`, "aaa"},
	{`a\{-,2}`, "aaa"},
	{`a\{-2,3}`, "aaaa"},
	{`\(ab\)\{2}`, "xababy"},
	{`\(ab\)\+`, "xababy"},

	// Groups and alternation.
	{`\(a\)\(b\)`, "xaby"},
	{`\%(ab\)\+`, "xababy"},
	{`\(a\|b\)c`, "xbcy"},
	{`\(\(a\|b\)c\)\+`, "xacbcy"},
	{`a\|b`, "xby"},
	{`foo\|foobar`, "xfoobary"},
	{`foobar\|foo`, "xfoobary"},
	{`a\|b\|`, "xby"},
	{`\(\)`, "ab"},
	{`\(a\|ab\)\(c\|bcd\)`, "abcd"},

	// Word boundaries.
	{`\<foo\>`, "a foo b"},
	{`\<foo`, "a foobar b"},
	{`foo\>`, "a barfoo b"},
	{`\<\w\+\>`, " word "},
	{`\<\I\i*`, " ident9 "},
	{`\<\h\w*`, " ident9 "},

	// Escapes and numeric character forms.
	{`\t`, "a\tb"},
	{`\e`, "a\x1bb"},
	{`\r`, "a\rb"},
	{`\n`, "a\nb"},
	{`a\nb`, "a\nb"},
	{`\%d65`, "xAy"},
	{`\%x41`, "xAy"},
	{`\%o101`, "xAy"},
	{`\%o40`, "x y"},
	{`\%o377`, "xÿy"},
	{`\%o570`, "x/0y"},
	{`\%u20ac`, "x€y"},
	{`\%U0001f600`, "x😀y"},
	{`\%d65\+`, "xAAy"},

	// File anchors.
	{`\%^ab`, "abc"},
	{`\%^b`, "abc"},
	{`a\%$`, "xa"},
	{`a\%$`, "xay"},
	{`\%^abc\%$`, "abc"},
	{`\%#=1foo`, "xfooy"},
	{`\%#=2foo`, "xfooy"},

	// Case forcing. \c wins over \C wherever either of them stands.
	{`\cFOO`, "xfooy"},
	{`\Cfoo`, "xFOOy"},
	{`\Cfoo`, "xfooy"},
	{`\cFOO\C`, "xfooy"},
	{`\Cfoo\c`, "xFOOy"},
	{`foo\c`, "xFOOy"},
	{`\cé`, "xÉy"},

	// Very magic.
	{`\v(a|b)+`, "xaby"},
	{`\vab{2}`, "xabby"},
	{`\v<foo>`, "a foo b"},
	{`\v%(ab)+`, "xababy"},
	{`\v[a-c]+`, "xabcy"},
	{`\v\.`, "xa.by"},
	{`\v\(`, "xa(by"},
	{`\v\<`, "xa<by"},
	{`\v\$`, "xa$y"},
	{`\v\^`, "x^y"},
	{`\va=`, "xay"},
	{`\vab?c`, "xacy"},
	{`\vx{-1,2}`, "xxxy"},
	{`\v%^ab`, "abc"},
	{`\v%d65`, "xAy"},
	{`\va^b`, "xa^by"},
	{`\va$b`, "xa$by"},
	{`\v*ab`, "x*aby"},
	{`\v~`, "xbcy"},
	{`\v\{`, "x{y"},
	{`\v_`, "x_y"},
	{`\v!#,:;'`, "x!#,:;'y"},

	// Nomagic.
	{`\M.`, "xa.by"},
	{`\M\.`, "xa.by"},
	{`\M[abc]`, "x[abc]y"},
	{`\M\[abc]`, "xby"},
	{`\Mfoo\{2}`, "xfoooy"},
	{`\M^abc`, "abcx"},
	{`\M\^abc`, "x^abcy"},
	{`\Mabc$`, "xabc"},
	{`\Ma$b`, "xa$by"},
	{`\Ma\|b`, "xby"},
	{`\M*`, "xa*by"},
	{`\Ma\*`, "xaaay"},
	{`\Ma*b`, "xa*by"},
	{`\M\~`, "xbcy"},
	{`\M~`, "x~y"},

	// Very nomagic.
	{`\V[abc]`, "x[abc]y"},
	{`\V\[abc]`, "xby"},
	{`\V.`, "xa.by"},
	{`\V\.`, "xaby"},
	{`\V\^abc`, "abc"},
	{`\V^abc`, "x^abcy"},
	{`\V\$`, "xa"},
	{`\V$`, "xa$"},
	{`\Va\|b`, "xa|by"},
	{`\V*`, "xa*by"},
	{`\Va\*`, "xaaay"},
	{`\Va.c`, "xa.cy"},

	// The level switching mid-pattern, which is the reason the level is state
	// and not an argument.
	{`foo\Vbar.baz`, "xfoobar.bazy"},
	{`\vab\Mcd.ef`, "xabcd.efy"},
	{`abc\v(d|e)`, "xabcey"},
	{`\Vfoo\vbar+`, "xfoobarry"},
	{`\Ma.b\mc.d`, "xa.bcXdy"},

	// Collections.
	{`[abc]\+`, "x abc y"},
	{`[^abc]\+`, "abcxyzabc"},
	{`[a-z]\+`, "AB cd EF"},
	{`[A-Za-z0-9_]\+`, " a_9 "},
	{`[]abc]`, "x]y"},
	{`[^]abc]`, "]x"},
	{`[a-]`, "x-y"},
	{`[-a]`, "x-y"},
	{`[a\]b]`, "x]y"},
	{`[xyz\\]`, `x\y`},
	{`[\d65]`, "xAy"},
	{`[\x41]`, "xAy"},
	{`[\o101]`, "xAy"},
	{`[€]`, "x€y"},
	{`[\U0001f600]`, "x😀y"},
	{`[\t]`, "a\tb"},
	{`[\e]`, "a\x1bb"},
	{`[\xyz]`, `a\b`},
	{`[a-c-e]`, "x-y"},
	{`[abc`, "x[abcy"},
	{`a[`, "xa[y"},
	{`[€中]`, "x中y"},
	{`[^\d65]`, "AB"},

	// POSIX classes inside collections.
	{`[[:alpha:]]\+`, " abc123"},
	{`[[:digit:]]\+`, " 123abc"},
	{`[[:alnum:]]\+`, " a1 "},
	{`[[:digit:][:upper:]]\+`, " A1b"},
	{`[[:lower:]]`, "ABéC"},
	{`[[:upper:]]`, "abÉc"},
	{`[[:print:]]`, "é"},
	{`[[:space:]]`, "ab c"},
	{`[[:blank:]]`, "ab\tc"},
	{`[[:punct:]]`, "ab,c"},
	{`[[:xdigit:]]\+`, " zf9 "},
	{`[[:graph:]]`, " a"},
	{`[[:cntrl:]]`, "a\x01b"},
	{`[[:tab:]]`, "a\tb"},
	{`[[:return:]]`, "a\rb"},
	{`[[:escape:]]`, "a\x1bb"},
	{`[[:backspace:]]`, "a\bb"},
	{`[[:ident:]]`, " é"},
	{`[[:keyword:]]`, " é"},
	{`[[:fname:]]`, " é"},
	{`[-./[:alnum:]_~]\+`, " a-b/c.d_e~f "},
	{`[[.a.]]`, "xay"},

	// The \_ forms. Every row with a line break in it is one where vim's
	// match() over a string and pvim's match over a buffer give the same
	// answer; the ones where they cannot, `.` and the negated classes and the
	// anchors, are tested against a buffer by hand instead. The header says so.
	{`\_s`, "a b"},
	{`\_s`, "ab\ncd"},
	{`\_S`, " a "},
	{`\_.`, "ab"},
	{`\_.\+`, "a\nb"},
	{`\_[ab]\+`, "xaby"},
	{`\_[ab]\+`, "ab\nba"},
	{`[a\n]\+`, "a\nb"},
	{`\_d\+`, "12"},
	{`\M\_.`, "ab"},
	{`\M\_[ab]`, "xay"},
	{`\M\_\[ab]`, "xay"},
	{`\V\_[ab]`, "xay"},
	{`\v\_s`, "a b"},
	{`\v\_.`, "ab"},

	// Things that look like operators and are not.
	{`a\/b`, "xa/by"},
	{`a\-b`, "xa-by"},
	{`\Nfoo`, "xNfooy"},
	{`5\+`, "x55y"},
}

// refusedCases are the atoms pvim will not translate. vim's answer is recorded
// anyway, because the day one of these grows an implementation the row it has
// to satisfy is already here.
var refusedCases = []pair{
	{`\zsfoo`, "xfooy"},
	{`foo\zsbar`, "xfoobary"},
	{`foo\zebar`, "xfoobary"},
	{`\v\zsfoo`, "xfooy"},
	{`\(foo\)\@=`, "foobar"},
	{`\(foo\)\@!`, "barfoo"},
	{`\(foo\)\@<=bar`, "foobar"},
	{`\(foo\)\@<!bar`, "bazbar"},
	{`\(foo\)\@>`, "foobar"},
	{`\v(foo)@=`, "foobar"},
	{`\v(foo)@<=bar`, "foobar"},
	{`\(a\)\1`, "aa"},
	{`\(a\)\(b\)\2\1`, "abba"},
	{`\%V`, "abc"},
	{`\%[ab]`, "xab"},
	{`\v%[ab]`, "xab"},
	{`\%23l`, "abc"},
	{`\%23c`, "abc"},
	{`\%23v`, "abc"},
	{`\%<3l`, "abc"},
	{`\%>3c`, "abc"},
	{`\%.lfoo`, "xfooy"},
	{`\%#`, "abc"},
	{`\%'a`, "abc"},
	{`a\&b`, "ab"},
	{`\va&&b`, "ab"},
	{`\Zfoo`, "xfooy"},
	{`\z1`, "aa"},
	{`\%C`, "abc"},
	{`[[=a=]]`, "xay"},
}

// vimrcCases are every regex in ~/.vimrc, by hand, with the inputs that show
// what each one does.
//
// Two of them do not do what the vimrc thinks. In 'dir' and 'file' of
// g:ctrlp_custom_ignore some of the alternations are written `|` and not `\|`,
// and under 'magic' a bare `|` is a literal pipe, so `public$|log` is one
// alternative matching the text "public|log" at the end of a line rather than
// two alternatives. In NERDTreeIgnore, `\ntuser.ini` starts with `\n`, which is
// a line break and not a backslash and an n, so that entry can never match a
// file name. Both are recorded here as vim behaves rather than as the vimrc
// intends, because the oracle is vim.
var vimrcCases = []pair{
	// The BufWritePre strip-trailing-whitespace autocmd, which is the pattern
	// this editor will run more often than any other.
	{`\s\+$`, "foo   "},
	{`\s\+$`, "foo\t "},
	{`\s\+$`, "foo"},
	{`\s\+$`, "   "},
	{`\s\+$`, "  foo  bar"},

	// g:ctrlp_custom_ignore 'dir'.
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/.git"},
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/.yardoc/b"},
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/public"},
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/public|log"},
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/tmp|.hg"},
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/.svn"},
	{`\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`, "a/src"},

	// g:ctrlp_custom_ignore 'file'.
	{`\.so$\|\.dat$|\.DS_Store|\.pyc$`, "libfoo.so"},
	{`\.so$\|\.dat$|\.DS_Store|\.pyc$`, "x.dat|.DS_Store|.pyc"},
	{`\.so$\|\.dat$|\.DS_Store|\.pyc$`, "x.pyc"},
	{`\.so$\|\.dat$|\.DS_Store|\.pyc$`, "main.go"},

	// NERDTreeIgnore.
	{`\~$`, "foo~"},
	{`\~$`, "foo"},
	{`\.pyc$`, "foo.pyc"},
	{`\.pyc$`, "foo.pycx"},
	{`\*NTUSER*`, "x*NTUSERRR"},
	{`\*NTUSER*`, "x*NTUSE"},
	{`\*ntuser*`, "x*ntuse"},
	{`\NTUSER.DAT`, "xNTUSERxDAT"},
	{`\ntuser.ini`, "\ntuserxini"},
	{`\ntuser.ini`, "ntuser.ini"},
}
