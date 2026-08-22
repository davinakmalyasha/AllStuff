# BizVerse — API Reference

Base URL: `/api/v1` · Auth: httpOnly cookies (`bv_access` 15min, `bv_refresh` rotating) · CSRF: `X-CSRF-Token` header on mutations · Errors: `{ "error": { "code", "message", "fields? } }` · Pagination: `limit`/`offset` (≤50).

## Auth & accounts
- `POST /auth/register` · `POST /auth/login` (2FA-gated: returns `challenge`) · `POST /auth/2fa/verify`
- `POST /auth/refresh` · `POST /auth/logout` · `POST /auth/verify-email` · `POST /auth/forgot-password` · `POST /auth/reset-password`
- `GET /auth/oauth/google` · `GET /auth/oauth/google/callback` (redirect flow)
- `GET /me` · `PATCH /me` · `GET /me/export` · `POST /me/delete` · `POST /me/delete/cancel`
- Security: `GET /me/security`, `POST /me/security/2fa`, `/2fa/confirm`, `DELETE /me/security/2fa`, `GET /me/security/sessions`, `DELETE /me/security/sessions/{id}`, `POST /me/security/sessions/revoke-others`
- Prefs: `GET|PATCH /me/notifications-settings` · `POST /me/appeal`

## Directory
- `GET /categories` (tree+counts) · `GET /categories/{slug}` (page+leaderboard) · `GET /b/{slug}` (`?draft=1` = owner preview) · `GET /u/{username}`
- `GET /search` (`q, category*, lat, lng, radius_km, bbox, price_level*, min_rating, open_now, verified_only, fully_verified_only, sort, limit`) · `GET /search/suggest`
- `GET /compare?b=id,id` (2–4) · `GET /featured` · `GET /trending` · `GET /rising` · `GET /leaderboards?window=24h|7d|30d&scope=global|category:{id}|city:{c}`
- `GET /rates` (currency) · `GET /meta` (announcement, rates freshness) · `GET /sitemap.xml` · `GET /og/b/{slug}` (SVG share card)

## Businesses (owner)
- `POST /businesses` (draft) · `GET /businesses` · `GET|PATCH /businesses/{id}` · `POST /businesses/{id}/submit|resubmit|publish|unpublish|pause|reopen|close`
- `PUT /businesses/{id}/storefront` · `GET /businesses/{id}/analytics?period=7d|30d|all`
- Documents: `GET|POST /businesses/{id}/documents` · `DELETE /businesses/{id}/documents/{docId}`
- Invites: `GET|POST /businesses/{id}/invites` · `DELETE /businesses/{id}/invites/{inviteId}` · `POST /invites/{token}/accept`
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

## Messaging
- Threads: `GET|POST /threads` · `GET /threads/{id}` · `POST /threads/{id}/messages` · `POST /threads/{id}/read|typing|close|leave` · `GET /threads/{id}/search|media|export`
- Messages: `PATCH /messages/{id}` · `DELETE /messages/{id}?scope=me|everyone` · `PUT|DELETE /messages/{id}/reaction` · `POST /messages/{id}/forward`
- Pins: `PUT|DELETE /threads/{id}/pin/{messageId}` · `GET /threads/{id}/pinned`
- Blocks: `GET /blocks` · `POST|DELETE /blocks/{userId}`
- Push: `POST|DELETE /push/subscribe` · `GET /push/vapid-key`
- WS: `GET /ws` (cookie or `?token=`); frames per ARCHITECTURE §3.

## Admin (`admin` claim)
- Verify: `GET /admin/verify` · `GET /admin/verify/{id}` · `POST /admin/verify/{id}/decide` · `GET /admin/verify/{id}/documents/{docId}/file`
- Moderation: `GET /admin/reports` · `POST /admin/reports/{reportId}/decide` · `POST /admin/content/{type}/{id}/hide|restore`
- Users: `GET /admin/users` · `POST /admin/users/{id}/{warn|suspend|ban|unban}`
- Curation/config: `GET|PUT /admin/curation` · `GET|PUT /admin/settings` · `GET|POST /admin/banned-words` · `DELETE /admin/banned-words/{word}`
- Platform: `GET /admin/kpis` · `GET /admin/appeals` · `POST /admin/appeals/{id}/decide` · `GET /admin/anomalies` · `POST /admin/anomalies/{eventId}/resolve` · `GET /admin/moderation-actions`

## Media
- `POST /media` (multipart: `kind`, `file`; kinds: logo, cover, gallery, product, avatar, chat_image, chat_file, chat_audio, chat_video, document_verification)
- `GET /media/{id}/file` · `GET /media/{id}/thumb`

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
