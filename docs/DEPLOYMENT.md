# BizVerse — Deployment Guide

Targets per PRD §10.6: **Vercel** (frontend) + **Railway** (API, Postgres, Redis, storage, workers).

## Prerequisites

- Node 24+, Go 1.26+, a GitHub repo.
- Accounts: Vercel, Railway, Cloudflare R2 (or any S3 bucket), Resend (email), Google Cloud Console (OAuth).

## 1. Environment variables

### API (Railway)

```
APP_ENV=production
PUBLIC_URL=https://<your-domain>
DATABASE_URL=postgres://…         # Railway Postgres plugin
JWT_SECRET=<64+ random chars>
COOKIE_SECURE=true
CORS_ORIGINS=https://<your-domain>
MIGRATIONS_DIR=/app/infra/postgres/migrations
MEDIA_DIR=/data/media             # or point to R2 via the S3 adapter
MEDIA_BASE=https://api.<your-domain>/api/v1/media
RESEND_API_KEY=<key>
EMAIL_FROM=no-reply@<your-domain>
GOOGLE_OAUTH_CLIENT_ID=<id>
GOOGLE_OAUTH_CLIENT_SECRET=<secret>
VAPID_PUBLIC_KEY=<base64url 65-byte P-256>
VAPID_PRIVATE_KEY=<base64url PKCS8 P-256>
```

Generate VAPID keys:

```powershell
# one-off via a tiny Go program or: node -e with 'web-push' lib
go run ./cmd/api -vapid            # prints keypair when implemented; otherwise use npx web-push generate-vapid-keys
```

### Web (Vercel)

- None required beyond the framework preset; `vercel.json` rewrites `/api/*` to the Railway service. Set `API_URL` in code if not same-origin (currently same-origin via rewrite).

## 2. Postgres

1. Add the Railway Postgres plugin (v17+). Note the connection string.
2. Run migrations once, then on every deploy before the app starts:

```powershell
$env:DATABASE_URL="postgres://…"; go run ./cmd/api -migrate
```

3. Enable backups (Railway managed) + daily dumps via cron job:

```bash
pg_dump "$DATABASE_URL" | gzip > /backups/bizverse-$(date +%F).sql.gz
```

4. **Test a restore quarterly** into a scratch database before touching prod.

## 3. Railway service

- `railway init` at repo root; set the root directory to `services/api`.
- Add a `Dockerfile` (or use `go build` nixpacks):

```dockerfile
FROM golang:1.26 AS build
WORKDIR /app
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api ./
RUN go build -o /bin/api ./cmd/api

FROM debian:bookworm-slim
COPY --from=build /bin/api /bin/api
ENTRYPOINT ["/bin/api"]
```

- Health check: `GET /api/v1/health`.
- Workers run in the same process (trending, currency, digest jobs) — no separate service needed; scale horizontally later.

## 4. Vercel

- Import the repo, root directory `apps/web`, build command `npm run build`, output `dist`.
- `vercel.json` rewrites `/api/*` and `/og/*` to the Railway domain. In production you may prefer a custom domain for the API and same-site cookies — set `CORS_ORIGINS` and `COOKIE_SECURE=true` accordingly.
- **SEO:** after deploy, run `npm run prerender` (needs a running API) or add the prerender step to CI, serving `dist/prerendered/*.html`. Add `robots.txt` and submit the sitemap (`/api/v1/sitemap.xml`) in Search Console.

## 5. Object storage (media)

Swap the local-disk `Media` service for the S3 implementation (documented in code): set bucket, access key, secret, and `MEDIA_BASE` to the CDN URL. Keep `document_verification` files in a private bucket served only via the admin endpoint.

## 6. CI/CD

`.github/workflows/ci.yml` runs vet, build, migrations, unit tests, and the Playwright E2E suite on every PR. Add a deploy workflow:

```yaml
- uses: railwayapp/railway-action@v3          # deploy services/api on main
- vercel --prod                               # web deploy on main
- go run ./cmd/api -migrate                   # run migrations before/after deploy (with a lock)
```

## 7. Go-live checklist

- [ ] Migrations applied in staging + prod, backup verified by restore test
- [ ] Resend verified (send a test verification email)
- [ ] Google OAuth configured with the callback URL registered
- [ ] VAPID keys set; a test push received on a real device
- [ ] `robots.txt` + sitemap submitted; OG images render on a share
- [ ] Domain TLS enforced (Vercel + Railway)
- [ ] Load test: 10× projected peak on search + map + WS
- [ ] Privacy + terms pages reachable; cookie banner if required by region
- [ ] Seed data refreshed (categories, demo businesses, products, reviews)
