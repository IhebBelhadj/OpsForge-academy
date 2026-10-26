# Changelog

OpsForge Academy follows semantic versioning: `v0.x` until M2's parity, then `v1.0.0`. Each release states the
OpsForge range it supports (its API contract: `academy/contract/PINNED`). History before this repository existed
(M0, spike S6, M1, Phase 11 split steps 1-5) is in OpsForge's `docs/PLAN.md`, Phase 11.

## Unreleased

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
