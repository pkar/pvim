BINARY := pvim
PKG    := ./cmd/$(BINARY)
BIN    := bin/$(BINARY)
LINUXBIN := dist/$(BINARY)-linux-amd64
# The oracle is built and then run rather than `go run`. Into dist/ because bin/
# is what `make install` ships from and a test harness is not part of the
# program; check 4 of the static gate has no opinion either way, it looks for a
# library sidecar and not for a headcount.
ORACLEBIN ?= dist/oracle
DEST   ?= /usr/local/bin/$(BINARY)
# The two external tools the static gate shells out to, named so a test can
# hand these targets a go that fails and an otool that is not installed and
# watch them go red. Nothing else overrides them.
GO     ?= go
GOFMT  ?= gofmt
OTOOL  ?= otool
# The keystroke cases and the fuzz corpus the oracle runs against vim.
KEYS   ?= testdata/keys
CORPUS ?= testdata/fuzz
# The reference, and the candidate diffed against it. The candidate defaults to
# vim itself, which makes `make oracle` the harness self-test: same cases, same
# appended trailer, same three artifacts compared, both sides real vim, so
# anything it reports is a bug in the harness and not in an editor. Grading the
# editor is the same command with
# `CANDIDATE=bin/pvim CANDIDATE_KIND=pvim` on it.
VIM            ?= /opt/homebrew/bin/vim
CANDIDATE      ?= $(VIM)
CANDIDATE_KIND ?= vim
# Scripts per fuzz run. The plan's gate is 10,000 clean, three runs in a row.
N      ?= 10000
# Per package binary, and see the note on the test target for the measurement
# behind it.
TIMEOUT ?= 30m

.DEFAULT_GOAL := help

.PHONY: help build test vet fmt-check check check-fast static-check oracle fuzz run linux install clean

##@ General

help: ## show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nusage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		END { printf "\nvars: DEST=%s KEYS=%s CORPUS=%s N=%s TIMEOUT=%s CANDIDATE=%s CANDIDATE_KIND=%s GO=%s OTOOL=%s\n\n", "$(DEST)", "$(KEYS)", "$(CORPUS)", "$(N)", "$(TIMEOUT)", "$(CANDIDATE)", "$(CANDIDATE_KIND)", "$(GO)", "$(OTOOL)" }' \
		$(MAKEFILE_LIST)

##@ Develop

build: ## build one file into bin/, no cgo
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN) $(PKG)

# -timeout and not go test's default, which is 10m per package binary. cmd/oracle
# takes about 557s on an idle machine with internal/motion at 307s beside it,
# and 43 seconds of margin is not margin: a slower box, or a loaded one, goes
# past 600s and go test kills the binary with
# "panic: test timed out after 10m0s" and a goroutine dump, which reads as a
# broken harness rather than a slow machine. Both slow packages shell out to
# /opt/homebrew/bin/vim thousands of times, so the number to beat is a machine's
# and not this code's.
test: ## run the tests
	$(GO) test -timeout $(TIMEOUT) ./...

vet: ## go vet
	$(GO) vet ./...

fmt-check: ## fail if anything is not gofmt clean
	@out=$$($(GOFMT) -l .); \
		if [ -n "$$out" ]; then echo "not gofmt clean:"; echo "$$out"; exit 1; fi

# The iteration gate. `make check` is about six minutes, nearly all of it two
# packages shelling out to /opt/homebrew/bin/vim thousands of times:
# internal/motion sweeps every motion from every position of eight corpus files,
# and cmd/oracle runs the whole keystroke suite through vim and through pvim.
# Both of those are behind testing.Short(), so this target is the same tests
# minus the reference, and it is the one to run in a loop while writing code.
#
# It is not a substitute for `make check` and nothing here should pretend it is:
# -short skips the differential half, which is the half that says whether this
# is vim. Run it while iterating, run `make check` before reporting.
check-fast: fmt-check vet ## gofmt, vet and the tests that do not shell out to vim
	$(GO) test -short ./...

check: fmt-check vet test static-check ## the whole gate: gofmt, vet, every test and the static checks

run: build ## build and run, ARGS=<files>
	$(BIN) $(ARGS)

##@ Static

# "Static" on macOS is not what it sounds like: Apple ships no static libSystem
# and every darwin binary is dyld-loaded against it whatever CGO_ENABLED says.
# The claim this project makes instead is these four checks, and they are checks
# and not echoes: each one exits non-zero on its own.
static-check: build ## the four checks that make "static" an exit code
	@echo "== 1/4 no package in the graph carries cgo files =="
	@# Deliberately measured with cgo ENABLED. At CGO_ENABLED=0 every package
	@# reports an empty CgoFiles list, so asking the question there answers
	@# itself. runtime/cgo is the one allowed entry: purego has a cgo.go behind
	@# a //go:build cgo tag that the CGO_ENABLED=0 build never sees. go list is
	@# captured before it is filtered because a pipeline ending in `|| true`
	@# exits 0 whatever go list did, and a go list that cannot resolve the
	@# module then reads here as "no cgo in the graph".
	@set -e; \
		all=$$(CGO_ENABLED=1 $(GO) list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' $(PKG)); \
		out=$$(printf '%s\n' "$$all" | grep -v '^runtime/cgo$$' || true); \
		if [ -n "$$out" ]; then echo "cgo files in the graph:"; echo "$$out"; exit 1; fi
	@echo "== 2/4 otool -L lists only the two libraries every Go binary gets =="
	@# libSystem is the one Apple makes unavoidable: there is no static libSystem
	@# and darwin syscalls go through it whatever CGO_ENABLED says. libresolv
	@# comes with `os`, which dynamically imports res_search, and no Go program
	@# that opens a file is without it. Nothing else may appear, and that is the
	@# check with teeth: AppKit, CoreGraphics and CoreText are dlopened by purego
	@# at runtime, so if a framework ever turns up on this line, a C toolchain
	@# got into the link and the whole no-cgo claim is gone. Same capture-then-
	@# filter as check 1, and otool is looked up before it is run: `build` is a
	@# CGO_ENABLED=0 build, so a box with Go and no Xcode command line tools
	@# compiles pvim happily, has no otool, and used to print the check with
	@# teeth green without ever running it.
	@set -e; \
		command -v $(OTOOL) >/dev/null 2>&1 \
			|| { echo "$(OTOOL) not found; the library check cannot run"; exit 1; }; \
		all=$$($(OTOOL) -L $(BIN)); \
		out=$$(printf '%s\n' "$$all" | tail -n +2 \
			| grep -v '/usr/lib/libSystem\.B\.dylib' \
			| grep -v '/usr/lib/libresolv\.9\.dylib' || true); \
		if [ -n "$$out" ]; then echo "unexpected libraries:"; echo "$$out"; exit 1; fi
	@echo "== 3/4 the build info says CGO_ENABLED=0 =="
	@set -e; \
		$(GO) version -m $(BIN) | grep -q 'CGO_ENABLED=0' \
		|| { echo "build info does not say CGO_ENABLED=0:"; $(GO) version -m $(BIN) | grep -i cgo; exit 1; }
	@echo "== 4/4 the binary ships alone: no library beside it =="
	@# What this has teeth for is a sidecar: a .dylib, .so or .framework that the
	@# binary needs at run time, which would make "one file" false however the
	@# link was done. It used to be a headcount of bin/ instead, demanding one
	@# entry and nothing else, which made the gate depend on which target ran
	@# last: anything that put a second binary there, a cross build or a harness,
	@# failed a check on a tree with nothing wrong with it. A second Go binary is
	@# a tool, not a sidecar.
	@set -e; \
		test -f $(BIN) || { echo "$(BIN) is not a regular file"; exit 1; }; \
		side=$$(ls -1 bin | grep -E '\.(dylib|so|a|framework)$$' || true); \
		if [ -n "$$side" ]; then echo "libraries beside the binary:"; echo "$$side"; exit 1; fi
	@echo "static-check: green"

##@ Oracle

# Built and then run, never `go run`. go run reports any non-zero exit of the
# program it ran as its own 1, and the oracle's exit codes are its whole
# interface: 4 is a difference nobody registered, 5 is a side that wrote no
# artifact at all, 1 is the harness itself falling over, and through go run all
# three arrive as 1 and cannot be told apart. make exits 2 on any failed recipe
# whatever the child did, so the code is printed as well as returned.
oracle: ## diff every keystroke case against real vim
	@CGO_ENABLED=0 $(GO) build -o $(ORACLEBIN) ./cmd/oracle
	@$(ORACLEBIN) -cases $(KEYS) -vim $(VIM) -pvim $(CANDIDATE) -candidate-kind $(CANDIDATE_KIND); s=$$?; \
		if [ $$s -ne 0 ]; then echo "make: oracle exited $$s, see the exit codes in cmd/oracle/main.go"; fi; \
		exit $$s

fuzz: ## generate N keystroke scripts and diff each against vim
	@CGO_ENABLED=0 $(GO) build -o $(ORACLEBIN) ./cmd/oracle
	@$(ORACLEBIN) -fuzz -n $(N) -corpus $(CORPUS) -vim $(VIM) -pvim $(CANDIDATE) -candidate-kind $(CANDIDATE_KIND); s=$$?; \
		if [ $$s -ne 0 ]; then echo "make: oracle exited $$s, see the exit codes in cmd/oracle/main.go"; fi; \
		exit $$s

##@ Ship

# Into dist/ and not bin/, because bin/$(BINARY) is what `make install` ships and
# a linux ELF sitting beside it is not part of the program. The static gate does
# not force the split: check 4 looks for a library sidecar, so a second binary
# there passes it.
linux: ## a genuinely static linux/amd64 ELF, which macOS cannot have
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags="-s -w" -o $(LINUXBIN) $(PKG)
	@file $(LINUXBIN) | grep -q 'statically linked' \
		|| { echo "not statically linked:"; file $(LINUXBIN); exit 1; }
	@file $(LINUXBIN)

install: static-check ## install bin/pvim to $(DEST)
	install -m 0755 $(BIN) $(DEST)

clean: ## remove bin/ and dist/
	rm -rf bin dist
