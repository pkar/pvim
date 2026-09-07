//go:build !unix

package server

import (
	"fmt"
	"os"
)

// ownedByCaller has no answer off unix, so this package refuses to bind or
// dial there rather than skipping the check.
//
// The build tag is not hypothetical bookkeeping: the static gate builds
// this tree for GOOS=linux as well as darwin, both of which are unix, and a
// third platform arriving should fail loudly here rather than silently running
// without the check the file above exists for.
func ownedByCaller(name string, fi os.FileInfo) error {
	return fmt.Errorf("server: cannot tell who owns %s on this platform, refusing to use a socket there", name)
}
