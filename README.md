# AllStuff

A self-hostable business directory and storefront platform. Every business gets
a storefront it can build without a developer; every user finds them through
search, a map, and published rankings.

| Landing (live verified-business count) | Storefront (trust badges, catalog, hours) |
|---|---|
| ![Landing](docs/demo/landing.png) | ![Storefront](docs/demo/storefront.png) |

| Owner dashboard (real login, both seed businesses) | Trending leaderboards |
|---|---|
| ![Owner dashboard](docs/demo/dashboard.png) | ![Leaderboards](docs/demo/leaderboards.png) |

Captured against the seed by `apps/web/scripts/shots.mjs` (Playwright +
`go run ./cmd/api -dev-login`); re-run it after visual changes so the tour
cannot rot.

PostgreSQL, Redis, and one Go binary. Three direct Go dependencies — JWT, TOTP,
Argon2id, the Redis client, Stripe, Web Push and the Prometheus exposition are all
hand-rolled. No proprietary APIs, no per-seat pricing, no data resale.

```
apps/web/       Next.js 16 + React 19 + TypeScript + Tailwind (light/dark, en/id)
services/api/   Go 1.27 — REST + WebSocket, 258 routes, 28 services
infra/          docker compose, 45 versioned SQL migrations
docs/           PRD (frozen), API (authoritative), ARCHITECTURE, RUNBOOK, DEPLOYMENT
```

---

## Three claims, and how to check each one

**1. Trust is document-backed, and every view of a document is audited.**
A business submits its registration or licence; an admin opens it in a viewer
that serves with `CSP: default-src 'none'; sandbox`, and each open writes a
`verification_document_views` row. Documents are AES-256-GCM encrypted at rest
and are never publicly reachable. There are two levels, and a paid plan cannot
buy the higher one — see claim 3.

```bash
grep -rn "LogDocumentView" services/api/internal/    # the audit write
grep -rn "document_verification" infra/postgres/migrations/0001_initial.sql
```

**2. The ranking formula is published, and it has a fairness floor.**
`score = Σ(weight × e^(−λ·age_hours))` over three windows, with the weights
documented (view 1, save 3, like 5, comment 8, chat 8, recommend 10, review 12)
and λ admin-tunable. There is a **Rising** tier — velocity normalised by business
age and trailing score — that holds guaranteed visibility slots for new and small
businesses, plus a tracked KPI for the share of those slots they actually win.
Helpful votes feed no score at all, by design.

Every platform that ranks businesses monetises incumbent rank, which is why none
of them can ship this.

```bash
sed -n '1,80p' infra/postgres/migrations/0030_open_now.sql   # the SQL twin
grep -n "RisingPaused\|lambda\|RisingN" services/api/internal/service/trending.go
```

**3. Paid tiers sell capacity. They never sell trust or placement.**
A plan can charge for how many products, how many gallery photos, how many team
seats, API access, analytics depth, and support priority. It cannot charge for
the verification badge, because an admin assigns that after reading documents,
and it cannot charge for a featured slot, because featured is an editorial
choice. Those two entitlements were in the original catalogue and were removed;
a test now fails if anyone puts them back.

```bash
grep -n "verified_badge\|featured_placement" services/api/internal/service/billing_test.go
```

---

## Run it

Requires Go 1.27, Node 24, and Docker (or a local PostgreSQL 17).

```bash
# 1. backing services
docker compose -f infra/docker/docker-compose.yml up -d postgres mailpit

# 2. database + migrations  (the Go runner is the only supported path)
cd services/api
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/allstuff?sslmode=disable"
export JWT_SECRET="$(openssl rand -hex 32)"
go run ./cmd/api -migrate

# 3. API on :8080
go run ./cmd/api

# 4. web on :3000, proxying /api to :8080 (WebSocket: same-origin in prod via
# the vercel.json WS rule; NEXT_PUBLIC_WS_BASE=ws://localhost:8080 for local dev)
cd ../../apps/web && npm install && npm run dev
```

Load `scripts/seed.sql` for demo data — two verified Jakarta businesses, a
category tree, engagement signals (so trending/leaderboards render), and two
users: `demo@allstuff.test` (owner of both businesses) and
`admin@allstuff.test` (platform admin). The seed hashes are placeholders, not
logins — applying a seed file must never create credentials on its own:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/seed.sql
```

To click through as a human, turn those rows into working logins (dev only —
the flag refuses `APP_ENV=prod`, and the passwords are documented because a
dev password you have to look up gets replaced with something weaker):

```bash
go run ./cmd/api -dev-login
# owner  demo@allstuff.test / DemoPass123!
# admin  admin@allstuff.test / DemoPass123!  (scan the printed QR into an authenticator app;
#                                             the admin gate requires an enrolled 2FA factor)
```

Manual equivalents, if you prefer SQL: `UPDATE users SET email_verified_at =
now() WHERE email = '...'` skips verification for a new registration, and
`UPDATE users SET role = 'admin' WHERE email = '...'` promotes one (then
enroll 2FA at `/me/security` before `/admin/*` will open).

> There is **no dotenv loader**. `config.Load()` reads `os.Getenv` only, so
> copying `.env.example` to `.env` and running the binary silently uses the
> built-in defaults. Export the variables, as above. An earlier version of this
> README told you to `copy .env.example .env`, which did nothing.

Two flags exist: `-migrate` (apply migrations and exit) and `-vapid` (print a
Web Push keypair and exit). Everything else is environment.

## Test it

```bash
cd services/api
go vet ./...
go test -race ./...            # required: several tests are concurrency tests
go test -cover ./...

# Migration safety — needs a PostgreSQL you may create and drop databases on
TEST_DATABASE_URL="postgres://postgres@localhost:5432/postgres?sslmode=disable" \
  go test ./internal/db/ -timeout 30m

cd ../../apps/web
npm run typecheck && npm run lint
npx playwright install chromium && npx playwright test
```

## Test scope

`go test ./...` covers the API wall to wall (the ratchet above fails the build
on any drop). Playwright covers the public and auth journeys end to end —
landing, discover, booking as a customer, chat, notifications — plus axe
accessibility across the surfaces. The owner dashboard happy path, admin, and
billing flows need a verified email, an enrolled 2FA factor, or real Stripe
keys, so those are covered by Go tests, not by the browser suite.

## Documentation

| File | What it is |
|---|---|
| [`docs/API.md`](docs/API.md) | **The endpoint reference.** `TestEveryAPIRouteIsDocumented` fails when a route group is absent, so no area of the API goes undocumented — the four `/admin/categories` endpoints and `GET /auth/csrf` were, until it was written. It checks groups rather than individual endpoints, deliberately: the document's `read\|typing\|close` family notation cannot be compared path-by-path, and a stricter version reported 31 false positives for the 2 real findings. |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Design decisions, invariants, and the traps. Not an endpoint list. |
| [`docs/RUNBOOK.md`](docs/RUNBOOK.md) | Admin operations, verification standards, incident response, migration recovery. |
| [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | Vercel + Railway, with every env var that fails startup. |
| [`SECURITY.md`](SECURITY.md) | Data handled, known limitations, and the doc-drift policy. |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | The migration contract, enforced. |
| [`docs/PRD.md`](docs/PRD.md) | **Frozen** 2026-08 product contract. The shipped code has outgrown it — do not use it as an acceptance criterion. |

## The file worth opening first

[`infra/postgres/migrations/REGISTRY.md`](infra/postgres/migrations/REGISTRY.md)
is a registry of reserved migration numbers *and* an inventory of latent defects
in this repository's own schema history — a silent `IF NOT EXISTS` name
collision that meant an index was never built, two byte-identical duplicate
indexes on the highest-write table, a seed that could wedge startup permanently,
and a `NOT NULL` without a `DEFAULT` that 500'd claim approval.

It also records what the migration replay test found: re-running `0015`
recreates three indexes `0031` deliberately dropped, including a duplicate on
`notifications`, and **nothing reports an error**. The reasoning behind every
one of these is written down, because the next person to touch a migration needs
to know why the rule exists.

## What is not built

Named here rather than discovered later:

- **No database-level tenant isolation.** Every boundary is enforced in Go. There
  is no least-privilege role and no row-level security; both are reserved as
  migrations `0041` and `0042`. A single missed `WHERE` clause in a repository
  method is a cross-tenant read, not a 500. This is the highest-priority
  outstanding work.
- **Single-location businesses only.** `lat`/`lng` are `NOT NULL`, so a mobile
  or service-area business is currently unrepresentable. Migration `0036` is
  reserved and is the smallest high-value fix in the schema.
- **English-first UI.** i18next is wired end to end and `id` is complete for
  `common` / `nav` / `landing` / `auth`, but most feature surfaces are still
  hardcoded English. The switcher changes what it covers, not everything.
- **No FFmpeg pipeline.** Images get one ≤512px JPEG thumbnail; video and audio
  are stored and served as uploaded.
- **No object storage.** Media is local disk; a single replica with a volume is
  the supported production topology.
- **Google OAuth only,** despite the PRD listing four providers.
- **No recurring bookings or payment at booking time.** Booking itself is
  built (availability derived from hours × staff × blackouts, blackout
  management, multi-location rosters, confirm/complete/no-show) and booking is
  free; paid plans are owner subscriptions via Stripe, not per-appointment
  charges.

## Licence

MIT. See [LICENSE](LICENSE).
