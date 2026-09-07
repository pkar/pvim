package search

// The five errors a search can end in, each carrying vim's own message text.
//
// The text matters as much as the code. cmd/oracle diffs vim's message line
// against pvim's, so a sentence that reads right and is not vim's is a failed
// case, and every string below was copied off a run of /opt/homebrew/bin/vim
// rather than out of the help. Error() is the whole message, ready for the
// message line, because the alternative is every caller reassembling "E486: "
// and one of them getting the colon wrong.
type (
	// NoPatternError is E35: n, N or a bare / with nothing searched for yet.
	NoPatternError struct{}

	// NoStringError is E348: * or # with no word and no non-blank run at or
	// after the cursor on its line.
	NoStringError struct{}

	// NotFoundError is E486, which is what a failed search says when
	// 'wrapscan' is on: the whole buffer was looked at and the pattern is not
	// in it.
	NotFoundError struct{ Pattern string }

	// HitBottomError is E385: a forward search under 'nowrapscan' that reached
	// the end of the buffer. It is not the same as E486 and vim never prints
	// both; with 'nowrapscan' the pattern may well be in the file, above the
	// cursor, and the message says where the search stopped rather than that
	// the pattern is absent.
	HitBottomError struct{ Pattern string }

	// HitTopError is E384, the backward half of HitBottomError.
	HitTopError struct{ Pattern string }
)

func (NoPatternError) Error() string { return "E35: No previous regular expression" }
func (NoStringError) Error() string  { return "E348: No string under cursor" }

func (e NotFoundError) Error() string {
	return "E486: Pattern not found: " + e.Pattern
}

func (e HitBottomError) Error() string {
	return "E385: Search hit BOTTOM without match for: " + e.Pattern
}

func (e HitTopError) Error() string {
	return "E384: Search hit TOP without match for: " + e.Pattern
}

// The two messages a search that wrapped prints before it reports where it
// landed. Vim prints one of these whenever the scan ran off an end and started
// again at the other, including on the run that then fails with E486, which is
// why they are values a caller shows and not part of an error.
const (
	WrappedToTopMessage    = "search hit BOTTOM, continuing at TOP"
	WrappedToBottomMessage = "search hit TOP, continuing at BOTTOM"
)

// WrapMessage is the message a search in this direction prints when it wrapped.
func WrapMessage(dir Direction) string {
	if dir == Backward {
		return WrappedToBottomMessage
	}
	return WrappedToTopMessage
}

// notFound picks between E486, E385 and E384 for a search that found nothing.
//
// The choice is 'wrapscan' and nothing else: with it on the search covered the
// whole buffer and the pattern is genuinely absent, with it off the search only
// covered one end of it and vim says so.
func notFound(pattern string, dir Direction, opt Options) error {
	switch {
	case opt.WrapScan:
		return NotFoundError{Pattern: pattern}
	case dir == Backward:
		return HitTopError{Pattern: pattern}
	default:
		return HitBottomError{Pattern: pattern}
	}
}
