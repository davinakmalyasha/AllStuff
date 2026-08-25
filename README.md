# BizVerse

Universal business directory + storefront platform. Every business in the world gets a customizable storefront; every user finds them through search, map, and leaderboards. Full spec in `docs/PRD.md`, architecture in `docs/ARCHITECTURE.md`.

## Stack

- **Web** — `apps/web`: React 18 + TypeScript + Vite + Tailwind (monochrome theme, light default / dark mode)
- **API** — `services/api`: Go 1.26, REST + WebSocket, PostgreSQL (pgx), argon2id auth, session registry, CSRF, rate limiting
- **DB** — PostgreSQL 17, migrations in `infra/postgres/migrations`
- **Infra** — `infra/`: docker compose (Postgres, Redis, MinIO, Mailpit, ClamAV)

## Quickstart (local)

1. Start the backing services (Postgres, Redis, MinIO, Mailpit, ClamAV):

   ```powershell
   docker compose -f infra/docker/docker-compose.yml up -d postgres mailpit
   ```

   Then create the database and run migrations:

   ```powershell
   psql -U postgres -h localhost -c "CREATE DATABASE bizverse;"
   psql -U postgres -h localhost -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/migrations/0001_initial.sql
   ```

   Or with the Go runner (from `services/api`): `go run ./cmd/api -migrate`

2. API:

   ```powershell
   cd services/api
   copy ..\..\.env.example .env   # or set env vars
   go run ./cmd/api
   # http://localhost:8080/api/v1/health
   ```

3. Web:

   ```powershell
   cd apps/web
   npm install
   npm run dev
   # http://localhost:5173  (proxies /api and /ws to :8080)
   ```

Dev emails (verification, reset) are logged to the API console when no `RESEND_API_KEY` is set.

## Tests

```powershell
cd apps/web
npx playwright install chromium
npx playwright test          # E2E smoke: register/login, discover, storefront, compare
```

## Documentation

- `docs/PRD.md` — product requirements (all 13 sections, approved)
- `docs/ARCHITECTURE.md` — API design, WS protocol, jobs, data flows
- `docs/API.md` — full endpoint reference
- `docs/RUNBOOK.md` — admin operations, verification standards, incident response
- `docs/DEPLOYMENT.md` — Vercel + Railway deployment guide

## Feature status (PRD §12)

- ✅ M0 Foundation — auth, sessions, CSRF, rate limits, theme, landing page
- ✅ M1 Directory core — categories, wizard + document verification, search, public pages, SEO
- ✅ M2 Storefront — themes/layout builder + live preview, publish snapshots, products + variants, analytics
- ✅ M3 Engagement — likes/recommends/collections, threaded comments, reviews + helpful votes, trending engine + leaderboards, notifications
- ✅ M4 Map & compare — MapLibre viewport search, Booming/Rising markers, compare tray + page
- ✅ M5 Messaging — direct + business threads, full message features, WS delivery, push subscriptions
- ✅ M6 Admin — verification, moderation queues + audit trail, users, curation
- ✅ M7 Hardening — TOTP 2FA + challenge login, sessions/security page, data export, E2E suite

## Platform extras

- ✅ Currency conversion (live rates), Google OAuth, Web Push, account deletion with grace
- ✅ User profiles, follow feed, Q&A, review photos, owner announcements, weekly digest
- ✅ Help center, contact + appeals, admin KPIs, co-owner invites, link previews, pinned messages
- ✅ Code-splitting, CI (vet/test/lint/migrate/E2E), OG images, prerender script
- ✅ 2026-08 audit hardening: single-use email tokens, per-account throttles, chat-media authz, streaming uploads, WS chain fix + regression test, job idempotency & retention (see ARCHITECTURE.md)

## Structure

```
docs/                    PRD + architecture (the contract)
infra/
  postgres/migrations/   versioned SQL (run in order, backward-compatible)
  docker/                compose for full local stack
services/api/            Go backend (REST + WS + jobs)
apps/web/                React frontend
```

## Deploy targets

Frontend: Vercel. Backend/services: Railway (see `docs/ARCHITECTURE.md` §6).
