# pvim

vim in Go. one binary, no cgo, no .app bundle.

keys, modes, ex commands, registers, options and the screen are vim's. no
vimscript interpreter beyond what a vimrc uses, and the plugins are replaced by
the parts of them I actually use: ctrlp, nerdtree, fugitive, vim-go.

## build

    make build      bin/pvim
    make check      vet, test, static-check
    make linux      dist/pvim-linux-amd64, a real static ELF

go 1.26. four deps, all pure go: purego, x/image, x/sys, xgb.

## static

not what it means elsewhere on macos. there is no static libSystem, every darwin
binary is dyld-loaded against it whatever CGO_ENABLED says, and
-extldflags=-static errors out. so it is an exit code and not a word:

    make static-check

red if any package in the graph carries cgo files, if otool -L lists anything
but libSystem and libresolv, if the build info does not say CGO_ENABLED=0, or if
a .dylib turns up beside the binary. a check that cannot run is red, not green.

## the oracle

behaviour is settled by running vim, not by reading it:

    make oracle                                          vim against vim
    make oracle CANDIDATE=bin/pvim CANDIDATE_KIND=pvim   pvim against vim
    make fuzz                                            generated scripts

863 cases under two option profiles, no unregistered difference. a case is a
keystroke file; the harness diffs the buffer, the registers, the marks, the undo
sequence and vim's message line. a deliberate difference goes in the register
with a reason, an unregistered one fails the run.

syntax reads vim's own syntax files and is graded against synID() per position.
diff mode is graded against vim -d. the finder, the tree and the git commands
have no oracle, so they are read out of the plugin source and the judgment calls
say so in the code.

## not built

vimscript functions past the ten a vimrc calls, filetype indent scripts, z=
suggestions, netrw, tree-sitter, wayland.
