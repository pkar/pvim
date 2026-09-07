package diff

// budget caps how far the middle-snake search will look before it gives up
// and splits where it has got to.
//
// Myers is O(ND) in the edit distance, so two files with a hundred changed
// lines cost nothing and two files with nothing in common cost the product of
// their lengths. Vim's xdiff has the same problem and answers it by falling
// back to a different algorithm; this answers it by returning a diff that is
// correct but not minimal, which is the answer that keeps one behaviour
// rather than two. At 4096 the fallback cannot be reached by any diff a
// person is reading: it wants more than four thousand changed lines in one
// file against another, at which point diff mode is not what is wanted.
const budget = 4096

// Diff is the line diff: what it takes to turn a into b, as blocks.
//
// Both sides are normalised by the 'diffopt' flags before anything is
// compared, so a diff under 'iwhite' is a diff of two files that had their
// white space folded, and the blocks that come back index the ORIGINAL lines.
func Diff(a, b [][]byte, o Options) []Change {
	ka := make(lines, len(a))
	for i, l := range a {
		ka[i] = o.normalize(l)
	}
	kb := make(lines, len(b))
	for i, l := range b {
		kb[i] = o.normalize(l)
	}
	// Lines with no counterpart anywhere on the other side are a change in
	// every possible script, so they come out before Myers runs. This is
	// xdiff's xdl_cleanup_records, and it is not only a speed-up: it decides
	// which of several equally short scripts is found, and the one it leads
	// to is vim's. Without it the two disagree on files with repeated lines
	// -- a "ddd" three times over against a "ddd" once -- about WHICH of the
	// identical lines is the one that survived, which is a filler line on the
	// wrong row.
	sa, ia := onlyMatched(ka, kb)
	sb, ib := onlyMatched(kb, ka)

	maxD := (len(sa)+len(sb))/2 + 2
	vf := make([]int, 2*maxD+2)
	vb := make([]int, 2*maxD+2)
	var out []Change
	walk(sa, sb, 0, len(sa), 0, len(sb), vf, vb, &out)
	// Myers has found a shortest script; compaction decides which of the
	// equally short ones this is, and that decision is vim's and git's and
	// not this package's. See compact.go.
	ma, mb := project(out, ka, kb, ia, ib)
	compact(ka, ma, mb)
	compact(kb, mb, ma)
	return blocks(ma, mb)
}

// onlyMatched drops the lines of s that do not appear in other, and returns
// what is left with the original 0-based index of each surviving line.
func onlyMatched(s, other lines) (lines, []int) {
	seen := make(map[string]struct{}, len(other))
	for _, l := range other {
		seen[l] = struct{}{}
	}
	kept := make(lines, 0, len(s))
	idx := make([]int, 0, len(s))
	for i, l := range s {
		if _, ok := seen[l]; ok {
			kept = append(kept, l)
			idx = append(idx, i)
		}
	}
	return kept, idx
}

// project turns a diff of the kept lines back into per-line change flags over
// the whole of both files: every line that was dropped is a change, and so is
// every line the diff of what was kept called one.
func project(cs []Change, ka, kb lines, ia, ib []int) (*marks, *marks) {
	ma := &marks{ch: make([]bool, len(ka)), n: len(ka)}
	mb := &marks{ch: make([]bool, len(kb)), n: len(kb)}
	for _, m := range []struct {
		f    *marks
		keep []int
	}{{ma, ia}, {mb, ib}} {
		for i := range m.f.ch {
			m.f.ch[i] = true
		}
		for _, i := range m.keep {
			m.f.ch[i] = false
		}
	}
	for _, c := range cs {
		for i := 0; i < c.ACount; i++ {
			ma.ch[ia[c.AStart+i-1]] = true
		}
		for i := 0; i < c.BCount; i++ {
			mb.ch[ib[c.BStart+i-1]] = true
		}
	}
	return ma, mb
}
