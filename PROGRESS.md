# PROGRESS.md — Gantry build tracker

I (Claude Code) maintain this file. One milestone at a time, strictly in order. A milestone is **done** only after its DoD commands are run here and the evidence is pasted below its checklist.

Legend: `[ ]` todo · `[~]` in progress · `[x]` done

---

## Environment (recorded once)

- Host: **native Windows** (MINGW64/Git Bash), repo at `D:\paas`. Not WSL2 — see SPEC.md D6.
- Toolchain: Go 1.23.3, Docker Desktop 27.4.0 (Linux containers), Node 22.19.0, pnpm 11.6.0, GNU Make 4.4, git 2.43.
- Docker endpoint: `npipe:////./pipe/docker_engine` (verified). No `jq` / `migrate` CLI on host (see D8, D10).

---

## M0 — Skeleton  ✅ DONE (2026-07-16)

- [x] Monorepo layout (`apps/web`, `services/controld`, `deploy`, `examples`, `migrations`, `scripts`)
- [x] `deploy/docker-compose.yml`: postgres 16 (`127.0.0.1:5432`) + caddy 2.8 (`80:80`, admin `127.0.0.1:2019`), `gantry-core` + `gantry-apps` networks, `host.docker.internal:host-gateway`, gantry labels
- [x] `deploy/caddy-bootstrap.json`: admin API only (`0.0.0.0:2019`, origins allowlisted)
- [x] `deploy/.env.example`: all env vars documented
- [x] Migrations: initial schema (§6 tables), embedded (`//go:embed`) + golang-migrate `iofs`+`pgx5` runner
- [x] `controld`: config load (+ dotenv), slog, pgx pool, run migrations, chi router, `GET /api/healthz` → `{"ok":true}`, graceful shutdown
- [x] `internal/proxy`: pure Caddy config renderer + admin client (`POST /load`, `GET /config/`), golden + structure tests
- [x] Initial Caddy render/load on controld boot (dashboard routes; retries for slow container)
- [x] Next.js 15 shell (App Router, TS, Tailwind): `/` shell served through Caddy, live health badge
- [x] `Makefile`: `dev`, `up`, `down`, `migrate`, `test`, `it`, `lint`, `fmt`, `typecheck`, `nuke`, `help`
- [x] `go vet` clean; web `tsc --noEmit` clean

**DoD** — all passed:
- [x] `make dev` brings everything up
- [x] `http://paas.localhost` serves the dashboard shell
- [x] `curl http://paas.localhost/api/healthz` → `{"ok":true}`
- [x] `curl -s 127.0.0.1:2019/config/ | jq '.apps.http'` shows controld-rendered routes
- [x] `make migrate` is idempotent

_Evidence (2026-07-16):_

```
# make dev — infra + migrate + controld + web
==> starting infra (postgres + caddy)   [gantry-postgres Running, gantry-caddy Running]
==> applying migrations                 msg="migrations up to date"
==> starting web (:3000)  /  controld (:8080)
level=INFO msg="caddy config loaded" app_routes=0
level=INFO msg="controld listening" addr=:8080
▲ Next.js 15.5.20  ✓ Ready in 1847ms   GET / 200

# DoD 2 — health via Caddy
$ curl -s http://paas.localhost/api/healthz
{"ok":true}

# DoD 3 — dashboard shell (HTTP 200), markers: gantry / Single-node mini-PaaS / Milestone status / served via caddy

# DoD 4 — curl -s 127.0.0.1:2019/config/  (.apps.http, jq unavailable -> node)
server: gantry, listen [":80"]
route[0] host  : ["paas.localhost"]
  /api,/webhooks -> host.docker.internal:8080
  (default)      -> host.docker.internal:3000

# DoD 5 — make migrate idempotent (run twice)
run-A: msg="migrations up to date"
run-B: msg="migrations up to date"
schema_migrations: version=1 dirty=f

# gates
go vet ./...      -> clean
go test ./...     -> ok proxy (golden + structure), rest no-test
tsc --noEmit      -> clean

# labeled infra
gantry-postgres  Up (healthy)  127.0.0.1:5432->5432/tcp
gantry-caddy     Up            0.0.0.0:80->80/tcp, 127.0.0.1:2019->2019/tcp
```

Notes: added decisions D6–D13 in SPEC.md (native Windows env, Docker pipe, embed/module root, Cygwin-make→bash dispatch, Next-via-node). `.localhost` resolves directly on this Windows host, so the literal spec `curl http://paas.localhost/...` forms work as written.

---

## M1 — Manual deploy, end to end  ✅ DONE (2026-07-16)
- [x] Project CRUD API (`POST/GET /api/projects`, `GET /api/projects/{id}`) + auth (token/cookie) + pages
- [x] Full pipeline §8 (clone → build → start → check → route → retire) — Postgres queue + worker pool drive it
- [x] Local-path `repo_url` support; `examples/hello-node` (zero-dep Node) deploys without GitHub
- [x] Build logs persisted to `log_lines` (batched writer + seq); polling endpoint `.../logs?after=<seq>`
- [x] Caddy route live for `<slug>.apps.localhost` (re-render from DB on each deploy)
- [x] Container hardening applied (§12) + gantry labels; `docker build` = BuildKit (D14)
- [x] Dashboard pages: `/login`, `/`, `/projects/new`, `/projects/[id]`, `/deployments/[id]` (live log viewer)
- [x] go vet / go test / gofmt / tsc --noEmit clean

**DoD** — all passed:
- [x] create project for `examples/hello-node` → Deploy → `live`
- [x] `curl -H "Host: hello.apps.localhost" http://127.0.0.1/` returns the app
- [x] build log visible (persisted + polled by the UI's LogViewer)
- [x] `docker inspect` proves CapDrop/memory/log-opts

_Evidence (2026-07-16):_

```
# create + deploy (via API, Bearer ADMIN_TOKEN; 401 without token)
POST /api/projects {slug:hello, repo_url:D:/paas/examples/hello-node, port:3000, health_path:/healthz} -> 201
POST /api/projects/{id}/deploy -> 202; status: queued -> building -> live

# DoD: app via Caddy (Host: hello.apps.localhost)
$ curl -H "Host: hello.apps.localhost" http://127.0.0.1/
hello from gantry / version: 1 / hostname: 92e13011fff4 / port: 3000 / node: v20.20.2
$ curl -H "Host: hello.apps.localhost" http://127.0.0.1/healthz  -> ok

# Caddy route rendered from DB
hello.apps.localhost -> gantry-hello-<deploy8>:3000

# build log persisted (51 lines: 10 system + 41 build[BuildKit]); polled by LogViewer.
# cookie-auth path verified (login 200 -> gantry_session -> logs 51 lines) = exact UI path.

# DoD: docker inspect (container hardening §12)
CapDrop ["ALL"] · Memory 536870912 (512m) · NanoCPUs 1e9 (1.0) · PidsLimit 256
SecurityOpt ["no-new-privileges"] · RestartPolicy on-failure/3
LogConfig json-file max-size=10m max-file=3 · Networks: gantry-apps only (NOT core)
Labels dev.gantry.{managed=true, project=hello, deployment=<uuid>}

# blue/green retire (2nd deploy): new 7427b40f live, old 6ef72bc3 retired + container removed,
# single live per project, Caddy re-pointed to new container. Zero-downtime rigor is M4.
```

Notes: fixed a Docker Desktop race where the ephemeral host port isn't in `NetworkSettings.Ports`
immediately after `ContainerStart` — `waitForHostPort` polls until published. Decisions D14–D16
recorded (docker-build/BuildKit, zero-dep example, plain-Tailwind+polling UI). Orphan container
cleanup from failed deploys is the reconciler's job (M5); removed manually during testing.

---

## M2 — Live logs  ✅ DONE (2026-07-16)
- [x] SSE log hub (batch writer + pub/sub + fanout) — generic `broker[T]`; `Writer.Line` publishes each line to live subscribers under the same seq lock as the batch persister (strict order); brokers GC'd once no writer is producing and no subscriber is reading
- [x] `GET /api/deployments/{id}/logs` — subscribe-before-backlog, replay `seq > Last-Event-ID`, dedupe overlap, live stream with SSE `id`=seq, 15s heartbeat comment, per-client buffer 1000 → `gap` event + drop on overflow
- [x] `GET /api/deployments/{id}/events` — status stream: current-state snapshot on connect + every pipeline transition (orchestrator emits after each status write)
- [x] Log viewer UI: native `EventSource` (`log` + `gap`), windowed/virtualized rows (fixed 18px, bounded 5000-line tail), autoscroll that releases on manual scroll-up; deployment page status now via `/events` (interval polling removed)
- [x] Tests: broker (fanout order, overflow-drop, unsubscribe) + SSE wire (Last-Event-ID precedence, `log`/`status` framing, headers)
- [x] go vet / go test / gofmt / tsc --noEmit clean

**DoD** — all passed:
- [x] lines stream live during a build
- [x] hard-refresh resumes via Last-Event-ID
- [x] two tabs stream concurrently

_Evidence (2026-07-16):_

```
# Deploy examples/hello-node (slug hellom2); two /logs SSE + one /events attached at enqueue.

# DoD: two tabs concurrently — both received the full stream, no drops
tab1: 57 "event: log"   tab2: 57 "event: log"   gap events: 0 / 0
frame shape:  id: 1 \n event: log \n data: {"seq":1,"stream":"system","line":"=== deploy 03704bb4 ..."}
seq 1..57, last = {"seq":57,"stream":"system","line":"=== LIVE at http://hellom2.apps.localhost/ ==="}

# DoD: live status stream (/events attached right after enqueue)
queued -> cloning -> building -> building -> starting -> checking -> routing -> live
(two `building` = emit at builder-start callback + after image recorded)

# DoD: Last-Event-ID resume (first deploy persisted 57 lines)
Last-Event-ID: 30  -> replays id 31..57  (27 lines)   # exactly seq>30
no header          -> replays all 57                  # default 0

# heartbeat: 1 `: ping` comment after 17s idle on a settled deployment
# gate: go vet clean · go test ok (api, build, deploy, logs, proxy) · gofmt clean · tsc --noEmit clean
```

Notes: gap-on-overflow is exercised by the broker unit test (a full subscriber is dropped and its `Dropped()` closes on the next publish); the handler turns that into an `event: gap` + disconnect. Decision D17 added (SSE/virtualization choices). Also fixed a **pre-existing M1 bug** found while running the DoD: `store.ListProjectsWithStatus` selected unqualified `id`/`created_at` while joining `projects` with a `live` (has `id`) and `latest` (has `created_at`) subquery → `GET /api/projects` failed with `column reference "id" is ambiguous` (SQLSTATE 42702), breaking the dashboard home. Qualified the projected columns with `p.`; endpoint now returns 200.

---

## M3 — Queue hardening + webhooks
- [ ] `FOR UPDATE SKIP LOCKED` claim, worker pool, poll w/ jitter
- [ ] Per-project advisory-lock serialization
- [ ] Supersession + cooperative cancel (build actually stops)
- [ ] Heartbeats + reaper (60s stale) + exponential backoff
- [ ] `POST /webhooks/github`: HMAC verify, branch filter, delivery dedupe, 202 fast
- [ ] Queue unit tests (concurrent-claim, supersession, reaper)
- [ ] README: smee.io / cloudflared webhook forwarding

**DoD:** back-to-back deploys → first `superseded` & stops; `kill -9` mid-build → reaper requeues → completes; bad signature → 401; replayed delivery → deduped; tests pass.

_Evidence:_ _(pending)_

---

## M4 — Zero-downtime + rollback + env
- [ ] Blue/green with health gating + 10s drain; failed health keeps old live
- [ ] Rollback (skip-build, reuse retained image)
- [ ] AES-256-GCM env vars (per-value nonce), write-only UI, reveal audited
- [ ] Env restart (skip-build redeploy)

**DoD:** 10 req/s loop during deploy → zero non-2xx; broken build → `deploy_failed`, old still serving; rollback restores; env change visible; DB shows only ciphertext.

_Evidence:_ _(pending)_

---

## M5 — Reconciliation
- [ ] 30s loop: recreate missing live containers, reap orphans, re-render Caddy on drift
- [ ] controld restart fully re-renders from DB
- [ ] Healing actions logged at `warn` with `reconciler=true`

**DoD:** `docker rm -f` live container → healed <30s; wipe Caddy config → restored <30s; restart controld → routes intact.

_Evidence:_ _(pending)_

---

## M6 — GC & disk
- [ ] Image retention (`GANTRY_KEEP_IMAGES` + live)
- [ ] Scheduled + on-demand GC: dangling prune, BuildKit cache cap, log purge (14d), exited-container cleanup
- [ ] Disk widget (`docker system df`) + "Run GC now"

**DoD:** 5 deploys → only last 3 + live images remain; builder cache under cap after GC; widget matches `docker system df`.

_Evidence:_ _(pending)_

---

## M7 — Stretch (ask human which)
- [ ] (a) Nixpacks fallback · (b) public mode + on_demand_tls · (c) containerize controld · (d) README polish + demo GIF + chaos section

_Evidence:_ _(pending)_
