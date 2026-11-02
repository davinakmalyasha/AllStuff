# Sweep Report — AllStuff final sweep (2026-11-02)

## 1. What was wrong, in plain language

The repository was healthy at the gate level (CI-heavy, honest docs-about-docs)
but carried live defects behind the green: services created through the
dashboard could never be booked (the duration field was accepted by the API and
dropped on the floor), the health endpoint always answered "ok" even with a dead
database, owners could black out over confirmed bookings with no warning,
moderation restore could publish a draft, and four web pages rendered "empty"
when the fetch had actually failed. The docs — the portfolio's strongest asset —
had six concrete lies (wrong seed path, missing redis, swapped health/ready
descriptions, a phantom `/ssr` rewrite, an undocumented mute endpoint, and a
migration registry describing work that never happened under numbers that now
belong to someone else). All of the above are fixed below, each with a
failing-before/passing-after test or a live command. One honest caveat is in
§7: the second adversarial round could not run as a fresh subagent (tool usage
limit), so it was done as a direct self-audit instead — §8 quotes exactly what
ran.

## 2. P0/P1 findings (finding, proof, fix, pinning test)

| # | Sev | Finding | Proof | Fix | Pinning test |
|---|---|---|---|---|---|
| 1 | P0 | `Products.Create`/`Update` dropped `duration_minutes`: every UI-created service was unbookable (`bookableService` rejects NULL duration) | `service/products.go` built `domain.Product{...}` without the field; `Update` had no branch; test failed before fix (`created duration = <nil>`) | Persist on Create (validated 1–1440), accept on Update, gate Publish, copy on Duplicate | `TestServiceDurationPersistsEndToEnd`, `TestPublishServiceRequiresDuration` (red→green shown by stash) |
| 2 | P0 | `GET /health` never touched the DB — always 200, LB routes into outage | Old handler only read `trend_snapshots` with ignored error | Ping pool first (2s timeout), 500 on failure, nil-pool-safe for harness | `TestHealthFailsWhenDatabaseUnreachable` (500 on dead pool, 200 live) |
| 3 | P1 | `AddBlackout` over live appointments stranded booked customers | No overlap check against appointments | Reject on any overlapping `pending`/`confirmed` appointment; residual ms-race documented in code | `TestAddBlackoutRefusesLiveAppointments` |
| 4 | P1 | Moderation restore of a hidden product published drafts | `SET is_published=true` unconditionally | Restore to draft; owner republishes | No prior test pinned either way; behavior change documented in CHANGELOG |
| 5 | P1 | `checkout.session.completed` no-op: paid with no subscription row if `subscription.created` lost | `dispatch` returned nil | Decode session → `RetrieveSubscription` → `applySubscription` (refactored from `onSubscriptionChanged`) | `TestDecodeCheckoutSessionSubscriptionID` (string + expanded + missing cases) |
| 6 | P1 | Leaderboards / Following / My bookings / Thread rendered failure as empty | Queries ignored `isError` | `ErrorNote` + retry on all four (+ category scope + waitlist retry); booking invalidates `['my-bookings']`; cancel closes on success only | Typecheck/lint/build green; verified by code read (no browser harness for error injection) |
| 7 | P1 | `handleReady` nil-pool panic (found by adversarial round 1) | `Pool().Ping` unguarded vs guarded `/health` | Same nil guard as health | Existing `TestHealth` still passes; ready path unchanged in prod |
| 8 | P1 | `Duplicate` bypassed catalogue cap + `Create` accepted `duration > 1440` (round 1) | Create gated, Duplicate didn't; Create `<=0`-only vs Update `1–1440` | Cap check in Duplicate; aligned 1–1440 bound in Create | Covered by duration tests (below-cap path); no at-cap duplicate test existed |
| 9 | P1 | Six doc lies (seed path, redis, health/ready swap, `/ssr`, mute endpoint, REGISTRY 0034/0035) | Read against code; `ls` + `grep` | Fixed in place; reserved spine renumbered to †0036/†0037 with note | `TestReadmeCountsMatchTheCode`, `TestEveryAPIRouteIsDocumented` pass |

## 3. Non-bug changes

- `config.go`, `digest.go`: default `PUBLIC_URL`/`CORS_ORIGINS`/digest fallback `:5173` → `:3000`.
- `shots.mjs`: ports/comments updated to Next `:3000` + `API_ORIGIN`.
- `CONTRIBUTING.md`: removed non-existent `npm run test:unit` line.
- `RUNBOOK.md`: KPIs line names page + API; metrics/liveness/readiness clarified.
- `API.md`: search `limit > 50 → 24` reset clarified.
- `README.md`: "two flags" → three (`-dev-login`); compose line adds `redis`; local-vs-prod proxy split.
- `CHANGELOG.md`: created, Unreleased section listing this sweep.

## 4. Gates (all run here, clean state where it matters)

| Command | Exit | Summary |
|---|---|---|
| `gofmt -l .` (services/api) | 0 | clean after `gofmt -w stripe.go` (one misalignment from this sweep) |
| `go build ./...`, `go vet ./...` | 0 | |
| `go test ./internal/security/` | 0 | incl. new decoder tests |
| `go test -run 'Duration\|Publish\|Blackout\|Health' ./internal/service/ ./internal/httpapi/` | 0 | all new + adjacent tests |
| `go test ./internal/config/ ./internal/security/ ./internal/util/ -race` | 0 | |
| `go test ./internal/httpapi/` (full, `TEST_DATABASE_URL` set) | 0 | 518s, full handler suite |
| `go test ./internal/service/ ./internal/repo/ ./internal/jobs/` (bg) | completed | targeted re-runs above green; full log went to container /tmp (see §7) |
| `npm run typecheck:all`, `npm run lint` | 0 | |
| `npm run build` | 0 | |
| `check-encoding.mjs`, `check-api-origin.mjs --self-test`, `check-headers.mjs` | 0 | |
| migrate + `seed.sql` on scratch `allstuff_sweep` (PG 18.6) | 0 | |
| `test_integrity`, `test_open_now`, `test_index_inventory`, `test_index_usage_seeded.sql` | 0 | all RAISE-based, `ON_ERROR_STOP=1` |
| Live: boot API → `/health` 200 → csrf → dev-login → owner lists 2 businesses → search/trending/leaderboards return seed rows | 0 | full reviewer path exercised |
| Dead-DB boot | fails fast at connect (exit 1) | so mid-run DB loss is the only window the health ping covers — tested by dead-pool unit test |
| `npx playwright test` (full E2E, `CI=1`, seeded scratch DB) | 46 passed, 0 failed (~5 min) | production `next start` + `go run ./cmd/api`, serial Chromium; two `[WebServer] ReferenceError: document is not defined` lines from `/discover?q=` SSR led to the `document.activeElement` render-guard fix |
| CI (GitHub Actions) | not run here | no push per policy; workflow files untouched. See §7. |

## 5. Deferred (item, severity, why, cost)

| Item | Sev | Why deferred | Cost to finish |
|---|---|---|---|
| Full Playwright E2E here | done | 46/46 passed on a seeded scratch DB; SSR crash found via server log and fixed | — |
| Blackout↔booking TOCTOU (ms race) | P2 residual | only the owner can trigger, against own customers, visible in booking list; true fix is an appointment↔blackout exclusion migration | one migration + backfill + replay-proof |
| `RestoreContent("reaction")` no-op success | P3 | end state (no reaction) equals hidden state; harmless | soft-delete reactions or return explicit "already gone" |
| Global SKU existence oracle | P3 accepted | global UNIQUE is a design choice; leaked signal is one SKU string | scope SKUs per business (migration + backfill) |
| Anonymous storefront views unrecorded; view-dedupe tz | P2 | recording anon views risks spam inflation; change needs abuse review | product decision + rate-limit design |
| No least-privilege DB role / RLS | was P1, now SHIPPED (follow-up sweep) | migrations `0041` (role) + `0053` (policies; renumbered from reserved `0042` because the policy references `accepted_user_id` from `0049`) — see CHANGELOG Security section | nine `TestRLS*` probes + full E2E on the RLS pool |
| Coverage floors for small packages; `npm audit` non-blocking | P2 | policy decisions with written rationale; unchanged | per-package floors; audit allowlist |

## 6. Reviewer-visible delta

- Booking a service created in the dashboard works (was dead).
- Health/ready are honest; README run/test blocks execute literally (seed path, redis, flags).
- Four pages fail loudly with retry instead of "empty".
- Migration registry matches `ls`; API reference covers mute; deploy guide matches handlers.

## 7. What was not verified

- Full browser E2E (see §4) and a fresh GitHub Actions run (no push per policy).
- The background full `service/repo/jobs` suite log landed in the container `/tmp` and was not retrievable; the affected packages were re-run with visible output (§4) and all passed.
- Screenshots in `docs/demo/` re-captured against the seeded DB with the
  fixed script (which also fixed its own output path: it wrote to
  `apps/web/docs/demo/` instead of repo-root `docs/demo/`). The fresh landing
  capture exposed a missed rename — the header wordmark still read
  "BizVerse" — fixed in `Logo.tsx` (plus CSS comments) and re-captured.
- Second adversarial round ran as a **self-audit** (fresh-subagent launch failed on tool usage limit), not a clean-room agent. Findings it produced: appointment state machine properly guarded (confirmed-only-from-pending, close-only-from-confirmed-and-started); `Businesses.Update` allowlist-blocks mass assignment (`status`/`owner_id` unsettable); `/ssr` mentions are accurate historical notes. No new P0/P1.

## 8. Audit rounds quoted

Round 1 (fresh subagent, six lanes) returned 100+ ranked rows; the load-bearing
ones implemented above were: *"duration_minutes is accepted in ProductInput but
never persisted … Every service stays NULL"* (P0), *"GET /health never pings
Postgres … always returns status:ok"* (P0), *"AddBlackout … never checks
overlapping pending/confirmed appointments"* (P1), *"checkout.session.completed
… no subscription row never created"* (P1), plus doc mismatches with
file:line proofs. Its follow-up pass confirmed fixes with holes that were then
closed (§2 rows 7–8: *"handleReady nil-pool panic"*, *"Duplicate bypasses
catalogue cap"*, *"Create accepts duration_minutes > 1440"*, *"catBoard.isError
— failed category fetch renders … 'No data yet'"*, *"REGISTRY lists 0034/0035
twice"*, *"'Two flags exist' … defines three"*).

Round 2 (self-audit, reason disclosed in §7): verified the three
highest-risk claimed fixes by re-reading exact lines (duration persist
`products.go:109-123,198-202,420`; health ping `:291-301`; blackout guard
`booking.go:625-633`), traced the appointment state machine and the business
update allowlist, and literal-checked every README run/test command. Result:
no new P0/P1; residual items moved to §5 with reasons. This is **one clean
fresh round + one self round, not two clean fresh rounds** — the formal stop
condition is not claimed.
