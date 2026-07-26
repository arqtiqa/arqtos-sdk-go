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
tidy-check:
	@cp go.mod go.mod.tidycheck && cp go.sum go.sum.tidycheck
	@$(GO) mod tidy
	@status=0; \
	if ! diff -q go.mod go.mod.tidycheck >/dev/null || ! diff -q go.sum go.sum.tidycheck >/dev/null; then \
		echo "go.mod/go.sum are not tidy; run 'go mod tidy' and commit the result"; \
		diff -u go.mod.tidycheck go.mod || true; \
		diff -u go.sum.tidycheck go.sum || true; \
		status=1; \
	fi; \
	mv go.mod.tidycheck go.mod; mv go.sum.tidycheck go.sum; \
	exit $$status

ci: build lint tidy-check test
