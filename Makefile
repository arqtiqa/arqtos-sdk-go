GO ?= go

# The targets here are the same gates .github/workflows/ci.yml runs, so a green
# `make ci` locally means the same thing as a green run on the branch.
.PHONY: all build test test-cover cover-gate lint fmt vet staticcheck verify tidy-check ci

all: build lint test

build:
	$(GO) build ./...

# -race is not optional: the SDK is a contract other people's code runs
# concurrently against.
test:
	$(GO) test -race ./... -count=1

lint: fmt vet

# gofmt -l prints misformatted files and exits 0, so the exit status has to be
# derived from the output or the check silently passes.
fmt:
	@out="$$($(GO)fmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt: these files need formatting:"; echo "$$out"; exit 1; \
	fi

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

ci: verify build lint staticcheck tidy-check cover-gate
