# Gantry

A single-node mini-PaaS. Point it at a Git repo (or a local path in dev), and it
clones, builds a Docker image, runs the container with sane security defaults,
health-checks it, and routes a subdomain to it through Caddy — with live build
logs, blue/green deploys, and a small dashboard. One Go control-plane binary
(`controld`) plus a Next.js dashboard; Postgres and Caddy run in Docker.

See `SPEC.md` for the full design and `PROGRESS.md` for milestone-by-milestone
build status.

## Quickstart (dev)

Requirements: Docker Desktop, Go 1.23+, Node 22+, pnpm, GNU Make.

```
cp deploy/.env.example deploy/.env   # then edit secrets (see below)
make dev                             # postgres + caddy + migrate + controld + web
```

Then open http://paas.localhost and log in with `ADMIN_TOKEN`. Deployed apps are
served at `http://<slug>.apps.localhost`.

Useful targets: `make up` / `make down` (infra only), `make migrate`, `make test`
(unit), `make it` (integration — needs infra up), `make lint`, `make fmt`.

## Configuration

All configuration is environment variables, documented in `deploy/.env.example`.
The security-relevant ones:

- `ADMIN_TOKEN` — dashboard/API bearer token.
- `GITHUB_WEBHOOK_SECRET` — HMAC secret for the GitHub webhook (see below).
- `GANTRY_MASTER_KEY` — base64 32-byte key for env-var encryption (M4).

Queue tuning (defaults match the spec; mostly for tests/demos):
`GANTRY_WORKERS` (2), `GANTRY_REAPER_INTERVAL` (30s), `GANTRY_JOB_STALE` (60s),
`GANTRY_HEARTBEAT` (15s), `GANTRY_LOCK_RETRY_DELAY` (10s).

## GitHub webhooks

Gantry deploys automatically on push. It exposes:

```
POST /webhooks/github
```

The endpoint has no auth middleware — it authenticates each request by verifying
the `X-Hub-Signature-256` HMAC against `GITHUB_WEBHOOK_SECRET`. Only `push`
events are acted on, and only for projects whose configured branch matches the
pushed ref. Deliveries are deduplicated by `X-GitHub-Delivery`, and the handler
responds `202` immediately — the build runs in the queue.

Configure the webhook in your repo under **Settings → Webhooks**:

- **Payload URL**: your public Gantry URL + `/webhooks/github`
- **Content type**: `application/json`
- **Secret**: the same value as `GITHUB_WEBHOOK_SECRET`
- **Events**: just the `push` event

### Forwarding to localhost during development

GitHub can't reach `paas.localhost`, so forward its deliveries to your machine
with either of these.

**smee.io** (no account needed):

1. Open https://smee.io/new and copy the channel URL it gives you.
2. Use that channel URL as the webhook **Payload URL** in GitHub.
3. Run a forwarder that relays deliveries to your local endpoint:

   ```
   npx smee-client --url https://smee.io/<your-channel> \
     --target http://paas.localhost/webhooks/github
   ```

**cloudflared** (public HTTPS tunnel):

1. Start a quick tunnel to Caddy:

   ```
   cloudflared tunnel --url http://paas.localhost
   ```

2. Cloudflare prints a `https://<random>.trycloudflare.com` URL. Set the
   webhook **Payload URL** to `https://<random>.trycloudflare.com/webhooks/github`.

Either way, keep the signature secret in GitHub identical to
`GITHUB_WEBHOOK_SECRET`, or Gantry will (correctly) reject the delivery with
`401`.
