# Migration registry

`0027` is the last migration from the pre-audit baseline. Everything after it is
reserved here so the two workstreams — remediation and feature build — cannot
collide on a filename or an intent.

The runner (`services/api/internal/db/migrate.go`) applies files in lexical
order and records `schema_migrations.version`. `CREATE/DROP INDEX CONCURRENTLY`
and anything tagged `/* autocommit */` are split out and executed on autocommit
after the transaction commits; the version marker is written only after they
succeed, so a failure in that window replays the file.

**Renumbering is free for anything not yet applied.** The marker is keyed on
filename, so a number that has never been applied carries no migration history.
This is why the reserved list below was renumbered rather than worked around.

`0032`, `0033`, `0047`, `0048` and `0049` are the exceptions and the reason for this note: all
five were reserved, then actually written — `0032` for the slug-release fix,
`0033` for the pagination total-order fix, `0047` for the `user_2fa.enabled_at`
default, `0048` for per-participant message hiding, `0049` for invite grants bound to user ids — and
everything above them shifted by one each time.
Remediation migrations will keep eating into this list, so treat a reservation as
a plan rather than a claim. Anything at `0050`+ is still free; `0032`–`0049` are
now spoken for.

---

## The replay contract

These are the rules a migration from `0028` forward must satisfy. They are
enforced, not advisory:

| Rule | Enforced by |
|---|---|
| Every file carries a `-- migrate:` header; `idempotent no` requires a `note` | `TestMigrationHeadersArePresent`, `TestNonIdempotentMigrationsExplainThemselves` |
| `CREATE TABLE` / `CREATE INDEX` / `ADD COLUMN` need `IF NOT EXISTS` | `TestMigrationsDeclareTheirIdempotencyContract` |
| `ADD CONSTRAINT` needs a `DROP CONSTRAINT IF EXISTS` of the same name in the same file | same |
| `DROP` needs `IF EXISTS` | same |
| `INSERT` needs `ON CONFLICT` | same |
| `CREATE INDEX CONCURRENTLY` needs a preceding `DROP INDEX CONCURRENTLY IF EXISTS` of the same name, and must NOT also use `IF NOT EXISTS` | same, plus `TestEveryConcurrentIndexBuildIsPrecededByItsDrop` |
| Re-running any migration after its marker is deleted must succeed and leave the schema byte-identical | `TestMigrateReplayIsSafe` |
| No invalid index may exist at boot | `InvalidIndexes` in `migrate.go` |
| All FK columns get an index in the same file that creates them | review |

Files at or before the **frozen baseline** (`0027`) are exempt from the shape
rules: they applied once, in order, to an empty database, and rewriting frozen
history is not worth the churn. They are **exempt, not ignored** — every one of
their known defects is listed below with the file and line, and
`TestMigrateReplayIsSafe` still runs against them to find anything not yet
recorded.

### 6. A seed declares its ownership

Every file with `migrate:seed != none` states in `migrate:note` which columns the
seed owns.

### 7. A seed never reverts operator-owned columns

- `ON CONFLICT DO NOTHING` when the row is **operator-editable at runtime** —
  `categories`, via `/admin/categories`.
- `ON CONFLICT (key) DO UPDATE SET <owned columns>` only when every non-listed
  column is **migration-owned**, and the file enumerates the excluded ones.

`0028`'s `plans` seed is the correct `DO UPDATE` example: `price_cents`,
`currency` and `stripe_price_id` are operator-owned, and a replay that reset
`stripe_price_id` would clear a live Stripe price out from under active
subscriptions.

### 8. `ON CONFLICT` with no target for reference data

`categories` carries three unique constraints (id, slug, and `(parent_id, name)`).
Targeting one leaves the other two able to raise; targeting `(id)` specifically
is *wrong*, because if an admin renamed a slug the replay would no longer conflict
on id and would insert a second row carrying the original slug. Bare
`ON CONFLICT DO NOTHING` is the only correct form for a multi-constraint
reference row.

### 9. Canonical seed template

```sql
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        <table>
-- migrate:risk        DML
-- migrate:note        <what the seed owns, and why DO NOTHING vs DO UPDATE>
--
-- 1. ONE statement, not N. A multi-statement seed has an order dependency
--    (parents before children for a self-FK tree) and a partial-replay window
--    between statements that ON CONFLICT cannot close.
-- 2. Bare ON CONFLICT DO NOTHING for operator-editable reference data.
-- 3. Replay-safe at every point between COMMIT and the marker write.

INSERT INTO <table> (<cols>) VALUES (...), (...)
ON CONFLICT DO NOTHING;
```

### 10. Comments and the statement splitter

`SplitStatements` is deliberately simple and migrations must live within it:

- A statement ends only when the accumulated line, trimmed, ends with `;`.
- **Whole-line `--` comments are dropped** before a statement is assembled. Put
  every comment on its own line.
- A **trailing** `--` comment on a line that also ends in `;` suppresses the
  boundary and merges two statements. This is the most likely way to break a
  migration by accident.
- `$$` and `$tag$` bodies are tracked, so a `;` inside plpgsql is safe.
- Single-quoted strings are **not** tracked. Never end a line with a string
  literal whose last character is `;`.
- `DO $tag$ ... $tag$` bodies are opaque to the static linter. It reports the
  count rather than pretending to have checked.

### 11. Ordering across a constraint swap

A partial unique index and a full unique constraint can legally coexist, so build
the replacement **before** dropping the original. `0029` does this for
`businesses_slug_live` / `businesses_slug_key`; the reverse order opens a window
in which `businesses.slug` has no uniqueness at all, and one concurrent insert of
a duplicate in that window wedges the file permanently.

To run a non-`INDEX CONCURRENTLY` statement on autocommit, tag it:

```sql
/* autocommit */ ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_slug_key;
```

A block comment rather than a `--` comment, because the splitter drops whole-line
`--` comments before a statement is ever assembled, so a `--` marker would be
invisible by the time the router looks for it.

---

## Applied

| Version | File | Workstream | Contents |
|---|---|---|---|
| `0028` | `0028_billing.sql` | Phase 7 — monetization | `plans`, `subscriptions`, `invoices`, `billing_webhook_events`. Seeded catalogue with `free` / `growth` / `pro`. Entitlements are **capacity only** — trust and placement are not sellable. |
| `0029` | `0029_integrity.sql` | Phase 0 / 2 — data layer | `description` DEFAULT (fixes the claim-approval 500), `owner_id` DROP NOT NULL (unblocks claim-an-existing-listing), `businesses_slug_live` partial unique index (INERT as first written - nothing set `deleted_at`; made real in `0032`), `media_path_key` UNIQUE, one-default-collection, `trend_snapshots_uniq`, one-open-claim-per-business, and `NOT VALID` CHECKs on every enum-like `text` column. Adds a pre-flight dedupe section and is fully replay-safe. |
| `0030` | `0030_open_now.sql` | Phase 0 / 2 — data layer | `biz_is_open_now(hours, special_hours, tz)` plus the `biz_resolve_day_entry` helper, and a 2-arg back-compat wrapper for rolling deploys. Fixes "Open now" on a day the owner marked closed, in BOTH the SQL `open_now` filter and the Go badge. |
| `0031` | `0031_index_hot.sql` | Phase 2 — data layer | The five critical indexes: business FTS (via `biz_search_tsv`), `businesses(owner_id)`, `collection_items(target_type,target_id)`, `moderation_actions(created_at)`, `engagement_events(flagged)`. Plus the secondary batch and the duplicate/redundant index drops. All 26 concurrent builds are drop-paired. |
| `0032` | `0032_retired_businesses_release_their_slug.sql` | Phase 2 — data layer | Backfills `deleted_at` for every `status = 'closed'` row, then rebuilds `businesses_slug_live` so its predicate is finally selective. Makes `0029`'s slug fix real: closing a listing now releases its slug, so a shop that shuts down no longer reserves its URL forever. Accompanied by `service.Businesses.Close` writing `deleted_at`, `GetByID`/`ListByOwner`/`CanManageBusiness` no longer filtering it (a retired listing stays visible to its owner), and `MaxLiveBusinessesPerOwner = 3` to bound slug squatting. Replay-safe; not in the non-replayable baseline. |
| `0033` | `0033_total_order_on_paginated_lists.sql` | Phase 2 — data layer | Extends `idx_questions_business`, `idx_comments_recent`, `idx_notifications_type` and `idx_notifications_user` with the `id` tiebreaker the seven `ORDER BY … OFFSET` queries now request. Completes what `0031` started: it built the tiebreaker into `idx_reviews_business_recent` and stated the rule, but the queries never asked for it, so the index was correct and the reads were not. Also fixes `GetLatestUnread` (`ORDER BY created_at DESC LIMIT 1`), which proved to be dead code carrying a stale doc comment that claimed it served bulk fan-out inserts — it was superseded years-of-commits ago by `INSERT … RETURNING` in `CreateNotificationsForFollowers`. Replay-safe; not in the non-replayable baseline. |

---

## Reserved

`0034`–`0042` is the "make universal true" spine followed by the security
enablers. Renumbered from the previous reservation so that each file lands in an
order where its dependencies already exist. `0032` and `0033` were taken by the
slug-release and pagination fixes above, both of which had to ship before any
feature that adds or paginations a listing.

| Version | File | Depends on | Contents |
|---|---|---|---|
| `0034` | `0034_places.sql` | — | `places` adjacency list with `admin_level` 0–4 (country / region / district / locality), ISO `code`, denormalised `country_code`, `name_en`, unique `slug`, centroid. Replaces free-text `businesses.city` matching, which currently happens in **five** independent places (`service/cities.go:42,61,77,84,111,132`, `service/search.go:97`, `service/trending.go:134,298`) and breaks on `Kota Jakarta` vs `Jakarta`. Backfills from existing rows — no bundled gazetteer. Includes `biz_slugify()`, the SQL twin of `service/categories.go:289`, with `COLLATE "C"` so non-Latin names slug identically in Go and SQL. |
| `0035` | `0035_address_phone.sql` | `0034` | `address_line1/2`, `region`, `postal_code`, `country_code`, and a real `phone_e164` column with a CHECK — required by the OpenAI "Get Quote" local-services spec, which will not accept a listing without a structured address and an E.164 number. `businesses.{address,city,lat,lng}` remain as a trigger-synced **cached copy** for rolling-deploy safety. Phone is backfilled out of the `contact` jsonb. |
| `0036` | `0036_service_area.sql` | `0035` | `lat`/`lng` become **nullable**; `service_area_kind` (address \| radius \| hybrid), `service_radius_km`, `service_center_lat/lng`. Today `lat`/`lng NOT NULL` makes a mobile or service-area business — the entire home-services category, and the base of Thumbtack and TaskRabbit — *unrepresentable*, which is a data-model impossibility rather than a missing feature. Requires `domain.Business.Lat/Lng` to become `*float64` in the same deploy. |
| `0037` | `0037_category_i18n.sql` | — | `category_names(category_id, locale, name, description)` with a fallback chain. Categories are currently 28 hardcoded English rows in SQL with no translation column and no admin path to translate them. `businesses` content is deliberately NOT translated: owner-authored content stays in the owner's language (PRD §9.5). |
| `0038` | `0038_price_model.sql` | `0034` | `price_min` / `price_max` in the business's own currency, plus an optional locale-aware `price_tier`. Replaces `price_level smallint 1..4`, which is a US/Europe construct — the project's own seed data is Jakarta coffee shops. **51 consumer sites** across `api.ts`, `DiscoverPage`, `ComparePage`, `SettingsPage`, `service/search.go` and `handlers_ssr.go`, so this is additive with a deprecation path, not a replacement. |
| `0039` | `0039_counters.sql` | `0034`–`0038` | Denormalized `businesses.{rating_avg,review_count,like_count,save_count,recommend_count}` and `reviews.helpful_count`, trigger-maintained + backfilled. Unlocks index-servable `sort=rating` / `sort=helpful` / `min_rating`, which are per-row correlated `avg()` subqueries today. Placed after the spine so the triggers are written against the final column set. |
| `0040` | `0040_agent_feed.sql` | `0034`–`0035` | `business_feed_cursor(business_id, changed_at, checksum)` for `changes_token`, `agent_actions(business_id, action_type, provider_business_url, display_name)`, `business_attributes`. Backs `GET /api/v1/agent/businesses`, `/llms.txt` and the MCP server. Replaces the previous reservation of `0037` for generic webhooks: webhooks are a *distribution* mechanism and there is nothing worth distributing until there is an agent-readable surface. Partner `webhook_subscriptions` / `webhook_deliveries` move here too. |
| `0041` | `0041_least_privilege.sql` | all `CREATE TABLE` above | `bizverse_app` role, `GRANT … ON ALL TABLES IN SCHEMA public`, `REVOKE … FROM PUBLIC`, `ALTER DEFAULT PRIVILEGES`. **The `ALTER DEFAULT PRIVILEGES` clause is what makes ordering irrelevant** — without it, a table created by a later migration ships ungranted and the app 500s on first read. |
| `0042` | `0042_rls.sql` | `0041` | Row-level security on **tenant and owner tables only** (`media`, `verification_documents`, `billing_webhook_events`, `push_subscriptions`, `collections`, `saved_searches`, `api_keys`, `business_claims`, `chat_*`), keyed on a `SET LOCAL app.user_id` written by `withAuth`. Deliberately **not** on `businesses` / `products` / `trend_snapshots`: every search, leaderboard and SSR query runs without a user context, so blanket RLS breaks the hot path. |
| `0043` | `0043_business_locations.sql` | `0034`–`0036` | Second and subsequent locations as a child table, with `businesses.primary_location_id` and `location_count`. **Re-scoped**: the previous reservation was "make `businesses.{address,city,lat,lng,hours}` FK-backed", which is exactly what `0034`–`0036` already deliver — doing it twice would mean two authors writing to `businesses.lat`. The existing columns stay as a cached copy, so this is additive. |

### Deferred, reserved, not scheduled

| Version | File | Why it is last |
|---|---|---|
| `0044` | `0044_booking.sql` | Capacity-based appointments with `btree_gist` + `EXCLUDE USING gist` for double-booking. **No `booking_slots` table and no `booking_services` table** — slots are derived from (location hours × availability × duration × existing appointments × blackouts), and a second catalogue means two admin UIs and a migration story for every existing service. Requires `products.duration_minutes` first. Table stakes: every competitor has it, and none of them differentiate on it. |
| `0045` | `0045_staff_offers.sql` | Per-staff scheduling and commissions. Vertical SaaS, not platform. Only worth building once `0044` proves the availability engine. |
| `0048` | `0048_chat_message_hides.sql` | `0001` | `chat_message_hides(message_id, thread_id, user_id)` so "delete for me" hides from the actor alone. The single `chat_messages.deleted_for` column could not express it: `me` is not `everyone`, so every read path (all of which filter `deleted_for <> 'everyone'`) kept serving the message to the other party while the author still saw it. Migrates existing `me` rows into per-author hides and clears the shared column. |
| `0049` | `0049_business_invite_accepted_user.sql` | `0048` | `business_invites.accepted_user_id` binds an accepted co-owner/viewer grant to a user id. Both authorisation predicates resolved the holder with `JOIN users u ON u.email = i.email`, so a holder who changed their own email (legitimately - `POST /me/email` re-verifies and alerts the old address) silently lost every grant while the owner still saw them listed. Backfills existing accepted invites by address and leaves genuinely orphaned ones NULL. |
| `0046` | `0046_business_hours.sql` | Per-department / per-service hours replacing the `hours` jsonb. Only meaningful per-location, so it belongs after `0043`, and per-staff only after `0045`. |
| `0047` | `0047_user_2fa_enabled_at.sql` | `0001` | `user_2fa.enabled_at` loses `NOT NULL DEFAULT now()`, which marked **every** inserted factor enabled the instant it was written. `UpsertSecret` only cleared the flag on its ON CONFLICT branch, so a first enrollment inherited the default and a re-enrollment did not - the pending/enabled distinction existed nowhere in the database. `Confirm2FA` refuses an already-enabled factor, so first-time enrollment could never be confirmed; abandoning enrollment left 2FA genuinely active with an unconfirmed secret. Repair clears `enabled_at` only where `recovery_codes_hash` is empty, which identifies unconfirmed factors exactly and never disables a confirmed one. Found by the 2FA integration tests, which had never executed because `TEST_DATABASE_URL` was unset wherever they were added. |

Do not create a file whose number is listed here for a different purpose.

---

## Known defects in the existing baseline

Documented so the work above does not re-introduce them, and so the debt is
enumerable rather than folklore.

### Silent `IF NOT EXISTS` name collisions

- **`0015_perf_indexes.sql:6` is a no-op by name.** `CREATE INDEX IF NOT EXISTS
  idx_comments_business` matched the name `0001:302` already used with a
  *different* column list, so the intended `(business_id, created_at)` was never
  built. `IF NOT EXISTS` compares names only.
- **`0013:5` and `0015:7` are byte-identical duplicates** on `notifications`.
  Both exist, so the highest-write table in the schema carries a duplicate
  index. `0031` drops one of them.

### Replaying `0015` resurrects the indexes `0031` removed

The most interesting defect in the baseline, and the one the replay test was
written to find.

`0015` is correctly written — all six statements are
`CREATE INDEX IF NOT EXISTS` — so the static linter passes it and no shape check
flags it. It still cannot be replayed, because **`0031` deliberately dropped
three of the indexes it creates**: `idx_reviews_business_rating`,
`idx_engagement_events_target`, and `idx_notifications_user_type` (the
byte-identical duplicate above).

Re-running `0015` therefore finds those three names missing and recreates them,
while `0031`'s own marker is already recorded so its drops never re-run. The
database is left permanently carrying exactly the redundant write amplification
`0031` existed to eliminate, and **nothing reports an error** — every statement
succeeded.

It is not fixed in place: `0015` is frozen, and every already-migrated
environment records the file as applied and would never see the edit. It is
recorded in `knownNonReplayable` in
`services/api/internal/db/migrate_replay_integration_test.go` with the same
explanation, so the test is green and the debt is visible in two places that
cannot drift apart.

### A non-idempotent seed that can wedge startup permanently

- **`0002_seed_categories.sql` had no `ON CONFLICT`.** The marker is written
  after `COMMIT`, so a crash in that window replayed the file and failed forever
  with `23505` on `categories_slug_key` — a fresh clone that could never start.
  **Fixed in place**, which is safe: the marker gates the body, so the edit is
  invisible to any environment that has the marker and a fix for any that does
  not. The repair procedure for a database already wedged this way is in
  `docs/RUNBOOK.md` under *Migration wedged: rows present, marker absent*. It is
  deliberately **not** automated: writing a version marker by hand asserts
  "I have verified this file's effects are present", and that should be a
  human's decision.

### A `NOT NULL` with no `DEFAULT`, which 500s a core flow

- **`0004` dropped the `CHECK` on `businesses.description` but left `NOT NULL`
  with no `DEFAULT`**, so every insert that omitted the column raised `23502`.
  `service/claims.go` hit this: approving a claim for a new business always
  returned 500. Fixed in `0029`.

### The two `0029` replay defects, for the record

`0029` was written *after* the audit and shipped unwedged, in three ways. Both
are now fixed and both are guarded by tests, but they are recorded because the
reasoning is what stops the next author repeating them:

1. **Twelve `ADD CONSTRAINT` with no `DROP`.** `NOT VALID` skips the table scan,
   not the catalog insert. They committed, the marker was not written, and any
   later failure in the file made the next boot die on `42710 duplicate_object`
   before reaching the index statements. The retry path was unreachable, which
   is worse than the `INVALID`-index case it was supposed to cause.
2. **Five `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS`.** A failed build
   leaves `indisvalid = false`; `IF NOT EXISTS` matches on name and never checks
   it, so the index stayed absent forever. Now drop-paired, and
   `InvalidIndexes` hard-fails boot rather than leaving it to be discovered by a
   slow query.

There was also a **third, purely additive fix**: `0029` dropped
`businesses_slug_key` in the transaction and built its replacement afterwards,
which leaves a window where `businesses.slug` has no uniqueness at all. The order
is now reversed, and `TestSlugConstraintIsDroppedAfterItsReplacement` asserts it.

### Baseline files that are known not to be replayable

`TestMigrateReplayIsSafe` skips twelve baseline files with a per-file
justification in `knownNonReplayable`. `TestKnownNonReplayableEntriesStillExist`
keeps that list honest: the ratchet is closed, so a **new** migration that cannot
be replayed fails the test rather than being quietly tolerated.
