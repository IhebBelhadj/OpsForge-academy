SHELL_SCRIPTS := $(shell find . -name '*.sh' -not -path './.git/*' -not -path './web/node_modules/*')
# Fall back to the official image when the local binary is missing or broken (e.g. Arch partial upgrade)
SHELLCHECK := $(shell shellcheck --version >/dev/null 2>&1 && echo shellcheck || \
	echo docker run --rm -v "$(CURDIR):/mnt:ro" -w /mnt koalaman/shellcheck:v0.11.0)

# OpsForge (the lab, its own repository): only its API is used. A checkout is needed only by the integration tests,
# the pack compile (steps checked by a throwaway `opsforge api serve`) and contract-update.
OPSFORGE_DIR ?= ../opsforge
# Absolute: the Go targets run from academy/
override OPSFORGE_DIR := $(abspath $(OPSFORGE_DIR))
export OPSFORGE_DIR

.PHONY: check lint-shell lint-yaml academy academy-test academy-lint academy-validate web-deps web-build web-check \
	gen gen-check e2e contract-check contract-update

check: lint-shell lint-yaml contract-check academy-lint academy-test academy-validate web-check gen-check

lint-shell:
	$(SHELLCHECK) $(SHELL_SCRIPTS)

lint-yaml:
	yamllint .

# academy/contract is a pinned copy of OpsForge's contract/ (its API's schema, OpenAPI document and golden documents).
# contract-update copies it from $(OPSFORGE_DIR) (check out the OpsForge release to pin first); contract-check tells
# when the pinned copy differs from that checkout (skipped without one).
CONTRACT_FILES = toolchain.schema.json openapi.yaml README.md golden

contract-check:
	@if [ ! -d "$(OPSFORGE_DIR)/contract" ]; then echo "contract-check: no OpsForge at $(OPSFORGE_DIR), skipping"; exit 0; fi; \
	for f in $(CONTRACT_FILES); do diff -r "$(OPSFORGE_DIR)/contract/$$f" academy/contract/$$f >/dev/null || { \
		echo "academy/contract/$$f differs from $(OPSFORGE_DIR)/contract/$$f: make contract-update"; exit 1; }; done

contract-update:
	for f in $(CONTRACT_FILES); do rm -rf academy/contract/$$f && cp -r "$(OPSFORGE_DIR)/contract/$$f" academy/contract/; done
	printf 'opsforge %s (contract %s)\n' \
		"$$("$(OPSFORGE_DIR)/opsforge" api version --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["opsforge"])')" \
		"$$("$(OPSFORGE_DIR)/opsforge" api version | sed 's/.*contract //')" > academy/contract/PINNED

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

# The tests (the integration ones included) and the pack compile run against a throwaway `opsforge api serve` from
# $(OPSFORGE_DIR) (scripts/with-opsforge-api.sh sets ACADEMY_OPSFORGE_URL and ACADEMY_OPSFORGE_TOKEN_FILE), so they
# need Python with PyYAML and jsonschema. Without an OpsForge the integration tests skip.
academy-test:
	cd academy && ../scripts/with-opsforge-api.sh go test -race ./...

academy-validate:
	cd academy && ../scripts/with-opsforge-api.sh go run ./cmd/academy compile --content ../labs

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

# End to end: the built binary against the fake OpsForge API (bin/opsforge-fake), driven by Playwright (web/e2e/)
e2e: academy
	cd web && pnpm e2e
