# Build gates for agent-peer.
#
# Every parallel-capable target takes its job count from JOBS, which the caller
# sets explicitly (default 4). Nothing here probes the host's CPU count.

JOBS ?= 4

GIT ?= git

# Total timeout of the end-to-end test binary, passed to go test -timeout.
E2E_TIMEOUT ?= 30m

# Hugo version for the website. The site workflow reads this line, so the
# version is written once.
HUGO_VERSION := 0.170.1

SITE_DIR ?= docs/site

# Version whose released section in CHANGELOG.md the release notes check
# extracts.
RELEASE_NOTES_VERSION ?= v0.1.0

.PHONY: gen gen-check lint release-notes-check archive-check proto-lint test e2e e2e-darwin docs-check check site site-check

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

# Format, static analysis, shell script lint, module tidiness and the module
# dependency-direction check.
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
	shellcheck scripts/install.sh
	golangci-lint run ./...
	golangci-lint run --build-tags agentpeer_testenv ./...
	go mod tidy -diff
	go run ./scripts/check-deps -goos linux
	go run ./scripts/check-deps -goos darwin
	go run ./scripts/check-deps -tags e2e

# Check that the release notes extracted from CHANGELOG.md become the body of
# the release goreleaser publishes. The command runs a real release against a
# fake GitHub API; scripts/check-release-notes explains why a snapshot run
# cannot catch this regression.
release-notes-check:
	go run ./scripts/check-release-notes -version $(RELEASE_NOTES_VERSION)

# Build a snapshot and check that every archive holds exactly the release
# contents: the binary, install.sh, the archive README, the licenses and the
# third-party license inventory. The test hook string is covered by the
# goreleaser build hook, which fails the run before an archive is written.
archive-check:
	go run ./scripts/check-archives

proto-lint:
	buf lint

test:
	go build ./...
	go vet ./...
	go test -race -p $(JOBS) -parallel $(JOBS) ./...
	root="$$(mktemp -d)"; home="$$(mktemp -d)"; \
	AGENT_PEER_TEST_RUNTIME_ROOT="$$root" AGENT_PEER_TEST_HOME="$$home" \
		go test -race -count=1 -tags agentpeer_testenv -p $(JOBS) -parallel $(JOBS) \
			-run 'TestTestRuntimeRoot|TestTestEnvHooksInstalled|TestRuntimeDirFromTestEnv|TestParseProtocolOverrides|TestProtocolOverridesInstalled|TestAPIBaseUsesTestOverride' \
			./internal/config ./internal/mcpserver ./cmd/agent-peer ./internal/broker/update; \
	status=$$?; rm -rf "$$root" "$$home"; exit $$status
	@if [ "$$(go env GOOS)" = darwin ]; then \
		go -C $(CURDIR)/third_party/fsnotify test -race -count=1 -p $(JOBS) -parallel $(JOBS) -run '^Test(KqueueCloseDoesNotCloseAReusedDescriptor|AddWatchKeepsAnExistingDescriptorWhenReregisterFails|WriteAfterCreateIsReported)$$' ./; \
	else \
		echo "skipping the kqueue backend tests: GOOS is not darwin"; \
	fi

# The e2e run cleans up after itself even when go test is killed or times out.
# The cleanup is TestMain's: a no-test run removes the projects, networks,
# processes, images and runtime roots whose owner process is gone, and leaves a
# live suite's resources alone. Image tags carry the owner marker, so this does
# not remove an image a parallel suite still uses.
#
# A systemd container that did not boot is rebuilt at most once; the rebuild
# appends a line to SYSTEMD_REBUILD_LEDGER (see
# test/e2e/syscontainer/syscontainer.go), which this target prints and then
# empties so a gate without -v still shows how often and why it happened.
SYSTEMD_REBUILD_LEDGER ?= /tmp/ape2e-systemd-rebuilds.log

e2e:
	@go test -tags e2e -timeout $(E2E_TIMEOUT) -p $(JOBS) -parallel $(JOBS) ./test/e2e/...; \
	status=$$?; \
	if [ -s $(SYSTEMD_REBUILD_LEDGER) ]; then \
		echo "e2e: systemd container rebuilds:"; \
		cat $(SYSTEMD_REBUILD_LEDGER); \
		: > $(SYSTEMD_REBUILD_LEDGER); \
	fi; \
	go test -tags e2e -count=1 -run '^$$' ./test/e2e/ >/dev/null 2>&1 || true; \
	exit $$status

# The macOS service end-to-end tests exercise launchd on the host, so they run
# on macOS only and use the real launchctl and sudo. The system-level and
# user-level tests run serially because both use the same launchd label.
e2e-darwin:
	@if [ "$$(go env GOOS)" != darwin ]; then \
		echo "e2e-darwin: requires macOS (go env GOOS is $$(go env GOOS))" >&2; \
		exit 1; \
	fi
	go test -v -tags e2e_darwin -timeout $(E2E_TIMEOUT) -p 1 -parallel 1 ./test/e2e/darwinservice

# Document standards plus a render of every Mermaid diagram in the docs that
# differ from origin/main. scripts/docs-check.sh owns the details, including
# how the two halves behave when their tooling or the comparison base is
# missing.
docs-check:
	bash scripts/docs-check.sh

# Preview the website locally.
site:
	@if ! command -v hugo >/dev/null 2>&1; then \
		echo "site: hugo is not installed; install Hugo $(HUGO_VERSION)" >&2; \
		exit 1; \
	fi
	hugo server --source $(SITE_DIR)

# Check that the English and Chinese pages match, build the website strictly
# and check the internal links of the generated pages. The link check maps the
# /agent-peer/ prefix onto the generated tree through a temporary root; page
# relative links still resolve against the file that contains them. The build
# cleans the destination first, so a page that was deleted or renamed cannot
# keep passing the link check through its stale output.
site-check:
	@missing=""; \
	for tool in hugo lychee go; do \
		command -v $$tool >/dev/null 2>&1 || missing="$$missing $$tool"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "site-check: missing tools:$$missing (Hugo $(HUGO_VERSION))" >&2; \
		exit 1; \
	fi; \
	have="$$(hugo version 2>/dev/null | sed -n 's/^hugo v\([^ ]*\).*/\1/p')"; \
	case "$$have" in \
		"$(HUGO_VERSION)"|"$(HUGO_VERSION)"-*) ;; \
		*) echo "site-check: Hugo $(HUGO_VERSION) required, found '$$have'" >&2; exit 1 ;; \
	esac
	go run -p $(JOBS) ./scripts/site-parity -en $(SITE_DIR)/content/en -zh $(SITE_DIR)/content/zh
	GOMAXPROCS=$(JOBS) hugo --source $(SITE_DIR) --cleanDestinationDir --gc --minify --panicOnWarning
	@root="$$(mktemp -d)"; trap 'rm -rf "$$root"' EXIT; \
		ln -s "$(CURDIR)/$(SITE_DIR)/public" "$$root/agent-peer"; \
		lychee --offline --no-progress --max-concurrency $(JOBS) --root-dir "$$root" '$(SITE_DIR)/public/**/*.html'

# Run every gate in a fixed order. Each gate is a separate recursive make so
# the order also holds when the caller passes -j.
check:
	$(MAKE) --no-print-directory lint
	$(MAKE) --no-print-directory proto-lint
	$(MAKE) --no-print-directory gen-check
	$(MAKE) --no-print-directory test
	$(MAKE) --no-print-directory e2e
	$(MAKE) --no-print-directory docs-check
