package keymap

import "github.com/pkar/pvim/internal/key"

// MaxDepth is 'maxmapdepth', the number of mappings one keystroke may expand
// through before E223. Vim's default is 1000, measured with
// :echo &maxmapdepth, and this is not an option because nothing in the vimrc
// sets it and a second copy of a number nobody changes is a second thing to
// keep in step.
const MaxDepth = 1000

// entry is one key in the typeahead and whether the table is still allowed to
// look at it.
//
// The flag is the leading-key protection: when a right-hand side starts with
// the whole left-hand side, exactly its first key comes back with noremap set,
// which is what keeps "nmap x xx" from being an immediate loop while leaving
// the second x remappable. See doc.go for the measurement that says it is one
// key and not the whole left-hand side.
type entry struct {
	k       key.Key
	noremap bool
	// mapped says a right-hand side put this key here rather than a person
	// typing it. It is what FlushMapped keeps and throws away, and the two
	// have to be told apart: vim's flush_buffers(FLUSH_MINIMAL) drops the
	// mapping's leftovers and leaves the script's next command alone.
	mapped bool
}

// State is what the machine has to be told about the editor before it can
// resolve the next key. It is asked for on every key rather than kept, because
// a right-hand side can change the mode halfway through itself: ":nnoremap X
// ihello" is one normal-mode key and five insert-mode ones, and the sixth is
// looked up in the mode the fifth left behind.
type State struct {
	// Mode is the single mode the next key will be executed in.
	Mode Mode

	// Buf is the current buffer, for the <buffer> mappings.
	Buf int

	// NoMap says the editor is holding the next key for a command that has
	// already started -- the target of an f, the name of a register or a mark
	// -- and vim reads those with no mapping at all. Measured: with "map {
	// gT" in place, "f{" finds the brace.
	NoMap bool
}

// Machine is the typeahead and the resolution over it: keys in through Push,
// keys out through Next, and an explicit wait in between.
//
// The zero Machine has no table and passes everything through, which is what an
// editor with no vimrc wants.
type Machine struct {
	tab *Table

	// ta is the typeahead: the keys that have arrived, or that a right-hand
	// side pushed, and have not been handed to the editor yet.
	ta []entry

	// depth is how many mappings deep the current chain is, reset the moment
	// the typeahead drains. A chain that never drains is the recursion E223
	// is for.
	depth int

	// forced is a Timeout that has not been used yet: the next Next resolves
	// the ambiguity instead of waiting on it.
	forced bool
}

// New builds a machine over a table. The table may be empty and may be added
// to afterwards; the machine holds the pointer and re-reads it on every key,
// so a mapping made at runtime takes effect on the next keystroke.
func New(t *Table) *Machine { return &Machine{tab: t} }

// Push puts a typed key on the end of the typeahead. It resolves nothing; Next
// does that.
func (m *Machine) Push(k key.Key) {
	if len(m.ta) == 0 {
		m.depth = 0
	}
	m.forced = false
	m.ta = append(m.ta, entry{k: k})
}

// PushKeys is Push for a whole sequence, used by a caller replaying a script.
func (m *Machine) PushKeys(keys []key.Key) {
	for _, k := range keys {
		m.Push(k)
	}
}

// Pending reports whether the machine is holding keys it has not resolved.
// A frontend that wants to run a mapping timeout starts its clock when this
// turns true and stops it when it turns false.
func (m *Machine) Pending() bool { return len(m.ta) > 0 }

// PendingKeys is the held keys, for a showcmd that wants to display them.
func (m *Machine) PendingKeys() []key.Key {
	out := make([]key.Key, len(m.ta))
	for i, e := range m.ta {
		out[i] = e.k
	}
	return out
}

// Timeout says the wait is over: the next Next resolves whatever is held
// instead of asking for another key.
//
// This is the whole of 'timeout' as far as this package is concerned. Whether
// to call it, and after how long, is the frontend's, and under the vimrc's
// 'notimeout' the answer is never. Calling it with nothing pending does
// nothing.
func (m *Machine) Timeout() {
	if len(m.ta) > 0 {
		m.forced = true
	}
}

// FlushMapped throws away the keys a right-hand side pushed and keeps the ones
// a person typed behind them.
//
// This is vim's flush_buffers(FLUSH_MINIMAL), which is what an error message
// and a beep both do to the typeahead, and it is why the vimrc's
//
//	nnoremap <Space> za " Spacebar to unfold
//
// does not type "Spacebar to unfold" into the buffer. Measured on a file with
// no folds: vim runs the za, says "E490: No fold found", and the twenty-one
// characters behind it never happen; the file comes back byte-identical.
//
// The keys a mapping pushed are always in front of the keys typed after it,
// because expand puts a right-hand side at the head of the typeahead, so this
// is the leading run and not a filter.
func (m *Machine) FlushMapped() {
	i := 0
	for i < len(m.ta) && m.ta[i].mapped {
		i++
	}
	m.ta = m.ta[i:]
	if len(m.ta) == 0 {
		m.depth = 0
		m.forced = false
	}
}

// Reset throws the typeahead away. It is what a beep does to a half-typed
// command and what the end of a script does to a mapping nobody completed.
func (m *Machine) Reset() {
	m.ta = nil
	m.depth = 0
	m.forced = false
}

// Next resolves and returns the next key for the editor to execute.
//
// The second return is false when there is nothing to execute: either the
// typeahead is empty, or it holds a prefix that could still grow and the
// machine is waiting for the key that decides. Pending tells those two apart.
//
// The error is E223 and the two refusals, and it arrives with the typeahead
// already thrown away, because vim's answer to a recursive mapping is the
// message and a cleared typebuf.
func (m *Machine) Next(st State) (key.Key, bool, error) {
	for {
		if len(m.ta) == 0 {
			m.depth = 0
			m.forced = false
			return key.Key{}, false, nil
		}
		if m.tab == nil || st.NoMap || m.ta[0].noremap {
			return m.pop(), true, nil
		}

		mp, n, more := m.tab.match(st.Mode, m.ta, st.Buf)

		switch {
		case mp != nil && (!more || m.forced || (mp.Nowait && mp.Buffer)):
			// A complete match, and either nothing longer could still arrive,
			// or the wait is over, or this is the buffer-local <nowait> case
			// that is not allowed to wait in the first place.
			if err := m.expand(mp, n); err != nil {
				return key.Key{}, false, err
			}
		case more && !m.forced:
			// Either a longer mapping is still possible over a complete match,
			// or nothing has completed yet and one still could. Both are the
			// wait, and both are 'timeout's to end.
			return key.Key{}, false, nil
		default:
			// No mapping over these keys, or the wait ended with only a
			// prefix. The first key is the editor's, and everything behind it
			// goes round again: it may start a mapping of its own, which is
			// how "abx" runs the ab mapping and then the x.
			m.forced = false
			return m.pop(), true, nil
		}
	}
}

// pop takes the first key off the typeahead.
func (m *Machine) pop() key.Key {
	k := m.ta[0].k
	m.ta = m.ta[1:]
	if len(m.ta) == 0 {
		m.depth = 0
		m.forced = false
	}
	return k
}

// expand replaces the n keys a mapping matched with the keys it produces.
//
// Three rules, all measured, all in doc.go: a :noremap right-hand side comes
// back untouchable; a :map right-hand side comes back remappable except for its
// first key when the whole left-hand side is a prefix of it; and the keys typed
// after the match stay where they were, behind the ones the mapping pushed.
func (m *Machine) expand(mp *Mapping, n int) error {
	if mp.SID {
		m.Reset()
		return &ScriptIDError{LHS: mp.LHSText}
	}
	m.depth++
	if m.depth > MaxDepth {
		m.Reset()
		return ErrRecursive
	}
	m.forced = false

	protect := 0
	if !mp.NoRemap && len(mp.RHS) > 0 && hasPrefix(mp.RHS, mp.LHS) {
		protect = 1
	}

	ins := make([]entry, 0, len(mp.RHS)+len(m.ta)-n)
	for i, k := range mp.RHS {
		ins = append(ins, entry{k: k, noremap: mp.NoRemap || i < protect, mapped: true})
	}
	m.ta = append(ins, m.ta[n:]...)
	return nil
}

// hasPrefix reports whether keys starts with the whole of pre.
func hasPrefix(keys, pre []key.Key) bool {
	if len(keys) < len(pre) {
		return false
	}
	for i, k := range pre {
		if keys[i] != k {
			return false
		}
	}
	return true
}
