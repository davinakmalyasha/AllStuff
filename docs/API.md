# BizVerse — API Reference

Base URL: `/api/v1` · Auth: httpOnly cookies (`bv_access` 15min, `bv_refresh` rotating) · CSRF: `X-CSRF-Token` header on mutations · Errors: `{ "error": { "code", "message", "fields? } }` · Pagination: `limit`/`offset` — general cap 50 on search/list endpoints; leaderboards accept up to 100 (documented exception).

## Auth & accounts
- `POST /auth/register` · `POST /auth/login` (2FA-gated: returns `challenge`) · `POST /auth/2fa/verify`
- `POST /auth/refresh` · `POST /auth/logout` · `POST /auth/verify-email` · `POST /auth/resend-verification` (always `{"sent":true}`; 5/h per account) · `POST /auth/forgot-password` · `POST /auth/reset-password`
- `POST /auth/restore` — cancel a pending account deletion during the 14-day grace (email + password)
- Invites: `GET /invites/{token}` (public preview: `{business_name, inviter_name, role, email}`) — accept via `POST /invites/{token}/accept` below
- `GET /auth/oauth/google` · `GET /auth/oauth/google/callback` (redirect flow)
- `GET /me` · `PATCH /me` · `GET /me/export` · `POST /me/delete` · `POST /me/delete/cancel` (body: `password`)
- Security: `GET /me/security`, `POST /me/security/2fa`, `/2fa/confirm`, `DELETE /me/security/2fa`, `POST /me/security/2fa/recovery-codes`, `GET /me/security/sessions`, `DELETE /me/security/sessions/{id}`, `POST /me/security/sessions/revoke-others`
- Password/email: `POST /me/password` (`current_password`, `new_password`) · `POST /me/email` (`password`, `email`, `totp_code?` when 2FA enrolled; notifies the old address)
- API keys: `GET|POST /me/api-keys` · `DELETE /me/api-keys/{keyId}`
- Claims: `POST /claims` · `GET /me/claims`
- Prefs: `GET|PATCH /me/notifications-settings` · `POST /me/appeal`

## Directory
- `GET /categories` (tree+counts) · `GET /categories/{slug}` (page+leaderboard) · `GET /b/{slug}` (`?draft=1` = owner preview) · `GET /u/{username}`
- `GET /search` (`q, category*, lat, lng, radius_km, bbox, price_level*, min_rating, open_now, verified_only, fully_verified_only, sort, limit`) · `GET /search/suggest` · `GET /users/search?q=`
- `GET /compare?b=id,id` (2–4) · `GET /featured` · `GET /trending` · `GET /rising` · `GET /leaderboards?window=24h|7d|30d&scope=global|category:{id}|city:{c}`
- Cities: `GET /cities` · `GET /cities/{slug}` (landing page payload)
- `GET /rates` (currency) · `GET /meta` (announcement, rates freshness) · `GET /sitemap.xml` · `GET /og/b/{slug}` (SVG share card)
- `GET /robots.txt` (public, no auth, `text/plain`): `User-agent: *` allow-all plus an absolute `Sitemap:` URL built from `PUBLIC_URL` (e.g. `Sitemap: https://<your-domain>/api/v1/sitemap.xml`)

## Businesses (owner)
- `POST /businesses` (draft) · `GET /businesses` · `GET|PATCH /businesses/{id}` · `POST /businesses/{id}/submit|resubmit|publish|unpublish|pause|reopen|close`
- `POST /businesses/{id}/slug-change` (one-time owner-requested slug change, PRD §8.2)
- `PUT /businesses/{id}/storefront` · `GET /businesses/{id}/analytics?period=7d|30d|all`
- Documents: `GET|POST /businesses/{id}/documents` · `DELETE /businesses/{id}/documents/{docId}`
- Invites: `GET|POST /businesses/{id}/invites` (roles: `co_owner` | `viewer`; `viewer` grants read-only access to `GET /businesses/{id}/analytics` only, `co_owner` grants full management rights) · `DELETE /businesses/{id}/invites/{inviteId}` · `GET /invites/{token}` (public preview) · `POST /invites/{token}/accept` (invitee receives an email with the accept link `{PUBLIC_URL}/invite/{token}`)
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
- Threads: `GET|POST /threads` · `GET /threads/{id}` · `POST /threads/{id}/messages` · `POST /threads/{id}/read|typing|close|leave` · `GET /threads/{id}/search|media|export`
- Messages: `PATCH /messages/{id}` · `DELETE /messages/{id}?scope=me|everyone` · `PUT|DELETE /messages/{id}/reaction` · `POST /messages/{id}/forward` · `GET /messages/search?q=` (across all of a user's threads)
- Pins: `PUT|DELETE /threads/{id}/pin/{messageId}` · `GET /threads/{id}/pinned` · Pinned conversations: `PUT|DELETE /threads/{id}/pinned-thread` · `GET /me/pinned-threads` (max 5)
- Blocks: `GET /blocks` · `POST|DELETE /blocks/{userId}`
- Push: `POST|DELETE /push/subscribe` · `GET /push/vapid-key`
- WS: `GET /ws` (cookie-authenticated only — no `?token=` query parameter); frames per ARCHITECTURE §3. `subscribe` frames are gated on thread membership.
- Support & client errors: `POST /support/contact` · `POST /errors`

## Admin (`admin` claim)
- Verify: `GET /admin/verify` (`?status=rejected`) · `GET /admin/verify/{id}` · `POST /admin/verify/{id}/decide` · `POST /admin/verify/{id}/re-request` · `GET /admin/verify/{id}/documents/{docId}/file`
- Moderation: `GET /admin/reports` · `POST /admin/reports/{reportId}/decide` · `POST /admin/content/{type}/{id}/hide|restore`
- Users: `GET /admin/users` · `POST /admin/users/{id}/{warn|suspend|ban|unban}`
- Claims: `GET /admin/claims` · `POST /admin/claims/{claimId}/decide`
- Businesses: `POST /admin/businesses/{id}/suspend|restore`
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
