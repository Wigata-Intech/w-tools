# w-tools — verification gate.
# `make check` is the whole gate; CI runs the same targets, in the same order.
# Order: cheap static checks -> compile -> dynamic -> network. Fail fast, fail cheap.

MODULES := logger

.PHONY: check fmt vet lint build test vuln cover bench fuzz

check: fmt vet lint build test vuln
	@echo "all checks passed"

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done

lint:
	@for m in $(MODULES); do (cd $$m && golangci-lint run) || exit 1; done

build:
	@for m in $(MODULES); do (cd $$m && GOWORK=off CGO_ENABLED=0 go build ./...) || exit 1; done

test:
	@for m in $(MODULES); do (cd $$m && go test -race -cover $$(go list ./... | grep -v /examples/)) || exit 1; done

cover:
	@for m in $(MODULES); do (cd $$m && go test -race -coverprofile=coverage.out $$(go list ./... | grep -v /examples/) && go tool cover -func=coverage.out) || exit 1; done

vuln:
	@for m in $(MODULES); do (cd $$m && govulncheck ./...) || exit 1; done

# Informational, not part of `check` — regressions surface in review, not as red builds.
bench:
	@for m in $(MODULES); do (cd $$m && go test -bench=. -benchmem -run='^$$' $$(go list ./... | grep -v /examples/)) || exit 1; done

# Time-boxed; per-package fuzz targets, extended as packages gain fuzzers.
fuzz:
	@(cd logger && go test -fuzz=FuzzMaskString -fuzztime=15s -run='^$$' . && go test -fuzz=FuzzRedact -fuzztime=15s -run='^$$' .)
