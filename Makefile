GO ?= go

# The targets here are the same gates .github/workflows/ci.yml runs, so a green
# `make ci` locally means the same thing as a green run on the branch.
.PHONY: all build test test-cover cover-gate lint fmt vet staticcheck verify tidy-check ip-isolation ci

all: build lint test

build:
	$(GO) build ./...

# Walks both the require graph (go mod graph) and the import graph
# (go list -deps -test) for any arqtiqa module other than this one — see the
# script for why neither graph alone is the full picture and why -test
# matters. This is the IP boundary between the public SDK and arqtos-cli.
ip-isolation:
	@GO=$(GO) bash scripts/check-ip-isolation.sh

# -race is not optional: the SDK is a contract other people's code runs
# concurrently against.
test:
	$(GO) test -race ./... -count=1

lint: fmt-check vet

# ADR-ARQ-J-02 — one meaning per verb across every arqtiqa Go repo:
#   `fmt`       WRITES (matching `go fmt` / `gofmt`, which write)
#   `fmt-check` CHECKS (fails on dirty; this is what `lint` and CI depend on)
#
# ⚠️ This target CHANGED on 2026-08-04. It used to be the check, and `lint`
# depended on it. If you have muscle memory from before, `make fmt` now
# rewrites your tree instead of failing — use `make fmt-check` to verify.
fmt:
	$(GO)fmt -w .

# ⚠️ The gofmt trap has TWO halves and both are load-bearing (ADR-ARQ-J-02):
#   1. `gofmt -l` EXITS 0 while listing files, so the exit code alone gates
#      nothing — the EMPTINESS of the output is the check.
#   2. `gofmt` exits NON-ZERO when it cannot PARSE a file, and that is not
#      "formatting is fine". Collapsing the two makes an unparseable tree report
#      CLEAN — the same defect class as arqtos-cli#1092, where "rule failed"
#      collapsed into "no match" and a broken scan reported clean.
fmt-check:
	@out=$$($(GO)fmt -l . 2>/tmp/gofmt.err); status=$$?; \
	if [ $$status -ne 0 ]; then \
	  echo "gofmt could not parse the tree (exit $$status) — this is NOT a passing format check"; \
	  cat /tmp/gofmt.err >&2; exit 1; \
	fi; \
	if [ -n "$$out" ]; then \
	  echo "not gofmt-clean — run 'make fmt':"; echo "$$out"; exit 1; \
	fi; \
	echo "gofmt: clean"

vet:
	$(GO) vet ./...

# Checksum-verifies the module graph against go.sum before anything compiles it.
verify:
	$(GO) mod download && $(GO) mod verify

# staticcheck catches what `go vet` does not, and this module is the contract
# external developers compile against. Installed on demand rather than assumed
# to be on PATH.
STATICCHECK_VERSION ?= latest

staticcheck:
	@command -v staticcheck >/dev/null 2>&1 || \
		$(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	@sc="$$(command -v staticcheck || echo "$$($(GO) env GOPATH)/bin/staticcheck")"; \
	"$$sc" ./...

# go.mod / go.sum must already be tidy: a dependency that nothing imports is
# removed by the next tidy, which would silently drop it from the module.
#
# The check has to mutate the working tree to do its job, so it lives in a
# script with an EXIT trap rather than in a recipe. A recipe cannot restore what
# it moved when make aborts the line before the restore, or when the tidy is
# interrupted — reproduced both ways: a failed tidy and a Ctrl-C each left a
# modified go.mod plus untracked go.{mod,sum}.tidycheck backups behind.
tidy-check:
	@GO=$(GO) bash scripts/tidy-check.sh

COVERPROFILE ?= coverage.txt

test-cover:
	$(GO) test -race ./... -count=1 -coverprofile=$(COVERPROFILE) -covermode=atomic

# The same floor CI enforces. See .github/scripts/coverage-gate.sh for the two
# package trees excluded from the denominator and why each is a measurement
# correction rather than a lowered bar.
cover-gate: test-cover
	@bash .github/scripts/coverage-gate.sh $(COVERPROFILE)

ci: verify build ip-isolation lint staticcheck tidy-check cover-gate
