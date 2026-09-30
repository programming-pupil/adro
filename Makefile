.PHONY: test test-race vet build architecture fuzz-smoke store-conformance contracts supply-chain postgres-conformance

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

contracts:
	ruby scripts/openapi-contract.rb
	node scripts/check-html.mjs

supply-chain:
	node scripts/release-assets.mjs verify
	./scripts/test-release-signing.sh

postgres-conformance:
	./scripts/postgres-conformance.sh
