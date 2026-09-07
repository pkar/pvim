package clip

import (
	"sync"

	"github.com/pkar/pvim/internal/register"
)

// Memory is a clipboard in a variable: no platform, no thread rules, no window
// server.
//
// It is what every test above this package uses, and it is not only a
// convenience. 'clipboard' in the vimrc is "unnamed,unnamedplus,autoselect",
// which makes the clipboard part of what ordinary keys do -- a bare p reads it,
// a bare y writes it, and every cursor motion in visual mode writes it -- so a
// test of the mode machine that has no clipboard at all is testing a different
// editor from the one that runs on this desktop. Handing it a Memory and
// counting the writes is testing this one.
//
// It is safe for concurrent use because a frontend may read it from a goroutine
// that is not the editor's; the real implementations have the same property for
// the same reason.
type Memory struct {
	mu sync.Mutex
	v  register.Value

	// reads and writes count the calls, which is the whole point of this type
	// in a test of 'autoselect': the question is not what ended up on the
	// clipboard, it is how many times the editor put it there.
	reads  int
	writes int

	// ReadErr and WriteErr, when set, are returned instead of doing the thing.
	// A clipboard that fails is a real case -- the pasteboard is another
	// process and it can be busy -- and the editor has to put the error on the
	// message line rather than lose the yank.
	ReadErr  error
	WriteErr error
}

// Read returns what was last written, or the zero value.
func (m *Memory) Read() (register.Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.ReadErr != nil {
		return register.Value{}, m.ReadErr
	}
	return m.v, nil
}

// Write replaces the contents.
func (m *Memory) Write(v register.Value) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes++
	if m.WriteErr != nil {
		return m.WriteErr
	}
	m.v = v
	return nil
}

// Reads and Writes are how many calls have been made. A test of 'autoselect'
// asserts on Writes: one per cursor motion in visual mode is the behaviour, and
// nought or two is a bug in the mode layer that no buffer comparison can see.
func (m *Memory) Reads() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reads
}

func (m *Memory) Writes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}

// Text is what the clipboard holds, flattened the way a pasteboard would hold
// it. It is here so a test can say what it means without building a Value.
func (m *Memory) Text() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(Bytes(m.v))
}

// SetText puts flat text on the clipboard as if another application had, which
// is the case the round trip cannot otherwise reach: a value that arrives from
// outside has been through ValueOf and has only ever been charwise or linewise.
func (m *Memory) SetText(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.v = ValueOf([]byte(s))
}
