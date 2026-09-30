.PHONY: test test-race vet build architecture fuzz-smoke store-conformance rebuild-ledger threat-map contracts supply-chain fault-matrix postgres-conformance production-conformance dsh-real verify

GO ?= ./scripts/e2e-go.sh

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

rebuild-ledger:
	./scripts/verify-rebuild-ledger.py

threat-map:
	./scripts/verify-threat-test-map.py

contracts:
	bash -n scripts/lib/env-file.sh
	bash -n scripts/lib/go-toolchain.sh
	bash -n scripts/dsh-real-e2e.sh
	bash -n scripts/lib/real-codex.sh
	bash -n scripts/test-real-codex-config.sh
	./scripts/test-real-codex-config.sh
	./scripts/test-go-toolchain.sh
	node --check apps/web/enhancements.js
	node --check e2e/static-server.js
	node --check e2e/workbench.spec.js
	node --check e2e/visuals.spec.js
	node --check e2e/platform-matrix.spec.js
	node --check e2e/graph-browser.spec.js
	node --check scripts/release-assets.mjs
	node --check scripts/fault-matrix.mjs
	bash -n scripts/test-release-signing.sh
	bash -n scripts/orchestration-guard.sh
	./scripts/orchestration-guard.sh
	node scripts/check-html.mjs
	ruby scripts/openapi-contract.rb
	./scripts/verify-rebuild-ledger.py
	./scripts/verify-threat-test-map.py
	./scripts/test-public-identity.py
	./scripts/verify-public-identity.py
	ruby -rjson -e 'require "yaml"; YAML.load_file("openapi/openapi.yaml"); JSON.parse(File.read("release/dependencies.json")); JSON.parse(File.read("SBOM"))'
	bash -n examples/three-repo-feign/run.sh

supply-chain:
	node scripts/release-assets.mjs verify
	./scripts/test-release-signing.sh

fault-matrix:
	node scripts/fault-matrix.mjs

postgres-conformance:
	./scripts/postgres-conformance.sh

production-conformance: postgres-conformance

dsh-real:
	ADRO_RUN_REAL_DSH=1 bash scripts/dsh-real-e2e.sh

verify: test test-race vet build architecture fuzz-smoke store-conformance contracts supply-chain
