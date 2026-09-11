GO ?= go

# The workspace is the monorepo layout: every adapter is its own module
# (ADR 0005), so build/test/vet/lint loop over the modules `go list -m`
# reports from go.work. The root module stays dependency-free; vendor
# SDKs are required only by the adapter modules.
MODULES = $(shell $(GO) list -m -f '{{.Dir}}')

.PHONY: build test vet fmt lint tidy live

build:
	for m in $(MODULES); do (cd $$m && $(GO) build ./...); done

test:
	for m in $(MODULES); do (cd $$m && $(GO) test -race ./...); done

vet:
	for m in $(MODULES); do (cd $$m && $(GO) vet ./...); done

fmt:
	for m in $(MODULES); do (cd $$m && $(GO) fmt ./...); done

lint:
	for m in $(MODULES); do (cd $$m && golangci-lint run); done

tidy:
	for m in $(MODULES); do (cd $$m && $(GO) mod tidy); done

# Live adapter tests behind the `live` build tag; never in CI (no keys).
live:
	for m in $(MODULES); do (cd $$m && $(GO) test -tags live ./...); done
