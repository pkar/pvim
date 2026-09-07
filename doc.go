// Package pvim is the root of a vim-compatible editor written in Go with no C
// compiler anywhere in it.
//
// # What this is
//
// One module, one binary, no .app bundle, no dylib beside it. "Static" on macOS
// is a word Apple has made impossible to mean literally: there is no static
// libSystem, every darwin binary is dyld-loaded against
// /usr/lib/libSystem.B.dylib whatever CGO_ENABLED says, and -extldflags=-static
// simply errors. So this project means something narrower and checkable by it:
// CGO_ENABLED=0, no package in the dependency graph with a CgoFiles list,
// otool -L listing libSystem and libresolv and nothing else, and no library
// sitting beside the binary for it to need at run time.
// `make static-check` is those four checks, all of which fail loudly, and it
// runs in `make check` beside vet and test so a dependency that grows a cgo
// file breaks the build the day it lands.
//
// # The subset
//
// The keys, modes, ex commands, registers, options and screen are vim's,
// exactly, because /opt/homebrew/bin/vim runs headless and is the oracle that
// says so. Underneath that line this is not a port. There is no vimscript
// interpreter: the config language is the subset ~/.vimrc actually uses, which
// is set, let, the map family, autocmd, augroup, command!, if over has(),
// colorscheme and hi, and anything else is an E-code on the message line. There
// is no syntax highlighting, no filetype indent, no backtracking regex engine
// (vim's dialect is translated to Go's RE2 and the six atoms RE2 cannot express
// are refused by name), and the plugins the vimrc loads are replaced by the
// five or six things in each that get used, in Go.
//
// # Dependency direction
//
// Strict, and go vet cannot enforce it, so internal/deps_test.go does by
// shelling out to go list:
//
// - internal/text and internal/regex import nothing else in this module.
// - internal/mode and internal/ex import those; internal/screen imports mode.
// - internal/tui and internal/raster import screen; internal/gui imports
// raster. internal/screen imports none of the three.
// - internal/tui and internal/gui are peers and neither knows the other
// exists.
// - purego is imported only under internal/gui and internal/clip. The editor
// has to run and be tested with no window at all, on a box with no display,
// or the GUI risk leaks into everything.
// - internal/clip imports internal/register, whose Clipboard interface it
// fills, and never internal/gui. It calls NSPasteboard itself and takes the
// thread that call has to happen on as a func value, so a package that
// wants a clipboard does not get the window with it.
// - internal/server imports nothing in this module at all. It is a socket and
// a line of JSON; the running editor is on the far side of a Handler func.
// - the standard library's regexp is imported only by internal/regex and by
// test files.
package pvim
