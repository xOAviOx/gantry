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

## M3 — Queue hardening + webhooks  ✅ DONE (2026-07-16)
- [x] `FOR UPDATE SKIP LOCKED` claim, worker pool, poll w/ jitter (claim was already M1; hardened here)
- [x] Per-project advisory-lock serialization — `pg_try_advisory_lock(hashtext('gantry:project:'||id))` on a dedicated conn held for the job; contention → requeue +10s (attempt not counted)
- [x] Supersession + cooperative cancel — `EnqueueDeploy` supersedes queued jobs+deployments and flags a running one; worker polls the flag, cancels the job's context (cause = superseded|canceled), which kills the `docker build`/`git` subprocess and cleans the temp dir
- [x] Heartbeats + reaper (stale by `locked_at`) + exponential backoff; workers refresh `locked_at`; reaper requeues (retries left) or fails (exhausted)
- [x] `POST /webhooks/github` — HMAC `X-Hub-Signature-256` verify, push-only, per-project branch filter, repo-url match, delivery dedupe (`webhook_deliveries` + jobs `dedupe_key`), 202 fast
- [x] `POST /api/deployments/{id}/cancel` (shares the cooperative-cancel mechanism; reason=canceled)
- [x] Tests: unit (HMAC verify, repo-url normalize/match) + integration `-tags=integration` (concurrent-claim, supersession, reaper)
- [x] README: smee.io / cloudflared webhook forwarding
- [x] Migration 0002 (`jobs.cancel_reason` + partial index); config knobs for the cadences
- [x] go vet / go test / integration / gofmt / tsc --noEmit clean

**DoD** — all passed:
- [x] back-to-back deploys → first `superseded` & stops
- [x] `kill -9` mid-build → reaper requeues → completes
- [x] bad signature → 401; replayed delivery → deduped

_Evidence (2026-07-16, controld run with GANTRY_WORKERS=1, CANCEL_POLL=400ms, JOB_STALE=6s, REAPER_INTERVAL=3s):_

```
# DoD: webhook (secret change-me-webhook-secret; project acmewidgets -> github.com/acme/widgets @ main)
bad X-Hub-Signature-256           -> 401 {"error":"invalid signature"}
ping event (valid sig)            -> 200 {"ok":true}
push  (valid sig, delivery del-1) -> 202 {"branch":"main","queued":1}
replay (same delivery del-1)      -> 202 {"deduped":true}
=> acmewidgets has exactly 1 deployment: trigger=webhook status=build_failed sha=abc123def4  (fake repo; replay added none)

# DoD: supersession (back-to-back deploys on hellom2)
A claimed -> 'building'; B enqueued while A in-flight
A: building -> superseded   (job superseded, cancel_reason=superseded, error "superseded by a newer deploy")
B: queued -> building -> live   (job done)

# DoD: kill -9 mid-build -> reaper requeues -> completes
deploy C -> caught at 'building' -> taskkill /F controld
  orphaned job 41: status=running attempts=1 locked_by=Avi-w0 (lock frozen)
restart controld -> reaper log: WARN "reaper swept stale jobs" requeued=1 failed=0 reconciler=true
  C: building -> ... -> live ; job 41 status=done attempts=2 ; curl Host: hellom2.apps.localhost -> "hello from gantry"

# explicit cancel endpoint
deploy D -> 'building' -> POST /deployments/D/cancel -> 202 {"canceling":true} -> D=canceled (job canceled, reason canceled)

# integration tests (make it)
ok queue  TestConcurrentClaimNoDoubleClaim · TestEnqueueDeploySupersedes · TestReaperRequeuesStale
# unit: TestValidSignature · TestNormalizeRepo · TestRepoMatches (+ M2 suites)
# gates: go vet clean · gofmt clean · tsc --noEmit clean
```

Notes: exponential backoff (`Fail`) applies to jobs that *return* errors; a deploy pipeline records its own terminal deployment status and the pool sets the job's terminal status (done/failed/canceled/superseded) from the context cause, so a deploy that fails the app is not a job retry — the retry surfaces are worker death (reaper) and lock contention (requeue). Decision D18 added. Advisory locks auto-release when a killed worker's connection closes, so a reaped job can re-acquire its project lock. Cadence knobs (`GANTRY_REAPER_INTERVAL`, `GANTRY_JOB_STALE`, `GANTRY_HEARTBEAT`, `GANTRY_LOCK_RETRY_DELAY`, `GANTRY_CANCEL_POLL`) default to the spec values; lowered here only to keep the DoD fast.

---

## M4 — Zero-downtime + rollback + env  ✅ DONE (2026-07-16)
- [x] Blue/green with health gating + 10s drain; failed health keeps old live (pipeline from M1; verified zero-downtime here under load)
- [x] Rollback (skip-build, reuse retained image) — `POST /api/projects/{id}/rollback {deployment_id}`; identical blue/green path, trigger=rollback
- [x] AES-256-GCM env vars (random nonce per value), write-only UI, reveal audited — `internal/secret` box; `env_vars` stores ciphertext+nonce; decrypt only at container-create; values never logged
- [x] Env restart (skip-build redeploy) — `POST /api/projects/{id}/env/restart` reuses the live image; `PUT /api/projects/{id}/env` (set/delete) changes nothing by itself
- [x] Endpoints: `GET/PUT /projects/{id}/env`, `POST /projects/{id}/env/reveal` (audited), `POST /projects/{id}/env/restart`, `POST /projects/{id}/rollback`
- [x] Frontend: write-only env editor (list keys, set/delete, reveal), per-deployment Rollback, "Restart with new env"
- [x] Example `hello-node` gains a `FAIL_HEALTH` toggle to demo a health-gate failure
- [x] Tests: crypto unit (round-trip, fresh-nonce, tamper, wrong-key, key-validation) + env store integration (encrypt→store→list→get→decrypt, ciphertext-only, delete)
- [x] go vet / go test / integration / gofmt / tsc --noEmit clean

**DoD** — all passed:
- [x] 10 req/s during a deploy → zero non-2xx
- [x] broken deploy → `deploy_failed`, old still serving
- [x] rollback restores; env change visible; DB shows only ciphertext

_Evidence (2026-07-16):_

```
# DoD: zero-downtime — 10 req/s through Caddy across a full deploy (build→health→route→10s drain)
total requests: 220   non-2xx: 0   histogram: 220x 200

# DoD: env change visible — write-only PUT then skip-build env/restart
PUT /projects/{id}/env {"set":{"GREETING":"hello from M4 env","APP_VERSION":"2"}} -> keys only (no values)
POST /projects/{id}/env/restart -> live (trigger=env_restart)
curl Host: hellom2.apps.localhost -> "hello from M4 env" / "version: 2"

# DoD: DB shows only ciphertext (AES-256-GCM, per-value nonce)
env_vars: GREETING enc=33 bytes (17 plaintext + 16 GCM tag), nonce=12 ; APP_VERSION enc=17, nonce=12
value_enc is non-UTF8 binary; plaintext substring absent

# DoD: broken deploy → deploy_failed, old still serving
PUT env FAIL_HEALTH=1 ; env/restart -> new container /healthz=500 -> health gate never green
  broken deploy: deploy_failed ; old deployment stays 'live'
  app served 200 "hello from M4 env" throughout and after

# DoD: rollback restores
edit server.js greeting -> deploy (full build) -> live "hello from gantry V2"
POST /projects/{id}/rollback {deployment_id: <baseline>} -> trigger=rollback, image_tag=gantry/hellom2:d-d0cdabe8 (retained), skip-build
  -> live "hello from gantry"   # restored

# tests: secret (round-trip/nonce/tamper/wrong-key/validation) · store env integration · (+ M2/M3 suites)
# gates: go vet · gofmt · go test · integration · tsc --noEmit  — all clean
```

Notes: env vars are applied at container-create, not baked into the image, so rollback restores the *code* (image) while env stays current — matching the spec (rollback reuses a retained image; env changes need a redeploy). Reveal is `POST .../env/reveal` (key in body, not URL) and logged at `warn` with project+key only. `secret.New` failure disables env features (API returns 503) rather than running insecurely. Decision D19 added.

---

## M5 — Reconciliation  ✅ DONE (2026-07-16)
- [x] 30s loop (`deploy.Reconciler`): recreate missing/dead live containers (advisory-locked, env-injected, health-gated), reap orphan gantry containers, reload Caddy on drift
- [x] controld restart fully re-renders from DB (`bootLoadCaddy` → `RenderAndLoad`, already on boot; verified)
- [x] Healing actions logged at `warn` with `reconciler=true`
- [x] Infra safety: only containers carrying a `dev.gantry.deployment` label are eligible for reaping, so `gantry-postgres`/`gantry-caddy` (managed-labeled but not deployment-labeled) are never touched
- [x] Orphan protection: reaps only deployments in a terminal state — live + in-flight deploys are protected, so a mid-deploy container is never removed
- [x] Drift check via host/dial markers in Caddy's live `/config/`; unit-tested; `GANTRY_RECONCILE_INTERVAL` config knob
- [x] go vet / go test / gofmt / tsc --noEmit clean

**DoD** — all passed:
- [x] `docker rm -f` live container → healed <30s
- [x] wipe Caddy config → restored <30s
- [x] restart controld → routes intact

_Evidence (2026-07-16, controld run with GANTRY_RECONCILE_INTERVAL=5s except where noted):_

```
# DoD: docker rm -f live container -> healed
docker rm -f gantry-hellom2-bd026553  -> app via Caddy = 502
reconciler log: recreating live container (slug=hellom2) -> started -> healthy (HTTP 200) -> "healed live container"
app 200 again within a couple seconds ; new container gantry-hellom2-bd026553 Up

# DoD: wipe Caddy config -> restored
POST /load {servers:{}}  -> app + dashboard = 000 (no routes)
reconciler log: WARN "reconciler repairing caddy config" force=false routes=2
restored in ~4s ; app=200, dashboard {"ok":true}

# DoD: restart controld -> routes intact (reconciler set to 300s to isolate the BOOT render)
stop controld -> wipe Caddy -> app=000
start controld -> log "caddy config loaded on boot" -> app=200 "hello from gantry", dashboard=200
  (routes restored by boot re-render from DB, before the reconciler could run)

# Orphan reap + infra safety
plant gantry-orphan-test (managed + deployment=0000... not in DB)
  -> WARN "reconciler reaping orphan container" deployment=0000... -> removed
  -> gantry-postgres / gantry-caddy / live app containers all still Up (never eligible: no deployment label / protected)

# unit: TestHasAllMarkers (drift detection) ; gates: vet · gofmt · go test · tsc — clean
```

Notes: Caddy dials apps by container *name* on the `gantry-apps` network, so a same-name recreate is reachable via Docker DNS without a Caddy change — the reconciler still reloads after a heal (force) to refresh upstreams, and the healed deployment's `host_port` (controld's own health port) is updated in the DB. Recreation takes the project advisory lock, so it never races a concurrent deploy. Decision D20 added.

---

## M6 — GC & disk  ✅ DONE (2026-07-16)
- [x] Image retention — `store.ImageTagsToKeep` (last `GANTRY_KEEP_IMAGES` deploys per project + live); GC removes gantry-labeled images not in the keep set
- [x] Scheduled (`GANTRY_GC_INTERVAL`, default 24h) + on-demand (`POST /api/system/gc`) GC: exited-container cleanup → image retention → dangling prune (label-scoped) → BuildKit cache prune (`ReservedSpace = GANTRY_BUILDER_KEEP_STORAGE`) → `log_lines` purge (`GANTRY_LOG_RETENTION`, default 14d); returns a reclamation report; single-flight via mutex (409 if busy)
- [x] Disk widget — `GET /api/system/disk` summarizes `docker system df` (images/containers/build-cache count+size+reclaimable); home-page widget + "Run GC now" button showing the report
- [x] Infra-safe: exited-container cleanup requires a `dev.gantry.deployment` label (never postgres/caddy)
- [x] Tests: `ParseBytes` unit; store integration (`ImageTagsToKeep` last-N + always-keep-live, `PurgeOldLogs`)
- [x] go vet / go test / integration / gofmt / tsc --noEmit clean

**DoD** — all passed:
- [x] 5 deploys → only last 3 + live images remain
- [x] builder cache under cap after GC
- [x] widget matches `docker system df`

_Evidence (2026-07-16, controld run with GANTRY_KEEP_IMAGES=3, GANTRY_BUILDER_KEEP_STORAGE=0):_

```
# DoD: 5 deploys of a fresh project 'gctest' -> 5 images
gantry/gctest:d-{b167821d(1) bdd85592(2) b5daba80(3) 6d9a5f4f(4) 0b13402a(5,live)}

POST /api/system/gc -> {"images_removed":9,"containers_removed":0,"cache_reclaimed":28672,"log_lines_purged":0,"duration_ms":1330}

# after GC: gctest keeps exactly 3 = last 3 deploys + live
gantry/gctest:d-0b13402a (live/d5) · d-6d9a5f4f (d4) · d-b5daba80 (d3)   # d1,d2 removed
(images_removed=9 also swept older images of other projects beyond their keep-3)

# DoD: widget matches `docker system df`  (before GC)
widget      : images count=22 size=1432.2MB · containers count=7 size=0.1MB · build_cache count=12 size~0
docker df   : Images 22 SIZE 1.432GB · Containers 7 106.5kB · Build Cache 12 28.67kB

# DoD: builder cache under cap after GC (cap=0)
before: Build Cache 12 / 28.67kB   ->  GC cache_reclaimed=28672  ->  after: Build Cache 9 / 0B
widget after: images count=13 (=df 13) · build_cache size=0

# apps stay live through GC (retention keeps live images): gctest=200, hellom2=200
# unit: TestParseBytes ; integration: TestImageTagsToKeep(+AlwaysKeepsLive), TestPurgeOldLogs
# gates: vet · gofmt · go test · integration · tsc — clean
```

Notes: exited containers are removed *before* images so their images become deletable; images are removed with force+prune-children. The disk widget's counts and sizes match `docker system df` exactly; its *reclaimable* is an approximation that clamps the negative value df sometimes reports for images (a shared-layer accounting quirk). GC runs synchronously on the API request (fast: prune ops) rather than through the job queue, with a mutex so the nightly sweep and a dashboard click never overlap — Decision D21.

---

## M7 — Stretch (ask human which)
- [ ] (a) Nixpacks fallback · (b) public mode + on_demand_tls · (c) containerize controld · (d) README polish + demo GIF + chaos section

_Evidence:_ _(pending)_
