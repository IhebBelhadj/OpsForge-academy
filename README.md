# OpsForge Academy

A learning platform for distributed systems and Kubernetes, built on the
[OpsForge](https://github.com/IhebBelhadj/OpsForge) lab: courses of lectures, multiple-choice quizzes, guided labs
checked on a live multi-site cluster, and LeetCode-style challenges, with a code editor and a terminal in the browser.

Academy is a standalone Go service with a React web app embedded in its binary. Everything it runs on the lab goes
through **OpsForge's HTTP API** (`opsforge api serve`): for each lab or challenge attempt it gets a sandbox on the lab
and works inside it. It needs the API's URL and token, never an OpsForge checkout or its files: the two run on the
same machine or on different ones.

## Requirements

- Go 1.27, Node 22 with pnpm (and Playwright's browsers for the e2e suite)
- PostgreSQL: `make db` runs one in Docker (`academy-pg` on 127.0.0.1:5433, volume `academy-pg`); the default
  `--database` points at it. Elsewhere: `--database postgres://user:pass@host/name` (created if missing)
- To run labs: an OpsForge lab and its API (`opsforge api serve`, contract 1; see `academy/contract/PINNED`)
- Nothing of OpsForge for `make check`: with `ACADEMY_OPSFORGE_URL` and `ACADEMY_OPSFORGE_TOKEN_FILE` set, its API
  also runs the integration tests and checks the packs' steps; without, those tests skip and the packs compile
  against a fake OpsForge (steps unchecked)

## Quick start

```bash
make academy                        # builds web/ into academy/bin/academy
# OpsForge on the same machine: on the lab host, ./opsforge api serve (127.0.0.1:8091), then copy its token:
install -m 600 <opsforge>/.opsforge/api-token ~/.local/share/opsforge-academy/opsforge-token
./academy/bin/academy serve --content labs   # http://127.0.0.1:8090/
```

OpsForge on another machine: serve its API over TLS on an address Academy can reach, copy the token (a secret:
the API opens shells on the lab host) and the certificate over, and point Academy at it:

```bash
# lab host
./opsforge api serve --host 10.0.0.5 --tls-cert lab.pem --tls-key lab.key
# Academy's machine (lab.pem's subjectAltName must name 10.0.0.5)
./academy/bin/academy serve --content labs --opsforge-url https://10.0.0.5:8091 --opsforge-ca-file lab.pem
```

(Or keep the API on loopback and reach it through an SSH tunnel: `ssh -L 8091:127.0.0.1:8091 lab-host`.)

Flags: `--database` (`ACADEMY_DATABASE_URL`), `--data` (default `$XDG_DATA_HOME/opsforge-academy`: the OpsForge token), `--content` (the packs, default `./labs`),
`--opsforge-url` (default `http://127.0.0.1:8091`; plain http only to loopback), `--opsforge-token-file` (default
`<data>/opsforge-token`), `--opsforge-ca-file` (a self-signed OpsForge certificate), each also `ACADEMY_*`.
`academy compile` checks the packs; `academy migrate-legacy --legacy-db <labs.db>` imports OpsForge Phase 10's
progress (a copy of OpsForge's `.opsforge/labs.db`).

## Development

```bash
make db      # start the PostgreSQL container (make check and make e2e start it too)
make check   # shellcheck, yamllint, contract pin, Go lint + tests, pack compile, web checks, generated code
make contract-update OPSFORGE_REF=v1.0.0   # re-pin OpsForge's contract from its git repository (OPSFORGE_REPO)
make e2e     # Playwright against the built binary and a fake OpsForge API
make gen     # after editing academy/api/openapi.yaml or SQL queries
```

The plan (architecture, content model, milestones) is in [docs/](docs/README.md).
