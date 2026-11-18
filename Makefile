# Build gates for agent-peer.
#
# Every parallel-capable target takes its job count from JOBS, which the caller
# sets explicitly (default 4). Nothing here probes the host's CPU count.

JOBS ?= 4

GIT ?= git

.PHONY: gen gen-check lint proto-lint test e2e docs-check check

# Regenerate the protobuf code with buf. Generated Go code is committed next to
# its .proto file, so this target is expected to leave the tree unchanged when
# the protos and the generated code agree.
gen:
	buf generate

# Regenerate the protobuf code and fail when the working tree does not already
# contain exactly that output. scripts/gen-check.sh owns the comparison: it
# generates into a temporary directory and diffs the result against the
# committed code, so uncommitted hand-written Go files do not affect it.
gen-check:
	bash scripts/gen-check.sh

# Format, static analysis, module tidiness and the module dependency-direction
# check.
lint:
	@files="$$(find . -type f -name '*.go' \
		-not -path './third_party/*' \
		-not -path './.git/*' \
		-not -path './.claude/*')"; \
	if [ -n "$$files" ]; then \
		unformatted="$$(gofmt -l $$files)"; \
		if [ -n "$$unformatted" ]; then \
			printf '%s\n' "lint: gofmt found unformatted Go files:" "$$unformatted" >&2; \
			exit 1; \
		fi; \
	fi
	golangci-lint run ./...
	go mod tidy -diff
	go run ./scripts/check-deps -goos linux
	go run ./scripts/check-deps -goos darwin
	go run ./scripts/check-deps -tags e2e

proto-lint:
	buf lint

test:
	go build ./...
	go vet ./...
	go test -race -p $(JOBS) -parallel $(JOBS) ./...

e2e:
	go test -tags e2e -p $(JOBS) -parallel $(JOBS) ./test/e2e/...

# Document standards plus a render of every Mermaid diagram in the docs that
# differ from origin/main. scripts/docs-check.sh owns the details, including
# how the two halves behave when their tooling or the comparison base is
# missing.
docs-check:
	bash scripts/docs-check.sh

# Run every gate in a fixed order. Each gate is a separate recursive make so
# the order also holds when the caller passes -j.
check:
	$(MAKE) --no-print-directory lint
	$(MAKE) --no-print-directory proto-lint
	$(MAKE) --no-print-directory gen-check
	$(MAKE) --no-print-directory test
	$(MAKE) --no-print-directory e2e
	$(MAKE) --no-print-directory docs-check
