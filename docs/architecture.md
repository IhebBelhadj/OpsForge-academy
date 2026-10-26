# Architecture

## System context

```
 browser ──▶ OpsForge Academy (Go binary, its own repo)          OpsForge (Python, its own repo, on the lab host)
  React SPA   ├─ /           embedded web build                   opsforge api serve (127.0.0.1:8091, bearer token)
  (same       ├─ /api/v1/*   REST (OpenAPI 3.1)                    ├─ /v1/version, /v1/state, /v1/heal
   origin)    ├─ /api/v1/events  SSE                               ├─ /v1/steps/validate
              ├─ /ws/terminal/*  WebSocket ──────┐                 ├─ /v1/sandboxes[/{id}]   create (setup) · delete
              ├─ database (SQLite → PostgreSQL)  │  HTTP + WS      │    ├─ /files/...        the sandbox's workspace
              ├─ content packs (labs/, compiled  ├── a URL and ──▶ │    ├─ /terminal         bash in it (WebSocket)
              │   in-process)                    │   a token       │    ├─ /probes, /actions, /reset   jobs
              └─ internal/opsforgeapi ───────────┘                 │    └─ (namespace sb-<id> on every cluster)
                                                                   └─ /v1/jobs/{id}
                                                                          │
                                                                          └─ incus / ssh / kubectl → the lab
 author ── edits labs/<pack>/ ── academy's compiler (steps checked by OpsForge's validate-steps) → bundle ── imported
```

Two standalone projects, one boundary (PLAN.md Phase 11, "Boundary", decided 2026-10-26):

- **OpsForge Academy** (Go + React, `opsforge-academy` repo): the learning platform. Owns users, courses and their
  content (packs, compiled in-process), progress, quizzes, badges, lab sessions and challenges as *learner* records,
  editor and terminal sessions, the HTTP API, the web app and its database. It knows nothing about Incus, netem, K3s
  or OpsForge's files. What it needs from OpsForge is **a place to run what the learner does**: for each lab or
  challenge attempt it asks for a **sandbox** sized by the course's requirements, works only inside it, deletes it.
- **OpsForge** (Python, existing repo): the lab. Owns topology, provisioning, faults, scenarios, the scenario step
  language and its execution (`ScenarioRun`, `validate-steps`), and **sandboxes**: an isolated namespace on every
  cluster plus a workspace and a shell on the lab host, created with the attempt's setup and always cleaned up with
  its teardown, even if Academy disappears.

The boundary is OpsForge's **versioned HTTP API** (below), not shared code, files or a database. Lab and challenge
steps in Academy's packs are data in OpsForge's step language, checked by OpsForge, like SQL sent to a database.

### Why Go for the platform

| Need | Go | Alternatives considered |
|---|---|---|
| RAM on a host where the lab is the budget | one process ≈ 15-30 MB RSS | Spring Boot 250-400 MB; NestJS/Node 80-120 MB; FastAPI 60-90 MB |
| Terminals, WebSockets, SSE, subprocesses | goroutines, `creack/pty`, `coder/websocket`, `os/exec` with contexts | all possible, Go is the most direct |
| Production structure (modules, interfaces, DI) | interfaces at the consumer, constructor injection, Wire | Spring/Nest have DI frameworks but cost RAM; Python lacks static guarantees |
| Deployment | one static binary with the web app embedded; `go test -race` | runtimes to install |
| Toolchain on this host | Go 1.27 installed (no pacman needed) | Java 17 and Node 22 installed too |

## Platform service: modules

A **modular monolith**: each module is a Go package tree with its own domain types, application service,
ports (interfaces it needs) and adapters (implementations), and talks to other modules only through their service
interfaces or domain events. Splitting a module into a service later means replacing an in-process interface with a
client, nothing else.

| Module | Responsibility | Depends on (ports) |
|---|---|---|
| `identity` | users, local session (cookie), CSRF token; one local user now | `UserRepo`, `Clock` |
| `catalog` | courses, sections, items, lectures, quizzes, challenges, labs **as imported**; content queries; retired content | `ContentRepo` |
| `compiler` | reads the content folder's packs (YAML as PyYAML read it), checks them, writes the bundle (content hashes over Python's JSON bytes); scenario steps checked by OpsForge (`validate-steps`) | `StepChecker` |
| `contentimport` | compiles in-process, flattens the bundle, upserts idempotently, reports changes | `Compiler`, `ContentRepo`, `EventBus` |
| `progress` | item status, course progress, resume point, stale revisions, notes | `ProgressRepo`, `catalog.Service`, `EventBus` |
| `quiz` | quiz attempts: draw/shuffle, timing, server-side scoring, explanations | `QuizRepo`, `catalog.Service`, `Random`, `Clock`, `EventBus` |
| `labsession` | lab attempts: start (create the sandbox: setup), check (probe in it), hint, reset, finish/abandon (delete the sandbox: teardown + heal); one running lab; busy lock | `AttemptRepo`, `Sandboxes`, `catalog.Service`, `EventBus`, `Clock` |
| `challenge` | challenge submissions: run (validate + server dry-run), submit (apply in a fresh sandbox + tests, then delete it), results, reference solution after pass | `SubmissionRepo`, `Sandboxes`, `EventBus` |
| `workspace` | the editor's view of a sandbox's files (list, read, write, rename, delete), versions (`base_sha256`), size limits, change notifications; the files live in the sandbox | `FileStore` (sandbox files), `Watcher` |
| `terminal` | terminal sessions on a sandbox's shell (linger, replay, takeover), resize, one shell per tab | `Spawner` (sandbox terminal) |
| `labstate` | live lab state for the mimic panel (instances, links, faults), cached and pushed | `LabController` (state), `EventBus` |
| `schemas` | Kubernetes JSON Schemas for the editor, generated from the cluster's `/openapi/v3`, cached per server version | `LabController` (kubectl raw) |
| `rewards` | XP, badge rules over the event history, awards (retroactive) | `RewardRepo`, `EventBus` |
| `history` | the `EventBus`: appends every domain event to the `events` table, pushes it to the user's event streams, runs the in-process handlers (progress notices a completed course) | `Repo`, `Sender` (hub) |
| `operations` | long operations (202): runs the work in the background, stores status and result, pushes each change; restart marks running ones interrupted | `Repo`, `Sender` (hub) |
| `httpapi` | transport only: generated OpenAPI handlers → services; SSE hub; WebSocket upgrade; auth/CSRF/Host/Origin middleware; problem+json errors | services |

Dependency direction: `httpapi` → services → domain; adapters implement ports; **domain imports nothing from
adapters or transport**. Enforced by `go-arch-lint` (or `depguard` rules in golangci-lint) in CI.

### Layers inside a module (example: `labsession`)

```
academy/internal/labsession/
  domain.go        Attempt, Step, CheckResult, errors (ErrAnotherLabRunning, ErrStepOrder) — pure
  service.go       type Service interface { Start; Check; Hint; Reset; Abandon } + implementation
  ports.go         AttemptRepo, Sandboxes interfaces (declared where consumed)
  service_test.go  table-driven tests with fakes (written first)
  sqlite/          AttemptRepo on sqlc queries (+ repo tests on a real temp database)
```

### Dependency injection

- **Constructor injection everywhere**: every service takes its ports as interface arguments; no globals, no
  `init()` side effects, no service locator.
- **Wire** (`github.com/google/wire`) generates the object graph in `academy/cmd/academy/wire_gen.go` from provider
  sets per module; compile-time, no reflection. Tests build services by hand with fakes.
- Configuration is one typed `config.Config` (env + flags, validated at start), injected like any dependency.

### Domain events

An in-process `EventBus` (`history.Service.Publish`): the event is appended to the `events` table (the learner's
history, which badge rules read), pushed to the user's event streams (`platform.Hub`, non-blocking: a stream that
falls behind is closed and replays on reconnect), then handed to the subscribed handlers in the publisher's
goroutine. Kinds are `<thing>.<what happened>` (Phase 10's names, so migrated history reads the same):
`lab.started`, `step.checked`, `hint.shown`, `lab.reset`, `lab.passed`, `lab.abandoned`, `lab.failed`,
`quiz.submitted`, `lecture.completed`, `course.completed`, later `challenge.accepted`, `badge.awarded`. Completion
is not derived from events: progress reads each module's rows through a `Source` port (a lab item added later shows
as done when its lab was passed).

## The OpsForge API (Academy → OpsForge)

OpsForge's HTTP API (`opsforge api serve`, `contract/openapi.yaml` in the OpsForge repo, rules in its
`contract/README.md`) is Academy's only way to the lab: one adapter, `internal/opsforgeapi` (an HTTP client over
`internal/lab`'s types; `opsforgefake` is the same API in memory for tests and e2e). Configured by a URL, a token
file and, for a self-signed https OpsForge, a CA file (`--opsforge-ca-file`); plain http only to loopback. The same
documents exist as `opsforge api <cmd> --json` for people and scripts on the lab host. Versioned by `contract: 1`
(the major) in every document and the minor in `GET /v1/version`: Academy refuses another major and, at `/readyz`, a
minor below what it uses; it ignores output fields it does not know, and keeps a pinned copy of the contract
(`academy/contract/`, fetched from OpsForge's git repository by `make contract-update OPSFORGE_REF=vX.Y.Z`; `PINNED`
records commit and checksum).

**Deployment.** The two share nothing but the API: no files, no checkout, no database, no process. Academy runs on
the lab host (OpsForge's API on loopback) or on another machine: OpsForge serves its API over TLS on a reachable
address (`opsforge api serve --host <addr> --tls-cert --tls-key`; it refuses a non-loopback address without TLS) or
stays on loopback behind an SSH tunnel; Academy gets the URL, a copy of the token and, for a self-signed
certificate, the certificate. The learner's browser only ever talks to Academy (OpsForge refuses requests with an
Origin), and Academy's terminal WebSockets are proxied to OpsForge's.

| Route | In | Out |
|---|---|---|
| `GET /v1/version` | – | `{contract, minor, opsforge}` (no token needed) |
| `GET /v1/state` | – | lab up, layout, sites, links (live/desired/condition), nodes, fault, `placeholders` (`${node:a}` → value) |
| `POST /v1/steps/validate` | `{sets: [{steps, expect?, faults?}]}` | `{errors: [{set, message}]}` (the compiler checks every pack's steps in one call) |
| `POST /v1/sandboxes` → job | `{kind: lab\|challenge, label, requires: {layout?, needs?, faults?, cluster_changes?}, namespace?, files?, setup?, teardown?, expires_after?}` | the job's `sandbox` is its id from the 202 on; its output the sandbox: `{id, kind, label, status: ready\|failed, namespace, layout, placeholders (+ `${ns}`), requires, setup, teardown, created_at, expires_at}`; 409 `lab-down`, `unmet-requirements` (with `unmet`), `lab-busy` (a sandbox with faults runs alone) |
| `GET /v1/sandboxes[/{id}]` | – | sandboxes still alive (Academy's attempts match them by id after a restart) |
| `/v1/sandboxes/{id}/files/...` | file bytes, `moveInput` | the workspace tree (1 MiB per file, 10 MiB per tree, confined) |
| `GET /v1/sandboxes/{id}/terminal` | WebSocket `{t:"i",d}`, `{t:"r",cols,rows}` | bash on the lab host in the workspace, `kubectl` defaulting to the sandbox's namespace |
| `POST /v1/sandboxes/{id}/probes` → job | `{steps, expect}` | `{passed, results: [{metric, want, got, passed, checked}], log, error}` (stops at the first failing named step) |
| `POST /v1/sandboxes/{id}/actions` → job | `{steps}` (no faults) | `{metrics, log, error}` (a challenge test's action) |
| `POST /v1/sandboxes/{id}/reset` → job | – | teardown + setup again: the sandbox |
| `DELETE /v1/sandboxes/{id}` → job | – | teardown, the namespace, `heal` if it had faults, the workspace |
| `POST /v1/heal` → job | – | `{healed}` (the lab's owner's escape hatch) |
| `GET /v1/jobs/{id}?wait=<s>` | – | `{id, kind, status, log, output, problem}` |

- Sandboxes (split step 7, done 2026-10-26) replaced the loose `/v1/runs`, `/v1/probes`, `/v1/workspaces` and
  `/v1/terminal` with a `namespace` Academy chose. Later minors: `apply` (`{manifest, dry_run: server|none}` in a
  sandbox, for challenges' Run), `openapi` (`{cluster}`, for `schemas`), `snapshot-restore` (`reset: snapshot` labs).
- **Requirements come from the course**: a lab's `layout`, `needs`, `namespace: auto`, `files` and
  `cluster_changes`; every lab's sandbox requires `faults` (the learner's terminal can break links, so OpsForge heals
  on delete, and the sandbox runs alone). OpsForge decides whether the running lab can host them (409
  `unmet-requirements` with the list); Academy shows the refusal (`/problems/lab-unavailable` with it).
- **OpsForge owns cleanup**: the teardown is stored with the sandbox (`.opsforge/sandboxes/`), runs at once after a
  failed setup and on delete, survives an API restart, and an idle sandbox expires (a day). Academy deletes the
  sandbox when an attempt passes or is abandoned; a sandbox made for an attempt that could not be recorded is
  deleted at once; one Academy lost otherwise expires (Academy does not sweep: OpsForge may serve other clients).
- A step that cannot run is `error` in the result (the check failed), not a problem document (the call failed).
- Placeholders (`${node:c}`, `${ns}`, ...): the bundle keeps texts raw. For display, `labstate` resolves them with the
  sandbox's `placeholders` (or `state`'s outside an attempt), computed by OpsForge's `scenario.substitute` itself, so
  there is no second implementation to drift. For execution OpsForge resolves them.

## Data

- **SQLite** (`academy.db` in Academy's data folder, WAL, foreign keys on) through **`modernc.org/sqlite`** (pure Go, no cgo)
  until the repository split. SQL stays portable (no SQLite-only functions in queries; JSON columns are text decoded
  in Go) so PostgreSQL is a driver + migration-dialect change.
- **PostgreSQL next** (decided 2026-10-25, PLAN.md Phase 11 split step 8): a dedicated Docker container
  (`academy-pg`, 127.0.0.1 only), relational with `jsonb` for documents (`content.doc`), `pgx/v5`, sqlc's
  `postgresql` engine, goose's postgres dialect, `timestamptz`; tests on per-test databases cloned from a migrated
  template. No document database, no ORM.
- **sqlc** generates typed query methods from `academy/internal/<module>/sqlite/queries.sql`; no runtime ORM.
  Chosen over GORM/ent: explicit SQL, compile-time checked, no reflection, easy to review.
- **goose** migrations in `academy/migrations/` (embedded), applied at start; never edited once released.
- Each module owns its tables; cross-module reads go through services (a module's repo never queries another
  module's tables).
- Schema: content-model.md. Phase 10's `labs.db` is imported once by `academy migrate-legacy` (attempts, step
  results, events, badges, awards), then deleted with the Phase 10 server.

## HTTP API

- **OpenAPI 3.1 first**: `academy/api/openapi.yaml` is written before handlers. `oapi-codegen` (strict server,
  `net/http` 1.22 routing) generates the server interface; `httpapi` implements it by calling services.
  `openapi-typescript` generates `web/src/api/schema.d.ts`; `openapi-fetch` is the typed client.
- Resources: content-model.md "API". Errors are RFC 9457 `application/problem+json` with stable `type` URIs
  (`/problems/another-lab-running`, ...), mapped from domain errors in one place.
- **SSE** `/api/v1/events` (one stream per tab, hand-written handler, excluded from oapi-codegen): `history` (each
  history event, with its id: the browser's reconnect sends Last-Event-ID and the missed ones are replayed) and
  `operation` (a long operation's new status). Later: `lab-state` (labstate), `workspace` (file watcher),
  `check-progress`. Replaces Phase 10's 2.5 s polling.
- **WebSocket** `/ws/terminal/{session}`: binary frames for pty output, JSON control frames (input, resize).
- Long operations (setup, checks with `wait_until`, submissions) return `202` with an operation id; progress and
  the result arrive on SSE; `GET /api/v1/operations/{id}` for reconnects.

## Security (local, but a shell is one request away)

- Binds 127.0.0.1 only by default. Middleware rejects any `Host` other than `127.0.0.1:<port>`/`localhost:<port>`
  (DNS rebinding) and any state-changing request or WebSocket upgrade whose `Origin` differs (cross-site WebSocket).
- Session cookie `HttpOnly; SameSite=Strict` + CSRF token header on mutations (the local user is created at first
  start; the cookie makes a future multi-user mode an extension, not a redesign). The page calls `GET /api/v1/me`
  (user + CSRF token), and on 401 `POST /api/v1/sessions` (the one open endpoint, still Origin-checked); the token
  is stored only as its SHA-256. Requests without Origin and Sec-Fetch-Site (tools on the machine) pass the origin
  check; the CSRF token still applies to them.
- Workspace paths are confined (`filepath.Rel` + no symlinks out), size-capped (10 MB per workspace, 1 MB per file).
- Quiz keys and challenge reference solutions never leave the server before they are earned.
- Lecture Markdown is rendered without raw HTML (sanitised on the client too).
- `kubectl` runs only inside a sandbox, in the namespace OpsForge gave it, never with learner-supplied flags from the
  API (the terminal is the place for free commands). Academy holds no lab credentials: only OpsForge's API token.

## Operations

- `academy serve` (default `127.0.0.1:8090`), `academy migrate-legacy` (`-legacy-db`, default `.opsforge/labs.db`;
  again imports only rows Phase 10 recorded since), `academy version`. Re-import: `POST /api/v1/admin/import`.
  `academy compile [--json] [pack...]` checks the packs (authors, `make check`).
- **Logs**: `log/slog` JSON (request id, module, attempt id). **Metrics**: Prometheus `/metrics` on a separate
  loopback port (request latency, check durations, OpsForge API errors), scrapeable by the lab's VictoriaMetrics.
- **Health**: `/healthz` (process), `/readyz` (DB + OpsForge reachable, token accepted, contract version).
- Graceful shutdown: stops accepting, lets running checks finish (with a cap), closes ptys, checkpoints WAL.
- Build: `make academy` → `pnpm build` (web) then `go build` with the web `dist/` embedded; version stamped with
  `-ldflags`.

## Later (kept possible, not built)

- Multi-user: real authentication, per-user lab sessions queued on the single lab or one lab per user on a bigger
  host; the `identity` module and ownership checks already exist.
- Splitting `labsession`/`challenge` into a lab-runner service next to the lab: decided instead as OpsForge's own HTTP
  API (PLAN.md Phase 11 split step 4) with sandboxes (step 7), Academy a client of it.
- Several sandboxes at once (challenges only observe; labs with faults stay one at a time on one cluster), or one
  OpsForge per learner on a bigger host: Academy's `Sandboxes` port does not change.
