.PHONY: test test-race vet build contracts supply-chain fault-matrix browser postgres-conformance production-conformance real-e2e test-expert verify run local

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build ./...

contracts:
	bash -n start.sh
	bash -n scripts/lib/env-file.sh
	bash -n scripts/test-start-permissions.sh
	bash -n scripts/release-system-e2e.sh
	bash -n scripts/real-pipeline-e2e.sh
	./scripts/test-start-permissions.sh
	node --check apps/web/enhancements.js
	node --check e2e/static-server.js
	node --check e2e/workbench.spec.js
	node --check e2e/visuals.spec.js
	node --check e2e/platform-matrix.spec.js
	node --check playwright.matrix.config.js
	node --check scripts/release-assets.mjs
	node --check scripts/fault-matrix.mjs
	bash -n scripts/test-release-signing.sh
	bash -n scripts/orchestration-guard.sh
	./scripts/orchestration-guard.sh
	node scripts/check-html.mjs
	ruby scripts/openapi-contract.rb
	ruby -rjson -e 'require "yaml"; YAML.load_file("openapi/openapi.yaml"); YAML.load_file("deploy/compose/docker-compose.yml"); YAML.load_file("charts/adro/Chart.yaml"); YAML.load_file("charts/adro/values.yaml"); JSON.parse(File.read("charts/adro/values.schema.json")); JSON.parse(File.read("release/dependencies.json")); JSON.parse(File.read("SBOM"))'
	bash -n examples/three-repo-feign/run.sh

supply-chain:
	node scripts/release-assets.mjs verify
	./scripts/test-release-signing.sh

fault-matrix:
	node scripts/fault-matrix.mjs

browser:
	npm run test:e2e:adro
	npm run test:e2e:matrix

postgres-conformance:
	./scripts/postgres-conformance.sh

production-conformance: postgres-conformance

real-e2e:
	ADRO_REQUIRE_CODEX=1 bash scripts/release-system-e2e.sh
	ADRO_REQUIRE_CODEX=1 bash scripts/real-pipeline-e2e.sh

test-expert:
	bash scripts/test-expert.sh

verify: test test-race vet build contracts supply-chain browser

run:
	go run ./cmd/adro-api -addr $${ADRO_ADDR:-:8080} -artifact-root $${ADRO_ARTIFACT_ROOT:-./var/artifacts}

local: run
