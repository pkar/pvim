package text

import "testing"

// Every case here is a vim session run through
//
//	vim --clean -i NONE --not-a-term -s keys file
//
// ending in :call writefile([string(getpos("'a"))],"state.txt"), on the file
// "one/two/three four five/six/seven". A mark that survives an edit it should
// not have survived is worse than one that is lost: it silently points at
// whatever text moved into the place it used to be.
func TestMarkAdjustment(t *testing.T) {
	const corpus = "one\ntwo\nthree four five\nsix\nseven\n"

	cases := []struct {
		name   string
		at     Pos
		change func(b *Buffer)
		want   Pos
		gone   bool
	}{
		{
			// 3Gmadd: vim reports [0, 0, 0, 0], the mark is gone.
			name:   "the line the mark is on is deleted",
			at:     Pos{3, 0},
			change: func(b *Buffer) { b.DeleteLines(3, 3) },
			gone:   true,
		},
		{
			// 4Gma then 2Gdd: vim reports [0, 3, 1, 0].
			name:   "a line above is deleted",
			at:     Pos{4, 0},
			change: func(b *Buffer) { b.DeleteLines(2, 2) },
			want:   Pos{3, 0},
		},
		{
			// 4G2lma then 4GOnew<Esc>: vim reports [0, 5, 3, 0], line down one
			// and the column untouched.
			name:   "a line is inserted above",
			at:     Pos{4, 2},
			change: func(b *Buffer) { b.InsertLines(4, [][]byte{[]byte("new")}) },
			want:   Pos{5, 2},
		},
		{
			name:   "a line is inserted below",
			at:     Pos{2, 1},
			change: func(b *Buffer) { b.InsertLines(3, [][]byte{[]byte("new")}) },
			want:   Pos{2, 1},
		},
		{
			name:   "the line is rewritten in place",
			at:     Pos{3, 6},
			change: func(b *Buffer) { b.SetLine(3, []byte("THREE FOUR FIVE")) },
			want:   Pos{3, 6},
		},
		{
			name:   "lines below are deleted",
			at:     Pos{2, 1},
			change: func(b *Buffer) { b.DeleteLines(4, 5) },
			want:   Pos{2, 1},
		},
		{
			name:   "a line above is split in two",
			at:     Pos{4, 1},
			change: func(b *Buffer) { b.Insert(Pos{2, 1}, []byte("\n")) },
			want:   Pos{5, 1},
		},
		{
			name:   "two lines above are joined",
			at:     Pos{4, 1},
			change: func(b *Buffer) { b.Replace(Range{Pos{1, 3}, Pos{2, 0}}, nil) },
			want:   Pos{3, 1},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read([]byte(corpus))
			b.SetMark('a', c.at)
			c.change(b)

			got, ok := b.Mark('a')
			if c.gone {
				if ok {
					t.Errorf("mark a survived at %v; vim clears it", got)
				}
				return
			}
			if !ok {
				t.Fatal("mark a was cleared")
			}
			if got != c.want {
				t.Errorf("mark a = %v, want %v", got, c.want)
			}
		})
	}
}

// An undo has to put the marks back where the change moved them from, or "u"
// followed by "'a" lands somewhere the text never was.
func TestMarksMoveBackOnUndo(t *testing.T) {
	b := Read([]byte("one\ntwo\nthree\nfour\n"))
	b.SetMark('a', Pos{4, 2})

	b.OpenUndoBlock(Pos{2, 0})
	b.DeleteLines(2, 2)
	b.CloseUndoBlock()
	if got, want := mustMark(t, b, 'a'), (Pos{3, 2}); got != want {
		t.Fatalf("after the delete mark a = %v, want %v", got, want)
	}

	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if got, want := mustMark(t, b, 'a'), (Pos{4, 2}); got != want {
		t.Errorf("after the undo mark a = %v, want %v", got, want)
	}
}

// The marks a change sets on its own. These were read out of vim with
// getpos("'[") and getpos("'.") after the keys named in each comment.
func TestAutomaticMarks(t *testing.T) {
	t.Run("dd sets both change marks to the same place", func(t *testing.T) {
		// 2Gdd: vim reports '[ = [0, 2, 1, 0] and '] = [0, 2, 1, 0].
		b := Read([]byte("one\ntwo\nthree\n"))
		b.DeleteLines(2, 2)
		if got, want := mustMark(t, b, MarkChangeStart), (Pos{2, 0}); got != want {
			t.Errorf("'[ = %v, want %v", got, want)
		}
		if got, want := mustMark(t, b, MarkChangeEnd), (Pos{2, 0}); got != want {
			t.Errorf("'] = %v, want %v", got, want)
		}
	})

	t.Run("an insert sets the last-change mark to its start", func(t *testing.T) {
		// 2GAzz<Esc>: vim reports '. = [0, 2, 4, 0], byte column 3, which is
		// where the inserted text starts and not where it ends.
		b := Read([]byte("one\ntwo\nthree\n"))
		b.Insert(Pos{2, 3}, []byte("zz"))
		if got, want := mustMark(t, b, MarkLastChange), (Pos{2, 3}); got != want {
			t.Errorf("'. = %v, want %v", got, want)
		}
		if got, want := mustMark(t, b, MarkChangeEnd), (Pos{2, 4}); got != want {
			t.Errorf("'] = %v, want %v", got, want)
		}
	})
}

// The names this buffer accepts. A-Z are global marks: in vim they name a file
// as well as a place, but a one-file session still stores and jumps to them, so
// this buffer stores them too. Measured, because the comment this replaces
// claimed the opposite and was written before internal/motion implemented them:
//
//	printf 'GmAgg'\''A' | vim --clean -i NONE --not-a-term -s - three-line-file
//
// leaves the cursor at [0, 3, 1, 0], the line mA was typed on. What is still
// missing is the FILE half of a global mark, which needs more than one buffer;
// internal/motion/mark.go:16 carries that note.
func TestMarkNames(t *testing.T) {
	b := Read([]byte("one\n"))
	for _, name := range []byte{'a', 'z', 'A', 'Z', '[', ']', '.', '^', '\''} {
		b.SetMark(name, Pos{1, 1})
		if _, ok := b.Mark(name); !ok {
			t.Errorf("mark %q was not stored", name)
		}
	}
	for _, name := range []byte{'0', '<', '>', ' '} {
		b.SetMark(name, Pos{1, 1})
		if _, ok := b.Mark(name); ok {
			t.Errorf("mark %q was stored and should not be", name)
		}
	}
	b.ClearMark('a')
	if _, ok := b.Mark('a'); ok {
		t.Error("ClearMark left the mark in place")
	}
}

// The changelist gets one entry per undo block, not one per change, and that is
// measurable: three "x" commands through "vim -s", which never syncs undo,
// leave getchangelist() reporting a single entry at the last of the three.
func TestChangeList(t *testing.T) {
	t.Run("one block is one entry", func(t *testing.T) {
		b := Read([]byte("l1\nl2\nl3\nl4\nl5\n"))
		b.OpenUndoBlock(Pos{1, 0})
		b.Delete(Range{Pos{1, 0}, Pos{1, 1}})
		b.Delete(Range{Pos{3, 0}, Pos{3, 1}})
		b.Delete(Range{Pos{5, 0}, Pos{5, 1}})
		b.CloseUndoBlock()

		got := b.ChangeList()
		if len(got) != 1 {
			t.Fatalf("ChangeList() = %v, want one entry", got)
		}
		if want := (Pos{5, 0}); got[0] != want {
			t.Errorf("entry = %v, want %v, the last change in the block", got[0], want)
		}
	})

	t.Run("three blocks are three entries", func(t *testing.T) {
		b := Read([]byte("l1\nl2\nl3\nl4\nl5\n"))
		for _, n := range []int{1, 3, 5} {
			b.OpenUndoBlock(Pos{n, 0})
			b.Delete(Range{Pos{n, 0}, Pos{n, 1}})
			b.CloseUndoBlock()
		}
		got := b.ChangeList()
		want := []Pos{{1, 0}, {3, 0}, {5, 0}}
		if len(got) != len(want) {
			t.Fatalf("ChangeList() = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %d = %v, want %v", i, got[i], want[i])
			}
		}

		// g; walks back from the newest, g, forward again.
		for _, want := range []Pos{{5, 0}, {3, 0}, {1, 0}} {
			p, ok := b.ChangeOlder()
			if !ok {
				t.Fatal("ChangeOlder() ran out early")
			}
			if p != want {
				t.Errorf("ChangeOlder() = %v, want %v", p, want)
			}
		}
		if _, ok := b.ChangeOlder(); ok {
			t.Error("ChangeOlder() past the oldest reported success")
		}
		for _, want := range []Pos{{3, 0}, {5, 0}} {
			p, ok := b.ChangeNewer()
			if !ok {
				t.Fatal("ChangeNewer() ran out early")
			}
			if p != want {
				t.Errorf("ChangeNewer() = %v, want %v", p, want)
			}
		}
		if _, ok := b.ChangeNewer(); ok {
			t.Error("ChangeNewer() past the newest reported success")
		}
	})

	t.Run("the list is capped", func(t *testing.T) {
		// One change per line, because two changes to the same line within a
		// line's width of each other share an entry however many undo blocks
		// they are spread over: that is what stops "xxxxx" filling the list
		// and it is why this fills it a line at a time.
		var lines []byte
		for i := 0; i < changeListMax+40; i++ {
			lines = append(lines, 'x', '\n')
		}
		b := Read(lines)
		for i := 1; i <= changeListMax+40; i++ {
			b.OpenUndoBlock(Pos{i, 0})
			b.Insert(Pos{i, 0}, []byte("y"))
			b.CloseUndoBlock()
		}
		if got := len(b.ChangeList()); got != changeListMax {
			t.Errorf("ChangeList() has %d entries, want %d", got, changeListMax)
		}
	})
}

func mustMark(t *testing.T, b *Buffer, name byte) Pos {
	t.Helper()
	p, ok := b.Mark(name)
	if !ok {
		t.Fatalf("mark %q is not set", name)
	}
	return p
}

// Undo has to put '[ '] and '. back, not drop them.
//
// The naive path is to let adjustMarks move them with everything else, and
// adjustMarks deletes any mark standing on a line the undo replaced. Undo an
// insertion that way and `[ and `] are E20 for the rest of the session. Vim's
// u_undoredo() instead widens b_op_start and b_op_end over every entry as it
// walks them and lets changed_lines() set b_last_change, so all three come out
// pointing at the text the undo put back.
//
// Every want below is vim 9.2.0321, run as
//
//	vim --clean -n -i NONE --not-a-term -s keys f.txt
//
// reporting getpos("'["), getpos("']") and getpos("'."), converted from vim's
// 1-based columns to this package's 0-based ones. Note that '. is not clamped
// to the buffer: vim leaves it past the end after undoing an append at the end
// of the file and so does this.
func TestUndoRestoresChangeMarks(t *testing.T) {
	const three = "one\ntwo\nthree\n"

	cases := []struct {
		name  string
		keys  string
		start string
		// change is the edit those keys make, run inside one undo block.
		change func(b *Buffer)
		// redo runs C-r after the undo when set, and the wants are the marks
		// after that instead.
		redo                        bool
		wantStart, wantEnd, wantDot Pos
	}{
		{
			name:   "undo a line put",
			keys:   "1Gyyp u",
			start:  three,
			change: func(b *Buffer) { b.InsertLines(2, [][]byte{[]byte("one")}) },
			// vim: '[ 2,1 '] 2,1 '. 2,1
			wantStart: Pos{2, 0}, wantEnd: Pos{2, 0}, wantDot: Pos{2, 0},
		},
		{
			name:      "undo a line open",
			keys:      "1GOnew<Esc> u",
			start:     three,
			change:    func(b *Buffer) { b.InsertLines(1, [][]byte{[]byte("new")}) },
			wantStart: Pos{1, 0}, wantEnd: Pos{1, 0}, wantDot: Pos{1, 0},
		},
		{
			name:   "undo an append past the last line",
			keys:   "2Goadded<Esc> u",
			start:  "one\ntwo\n",
			change: func(b *Buffer) { b.InsertLines(3, [][]byte{[]byte("added")}) },
			// '[ and '] are clamped to the two lines left, '. is not.
			wantStart: Pos{2, 0}, wantEnd: Pos{2, 0}, wantDot: Pos{3, 0},
		},
		{
			name:      "undo a middle line delete",
			keys:      "2Gdd u",
			start:     three,
			change:    func(b *Buffer) { b.DeleteLines(2, 2) },
			wantStart: Pos{2, 0}, wantEnd: Pos{2, 0}, wantDot: Pos{2, 0},
		},
		{
			name:      "undo the first line delete",
			keys:      "1Gdd u",
			start:     three,
			change:    func(b *Buffer) { b.DeleteLines(1, 1) },
			wantStart: Pos{1, 0}, wantEnd: Pos{1, 0}, wantDot: Pos{1, 0},
		},
		{
			name:      "undo the last line delete",
			keys:      "3Gdd u",
			start:     three,
			change:    func(b *Buffer) { b.DeleteLines(3, 3) },
			wantStart: Pos{2, 0}, wantEnd: Pos{3, 0}, wantDot: Pos{3, 0},
		},
		{
			name:      "undo a join",
			keys:      "1GJ u",
			start:     three,
			change:    func(b *Buffer) { b.Replace(Range{Pos{1, 3}, Pos{2, 0}}, []byte(" ")) },
			wantStart: Pos{1, 0}, wantEnd: Pos{2, 0}, wantDot: Pos{1, 0},
		},
		{
			name:      "undo a change inside one line",
			keys:      "1Gwdw u",
			start:     "hello world\nsecond\n",
			change:    func(b *Buffer) { b.Delete(Range{Pos{1, 6}, Pos{1, 11}}) },
			wantStart: Pos{1, 0}, wantEnd: Pos{1, 0}, wantDot: Pos{1, 0},
		},
		{
			name:  "undo a block holding two changes",
			keys:  ":1d :2s/C/Z/ u",
			start: "A\nB\nC\nD\n",
			change: func(b *Buffer) {
				b.DeleteLines(1, 1)
				b.SetLine(2, []byte("Z"))
			},
			wantStart: Pos{1, 0}, wantEnd: Pos{3, 0}, wantDot: Pos{1, 0},
		},
		{
			name:      "redo sets them too",
			keys:      "2Gdd u C-r",
			start:     three,
			change:    func(b *Buffer) { b.DeleteLines(2, 2) },
			redo:      true,
			wantStart: Pos{2, 0}, wantEnd: Pos{2, 0}, wantDot: Pos{2, 0},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read([]byte(c.start))
			b.OpenUndoBlock(Pos{1, 0})
			c.change(b)
			b.CloseUndoBlock()
			if _, ok := b.Undo(); !ok {
				t.Fatal("Undo() found nothing to undo")
			}
			if c.redo {
				if _, ok := b.Redo(); !ok {
					t.Fatal("Redo() found nothing to redo")
				}
			}
			for _, m := range []struct {
				name byte
				want Pos
			}{
				{MarkChangeStart, c.wantStart},
				{MarkChangeEnd, c.wantEnd},
				{MarkLastChange, c.wantDot},
			} {
				got, ok := b.Mark(m.name)
				if !ok {
					t.Errorf("after %q mark '%c is unset, vim has it at %v", c.keys, m.name, m.want)
					continue
				}
				if got != m.want {
					t.Errorf("after %q mark '%c = %v, vim = %v", c.keys, m.name, got, m.want)
				}
			}
		})
	}
}
