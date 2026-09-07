// Package keymap is the map table and the state machine that resolves it: a
// key-sequence trie per mode, vim's longest-match rules over it, and an
// explicit "need more input" return for the ambiguity 'timeout' decides.
//
// It imports internal/key and nothing else. A mapping is keys in and keys out,
// so this package knows what a keystroke is and knows nothing about buffers,
// windows, modes-as-behaviour or the editor: the frontend hands it the mode the
// next key will be executed in and gets back the keys to execute.
//
// # The clock is not in here
//
// Nothing in this package sleeps and nothing reads a timer. An ambiguous
// prefix comes back as "no key yet", exactly as internal/key's Decoder returns
// StatusNeedMore for an unfinished escape sequence, and the frontend that owns
// the event loop decides how long to wait before calling Timeout. That is the
// only way the whole thing is testable without a clock, and it is the only way
// the two timeouts stay apart.
//
// They are two different timeouts and conflating them is the classic bug. The
// vimrc this editor exists to run sets, inside its !has('gui_running') branch:
//
//	set notimeout
//	set ttimeout
//	set ttimeoutlen=10
//
// 'notimeout' is about MAPPINGS: an ambiguous mapping prefix waits forever, so
// "," never fires until the second comma arrives however long that takes.
// 'ttimeout' is about KEY CODES: a lone Escape is told from the start of
// <Esc>[A after 10ms, which is internal/key's Decoder and the terminal's
// business, not this package's. A frontend that ran the mapping wait on
// ttimeoutlen would fire "," after 10ms; one that ran the escape wait on
// 'notimeout' would hang on every Escape. Machine.Pending answers the first
// question and key.Decoder answers the second.
//
// # What was measured
//
// Every resolution rule below came out of /opt/homebrew/bin/vim 9.2, run as
// `vim --clean -i NONE --not-a-term -s KEYS FILE` with the mapping's
// right-hand side writing a file so that "did it fire" survives vim exiting on
// end of input. The measurements:
//
// - Ambiguity resolves on the next key, not on the buffer. With `ab` and
// `abc` both mapped and 'notimeout' set, "ab:wq<CR>" runs the ab mapping
// and then the :wq; "abx" runs ab and then x; "abc" runs abc. The colon
// and the x are what say ab is complete, and they are executed after it.
//
// - Under 'notimeout' an ambiguous prefix at the end of the input waits, and
// vim waits for a person: "ab" with nothing after it never returns. So does
// 'timeout' vim once the script is exhausted, because a -s script running
// out is not a timeout, which is why the timeout half of this cannot be
// measured headlessly and is implemented from :help 'timeout' instead.
//
// - The count in front of a mapped key belongs to the key the mapping
// produced. With `map { gT` over four tab pages sitting on the fourth,
// "{" lands on tab 3 and "2{" on tab 2. Nothing in here does that: the
// count is typed, is not a mapping, and reaches the mode machine on its
// own, which then sees the g and the T behind it.
//
// - A bare :map is normal, visual and operator-pending, and the
// operator-pending half is not decoration. With `map } gt`, "d}" deletes
// nothing at all: the } becomes gt, gt is not a motion, and the operator
// is thrown away. Measured on "alpha beta gamma", which comes back
// unchanged.
//
// - A remappable right-hand side is fed back through the table. `nmap ab cd`
// with `nnoremap cd ...` runs the cd mapping. `nnoremap ab cd` does not.
//
// - When the right-hand side starts with the whole left-hand side, exactly
// the FIRST KEY of it is protected from remapping, and the rest is not.
// This is the measurement that decides the shape of expand(). With `nmap l
// <something>` and `nmap <Space>l <Space>l`, typing "<Space>l" moves the
// cursor one column, which is the protected Space, and then runs the l
// mapping, which is the unprotected l. A whole-left-hand-side protection
// would have moved the cursor twice and run nothing. With `nmap <F2>l
// <F2>x` the leading keys are the same but the whole left-hand side is not
// a prefix of the right, so nothing is protected and the <F2> mapping
// fires: the test is the whole sequence, and the effect is one key.
//
// - Recursion stops at 'maxmapdepth', which is 1000, with "E223: Recursive
// mapping". Measured on `nmap a b` plus `nmap b a`.
//
// - <nowait> only does anything on a buffer-local mapping. Global `<nowait>
// ab` beside global `abc`, typing "abc", runs abc; buffer-local `<nowait>
// ab` beside global `abc`, typing "abc", runs ab and then leaves the c.
// Without the <nowait> the buffer-local mapping loses to the longer global
// one, which is the whole reason the argument exists.
//
// - <unique> is an exact-left-hand-side test. `nnoremap <unique> ab` fails
// with "E227: Mapping already exists for ab" when ab is mapped and passes
// when only abc is.
//
// - :unmap of a mapping that is not there is "E31: No such mapping", and
// :nunmap of a mapping made with :map takes the normal-mode half and
// leaves the visual and operator-pending halves behind.
//
// - A mapping is not applied to a key a command is waiting for. With `map {
// gT`, "f{" finds the brace and "r{" writes one, exactly as they do with no
// mapping in sight. That is Machine.Next's NoMap, and the frontend sets it
// from whether the mode machine is holding the next key.
//
// # What is not built
//
// <expr> is refused with an E-code at the moment the mapping is made. It needs
// an expression evaluator and there is not going to be one, and a mapping that
// silently did nothing would be worse than one that says why.
//
// <Plug> and <SID> parse and are stored, and nothing resolves them. A <Plug>
// left-hand side sits in the trie where no terminal can reach it, which is
// correct and free: the day a mapping's right-hand side names it, the lookup
// finds it. A <SID> right-hand side has no script context to resolve against
// and refuses with an E-code when the keys are pressed rather than feeding
// five literal characters into the editor.
//
// <silent> is stored and inert. It suppresses the echo of the command a
// mapping runs, and the message line belongs to the layer above this one.
package keymap
