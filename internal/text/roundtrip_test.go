package text

import (
	"bytes"
	"math/rand"
	"testing"
)

// This is the gate for internal/text: ten thousand random edit
// sequences applied and then undone to the root leave the buffer byte-identical
// to the file it was read from, for every corpus shape.
//
// It is worth being clear about why this one test is worth more than the rest
// of the file put together. Undo is the feature nobody exercises deliberately
// and everybody relies on absolutely, and the way it breaks is not a crash: it
// is a buffer that comes back almost right, one byte short at the end or with a
// line ending that changed, after an editing session nobody can reproduce. A
// property test over random edits is the only thing that finds that class of
// bug before a file does.
//
// The seed is fixed. A test that finds a different bug every run is a test
// nobody can bisect, and the fuzzing that goes hunting for new inputs belongs
// in the oracle harness, not here.
const (
	roundTripSequences = 10000
	roundTripSeed      = 20260902
)

// TestUndoRoundTrip is the gate.
func TestUndoRoundTrip(t *testing.T) {
	for _, name := range corpusFiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			roundTrip(t, readCorpus(t, name), roundTripSequences)
		})
	}
	t.Run("40k lines", func(t *testing.T) {
		t.Parallel()
		roundTrip(t, bigCorpus(), roundTripSequences)
	})
}

// roundTrip runs the property: edit, go home, compare.
//
// The buffer is built once and reused, because returning to the root is the
// property under test and doing it in a loop also builds a wide, shallow undo
// tree of ten thousand branches off one root, which is the shape a fresh buffer
// per iteration would never produce and the shape that breaks a tree walk that
// quietly assumed a stack.
func roundTrip(t *testing.T, data []byte, sequences int) {
	t.Helper()
	b := Read(data)
	if !sameAs(b, data) {
		t.Fatal("Read then Bytes already differs from the input")
	}
	rng := rand.New(rand.NewSource(roundTripSeed))

	for i := 0; i < sequences; i++ {
		edits := 1 + rng.Intn(6)
		for j := 0; j < edits; j++ {
			b.OpenUndoBlock(randomPos(rng, b))
			// More than one change per block, because one change per block is
			// the case where the entry ordering CloseUndoBlock and applyNode
			// exist to maintain cannot be wrong: reversing a one-element slice
			// is a no-op. Multi-entry blocks are what an insert-mode session,
			// ":g", "3J" and a ranged ":s" all produce, and with only
			// single-entry blocks in the gate both reverse() calls can be
			// deleted and ten thousand sequences over eight corpora say
			// nothing.
			for k := 1 + rng.Intn(4); k > 0; k-- {
				randomEdit(rng, b)
			}
			b.CloseUndoBlock()
		}
		tip := b.UndoSeq()

		// Every so often, wander around the tree before coming home. Redo and
		// the two time-travel walks have to leave the buffer in the same state
		// the straight undo path does, and if they do not, the difference shows
		// up as a failure of the property below rather than as nothing at all.
		//
		// Note what is NOT done here: "g-" all the way to the root. It walks
		// one sequence number at a time through every state ever created, so by
		// sequence 5,000 that is 17,000 steps and the test is quadratic in its
		// own iteration count. Jumping to a random older state exercises the
		// same code and crosses branches, which walking back never does.
		switch rng.Intn(4) {
		case 0:
			undoToRoot(t, b)
			redoToTip(t, b, tip)
		case 1:
			for k := 0; k < edits; k++ {
				if _, ok := b.Older(); !ok {
					break
				}
			}
			b.Newer()
		case 2:
			if _, ok := b.GotoSeq(0); !ok && tip != 0 {
				t.Fatalf("sequence %d: GotoSeq(0) failed from seq %d", i, tip)
			}
		case 3:
			if tip > 0 {
				// Almost always a node on a branch this sequence never
				// touched, which is the walk up to a common ancestor and back
				// down the other side.
				if _, ok := b.GotoSeq(rng.Intn(tip)); !ok {
					t.Fatalf("sequence %d: GotoSeq to an older state failed", i)
				}
			}
		}
		undoToRoot(t, b)

		if b.UndoSeq() != 0 {
			t.Fatalf("sequence %d: back at the root the sequence is %d", i, b.UndoSeq())
		}
		if !sameAs(b, data) {
			t.Fatalf("sequence %d: undone to the root, the buffer differs from the input\n got %q\nwant %q",
				i, truncate(b.Bytes()), truncate(data))
		}
	}
}

func undoToRoot(t *testing.T, b *Buffer) {
	t.Helper()
	for b.UndoSeq() > 0 {
		if _, ok := b.Undo(); !ok {
			t.Fatalf("Undo() stalled at seq %d", b.UndoSeq())
		}
	}
}

func redoToTip(t *testing.T, b *Buffer, tip int) {
	t.Helper()
	for b.UndoSeq() != tip {
		if _, ok := b.Redo(); !ok {
			t.Fatalf("Redo() stalled at seq %d on the way back to %d", b.UndoSeq(), tip)
		}
	}
}

// randomPos picks a position that exists, so that the undo block's remembered
// cursor is as varied as the edits are.
func randomPos(rng *rand.Rand, b *Buffer) Pos {
	n := 1 + rng.Intn(b.LineCount())
	l := b.Line(n)
	col := 0
	if len(l) > 0 {
		col = rng.Intn(len(l) + 1)
	}
	return Pos{Line: n, Col: col}
}

// editAlphabet is what random insertions are made of. The awkward members earn
// their place: a newline splits and joins lines, a tab moves every display
// column after it, a carriage return is the byte that decides whether a DOS
// file round-trips, and the last two are a wide rune and a combining mark whose
// bytes must never be split by a column calculation.
var editAlphabet = []string{
	"a", "b", "z", " ", "\t", "\n", "\r", "x\ny", "\t\t", "世", "é", "long piece of text",
}

// randomEdit makes one change through the public API, weighted towards the
// single-line edits that are what real editing mostly is.
func randomEdit(rng *rand.Rand, b *Buffer) {
	switch rng.Intn(10) {
	case 0:
		n := 1 + rng.Intn(b.LineCount())
		last := n + rng.Intn(3)
		b.DeleteLines(n, last)
	case 1:
		n := 1 + rng.Intn(b.LineCount()+1)
		b.InsertLines(n, [][]byte{[]byte(randomText(rng)), []byte(randomText(rng))})
	case 2:
		b.SetLine(1+rng.Intn(b.LineCount()), []byte(randomText(rng)))
	default:
		start := randomPos(rng, b)
		end := start
		// Mostly stay inside the line: that is both what an editor does and
		// what keeps the fuzz from turning every corpus into confetti by
		// sequence fifty.
		if rng.Intn(8) == 0 {
			end.Line += rng.Intn(3)
		}
		end.Col += rng.Intn(6)
		b.Replace(Range{Start: start, End: b.Clamp(end)}, []byte(randomText(rng)))
	}
}

// randomText builds a short insertion out of the alphabet, sometimes empty so
// that pure deletions get their turn.
func randomText(rng *rand.Rand) string {
	n := rng.Intn(4)
	s := ""
	for i := 0; i < n; i++ {
		s += editAlphabet[rng.Intn(len(editAlphabet))]
	}
	return s
}

// sameAs compares the buffer to a file's bytes without building the file, so
// that ten thousand comparisons against a 40,000-line corpus do not spend all
// their time in the allocator.
func sameAs(b *Buffer, data []byte) bool {
	if b.emptied {
		// vim's ML_EMPTY, which Bytes renders as no bytes at all: a buffer
		// every line of which was deleted, and a file that was empty when it
		// was read.
		return len(data) == 0
	}
	eol := "\n"
	if b.format == DOS {
		eol = "\r\n"
	}
	at := 0
	for i, l := range b.lines {
		if at+len(l) > len(data) || !bytes.Equal(data[at:at+len(l)], l) {
			return false
		}
		at += len(l)
		if i < len(b.lines)-1 || !b.noEOL {
			if at+len(eol) > len(data) || string(data[at:at+len(eol)]) != eol {
				return false
			}
			at += len(eol)
		}
	}
	return at == len(data)
}

// truncate keeps a failure message readable when the corpus is 40,000 lines.
func truncate(b []byte) []byte {
	if len(b) > 200 {
		return b[:200]
	}
	return b
}
