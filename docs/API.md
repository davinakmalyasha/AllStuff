# BizVerse — API Reference

Base URL: `/api/v1` · Auth: httpOnly cookies (`bv_access` 15min, `bv_refresh` rotating) · CSRF: `X-CSRF-Token` header on mutations · Errors: `{ "error": { "code", "message", "fields? } }` · Pagination: `limit`/`offset` — general cap 50 on search/list endpoints; leaderboards accept up to 100 (documented exception).

## Auth & accounts
- `POST /auth/register` · `POST /auth/login` (2FA-gated: returns `challenge`) · `POST /auth/2fa/verify`
- `POST /auth/refresh` · `POST /auth/logout` · `POST /auth/verify-email` · `POST /auth/resend-verification` (always `{"sent":true}`; 5/h per account) · `POST /auth/forgot-password` · `POST /auth/reset-password`
- `POST /auth/restore` — cancel a pending account deletion during the 14-day grace (email + password)
- Invites: `GET /invites/{token}` (public preview: `{business_name, inviter_name, role, email}`) — accept via `POST /invites/{token}/accept` below
- `GET /auth/oauth/google` · `GET /auth/oauth/google/callback` (redirect flow)
- CSRF: `GET /auth/csrf` — idempotent; sets the site-wide `bv_csrf` double-submit cookie that the SPA echoes back in `X-CSRF-Token` on every mutation. Public and unauthenticated, because it is what a not-yet-signed-in visitor's first write depends on.
- `GET /me` · `PATCH /me` · `GET /me/export` · `POST /me/delete` · `POST /me/delete/cancel` (body: `password`)
- Security: `GET /me/security`, `POST /me/security/2fa`, `/2fa/confirm`, `DELETE /me/security/2fa`, `POST /me/security/2fa/recovery-codes`, `GET /me/security/sessions`, `DELETE /me/security/sessions/{id}`, `POST /me/security/sessions/revoke-others`
- Password/email: `POST /me/password` (`current_password`, `new_password`) · `POST /me/email` (`password`, `email`, `totp_code?` when 2FA enrolled; notifies the old address)
- API keys: `GET|POST /me/api-keys` · `DELETE /me/api-keys/{keyId}`
- Claims: `POST /claims` · `GET /me/claims`
- Prefs: `GET|PATCH /me/notifications-settings` · `POST /me/appeal`

## Directory
- `GET /categories` (tree+counts) · `GET /categories/{slug}` (page+leaderboard) · `GET /b/{slug}` (`?draft=1` = owner preview) · `GET /u/{username}`
- `GET /search` (`q, category*, lat, lng, radius_km, bbox, price_level*, min_rating, open_now, verified_only, fully_verified_only, sort, limit, offset, with_total`) · `GET /search/suggest` · `GET /users/search?q=`
  - Response: `{ businesses, count, has_more }`. **`count` is `null` unless
    computed.** The exact count re-evaluates the entire candidate predicate — the
    5-way FTS OR, the per-row products `EXISTS`, a `biz_is_open_now` call per
    row, the `avg(rating)` subquery and the trend join — so it is opt-in: it
    defaults on for `offset=0` (where a UI shows "1–24 of N") and off for
    subsequent pages, which read the free `has_more` instead. `with_total=1`
    forces it on; `with_total=0` forces it off, which internal callers that read
    only `businesses` should pass. Rendering `count: null` as `0` would claim
    "no results" on a page visibly showing 24.
- `GET /compare?b=id,id` (2–4) · `GET /featured` · `GET /trending` · `GET /rising` · `GET /leaderboards?window=24h|7d|30d&scope=global|category:{id}|city:{c}`
- Cities: `GET /cities` · `GET /cities/{slug}` (landing page payload)
- `GET /rates` (currency) · `GET /meta` (announcement, rates freshness) · `GET /sitemap.xml` · `GET /og/b/{slug}` (SVG share card)
- `GET /robots.txt` (public, no auth, `text/plain`): `User-agent: *` allow-all plus an absolute `Sitemap:` URL built from `PUBLIC_URL` (e.g. `Sitemap: https://<your-domain>/api/v1/sitemap.xml`)

## Businesses (owner)
- `POST /businesses` (draft) · `GET /businesses` · `GET|PATCH /businesses/{id}` · `POST /businesses/{id}/submit|resubmit|publish|unpublish|pause|reopen|close`
- `POST /businesses/{id}/slug-change` (one-time owner-requested slug change, PRD §8.2)
- `PUT /businesses/{id}/storefront` · `GET /businesses/{id}/analytics?period=7d|30d|all`
- Documents: `GET|POST /businesses/{id}/documents` · `DELETE /businesses/{id}/documents/{docId}`
- Bookings: `GET /businesses/{id}/availability?product_id=&date=YYYY-MM-DD[&staff_id=][&location_id=]` (public; slots derived from hours, special hours, service duration, existing appointments, blackouts and, when the business has staff, each person's shift and calendar) · `POST /businesses/{id}/appointments` · `GET /businesses/{id}/appointments?from=&to=` (manager) · `GET /me/appointments` · `DELETE /appointments/{appointmentId}` (customer or manager) · `PATCH /appointments/{appointmentId}/reschedule` (customer or manager; the new time must be an exact generated slot for the same service and staff, so closed days and off-grid times are rejected, and a lost race is still **409 `slot_taken`**; moving a booking releases its reminder claims so the new time is reminded) · `GET /appointments/{appointmentId}/calendar.ics` (customer or manager; an RFC 5545 VEVENT with CRLF endings, 75-octet folding on rune boundaries and escaped text) · `PATCH /appointments/{appointmentId}/status` (manager: `confirmed` from pending, `completed`/`no_show` once it has started)
- Waitlist: `POST /businesses/{id}/waitlist` (`product_id`, `day` YYYY-MM-DD in the business timezone; idempotent per customer+service+day) · `GET /businesses/{id}/waitlist` (manager; per-day counts only, no customer identities) · `GET /me/waitlist` · `DELETE /waitlist/{waitlistId}` (owner of the entry only). A cancellation notifies the earliest un-notified waiter for that service and day once; the slot is **not** held, so normal first-come booking still decides the race.
- Locations: `GET /businesses/{id}/locations` (public, except that a manager also sees them while the listing is in review) · `POST /businesses/{id}/locations` · `PATCH|DELETE /businesses/{id}/locations/{locationId}` · `POST /businesses/{id}/locations/{locationId}/primary` (manager) · `GET|PUT /businesses/{id}/locations/{locationId}/service-hours[/{productId}]` (manager; a per-service window that can only NARROW the place's hours, and a missing weekday means the service follows the place). Each place has its own address, hours and timezone; the business's own address columns mirror the PRIMARY location so search, the directory and the SSR pages keep working. The last location cannot be deleted, and deleting the primary promotes another.
- Staff: `GET|POST /businesses/{id}/staff` · `PATCH|DELETE /businesses/{id}/staff/{staffId}` · `PUT /businesses/{id}/staff/{staffId}/services` · `PUT /businesses/{id}/staff/{staffId}/hours` (manager). Staff are per-business rows and do not require a platform account. Qualification is explicit; a person with no services is bookable for none, and a weekday with no hours row inherits the business hours.
- Blackouts: `GET|POST /businesses/{id}/blackouts` · `DELETE /businesses/{id}/blackouts/{blackoutId}` (manager). Overlapping windows are rejected by the database, and a booking whose slot was taken by another request answers **409 `slot_taken`**.
- Invites: `GET|POST /businesses/{id}/invites` (roles: `co_owner` | `viewer`; `viewer` grants read-only access to `GET /businesses/{id}/analytics` only, `co_owner` grants full management rights) · `DELETE /businesses/{id}/invites/{inviteId}` · `GET /invites/{token}` (public preview) · `POST /invites/{token}/accept` (invitee receives an email with the accept link `{PUBLIC_URL}/invite/{token}`)
  - An accepted grant is held by the accepting **user id**. The invited `email` is retained only as a record of what was sent and is never re-resolved to decide access, so a holder who changes their own address keeps their access. Accepted grants are unique per (business, user, role). Importing `PRD.md`'s older description of invite resolution will reintroduce the bug this fixes.
- Quick replies: `GET|POST /businesses/{id}/quick-replies` · `DELETE .../quick-replies/{replyId}`

## Products
- `GET|POST /businesses/{id}/products` · `PATCH /products/{id}` · `PUT /products/{id}/variants` (options+variants atomic)
- `POST /products/{id}/publish|unpublish|duplicate` · `DELETE /products/{id}`

## Engagement
- Likes: `GET|PUT|DELETE /likes/{type}/{id}` · Recommends: `GET|PUT|DELETE /recommends/{id}`
- Collections: `GET|POST /me/collections` · `PATCH|DELETE /me/collections/{id}` · `GET|POST /me/collections/{id}/items` · `POST /me/collections/items` (default) · `DELETE /me/collections/{id}/items/{itemId}` · `GET /saved/{type}/{id}`
- Reviews: `GET|POST /businesses/{id}/reviews?sort=newest|highest|helpful&product_id=` · `PATCH|DELETE /reviews/{id}` · `POST /reviews/{id}/reply` · `PUT /reviews/{id}/helpful`
- Comments: `GET|POST /businesses/{id}/comments` · `PATCH|DELETE /comments/{id}` · `PUT|DELETE /comments/{id}/like`
- Q&A: `GET|POST /businesses/{id}/questions` · `POST /questions/{questionId}/answers`
- Follows: `GET|PUT|DELETE /businesses/{id}/follow` · `GET /me/following-feed`
- Updates: `GET|POST /businesses/{id}/updates`
- Notifications: `GET /notifications` · `POST /notifications/read` · `POST /reports`
- Compare sync: `GET|PUT /me/compare` (server-mirrored tray, PRD §5.1.5)
- Public collection: `GET /collections/{id}` (when `is_public`)
- Reviews by user: `GET /me/reviews`

## Messaging
- Threads: `GET|POST /threads` · `GET /threads/{id}` (see pagination below) · `POST /threads/{id}/messages` · `POST /threads/{id}/read|typing|close|leave` · `GET /threads/{id}/search|media|export`
- Messages: `PATCH /messages/{id}` · `DELETE /messages/{id}?scope=me|everyone` · `PUT|DELETE /messages/{id}/reaction` · `POST /messages/{id}/forward` · `GET /messages/search?q=` (across all of a user's threads)
- Pins: `PUT|DELETE /threads/{id}/pin/{messageId}` · `GET /threads/{id}/pinned` · Pinned conversations: `PUT|DELETE /threads/{id}/pinned-thread` · `GET /me/pinned-threads` (max 5)
- Blocks: `GET /blocks` · `POST|DELETE /blocks/{userId}`
- Push: `POST|DELETE /push/subscribe` · `GET /push/vapid-key`
- WS: `GET /ws` (cookie-authenticated only — no `?token=` query parameter); frames per ARCHITECTURE §3. `subscribe` frames are gated on thread membership.
- Support & client errors: `POST /support/contact` · `POST /errors`

### Thread history pagination

`GET /threads/{id}` is **keyset**-paginated, not offset-paginated:

- `before` — exclusive upper bound, a **message id**. Omit (or `0`) for the
  newest page. For the next page back, pass the *oldest id you have received*.
- `limit` — 50 by default, capped at 200.
- Response: `{ thread, messages, has_more }`. `messages` are ascending by id.
  `has_more` is derived by fetching one extra row, so a pager never needs a
  `COUNT`.

```bash
# newest 50
GET /api/v1/threads/{id}
# then walk backwards; suppose the oldest id received was 41180
GET /api/v1/threads/{id}?before=41180&limit=50
```

Served by `idx_chat_messages_thread (thread_id, id DESC)` — the best-suited
index in the schema. This endpoint previously read no cursor at all and returned
a fixed 50, which made the older half of any longer conversation permanently
unreachable: no API path and no UI control could reach it, and reply/quote,
jump-to-message and pinned cross-references all broke for old messages.

## Admin (`admin` claim)
- Verify: `GET /admin/verify` (`?status=rejected`) · `GET /admin/verify/{id}` · `POST /admin/verify/{id}/decide` · `POST /admin/verify/{id}/re-request` · `GET /admin/verify/{id}/documents/{docId}/file`
- Moderation: `GET /admin/reports` · `POST /admin/reports/{reportId}/decide` · `POST /admin/content/{type}/{id}/hide|restore`
- Users: `GET /admin/users` · `POST /admin/users/{id}/{warn|suspend|ban|unban}`
- Claims: `GET /admin/claims` · `POST /admin/claims/{claimId}/decide`
- Businesses: `POST /admin/businesses/{id}/suspend|restore`
- Categories: `POST /admin/categories` · `PATCH|DELETE /admin/categories/{id}` · `POST /admin/categories/{id}/merge/{intoId}` (siblings only — source and target must be at the same tree level; the source's businesses and child categories are re-pointed at the target, then the source is retired)
- Curation/config: `GET|PUT /admin/curation` · `GET|PUT /admin/settings` · `GET|POST /admin/banned-words` · `DELETE /admin/banned-words/{word}` · `GET|PUT /admin/banned-words/allowlist`
- Platform: `GET /admin/kpis` · `GET /admin/appeals` · `POST /admin/appeals/{id}/decide` · `GET /admin/anomalies` · `POST /admin/anomalies/{eventId}/resolve` · `GET /admin/moderation-actions`

## Ops
- `GET /health` (liveness + depth) · `GET /ready` (DB ping) · `GET /metrics` (Prometheus text; admin-only in prod)

## Media
- `POST /media` (multipart: `kind`, `file`; kinds: logo, cover, gallery, product, avatar, chat_image, chat_file, chat_audio, chat_video, document_verification). Uploads stream to disk with per-kind size caps (images/docs 10 MB, files 20 MB, audio 25 MB, video 200 MB).
- `GET /media/{id}/file` · `GET /media/{id}/thumb` — public for storefront kinds; **chat uploads require authentication and thread membership** and non-media files are served as downloads. Verification documents are never publicly served.

## Public API (read-only, API keys)

Authenticate with `X-API-Key: bv_...` (issue keys at `POST /me/api-keys`; 300 req/min per key).

- `GET /api/v2/search` (same params as `/api/v1/search`)
- `GET /api/v2/categories` / `GET /api/v2/categories/{slug}`
- `GET /api/v2/cities/{slug}`
- `GET /api/v2/b/{slug}`
- `GET /api/v2/businesses/{id}/reviews`
- `GET /api/v2/rates`

## Saved searches & alerts
- `GET|POST /me/saved-searches` (body: `name`, `query`, `notify_daily`)
- `PATCH /me/saved-searches/{id}` (body: `notify_daily`)
- `DELETE /me/saved-searches/{id}`

## Category follows & stock alerts
- `GET|PUT|DELETE /categories/{id}/follow`
- `GET|PUT|DELETE /products/{id}/stock-alert`

## Billing (Phase 7.1 monetization)

Stripe is the system of record for money; this API is the system of record for
*access*. Every entitlement decision reads the `subscriptions` table, which is
only ever written by a signature-verified webhook or the 6-hourly reconciliation
job — never by the client.

Entitlements are capability strings on the plan (`analytics`,
`analytics_advanced`, `api_access`, `support_priority`) plus numeric caps
(`product_limit:N`, `gallery_limit:N`, `team_seats:N`). The strings are a
storage format: the API exposes them resolved into a booleans-and-caps object so
a client never has to parse them. `verified_badge` and `featured_placement`
were removed on purpose — trust and placement are not for sale, and a policy
test fails the build if either returns. `webhooks` and `embeddable_widget` are
reserved for when the features behind them land.

- `GET /plans` — **public**, no auth. The active catalogue plus `enabled: bool`
  (false when Stripe is not configured on the deployment). `purchasable` is
  false for a paid plan whose `stripe_price_id` has not been filled in yet, so
  clients must not offer a buy button for it.
- `GET /businesses/{id}/billing` — effective plan, subscription row, resolved
  `entitlements`, `enabled`. Authorised for the owner **and** co-owners
  (`CanManageBusiness`), so a co-owner can pay for the business they manage.
- `GET /businesses/{id}/billing/invoices?limit=` — display-only history; Stripe
  remains authoritative. `limit` caps at 100.
- `POST /businesses/{id}/billing/checkout` (body: `{plan_id}`) → `{url}`, a
  Stripe Checkout URL. The business is re-authorised server-side; a body-supplied
  business id is never trusted. Refuses when Stripe is unconfigured (404, not
  500), when the plan is not yet purchasable (409), or when the business already
  has an active subscription (409, use the portal).
- `POST /businesses/{id}/billing/portal` → `{url}`, a Stripe customer-portal URL
  for self-serve cancellation and card updates. 409 when the business has no
  billing account yet.
- `POST /billing/webhook` — **no session, no CSRF, no API key.** Authenticated
  solely by the `Stripe-Signature` HMAC over the *raw* body, verified before any
  parsing. Handled event types:
  `customer.subscription.created|updated|deleted`,
  `invoice.created|paid|payment_failed|finalized`,
  `checkout.session.completed` (acknowledged; state arrives via the
  subscription events). Unknown types are acknowledged so a new Stripe event
  cannot put the endpoint into a retry loop.
  - **Idempotency:** `billing_webhook_events` is keyed on `provider_event_id`.
    A duplicate returns 200 without reapplying — Stripe retries until it sees a
    2xx, so a duplicate is a success, not an error. A *retry after a genuine
    failure* re-runs the handler, so every handler is an idempotent upsert.
  - A 4xx (bad signature, unparseable body, test-mode event in a live
    deployment) is **not** retried. Anything else is a 5xx so Stripe retries.
  - Configure the endpoint as
    `https://<api-host>/api/v1/billing/webhook`. It is CSRF-exempt by explicit
    allowlist (`csrfExemptPaths` in `httpapi/middleware.go`); adding a path there
    is a security decision that requires a non-cookie auth mechanism.

**Operator step before a paid plan can be sold** (a paid plan with no
`stripe_price_id` is a valid catalogue entry, just not purchasable):

```sql
UPDATE plans SET stripe_price_id = 'price_...' WHERE id = 'growth';
```

**Subscription status → access.** `active`, `trialing` and `past_due` grant
entitlements; `incomplete`, `unpaid`, `paused` and `canceled` do not. The list is
a strict allowlist (`repo.Subscription.GrantsEntitlements`), so a status Stripe
adds later cannot silently become a paid tier. `past_due` keeps access while
Stripe retries the card; the `idx_subscriptions_period_end` partial index backs
the sweep that catches rows the webhook never reached.

## Server-rendered SEO (Next.js)

SEO HTML is rendered by the Next.js frontend (`apps/web/app`), not by the API.
Public routes (`/`, `/b/{slug}`, `/c/{slug}`, `/city/{slug}`, `/u/{username}`,
`/collections/{id}`, …) export `generateMetadata` (per-row `<title>`, meta
description, canonical, Open Graph + Twitter card) and inline JSON-LD
(`LocalBusiness` + `BreadcrumbList` + `ItemList` on business pages,
`CollectionPage` elsewhere). ISR (`revalidate` 120–300s) keeps the HTML fresh
without a render per crawl; auth routes carry `robots: noindex`.

Behaviour worth relying on:

- Only a `verified` business renders metadata; unknown slugs render the
  `not-found.tsx` 404.
- Every interpolated JSON-LD value has `<` escaped so a business name
  containing `</script>` cannot break out.
- `aggregateRating` is emitted only when there is at least one review — a zero
  review count is a rich-result spam signal.
- The API still serves `GET /robots.txt` and `GET /api/v1/sitemap.xml`; Next
  proxies them (`app/sitemap.xml/route.ts`, `app/robots.ts`) so Go remains the
  single source of truth for the URL inventory.
