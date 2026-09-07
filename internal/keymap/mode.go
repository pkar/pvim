package keymap

import "fmt"

// Mode is the set of modes a mapping is in, one bit per mode letter.
//
// A bitmask and not an enum because one :map command makes one mapping in
// several modes at once, and because the lookup asks about exactly one of them:
// the frontend knows which mode the next key will be executed in, and the trie
// it looks in is that mode's.
//
// Select mode has a bit even though pvim has no select mode, because :vmap
// means visual AND select and :xmap means visual alone, and a table that
// collapsed the two would make :xmap and :vmap the same command. Lang mode has
// one for the same reason :lmap is a command, nothing in this vimrc uses it,
// and a letter with no bit would parse as an error.
type Mode uint8

// The mode bits, one per letter in :help map-modes.
const (
	// Normal is "n".
	Normal Mode = 1 << iota
	// Visual is "x": visual mode and not select mode.
	Visual
	// Select is "s".
	Select
	// OpPending is "o", the mode between an operator and its motion.
	OpPending
	// Insert is "i", which is insert mode AND replace mode: vim's map-overview
	// table names both against the i column, so an :imap fires under R.
	Insert
	// Cmdline is "c", the ":" and "/" prompts.
	Cmdline
	// Lang is "l", the language mappings :lmap makes. Stored and never looked
	// up, because nothing in pvim reads a key through a language mapping.
	Lang
)

// modeCount is how many bits Mode has, and therefore how many tries a table
// holds. It is a constant rather than a count of the list above because the
// tries are an array and an array wants a constant.
const modeCount = 7

// The combinations the map commands are spelled with.
const (
	// NVO is a bare :map and :noremap.
	NVO = Normal | Visual | Select | OpPending
	// VS is :vmap, which is visual and select together.
	VS = Visual | Select
	// IC is :map!, which is insert and command line.
	IC = Insert | Cmdline
)

// ParseModes turns the mode letters internal/vimrc puts on a Map into a
// bitmask.
//
// The letters are vim's: n, v, x, s, o, i, c and l, where v is the pair
// visual-and-select. An empty string is a bare :map, which is nvo, because that
// is what vim means by the absence of a letter and because a caller with no
// letters to give is describing a :map.
func ParseModes(s string) (Mode, error) {
	if s == "" {
		return NVO, nil
	}
	var m Mode
	for _, c := range s {
		switch c {
		case 'n':
			m |= Normal
		case 'v':
			m |= VS
		case 'x':
			m |= Visual
		case 's':
			m |= Select
		case 'o':
			m |= OpPending
		case 'i':
			m |= Insert
		case 'c':
			m |= Cmdline
		case 'l':
			m |= Lang
		case '!':
			m |= IC
		default:
			return 0, fmt.Errorf("keymap: %q is not a mode letter", string(c))
		}
	}
	return m, nil
}

// String prints the letters back, in the order :map listings use.
func (m Mode) String() string {
	if m == NVO {
		return "nvo"
	}
	var out []byte
	for i, c := range "nxsoicl" {
		if m&(1<<uint(i)) != 0 {
			out = append(out, byte(c))
		}
	}
	return string(out)
}

// index is the trie slot for a single mode bit, or -1 for a Mode that is not
// exactly one bit. Lookup takes one mode and a caller that hands it two has a
// bug that must not silently resolve to the first.
func (m Mode) index() int {
	for i := 0; i < modeCount; i++ {
		if m == 1<<uint(i) {
			return i
		}
	}
	return -1
}

// each calls f with every bit set in m, so that one Set writes one mapping into
// as many tries as the command named.
func (m Mode) each(f func(i int)) {
	for i := 0; i < modeCount; i++ {
		if m&(1<<uint(i)) != 0 {
			f(i)
		}
	}
}
