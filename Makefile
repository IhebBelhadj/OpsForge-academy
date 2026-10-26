SHELL_SCRIPTS := $(shell find . -name '*.sh' -not -path './.git/*' -not -path './web/node_modules/*')
# Fall back to the official image when the local binary is missing or broken (e.g. Arch partial upgrade)
SHELLCHECK := $(shell shellcheck --version >/dev/null 2>&1 && echo shellcheck || \
	echo docker run --rm -v "$(CURDIR):/mnt:ro" -w /mnt koalaman/shellcheck:v0.11.0)

# OpsForge (the lab) is another project, possibly on another machine: Academy reaches it only through its API
# (ACADEMY_OPSFORGE_URL, ACADEMY_OPSFORGE_TOKEN_FILE, ACADEMY_OPSFORGE_CA_FILE) and takes its contract from its git
# repository (contract-update). Nothing here reads an OpsForge checkout.
OPSFORGE_REPO ?= https://github.com/IhebBelhadj/OpsForge.git
OPSFORGE_REF ?= v1.0.0

.PHONY: check lint-shell lint-yaml academy academy-test academy-lint academy-validate web-deps web-build web-check \
	gen gen-check e2e contract-check contract-update db

check: db lint-shell lint-yaml contract-check academy-lint academy-test academy-validate web-check gen-check

# The platform's database: PostgreSQL in a Docker container of its own (academy-pg, 127.0.0.1:5433, volume academy-pg;
# outside Incus, so `opsforge down` never touches it), started if needed. The tests clone a database each from it
# (internal/platform/postgres/pgtest); with ACADEMY_TEST_DATABASE_URL set (CI's service) they use that server instead.
DB_IMAGE := postgres:16-alpine

db:
	@if [ -n "$$ACADEMY_TEST_DATABASE_URL" ]; then exit 0; fi; \
	docker start academy-pg >/dev/null 2>&1 || docker run -d --name academy-pg -p 127.0.0.1:5433:5432 \
		-e POSTGRES_USER=academy -e POSTGRES_PASSWORD=academy -e POSTGRES_DB=academy \
		-v academy-pg:/var/lib/postgresql/data $(DB_IMAGE) -c shared_buffers=16MB -c max_connections=100 >/dev/null; \
	for _ in $$(seq 30); do docker exec academy-pg pg_isready -U academy -q && exit 0; sleep 1; done; \
	echo "academy-pg did not become ready (docker logs academy-pg)" >&2; exit 1

lint-shell:
	$(SHELLCHECK) $(SHELL_SCRIPTS)

lint-yaml:
	yamllint .

# academy/contract is a pinned copy of OpsForge's contract/: contract-update fetches it from $(OPSFORGE_REPO) at
# $(OPSFORGE_REF) (its release tag once there is one) and records commit and checksum in PINNED; contract-check
# (offline) fails when the copy was edited by hand.
contract-check:
	scripts/contract.sh check

contract-update:
	scripts/contract.sh update "$(OPSFORGE_REPO)" "$(OPSFORGE_REF)"

# The Go platform in academy/ with the React app from web/ embedded.
# Go tools (sqlc, oapi-codegen, wire, golangci-lint) are pinned in academy/tools/go.mod.
ACADEMY_VERSION := $(shell git describe --always --dirty 2>/dev/null || echo dev)
GO_TOOL := go tool -modfile=tools/go.mod

academy: web-build
	rm -rf academy/web/dist && mkdir -p academy/web/dist && cp -r web/dist/. academy/web/dist/
	touch academy/web/dist/.gitkeep
	cd academy && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(ACADEMY_VERSION)" \
		-o bin/academy ./cmd/academy
	cd academy && CGO_ENABLED=0 go build -trimpath -o bin/opsforge-fake ./cmd/opsforge-fake
	@echo "built academy/bin/academy $(ACADEMY_VERSION): ./academy/bin/academy serve"

# With ACADEMY_OPSFORGE_URL and ACADEMY_OPSFORGE_TOKEN_FILE (+ ACADEMY_OPSFORGE_CA_FILE) naming a running
# `opsforge api serve`, here or on another machine, the integration tests run against it and it checks the packs'
# steps; without, the integration tests skip and the packs compile against the fake OpsForge (steps unchecked).
academy-test:
	cd academy && go test -race ./...

academy-validate:
	cd academy && ../scripts/with-opsforge.sh go run ./cmd/academy compile --content ../labs

academy-lint:
	cd academy && go vet ./... && $(GO_TOOL) golangci-lint run ./...

web-deps:
	cd web && pnpm install --frozen-lockfile

web-build: web-deps
	cd web && pnpm build

web-check: web-deps
	cd web && pnpm typecheck && pnpm lint && pnpm format:check && pnpm test

# Generated code: Go queries (sqlc), the HTTP server (oapi-codegen), the object graph (wire), the web client and
# route tree. Generated files are committed; gen-check fails when they are stale.
gen: web-deps
	cd academy && $(GO_TOOL) sqlc generate
	cd academy && $(GO_TOOL) oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	cd academy && $(GO_TOOL) wire gen ./cmd/academy >/dev/null
	cd web && pnpm gen >/dev/null

GENERATED_HASHES = find academy web/src -type f -not -path '*/node_modules/*' | sort | xargs sha256sum

gen-check:
	@$(GENERATED_HASHES) > .gen-before
	@$(MAKE) --no-print-directory gen >/dev/null 2>&1
	@$(GENERATED_HASHES) > .gen-after
	@diff .gen-before .gen-after >/dev/null && rm -f .gen-before .gen-after || { \
		diff .gen-before .gen-after; rm -f .gen-before .gen-after; \
		echo "generated code was stale: make gen rewrote the files above (review and commit them)"; exit 1; }

# End to end: the built binary against the fake OpsForge API (bin/opsforge-fake), driven by Playwright (web/e2e/),
# on a database of its own (academy_e2e, made afresh each run)
e2e: academy db
	cd academy && go run ./cmd/dbreset academy_e2e
	cd web && pnpm e2e
