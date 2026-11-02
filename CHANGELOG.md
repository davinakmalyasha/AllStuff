# Changelog

## Unreleased

### Fixed
- Services created or edited through the dashboard now persist
  `duration_minutes` (`Products.Create`/`Update` dropped the field, so every
  UI-created service was unbookable; `Duplicate` copied every column except the
  duration). `Publish` now requires a duration for services instead of failing
  every later booking with `slot_taken`/`not found`.
  Pinned by `TestServiceDurationPersistsEndToEnd`,
  `TestPublishServiceRequiresDuration`.
- `AddBlackout` refuses a window containing live (`pending`/`confirmed`)
  appointments instead of silently stranding booked customers.
  Pinned by `TestAddBlackoutRefusesLiveAppointments`.
- `GET /api/v1/health` pings the database and 500s when it is unreachable;
  previously it always answered `{"status":"ok"}`, so load balancers kept
  routing to a dead API.
- `checkout.session.completed` webhooks now resolve the session's subscription
  from Stripe and apply it, instead of assuming the `subscription.created`
  delivery always arrives first. Pinned by
  `TestDecodeCheckoutSessionSubscriptionID`.
- Moderation restore of a hidden product returns it to draft instead of
  publishing a listing the owner never released.
- Leaderboards, Following feed, My bookings and the chat thread page render an
  error with a retry instead of "empty" when the fetch fails; a successful
  booking invalidates `['my-bookings']`; the cancel dialog only closes on
  success.

### Docs
- `REGISTRY.md`: the reserved-numbers table claimed `0034`/`0035` were future
  places/address work; they are the applied subscriptions dedupe/UNIQUE fix.
  The Applied table now lists `0034`–`0052`.
- `README.md`: seed path corrected to repo-root relative, compose line includes
  `redis` (rate limits/TOTP guard/WS fan-out degrade per-instance without it),
  local-vs-prod proxy description split.
- `DEPLOYMENT.md`: health/ready descriptions swapped to match the handlers;
  stale `/ssr/:path*` rewrite removed.
- `API.md`: `PUT|DELETE /threads/{id}/mute` documented; search `limit > 50`
  reset clarified. `CONTRIBUTING.md`: removed the `npm run test:unit` line
  (no such script).
- Config defaults `PUBLIC_URL`/`CORS_ORIGINS` (and the digest fallback) point
  at `:3000`, not the dead Vite `:5173` origin; `shots.mjs` ports updated.
