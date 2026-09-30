GO ?= go

# The workspace is the monorepo layout: every adapter is its own module
# (ADR 0005), so build/test/vet/lint loop over the modules `go list -m`
# reports from go.work. The root module stays dependency-free; vendor
# SDKs are required only by the adapter modules.
MODULES = $(shell $(GO) list -m -f '{{.Dir}}')

.PHONY: build test vet fmt lint tidy live tools apidiff apidiff-store apidiff-thread apidiff-selftest offline fuzz fuzz-thread studio-build studio-check

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

# The thread module's half — vs the last thread/v* tag, from
# thread/v0.2.0 on (plan §10, as store joined at store/v0.1.1). Before
# the first tag exists the gate skips with a note.
apidiff-thread: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff.sh "" thread

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

# The thread module's decoders join the fuzz gate (plan §10, step 7.1):
# entries, headers, signed decisions and grant predicates — one
# invocation each, crashers committed as seeds the same way.
fuzz-thread:
	for p in . ./jsonl; do \
	  for f in $$(cd thread && $(GO) test -list 'Fuzz.*' $$p | grep '^Fuzz'); do \
	    (cd thread && $(GO) test -run '^$$' -fuzz "^$$f\$$" -fuzztime $(FUZZTIME) $$p) || exit 1; \
	  done; \
	done

# Live adapter tests behind the `live` build tag; never in CI (no keys).
live:
	for m in $(MODULES); do (cd $$m && $(GO) test -tags live ./...) || exit 1; done

# ── Studio (TODO §12, ADR 0018) ─────────────────────────────────────
# Needs Bun. Contributors run these only when the UI changes; users of
# the module get the committed, embedded studio/dist.

studio-build:
	cd studio/web && bun install --frozen-lockfile && bun run build

# The freshness gate (ADR 0018 §4): rebuild the web app and prove the
# committed dist matches, fits the 600 KiB gzip budget (§7), and that
# its own checks pass. CI runs this on every push.
studio-check: studio-build
	git diff --exit-code -- studio/dist || { echo "studio/dist is stale: run 'make studio-build' and commit"; exit 1; }
	total=0; for f in $$(find studio/dist -type f); do \
	  sz=$$(gzip -c $$f | wc -c); total=$$((total+sz)); done; \
	kib=$$((total / 1024)); echo "studio dist: $$kib KiB gzipped (budget 600)"; \
	test $$kib -le 600 || { echo "studio dist exceeds the 600 KiB gzip budget (ADR 0018 §7)"; exit 1; }
	cd studio/web && bun run typecheck && bun run test
