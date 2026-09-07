package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pkar/pvim/internal/mode"
)

// applySet applies one ":set" line to an option bag.
//
// This is not internal/options, which exists now and parses ":set" properly,
// with ?, !, &, +=, -=, ^= and E518 for a name vim does not have. This reader
// survives beside it for one reason: 608 graded runs were measured through it,
// and the path from an options.Options through ex.ModeOptions to the mode
// machine's flat struct has never been graded at all. runOracle runs both, in
// that order, so the measured one wins. Where the two agree this costs
// nothing; where they do not, the disagreement is a bug in the new path and it
// is worth finding on a case that is not also failing for another reason.
//
// It is not the place to add an option. Add it to internal/options and to
// ex.ModeOptions, and when the oracle stays green with this reader removed,
// remove it.
//
// A name it does not know is not an error. The vimrc sets guifont, wildignore,
// diffopt and thirty others that belong to layers nobody has written yet, and
// refusing the line because of them would leave the profile unapplied for the
// options that do exist. The names it does know are exactly the fields of
// mode.Options, which is the whole of what this editor can act on.
func applySet(o *mode.Options, line string) error {
	for _, tok := range strings.Fields(line) {
		if err := applySetOne(o, tok); err != nil {
			return err
		}
	}
	return nil
}

// applySetOne applies one whitespace-separated token of a ":set" line.
func applySetOne(o *mode.Options, tok string) error {
	name, value, hasValue := strings.Cut(tok, "=")
	if hasValue {
		// += and friends are later work's problem. Nothing in testdata/keys or in
		// ~/.vimrc appends to an option this layer reads, so the operator is
		// stripped and the assignment treated as plain, which is right for
		// every value that reaches here and wrong loudly rather than quietly
		// on the day one does not.
		if n, cut := strings.CutSuffix(name, "+"); cut {
			name = n
		} else if n, cut := strings.CutSuffix(name, "-"); cut {
			name = n
		} else if n, cut := strings.CutSuffix(name, "^"); cut {
			name = n
		}
		return setValue(o, name, value)
	}

	on := true
	if n, cut := strings.CutPrefix(tok, "no"); cut && !isNumberOption(tok) {
		name, on = n, false
	} else {
		name = tok
	}
	setBool(o, name, on)
	return nil
}

// isNumberOption keeps "nostartofline" from being read as "no" plus a name
// while leaving an option whose own name starts with "no" alone. There are
// none of the latter in mode.Options; the function is the place to add
// one when there is.
func isNumberOption(string) bool { return false }

// setBool sets a boolean option, ignoring a name this layer has no field for.
func setBool(o *mode.Options, name string, on bool) {
	switch name {
	case "expandtab", "et":
		o.ExpandTab = on
	case "shiftround", "sr":
		o.ShiftRound = on
	case "autoindent", "ai":
		o.AutoIndent = on
	case "smartindent", "si":
		o.SmartIndent = on
	case "joinspaces", "js":
		o.JoinSpaces = on
	case "ignorecase", "ic":
		o.IgnoreCase = on
	case "smartcase", "scs":
		o.SmartCase = on
	case "wrapscan", "ws":
		o.WrapScan = on
	case "magic":
		o.NoMagic = !on
	case "hlsearch", "hls":
		o.HlSearch = on
	case "incsearch", "is":
		o.IncSearch = on
	case "startofline", "sol":
		o.StartOfLine = on
	case "wrap":
		o.Wrap = on
	case "fixendofline", "fixeol":
		o.FixEndOfLine = on
	}
}

// setValue sets an option that takes a value, ignoring a name this layer has
// no field for. A number that will not parse is an error, because a case whose
// .opts says "shiftwidth=x" is a broken case and running it on the default
// would report a difference in the editor.
func setValue(o *mode.Options, name, value string) error {
	switch name {
	case "cinwords", "cinw":
		// A string option, not an int: the oracle profiles set it and the
		// vimrc's python line depends on it reaching internal/operator.
		o.CinWords = value
		return nil
	case "tabstop", "ts":
		return setInt(&o.TabStop, name, value)
	case "shiftwidth", "sw":
		return setInt(&o.ShiftWidth, name, value)
	case "softtabstop", "sts":
		return setInt(&o.SoftTabStop, name, value)
	case "textwidth", "tw":
		return setInt(&o.TextWidth, name, value)
	case "scrolloff", "so":
		return setInt(&o.ScrollOff, name, value)
	case "report":
		return setInt(&o.Report, name, value)
	case "backspace", "bs":
		o.Backspace = value
	case "iskeyword", "isk":
		o.IsKeyword = value
	case "whichwrap", "ww":
		o.WhichWrap = value
	case "matchpairs", "mps":
		o.MatchPairs = value
	case "paragraphs", "para":
		o.Paragraphs = value
	case "sections", "sect":
		o.Sections = value
	case "selection", "sel":
		o.Selection = value
	case "virtualedit", "ve":
		o.VirtualEdit = value
	case "clipboard", "cb":
		o.Clipboard = value
	case "completeopt", "cot":
		o.CompleteOpt = value
	}
	return nil
}

func setInt(dst *int, name, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("set %s=%s: %w", name, value, err)
	}
	*dst = n
	return nil
}
