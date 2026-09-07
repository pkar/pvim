// Package regex translates vim's regular expression dialect into Go's and
// compiles the result. It is the only place in pvim that imports regexp.
//
// There is no matching engine here. Vim's dialect and RE2 differ in spelling
// far more than in meaning: `\+` is `+`, `\{n,m}` is `{n,m}`, `\{-}` is `*?`,
// `\(` is `(`, `\%(` is `(?:`, and `\v` flips which of every pair of forms is
// the operator. That is a tokenizer and a table, which is what this package is,
// and everything it accepts then runs in RE2's linear time. The alternative was
// a hand-written backtracking engine, and the first time 'hlsearch' is on with
// `\(a*\)*b` half typed over a 40,000-line log, a backtracker hangs the editor
// with the search highlighted and RE2 does not.
//
// # What it will not do
//
// A handful of vim's atoms cannot be expressed in RE2 at all, and each is
// refused by name with its own error type, the atom, and the byte column it
// stood at: `\zs`, `\ze`, the five `\@` lookaround forms, `\1` through `\9`,
// `\%V`, `\%[`, and the position atoms `\%23l`, `\%23c`, `\%23v`, `\%#` and
// `\%'m`. `\&`, `\Z`, `\%C`, the `\z` syntax-extension family and `[=a=]` go
// the same way. See Refused. A refusal is never a silent mistranslation, which is the
// whole point of it being a type per atom rather than a comment in a file.
//
// # Known differences from vim 9.2
//
// Each of these was checked against /opt/homebrew/bin/vim rather than reasoned
// about, and each is here because it is a difference somebody will eventually
// hit rather than a rounding error.
//
// Word boundaries. `\<` and `\>` both become Go's `\b`, and `\b` is one
// symmetric boundary where vim has two directional ones, and it is ASCII where
// vim's are 'iskeyword'-aware. In the shape everyone writes, `\<word\>` and
// `\<\w\+\>`, the two agree exactly, because a boundary in front of a word
// character is a word start and there is nothing else it could be. They part
// company twice. Direction: vim's `\>` over "AĀĀ0" matches at byte 6, the end
// of the word, and pvim's `\b` matches at byte 0, its start. Keywords: vim's
// 'iskeyword' takes é and 中 as word characters, so `\<é` finds the é in " été"
// at byte 1, and pvim finds nothing at all because Go's `\b` only knows
// [0-9A-Za-z_].
//
// Alternation order. Go's regexp is leftmost-first, the same as a backtracking
// search, and so is vim's old engine; vim's default NFA engine is not, quite.
// Over "09)中" the pattern `^[[:digit:]aa-c]\{-0,2}\|.\{-}` matches "0" in vim
// as it ships and the empty string here, because the NFA engine takes a longer
// match where a backtracking search would have stopped at the empty one that
// the first branch offers. Prefixing the same pattern with `\%#=1` to select
// vim's backtracking engine gives the empty string, which is to say vim's two
// engines disagree and pvim follows the one that agrees with Perl and Go. A
// differential fuzz of 500,000 generated patterns found this shape 32 times and
// nothing else, and every one of the 32 had an alternative that could match
// nothing in it.
//
// Keyword and printable characters. `\k`, `\K`, `\p`, `\P` and the
// `[:keyword:]` and `[:print:]` classes are lists of code points read off vim
// rather than sets of Unicode categories, because that is what they are in vim:
// utf_class calls everything a word character that it has no punctuation or
// space entry for, so an emoji and a superscript zero are keyword characters
// and the two ordinal indicators are not, and utf_printable has holes in it
// where the zero-width and bidi format characters are. The tables are in
// classtab.go and TestClassesAgainstVim sweeps them against the binary.
//
// POSIX classes. `[:lower:]` and `[:upper:]` are code point lists for the same
// reason: vim asks whether the character has a case counterpart, not which
// category it is in, so the titlecase letters are both lower and upper, and an
// Ll with no upper case, like kra, is neither. An unknown class name is not an
// error, here or in vim: `[[:bogus:]` is the collection of the eight ordinary
// characters that were typed.
//
// Loops whose body can match nothing. Over "#9", `\%(^#\{-}\)*` matches "#" in
// both of vim's engines and the empty string here: RE2 takes zero iterations of
// a loop whose body is nullable where a backtracking search runs it once and
// lets the body go as far as it can. This one is Go's difference and not vim's,
// both of vim's engines agree with each other about it, and there is nothing to
// be done short of the backtracking engine not to write.
//
// A vim parser bug, not reproduced. In vim 9.2 a `-` standing in range position
// immediately before a `[:` breaks the collection: `[a-c-[:alpha:]]` matches an
// alpha or a dash *followed by a literal ]*, and `[a-c-[:alpha:]]` matches
// nothing at all against a lone "a". This package reads it the obvious way, as
// the collection {a-c, -, alpha}. Write the dash first, `[-a-c[:alpha:]]`, and
// both agree.
//
// The four character-class options. 'isident', 'iskeyword', 'isfname' and
// 'isprint' are frozen at their defaults, which is what `\i`, `\k`, `\f` and
// `\p` are built from. Nothing in the vimrc sets any of them.
//
// `~` with no previous substitute expands to an empty atom instead of raising
// E33, because Options carries the replacement string and not the difference
// between empty and never-set.
//
// Repeat counts. RE2 stops at 1000, so `\{1001}` compiles in vim and comes back
// as a CompileError here.
//
// # Lines and files
//
// Every pattern is compiled with Go's m flag on, so `^` and `$` are line
// boundaries and `\%^` and `\%$`, which become `\A` and `\z`, are file
// boundaries. That means the caller decides what a pattern sees: hand it one
// line and the two pairs are the same thing, hand it a whole buffer and they
// are not, and either way `.` and the negated character classes stop at a line
// break exactly as vim's do.
package regex
