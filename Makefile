GO ?= go

.PHONY: all build test lint fmt tidy-check ci

all: build lint test

build:
	$(GO) build ./...

# -race is not optional: the SDK is a contract other people's code runs
# concurrently against.
test:
	$(GO) test -race ./... -count=1

lint: fmt
	$(GO) vet ./...

# gofmt -l prints misformatted files and exits 0, so the exit status has to be
# derived from the output or the check silently passes.
fmt:
	@out="$$($(GO)fmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt: these files need formatting:"; echo "$$out"; exit 1; \
	fi

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

ci: build lint tidy-check test
