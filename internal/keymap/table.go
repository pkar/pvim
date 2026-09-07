package keymap

import (
	"errors"
	"strings"

	"github.com/pkar/pvim/internal/key"
)

// Mapping is one :map command, parsed.
//
// The left- and right-hand sides are keys and not text, because the trie is
// keyed on key.Key and because <leader> has to be expanded at the moment the
// command runs and not at the moment it fires. The text forms are kept beside
// them for the two things that quote a mapping back: an error message and a
// :map listing.
type Mapping struct {
	// Modes is every mode the command named. A bare :map is NVO.
	Modes Mode

	// LHS is the keys that trigger the mapping and RHS the keys it produces.
	LHS, RHS []key.Key

	// LHSText and RHSText are the two sides as they were written. RHSText
	// still carries any trailing comment, because :map has no comment syntax:
	// this vimrc's "nnoremap <Space> za \" Spacebar to unfold" maps <Space> to
	// all twenty-four characters and vim runs all twenty-four.
	LHSText, RHSText string

	// NoRemap is the "nore" in the command name: the right-hand side is fed
	// back as keys the table never sees again.
	NoRemap bool

	// Buffer is <buffer> and Buf the buffer it belongs to. A buffer-local
	// mapping beats a global one of the same length and loses to a longer
	// global one, which is what <nowait> exists to change.
	Buffer bool
	Buf    int

	// Silent is <silent>: stored, and inert here. It suppresses the echo of
	// the command the right-hand side runs, which is the message line's
	// business and therefore the layer above this one's.
	Silent bool

	// Nowait is <nowait>: a full match fires without waiting for a longer
	// mapping that could still grow. Measured: it does nothing at all on a
	// global mapping, and vim's own documentation says as much.
	Nowait bool

	// Unique is <unique>, which is a check made once at Set and never read
	// again.
	Unique bool

	// Expr is <expr>, which Set refuses. See ExprError.
	Expr bool

	// Script is <script>, stored and inert: it restricts remapping to
	// mappings the same script defined, and pvim has one script.
	Script bool

	// SID says the right-hand side carries a literal <SID>, which key.Parse
	// leaves as its five characters because :map is what would have resolved
	// it. Nothing resolves it, so expanding this mapping is ScriptIDError.
	SID bool
}

// HasPlug reports whether the left-hand side starts with <Plug>, which is the
// prefix no terminal can send. Such a mapping is reachable only from another
// mapping's right-hand side, which is exactly what the trie already does with
// it, so this is here for a :map listing and for a test to assert the parse.
func (m *Mapping) HasPlug() bool {
	return len(m.LHS) > 0 && m.LHS[0].Special == key.KeyPlug
}

// node is one key in the trie.
type node struct {
	kids map[key.Key]*node
	// m is the mapping that ends here, nil for a node that is only a prefix.
	m *Mapping
}

func (n *node) child(k key.Key) *node {
	if n.kids == nil {
		n.kids = map[key.Key]*node{}
	}
	c := n.kids[k]
	if c == nil {
		c = &node{}
		n.kids[k] = c
	}
	return c
}

// Table is the map table: one trie per mode, plus one set of tries per buffer
// for the <buffer> mappings.
//
// The zero Table is empty and usable.
type Table struct {
	global [modeCount]node
	local  map[int]*[modeCount]node
}

// Set adds a mapping, replacing any mapping with the same left-hand side in
// each mode it names.
//
// Replacing and not appending: vim's map table holds one mapping per (mode,
// lhs) pair and a second :nnoremap of the same keys overwrites the first, which
// is what makes re-sourcing a vimrc idempotent. The vimrc's own BufWritePost
// autocmd does exactly that.
func (t *Table) Set(m Mapping) error {
	if m.Expr {
		return &ExprError{LHS: m.LHSText}
	}
	if len(m.LHS) == 0 {
		// Never reachable from internal/vimrc, which refuses a :map with no
		// left-hand side before this is called, and an error rather than a
		// silent drop because a mapping keyed on nothing would match
		// everything.
		return errors.New("E474: Invalid argument: a mapping with no left-hand side")
	}
	if strings.Contains(m.RHSText, "<SID>") || strings.Contains(m.RHSText, "<sid>") {
		m.SID = true
	}
	if m.Unique {
		var clash bool
		m.Modes.each(func(i int) {
			// The node has to carry a mapping and not merely exist:
			// "ab" is a node in the trie the moment "abc" is mapped, and
			// measured, "<unique> ab" beside "abc" is not an error.
			if n := t.at(i, m.Buffer, m.Buf).find(m.LHS); n != nil && n.m != nil {
				clash = true
			}
		})
		if clash {
			return &UniqueError{LHS: m.LHSText}
		}
	}
	// One Mapping value shared by every mode's trie, so that a lookup can
	// compare pointers and so that the modes a listing prints are the ones the
	// command named rather than the one it was found under.
	mp := m
	m.Modes.each(func(i int) {
		n := t.at(i, m.Buffer, m.Buf)
		for _, k := range m.LHS {
			n = n.child(k)
		}
		n.m = &mp
	})
	return nil
}

// Unmap removes the mapping with this left-hand side from each mode named,
// and answers E31 when it was in none of them.
//
// Each mode named: ":map ab" then ":nunmap ab" leaves ab mapped in visual and
// operator-pending, measured, so the loop below removes what it finds and only
// the whole miss is an error.
func (t *Table) Unmap(modes Mode, lhs []key.Key, buffer bool, buf int) error {
	found := false
	modes.each(func(i int) {
		n := t.at(i, buffer, buf).find(lhs)
		if n != nil && n.m != nil {
			n.m = nil
			found = true
		}
	})
	if !found {
		return ErrNoMapping
	}
	return nil
}

// Clear is :mapclear: every mapping in each mode named, gone.
func (t *Table) Clear(modes Mode, buffer bool, buf int) {
	modes.each(func(i int) {
		*t.at(i, buffer, buf) = node{}
	})
}

// Empty reports whether the table holds no mapping at all. The frontend asks
// so that an editor with no vimrc pays nothing for the machinery.
func (t *Table) Empty() bool {
	for i := range t.global {
		if len(t.global[i].kids) > 0 {
			return false
		}
	}
	for _, set := range t.local {
		for i := range set {
			if len(set[i].kids) > 0 {
				return false
			}
		}
	}
	return true
}

// Get returns the mapping with this exact left-hand side in this one mode.
func (t *Table) Get(mode Mode, lhs []key.Key, buf int) (*Mapping, bool) {
	i := mode.index()
	if i < 0 {
		return nil, false
	}
	if set := t.local[buf]; set != nil {
		if n := set[i].find(lhs); n != nil && n.m != nil {
			return n.m, true
		}
	}
	if n := t.global[i].find(lhs); n != nil && n.m != nil {
		return n.m, true
	}
	return nil, false
}

// at is the trie for one mode, global or one buffer's.
func (t *Table) at(i int, buffer bool, buf int) *node {
	if !buffer {
		return &t.global[i]
	}
	if t.local == nil {
		t.local = map[int]*[modeCount]node{}
	}
	set := t.local[buf]
	if set == nil {
		set = &[modeCount]node{}
		t.local[buf] = set
	}
	return &set[i]
}

// find walks the trie to the node this key sequence names, or nil.
func (n *node) find(lhs []key.Key) *node {
	for _, k := range lhs {
		if n.kids == nil {
			return nil
		}
		c := n.kids[k]
		if c == nil {
			return nil
		}
		n = c
	}
	return n
}

// match is the whole of vim's resolution, run over a key sequence.
//
// It walks the buffer-local trie and the global one together and reports the
// longest mapping that completed, how many keys it took, and whether the walk
// ran off the end of the sequence with a node that could still grow. That last
// value is the ambiguity 'timeout' decides, and it is only ever true at the end
// of the keys, which is why it is one bool and not a position.
//
// Ties go to the buffer-local mapping, which is what makes a <buffer> mapping
// shadow a global one of the same length.
func (t *Table) match(mode Mode, keys []entry, buf int) (m *Mapping, n int, more bool) {
	i := mode.index()
	if i < 0 {
		return nil, 0, false
	}

	type live struct {
		n     *node
		local bool
	}
	var cur []live
	if set := t.local[buf]; set != nil {
		cur = append(cur, live{&set[i], true})
	}
	cur = append(cur, live{&t.global[i], false})

	bestLocal := false
	used := 0
	for ; used < len(keys) && len(cur) > 0; used++ {
		var next []live
		for _, c := range cur {
			kid := c.n.kids[keys[used].k]
			if kid == nil {
				continue
			}
			next = append(next, live{kid, c.local})
			if kid.m == nil {
				continue
			}
			if used+1 > n || (used+1 == n && c.local && !bestLocal) {
				m, n, bestLocal = kid.m, used+1, c.local
			}
		}
		cur = next
	}
	if used == len(keys) {
		for _, c := range cur {
			if len(c.n.kids) > 0 {
				more = true
				break
			}
		}
	}
	return m, n, more
}
