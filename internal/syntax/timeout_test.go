package syntax

import "time"

// timeout is the deadline the two termination tests wait on. It is a helper
// rather than a literal so that both say what they are waiting for and neither
// says how long.
func timeout() <-chan time.Time { return time.After(10 * time.Second) }
