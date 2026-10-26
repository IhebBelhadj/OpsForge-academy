# Changelog

OpsForge Academy follows semantic versioning: `v0.x` until M2's parity, then `v1.0.0`. Each release states the
OpsForge range it supports (its API contract: `academy/contract/PINNED`). History before this repository existed
(M0, spike S6, M1, Phase 11 split steps 1-5) is in OpsForge's `docs/PLAN.md`, Phase 11.

## Unreleased

- **PostgreSQL (2026-10-26, split step 8):** the platform's database is PostgreSQL; no SQLite left but the reader of
  Phase 10's `labs.db`. `make db` runs it in Docker (`academy-pg`, `postgres:16-alpine`, 127.0.0.1:5433, volume
  `academy-pg`); `--db` is now `--database` / `ACADEMY_DATABASE_URL` (created if missing). The 11 SQLite migrations
  are one baseline (`timestamptz`, identity ids, booleans, `jsonb` documents; the attempts' `workspace` column is now
  `sandbox`); module adapters moved to `internal/<module>/postgres` (sqlc's `postgresql` engine, database/sql over
  pgx/v5). Tests run on a database each, cloned from a migrated template (`pgtest`); CI has a Postgres service; e2e
  uses `academy_e2e` (`cmd/dbreset` drops it first). Documents are compared as JSON values (`catalog.SameJSON`): jsonb
  normalizes them. Acceptance (2026-10-26): `make check` and `make e2e` (16/16) green on PostgreSQL;
  `migrate-legacy` from Phase 10's `labs.db` gives 9 attempts, 62 events and 6 awards, then nothing the second time,
  and Academy reads 800 XP and 6/6 badges, as on SQLite; importing the packs twice writes nothing; the six core labs
  passed live again (16, 8, 24, 5, 18, 174 s), no sandbox left, `verify` green. Academy 23-26 MiB RSS, PostgreSQL
  20 MiB idle, 58 MiB after the run. Your SQLite `academy.db` (data folder) is no longer read.

## 0.1.0 (2026-10-26)

The first release: M0 and M1 (in OpsForge's `docs/PLAN.md`), its own repository, and every lab attempt in an
OpsForge sandbox. **Supports OpsForge 1.0.x and later 1.x** (contract 1, minor 0 or more; `academy/contract/PINNED`:
`v1.0.0`).

- **Sandboxes (2026-10-26, split step 7):** every lab attempt runs in an OpsForge sandbox (contract pinned at
  OpsForge `v1.0.0`). `labsession` takes one `Sandboxes` port (`Create`/`Await`/`Probe`/`Reset`/`Delete`) instead of
  `Run`/`Probe`/`Heal` on a namespace and workspace it named; OpsForge checks the lab's requirements (409s →
  `lab-down`, `lab-busy`, `lab-unavailable` with what is missing) and cleans up. The attempt's workspace is its
  sandbox (files and terminal under `/v1/sandboxes/{id}/`), so its files go with it; `POST /api/v1/terminals` now
  requires `workspace`. Packs write `opsforge ...` (steps run in the workspace with it on the PATH), which changes
  the content hash of five core labs and the demo pack's lab and challenge: passes of those show as updated.
  Acceptance (live, 2026-10-26): the six core labs passed through Academy's API, every command typed into the
  attempt's terminal (Academy's WebSocket → the sandbox's shell), Academy talking only HTTP to `opsforge api serve`:
  one sandbox per attempt, none (and no `sb-*` or `lab` namespace) left after any, `verify` green. Times: 16, 8, 24,
  5, 20 and 127 s (losing-a-site waits for the eviction); Academy 26 MiB RSS, OpsForge's API 43 MiB. Every lab's
  sandbox requires `faults`: some labs have the learner break links in the terminal, and Phase 10 healed after every
  lab.

- **Own repository (2026-10-26, split step 6):** moved out of OpsForge with `academy/`, `web/`, `labs/` and
  `docs/` (was OpsForge's `docs/courses/`). Go module `github.com/IhebBelhadj/opsforge-academy/academy`. OpsForge is
  reached only over its API. Measured: `make check` green, `make e2e` 16/16.
- **Disconnected from OpsForge's checkout (2026-10-26):** Academy and OpsForge share no files and may run on
  different machines. `OPSFORGE_DIR` and `scripts/with-opsforge-api.sh` are gone: the integration tests and the pack
  compile use the API named by `ACADEMY_OPSFORGE_URL`/`ACADEMY_OPSFORGE_TOKEN_FILE` (else they skip / compile against
  the fake, `scripts/with-opsforge.sh`); `make contract-update` fetches OpsForge's `contract/` from its git repository
  (`OPSFORGE_REPO`, `OPSFORGE_REF`) and `PINNED` records commit and checksum (`contract-check` is offline). New
  `--opsforge-ca-file` (`ACADEMY_OPSFORGE_CA_FILE`) trusts a self-signed OpsForge certificate, for calls and
  terminals. CI runs OpsForge's API as a service outside this checkout.
- Next: sandboxes (split step 7), PostgreSQL (step 8), then M2.
