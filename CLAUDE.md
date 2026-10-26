# OpsForge Academy

A learning platform (courses of lectures, quizzes, guided labs and LeetCode-style challenges, with an editor and a
terminal) built on **OpsForge**, the multi-site WAN lab, which is another repository (checked out at `../opsforge` on
this host, but nothing here may rely on that). Academy is standalone: from OpsForge it needs only somewhere to run
what the learner does, through OpsForge's HTTP API (a URL, a token and maybe a CA certificate), never its checkout or
its files; the two may run on different machines. Read `docs/README.md` and `docs/implementation.md` before
structural changes (architecture, content model, milestones, tests first).

**Now** (2026-10-26): moved out of OpsForge (its `docs/PLAN.md` Phase 11 holds the history up to here: M0, spike S6,
M1, split steps 1-6). **Next:** split step 7, sandboxes: OpsForge serves a sandbox per lab/challenge attempt (sized
by the course's requirements, cleaned up by OpsForge; design in `docs/architecture.md` "The OpsForge API"); Academy's
`labsession`/`challenge` move onto one `Sandboxes` port. Then step 8, PostgreSQL in Docker, then M2 (player and
parity). Tags: `v0.1.0` once Academy runs on OpsForge `v1.0.0`.

## Layout / index

| Path | What it is |
|---|---|
| `academy/` | Go platform (module `github.com/IhebBelhadj/opsforge-academy/academy`): `cmd/academy` (main, Wire `wire.go` → `wire_gen.go`), `cmd/opsforge-fake` (OpsForge's API in memory, for e2e), `api/` (`openapi.yaml`, `bundle.schema.json`, the pack formats `course.schema.json` + `labpack.schema.json`, oapi-codegen config), `contract/` (pinned copy of OpsForge's contract + `PINNED`), `internal/{catalog,compiler,config,contentimport,history,httpapi,identity,lab,labsession,labstate,legacy,operations,opsforgeapi,platform,progress,quiz,rewards,terminal,workspace}`, `migrations/` (goose, embedded), `web/dist` (embed target), `testdata/` (golden fixtures, demo pack, `labs.phase10.sql`), `tools/go.mod` (pinned sqlc, oapi-codegen, wire, golangci-lint: `go tool -modfile=tools/go.mod <tool>`), `.golangci.yml` (depguard layering). |
| `web/` | React 19 + Vite + TS app (pnpm): `src/{app,routes,features,components/ui,api,lib,test,styles}`, `e2e/` (Playwright against the built binary + `academy/bin/opsforge-fake`). `src/api/schema.d.ts` and `src/routeTree.gen.ts` are generated and committed. |
| `labs/` | Content packs: `<pack>/pack.yml`, `curricula/*.yml`, `labs/<id>.yml` (+ `labs/<id>/files/`), `badges.yml`, `courses/<id>/`, `assets/`. Compiled by `academy compile`. Lab steps are OpsForge's step language (data, checked by its `validate-steps`). |
| `docs/` | The plan: architecture, frontend, design, content model, Kubernetes syllabus, testing, implementation (milestones). |
| `scripts/with-opsforge.sh` | Runs a command with `ACADEMY_OPSFORGE_URL`'s API when set, else with a throwaway `opsforge-fake` (warning: steps unchecked); used by `academy-validate`. |
| `scripts/contract.sh` | `update <repo> <ref>` fetches OpsForge's `contract/` from git into `academy/contract/` + `PINNED` (commit, checksum); `check` verifies the checksum offline. |
| `Makefile` | `make check` (shellcheck, yamllint, contract-check, Go lint + tests, pack compile, web checks, gen-check), `make academy` (web build embedded → `academy/bin/academy`), `academy-test`, `academy-lint`, `academy-validate`, `web-check`, `gen`, `e2e`, `contract-update`. |
| `CHANGELOG.md` | Releases and milestone notes with measurements. |

## Host facts

- Same host as OpsForge: Arch Linux, 12 threads, 14 GB RAM, and the lab is the RAM budget (Academy idles at ~50 MiB
  RSS). Go 1.27, Node 22 + pnpm, Playwright browsers in `~/.cache/ms-playwright`. The host is mid-upgrade: no `pacman
  -S`; the local `shellcheck` is broken, so the Makefile falls back to the `koalaman/shellcheck` Docker image.
- `sudo` needs a password: never run privileged commands; hand them to the user.
- The live lab's API (on this host): in `../opsforge`, `./opsforge api serve` (127.0.0.1:8091, token
  `.opsforge/api-token`; lab commands need the `incus-admin` group). To run the real-API tests against it:
  `ACADEMY_OPSFORGE_URL=http://127.0.0.1:8091 ACADEMY_OPSFORGE_TOKEN_FILE=<a copy of the token> make check`.

## Conventions

- Teaching-lab content (Phase 10's packs, now Academy's): pack files are the source of truth, the database only
  mirrors them. A lab's content hash vs. the pass's hash = "updated since you passed it". Checks only observe
  (`scenario.FAULT_STEPS` refused in probes by `api validate-steps`); faults go in a lab's `setup`, and finishing or
  abandoning always runs `teardown` + `heal`. Probes run on the host from the repo root (`./opsforge ...`); lab texts
  tell the learner `opsforge ...` (a function in the terminal's rcfile). Quiz answers never leave the server.
  New check/rule types: `compiler.checks`/`compiler.ruleTypes` (+ `rewards.Rules`, + labsession for a new check).
  A lab's teardown must leave `verify` green: anything that moves pods (a partition) must re-spread them (losing-a-site
  rolls every spread Deployment outside kube-system). Spread workloads need `matchLabelKeys: [pod-template-hash]`
  (the demo has it since Phase 10). Probes stop at the first failing named step, so order cheap preconditions first.
  Check every lab text against the live output before shipping it (the walkthrough found five wrong claims).
- Academy (Phase 11): the Go platform never touches Incus/K3s nor an OpsForge checkout; everything lab-side goes
  through OpsForge's HTTP API (`opsforge api serve`, `contract/openapi.yaml`; the contract and its versioning rules:
  `contract/README.md`) with the adapter `internal/opsforgeapi` (lab state, jobs, step checks, `workspace.FileStore`,
  `terminal.Spawner`). The `opsforge api <cmd> --json` CLI is the same contract for people and scripts. OpsForge owns
  `contract/` (schema, `openapi.yaml`, `golden/` written by its pytest); Academy reads its pinned copy `academy/contract/`
  (`make contract-update [OPSFORGE_REF=vX.Y.Z] [OPSFORGE_REPO=<git url or clone>]` fetches it from git and writes
  `PINNED`: commit, contract version, checksum; `contract-check` in `make check` fails on a hand-edited copy). Never
  add a build or test step that reads an OpsForge checkout or starts OpsForge's code: CI runs its API as a service. Additive change = bump
  OpsForge's `contract.CONTRACT_MINOR` + a row in contract/README.md's table (Academy raises `opsforgeapi.MinMinor`/`MinRelease`
  only when it starts using the addition); breaking = major (`CONTRACT` and `opsforgeapi.Contract`). Inputs are strict, outputs open (Academy ignores unknown fields; OpsForge's tests check its
  outputs against the closed schema). `api version` = `{contract, minor, opsforge}` (`VERSION` in `opsforge`).
  `validate-steps` (OpsForge's `contract.step_errors`) is how the course compiler checks scenario steps: one call with every set.
  Labs without `files` keep Phase 10's content hash (passes stay fresh).
  `run`/`probe`/`heal` (`lab_run`, `lab_probe`) are jobs over HTTP (the adapter polls `?wait=`; a lost job is
  `ErrJobLost`), their input checked against `contract/toolchain.schema.json` (the Go adapter checks the outputs); a
  failing step is the result's `error`, a bad call a problem. A `namespace` must be `lab-*`/`ch-*` (setup creates it on every cluster, labelled
  `app.kubernetes.io/managed-by=opsforge-api`; teardown always deletes it). The documents are Go types in the pure
  package `internal/lab`, so module ports and the adapter share them; lab jobs use `LabTimeout` (20 min), other calls
  `OpsForgeTimeout`. Golden: `run.golden`, `probe.{passed,failed}`, `heal`, `job` (timings zeroed).
- Academy config (split step 5): `--data` (default `$XDG_DATA_HOME/opsforge-academy`: `academy.db`, `opsforge-token`,
  a copy of OpsForge's `.opsforge/api-token` read at every call, so a missing one is a readyz problem, not a start
  failure), `--content` (packs, default `./labs`), `--opsforge-url` (default `http://127.0.0.1:8091`; plain http only to
  loopback: the token), `--opsforge-ca-file` (PEM to trust for a self-signed https OpsForge, used by calls and
  terminals: `Client.TrustCA`), `--legacy-db` (migrate-legacy only; a copy of OpsForge's `.opsforge/labs.db`). Tests: `opsforgeapi/opsforgefake` is the API in memory
  (httpapi's world, cmd/academy, `cmd/opsforge-fake` for e2e); `make academy-test`/`academy-validate` use the real
  `opsforge api serve` named by `ACADEMY_OPSFORGE_URL`/`ACADEMY_OPSFORGE_TOKEN_FILE`/`ACADEMY_OPSFORGE_CA_FILE` (any
  machine; `opsforgeapi`'s `TestReal*` skip without, the compile falls back to the fake), which is where real bash in
  the terminal is tested.
- Academy contract-first: edit `academy/api/openapi.yaml`, `make gen`, then tests, then handlers. Generated files
  (`*.gen.go`, `wire_gen.go`, `web/src/api/schema.d.ts`, `web/src/routeTree.gen.ts`) are committed; `gen-check` fails
  when stale. httpapi's TestMain fails when a documented response has no contract test. Module layering is enforced by
  depguard in `academy/.golangci.yml` (transport only in `httpapi`; `domain.go`/`service.go` never import adapters,
  `database/sql` or `os/exec`; `internal/platform` imports no module).
- Academy security (`httpapi/security.go` `guard`, `internal/identity`): every `/api/` and `/ws/` request needs the
  `academy_session` cookie (401 `/problems/no-session`) except `POST /api/v1/sessions`; every change also needs
  `X-CSRF-Token` (403 `/problems/bad-csrf`); a change or WebSocket upgrade whose Origin/Sec-Fetch-Site is another
  site gets 403 `/problems/cross-origin`. Handlers read the user with `principal(ctx)`. Guard-made statuses are
  documented per operation with `components/responses` (`NoSession`, `Forbidden`) so the contract tests cover them.
  Tests sign in with `signIn(t, srv)` and send as the page (`browser.send`). Timestamps are stored with
  `sqlite.FormatTime` (fixed-width UTC: text order = time order). A module's queries get their own sqlc entry
  (`sqlc.yaml`, `<<: *go` + `out`).
- Academy content (`internal/catalog`, `internal/contentimport`): one `content` table, PK (kind, key), the entry's
  bundle document in `doc`. `contentimport.Entries` flattens a bundle, `Diff` compares (`Entry.Same`: document,
  position, parents), only changes are written (same packs = no write at all, timestamps kept); missing entries are
  retired, never deleted. Imported at start (a failure is logged, the last catalogue served) and by `POST
  /api/v1/admin/import` (422 `/problems/invalid-packs` with `errors`, 503 `/problems/toolchain` for `ErrCompile`). Reads
  (`catalog.Service`: `GET /courses`, `/courses/{course}`, `/courses/{course}/items/{item}`, 404 `/problems/not-found`
  for unknown or retired) decode documents into view types (`CourseView`, `LabView`, ...) that have no field for what
  is earned: quiz keys/explanations (a quiz item gives only pass, xp, limit, question count; questions come with an
  attempt), solutions, check/test probes, hint texts (counts only). New content fields reach the API only by being
  added to a view; the API's view schemas are `additionalProperties: false`. Texts keep their placeholders until
  `labstate` resolves them.
- Academy compiler (`internal/compiler`, Phase 11 split step 2): compiles the content folder (`--content`, default
  `<root>/labs`: sub-folders with a pack.yml, each read through an `os.Root`) into the bundle; contentimport calls it
  in-process, `academy compile [--json] [pack...]` prints a summary or the bundle (`make check` runs it). Content
  hashes must stay byte-identical to Python's (Phase 10 and the Python compiler recorded them; a changed hash flags a
  pass as stale): YAML is read as PyYAML's safe_load did (`yaml.go`: YAML 1.1 scalars, merge keys, `testdata/yaml11.*`
  pins Python's reading) and hashed over `pyjson.go`'s bytes (Python's `json.dumps(sort_keys, compact, ensure_ascii)`,
  float repr); the bundle is written the same way. Never change either without a hash test proving old content keeps
  its hash. Scenario steps are OpsForge's: queued as `lab.StepSet`s (probes and challenge actions refuse faults) and
  checked in one `api validate-steps` call; their errors take the queued place. Golden: `academy/testdata/
  bundle.golden.json` (`ACADEMY_UPDATE_GOLDEN=1 go test ./internal/compiler`, review the diff).
- Academy terminals (`internal/terminal`): sessions outlive their socket for `Linger` (60 s) and replay the last 64 KiB;
  the newest socket takes a session over. Wire format = Phase 10's (binary out; `{t:"i",d}` / `{t:"r",cols,rows}` in);
  close codes 1000 exited, 4000 replaced, 4001 slow, 1001 shutdown (`httpapi.StatusReplaced/StatusSlow`). Every
  request passes `hostGuard` (loopback names only, 421 otherwise) and the WebSocket upgrade checks Origin. A session's
  shell is a WebSocket to OpsForge's `/v1/terminal` (`opsforgeapi.Shells`), bash on the lab host in the attempt's
  workspace; closing it hangs the shell up. Shells are hung up through `RegisterOnShutdown` (hijacked sockets are not
  waited for by `Shutdown`). Leftover shells on the lab host: `pgrep -f api-bashrc` (the pattern matches the checking
  shell itself).
- Academy web look (2026-10-25, Udemy/LeetCode family): tokens in `web/src/styles/app.css` keep Phase 10's names with
  new roles (`paper` ground #f7f8fa, `surface` white cards, `enamel` subtle fill, `ink`, `rule` 1px borders, `band` the
  dark course title band) plus `ember` (#e8590c, the one hue: the learner's own progress, the current item, streak and
  heatmaps) and `easy/medium/hard`. Chrome stays colourless, the primary button is near-black, cards are 1px `rule` with
  no shadow; IBM Plex Sans everywhere, Source Serif 4 only for lecture prose, JetBrains Mono only for code. No tracked
  uppercase labels, no zebra rows, no 2px ink borders or offset shadows.
- Academy web pins: `typescript ~6.0` (TS 7 has no JS API for typescript-eslint), `eslint ^9` (jsx-a11y). The shadcn
  CLI may write a wrong `cn` import: check every `shadcn add`. MSW 3: `onUnhandledFrame`. The API client looks up
  `fetch` per call (MSW). Theme = `data-theme` on `<html>` (none = system); `dark:` utilities follow the same rule.
  A Go test that re-executes itself as a fake CLI sets `GORACE=atexit_sleep_ms=0` (else +1 s per call under -race).
- Academy web screens (M2, `web/src/features/{shell,home,roadmap,catalog,course,practice,profile,settings,player,
  lecture,quiz,lab,challenge,editor,terminal,labmap}`): routes `/` (home), `/roadmap`, `/courses`, `/courses/$course`,
  `/practice`, `/profile`, `/settings`, `/learn/$course/$item` (the player brings its own chrome; the root shell steps
  aside for `/learn/`; labs and challenges fill the window and fold the course content away). Data the API has no
  endpoint for yet comes from `web/src/mock/` (one import per screen, replaced by a query when its module lands; the
  mock quiz keys exist only because nothing is scored server-side yet). The editor and terminal are placeholders with
  Monaco's and xterm's props (`CodeEditor`, `TerminalPane`). jsdom lays out at 0×0, so react-resizable-panels takes
  any click inside a panel for a divider grab: tests focus inputs directly. The theme is a store (`useTheme`,
  `setTheme`) shared by the header toggle and settings. `pnpm dev`'s proxy sends the platform's Host and Origin. The API client
  (`src/api/client.ts`) opens the session itself (GET /me, else POST /sessions), adds `X-CSRF-Token` to changes and
  repeats a read once after a 401; `queries.*` reject with `ProblemError` (not retried below 500). Lecture Markdown:
  react-markdown + remark-gfm + rehype-sanitize (no raw HTML; `[!NOTE]` alerts via `data-alert`), Shiki through
  `shiki/core` with the languages listed in `highlight.ts` (the full bundle adds 13 MB to the binary). Quiz, lab and
  challenge items run full screens on mock runtime data until their backends exist. Page tests render the whole route tree with
  `renderRoute(path)` against MSW fixtures (`src/test/server.ts`, the demo course); `src/test/setup.ts` stubs what
  jsdom lacks (matchMedia, pointer capture, scrollIntoView). The e2e suite serves
  `academy/testdata/packs` (`--content`; the fake OpsForge finds every step valid). Phones (< 900 px): the course content is a drawer that closes on a choice.
- Academy learner modules (M1): every domain event goes through `history.Service.Publish` (events table, then
  `platform.Hub` → SSE `history` with the id for Last-Event-ID replay, then handlers); kinds keep Phase 10's dotted
  names. Completion is read, not derived from events: progress takes a `progress.Source` per module (cmd/academy
  adapts quiz/labsession passes: quizzes by `<course>/<item>`, labs by lab id), so stale = completion hash ≠ item
  hash. Modules needing what views hide (keys, probes, setup) read raw entries with `catalog.Service.Content`/`Entry`,
  never the transport. Long lab work = `operations.Service.Start` (202 + SSE `operation`, `GET /operations/{id}`;
  `done` with the outcome even when a setup failed, `failed` only for internal errors); labsession holds one busy lock
  (TryLock, refused not queued) from the request until its background work ends, and hands it over explicitly.
  Restart: `ops.Recover` fails running operations, `labs.Recover` makes setup attempts running (abandonable);
  shutdown closes the hub (SSE streams) and waits for operations within the grace. Workspaces: on the lab host
  (OpsForge's `/v1/workspaces/<lab-N>`, `opsforgeapi.Files`; the learner also edits them in the terminal), 1 MiB/file,
  10 MiB/tree, saves against `base_sha256` (Academy's); the terminal starts there. Guard 401/403 contract coverage is generic
  (`guard_test.go` walks the spec); httpapi tests share one database through `newWorld(t)` (golden catalogue + a
  quiz lab, scripted lab controller). Lab XP = `labsession.PassXP` (half-to-even, as Phase 10's `pass_xp`).
- Academy rewards/labstate/legacy (end of M1): `rewards` stores only awards (`user_badges`); XP is read (best pass per
  lab + best quiz attempt per item) through `rewards.Sources` (cmd/academy adapts labsession, quiz, progress,
  identity). Rule types = `rewards.Rules` ↔ `compiler.ruleTypes` (keep both); a course's `badge` is awarded on
  completion. Badges are evaluated on `lab.passed`/`quiz.submitted`/`course.completed` and for every user after each
  import (cmd/academy's `importer` wraps contentimport), so new rules are retroactive; retired badges stay shown once
  awarded (`catalog.Service.Entries` includes retired entries). `labstate` caches `state` 5 s (a `lab.*` event
  invalidates it) and resolves texts with `state`'s `placeholders` table (Python's `scenario.placeholders`, i.e.
  `substitute` itself: no Go port) plus `${ns}` from the learner's attempt; httpapi resolves item views, attempt
  hints and quiz choices (the generated `LabState` type forced the port name `httpapi.Lab`). `academy migrate-legacy`
  (`internal/legacy`, a one-off data migration that writes several modules' tables in one transaction, like a goose
  migration) remembers imported Phase 10 ids in `legacy_rows`, so running it again imports only what is new; then
  `progress.Refresh` + `rewards.Evaluate` (its rows came without events).
- Shell: bash, `set -euo pipefail`, tabs, must pass shellcheck. YAML must pass `yamllint` (`.yamllint`).
- After a milestone: its acceptance met, a CHANGELOG.md entry with measurements, docs and this file updated.
- Commit messages: `feat(<scope>): <message>`. Default branch: `master`.

## Verify

```bash
make check             # lint, contract pin, Go tests, pack compile, web checks, gen-check (real API: ACADEMY_OPSFORGE_*)
make e2e               # Playwright against the built binary and the fake OpsForge API
make academy && ./academy/bin/academy serve --content labs   # http://127.0.0.1:8090/ with OpsForge's API running
#   (copy its token first: install -m 600 ../opsforge/.opsforge/api-token ~/.local/share/opsforge-academy/opsforge-token)
```
