# GPUHub

**Self-hosted orchestration platform for FREE-tier, compute/notebook GPU providers.**
One web UI, one REST API, one job queue - many free GPU clouds. GPUHub schedules GPU workloads
only onto providers whose free tier offers compute (notebook/instance) GPUs. Inference APIs
and paid/trial-credit-only providers are out of scope by design.

```
┌─────────────┐   HTTPS    ┌────────────────────────────── GPUHub (Go) ─────────────────────────────┐
│  Browser /  │◀──────────▶│  embed SPA (React)  ·  /api/v1 (chi)  ·  /docs (Swagger)               │
│  curl / CI  │            │      │                                                               │
└─────────────┘            │      ├── auth (JWT + refresh, bcrypt, rate-limit, CSRF header)       │
                           │      ├── providers registry (32 adapters)                             │
                           │      ├── proxy manager  ← STICKY 1:1 binding, transport cache        │
                           │      ├── jobs scheduler ← filter/sort/failover, SQLite queue          │
                           │      └── notify/audit ──▶ notifications, audit_log, webhook           │
                           │      ▼                                                               │
                           │  ┌─────────────────┐  ┌────────────────┐                              │
                           │  │ SQLite (WAL)    │  │ External free  │  enforce via                 │
                           │  │ /data/gpuhub.db │  │ GPU providers  │  x-bound proxies             │
                           │  └─────────────────┘  └────────────────┘                              │
                           └───────────────────────────────────────────────────────────────────────┘
```

## Run it (5 lines)

```bash
cp .env.example .env                      # then edit: GPUHUB_MASTER_KEY (openssl rand -base64 32), ADMIN_PASSWORD
docker compose up --build -d
open http://localhost:8080                # or https://your-domain via Caddy
```

If `ADMIN_PASSWORD` is set, the admin is created on first boot; otherwise the UI shows a one-time setup wizard. Login → add a proxy → add a provider key bound to that proxy → submit a job with `gpu_type` + `min_vram_gb`.

Smoke test: `./scripts/smoke.sh` (login → create proxy → add Kaggle key → submit T4 job → fetch detail).

**What to expect on a first run:** the sample proxy in `smoke.sh` (`127.0.0.1:3128`) is intentionally unreachable - GPUHub's health loop will mark it unhealthy within 60s, deactivate any key bound to it, write an `audit_log` entry and a critical notification ("NO automatic rebinding performed"), and the job will end `unschedulable` with per-provider rejection reasons. That is the *correct* system behavior, not a bug: GPUHub never silently rebinds keys.

**Re-enabling after a health-check disable:** fix/replace the proxy (or assign a new one via `PATCH /api/v1/keys/{key_id}/proxy`), then re-activate the key in the Keys page (`active` toggle) and resubmit the job.

## Contents
- [Architecture](#architecture)
- [GPU type specification & matching rules](#gpu-matching-rules)
- [Multi-key management & rotation](#multi-key-rotation)
- [STICKY PROXY binding model](#sticky-proxy-binding)
- [Job lifecycle & failover](#job-lifecycle--failover)
- [Providers & free-tier verification](#providers-verification-2026-11)
- [REST API + curl example](#rest-api)
- [Security](#security)
- [Configuration](#configuration)
- [Tests](#tests)
- [Known limitations & roadmap](#known-limitations--roadmap)

## Architecture

Single Go binary (`cmd/gpuhub`), modular monolith:

| Package | Responsibility |
|---|---|
| `internal/api` | chi router, handlers, middleware (JWT, rate limit, CSRF header), WebSocket log streaming, SPA embedding |
| `internal/auth` | JWT access (15m) + refresh tokens (7d, rotating, sha256-hashed at rest), bcrypt |
| `internal/config` | `config.yaml` + env overrides (env wins) |
| `internal/crypto` | AES-256-GCM box for provider credentials and proxy URLs |
| `internal/db` | modernc.org/sqlite (pure Go, no CGO), WAL, `foreign_keys=ON`, `busy_timeout=5000`, embedded goose-style migrations applied at boot |
| `internal/providers` | `Provider` interface + 32 adapters + registry + GPU catalog |
| `internal/proxy` | Sticky binding enforcement: per-key pinned `http.Transport` cache, proxy CRUD, 60s health loop |
| `internal/jobs` | GPU matching, scheduler (filter → sort → submit), poll loop, failover, cost cap, timeout enforcement |
| `internal/audit` | audit_log helper |

Frontend: React 18 + Vite + TypeScript + Tailwind SPA (`web/`), embedded via `go:embed` (`web/web.go`), dark theme, Recharts cost chart, TanStack-style query patterns (hand-rolled), WebSocket live logs. Pages: Dashboard, Providers, Proxies, Jobs (+detail), Keys, Settings, Audit Log.

Default login has no users until bootstrap; see **Security**.

## GPU matching rules

Every job carries mandatory GPU requirements (API and UI both enforce):

```json
{
  "type": "script|notebook|docker|command",
  "payload": "...",
  "gpu_type": "A100",                     // or T4 P100 V100 A10 A100 H100 H200 L4 L40S RTX4090 RTX6000Ada RTX6000Pro B200 B300 MI300X Gaudi2 M4000 P4000 any
  "gpu_type_fallback": ["H100", "L40S", "T4"],   // OPTIONAL ordered fallback chain - tried in order when gpu_type is unavailable
  "min_vram_gb": 16,                      // integer GB; required (≥1)
  "gpu_count": 1,                         // 1..8
  "session_renew": false                  // true → scheduler re-queues on unexpected death; payload sees GPUHUB_ATTEMPT/GPUHUB_RESUME envs
}
```

Canonical VRAM table (`internal/providers/gputable.go`, updated 2026-11 research pass):
T4=16, P100=16, V100=32, A10/A10G=24, A100(80GB variant canonical)=80, H100=80, H200=141, L4=24, L40S=48, RTX4090=24, RTX6000Ada=48, RTX6000Pro=96, M4000=8, P4000=8, MI300X=192, B200=180, B300=288, Gaudi2=96.

Scheduler filter per provider:
1. `gpu_type != "any"` → provider must offer that exact canonical type; `gpu_type_fallback` entries are tried left→right in order when the primary type has no match.
2. Any offering GPU must have `vram_gb >= min_vram_gb`.
3. `gpu_count <= max_count` of the offering.
4. The lowest-VRAM fitting GPU of the highest-priority requested type wins (least waste).
5. Provider must be enabled AND have ≥1 eligible key (active, not in cooldown, not over local daily/weekly quota - proxy binding is now OPTIONAL, see below).
6. `provider_preference: ["modal"]` restricts candidates to the listed ids.
7. Provider-side quota ≥ 90% watermark (`GPUHUB_QUOTA_HIGH_WATERMARK`) → key skipped.
8. If nothing fits → job marked **`unschedulable`** and the UI shows *why* per provider.

`any` selects the most suitable offering by quota headroom + priority.

**VRAM-only jobs**: set `gpu_type: "any"` and `min_vram_gb: N` - the scheduler picks any GPU with ≥N GB. Combine with `provider_preference` to constrain where. **GPU chains**: `gpu_type_fallback` gives ordered fallbacks *within one request* - e.g. `"gpu_type": "A100", "gpu_type_fallback": ["H100", "L40S"]` runs on A100 if offered, else H100, else L40S - useful when free tiers bounce availability.

## Multi-key rotation

Each provider accepts N keys. Every key has: `label`, `credential` (AES-256-GCM encrypted), `bound_proxy_id` (**now OPTIONAL** - omit for direct egress; 1:1 binding when present), `daily_quota` / `weekly_quota` (local accounting via `key_usage`, auto-reset per day/ISO-week), `priority` (lower = preferred), `weight` (for weighted rotation), `active`, `cooldown_until`, `error_count`, `consecutive_errors`.

Key rotation (never proxies) strategies, chosen in Settings (`rotation_strategy` / `scheduler_strategy`):

- `round_robin` - least-recently-used key first
- `least_used` - lowest `usage_today` first
- `most_quota` - most remaining daily/weekly allowance first (default)
- `weighted` - weighted random by `weight`

Failure handling: on submit error (429/5xx/network) the key gets `consecutive_errors++` and `cooldown_until = now + 2^min(n,6)` minutes (2,4,8,16,32,64). At 5 consecutive errors the key is **auto-disabled** and the admin is notified (`notifications` + optional webhook). Success resets the counter. Cooldown expiry is the only automatic way back.

## STICKY PROXY binding

**Rule: a proxy, once bound to a key, stays bound - forever - until (a) an admin manually reassigns it, or (b) the proxy is deleted or fails health checks. GPUHub never rotates proxies and never auto-rebinds keys.**

> **Binding is optional since 2026-11.** A key with no proxy uses direct egress (no pinning, no sticky transport - but also no proxy protection). The 1:1 sticky rules below apply only to keys WITH a proxy bound. Proxy deletion behaves as before: bound keys become proxy-less, disabled, and are NEVER auto-rebound. Admins can unbind via `PATCH /api/v1/keys/{key_id}/proxy` with `{"proxy_id": 0}` - the key stays active on direct egress.

```
┌───────────┐        1:1 (UNIQUE index on bound_proxy_id WHERE NOT NULL)
│  proxies  │◄─────────────── bound_proxy_id ────────────────┐
│  id,label │                                                │
│  url(enc) │        ┌────────────────────┐                  │
│  country  │        │   provider_keys    │                  │
│  health   │        │  id, provider_id   │                  │
└─────┬─────┘        │  credential_enc    │                  │
      │              │  bound_proxy_id ───┼──────────────────┘
      │              │  active, quotas... │
      │              └────────────────────┘
      │                       │
      ▼                       ▼
 health loop (60s)      per-key *http.Transport cache
 tests bound proxies    proxy URL pinned ONCE per key_id
                        identical pool reused for every request
```

Assignment flows:
1. **Key creation** requires `bound_proxy_id` of an *unbound* proxy. If the proxy is already bound → 409 with the owning key id/label; admin may retry with `force: true` on reassign to detach the old key (it's disabled + audit-logged) and bind the new one.
2. **Manual reassign** - `PATCH /api/v1/keys/{key_id}/proxy` (admin only), body `{"proxy_id": N, "force": false}`. Audit-logged as `proxy_reassign` / `proxy_reassign_force`. The transport cache is invalidated for the two affected keys.
3. **Proxy deletion** - `DELETE /api/v1/proxies/{id}`: bound keys get `bound_proxy_id=NULL, active=0` ("no proxy" state), are **NOT** rebound, admins are notified per key; the deletion is audit-logged. Re-binding is a manual admin action.
4. **Health failures** - every 60s (`GPUHUB_PROXY_HEALTH_URL`), proxies bound to ≥1 key are tested. Unhealthy → proxy marked `unhealthy`, bound keys deactivated, admin notified (`notifications`, webhook, audit). **No automatic rebinding, ever.** Keys stay bound to the failed proxy for traceability; an admin decides the next binding. A `healthy` recovery does NOT auto-reactivate keys - re-enable manually after you trust the proxy again. Health checks are single-strike by design (alert + disable fast); the binding itself never moves.
5. **URL edits** - changing a proxy's URL invalidates cached transports of bound keys (connection pools rebuilt against the new URL; the binding itself stays 1:1).

There is intentionally **no** "proxy rotate" setting anywhere in config, env, UI or API.

GUI surfaces the binding everywhere: Providers → keys table shows Masked credential + bound proxy; Proxies → table shows "Bound key"; Keys → central view with "Reassign proxy" (admin, warning confirmation); delete/reassign actions are audit-logged (`proxy_reassign`, `proxy_reassign_force`, `proxy_delete`, `proxy_health_fail`).

Go enforcement points:

```go
// internal/proxy/proxy.go
mgr.ClientForKey(ctx, keyID)        // only way adapters get an *http.Client
//   └─ TransportForKey → cache[keyID] built ONCE from the key's CURRENT bound proxy
//      └─ socks5: custom DialContext via x/net/proxy · http/https: fixed Proxy func
```

`internal/providers/base.go → proxyClient()` routes every outbound adapter call through this pinned transport. Adapters never see a raw `http.Client`.

## Job lifecycle & failover

States: `queued → scheduling → running → succeeded | failed | cancelled | timeout | unschedulable`.

1. **schedule** - optimistic `UPDATE … SET status='scheduling' WHERE status='queued'` picks the job; candidates are filtered/sorted by rotation strategy; each attempt is recorded in `job_attempts` (provider, key, proxy, status, error).
2. **submit** - success pins `provider_id`, `key_id`, `remote_id`, sets `running`; increments key usage + `key_usage` table.
3. **failover** - on 429 (recorded as `quota`), 5xx, or network error: key cools down exponentially; scheduler tries the *next* candidate within the same pass; job stays `queued` until `attempts >= max_retries` → `failed` with `fail_reason`, or all fits were transient (cooldown) → remains `queued`.
4. **poll** - running jobs are polled every `poll_interval` (5s). Provider state is normalized (`COMPLETED`, `done`, `FINISHED` → `succeeded`, …). On failure/timeout: if attempts remain → requeue with cleared binding (next provider), else terminal `failed|timeout`.
5. **cost cap** - if `cost_usd_micros > max_cost_usd`, the job is cancelled remotely and failed with `max_cost_usd exceeded`.
6. **timeout** - `max_duration_seconds` exceeded → remote cancel + same failover logic.
7. **logs** - full log text stored on the job row; WebSocket `GET /api/v1/jobs/{id}/logs?token=…` streams the buffer live; REST falls back to `GET /api/v1/jobs/{id}`.

## Providers verification (2026-11)

32 adapters are registered (`internal/providers/registry.go`). **Only providers with a real free tier offering COMPUTE GPUs are enabled by default.** Everything marked "disabled" is seeded but off; flip it in Providers → toggle. Verified notes per provider (public docs/marketing pages as of Nov 2026 - free-tier terms change frequently; **verify before relying on them**):

| # | Provider | Free GPU tier (as advertised, verified 2026-11) | API class | Status |
|---|---|---|---|---|
| 1 | Kaggle Notebooks | ~30 GPU-h/week; **T4 x2 only**; phone verified; 12h session cap | Full session API via `kaggle kernels` (push/status/logs/cancel, Bearer auth) | enabled |
| 2 | Google Colab | free T4, ~12h sessions, dynamic limits | none official; unofficial runtime protocol (NOTE) | enabled* |
| 3 | Lightning AI | ~15 free credits/month (≈75 T4 GPU-h); T4/L4/L40S/A100/H200 by account | Bearer REST (Studios) | enabled |
| 4 | Paperspace Gradient | Free plan: **M4000 8GB** notebook GPU only (Maxwell) + C4 CPU; 6h auto-shutdown; Pro $8/mo adds P4000/RTX4000/A4000, Growth $39/mo adds A100-80G | REST (x-api-key); legacy API | enabled |
| 5 | Saturn Cloud | **150 GPU+CPU hours/month** (shared, T4 16GB only); JupyterLab/SSH/Dask; auto-shutdown when idle | Bearer REST | enabled |
| 6 | SageMaker Studio Lab | **CLOSED to new customers since 2026-07** - replaced by SageMaker Free Tier (250 CPU-h/mo); T4 sessions for legacy accounts | none (UI-only) | disabled - NOTE |
| 7 | Paddle AI Studio (Baidu) | V100 16GB activations, ~12h/day cap; Baidu phone account | Bearer REST (`/user/info`); submit UI-only (NOTE) | enabled* |
| 8 | ModelScope (Alibaba) | 36h one-time NVIDIA A10 24GB (10h/run) + **100h AMD MI300X 192GB** via AMD Developer Program (ROCm) | SDK-token; REST for identity only (NOTE) | enabled* |
| 9 | Tencent CloudStudio | ~10000 free GPU-minutes/month (T4 16GB, region-dependent); real-name account | token REST (NOTE) | enabled* |
| 10 | Leafcloud | free research-credit GPU program (T4/L4) | Bearer REST `/instances` | enabled |
| 11 | Hugging Face Spaces (ZeroGPU) | shared **NVIDIA RTX PRO 6000 Blackwell**, free 5 GPU-min/day (~3 runs); large=48GB (1×quota) / xlarge=96GB (2×quota); 24h reset; Gradio only, 60s/call | Bearer REST `/whoami-v2`; ZeroGPU attach is code-level (NOTE) | enabled* |
| 12 | Modal | **$30/mo Starter compute credits**; 11 GPU SKUs: T4 16G, L4 24G, A10 24G, L40S 48G, A100 40/80G, RTX PRO 6000 96G, H100 80G, H200 141G, B200 180G, B300 288G | token auth; **no public REST submit** - use `modal run` SDK (NOTE) | enabled* |
| 13 | Beam | free monthly container allowance, T4/L4 | Bearer REST `/containers` (inferred) | enabled |
| 14 | Intel Tiber AI Cloud | free Gaudi2 96GB developer program | Bearer REST; OpenShift notebooks (NOTE) | enabled* |
| 15 | Genesis Cloud | **trial credit only, no permanent free GPU** | REST | disabled - NOTE |
| 16 | Anyscale | trial credits only (free tier ended) | REST | disabled - NOTE |
| 17 | JarvisLabs | free notebook credits (new accounts) | Bearer REST (inferred) | enabled |
| 18 | Nebius Cloud | no free GPU quota | REST | disabled - NOTE |
| 19 | Hyperbolic | one-time trial credits | REST | disabled - NOTE |
| 20 | Salad Cloud | community-capacity free tier, containers only | Bearer REST | disabled - NOTE |
| 21 | RunPod | **no permanent free tier** ($1 one-time credit) | GraphQL | disabled - PoC skeleton per spec |
| 22 | Codesphere | free tier = CPU only | REST | disabled - NOTE |
| 23 | Replit | free tier = CPU only | REST | disabled - NOTE |
| 24 | Deepnote | free tier = CPU only | REST | disabled - NOTE |
| 25 | Hex | free tier = no GPU | REST | disabled - NOTE |
| 26 | Noteable | free tier = no GPU | REST | disabled - NOTE |
| 27 | Domino Datalab | trial / OSS-program free, verify | REST | disabled - NOTE |
| 28 | FloydHub | **service discontinued** | - | disabled - skeleton kept |
| 29 | Spell.ml | rebranded (Determined/HPE) | - | disabled - NOTE |
| 30 | Valohai | 10 free compute-hours trial project | Bearer REST | disabled - NOTE |
| 31 | Determined AI | OSS, free on your own GPUs | Bearer REST | enabled (self-host) |
| 32 | ClearML | free hosted orchestration; GPUs must come from your agents | HTTP basic (access/secret) | enabled (self-host GPU) |
| **33** | **Self-hosted Runner** | **your own machine anywhere** - Kaggle/Colab cell, Modal deploy, own RTX box | Deploy `examples/runner.py` (HTTP + Bearer token) | **enabled** |

`enabled*` = provider is usable for quota/scheduling bookkeeping and key management, but **submit** runs through the provider's own UI/SDK (NOTE) - GPUHub will mark attempts failed with an explicit "submission via API not supported; see README NOTE" and fail over to the next candidate.

**Not added (research findings):** Together/Fireworks/Groq/Cerebras/SambaNova/Replicate/Baseten/DeepInfra/Novita/Chutes/Cohere/Mistral/Google AI Studio/Cloudflare Workers AI/GitHub Models - inference-only, excluded by scope. Vast.ai, RunPod community (paid), Lambda Labs, AMD Developer Cloud - paid or trial-credit-only. Regular "free Soon™" marketing pages without an actual free GPU SKU were not added. If you find a genuinely free compute-GPU site, here's the 20-line recipe to add it:

```go
// internal/providers/adapters4.go
func NewMyProvider() *apiAdapter { /* id, Name, caps{GPUs:…}, baseURL, auth, paths */ }
func init() { registerBuiltin(func() []Provider { return []Provider{NewMyProvider()} }) }
// + add row to DefaultDisabled if unsure; + README line; + registry test covers it automatically.
```

## Self-hosted Runner - any provider's GPU becomes usable via API (including Kaggle/Colab cells!)

GPUHub ships a **generic runner** (`examples/runner.py`, ~250 lines, **stdlib-only Python**) that bridges GPUHub and ANY environment where you can execute code:

1. Run it inside a Kaggle/Colab cell, a Modal container, a Lightning studio, your home RTX box or a VPS:
   ```bash
   # in a notebook cell (or on your own machine):
   !pip install fastapi 2>&1 | tail -1
   import getpass, os
   os.environ["GPUHUB_RUNNER_TOKEN"] = getpass.getpass("runner token: ")
   !python examples/runner.py --host 0.0.0.0 --port 8787 &     # or paste the file inline
   # expose it:
   !pip install pyngrok && from pyngrok import ngrok; print(ngrok.connect(8787))
   ```
2. Paste the public URL into **Providers → Self-hosted Runner → Add key** with credential `{"base_url":"https://xxxx.ngrok.io","token":"<your token>"}`. GPU binding = sticky (optional); unbound = direct.
3. GPUHub schedules work to it like any provider: `gpu_type`, fallback chain, quotas, logs, cancel - all work.

**~9h session wall:** Free providers kill sessions (Kaggle ~12h, Colab ~12h, Studio Lab 4h). GPUHub can't change a provider's limit - but it makes it survivable: set `session_renew: true`, and when the session dies unexpectedly the job is automatically re-queued on the next eligible key; the payload sees `GPUHUB_ATTEMPT=<n>` and `GPUHUB_RESUME=1` - write your trainer to load `checkpoint.pt` when `RESUME=1` (the shipped `examples/runner.py` passes these through; a torch trainer with ~10 lines of save/load logic survives multi-session runs). Kaggle's 30 GPU-hours/week still caps total work - renewal ≠ quota bypass.

**Runner protocol (for anything calling GPUHub):** `GET /health`, `POST /jobs`, `GET /jobs/{id}`, `GET /jobs/{id}/logs`, `POST /jobs/{id}/cancel`, auth `Bearer <token>`. The protocol is deliberately trivial - any language can implement it.

## REST API

Base URL `/api/v1`. Mutating calls require `X-Requested-With: XMLHttpRequest` (CSRF) + `Authorization: Bearer <jwt>`. WebSocket logs accept `?token=` for browser convenience. Full spec: **`docs/openapi.yaml`**, Swagger UI at **`/docs`**.

Job submit example (see also `examples/submit_job.py` for GPU fallback chains, VRAM-only requests and session renew):

```bash
curl -sS -X POST http://localhost:8080/api/v1/jobs \
  -H "Authorization: Bearer $TOKEN" -H "X-Requested-With: XMLHttpRequest" \
  -H "Content-Type: application/json" \
  -d '{
        "type": "script",
        "payload": "import torch;print(torch.cuda.get_device_name(0))",
        "gpu_type": "A100",
        "gpu_type_fallback": ["H100", "L40S", "T4"],
        "min_vram_gb": 16,
        "gpu_count": 1,
        "max_duration_seconds": 3600,
        "provider_preference": ["modal", "saturn", "runner"],
        "session_renew": false
      }'
# → {"id": 42, "status": "queued"}
```

If `gpu_type`, `min_vram_gb` (≥1) or `gpu_count` (1–8) are missing/invalid → `400` with a precise message. Every GPU field is mandatory; fallback chain and provider preference are optional.

Endpoints (selection; see openapi.yaml): `POST /auth/login` · `GET /providers` · `POST /providers/{id}/keys` · `PATCH /keys/{key_id}/proxy` · `GET /providers/{id}/quota` · `POST /providers/{id}/test` · `GET|POST|DELETE /proxies[/{id}]` · `POST /jobs` · `GET /jobs` · `GET /jobs/{id}` · `GET /jobs/{id}/logs` (WS) · `POST /jobs/{id}/cancel` · `GET /dashboard/summary` · `GET /audit` · `GET|PATCH /settings` · `POST /settings/test-webhook` · `POST /settings/rotate-master-key`.

## Security

- **Secrets at rest:** provider credentials and proxy URLs are AES-256-GCM encrypted (`credential_enc`, `proxies.url` with `enc:` prefix). Master key lives **only** in `GPUHUB_MASTER_KEY` (env). DB file chmod 0600. Rotation: `POST /api/v1/settings/rotate-master-key` re-encrypts everything (audit-logged), then you set the new key in `.env` and restart.
- **Auth:** bcrypt(12) password hashes; JWT HS256 access tokens, rotating refresh tokens (sha256 at rest); login rate-limit 10/min/IP; global API limit 600/min/IP; `X-Requested-With` required on mutations; CSP/nosniff/frame-deny headers; constant-time username check.
- **Network:** all outbound calls have hard timeouts, capped retries/cooldowns, and go through key-pinned proxy transports; response bodies are size-capped (32MB).
- **Injection:** 100% parameterized SQL (database/sql), no string-concatenated values except vetted column names in `handleUpdateKey` (fixed whitelist).
- **Audit:** every sensitive action (`login`, `key_create/delete/reveal/…`, `proxy_reassign(_force)`, `proxy_delete`, `proxy_health_fail`, `job_submit/failover`, `master_key_rotated`) → `audit_log` (viewable in UI). Notifications → `notifications` table + optional `GPUHUB_NOTIFY_WEBHOOK_URL`.
- **Least privilege by role:** admin-only - delete key/proxy, reassign proxy, force-detach, settings edit, master-key rotation, credential reveal. Everyone else: full read + job submit/cancel + key metadata edits.
- No real secret is committed: `.env.example` holds placeholders; real `.env` is git-ignored.
- Proxy health check endpoint defaults to ipify; override via `GPUHUB_PROXY_HEALTH_URL`.

## Configuration

`.env` (or env) beats `config.yaml`. Keys: `GPUHUB_PORT`, `GPUHUB_DB_PATH`, `GPUHUB_MASTER_KEY` (**required**), `GPUHUB_JWT_SECRET`, `ADMIN_USERNAME`, `ADMIN_PASSWORD` (first-boot only), `GPUHUB_SCHEDULER_STRATEGY`, `GPUHUB_ROTATION_STRATEGY` (`round_robin|least_used|most_quota|weighted`), `GPUHUB_MAX_RETRIES`, `GPUHUB_QUOTA_HIGH_WATERMARK`, `GPUHUB_PROXY_HEALTH_URL`, `GPUHUB_NOTIFY_WEBHOOK_URL` (for Slack/generic: plain JSON POST; **for discord.com webhooks: automatic colored embed** with username "GPUHub"; `POST /settings/test-webhook` sends a test ping), `GPUHUB_DEV=1` (ephemeral insecure key, dev only).

Migrations (embedded, applied at boot, idempotent): `internal/db/migrations/` - 0001 initial schema, 0002 `jobs.gpu_fallback`, 0003 `jobs.session_renew`, tracked in `schema_migrations`. Apply manually: `go run ./cmd/gpuhub --migrate-only` (or `make migrate`).

## Tests

`go test ./...` covers:
- crypto round-trip/wrong-key; JWT issue/parse via API test login
- GPU matcher (T4≠A100, VRAM floor, count cap, "any", lowest-waste)
- Scheduler: failover unsupported→fallback provider, 429→quota attempt + cooldown, GPU-incompatible→`unschedulable` + per-provider rejections, disabled provider rejected, poll-failure→failover requeue; incomplete-unavailability stays queued
- Sticky proxy: 100× cached transport = same proxy; no rebind without manual SQL; `HealthCheckAll` deactivates keys without rebinding; delete-proxy simulation disables keys; proxy URL validation + redaction
- Adapters vs `httptest` mocks: auth header, submit ID parsing, state normalization, 429 mapping, logs, quota shaping, mixed registry sanity (≥32, unique ids, known GPUs)
- API integration: CSRF header enforcement, login, 1:1 proxy constraint (409), delete-proxy flow → key disabled & unbound, job GPU-field validation, dashboard/audit endpoints
- Notify: Discord webhook detection → colored embed payload, plain-JSON fallback for other URLs, webhook 5xx → error

Run: `make test` · smoke: `./scripts/smoke.sh` · compose: `make up` · logs: `make logs`.

## Deployment

`docker compose up --build -d` runs **gpuhub** (binary + embedded SPA, SQLite at `/data/gpuhub.db`, volume `gpuhub_data`, host port `GPUHUB_PORT` (default 8080) → container 8080) and **caddy** (TLS termination; edit `Caddyfile` domain). If host port 8080 is taken on your machine, set `GPUHUB_PORT=8090` in `.env` and `docker compose up -d` again. Health: `GET /api/v1/auth/status`.

For bare metal: `cd web && npm ci && npm run build && cd .. && go build -o bin/gpuhub ./cmd/gpuhub && GPUHUB_MASTER_KEY=… ADMIN_PASSWORD=… ./bin/gpuhub`.

## Known limitations & roadmap

- Free-tier terms change often; several adapters (Colab/Studio Lab/ModelScope ZeroGPU/Modal) **have no public job-submit HTTP API** - GPUHub schedules and enforces quota/failover honestly, but actual run submission for those providers goes through our **Self-hosted Runner** (deploy `examples/runner.py` inside the environment and GPUHub drives it over HTTP) or the provider's own UI/SDK (attempts are marked failed-by-design with explicit messages, and other candidates are tried). Kaggle (kernels API), Paperspace /jobs, Lightning REST and most other enabled adapters submit jobs for real via their API.
- Provider QUOTA endpoints: many providers don't expose remaining free quota; GPUHub meters local usage (`key_usage`, per-day/week counters) and only consumes provider-reported quota when available.
- Live log streaming is poll-based over WebSocket (no provider websockets), 1s cadence; logs are also persisted on the job row.
- The ~9–12h session wall of free providers (Kaggle ~12h, Colab ~12h, Studio Lab 4h) cannot be removed - `session_renew` + checkpoint/resume makes long runs survive, it does not extend quotas.
- SQLite WAL + `busy_timeout(5000)`; single-writer workload fits free-tier GPU concurrency easily.
- Notifications: Discord auto-detects colored embeds; generic/Slack get plain JSON. Email templating not implemented.
- UI: English + Turkish (top-right switcher); light + dark themes (top bar toggle). Jobs are created via code/API (the UI no longer has a job creation form - monitoring lives on the Dashboard).
- Roadmap: Postgres option, per-key spend forecasts, `unschedulable` wait-notify, multi-runner load balancing for the Runner provider, Helm chart.

## CI (GitHub Actions � GHCR)

`.github/workflows/ghcr.yml` pushes `ghcr.io/neighboth/gpuhub:latest` (and `sha-<short>`) to GitHub Container Registry on every push to `main`. Private package by default; make it public in repo � Packages � package settings, or keep private and `docker login ghcr.io -u <user> -p <read:packages PAT>` on the target host. CasaOS deploy: see `deploy/casaos/docker-compose.yml`.
