GO ?= go

# Where the release steps stage their artifacts (step 8's release
# uploads from here); overridable per invocation, a relative path
# resolving from this directory:
#   make studio-panel-asset RELEASE_DIR=/tmp/rel
# The default is ignored by studio/web/.gitignore.
RELEASE_DIR ?= studio/web/dist-release

# The workspace joins the framework module (the root: facade, adapters,
# thread, otel, obsdb, studio, runtime), the core module (the loop
# alone; its only dependency is the OTel API) and the example modules
# (ADR 0027), so build/test/vet/lint loop over the modules `go list -m`
# reports from go.work.
MODULES = $(shell $(GO) list -m -f '{{.Dir}}')

.PHONY: build test vet fmt lint tidy generate live tools apidiff apidiff-core apidiff-all apidiff-selftest offline fuzz fuzz-thread soak-thread studio-build studio-check studio-panel-asset

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

# The facades (the root package, mw, wefttest, wefttest/conformance)
# are generated from core (internal/cmd/genfacade, ADR 0027); run this
# after changing core's exported API. TestFacadesAreComplete fails
# when a committed facade is stale.
generate:
	$(GO) generate ./...

# Pinned tooling — the exact versions CI installs, so a local gate and
# the remote gate can never disagree (TODO §1.2a, the §1 pre-flight of
# docs/phase2b-store-plan.md). go install is idempotent; a warm module
# cache makes this a no-op.
tools:
	$(GO) install golang.org/x/exp/cmd/apidiff@v0.0.0-20260908205506-85c1c2202aba

# The apidiff gate (TODO §1.7): the framework module vs the last v*
# tag (reported: the pre-freeze layers live in it). The tools target
# installs the pinned apidiff so the gate runs on a machine without a
# pre-existing binary; PATH gains GOPATH/bin for it.
apidiff: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff.sh

# The core module vs the last core/v* tag (enforced). Deliberate
# pre-1.0 widenings are listed in core/.apidiff-allow.
apidiff-core: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff.sh "" core

# Every module go.work lists, each vs its own last tag and under its
# own policy (scripts/apidiff.sh's header: core is enforced, the root
# is reported, examples skip). A module added to go.work without a
# policy fails here. CI runs this.
#   APIDIFF_STRICT=1 make apidiff-all   # release check: every module
#                                       # builds from published tags
apidiff-all: tools
	PATH="$$(go env GOPATH)/bin:$$PATH" scripts/apidiff.sh "" all

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

# Fuzz every Fuzz* target of the core module, one invocation each (Go
# fuzzes one target at a time), FUZZTIME apiece. A crasher is written
# to core/testdata/fuzz/<Target>/ — commit it as a regression seed.
FUZZTIME ?= 10s
fuzz:
	for f in $$(cd core && $(GO) test -list 'Fuzz.*' . | grep '^Fuzz'); do \
	  (cd core && $(GO) test -run '^$$' -fuzz "^$$f\$$" -fuzztime $(FUZZTIME) .) || exit 1; \
	done

# The thread package's decoders join the fuzz gate:
# entries, headers, signed decisions and grant predicates — one
# invocation each, crashers committed as seeds the same way.
fuzz-thread:
	for p in . ./jsonl; do \
	  for f in $$(cd thread && $(GO) test -list 'Fuzz.*' $$p | grep '^Fuzz'); do \
	    (cd thread && $(GO) test -run '^$$' -fuzz "^$$f\$$" -fuzztime $(FUZZTIME) $$p) || exit 1; \
	  done; \
	done

# The race soak CI runs nightly (.github/workflows/soak.yml): the
# thread package's suite ten times under the race detector, and
# thread/sqlite's three times.
soak-thread:
	cd thread && $(GO) test -race -count=10 -timeout 45m ./...
	cd thread/sqlite && $(GO) test -race -count=3 -timeout 30m ./...

# Live adapter tests behind the `live` build tag; never in CI (no keys).
live:
	for m in $(MODULES); do (cd $$m && $(GO) test -tags live ./...) || exit 1; done

# ── Studio (TODO §12, ADR 0018) ─────────────────────────────────────
# Needs Bun. Contributors run these only when the UI changes; users of
# the module get the committed, embedded studio/dist.
#
# The panel is stamped with the one version (B6): vite.panel.config.ts
# reads `const Version` from version/version.go (studio/web/scripts/
# weft-version.ts), so bumping that line at release and rerunning this
# target is the whole bump; studio's TestVersionIsTheModules fails on a
# stale or hand-edited stamp.

studio-build:
	cd studio/web && bun install --frozen-lockfile && bun run build

# The devtools panel as a release asset (WEFT-DEVTOOLS §5.1): non-Go
# backends serve this file themselves (V2).
studio-panel-asset: studio-build
	cd studio/web && bun run scripts/panel-asset.ts $(abspath $(RELEASE_DIR))

# The freshness gate (ADR 0018 §4): rebuild the web app and prove the
# committed dist matches, fits the 600 KiB gzip budget (§7), and that
# its own checks pass. CI runs this on every push.
studio-check: studio-build
	git diff --exit-code -- studio/dist || { echo "studio/dist is stale: run 'make studio-build' and commit"; exit 1; }
	test -z "$$(git status --porcelain -- studio/dist)" || { git status --short -- studio/dist; echo "studio/dist has files the commit does not: run 'make studio-build' and commit"; exit 1; }
	total=0; for f in $$(find studio/dist -type f); do \
	  sz=$$(gzip -c $$f | wc -c); total=$$((total+sz)); done; \
	kib=$$((total / 1024)); echo "studio dist: $$kib KiB gzipped (budget 600)"; \
	test $$kib -le 600 || { echo "studio dist exceeds the 600 KiB gzip budget (ADR 0018 §7)"; exit 1; }
	cd studio/web && bun run typecheck && bun run test
