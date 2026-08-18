# w-tools — verification gate.
# `make check` is the whole gate; CI runs the same targets, in the same order.
# Order: cheap static checks -> compile -> dynamic -> network. Fail fast, fail cheap.

MODULES := cli httpx logger x/circuitbreaker x/hasher x/sshx

.PHONY: check fmt vet lint build test examples vuln cover bench fuzz

check: fmt vet lint build test examples vuln
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

# Not in MODULES: the examples module needs the workspace (it imports siblings).
examples:
	@(cd httpx/examples && go vet ./... && go build ./... \
		&& out=$$(go run ./redaction) && echo "$$out" | grep -q '\[REDACTED\]' \
		&& out=$$(go run ./breaker) && echo "$$out" | grep -q 'CIRCUIT OPEN') \
		&& (cd cli/examples && go vet ./... && go build ./... \
		&& out=$$(DEMO_GREETING=hallo go run ./demo greet World) && echo "$$out" | grep -q 'hallo, World' \
		&& out=$$(go run ./service --help) && echo "$$out" | grep -q 'migrate') \
		&& echo "examples ok"

vuln:
	@for m in $(MODULES); do (cd $$m && govulncheck ./...) || exit 1; done

# Informational, not part of `check` — regressions surface in review, not as red builds.
bench:
	@for m in $(MODULES); do (cd $$m && go test -bench=. -benchmem -run='^$$' $$(go list ./... | grep -v /examples/)) || exit 1; done

# Time-boxed; per-package fuzz targets, extended as packages gain fuzzers.
fuzz:
	@(cd httpx && go test -fuzz=FuzzRealIP -fuzztime=15s -run='^$$' ./middleware && go test -fuzz=FuzzTraceparent -fuzztime=15s -run='^$$' ./middleware)
	@(cd logger && go test -fuzz=FuzzMaskString -fuzztime=15s -run='^$$' . && go test -fuzz=FuzzRedact -fuzztime=15s -run='^$$' .)
	@(cd x/sshx/keys && go test -fuzz=FuzzParsePrivate -fuzztime=15s -run='^$$' . && go test -fuzz=FuzzGenerateComment -fuzztime=15s -run='^$$' .)
