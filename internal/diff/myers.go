package diff

// Myers' O(ND) difference algorithm, the linear-space divide-and-conquer form
// from section 4b of the 1986 paper.
//
// It is here rather than behind a dependency because four
// third-party modules and none of them is a differ, and because 200 lines of
// well-understood recursion is cheaper to own than a module that has to be
// audited for cgo on every upgrade. There is no heuristic bail-out: vim's
// xdiff gives up on very large inputs and falls back, and a fall-back that
// produces a different answer from the one the tests measured is worse than
// being slow, so this one always finishes. See Diff for the size guard that
// keeps "always finishes" from meaning "eventually".

// lines is one side of the comparison, already normalised by the 'diffopt'
// flags: the strings compared here are keys and not the text that is shown.
type lines []string

// bisect finds the middle snake of a[as:ae] against b[bs:be] and returns the
// point to split both sequences at.
//
// vf and vb are the forward and backward furthest-reaching D-paths, passed in
// so that the recursion allocates them once rather than per level. Both are
// indexed by k+off where off is half their length.
func bisect(a, b lines, as, ae, bs, be int, vf, vb []int) (int, int) {
	n, m := ae-as, be-bs
	maxD := (n + m + 1) / 2
	off := maxD
	// Only the window this call can reach is cleared. Clearing the whole of
	// vf and vb would make every level of the recursion cost the length of
	// the longest file rather than the length of the range it is looking at,
	// which turns an O(ND) differ into an O(N*D) one on the first big file.
	for i := max(0, off-maxD-1); i <= min(len(vf)-1, off+maxD+1); i++ {
		vf[i], vb[i] = -1, -1
	}
	vf[off+1], vb[off+1] = 0, 0
	delta := n - m
	// An odd delta means the forward and backward paths overlap on an odd
	// step, which decides which of the two loops below is allowed to declare
	// the middle snake found.
	front := delta%2 != 0
	// The four bounds shrink the k range once a diagonal has run off the end
	// of either sequence, which is what keeps the loop O(ND) rather than
	// O(D^2) on lopsided input.
	k1start, k1end, k2start, k2end := 0, 0, 0, 0
	for d := 0; d <= maxD; d++ {
		if d > budget {
			// Out of budget. The furthest the forward search reached is a
			// point both halves of the recursion can be split at, so the
			// answer is still a diff of the whole input; it is just not the
			// shortest one. See budget.
			return giveUp(as, ae, bs, be, off, vf)
		}
		for k1 := -d + k1start; k1 <= d-k1end; k1 += 2 {
			i := off + k1
			var x int
			if k1 == -d || (k1 != d && vf[i-1] < vf[i+1]) {
				x = vf[i+1]
			} else {
				x = vf[i-1] + 1
			}
			y := x - k1
			for x < n && y < m && a[as+x] == b[bs+y] {
				x++
				y++
			}
			vf[i] = x
			switch {
			case x > n:
				k1end += 2
			case y > m:
				k1start += 2
			case front:
				j := off + delta - k1
				if j >= 0 && j < len(vb) && vb[j] != -1 && x >= n-vb[j] {
					return as + x, bs + y
				}
			}
		}
		for k2 := -d + k2start; k2 <= d-k2end; k2 += 2 {
			i := off + k2
			var x int
			if k2 == -d || (k2 != d && vb[i-1] < vb[i+1]) {
				x = vb[i+1]
			} else {
				x = vb[i-1] + 1
			}
			y := x - k2
			for x < n && y < m && a[ae-x-1] == b[be-y-1] {
				x++
				y++
			}
			vb[i] = x
			switch {
			case x > n:
				k2end += 2
			case y > m:
				k2start += 2
			case !front:
				j := off + delta - k2
				if j < 0 || j >= len(vf) || vf[j] == -1 || vf[j] < n-x {
					continue
				}
				// vf[j] is stored before the "ran off the end" cases below
				// have a chance to discard it, so a diagonal that already
				// left the sequence can be sitting in it. Splitting there
				// would hand the recursion a range wider than the one it was
				// given, which is a panic two levels down.
				fx, fy := vf[j], vf[j]-(j-off)
				if fx < 0 || fy < 0 || fx > n || fy > m {
					continue
				}
				return as + fx, bs + fy
			}
		}
	}
	// Unreachable for any two finite sequences: by d == maxD the forward and
	// backward paths have met. Returning the whole range as one change is the
	// answer that is still correct if it ever is reached.
	return ae, be
}

// walk fills out with the changes between a[as:ae] and b[bs:be], in line
// order.
func walk(a, b lines, as, ae, bs, be int, vf, vb []int, out *[]Change) {
	for as < ae && bs < be && a[as] == b[bs] {
		as++
		bs++
	}
	for as < ae && bs < be && a[ae-1] == b[be-1] {
		ae--
		be--
	}
	switch {
	case as == ae && bs == be:
		return
	case as == ae:
		*out = append(*out, Change{AStart: as + 1, ACount: 0, BStart: bs + 1, BCount: be - bs})
		return
	case bs == be:
		*out = append(*out, Change{AStart: as + 1, ACount: ae - as, BStart: bs + 1, BCount: 0})
		return
	}
	// One line against one line is a change and not a delete and an insert.
	// The recursion below would reach the same answer; stopping here saves a
	// bisect on the commonest hunk there is.
	if ae-as == 1 && be-bs == 1 {
		*out = append(*out, Change{AStart: as + 1, ACount: 1, BStart: bs + 1, BCount: 1})
		return
	}
	x, y := bisect(a, b, as, ae, bs, be, vf, vb)
	if (x == as && y == bs) || (x == ae && y == be) {
		// A split at either end would recurse on the range it was given.
		// Nothing in the algorithm produces one -- the middle snake is
		// strictly inside -- and the guard is here so that a bug in bisect
		// is a wrong diff and not a stack overflow.
		*out = append(*out, Change{AStart: as + 1, ACount: ae - as, BStart: bs + 1, BCount: be - bs})
		return
	}
	walk(a, b, as, x, bs, y, vf, vb, out)
	walk(a, b, x, ae, y, be, vf, vb, out)
}

// giveUp is the split bisect takes when it runs out of budget: the furthest
// point the forward search reached, clamped inside the range so that both
// halves of the recursion are strictly smaller than the whole and the
// recursion terminates.
func giveUp(as, ae, bs, be, off int, vf []int) (int, int) {
	bestX, bestY := -1, -1
	for i, x := range vf {
		if x < 0 {
			continue
		}
		y := x - (i - off)
		if x < 0 || y < 0 || as+x > ae || bs+y > be {
			continue
		}
		if x+y > bestX+bestY {
			bestX, bestY = x, y
		}
	}
	if bestX < 0 || (bestX == 0 && bestY == 0) || (as+bestX == ae && bs+bestY == be) {
		// Nothing usable: halve the longer side, which always shrinks.
		return as + (ae-as+1)/2, bs + (be-bs+1)/2
	}
	return as + bestX, bs + bestY
}
