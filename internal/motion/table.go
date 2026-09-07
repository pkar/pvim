package motion

// The motion table.
//
// Every Kind below is the tag vim 9.2's own motion.txt puts on that command --
// |exclusive|, |inclusive| or |linewise| -- read out of
// runtime/doc/motion.txt on this machine and not remembered. Where the doc and
// this table disagree the doc is right and this table is a bug.
//
// Every Jump was measured, not read. The list under :help jumplist is short of
// the truth: it does not mention gg, go, ][, [], [(, [{, ]) or ]}, and all
// eight set the ' mark on this vim, checked by running "20G<motion>''" through
// vim --clean and asking where the cursor ended up. The ones the doc does list
// were measured too and all agreed.
//
// Four motions are missing on purpose and the mode machine owns them, because
// each needs something no Func signature can carry: / and ? open a command
// line, and * and # build a pattern out of the word under the cursor. All four
// are exclusive charwise jumps, and n and N below are the same motion once the
// pattern exists.
//
// The scrolling commands are not here either. CTRL-F, CTRL-B, CTRL-D, CTRL-U,
// CTRL-E, CTRL-Y, zt, zz and zb move the cursor but are not motions an
// operator may take: d CTRL-F is not a delete, it is a beep.

// table is every motion, keyed by the notation the mode machine looks up.
var table = map[string]Motion{}

// add puts a motion in the table. It panics on a duplicate, because two
// entries for one key is a merge that went wrong and finding out at the first
// keystroke instead of at init is a bad trade.
func add(m Motion) {
	if _, dup := table[m.Keys]; dup {
		panic("motion: two motions bound to " + m.Keys)
	}
	table[m.Keys] = m
}

func init() {
	// Left-right motions. motion.txt: h, <BS>, l, <Space>, 0 and <Home> and ^
	// are all |exclusive|; $, <End> and g_ are |inclusive|.
	for _, m := range []Motion{
		{Keys: "h", Kind: KindCharExclusive, Do: motionLeft('h')},
		{Keys: "<BS>", Kind: KindCharExclusive, Do: motionLeft('b')},
		{Keys: "<Left>", Kind: KindCharExclusive, Do: motionLeft('<')},
		{Keys: "l", Kind: KindCharExclusive, Do: motionRight('l')},
		{Keys: "<Space>", Kind: KindCharExclusive, Do: motionRight('s')},
		{Keys: "<Right>", Kind: KindCharExclusive, Do: motionRight('>')},
		{Keys: "0", Kind: KindCharExclusive, Do: motionLineStart},
		{Keys: "<Home>", Kind: KindCharExclusive, Do: motionLineStart},
		{Keys: "^", Kind: KindCharExclusive, Do: motionFirstNonBlank},
		{Keys: "$", Kind: KindCharInclusive, Do: motionEndOfLine},
		{Keys: "<End>", Kind: KindCharInclusive, Do: motionEndOfLine},
		{Keys: "g_", Kind: KindCharInclusive, Do: motionLastNonBlank},
		{Keys: "g0", Kind: KindCharExclusive, Do: motionDisplayLineStart},
		{Keys: "g^", Kind: KindCharExclusive, Do: motionDisplayFirstNonBlank},
		{Keys: "g$", Kind: KindCharInclusive, Do: motionDisplayEndOfLine},
		{Keys: "gm", Kind: KindCharExclusive, Do: motionScreenMiddle},
		{Keys: "gM", Kind: KindCharExclusive, Do: motionLineMiddle},
		{Keys: "|", Kind: KindCharExclusive, Do: motionColumn},
		// f and t include the character they land on; F and T do not. This is
		// the pair most often got wrong, and dfx against dFx is the one-line
		// oracle case that says so.
		{Keys: "f", Kind: KindCharInclusive, NeedsArg: true, Do: motionFind('f')},
		{Keys: "F", Kind: KindCharExclusive, NeedsArg: true, Do: motionFind('F')},
		{Keys: "t", Kind: KindCharInclusive, NeedsArg: true, Do: motionFind('t')},
		{Keys: "T", Kind: KindCharExclusive, NeedsArg: true, Do: motionFind('T')},
		// ; and, take the kind of the find they repeat, so the Kind here is a
		// placeholder and the Result is the answer. , reverses the direction
		// and keeps the inclusiveness of the original: ,-ing back over an f is
		// still inclusive.
		{Keys: ";", Kind: KindCharInclusive, Do: motionFind(';')},
		{Keys: ",", Kind: KindCharInclusive, Do: motionFind(',')},
	} {
		add(m)
	}

	// Up-down motions. All |linewise| except gj and gk, which move by display
	// line and are |exclusive|.
	for _, m := range []Motion{
		{Keys: "j", Kind: KindLine, Do: motionDown},
		{Keys: "<Down>", Kind: KindLine, Do: motionDown},
		{Keys: "<C-N>", Kind: KindLine, Do: motionDown},
		{Keys: "<NL>", Kind: KindLine, Do: motionDown},
		{Keys: "k", Kind: KindLine, Do: motionUp},
		{Keys: "<Up>", Kind: KindLine, Do: motionUp},
		{Keys: "<C-P>", Kind: KindLine, Do: motionUp},
		{Keys: "gj", Kind: KindCharExclusive, Do: motionScreenDown},
		{Keys: "gk", Kind: KindCharExclusive, Do: motionScreenUp},
		{Keys: "+", Kind: KindLine, Do: motionDownFirstNonBlank},
		{Keys: "<CR>", Kind: KindLine, Do: motionDownFirstNonBlank},
		{Keys: "-", Kind: KindLine, Do: motionUpFirstNonBlank},
		{Keys: "_", Kind: KindLine, Do: motionLineFirstNonBlank},
		// G and gg are jumps; the plain vertical family is not.
		{Keys: "G", Kind: KindLine, Jump: true, Do: motionGoto(true)},
		{Keys: "gg", Kind: KindLine, Jump: true, Do: motionGoto(false)},
		{Keys: "go", Kind: KindCharExclusive, Jump: true, Do: motionByte},
	} {
		add(m)
	}

	// Word motions. w and b are |exclusive|; e and ge are |inclusive|.
	for _, m := range []Motion{
		{Keys: "w", Kind: KindCharExclusive, Do: motionWord(false)},
		{Keys: "W", Kind: KindCharExclusive, Do: motionWord(true)},
		{Keys: "b", Kind: KindCharExclusive, Do: motionBackWord(false)},
		{Keys: "B", Kind: KindCharExclusive, Do: motionBackWord(true)},
		{Keys: "e", Kind: KindCharInclusive, Do: motionEndWord(false)},
		{Keys: "E", Kind: KindCharInclusive, Do: motionEndWord(true)},
		{Keys: "ge", Kind: KindCharInclusive, Do: motionBackEndWord(false)},
		{Keys: "gE", Kind: KindCharInclusive, Do: motionBackEndWord(true)},
	} {
		add(m)
	}

	// Object motions: sentences, paragraphs and sections. All |exclusive| and
	// all jumps.
	for _, m := range []Motion{
		{Keys: "(", Kind: KindCharExclusive, Jump: true, Do: motionSentence(-1)},
		{Keys: ")", Kind: KindCharExclusive, Jump: true, Do: motionSentence(1)},
		{Keys: "{", Kind: KindCharExclusive, Jump: true, Do: motionPara(-1)},
		{Keys: "}", Kind: KindCharExclusive, Jump: true, Do: motionPara(1)},
		{Keys: "]]", Kind: KindCharExclusive, Jump: true, Do: motionSection(1, '{')},
		{Keys: "][", Kind: KindCharExclusive, Jump: true, Do: motionSection(1, '}')},
		{Keys: "[[", Kind: KindCharExclusive, Jump: true, Do: motionSection(-1, '{')},
		{Keys: "[]", Kind: KindCharExclusive, Jump: true, Do: motionSection(-1, '}')},
	} {
		add(m)
	}

	// Marks and matches. `x is |exclusive| and 'x is |linewise|; both are
	// jumps, and so is a bare %.
	for _, m := range []Motion{
		{Keys: "`", Kind: KindCharExclusive, Jump: true, NeedsArg: true, Do: motionMark(false)},
		{Keys: "'", Kind: KindLine, Jump: true, NeedsArg: true, Do: motionMark(true)},
		// A bare % is the matchpair jump and is |inclusive|; {count}% is a
		// percentage of the file and is |linewise|. One entry cannot say both,
		// so the Result decides and this Kind is the bare case.
		{Keys: "%", Kind: KindCharInclusive, Jump: true, Do: motionMatch},
		{Keys: "[(", Kind: KindCharExclusive, Jump: true, Do: motionUnmatched(-1, '(', ')')},
		{Keys: "[{", Kind: KindCharExclusive, Jump: true, Do: motionUnmatched(-1, '{', '}')},
		{Keys: "])", Kind: KindCharExclusive, Jump: true, Do: motionUnmatched(1, '(', ')')},
		{Keys: "]}", Kind: KindCharExclusive, Jump: true, Do: motionUnmatched(1, '{', '}')},
	} {
		add(m)
	}

	// Window-relative motions, all |linewise| jumps, and the only motions that
	// need to know what is on the screen.
	for _, m := range []Motion{
		{Keys: "H", Kind: KindLine, Jump: true, Do: motionWindow('H')},
		{Keys: "M", Kind: KindLine, Jump: true, Do: motionWindow('M')},
		{Keys: "L", Kind: KindLine, Jump: true, Do: motionWindow('L')},
	} {
		add(m)
	}

	// Search repeats. The pattern comes from internal/search; these two are
	// the motion around it, |exclusive| and jumps, and an offset of /e makes
	// the Result inclusive.
	for _, m := range []Motion{
		{Keys: "n", Kind: KindCharExclusive, Jump: true, Do: notImplemented},
		{Keys: "N", Kind: KindCharExclusive, Jump: true, Do: notImplemented},
	} {
		add(m)
	}
}

// ByKeys returns the motion bound to a key sequence, written in vim's
// notation, which is what key.Format prints. This is the seam that keeps this
// package free of internal/key: the mode machine formats the keys it has
// collected and asks here.
func ByKeys(keys string) (Motion, bool) {
	m, ok := table[keys]
	return m, ok
}

// IsPrefix reports whether keys is the start of a longer motion and not a
// motion itself, which is how the mode machine knows to wait after g rather
// than beep. "g" is a prefix, "ge" is a motion, and "gz" is neither.
func IsPrefix(keys string) bool {
	for k := range table {
		if len(k) > len(keys) && k[:len(keys)] == keys {
			return true
		}
	}
	return false
}

// All returns every motion in the table, for a test that wants to walk it.
// The order is not defined; a caller that needs one sorts by Keys.
func All() []Motion {
	out := make([]Motion, 0, len(table))
	for _, m := range table {
		out = append(out, m)
	}
	return out
}
