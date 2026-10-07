# BizVerse — Architecture

Companion to `PRD.md`. This document describes **design decisions, invariants,
and traps**. It is deliberately *not* an endpoint reference.

> **Why there is no endpoint table here.** An earlier revision of this file
> carried one, and it drifted: roughly thirty rows described paths and methods
> that do not exist (`GET /map`, `GET /admin/analytics`, `GET /admin/users/{id}`,
> `POST /comments/{id}/replies`, `GET /products/{id}/reviews`, and others), while
> omitting the rate-limit tiers and background jobs that were actually there. A
> second hand-maintained copy of the route table is a liability, because there is
> nothing to stop it rotting.
>
> `docs/API.md` is the endpoint reference. It is checked against
> `internal/httpapi/server.go` and has no phantom entries. When the two
> documents disagree about a path, `API.md` is right.

### Deviations from the PRD, and things this file used to get wrong

Stated up front because the rest of the document is easier to trust when the
known gaps are in one place.

- **OAuth is Google only** (`GET /auth/oauth/google` + `/callback`). PRD §5.9.1
  also specifies Facebook, Apple and GitHub; none are implemented.
- **Pagination** is `?limit=&offset=` with a ceiling of 100 on list endpoints.
  Two exceptions: `GET /threads/{id}` is keyset-paginated by message id
  (`?before=<id>&limit=`, plus `has_more`), and `GET /search` resets any
  `limit > 50` back to its default of 24 — see `API.md` for why, because a client
  asking for 100 results gets 24 with no error.
- **Data export is synchronous** at `GET /me/export`. The PRD describes an async
  job producing a download link; there is no object storage and no queue.
- **Account deletion** is `POST /me/delete` with `POST /me/delete/cancel`
  (password-verified) and a grace-period restore at `POST /auth/restore`.
- **Recovery codes** are regenerated at `POST /me/security/2fa/recovery-codes`.
- **WebSocket is multi-device**, not "one connection per user, last wins". A user
  may have any number of concurrent connections (tabs, phone, desktop) and every
  frame is delivered to all of them. This document previously said otherwise
  and was wrong; the registry is `map[userID]map[*conn]struct{}`
  (`internal/ws/hub.go`). The per-connection `subscribe` set is **not** shared
  across replicas, so a `typing` frame raised on instance B does not reach a
  client connected to instance A.
- **Frames that are documented but not emitted**: `receipt.delivered`,
  `presence`, `unread.updated`, `media.ready`, `leaderboard.refresh`, and the
  client-initiated `sync {since_message_id}` replay. The first and last were
  flagged before; the middle four were not, and had been presented as contract.
  See §3 for what is actually emitted.
- **Keepalive is server-driven.** The client never sends `ping`; the server sends
  a WebSocket protocol `PingMessage` every 30s and the read deadline is 70s. A
  client-sent `ping` frame is accepted and answered with a `pong` application
  frame, but nothing in the codebase sends one.
- **Media upload** is a direct multipart `POST /api/v1/media` streaming to local
  disk with per-kind size caps and a ClamAV INSTREAM scan when `CLAMAV_ADDR` is
  set. There is **no S3/R2 adapter** despite `service/media.go` and the deploy
  docs implying one swaps in cleanly, no presigned URLs, and **no FFmpeg variant
  pipeline** — images get a single ≤512px JPEG thumbnail, and video and audio are
  stored and served exactly as uploaded. Chat media is served only to
  authenticated thread participants; verification documents are admin-only and
  every view is audited.
- **Rate limiting is Redis-backed and fails open.** A fixed window via `EVALSHA`
  (`internal/ratelimit/redis.go`), shared across replicas so N replicas enforce
  one budget rather than N. On any Redis error the request is allowed and the
  in-memory limiter takes over: failing closed would convert a cache blip into a
  site-wide outage. TOTP single-use is likewise shared
  (`internal/service/totp_guard.go`, `SET NX EX`) and falls back to a
  process-local guard, also failing open. With `REDIS_URL` unset both degrade to
  per-instance, so **2–3 replicas require it** — the 5/min auth and 3/15min 2FA
  buckets otherwise become 5/min × N.
- **Jobs run in-process** on one instance, chosen by advisory-lock leader
  election with retry. There is no separate worker binary.
- **Search is two-phase and index-backed by expression, not by trigger.** There
  is no `tsvector` column and no FTS trigger anywhere in the schema; the search
  path indexes `IMMUTABLE` helper functions (`biz_search_tsv`,
  `product_search_tsv`) with GIN, added in migration `0031`. PostGIS is **not**
  installed — the project deliberately uses `cube` + `earthdistance`, and a
  bounding box is a plain `lat BETWEEN … AND lng BETWEEN …`. This document
  previously described both a tsvector trigger and `ST_MakeEnvelope`; neither
  exists.
- **Ids are UUIDv4**, not UUIDv7. `internal/util/uuid.go` sets the version nibble
  to `4`. Nothing in the system depends on monotonicity except the pagination
  tiebreakers, which use `created_at` plus a sequential `id` rather than a
  time-ordered UUID.
- **Verification documents are encrypted and access-logged but not watermarked.**
  AES-256-GCM at rest, `CSP: default-src 'none'; sandbox` on serve, and a
  `verification_document_views` row per view. No watermark is applied to the
  bytes. Auto-redaction is not implemented either.
- **The database role is the table owner.** There is no least-privilege role and
  no row-level security; every tenant boundary is enforced in Go. Migrations
  `0041` and `0042` are reserved for both. See `SECURITY.md`.
- **Monetisation is capacity-only.** A paid plan may sell how many products, how
  many gallery photos, how many team seats, API access, analytics depth, and
  support priority. It may **not** sell the verification badge or a featured
  slot: verification is an admin decision made after reviewing documents, and
  featured placement is an editorial slot, so making either purchasable would
  destroy the thing users trust. An earlier catalogue sold both and was changed.

### 2026-08 security & reliability audit — implemented changes
- WS upgrades work through the full middleware chain (`Hijack`/`Flush` passthrough) with an authenticated upgrade regression test.
- Redis fan-out frames carry an instance tag (echo loop eliminated); publishing is async via a bounded queue.
- Domain error sentinels are immutable (`WithField` clones) — no cross-request mutation race.
- OG share cards XML-escape every owner field, validate URL schemes at write time (`logo_url`/`cover_url`: internal media paths or https), and serve with `CSP: default-src 'none'; sandbox` + nosniff.
- Password-reset/verification tokens are single-use (`consumed_tokens` table); refresh-token replay revokes the whole session family; TOTP codes are single-use per timestep; argon2id uses t=3 (RFC 9106 low-memory profile).
- Per-account login/2FA throttles complement per-IP limits; email change notifies the old address and requires TOTP when enrolled.
- Co-owner invite roles are enforced (`viewer` is read-only); invites persist their inviter; claim approval transfers ownership atomically and creates drafts with valid coordinates/slug.
- Support contact and client-error reports persist correctly (extended `reports` CHECK, nullable reporter).
- Link previews resolve DNS at dial level on every redirect hop and block private/reserved IPs; push endpoints disable redirects and derive VAPID `aud` from the endpoint origin; dead subscriptions (404/410) are pruned.
- Public business pages serve only verified/paused listings (suspension hides by slug too). Banned users' API keys stop working on `/api/v2/*`.
- Jobs: leader lock retried until held; digest/alerts idempotent per period (`job_runs`); retention purges consumed tokens, revoked sessions >90d, engagement events >90d, auth events >180d; currency sync is one batched upsert with ctx-bound fetch.
- Migrations run under an advisory lock; shutdown order is HTTP drain → ctx cancel (WS/jobs) → pool close.
- Frontend: logout clears the query cache + WebSocket + compare store; map popups escape HTML; JSON-LD escapes `<`; all user-supplied links pass a scheme allowlist; chat sends optimistically and backfills on reconnect.

### 2026-09 migration-safety pass

- Every migration from `0028` forward carries a machine-readable `-- migrate:`
  header declaring whether it is replay-safe, whether it is a seed, and why.
- `TestMigrationsDeclareTheirIdempotencyContract` checks statement shape against
  a fixed rule table: unguarded `CREATE TABLE` / `CREATE INDEX` / `ADD COLUMN`,
  `ADD CONSTRAINT` with no matching `DROP CONSTRAINT IF EXISTS`, `INSERT` without
  `ON CONFLICT`, and `CREATE INDEX CONCURRENTLY IF NOT EXISTS` without a
  drop-paired rebuild.
- `TestMigrateReplayIsSafe` proves it rather than asserting it. For every
  migration it deletes that migration's version marker — reproducing the exact
  crash window between `COMMIT` and the marker write — re-runs the chain, and
  requires both that the re-run succeeds and that a schema fingerprint
  (indexes, constraints, columns, enums) is byte-identical.
- `Migrate()` fails at boot if any index is invalid. `CREATE INDEX CONCURRENTLY`
  that fails leaves an `indisvalid = false` row that `IF NOT EXISTS` skips on
  every subsequent run, so the index would be permanently absent; that is now a
  hard startup error naming the index rather than a query that quietly got slow.
- `0029` gained a pre-flight dedupe section and is fully replay-safe. Two of its
  three original defects are documented in
  `infra/postgres/migrations/REGISTRY.md`, along with the `0015` defect that the
  replay test found: re-running it resurrects three indexes `0031` deliberately
  dropped, and nothing reports an error.

## 1. Conventions

- Base path: `/api/v1`, with five deliberate exceptions registered at the
  **origin root**: `/robots.txt`, `/og/b/{slug}`, `/ssr/*`, and the three
  `/metrics` and `/health` paths. `docs/API.md` lists them explicitly, because
  requesting `/api/v1/robots.txt` returns 404.
- **Middleware chain**, outermost to innermost, applied in
  `internal/httpapi/server.go:372`:

  `requestID → gzip → recover → securityHeaders → accessLog → etag → cors → csrf → auth → rateLimit → admin2FA → routes`

  Two consequences worth stating: `withAuth` is **optional** and never rejects —
  it populates the context and each handler decides what it requires — and
  `withRateLimit` is inside `withAuth`, so a tier keyed by *user* only sees a
  user if authentication succeeded. `withRecover` covers the request goroutine
  only; detached goroutines and WebSocket pumps recover separately (§7.4).

- **Auth:** httpOnly+Secure+SameSite cookies (`bv_access` JWT 15 min,
  `bv_refresh` rotating 30 days, registry-backed per §5.9.1/§7.4). CSRF:
  double-submit token cookie `bv_csrf`; all mutating requests must send
  `X-CSRF-Token`. The one CSRF-exempt path is `POST /billing/webhook`, which is
  authenticated by Stripe signature over the raw body instead.
- **Errors:** `{ "error": { "code": "<stable_code>", "message": "<human>", "fields": { "field": "msg" } } }` (PRD §11.2). Codes documented per endpoint. `domain.Error` sentinels are immutable — `WithField` clones, because mutating a shared global per request leaked fields across concurrent requests.
- **Pagination:** `?limit=&offset=`, default 20–50 depending on the list, hard ceiling 100.
- **Ids:** UUIDv4, typed as `uuid` in JSON. See the deviation note above.
- **Money:** `{ "amount": "12.50", "currency": "USD" }` — never bare numbers.
  Conversion is display-only and happens client-side from the hourly
  `currency_rates` snapshot; the server stores the original.
- **Time:** RFC3339 UTC. Open-now is computed server-side per business timezone
  (`is_open_now`), and the SQL and Go implementations are held in parity by a
  table-driven test.
- **Rate-limit tiers** — thirteen, checked in order, first match wins, and each
  namespace-keyed by `tier:key` so two tiers never share a counter:

  | Tier | Applies to | Budget | Keyed by |
  |---|---|---|---|
  | `auth` | register, login, forgot/reset password, restore | 5/min | IP |
  | `tfa` | 2FA verify | 3/15min | IP |
  | `refresh` | token refresh | 60/min | IP |
  | `search` | search, suggest, user search | 60/min | IP |
  | `ssr` | `/ssr/*` | 60/min | IP |
  | `sitemap` | sitemap.xml | 10/min | IP |
  | `export` | GDPR export, thread export | 5/h | IP |
  | `media` | uploads | 20/h | user |
  | `engage` | all engagement writes | 30/min | user |
  | `chat` | message send | 30/min | user |
  | `typing` | typing indicators | 60/min | IP |
  | `billing` | checkout + portal | 10/h | user |
  | `billinghook` | Stripe webhook | 600/min | IP |
  | default | everything else | `RATELIMIT_GLOBAL` (120) | IP |

  On top of the middleware tiers, failed auth attempts draw on **per-account**
  buckets in the handler (`10/15min` per (account, IP) and `40/15min` per
  account, three independent kinds). Those deliberately run *after* the
  credential check: an earlier version keyed them on the submitted email before
  verifying anything, which let an unauthenticated caller lock any account out
  for 15 minutes, repeatably and with no notification.

  `IP` is a canonicalised address — IPv4-mapped IPv6 folded to IPv4, IPv6
  expanded forms collapsed — so a /64 holder cannot mint 2⁶⁴ separate 5/min
  login buckets. `X-Forwarded-For` is honoured only when
  `TRUST_X_FORWARDED_FOR=true`, only its rightmost entry, and a single-value XFF
  is treated as attacker-controlled and ignored.

- **ETags** on small JSON GET responses under `/api/v1` (errors and streamed files
  pass through untouched). The digest is of the body, so a mutation is not a
  cache-busting event by itself.
- **Response shape:** every JSON response passes through a reflective
  normaliser that renders nil slices as `[]` rather than `null`, and deliberately
  leaves pointers and maps alone so `logo_url: null` stays null.


## 2. Route surface

**`docs/API.md` is the endpoint reference.** It is verified against
`internal/httpapi/server.go` and contains no phantom entries. This section
exists to explain the *shape* of the surface — which routes are public, which
are guarded, and where the boundaries are — because those are the things a route
table cannot tell you.

### 2.1 Authorisation model

There is no RBAC and no per-route role declaration. The role set is binary —
`user` and `admin` (`domain/models.go`) — and authorisation is decided at three
separate points, which is worth knowing because it means "is this route
protected?" is not a single question:

1. **`adminOnly(fn)`** wraps 34 routes. Requires the `admin` claim. This is the
   only *route-level* guard in the system; a handler that is not wrapped cannot
   be admin-gated without a code change.
2. **`apiKeyOnly(fn)`** wraps the seven `/api/v2/*` read routes. Validates
   `X-API-Key`, requires the `read` scope, applies a separate 300 req/min
   per-key bucket, and injects the key's owner as the request user. Banned
   owners are rejected.
3. **Handler-level** ownership checks everywhere else. `withAuth` is optional
   middleware that never rejects; ~200 handlers independently decide what they
   require via `currentUser(r)`. Tenant boundaries are enforced in the
   repository layer — `CanManageBusiness` for owner-or-accepted-co-owner,
   `IsBusinessViewer` for read-only, and `chat_participants` membership for
   threads.

That third point is the security posture's real weak spot, and it is why
migrations `0041` (least-privilege role) and `0042` (row-level security) are
reserved. A single missed `WHERE` clause in a repository method is a cross-tenant
read, not a 500. See `SECURITY.md`.

### 2.2 Publicly reachable surface

Unauthenticated, and therefore the part that has to be right about abuse limits:

| Surface | Route | Notes |
|---|---|---|
| Directory | `GET /categories`, `/categories/{slug}`, `/cities`, `/cities/{slug}`, `/search`, `/search/suggest`, `/compare`, `/featured`, `/leaderboards`, `/trending`, `/rising`, `/meta`, `/rates` | search is the heaviest read path; two-phase, see §5 |
| Businesses | `GET /b/{slug}` | serves verified and paused only; `?draft=1` for the owner |
| Profiles | `GET /u/{username}`, `/collections/{id}` | public collections only |
| SEO | `GET /robots.txt`, `/og/b/{slug}`, `/ssr/b/{slug}`, `/ssr/c/{slug}`, `/ssr/city/{slug}`, `/api/v1/sitemap.xml` | the first four are at the **origin root**, not under `/api/v1` |
| Ops | `GET /health`, `/ready`, `/metrics` | `/metrics` is open in non-prod, and in prod needs either an admin session or the `METRICS_TOKEN` bearer |
| Writes | `POST /api/v1/auth/*`, `/auth/oauth/google`, `/auth/csrf`, `POST /errors`, `/support/contact` | auth-tier and IP-tiered |

`GET /auth/csrf` deserves a callout: it is the bootstrap endpoint that issues the
`bv_csrf` cookie the SPA needs before its first mutation, and it is required for
any client to function at all. It was undocumented, which made the API
unusable by a third party who was not reading the frontend.

### 2.3 Owner surface

All under `/businesses/{id}` or `/products/{id}`, all requiring
`CanManageBusiness` (owner **or** an accepted `co_owner` invite). Co-owner
invites carry their own role, and `viewer` is genuinely read-only.

Creation is a six-step wizard with per-step autosave and resume of the latest
draft or rejected listing. Products are full-fidelity: option groups, generated
variants with per-variant SKU, price, currency, image and stock, plus a
duplicate action that copies variants.

Two entitlements are enforced on this surface, both capacity-only, both
resolving the business's plan per request:

- `product_limit` — checked on product create, counted per type
  (products and services share the cap but not the pool), excluding
  soft-deleted rows so a deleted product stops occupying a slot.
- `gallery_limit` — checked against the array the owner is about to save, so
  removing and re-adding photos in one edit is judged on the result.

A plan that omits a cap is treated as **uncapped, not zero**. A limit nobody
agreed to would lock an owner out of their own storefront after they upgraded
for an unrelated reason.

### 2.4 Moderation surface

`/admin/*` is 34 routes behind `adminOnly`. It covers the verification queue
(with per-view document auditing), report moderation with an immutable
`moderation_actions` trail, category CRUD with merge, user warn/suspend/ban,
curation, site config, claims, appeals, trending anomalies, and KPIs.

Two details that are not obvious from the route list: an admin without enrolled
TOTP is blocked from **everything** except the enrolment, refresh, logout and 2FA
routes themselves (`withAdmin2FA`), and the document viewer is the only place in
the product that serves a file with `CSP: default-src 'none'; sandbox`.

## 3. WebSocket Protocol

**Connect:** `GET /api/v1/ws` — **cookie-authenticated only**. There is no
`?token=` query parameter: it leaked a live 15-minute credential into proxy
access logs and browser history. The route is exempt from ETag buffering and
gzip, and the middleware chain's `statusRecorder`/`etagRecorder` implement
`Hijack` so the upgrade survives it.

**Multi-device.** A user may hold any number of concurrent connections — tabs,
phone, desktop — and `SendToUser` delivers to all of them. The registry is
`map[userID]map[*conn]struct{}` (`internal/ws/hub.go:29`). *This document
previously claimed "one connection per user, last wins"; that was wrong.*

The per-connection `subscribe` set is **local to the connection and not shared
across replicas**. A `typing` frame raised on instance B will not reach a client
connected to instance A, even though `message.new` fan-out does cross instances
via Redis. That asymmetry is inherent to the design and is called out here
because it is the kind of thing that looks like a bug in staging and is not.

**Frames** — JSON envelope: `{ "id": "<client uuid>", "type": "<event>", "payload": {...} }`

| Direction | Type | Payload | Notes |
|-----------|------|---------|-------|
| S→C | `welcome` | `{user_id}` | sent on connect |
| S→C | `pong` | — | echoes the request's `id` |
| C→S | `ping` | — | answered with `pong`; **nothing in this codebase sends it** |
| C→S | `subscribe` | `{thread_ids[]}` | capped at 50, each id membership-checked, non-members silently dropped |
| S→C | `message.new` | full message | at-least-once; dedupe by `client_msg_id` / message id |
| S→C | `message.edited` | full message | also reused for async link-preview enrichment |
| S→C | `message.deleted` | `{message_id, deleted_for}` | tombstone, **`everyone` scope only**. A private delete is NOT broadcast — the message still exists for everyone else, so there is nothing to tombstone. |
| S→C | `reaction.updated` | `{message_id, emoji, user_id}` | |
| S→C | `receipt.read` | `{thread_id, user_id, last_read_message_id}` | |
| S→C | `typing` | `{thread_id, user_id, is_typing}` | 3s expiry |
| S→C | `notification.new` | `{notification, unread}` | real-time in-app |

**Documented but not emitted:** `receipt.delivered`, `presence`,
`unread.updated`, `media.ready`, `leaderboard.refresh`, and the client-initiated
`sync {since_message_id}` replay. Four of those five were previously presented in
this table as though they were contract. They are not, and nothing in the
codebase references them.

**Keepalive** is server-driven: a WebSocket protocol `PingMessage` every 30s
against a 70s read deadline refreshed by `SetPongHandler`.

**Delivery and reconnect.** The server persists before broadcasting, and
`client_msg_id` is `UNIQUE (thread_id, client_msg_id)` so a retried send returns
the existing message rather than duplicating it. There is **no server-side
replay**: the client refetches the thread tail over REST using the `?before=`
keyset, which also gives it `has_more` without a second count query. Frames
published while a replica's Redis subscriber is reconnecting are lost, by design
— the client resyncs over REST.

**Backpressure.** Per-connection send buffer of 64 frames; a full buffer drops
the frame and the client resyncs. Drops are not counted or exported, so a
systematically slow client population is invisible in `/metrics`. There is no
slow-consumer metric today.

**Panic isolation.** `withRecover` covers the request goroutine only. The WS
`readPump`/`writePump` are started with a bare `go` and have **no** recovery, so
a panic there would take the process down. `util.GoNamed` exists for exactly this
purpose and is used by every other detached goroutine, but not by the pumps.

**Cross-instance fan-out** is a hand-rolled Redis RESP client
(`internal/ws/redis.go`) publishing to `bizverse:ws` on a bounded 256-frame queue
with drop-on-full. Each frame carries an originating instance tag and the
subscriber drops its own echoes — without that, Redis redelivering publishes to
the publishing connection amplifies the fan-out exponentially.

## 4. Background Jobs

All jobs run **in-process** inside the API binary. There is no separate worker
binary. One instance wins `pg_try_advisory_lock(hashtext('bizverse:jobs'))` and
the rest block on `<-ctx.Done()` until shutdown; a DB error during acquisition
retries with a 3s backoff against a 2-minute deadline, so two replicas booting
during a blip cannot both fan out duplicate mail.

Every job also runs **once at boot** before any ticker fires.

| Job | Cadence | Work |
|-----|---------|------|
| `trending` | 10 min | `Σ(weight × e^(−λ·age))` over three windows → `trend_snapshots`, then velocity, global/category/city ranks, and the Booming/Rising flags (§5.6.3). Zero-score rows are skipped. Spike anomalies set `engagement_events.flagged` and raise an admin notification. |
| `currency` | hourly | rate feed → `currency_rates`, one batched `unnest` upsert (was ~160 individual statements) |
| `billing_reconcile` | 6h | re-read subscriptions from Stripe, so a dropped webhook cannot silently revoke or grant a paid plan |
| `search_alerts` | 24h | daily saved-search results, claimed per UTC day in `job_runs` |
| `purges` | 24h | notification expiry, account-deletion anonymisation, and the retention sweep below |
| `ops` | 24h | trend-snapshot prune (14d, keeps the latest per period) and orphan-media cleanup with a 24h file grace so an in-flight upload is never destroyed |
| `weekly_digest` | hourly check, sends weekly | claimed against the current ISO week; released on failure so the next tick retries rather than burning the week |

**Retention** (`purges`, chunked by `ctid` at 50k rows per statement):
consumed tokens >48h, revoked sessions >90d, engagement events >90d, auth events
>180d. `billing_webhook_events` has **no** retention job despite a comment in
migration `0028` saying the purge handles it.

**Idempotency** is a `job_runs(job, period)` ledger with `ON CONFLICT DO
NOTHING`, used by the digest and the search alerts. On failure the claim is
released so the next tick retries. There is no retry counter and no
dead-lettering anywhere in the scheduler: a job that fails on the last tick of
its period waits a full period.

**Not implemented**, and previously listed here as though they were: the FFmpeg
media pipeline, an async email queue, and on-demand export jobs. The list above
is the complete set.

## 5. Data Flow Contracts (PRD §10.5)

- **Publish:** the draft `theme`/`layout` are copied into `published_snapshot`
  in one transaction, then `last_published_at` is set. The public page always
  reads the snapshot, so a draft is never public. *This document previously
  claimed a `FOR UPDATE` lock, an ETag bump and a CDN purge; there is no CDN and
  the ETag is a body digest computed per response, not a mutation event.*
- **Search:** two-phase. Phase 1 selects only the page's business ids; phase 2
  hydrates the full row shape for exactly those ids. The split exists because
  the five correlated count subqueries in the row shape (rating average, review
  count, like, recommend and save counts) were being evaluated per *candidate*
  rather than per *result*.
  Full-text is a five-way `OR` — a `tsvector` match, an `ILIKE`, a trigram
  similarity, a category-name match, and an `EXISTS` over published products —
  indexed by GIN on **`IMMUTABLE` helper functions** (`biz_search_tsv`,
  `product_search_tsv`) added in migration `0031`. There is no `tsvector`
  *column* and no FTS trigger; the helpers exist precisely so the index
  expression and the query text cannot drift apart, which three hand-written
  copies of that predicate had already done.
  Geo is `cube` + `earthdistance` for radius and a plain
  `lat BETWEEN … AND lng BETWEEN …` for a bounding box. **PostGIS is not
  installed** and `ST_MakeEnvelope` appears nowhere.
  Open-now is a SQL function so pagination stays exact, and the Go twin that
  renders the badge is held in parity by a table-driven test.
  `with_total` is opt-in because the count re-evaluates the whole predicate; an
  uncomputed count renders as `null`, not `0`, because rendering zero would be a
  lie.
- **Engagement:** events are append-only, deduped on `user:target:signal:day`,
  and the owner's own actions are excluded at write time.
- **Verification documents:** upload → `media` row (kind
  `document_verification`, AES-256-GCM at rest) → admin viewer serving with
  `CSP: default-src 'none'; sandbox` and a `verification_document_views` row per
  view → decision → level assignment. *This document previously claimed a
  watermark; no watermark is applied to the bytes.*

## 6. Environments & Deploy (PRD §10.6)

- **dev:** `docker compose` for Postgres, Redis, MinIO, Mailpit and ClamAV. The
  API and web app run on the host, not in compose — there is no `api` service in
  the compose file. *This document previously listed one.*
- **staging / prod:** Vercel hosts the web app; Railway hosts the API, Postgres
  and Redis. Migrations run at boot under an advisory lock, before the listener
  opens, so a new revision migrates and then serves. See `DEPLOYMENT.md`.
  A single API replica with a persistent volume for `MEDIA_DIR` is the supported
  production topology, because there is no object-storage adapter.
- **CI** gates `go vet`, `go build`, all migrations applying to a real
  PostgreSQL 17, the seed loading, `go test`, `tsc`, ESLint, the production
  build, and the Playwright suite. Deployment is gated on CI passing.


---

## 7. Addendum (platform extensions)

### 7.1 Community & profiles
- Q&A: questions/answers tables; owner answers highlighted; notifications on ask/answer (PRD §5.6.2 extension).
- Follows + owner announcements: follows table (unique user+business); business_updates with follower notification fan-out; following feed endpoint.
- Public profiles: GET /u/:username → reviews/comments/public collections/verified businesses.

### 7.2 Verification & trust (B3)
- Resubmission cap: businesses.resubmit_count (max 3, PRD §8.2).
- Appeals: appeals table; user submits via /me/appeal; admin decides (approve restores account); result notified.
- Co-owner invites: business_invites (accepted_at = active); CanManageBusiness replaces owner-only checks everywhere.
- Trending anomalies: engagement_events.flagged → admin review queue; resolve clears the flag.

### 7.3 Messaging extensions
- Link previews: server-side og: extraction on text sends (SSRF-safe: http/https only, 2s timeout, 256KB cap, private IPs rejected); stored in chat_messages.link_preview.
- Pinned messages: chat_participants.pinned_message_ids (bigint[], max 5) + pin endpoints.
- Quiet hours: notification_prefs.quiet_hours enforced for push delivery (22:00–08:00 local).

### 7.4 Platform ops
- KPI endpoint: /admin/kpis (counts + 14-day registration series).
- Site config: site_config key/value JSONB (announcement banner); exposed at /meta.
- Currency: currency_rates synced hourly (open.er-api.com); read-time conversion client-side; display currency persisted per user.
- OG images: /og/b/{slug} generates an SVG share card; referenced by og:image meta.
- **SEO:** server-rendered by the API, not prerendered. `GET /ssr/b/{slug}`, `/ssr/c/{slug}` and `/ssr/city/{slug}` (`handlers_ssr.go`) return a complete crawlable HTML document with per-row meta, canonical, OG/Twitter and JSON-LD; `apps/web/vercel.json` rewrites `/b/:slug`, `/c/:slug` and `/city/:slug` onto them. The SPA mounts into `#root` and removes `#seo` before first paint. Assets are resolved from `WEB_DIST_DIR/.vite/manifest.json`, and a missing manifest degrades to a no-JS document rather than to an empty page. The old `apps/web/scripts/prerender.mjs` was removed: CI served `vite preview` on `:5173` while the script targeted `:4173`, it wrote into `dist/prerendered/` which nothing served, and its per-route `catch` exited 0, so it produced 0 files in a green run.

- Support: /support/contact stores a support report for the admin inbox.
