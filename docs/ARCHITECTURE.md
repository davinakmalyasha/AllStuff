# BizVerse — Architecture & API Design

Companion to `PRD.md`. Golden standard applies: every endpoint in this doc is the full contract for that resource.

> **Implementation status note (docs aligned to code):** this document describes the target design. Where the code differs today, these are the authoritative deviations:
> - **OAuth:** Google only (`GET /auth/oauth/google` + `/callback`); Facebook/Apple/GitHub are roadmap.
> - **Pagination:** `?limit=&offset=` everywhere (limit ≤ 100); no cursors or `X-Next-Cursor`.
> - **Data export:** synchronous download at `GET /me/export`; account deletion is `POST /me/delete` with `POST /me/delete/cancel` (password-verified) and a grace-period restore at `POST /auth/restore`.
> - **Recovery codes:** regenerate at `POST /me/security/2fa/recovery-codes`.
> - **WebSocket:** one connection per user ("last wins"); frames delivered server→client are `welcome`, `message.new`, `message.edited`, `message.deleted`, `reaction.updated`, `receipt.read`, `typing`, `notification.new`. Client→server supports `ping` (→`pong`) and `subscribe` (**membership-checked** since the 2026-08 audit). `receipt.delivered`/`presence`/`media.ready`/replay-sync are not implemented yet.
> - **Media upload:** direct multipart `POST /api/v1/media` streaming to disk with per-kind caps; ClamAV INSTREAM scan when `CLAMAV_ADDR` is set. Chat media is served only to authenticated thread participants; verification docs are admin-only. Presigned uploads and the async FFmpeg variant pipeline are roadmap.
> - **Rate limiting & jobs:** in-memory limiter (not Redis) — set `REDIS_URL` only for multi-instance WS fan-out. All jobs run in-process on one instance (advisory-lock leader election with retry; weekly digest/alerts idempotent via the `job_runs` table).
>
> Where the text below conflicts with the list above, the list wins.

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

## 1. Conventions

- Base path: `/api/v1`. Public reads are unauthenticated; writes require auth unless noted.
- **Auth:** httpOnly+Secure+SameSite cookies (`bv_access` JWT 15 min, `bv_refresh` rotating 30 days, registry-backed per §5.9.1/§7.4). CSRF: double-submit token cookie `bv_csrf`; all mutating requests must send `X-CSRF-Token`.
- **Errors:** `{ "error": { "code": "<stable_code>", "message": "<human>", "fields": { "field": "msg" } } }` (PRD §11.2). Codes documented per endpoint.
- **Pagination:** `?limit=&offset=`, default 20–50 depending on the list, hard ceiling 100.
- **Ids:** UUIDv7; typed as `uuid` in JSON.
- **Money:** `{ "amount": "12.50", "currency": "USD" }` — never bare numbers.
- **Time:** RFC3339 UTC. Open-now computed server-side per business timezone (`is_open_now` field).
- **Rate limits** (in-memory, per IP or user where noted): auth 5/min/IP **plus 10/15min per account**; 2FA verify 3/15min/IP plus per-challenge throttle; engagement writes 30/min/user; chat sends 30/min/user; search/suggest/users-search 60/min/IP; media upload 20/h/user; sitemap 10/min/IP; exports 5/h; global 120/min/IP.
- **ETags** on small JSON GET responses under `/api/v1` (errors and streamed files pass through untouched).

## 2. REST Endpoints

### 2.1 Auth & accounts
| Method | Path | Notes |
|--------|------|-------|
| POST | `/auth/register` | email+password; emits verify email |
| POST | `/auth/login` | password; if 2FA enabled → `2fa_required` challenge id |
| POST | `/auth/2fa/verify` | TOTP or recovery code; returns session cookies |
| POST | `/auth/oauth/:provider` | **Google only** (implemented as `GET /auth/oauth/google` + `/callback`); facebook\|apple\|github are out-of-scope/roadmap — see deviations note |
| POST | `/auth/refresh` | rotate refresh token (cookie) |
| POST | `/auth/logout` | revoke session |
| POST | `/auth/verify-email` | `{token}` |
| POST | `/auth/resend-verification` | 60s cooldown |
| POST | `/auth/forgot-password` | `{email}` → email token (15 min, single-use) |
| POST | `/auth/reset-password` | `{token, password}` |
| GET  | `/me` | own profile + derived roles/claims |
| PATCH| `/me` | name, avatar, bio, timezone, links; username edit (once) |
| GET  | `/me/security` | 2FA state, recovery-code status, sessions, login history |
| POST | `/me/security/2fa` | enroll (returns TOTP secret + QR URI) |
| POST | `/me/security/2fa/confirm` | verify code → enable; issues 10 recovery codes (hashed, returned once) |
| DELETE | `/me/security/2fa` | disable (requires current code) |
| POST | `/me/security/recovery-codes/regenerate` | invalidates previous |
| GET  | `/me/security/sessions` | active sessions (ip, ua, last_seen) |
| DELETE | `/me/security/sessions/:id` | revoke one |
| POST | `/me/security/sessions/revoke-others` | keeps current |
| POST | `/me/export` | queue full JSON export (async job, download link) |
| DELETE | `/me` | 14-day grace; cancellation via POST `/me/delete-cancel` |
| GET  | `/u/:username` | public profile: reviews, comments, public collections, businesses |

### 2.2 Discovery
| Method | Path | Notes |
|--------|------|-------|
| GET | `/categories` | full tree, counts, icons |
| GET | `/categories/:slug` | category + parent path + leaderboard (top 5) |
| GET | `/search` | params: `q, category (multi), lat, lng, radius_km, price_level (multi), min_rating, open_now, verified_only, fully_verified_only, has_chat, sort (trending\|rating\|newest\|nearest\|relevance), cursor, limit` — supports `bbox=minLng,minLat,maxLng,maxLat` for map viewport |
| GET | `/search/suggest` | autocomplete (250ms-debounced upstream): businesses + categories |
| GET | `/map` | same contract as `/search` with `bbox` required; cluster points `&cluster=true` |
| GET | `/leaderboards` | `?window=24h\|7d\|30d&scope=global\|category:\<id>\|city:\<city>` |
| GET | `/b/:slug` | full business page payload: hero, about, products (published), hours+is_open_now, contact, socials, reviews summary, comments count, engagement counters, similar, badges |
| GET | `/compare` | `?b=<id>,<id>,<id>,<id>` (2–4) → columnar payload per §5.1.5 |

### 2.3 Engagement
| Method | Path | Notes |
|--------|------|-------|
| PUT/DELETE | `/b/:slug/like`, `/products/:id/like` | toggle, idempotent |
| PUT/DELETE | `/b/:slug/recommend` | toggle |
| GET/POST | `/b/:slug/reviews` | list (sort newest\|highest\|helpful, cursor); create |
| PATCH/DELETE | `/reviews/:id` | own review; edit history kept |
| POST | `/reviews/:id/reply` | owner only, one per review |
| PUT/DELETE | `/reviews/:id/helpful` | up/down toggle |
| GET/POST | `/b/:slug/comments` | list with full nested tree (max depth 3 displayed) |
| POST | `/comments/:id/replies` | any depth |
| PUT/DELETE | `/comments/:id` | edit within 10 min |
| PUT/DELETE | `/comments/:id/like` | |
| GET/POST | `/products/:id/reviews` | product reviews (same model) |
| POST | `/reports` | `{target_type, target_id, reason}`; one open per pair |
| GET/POST | `/me/collections` | list/create (default "Favorites" auto-created) |
| PATCH/DELETE | `/me/collections/:id` | rename, privacy, description, cover, sort |
| GET/POST | `/me/collections/:id/items` | list/add `{target_type, target_id, note}` |
| PATCH/DELETE | `/me/collections/:id/items/:itemId` | note/sort; remove |
| GET | `/me/reviews` | my reviews + helpful votes |

### 2.4 Business dashboard (owner claim per business)
| Method | Path | Notes |
|--------|------|-------|
| POST | `/businesses` | start wizard; returns draft id |
| PATCH | `/businesses/:id` | wizard fields per step (draft auto-save) |
| POST | `/businesses/:id/submit` | → pending_review (documents required for Fully Verified level) |
| GET | `/businesses/:id/verification` | status, level, documents, rejection reason |
| POST | `/businesses/:id/documents` | upload verification document (kind, file) |
| DELETE | `/businesses/:id/documents/:docId` | remove (pending only) |
| GET/PUT | `/businesses/:id/storefront` | theme + layout drafts |
| POST | `/businesses/:id/publish` | atomically copies draft → published_snapshot (row lock) |
| POST | `/businesses/:id/unpublish` | |
| GET | `/businesses/:id/products` | incl. drafts, options, variants |
| POST/PATCH/DELETE | `/products/:id` | full CRUD; variants/options nested |
| POST | `/products/:id/duplicate` | copies variants |
| GET/PATCH | `/businesses/:id/settings` | info, hours, location, contact, socials, currency, danger zone (pause/close endpoints) |
| POST | `/businesses/:id/pause` / `/businesses/:id/close` | danger zone |
| GET | `/businesses/:id/analytics` | `?period=7d\|30d\|all` per §5.4.5 |
| GET | `/businesses/:id/quick-replies` | CRUD (max 20) |
| POST | `/businesses/:id/invites` | co-owner/viewer invite; DELETE revoke; POST `/invites/:token/accept` |
| GET | `/dashboard` | multi-business overview (engagement summary per business) |

### 2.5 Messaging
| Method | Path | Notes |
|--------|------|-------|
| GET | `/threads` | inbox: all (direct + business), `?business_id=` filter, `?q=` search, unread first, pinned on top |
| GET | `/threads/:id` | detail + messages (cursor desc) + participants + last_read |
| POST | `/threads` | create direct `{user_id}` or business `{business_id}` (idempotent: returns existing) |
| POST | `/threads/:id/messages` | text/image/file/audio/video/link; `client_msg_id` dedupe; returns 201 message |
| PATCH | `/messages/:id` | edit (sender only, history appended) |
| DELETE | `/messages/:id` | `?scope=me\|everyone` per §8.5 windows |
| POST | `/messages/:id/reaction` | `{emoji}` toggle (1 active per user) |
| POST | `/messages/:id/forward` | `{thread_id}` |
| GET | `/threads/:id/search` | `?q=` + cursor, context snippets |
| GET | `/threads/:id/media` | gallery grid |
| POST | `/threads/:id/read` | `{last_read_message_id}` |
| PUT | `/threads/:id/pin` / `/threads/:id/pin/:messageId` | pin thread / message (max 5) |
| PATCH | `/threads/:id/mute` | `{until}` |
| POST | `/threads/:id/close` | business side (resolved) |
| POST | `/threads/:id/leave` | user side (hides) |
| GET | `/threads/:id/export` | JSON export |
| POST | `/blocks` / DELETE `/blocks/:userId` | block/unblock |
| GET | `/blocks` | my block list |

**WS** — `/ws` (cookie-authenticated only): see §3.

### 2.6 Notifications
| Method | Path | Notes |
|--------|------|-------|
| GET | `/notifications` | cursor, `?type=&unread_only=` |
| POST | `/notifications/read` | `{ids}` or `{all: true}` |
| PATCH | `/notifications/settings` | per-type × per-channel matrix + quiet hours (PRD §5.7) |
| POST | `/push/subscribe` / DELETE `/push/subscribe` | VAPID subscription |

### 2.7 Admin (`admin` claim; `/api/v1/admin/*`)
| Method | Path | Notes |
|--------|------|-------|
| GET | `/admin/verify` | queue `?status=pending_review\|rejected\|resubmit` |
| GET | `/admin/verify/:businessId/documents` | secure viewer — watermarked, access-logged (`verification_document_views`) |
| POST | `/admin/verify/:businessId/decide` | `{decision: approve, level, reason} \| {reject, reason, request_document}` |
| GET | `/admin/reports` | queues per target type |
| GET | `/admin/reports/:id` | context view (target + evidence) |
| POST | `/admin/reports/:id/decide` | dismiss \| warn `{message}` \| hide \| delete_for_everyone \| suspend `{days, reason}` |
| POST | `/admin/content/:type/:id/hide\|restore` | direct actions, reason required |
| GET/POST/PATCH/DELETE | `/admin/categories` | tree CRUD, merge `{into_id}`, bulk reassign |
| GET | `/admin/users` | `?q=` search |
| GET | `/admin/users/:id` | profile: businesses, reviews, reports, ban history, sessions |
| POST | `/admin/users/:id/warn\|suspend\|ban\|unban` | reason required |
| GET/PUT | `/admin/curation` | featured picker, rising strip, leaderboard config (weights, windows, N) |
| GET/PUT | `/admin/settings` | site config, announcement banner |
| GET | `/admin/analytics` | KPIs per §5.8.6 |
| GET | `/admin/health` | roadmap; covered today by public `/health` (liveness + depth) and `/metrics` |
| GET | `/admin/moderation-actions` | audit trail query |

### 2.8 Media & misc
| Method | Path | Notes |
|--------|------|-------|
| POST | `/media/presign` | documented deviation — replaced by direct multipart `POST /media` streaming upload (see note above); presigned URLs remain roadmap |
| GET | `/media/:id/status` | pipeline status (documented deviation: uploads are direct multipart today; async FFmpeg variant pipeline is roadmap) |
| GET | `/meta` | leaderboard "updated X min ago", currency rates fresh flag, announcement banner |
| GET | `/sitemap.xml`, `/robots.txt` | SEO (PRD §9.4); both **implemented** — the API serves `/robots.txt` publicly (`text/plain`) with an absolute `Sitemap:` URL derived from `PUBLIC_URL` |

## 3. WebSocket Protocol

**Connect:** `/ws` — cookie-authenticated only (no `?token=` query parameter, no `device_id`). One connection per user; presence shared per user across devices.

**Frames** — JSON envelope: `{ "id": "<client uuid>", "type": "<event>", "payload": {...} }`

| Direction | Type | Payload | Notes |
|-----------|------|---------|-------|
| C→S | `ping` | | keepalive 30s |
| C→S | `subscribe` | `{thread_ids[]}` | join threads for live events |
| S→C | `message.new` | full message | at-least-once; dedupe by `client_msg_id`/message id |
| S→C | `message.edited` | `{message_id, body, edited_at}` | |
| S→C | `message.deleted` | `{message_id, deleted_for}` | tombstone |
| S→C | `reaction.updated` | `{message_id, emoji, delta, user_id}` | |
| S→C | `receipt.delivered` | `{message_id}` | documented deviation — not sent today; clients refetch the thread tail on reconnect |
| S→C | `receipt.read` | `{thread_id, user_id, last_read_message_id}` | |
| S→C | `typing` | `{thread_id, user_id, is_typing}` | 3s expiry |
| S→C | `notification.new` | notification payload | real-time in-app |
| S→C | `unread.updated` | `{thread_id, unread_count}` | |
| S→C | `presence` | `{user_id, online}` | chat-only visibility |
| S→C | `media.ready` | `{media_id, variants}` | pipeline complete |
| S→C | `leaderboard.refresh` | `{window}` | client re-fetches (10-min cadence) |

**Delivery:** server persists before ack; reconnect → client sends `sync {since_message_id}` → server replays missed (REST `/threads/:id` cursor). Outbox + `client_msg_id` dedupe on server (UNIQUE constraint). Redis pub/sub fans out across instances.

## 4. Background Jobs

| Job | Cadence | Work |
|-----|---------|------|
| trending recompute | 10 min | aggregate engagement_events → trend_snapshots (§5.6.3); anomaly flags (`flagged`) → admin notification |
| currency sync | hourly | rate feed → currency_rates |
| media pipeline | on upload | FFmpeg variants (image WebP/AVIF, audio m4a, video mp4+poster), ClamAV scan, status update + WS notify |
| email queue | continuous | Resend: verify, reset, alerts, digests; chat emails throttled (1/thread/5min) |
| notification cleanup | daily | expiry per §5.7 retention |
| orphan media cleanup | daily | unreferenced media > 24h |
| export jobs | on demand | user data JSON → object storage → download link |
| digest | weekly opt-in | per §5.7 |

## 5. Data Flow Contracts (PRD §10.5)

- **Publish:** `SELECT ... FOR UPDATE` on business → copy draft theme/layout into `published_snapshot` → `last_published_at` → ETag bump → CDN purge.
- **Search:** triggers maintain `tsvector` on businesses/products/chat; `pg_trgm` for prefix; filters in SQL; bbox via PostGIS `ST_MakeEnvelope` + earthdistance fallback.
- **Engagement:** events append-only; dedupe key `user:target:signal:day`; owner self-actions excluded at write time (`owner_id` check).
- **Verification documents:** upload → media (kind `document_verification`, encrypted at rest) → admin secure viewer (watermark + `verification_document_views` row per view) → decision → level assignment.

## 6. Environments & Deploy (PRD §10.6)

- **dev:** docker compose (api, db, redis, minio, mailpit, clamav), seeded categories + demo businesses.
- **staging:** Railway preview; prod-mirror schema; sanitized seed.
- **prod:** Vercel (web) + Railway (api/ws/workers/postgres/redis/R2). CI: lint+typecheck+test on PR; Vercel preview per PR; Railway deploy on main; migrations run before app start.

---

## 7. Addendum (platform extensions)

### 7.1 Community & profiles
- Q&A: questions/answers tables; owner answers highlighted; notifications on ask/answer (PRD �5.6.2 extension).
- Follows + owner announcements: follows table (unique user+business); business_updates with follower notification fan-out; following feed endpoint.
- Public profiles: GET /u/:username � reviews/comments/public collections/verified businesses.

### 7.2 Verification & trust (B3)
- Resubmission cap: businesses.resubmit_count (max 3, PRD �8.2).
- Appeals: appeals table; user submits via /me/appeal; admin decides (approve restores account); result notified.
- Co-owner invites: business_invites (accepted_at = active); CanManageBusiness replaces owner-only checks everywhere.
- Trending anomalies: engagement_events.flagged ? admin review queue; resolve clears the flag.

### 7.3 Messaging extensions
- Link previews: server-side og: extraction on text sends (SSRF-safe: http/https only, 2s timeout, 256KB cap, private IPs rejected); stored in chat_messages.link_preview.
- Pinned messages: chat_participants.pinned_message_ids (bigint[], max 5) + pin endpoints.
- Quiet hours: notification_prefs.quiet_hours enforced for push delivery (22:00�08:00 local).

### 7.4 Platform ops
- KPI endpoint: /admin/kpis (counts + 14-day registration series).
- Site config: site_config key/value JSONB (announcement banner); exposed at /meta.
- Currency: currency_rates synced hourly (open.er-api.com); read-time conversion client-side; display currency persisted per user.
- OG images: /og/b/{slug} generates an SVG share card; referenced by og:image meta.
- Prerendering: apps/web/scripts/prerender.mjs renders public routes to static HTML for SEO.
- Support: /support/contact stores a support report for the admin inbox.
