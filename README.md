# OpsForge Academy

A learning platform for distributed systems and Kubernetes, built on the [OpsForge](../opsforge) lab: courses of
lectures, multiple-choice quizzes, guided labs checked on a live multi-site cluster, and LeetCode-style challenges,
with a code editor and a terminal in the browser.

Academy is a standalone Go service with a React web app embedded in its binary. Everything it runs on the lab goes
through **OpsForge's HTTP API** (`opsforge api serve`): for each lab or challenge attempt it gets a sandbox on the lab
and works inside it. It needs the API's URL and token, not an OpsForge checkout.

## Requirements

- Go 1.27, Node 22 with pnpm (and Playwright's browsers for the e2e suite)
- To run labs: an OpsForge lab and its API (`opsforge api serve`, contract 1; see `academy/contract/PINNED`)
- For the full `make check`: an OpsForge checkout at `../opsforge` (or `OPSFORGE_DIR=...`) with Python 3, PyYAML and
  jsonschema; its API checks the packs' steps and runs the integration tests

## Quick start

```bash
make academy                        # builds web/ into academy/bin/academy
# on the lab host, in the OpsForge checkout: ./opsforge api serve
install -m 600 ../opsforge/.opsforge/api-token ~/.local/share/opsforge-academy/opsforge-token
./academy/bin/academy serve --content labs   # http://127.0.0.1:8090/
```

Flags: `--data` (default `$XDG_DATA_HOME/opsforge-academy`), `--content` (the packs, default `./labs`),
`--opsforge-url` (default `http://127.0.0.1:8091`), `--opsforge-token-file`. `academy compile` checks the packs;
`academy migrate-legacy --legacy-db <labs.db>` imports OpsForge Phase 10's progress.

## Development

```bash
make check   # shellcheck, yamllint, contract pin, Go lint + tests, pack compile, web checks, generated code
make e2e     # Playwright against the built binary and a fake OpsForge API
make gen     # after editing academy/api/openapi.yaml or SQL queries
```

The plan (architecture, content model, milestones) is in [docs/](docs/README.md).
