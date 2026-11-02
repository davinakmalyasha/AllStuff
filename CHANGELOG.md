# Changelog

## Unreleased

### Security
- Least-privilege database role + row-level security (migrations `0041` and
  `0053`, helpers hardened in `0054`). The request path now runs as
  `allstuff_app` (owns nothing; DML only) with per-request identity, and
  tenant/owner tables re-check the reader on every row — a missed `WHERE`
  in a repository method returns empty instead of another user's data. Jobs,
  migrations, the notifier and tests stay on the owner pool and bypass RLS
  by design; `businesses` / `products` / `trend_snapshots` are deliberately
  unpolicied (search, leaderboard and SSR run without a user context).
  Proved by `TestRLS*` probe tests plus full E2E runs against the RLS pool.
- Web-push fan-out and link previews now carry the requesting user
  (`context.WithoutCancel`): under RLS both ran with no identity and
  silently delivered nothing.
- Concurrent invite acceptance can no longer double-report success:
  `AcceptInvite` claims via rows-affected and the loser gets
  `ErrTokenInvalid`.
- `StaffRepo.Update` allowlists columns (like its sibling repos); location
  updates validate merged coordinates and hour shapes.
- Media items expose `virus_scan_status`: an unscanned upload (dev, no
  ClamAV) is now distinguishable from a clean one instead of reporting
  success identically.
- Moderation restore of a deleted reaction no longer reports false success;
  restoring hidden non-reaction attachments re-checks the hideable-kind
  gate.
- Fixed an SSR crash: `/discover?q=` evaluated `document.activeElement`
  during server render (`ReferenceError: document is not defined` in server
  logs; pages still rendered). Render-path browser globals are now guarded
  in Discover/Category/City pages.
- Finished the BizVerse → AllStuff rename in user-visible surfaces (header
  wordmark, email header, `allstuff_*` Prometheus metrics).

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

### Internals
- Test template builds are serialized across `go test` processes
  (`testutil` takes `allstuff:tmpl-build` on the shared `postgres`
  database). The migrate advisory lock is per-database, so it never
  excluded concurrent builds of different template DBs — harmless until
  `0041` wrote cluster-shared catalogs, at which point parallel
  `service`+`repo`+`httpapi` suites aborted with `tuple concurrently
  updated`. The runner documents the per-database scope.
- `SECURITY.md` no longer lists the owner-connection as a limitation; the
  `0041` reservation row is marked applied and `0042` superseded by `0053`.

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
