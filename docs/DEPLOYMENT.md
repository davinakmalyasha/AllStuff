# BizVerse — Deployment Guide

Targets per PRD §10.6: **Vercel** (frontend) + **Railway** (API, Postgres, Redis).

> **Read this before copying an env block.** `config.Validate()` **fails startup**
> on several of the variables below rather than warning. A deployment that boots
> is one where every hard requirement is met; the ones marked REQUIRED are not
> negotiable and the error names the variable.

## Prerequisites

- Node 24+, Go 1.26+ (both pinned in `.github/workflows/ci.yml`), a GitHub repo.
- Accounts: Vercel, Railway, Resend (email), and Google Cloud Console if you
  want OAuth. **No object-storage account is needed** — there is no S3/R2
  adapter, so media is local disk (see §5). An earlier version of this guide
  listed Cloudflare R2 as a prerequisite; that was aspirational.

## 1. Environment variables

### API (Railway)

```
# --- required to boot in prod -------------------------------------------
APP_ENV=production
PUBLIC_URL=https://<your-domain>
# config.Validate REJECTS a DATABASE_URL whose sslmode is not require or
# verify-full. The Railway plugin's connection string does not set it, so
# append it yourself or the pod crash-loops.
DATABASE_URL=postgres://…?sslmode=require
JWT_SECRET=<64+ random chars>          # openssl rand -hex 32
COOKIE_SECURE=true                     # REQUIRED in prod
CORS_ORIGINS=https://<your-domain>
# REQUIRED in prod. Without it the API is a production deployment with no
# client-side rate limiting, no shared TOTP replay guard, and no cross-instance
# WS fan-out — the auth and 2FA tiers then multiply by your replica count.
REDIS_URL=rediss://…                   # rediss:// is REQUIRED in prod
RESEND_API_KEY=<key>                   # OR SMTP_ADDR; one of the two is required
MEDIA_ENCRYPTION_KEY=<64 hex chars>    # openssl rand -hex 32
CLAMAV_ADDR=<clamd host:port>          # uploads FAIL CLOSED without it

# --- strongly recommended ------------------------------------------------
# REQUIRED behind any proxy, and the documented topology (Vercel -> Railway)
# is a proxy. Left false, clientIP() returns the proxy egress IP for every
# request, so the 5/min auth and 3/15min 2FA buckets collapse into ONE global
# bucket and a single attacker triggers a platform-wide 2FA lockout.
TRUST_X_FORWARDED_FOR=true
WS_ORIGINS=https://<your-domain>       # additional WS origins; same-origin always allowed
MIGRATIONS_DIR=/app/infra/postgres/migrations
METRICS_TOKEN=<random bearer>          # bearer token for prod /metrics scraping

# --- media ---------------------------------------------------------------
MEDIA_DIR=/data/media                  # persistent volume; see §5
MEDIA_BASE=https://<your-api-domain>/api/v1/media

# --- optional ------------------------------------------------------------
LOG_LEVEL=info
# BARE INTEGER. "120/min" is not parsed: Atoi rejects it, a warning is logged
# and the default 120 is used, so a unit suffix makes the knob silently inert.
# An earlier version of this guide showed "120/min".
RATELIMIT_GLOBAL=120
# Only set this on a single-container deployment where the API also serves the
# built web app. Leave EMPTY for the Vercel split below.
WEB_DIST_DIR=

# --- optional: OAuth, push, billing --------------------------------------
GOOGLE_OAUTH_CLIENT_ID=
GOOGLE_OAUTH_CLIENT_SECRET=
VAPID_PUBLIC_KEY=                      # go run ./cmd/api -vapid
VAPID_PRIVATE_KEY=
EMAIL_FROM=no-reply@<your-domain>
# If STRIPE_SECRET_KEY is set, STRIPE_WEBHOOK_SECRET is a hard requirement.
# Without Stripe at all, the free plan still resolves and /pricing renders;
# only checkout, portal and the webhook return a not-configured error.
STRIPE_SECRET_KEY=
STRIPE_WEBHOOK_SECRET=
STRIPE_PUBLISHABLE_KEY=
```

`.env.example` in the repo root documents the same variables with the reasoning
inline. It is a **reference, not a loader** — there is no dotenv dependency, so
`cp .env.example .env` and then running the binary silently uses built-in
defaults.

### Web (Vercel)

No framework env required beyond `vercel.json`, which rewrites `/api/*`,
`/og/*` and the three SSR paths to the Railway domain. `STRIPE_PUBLISHABLE_KEY`
is the one value the browser needs, and it is served from `/api/v1/meta` rather
than a build-time variable.

## 2. Postgres

1. Add the Railway Postgres plugin (v17). Note the connection string and append
   `?sslmode=require`.
2. Migrations run automatically at boot, before the listener opens, under a
   Postgres advisory lock so two booting replicas cannot race. There is no
   separate migrate step, and adding one is both redundant and racy.
3. Enable managed backups, plus daily dumps:

   ```bash
   pg_dump "$DATABASE_URL" | gzip > /backups/bizverse-$(date +%F).sql.gz
   ```

   `scripts/backup.ps1` is the Windows equivalent.
4. **Test a restore quarterly** into a scratch database before touching prod.

## 3. Railway service

- `railway init` at repo root; root directory `services/api`.
- `Dockerfile` (or `go build` nixpacks). **The web build must be copied in if
  you want SSR hydration** — the Dockerfile below includes it, and the version
  in the previous revision of this guide did not, so following it verbatim
  produced a site where every `/b/:slug` served crawlable HTML that never
  hydrated:

```dockerfile
# ---- web build (needed for SSR hydration) ----
FROM node:24-slim AS web
WORKDIR /web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci
COPY apps/web/ ./
# manifest: true is what makes .vite/manifest.json exist for the SSR shell.
RUN npm run build

# ---- api build ----
FROM golang:1.26 AS build
WORKDIR /app
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api ./
COPY infra/postgres/migrations /app/migrations
RUN go build -o /bin/api ./cmd/api

FROM debian:bookworm-slim
COPY --from=build /bin/api /bin/api
COPY --from=build /app/migrations /app/infra/postgres/migrations
# Only needed if WEB_DIST_DIR is set; omit both lines for the Vercel split.
COPY --from=web /web/dist /app/web/dist
ENV WEB_DIST_DIR=/app/web/dist
ENTRYPOINT ["/bin/api"]
```

- Health check: `GET /api/v1/health` (it queries Postgres, so it is a real
  readiness signal). `GET /api/v1/ready` is the cheaper liveness variant.
- Background jobs run in the same process. Scaling past one replica is safe —
  leader election guarantees exactly one scheduler — but **`REDIS_URL` becomes
  mandatory**, for the reasons in §1.
- `/metrics` in prod requires an admin session or the `METRICS_TOKEN` bearer.

## 4. Vercel

- Import the repo, root `apps/web`, build `npm run build`, output `dist`.
- `vercel.json` carries five rewrites and their order is load-bearing:
  1. `/api/v1/ws` → the API over `wss://`. **Must be first.** Get this wrong and
     the rewrite chain silently breaks chat while everything else looks fine.
  2. `/b/:slug`, `/c/:slug`, `/city/:slug` → the SSR endpoints.
  3. `/api/:path*`, `/og/:path*`, `/robots.txt` → the API.
- **SEO:** no build step and no prerender job. The API renders the crawlable
  document for those three paths on every request, so a newly published business
  is indexable immediately — there is no artifact to rebuild. Two things must
  both hold in production:
  1. Those three paths rewrite to `https://<your-api-domain>/ssr/...`. Verify
     the `api.example.com` placeholder has been replaced.
  2. `WEB_DIST_DIR` on Railway points at a directory containing
     `.vite/manifest.json`, otherwise the page is crawlable but does not
     hydrate. If the manifest is absent the renderer degrades to a
     document-only response rather than an error, so this fails quietly —
     check the console for a real hydration warning.

  `curl -sS "https://<your-api-domain>/ssr/b/<slug>" | head -40` should show
  `<title>`, `application/ld+json` and body text. A `404` carries
  `X-Robots-Tag: noindex,follow`. The API serves `/robots.txt` from the origin
  root — verify the Sitemap URL shows your production domain — and submit
  `/api/v1/sitemap.xml` in Search Console.

## 5. Media storage

Media is stored on **local disk** under `MEDIA_DIR`. There is no S3, R2 or MinIO
adapter, despite `service/media.go` and the deploy topology listing implying one
swaps in cleanly. A single API replica with a persistent volume is the supported
production topology; without the volume, media is lost on every redeploy.

`document_verification` files are AES-256-GCM encrypted at rest when
`MEDIA_ENCRYPTION_KEY` is set — which prod requires — and served only through the
admin endpoint, with `CSP: default-src 'none'; sandbox` and a
`verification_document_views` row per view. Migrate a business to a new replica
by copying the volume contents, not the database alone.

## 6. CI/CD

`.github/workflows/ci.yml` runs, on every PR: `go vet`, `go build`, **all
migrations against a real PostgreSQL 17**, the seed load, `go test -race`, the
static migration-contract tests, the behavioural migration-replay test, the SQL
harnesses, `tsc`, ESLint, the production build, and the Playwright suite.

`.github/workflows/deploy.yml` deploys the API to Railway and the web app to
Vercel on `main`, **gated on CI passing**. It is gated by the
`vars.DEPLOY_ENABLED == 'true'` repository variable, so a fork does not deploy.

An earlier version of this guide said "add a deploy workflow" and sketched one
using `railwayapp/railway-action@v3` and an unpinned `vercel --prod`. That
workflow already exists and does neither of those things; the sketch is removed
because following it would duplicate and race the real pipeline.

## 7. Go-live checklist

- [ ] Migrations applied in staging + prod; backup verified by a restore test
- [ ] `TRUST_X_FORWARDED_FOR=true` and `REDIS_URL` set — confirm the auth bucket
      is per-client and not shared across the proxy
- [ ] Resend verified (a real verification email arrives); confirm the fallback
      console sender is not active in prod
- [ ] Google OAuth configured with the callback URL registered
- [ ] VAPID keys set; a test push received on a real device, outside quiet hours
- [ ] `robots.txt` + sitemap submitted; OG images render on a share
- [ ] SSR: `curl` one `/ssr/b/<slug>` and confirm the page **hydrates**, not just
      that it returns HTML
- [ ] Domain TLS enforced (Vercel + Railway)
- [ ] Stripe: plans have `stripe_price_id` bound, and a test checkout +
      cancellation round-trips; verify `/admin/analytics` shows the
      reconciliation job running
- [ ] ClamAV reachable; confirm an upload is accepted, then confirm it is
      **rejected** when ClamAV is stopped (fail-closed is a feature)
- [ ] Capacity limits enforced: publish past `product_limit` on a free plan and
      confirm the error, not a silent 500
- [ ] Load test: 10× projected peak on search + map + WS
- [ ] Privacy + terms pages reachable; cookie banner if required by region
- [ ] Seed data refreshed, and the demo businesses are `fully_verified`

