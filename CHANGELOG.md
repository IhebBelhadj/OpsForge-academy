# Changelog

OpsForge Academy follows semantic versioning: `v0.x` until M2's parity, then `v1.0.0`. Each release states the
OpsForge range it supports (its API contract: `academy/contract/PINNED`). History before this repository existed
(M0, spike S6, M1, Phase 11 split steps 1-5) is in OpsForge's `docs/PLAN.md`, Phase 11.

## Unreleased

- **Own repository (2026-10-26, split step 6):** moved out of OpsForge with `academy/`, `web/`, `labs/` and
  `docs/` (was OpsForge's `docs/courses/`). Go module `github.com/IhebBelhadj/opsforge-academy/academy`. OpsForge is
  reached only over its API; its checkout (`OPSFORGE_DIR`, default `../opsforge`) is used only by the real-API
  tests, the pack compile and `contract-update`. Measured: `make check` green, `make e2e` 16/16.
- Next: sandboxes (split step 7), PostgreSQL (step 8), then M2.
