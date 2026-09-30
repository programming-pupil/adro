.PHONY: test test-race vet build architecture fuzz-smoke store-conformance supply-chain postgres-conformance

GO ?= go

test:
	$(GO) test ./... -count=1 -p 1

test-race:
	$(GO) test -race ./... -count=1 -p 1

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

architecture:
	$(GO) test ./internal/architecture -count=1

fuzz-smoke:
	$(GO) test ./core/encoding -run '^$$' -fuzz FuzzCanonicalizeIsIdempotent -fuzztime=5s -parallel=1

store-conformance:
	$(GO) test ./adapters/eventstore/... -count=1

supply-chain:
	python3 scripts/test_release_assets.py
	python3 scripts/release-assets.py verify
	./scripts/test-release-signing.sh

postgres-conformance:
	./scripts/postgres-conformance.sh
