package quickfix

import (
	"strings"
	"testing"
)

// TestStackDepthIsVims. Ten lists, because that is vim's ":colder" limit and
// because a shallower stack loses the ":grep" you ran before the ":make" that
// overwrote it.
func TestStackDepthIsVims(t *testing.T) {
	if MaxDepth != 10 {
		t.Errorf("MaxDepth is %d; vim keeps 10", MaxDepth)
	}
}

// TestCurrentOnAnEmptyStack answers nil rather than panicking, because every
// ":c" command asks before it knows whether anything has filled a list.
func TestCurrentOnAnEmptyStack(t *testing.T) {
	var s *Stack
	if s.Current() != nil {
		t.Error("a nil stack has a current list")
	}
	if (&Stack{}).Current() != nil {
		t.Error("an empty stack has a current list")
	}
	s2 := &Stack{Lists: []*List{{Title: "make"}}, Cur: 0}
	if got := s2.Current(); got == nil || got.Title != "make" {
		t.Errorf("Current() = %v, want the one list on the stack", got)
	}
}

// TestErrorCodes pins the two codes the :c commands answer with, because the
// code is what a habit keys off and the sentence after it is not.
func TestErrorCodes(t *testing.T) {
	if !strings.HasPrefix(ErrNoList.Error(), "E42:") {
		t.Errorf("ErrNoList is %q; vim says E42", ErrNoList)
	}
	if !strings.HasPrefix(ErrNoMore.Error(), "E553:") {
		t.Errorf("ErrNoMore is %q; vim says E553", ErrNoMore)
	}
}

// TestCompileReturnsAFormat is a shape check and nothing more: the parser is a
// stub, and this holds the signature callers are being written against so that
// a change to it fails here rather than in five packages at once.
func TestCompileReturnsAFormat(t *testing.T) {
	f, err := Compile("%f:%l:%c: %m")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if f == nil {
		t.Fatal("Compile returned nil with no error")
	}
	if _, err := f.Parse([]string{"main.go:3:5: undefined: foo"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}
