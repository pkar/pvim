package syntax

import "unicode/utf8"

// The matcher: a per-line state machine that walks a buffer and says what
// colour every byte is.
//
// Vim's own rules, from:help:syn-priority, and they are the whole of it:
//
// 1. When two Match or Region items start in the same position, the one
// defined last wins.
// 2. A Keyword beats a Match or a Region.
// 3. An item that starts earlier beats one that starts later.
//
// So the loop walks left to right, and at each column asks every item that is
// allowed there for its first match at or after that column, takes the earliest
// and, on a tie, the one with the highest id. Items are asked in reverse
// definition order and a later one is only displaced by a strictly earlier
// match, which is rule 1 written as a comparison.
//
// What makes it fast enough to run on every frame is not the loop, it is the
// cache: the state at the end of every line -- the stack of regions still open
// and the nextgroup still pending -- is kept, so a redraw of twenty visible
// lines is twenty lines of work, and an edit only throws away the states from
// the changed line down. See the benchmark in the package doc for the number.

// Span is a run of bytes on one line that carries a syntax group.
//
// Start and End are byte columns, 0-based, End exclusive, exactly as
// screen.Match spells the same idea. Spans on a line do not overlap and are in
// column order: where vim would have nested items, the innermost one is what
// is here, because that is the one whose colour is drawn.
type Span struct {
	Start, End int
	// Group indexes the syntax group table. GroupName turns it into the name
	// vim's synIDattr() would print.
	Group int
}

// Source is the buffer the highlighter reads. It is the two methods
// text.Buffer already has, named the same, so that cmd/pvim hands one over
// without an adapter.
type Source interface {
	LineCount() int
	Line(n int) []byte
}

// frame is one open item on the stack: a region that has not ended, or a match
// item whose contained items are being looked for inside it.
type frame struct {
	item int
	// group is what text inside this frame is painted, which is the item's own
	// group unless it is transparent, in which case it is whatever encloses it.
	group int
	// contains is the item set allowed inside, already resolved for
	// transparency.
	contains bitset
	// endAt is where the frame closes on this line, or -1 when it runs past
	// the end of it. For a region it is the start of the end match.
	endAt int
	// endTo is where scanning resumes after the frame closes: the end of the
	// end match for a region, the same as endAt for a match item.
	endTo int
	// endHL is the highlight range of the end match, for a region with a
	// matchgroup.
	endHLFrom, endHLTo int
	// region says whether the frame came from a syn region, which is the only
	// kind that can outlive a line.
	region bool

	// ext is what the region's start pattern captured with \z(, which its skip
	// and end patterns need before they can be compiled at all.
	ext string

	// startAt is the column the frame's item matched at, kept so that the same
	// item cannot be entered inside itself at the same column. Without it a
	// `contains=ALLBUT,...` that does not name its own group is an endless
	// loop: the item matches, the matcher steps inside it, and the same
	// pattern matches at the same column again. Vim has the same check and for
	// the same reason.
	startAt int
}

// state is what one line hands the next.
type state struct {
	stack []frame
	// next is the item whose nextgroup is still pending, or -1. It survives a
	// line break only when the item asked for skipnl or skipempty.
	next int
}

// Highlighter walks a buffer and answers what is highlighted on a line.
//
// It is not safe for concurrent use and it is not meant to be: one buffer, one
// highlighter, called from the redraw.
type Highlighter struct {
	syn *Syntax
	src Source

	// ends[i] is the state at the end of buffer line i+1. The entries from
	// line from through line valid are the ones that mean anything, and the
	// state before line from is empty.
	//
	// from is not always 1, and `syn sync` is why. Walking from the top of the
	// buffer is the only way to be certain, and it is what happens whenever it
	// is affordable, but a jump to line 40,000 of a file nobody has looked at
	// would be 40,000 lines of work before the first frame. Vim's answer is
	// `syn sync minlines=N`: start N lines back and take the state there as
	// nothing. go.vim asks for 500, markdown for 10. See syncLimit.
	ends  []state
	from  int
	valid int

	// spans caches what SpansOn answered, so that a redraw of a screen nothing
	// has touched costs a map lookup a line. It is dropped whole on a change
	// rather than trimmed, because the lines below a change are the ones whose
	// colour a change moves.
	spanCache map[int][]Span

	// paint is the per-byte group of the line being walked, reused so that a
	// redraw allocates nothing.
	paint []int32
	// spans is the coalesced answer, reused for the same reason.
	spans []Span

	// blocked holds the items that have already matched, zero width, at
	// blockedAt. Without it a zero-width match is found again the moment the
	// loop asks the same column a second time.
	blocked   bitset
	blockedAt int

	// curLine and lastLine are which buffer line is being walked and how many
	// there are, which is the whole of what \%^ and \%$ need.
	curLine  int
	lastLine int

	// next caches, per item, where its pattern next matches on the line being
	// walked. This is vim's next_match_col and it is the difference between an
	// editor and a slideshow: without it every column asks every item that is
	// allowed there for a fresh search, which on a Go file is sixty regexp
	// searches per step and ten steps per line. A cached answer stays true as
	// long as the column has not passed it, because "the first match at or
	// after column 4" is also the first match at or after column 2 whenever it
	// starts at 4 or later.
	next []nextMatch
	gen  int32
}

// nextMatch is one item's cached answer for the line being walked.
type nextMatch struct {
	gen    int32
	from   int
	ms, me int
	ok     bool
}

// nextOf answers where an item's pattern next matches at or after col, out of
// the cache when the cache still holds.
func (h *Highlighter) nextOf(id int, line string, col int) (int, int, bool) {
	if len(h.next) != len(h.syn.items) {
		h.next = make([]nextMatch, len(h.syn.items))
	}
	c := &h.next[id]
	if c.gen == h.gen && c.from <= col && (!c.ok || c.ms >= col) {
		return c.ms, c.me, c.ok
	}
	it := h.syn.items[id]
	ms, me, ok := 0, 0, false
	switch it.kind {
	case kindMatch:
		if h.usable(it.pat) {
			ms, me, ok = it.pat.find(line, col)
		}
	case kindRegion:
		for _, p := range it.starts {
			if !h.usable(p) {
				continue
			}
			a, b, found := p.find(line, col)
			if !found || (ok && a >= ms) {
				continue
			}
			ms, me, ok = a, b, true
		}
	}
	*c = nextMatch{gen: h.gen, from: col, ms: ms, me: me, ok: ok}
	return ms, me, ok
}

// block marks an item as already used at a column.
func (h *Highlighter) block(id, col int) {
	if h.blockedAt != col || h.blocked == nil {
		h.blocked = newBitset(len(h.syn.items))
		h.blockedAt = col
	}
	h.blocked.set(id)
}

// isBlocked reports whether an item has already matched zero width here.
func (h *Highlighter) isBlocked(id, col int) bool {
	return h.blockedAt == col && h.blocked.has(id)
}

// NewHighlighter returns a highlighter over a buffer.
func NewHighlighter(s *Syntax, src Source) *Highlighter {
	return &Highlighter{syn: s, src: src, from: 1}
}

// Syntax returns the rules this highlighter runs.
func (h *Highlighter) Syntax() *Syntax { return h.syn }

// Changed tells the highlighter that a buffer line has been edited, so that
// everything cached from there down is recomputed.
//
// It takes the first changed line and not a range because a change on line 40
// can change the colour of line 40,000: an unterminated string opens a region
// that runs to the end of the file. Everything above the changed line is
// untouched, which is what makes typing cost one line's work and not the
// buffer's.
func (h *Highlighter) Changed(line int) {
	if line-1 < h.valid {
		h.valid = line - 1
	}
	if h.valid < h.from-1 {
		h.from, h.valid = 1, 0
	}
	if h.valid < 0 {
		h.valid = 0
	}
	h.spanCache = nil
}

// Invalidate throws the whole cache away, which is what a colourscheme reload
// or a:syntax off and on again wants.
func (h *Highlighter) Invalidate() {
	h.from, h.valid = 1, 0
	h.spanCache = nil
}

// syncLimit is how far back a jump into unvisited buffer starts from, in
// lines, or 0 for "from the top of the buffer".
//
// `syn sync fromstart` is 0 and means what it says. `syn sync minlines=N` is N.
// A file that says neither gets defaultSync, which is a guess in the same shape
// as every number vim's own syntax files put there.
func (h *Highlighter) syncLimit() int {
	if h.syn.syncFromStart {
		return 0
	}
	if n := h.syn.syncMinLines; n > 0 {
		return n
	}
	if n := h.syn.syncMaxLines; n > 0 {
		return n
	}
	return defaultSync
}

// defaultSync is how far back a file with no `syn sync` line starts from.
//
// 500 because that is what go.vim asks for in as many words, with a comment
// saying it is the expensive-but-imprecise workaround for a bug in vim's
// grouphere. A file whose regions are longer than 500 lines will be wrong at
// the top of the screen after a jump, and right again as soon as the scroll
// reaches it from above, which is exactly the deal vim offers.
const defaultSync = 500

// SpansOn returns the highlighted runs on a buffer line.
//
// The slice is the cache's own and must not be written to. It stays valid until
// the next Changed or Invalidate, so a caller redrawing the same screen twice
// gets the same slice back both times and allocates nothing.
func (h *Highlighter) SpansOn(line int) []Span {
	if h.syn == nil || h.src == nil || line < 1 || line > h.src.LineCount() {
		return nil
	}
	if sp, ok := h.spanCache[line]; ok {
		return sp
	}
	h.catchUp(line - 1)
	in := h.stateBefore(line)
	out, spans := h.walk(line, string(h.src.Line(line)), in, true)
	h.record(line, out)

	if h.spanCache == nil || len(h.spanCache) > spanCacheMax {
		h.spanCache = map[int][]Span{}
	}
	h.spanCache[line] = append([]Span(nil), spans...)
	return h.spanCache[line]
}

// spanCacheMax caps the span cache at a few screens' worth of lines. It is
// dropped whole when it fills rather than evicted one line at a time, because
// the cost of being wrong about which line to drop is one line recomputed and
// the cost of an eviction list is a list.
const spanCacheMax = 2048

// catchUp computes the end state of every line up to and including n.
func (h *Highlighter) catchUp(n int) {
	if n > h.src.LineCount() {
		n = h.src.LineCount()
	}
	if n < h.from-1 {
		// Scrolled back above the window the last sync started from, so what
		// is cached says nothing about here.
		h.from, h.valid = 1, 0
	}
	if limit := h.syncLimit(); limit > 0 && n-h.valid > limit {
		h.from = n - limit + 1
		if h.from < 1 {
			h.from = 1
		}
		h.valid = h.from - 1
	}
	for h.valid < n {
		lnum := h.valid + 1
		in := h.stateBefore(lnum)
		out, _ := h.walk(lnum, string(h.src.Line(lnum)), in, false)
		h.record(lnum, out)
	}
}

// stateBefore is what line lnum starts with.
func (h *Highlighter) stateBefore(lnum int) state {
	if lnum <= h.from || lnum <= 1 || lnum-2 >= h.valid || lnum-2 >= len(h.ends) {
		return state{next: -1}
	}
	return h.ends[lnum-2]
}

// record stores the state at the end of a line.
func (h *Highlighter) record(lnum int, st state) {
	if lnum > len(h.ends) {
		// Grown to the buffer's length in one go: appending a line at a time
		// over a 40,000-line file is 17 reallocations and a copy of the whole
		// slice on each.
		want := h.src.LineCount()
		if want < lnum {
			want = lnum
		}
		grown := make([]state, want)
		copy(grown, h.ends)
		for i := len(h.ends); i < want; i++ {
			grown[i] = state{next: -1}
		}
		h.ends = grown
	}
	h.ends[lnum-1] = st
	if lnum > h.valid {
		h.valid = lnum
	}
}

// walk is the line loop. It returns the state the next line starts with and,
// when collect is set, the spans on this one.
func (h *Highlighter) walk(lnum int, line string, in state, collect bool) (state, []Span) {
	s := h.syn
	if collect {
		// One entry per byte plus one, all "no group", reused between lines.
		if cap(h.paint) < len(line)+1 {
			h.paint = make([]int32, len(line)+1)
		}
		h.paint = h.paint[:len(line)+1]
		for i := range h.paint {
			h.paint[i] = -1
		}
	}

	h.blockedAt = -1
	h.gen++
	h.curLine, h.lastLine = lnum, h.src.LineCount()
	stack := append([]frame(nil), in.stack...)
	pending, pendingAt := in.next, 0
	if pending >= 0 && !s.items[pending].skipNl && !s.items[pending].skipEmpty {
		pending = -1
	}

	// A region that came in from the line before has to find its end on this
	// line before anything is looked for inside it.
	for i := range stack {
		if stack[i].region && stack[i].endAt < 0 {
			h.locateEnd(&stack[i], line, 0)
		}
	}

	col := 0
	stuck, stuckAt := 0, -1
	for col <= len(line) {
		// The column has to make progress. Every way it can fail to is a bug
		// in this file rather than in the syntax file, and a redraw that hangs
		// the editor is the worst of them, so the loop steps over the
		// character it could not get past and carries on.
		if col == stuckAt {
			stuck++
			if stuck > 64 {
				col = h.forward(line, col)
				stuck, stuckAt = 0, -1
				continue
			}
		} else {
			stuck, stuckAt = 0, col
		}

		// Close every frame whose end has been reached.
		closed := false
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.endAt < 0 || col < top.endAt {
				break
			}
			if collect && top.region && top.endHLTo > top.endHLFrom {
				h.fill(top.endHLFrom, top.endHLTo, h.endGroup(top))
			}
			done := *top
			stack = stack[:len(stack)-1]
			if col < done.endTo {
				col = done.endTo
			}
			pending, pendingAt = h.arm(done.item, col)
			closed = true
		}
		if closed {
			continue
		}
		if col >= len(line) {
			break
		}

		allowed, group := h.context(stack)
		stop := h.frameEnd(stack, len(line))

		// A nextgroup applies at the position straight after the item that
		// armed it, or past the blanks when that item asked for skipwhite.
		// Those blanks are not a gap where ordinary matching resumes: vim
		// steps over them and looks for the nextgroup on the far side, and
		// looking for anything else in between is how `def foo` loses
		// pythonFunction to whatever else matches the space. When nothing in
		// the nextgroup matches there, ordinary matching resumes at that same
		// position, which is also vim.
		it, ms, me, hs, he := -1, 0, 0, 0, 0
		if pending >= 0 {
			target := h.skipTo(line, pendingAt, pending)
			if target > stop {
				target = stop
			}
			if col < target {
				if collect {
					h.fill(col, target, group)
				}
				col = target
				continue
			}
			if col == target {
				it, ms, me, hs, he = h.best(line, col, stop, s.next[pending], stack)
				if it >= 0 && ms != col {
					it = -1
				}
			}
			pending = -1
		}
		if it < 0 {
			it, ms, me, hs, he = h.best(line, col, stop, allowed, stack)
		}
		if it < 0 {
			// Nothing matches between here and the end of whatever encloses
			// this, so all of it is that item's own colour.
			if collect {
				h.fill(col, stop, group)
			}
			col = stop
			if col >= len(line) {
				break
			}
			continue
		}
		if ms > col && collect {
			h.fill(col, ms, group)
		}
		zero := ms == me
		col = h.enter(&stack, it, line, ms, me, hs, he, group, collect)
		if len(stack) == 0 || stack[len(stack)-1].item != it {
			pending, pendingAt = h.arm(it, me)
		} else {
			pending = -1
		}
		if zero {
			// A zero-width match cannot move the column, and markdown is built
			// on one: `syn match markdownLineStart "^[<@]\@!"` matches nothing
			// at all and exists only to carry a nextgroup that every heading,
			// list marker and code block hangs off. So the item is blocked at
			// this column rather than stepped over, its nextgroup is armed,
			// and the loop asks again for whatever else is here.
			h.block(it, col)
			if pending < 0 {
				col = h.forward(line, ms)
			}
		}
	}

	// Whatever is still open paints to the end of the line.
	if collect {
		_, group := h.context(stack)
		if col < len(line) {
			h.fill(col, len(line), group)
		}
	}

	var out state
	out.next = -1
	if pending >= 0 && (s.items[pending].skipNl || s.items[pending].skipEmpty) {
		out.next = pending
	}
	for _, f := range stack {
		if f.region && f.endAt < 0 {
			g := f
			g.endAt, g.endTo = -1, -1
			out.stack = append(out.stack, g)
		}
	}
	if !collect {
		return out, nil
	}
	return out, h.coalesce(line)
}

// context is the item set and the highlight in force at the top of the stack.
func (h *Highlighter) context(stack []frame) (bitset, int) {
	if len(stack) == 0 {
		return h.syn.top, -1
	}
	top := stack[len(stack)-1]
	return top.contains, top.group
}

// frameEnd is where the innermost frame runs to on this line.
func (h *Highlighter) frameEnd(stack []frame, lineEnd int) int {
	if len(stack) == 0 {
		return lineEnd
	}
	if e := stack[len(stack)-1].endAt; e >= 0 && e < lineEnd {
		return e
	}
	return lineEnd
}

// usable reports whether a pattern can match on the line being walked.
//
// \%^ and \%$ are the start and the end of the buffer, and internal/regex turns
// them into \A and \z, which are the start and the end of whatever the matcher
// hands over -- one line. So the line number is the rest of the answer, and
// without it json.vim's jsonPadding, which is anchored to the top of the file,
// would match at the top of every line in it.
func (h *Highlighter) usable(p *pattern) bool {
	if p.bof && h.curLine != 1 {
		return false
	}
	if p.eof && h.curLine != h.lastLine {
		return false
	}
	return true
}

// best finds the item that wins at or after col.
//
// Reverse definition order with a strict comparison is rule 1 of
// :syn-priority: a later item is only displaced by one that matches strictly
// earlier, so a tie goes to the one defined last.
// stop is where the enclosing item ends on this line. Nothing may be found at
// or after it: an item that starts where its container ends is outside the
// container, and taking it anyway is how a `syn match` that ends mid-line ends
// up painting the rest of it.
func (h *Highlighter) best(line string, col, stop int, allowed bitset, stack []frame) (item, ms, me, hs, he int) {
	s := h.syn

	best := -1
	bestMS, bestME := stop, 0
	// Ascending id with a "not strictly later" test is rule 1: an item defined
	// later replaces one that matched at the same column, and only a strictly
	// earlier match displaces it the other way.
	allowed.each(func(id int) {
		if s.items[id].kind == kindKeyword {
			return
		}
		a, b, ok := h.nextOf(id, line, col)
		if !ok || a >= stop || a > bestMS || onStack(stack, id, a) || (a == b && h.isBlocked(id, a)) {
			return
		}
		best, bestMS, bestME = id, a, b
	})

	// A keyword beats a Match or a Region that starts where it does, and loses
	// to one that starts earlier. Both halves are rule 2 and rule 3 together,
	// and the keyword has to be looked for ahead of the column rather than at
	// it: a line whose only rule is a keyword six characters in has nothing
	// matching at the column at all.
	limit := stop
	if best >= 0 {
		limit = bestMS
	}
	if id, ks, ke, ok := h.nextKeyword(line, col, limit, stop, allowed); ok {
		return id, ks, ke, ks, ke
	}
	if best < 0 {
		return -1, 0, 0, 0, 0
	}

	it := s.items[best]
	pat := it.pat
	if it.kind == kindRegion {
		pat = h.startPattern(it, line, col, bestMS)
	}
	ms, me = bestMS, bestME
	hs, he = ms, me
	if pat != nil {
		ms, me, hs, he = resolveOffsets(pat, line, bestMS, bestME)
	}
	return best, ms, me, hs, he
}

// onStack reports whether an item is already open at the same column, which
// is the only way an item can contain itself.
func onStack(stack []frame, id, at int) bool {
	for _, f := range stack {
		if f.item == id && f.startAt == at {
			return true
		}
	}
	return false
}

// nextKeyword finds the first keyword at or after col that starts no later
// than limit, so that a keyword and a match compete on where they start.
func (h *Highlighter) nextKeyword(line string, col, limit, stop int, allowed bitset) (int, int, int, bool) {
	s := h.syn
	if len(s.keywords) == 0 {
		return 0, 0, 0, false
	}
	at := col
	// A column in the middle of a word is not a word start, and vim will not
	// find a keyword there either.
	if at > 0 && at < len(line) {
		r, _ := utf8.DecodeLastRuneInString(line[:at])
		if s.isKeyword(r) {
			for at < len(line) {
				r, w := utf8.DecodeRuneInString(line[at:])
				if !s.isKeyword(r) {
					break
				}
				at += w
			}
		}
	}
	for at <= limit && at < stop {
		r, w := utf8.DecodeRuneInString(line[at:])
		if !s.isKeyword(r) {
			at += w
			continue
		}
		id, end, ok := h.keywordAt(line, at, allowed)
		if ok && at <= limit && at < stop {
			return id, at, end, true
		}
		// Step over the whole word: no keyword starts inside one.
		for at < len(line) {
			r, w := utf8.DecodeRuneInString(line[at:])
			if !s.isKeyword(r) {
				break
			}
			at += w
		}
	}
	return 0, 0, 0, false
}

// startPattern finds which of a region's start patterns produced a match at
// ms, so that its offsets are the ones applied.
func (h *Highlighter) startPattern(it *item, line string, col, ms int) *pattern {
	for _, p := range it.starts {
		if a, _, ok := p.find(line, col); ok && a == ms {
			return p
		}
	}
	return nil
}

// resolveOffsets applies ms, me, hs and he to a raw match.
func resolveOffsets(p *pattern, line string, s, e int) (ms, me, hs, he int) {
	ms, me = s, e
	if v := apply(p.off.ms, line, s, e); v >= 0 {
		ms = v
	}
	if v := apply(p.off.me, line, s, e); v >= 0 {
		me = v
	}
	if p.off.lc > 0 {
		ms = step(line, s, p.off.lc)
	}
	hs, he = ms, me
	if v := apply(p.off.hs, line, s, e); v >= 0 {
		hs = v
	}
	if v := apply(p.off.he, line, s, e); v >= 0 {
		he = v
	}
	if me < ms {
		me = ms
	}
	if he < hs {
		he = hs
	}
	return ms, me, hs, he
}

// keywordAt looks the word starting at col up in the keyword table.
func (h *Highlighter) keywordAt(line string, col int, allowed bitset) (int, int, bool) {
	s := h.syn
	if len(s.keywords) == 0 || col >= len(line) {
		return 0, 0, false
	}
	if col > 0 {
		r, w := utf8.DecodeLastRuneInString(line[:col])
		_ = w
		if s.isKeyword(r) {
			return 0, 0, false
		}
	}
	end := col
	for end < len(line) {
		r, w := utf8.DecodeRuneInString(line[end:])
		if !s.isKeyword(r) {
			break
		}
		end += w
	}
	if end == col {
		return 0, 0, false
	}
	word := line[col:end]
	ids, ok := s.keywords[word]
	if !ok && s.anyIgnoreCas {
		ids, ok = s.keywords[lower(word)]
	}
	if !ok {
		return 0, 0, false
	}
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		if !allowed.has(id) {
			continue
		}
		it := s.items[id]
		if !it.ignoreCase && it.word != word {
			continue
		}
		return id, end, true
	}
	return 0, 0, false
}

// enter applies the winning item: paints it, and pushes a frame when there is
// anything to look for inside it.
func (h *Highlighter) enter(stack *[]frame, id int, line string, ms, me, hs, he, outer int, collect bool) int {
	s := h.syn
	it := s.items[id]

	group := it.groupID
	if it.transparent {
		group = outer
	}

	switch it.kind {
	case kindKeyword:
		if collect {
			h.fill(ms, me, group)
		}
		return me

	case kindMatch:
		if collect && hs < he {
			h.fill(hs, he, group)
		}
		if !s.inside[id].any() {
			return me
		}
		f := frame{item: id, group: group, contains: s.inside[id], endAt: me, endTo: me, startAt: ms}
		clampTo(*stack, &f)
		*stack = append(*stack, f)
		return ms

	default: // kindRegion
		f := frame{item: id, group: group, region: true, startAt: ms}
		if p := h.startPattern(it, line, ms, ms); p != nil {
			_, _, f.ext, _ = p.findExt(line, ms)
		}
		f.contains = s.inside[id]
		if it.transparent && !it.contains.set {
			f.contains = h.outerContains(*stack)
		}
		body := me
		if p := h.startPattern(it, line, ms, ms); p != nil {
			if v := apply(p.off.rs, line, ms, me); v >= 0 {
				body = v
			}
		}
		h.locateEnd(&f, line, body)
		if it.oneline && f.endAt < 0 {
			// A oneline region whose end is not on this line never started.
			// Painting the start match anyway is the one thing worse than not
			// painting it, so the whole match is dropped and the column moves
			// on by one.
			return h.forward(line, ms)
		}
		if collect {
			if hs < he {
				h.fill(hs, he, h.startGroup(it, group))
			}
			if body > he {
				h.fill(he, body, group)
			}
		}
		clampTo(*stack, &f)
		*stack = append(*stack, f)
		return body
	}
}

// clampTo holds a frame inside the one that encloses it.
//
// This is vim's `keepend`, applied to everything rather than only to the items
// that ask for it, and it is the one deliberate departure in the matcher. Vim
// lets a contained region that runs past its container push the container's end
// outward unless the container said keepend; here the inner one is cut short
// instead. The reason is not simplicity, it is that the alternative leaks: a
// region that never finds its end, opened inside a `syn match` that ends on the
// line it started, would otherwise sit on the stack for the rest of the buffer
// and paint everything below it. That is json.vim's jsonKeyword over a file
// whose last key has no colon, and it turns one malformed line into a file with
// no colour in it. A region cut short is a colour that stops early; a region
// that leaks is every colour after it wrong.
func clampTo(stack []frame, f *frame) {
	if len(stack) == 0 {
		return
	}
	outer := stack[len(stack)-1]
	if outer.endAt < 0 {
		return
	}
	if f.endAt < 0 || f.endAt > outer.endAt {
		f.endAt, f.endTo = outer.endAt, outer.endAt
		f.endHLFrom, f.endHLTo = 0, 0
	}
	if f.endTo > outer.endAt {
		f.endTo = outer.endAt
	}
}

// outerContains is what a transparent item with no contains of its own uses:
// the item set of whatever encloses it.
func (h *Highlighter) outerContains(stack []frame) bitset {
	if len(stack) == 0 {
		return h.syn.top
	}
	return stack[len(stack)-1].contains
}

// startGroup and endGroup are what a region's start and end matches are
// painted, which is `matchgroup` when it was given and the region itself
// otherwise.
func (h *Highlighter) startGroup(it *item, group int) int {
	if it.matchGroup == "" {
		return group
	}
	return h.syn.groupID(it.matchGroup)
}

func (h *Highlighter) endGroup(f *frame) int {
	it := h.syn.items[f.item]
	if it.matchGroup == "" {
		return f.group
	}
	return h.syn.groupID(it.matchGroup)
}

// locateEnd finds where a region closes on this line, honouring skip=.
func (h *Highlighter) locateEnd(f *frame, line string, from int) {
	it := h.syn.items[f.item]
	f.endAt, f.endTo = -1, -1

	ends := h.lineUsable(resolveAll(it.ends, f.ext))
	skips := h.lineUsable(resolveAll(it.skips, f.ext))

	pos := from
	for guard := 0; guard < 1000; guard++ {
		bs, be, ok := earliest(ends, line, pos)
		if !ok {
			return
		}
		if ss, se, sok := earliest(skips, line, pos); sok && ss < bs && se > ss {
			pos = se
			continue
		}
		p := endPattern(ends, line, pos, bs)
		ms, me, hs, he := bs, be, bs, be
		if p != nil {
			ms, me, hs, he = resolveOffsets(p, line, bs, be)
			if v := apply(p.off.re, line, bs, be); v >= 0 {
				ms = v
			}
		}
		f.endAt, f.endTo = ms, me
		f.endHLFrom, f.endHLTo = hs, he
		return
	}
}

// lineUsable drops the patterns in a list that cannot match on this line.
func (h *Highlighter) lineUsable(list []*pattern) []*pattern {
	keep := true
	for _, p := range list {
		if !h.usable(p) {
			keep = false
		}
	}
	if keep {
		return list
	}
	out := make([]*pattern, 0, len(list))
	for _, p := range list {
		if h.usable(p) {
			out = append(out, p)
		}
	}
	return out
}

// resolveAll compiles a region's skip and end patterns against what its start
// captured. A list with no \z1 in it comes back untouched.
func resolveAll(list []*pattern, ext string) []*pattern {
	needs := false
	for _, p := range list {
		if p.tmpl != "" {
			needs = true
		}
	}
	if !needs {
		return list
	}
	out := make([]*pattern, 0, len(list))
	for _, p := range list {
		if q := p.resolve(ext); q != nil {
			out = append(out, q)
		}
	}
	return out
}

// earliest returns the first match of any pattern in the list at or after from.
func earliest(list []*pattern, line string, from int) (int, int, bool) {
	bs, be, ok := 0, 0, false
	for _, p := range list {
		a, b, found := p.find(line, from)
		if !found || (ok && a >= bs) {
			continue
		}
		bs, be, ok = a, b, true
	}
	return bs, be, ok
}

// endPattern says which of a region's end patterns matched at bs.
func endPattern(ends []*pattern, line string, from, bs int) *pattern {
	for _, p := range ends {
		if a, _, ok := p.find(line, from); ok && a == bs {
			return p
		}
	}
	return nil
}

// arm sets the nextgroup pending after an item finished at col.
func (h *Highlighter) arm(id, col int) (int, int) {
	if !h.syn.items[id].nextGroup.set {
		return -1, 0
	}
	return id, col
}

// skipTo is where a nextgroup is allowed to start: straight after the item, or
// past the blanks when the item asked for skipwhite.
func (h *Highlighter) skipTo(line string, at, id int) int {
	if !h.syn.items[id].skipWhite {
		return at
	}
	for at < len(line) && (line[at] == ' ' || line[at] == '\t') {
		at++
	}
	return at
}

// forward steps one character.
func (h *Highlighter) forward(line string, at int) int {
	if at >= len(line) {
		return len(line)
	}
	_, w := utf8.DecodeRuneInString(line[at:])
	return at + w
}

// fill paints a byte range with a group.
func (h *Highlighter) fill(from, to, group int) {
	if from < 0 {
		from = 0
	}
	if to > len(h.paint) {
		to = len(h.paint)
	}
	for i := from; i < to; i++ {
		h.paint[i] = int32(group)
	}
}

// coalesce turns the per-byte paint into spans.
func (h *Highlighter) coalesce(line string) []Span {
	h.spans = h.spans[:0]
	i := 0
	for i < len(line) {
		g := h.paint[i]
		j := i
		for j < len(line) && h.paint[j] == g {
			j++
		}
		if g >= 0 {
			h.spans = append(h.spans, Span{Start: i, End: j, Group: int(g)})
		}
		i = j
	}
	return h.spans
}

// any reports whether a bitset holds anything.
func (b bitset) any() bool {
	for _, w := range b {
		if w != 0 {
			return true
		}
	}
	return false
}

// lower is strings.ToLower for a keyword, which is ASCII in every syntax file
// that asks for `syn case ignore`.
func lower(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + 32
		}
	}
	return string(out)
}
