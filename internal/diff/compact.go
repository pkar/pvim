package diff

// Hunk compaction: xdiff's xdl_change_compact, without which two files with
// repeated lines diff differently here and in vim.
//
// Myers finds a shortest edit script and there is usually more than one. Two
// identical lines next to a deleted one give the same edit distance whichever
// of them is called deleted, and Myers and xdiff pick different ones. Vim's
// diff mode is xdiff and so is git's, so "different but equally minimal" is
// still a wrong answer here: it is a filler line on the wrong row and a ]c
// that stops one line off from where the person's git and their vim both say
// the change is.
//
// So the blocks Myers produces are run through xdiff's own compaction. Every
// group of changed lines is slid as early as it can go and then as late as it
// can go, and it comes to rest either beside a group in the other file --
// which is what makes a changed block show as one block on both sides rather
// than as a delete beside an unrelated insert -- or, when there is no such
// place, at the latest position it can reach.
//
// The transcription is of git's xdiff/xdiffi.c as of 2.51: group_init,
// group_next, group_previous, group_slide_up, group_slide_down and the loop
// that drives them.
//
// XDF_INDENT_HEURISTIC is the one branch not taken, and this is the honest
// version of what that costs. Git turns it on by default; vim 9.2's default
// 'diffopt' has "indent-heuristic" in it too, and the vimrc this editor is
// built for replaces that default with "vertical,filler,iwhite", which does
// not. So the setting the daily editor runs under is the one measured here,
// and the two defaults are a place where a hunk boundary could land a line
// away from where git or a stock vim would put it. Nothing in the corpus
// separates them -- two fixtures in vim_test.go run under the heuristic
// spellings and both pass -- so the difference is a possibility that was
// looked for and not found, which is not the same as one that is not there.

// marks is one file's per-line "this line is part of a change" flags, with
// xdiff's zero sentinels at -1 and n so that the loops need no bounds test.
type marks struct {
	ch []bool
	n  int
}

func (m *marks) at(i int) bool {
	if i < 0 || i >= m.n {
		return false
	}
	return m.ch[i]
}

// group is a maximal run of changed lines, [start, end). An empty group --
// start == end -- is a position between two unchanged lines, and those are
// the positions that pair up with a non-empty group on the other side.
type group struct{ start, end int }

func groupInit(m *marks) group {
	g := group{}
	for m.at(g.end) {
		g.end++
	}
	return g
}

func groupNext(m *marks, g *group) bool {
	if g.end == m.n {
		return false
	}
	g.start = g.end + 1
	for g.end = g.start; m.at(g.end); g.end++ {
	}
	return true
}

func groupPrev(m *marks, g *group) bool {
	if g.start == 0 {
		return false
	}
	g.end = g.start - 1
	for g.start = g.end; m.at(g.start - 1); g.start-- {
	}
	return true
}

// slideUp moves the group one line earlier, which is only possible when the
// line before it is the same as its last line. Sliding can join it to the
// group above, which is why start walks back over any changed lines it lands
// beside.
func slideUp(m *marks, recs lines, g *group) bool {
	if g.start <= 0 || g.end <= g.start || recs[g.start-1] != recs[g.end-1] {
		return false
	}
	g.start--
	m.ch[g.start] = true
	g.end--
	m.ch[g.end] = false
	for m.at(g.start - 1) {
		g.start--
	}
	return true
}

// slideDown is slideUp the other way.
func slideDown(m *marks, recs lines, g *group) bool {
	if g.end >= m.n || g.end <= g.start || recs[g.start] != recs[g.end] {
		return false
	}
	m.ch[g.start] = false
	g.start++
	m.ch[g.end] = true
	g.end++
	for m.at(g.end) {
		g.end++
	}
	return true
}

// compact slides every group of ma to the position xdiff would leave it in,
// keeping mb's groups in step so that the two files stay aligned.
func compact(recs lines, ma *marks, mb *marks) {
	g := groupInit(ma)
	go_ := groupInit(mb)
	for {
		if g.end == g.start {
			goto next
		}
		for {
			size := g.end - g.start
			endMatchingOther := -1

			for slideUp(ma, recs, &g) {
				if !groupPrev(mb, &go_) {
					return // the two files fell out of step; leave the rest alone
				}
			}
			earliestEnd := g.end
			if go_.end > go_.start {
				endMatchingOther = g.end
			}
			for slideDown(ma, recs, &g) {
				if !groupNext(mb, &go_) {
					return
				}
				if go_.end > go_.start {
					endMatchingOther = g.end
				}
			}

			if size == g.end-g.start {
				// The group did not swallow a neighbour on the way past, so
				// its size is settled and the position can be chosen.
				switch {
				case g.end == earliestEnd:
					// It could not move at all.
				case endMatchingOther != -1:
					// Put it back beside the last group in the other file it
					// lined up with, which is what makes one change read as
					// one change on both sides.
					for go_.end == go_.start {
						if !slideUp(ma, recs, &g) || !groupPrev(mb, &go_) {
							return
						}
					}
				}
				break
			}
		}
	next:
		if !groupNext(ma, &g) {
			return
		}
		if !groupNext(mb, &go_) {
			return
		}
	}
}

// blocks reads the flag arrays back as a list of changes.
//
// The two files' groups are walked in lockstep, which is sound because an
// unchanged line on one side is an unchanged line on the other: they are the
// pairs the diff matched, in order, so the k-th gap between unchanged lines
// on the left is the k-th gap on the right.
func blocks(ma, mb *marks) []Change {
	var out []Change
	g := groupInit(ma)
	go_ := groupInit(mb)
	for {
		if g.end > g.start || go_.end > go_.start {
			out = append(out, Change{
				AStart: g.start + 1, ACount: g.end - g.start,
				BStart: go_.start + 1, BCount: go_.end - go_.start,
			})
		}
		if !groupNext(ma, &g) || !groupNext(mb, &go_) {
			return out
		}
	}
}
