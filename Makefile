GO ?= go

# The workspace is the monorepo layout: every adapter is its own module
# (ADR 0005), so build/test/vet/lint loop over the modules `go list -m`
# reports from go.work. The root module stays dependency-free; vendor
# SDKs are required only by the adapter modules.
MODULES = $(shell $(GO) list -m -f '{{.Dir}}')

.PHONY: build test vet fmt lint tidy live tools apidiff apidiff-store apidiff-selftest offline fuzz

build:
	for m in $(MODULES); do (cd $$m && $(GO) build ./...) || exit 1; done

test:
	for m in $(MODULES); do (cd $$m && $(GO) test -race ./...) || exit 1; done

vet:
	for m in $(MODULES); do (cd $$m && $(GO) vet ./...) || exit 1; done

fmt:
	for m in $(MODULES); do (cd $$m && $(GO) fmt ./...) || exit 1; done

# Needs: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
# PATH gains GOPATH/bin so the target works from a bare shell (as apidiff does).
lint:
	for m in $(MODULES); do (cd $$m && PATH="$$(go env GOPATH)/bin:$$PATH" golangci-lint run) || exit 1; done

tidy:
	for m in $(MODULES); do (cd $$m && $(GO) mod tidy) || exit 1; done

# Pinned tooling — the exact versions CI installs, so a local gate and
# the remote gate can never disagree (TODO §1.2a, the §1 pre-flight of
# docs/phase2b-store-plan.md). go install is idempotent; a warm module
# cache makes this a no-op.
tools:
	$(GO) install golang.org/x/exp/cmd/apidiff@v0.0.0-20260908205506-85c1c2202aba

# The apidiff gate (TODO §1.7): root module vs the last v* tag.
# The tools target installs the pinned apidiff so the gate runs on a
# machine without a pre-existing binary; PATH gains GOPATH/bin for it.
apidiff: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff.sh

# The store module's half of the gate — vs the last store/v* tag, from
# the store's second tag on (plan §3.1). Other sub-modules join the
# same way when they tag.
apidiff-store: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff.sh "" store

# Exercises the gates' own failure modes — a broken tree must fail
# them, never read green.
apidiff-selftest: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff-selftest.sh

# The offline gate (ADR 0013's kill-switch clause): every suite in the
# workspace stays green under WEFT_MODEL_REQUESTS=deny. Adapter suites
# drive their fixtures through injected SDK clients — test doubles by
# construction — so deny still means "no self-built client egress".
offline:
	for m in $(MODULES); do (cd $$m && WEFT_MODEL_REQUESTS=deny $(GO) test ./...) || exit 1; done

# Fuzz every Fuzz* target of the root module, one invocation each (Go
# fuzzes one target at a time), FUZZTIME apiece. A crasher is written
# to testdata/fuzz/<Target>/ — commit it as a regression seed.
FUZZTIME ?= 10s
fuzz:
	for f in $$($(GO) test -list 'Fuzz.*' . | grep '^Fuzz'); do \
	  $(GO) test -run '^$$' -fuzz "^$$f\$$" -fuzztime $(FUZZTIME) . || exit 1; \
	done

# Live adapter tests behind the `live` build tag; never in CI (no keys).
live:
	for m in $(MODULES); do (cd $$m && $(GO) test -tags live ./...) || exit 1; done
