-- 0029_integrity.sql — schema integrity fixes.
--
-- migrate:idempotent  yes
-- migrate:concurrent  true
-- migrate:seed        none
-- migrate:risk        MIXED
-- migrate:note        Pre-flight dedupe (TX) then constraint/index rebuild (autocommit). Every ADD CONSTRAINT is preceded by its DROP and every CONCURRENTLY index by a DROP of the same name, so a partial failure converges on the next boot instead of wedging.
--
-- Each item below is a defect the audit found in the 0001-0027 baseline. They
-- are ordered: pre-flight repair first, then schema relaxations, then
-- constraints, then indexes.
--
-- All changes are backward-compatible with the currently-deployed code, so a
-- rolling deploy (old + new replicas against the same DB) is safe in both
-- directions. Nothing here drops a column a running revision still selects.
--
-- See REGISTRY.md for the numbering reservation and for the known baseline
-- defects this file partially compensates for.
--
-- ---------------------------------------------------------------------------
-- REPLAY SAFETY — READ THIS BEFORE ADDING A STATEMENT
-- ---------------------------------------------------------------------------
-- This file was originally not replay-safe in THREE distinct ways, two of which
-- compounded into a permanent wedge. All three are now fixed, and the shape
-- rules below are enforced by TestMigrationsDeclareTheirIdempotencyContract and
-- proved behaviourally by TestMigrateReplayIsSafe.
--
-- (1) Twelve `ALTER TABLE ... ADD CONSTRAINT` with no `DROP CONSTRAINT IF
--     EXISTS`. NOT VALID skips the table scan; it does NOT skip the catalog
--     insert. These are transaction statements, so they COMMIT at
--     db/migrate.go, and the version marker is written afterwards. Any failure
--     after that commit — including every one of the five CONCURRENTLY
--     statements below — left the marker unwritten, so the next boot re-entered
--     the transaction and died on 42710 duplicate_object before ever reaching
--     the index statements. The retry path was unreachable, which is worse than
--     the INVALID-index case it was supposed to cause.
--
-- (2) Five `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS`. A concurrent build
--     that fails leaves an INVALID index: pg_class has a row, pg_index.indisvalid
--     is false. `IF NOT EXISTS` matches on NAME and never checks validity, so
--     every later replay skipped the build and the invariant was absent forever.
--     Migrate() now hard-fails on any invalid index at boot (see InvalidIndexes)
--     so this state can no longer be reached silently. The CREATE statements
--     below additionally drop-then-build so a partial failure self-heals.
--
-- (3) A pre-flight gap: nothing resolved duplicates that already existed, so
--     the UNIQUE builds were guaranteed to fail on a database that had them.
--     Section 0 below repairs all five duplicate classes first. It runs in the
--     transaction and therefore commits before the first concurrent statement.
--
-- The general rules, from REGISTRY.md:
--   * every ADD CONSTRAINT gets a DROP CONSTRAINT IF EXISTS in the same file
--   * every CREATE INDEX CONCURRENTLY gets a DROP INDEX CONCURRENTLY IF EXISTS
--     on the line above, and does NOT use IF NOT EXISTS
--   * comments go on their own lines — SplitStatements drops whole-line `--`
--     comments, and a trailing `--` suppresses a statement boundary
--   * never end a line with a string literal whose last character is `;`
-- ---------------------------------------------------------------------------

-- ===========================================================================
-- 0. PRE-FLIGHT — resolve duplicates the UNIQUE constraints below will trip on
-- ===========================================================================
--
-- Every block here is written so that re-running it is a no-op: the ORDER BY
-- clauses are TOTAL (each ends in a unique column), so the "winner" is the same
-- row every time, and once there is nothing to fix the ranking yields rn = 1
-- for all rows and the UPDATE matches nothing.
--
-- Four of the five repair in place. One (media) is operator-gated and is
-- explained where it is.

-- ---------------------------------------------------------------------------
-- 0.1 businesses.slug among live rows
--
-- A slug is PUBLIC SURFACE. Breaking it breaks printed menus, QR codes, inbound
-- links, and the single owner-requested rename budget that 0022 tracks in
-- `slug_changed_at`. Deleting the loser would cascade to its products, reviews,
-- comments, follows and trend snapshots. Renaming costs one URL; deleting costs
-- all of that.
--
-- The ranking is therefore by "how much of the outside world points at this",
-- not by recency: a five-year-old listing with 40 reviews outranks the draft
-- created last week that a retry happened to assign the same slug to.
--
-- The derived slug embeds the row's own id, so two losers can never collide
-- with each other and a collision with a hand-made slug is not reachable in
-- practice. The post-condition below is the belt-and-braces: if one did occur
-- we RAISE here rather than let the concurrent build fail opaquely twenty
-- minutes later.
--
-- `slug_changed_at` is deliberately NOT set. That column is the owner's one
-- permitted rename; a system-initiated rename must not spend it.
-- ---------------------------------------------------------------------------
WITH ranked AS (
    SELECT b.id,
           row_number() OVER (
               PARTITION BY b.slug
               ORDER BY
                   (SELECT count(*) FROM products p
                     WHERE p.business_id = b.id AND p.deleted_at IS NULL) DESC,
                   (SELECT count(*) FROM reviews r
                     WHERE r.business_id = b.id AND r.deleted_at IS NULL) DESC,
                   (b.status IN ('verified', 'paused')) DESC,
                   (b.owner_id IS NOT NULL) DESC,
                   b.created_at,
                   b.id
           ) AS rn
      FROM businesses b
     WHERE b.deleted_at IS NULL
)
UPDATE businesses b
   SET slug = b.slug || '-' || replace(left(b.id::text, 8), '-', ''),
       updated_at = now()
  FROM ranked r
 WHERE b.id = r.id
   AND r.rn > 1;

DO $chk_slug$
DECLARE n int;
BEGIN
    SELECT count(*) INTO n
      FROM (SELECT slug FROM businesses WHERE deleted_at IS NULL
             GROUP BY slug HAVING count(*) > 1) d;
    IF n > 0 THEN
        RAISE EXCEPTION
            'pre-flight: % business slug group(s) still duplicated after rename; resolve by hand', n;
    END IF;
END $chk_slug$;

-- ---------------------------------------------------------------------------
-- 0.2 media.path — OPERATOR-GATED, see the long note at section 4b
-- ---------------------------------------------------------------------------
DO $gate_media$
DECLARE n int;
BEGIN
    SELECT count(*) INTO n
      FROM (SELECT path FROM media GROUP BY path HAVING count(*) > 1) d;
    IF n > 0 AND current_setting('bizverse.allow_dedupe', true) IS DISTINCT FROM 'on' THEN
        RAISE EXCEPTION
            'pre-flight: % duplicate-path media group(s) block media_path_key.%Review each group (confirm the file still exists under MEDIA_DIR), then re-run with:  SET bizverse.allow_dedupe = ''on'';',
            n, chr(10);
    END IF;
END $gate_media$;

DO $dedupe_media$
DECLARE
    g        record;
    v_winner uuid;
    v_loser  uuid;
    v_losers uuid[];
BEGIN
    FOR g IN
        SELECT path FROM media GROUP BY path HAVING count(*) > 1 ORDER BY path
    LOOP
        -- Winner: a LIVE row first (that is the one currently serving), then
        -- the oldest (its id is already baked into stored URLs, OG tags and
        -- sent chat messages), then the lowest id for a total order.
        SELECT id INTO v_winner
          FROM media
         WHERE path = g.path
         ORDER BY (deleted_at IS NOT NULL), created_at, id
         LIMIT 1;

        SELECT coalesce(array_agg(id), '{}'::uuid[]) INTO v_losers
          FROM media
         WHERE path = g.path AND id <> v_winner;

        -- Re-point EVERY reference before deleting a loser. Eight columns
        -- reference a media id and not one of them has a foreign key, so a
        -- bare DELETE orphans a compliance document, a chat message, and the
        -- JSON gallery a storefront renders.
        FOREACH v_loser IN ARRAY v_losers LOOP
            -- uuid columns
            UPDATE chat_messages
               SET media_id = v_winner
             WHERE media_id = v_loser;
            UPDATE verification_documents
               SET media_id = v_winner
             WHERE media_id = v_loser;
            UPDATE products
               SET cover_image_id = v_winner
             WHERE cover_image_id = v_loser;
            UPDATE product_variants
               SET image_id = v_winner
             WHERE image_id = v_loser;

            -- uuid[] columns. DISTINCT collapses the loser against the winner
            -- if both are present, and array_length stays within the 1-10
            -- budget service/businesses.go enforces. Sorting is safe here
            -- because these arrays are an UNSET set.
            UPDATE products p
               SET image_ids = (SELECT coalesce(array_agg(DISTINCT x ORDER BY x), '{}'::uuid[])
                                  FROM unnest(p.image_ids) x)
             WHERE p.image_ids @> ARRAY[v_loser];
            UPDATE reviews r
               SET image_ids = (SELECT coalesce(array_agg(DISTINCT x ORDER BY x), '{}'::uuid[])
                                  FROM unnest(r.image_ids) x)
             WHERE r.image_ids @> ARRAY[v_loser];

            -- jsonb array of ids, ORDERED. jsonb's text form is normalised and
            -- deterministic, so a targeted uuid-to-uuid replace is exact and
            -- preserves the owner's gallery ordering.
            UPDATE businesses b
               SET gallery = replace(b.gallery::text,
                                     '"' || v_loser::text || '"',
                                     '"' || v_winner::text || '"')::jsonb
             WHERE b.gallery @> to_jsonb(v_loser::text);

            -- text URL columns: the media id is EMBEDDED in the path. Guarded
            -- by position() so rows that do not reference the loser are not
            -- rewritten (which would bump updated_at for no reason).
            UPDATE businesses b
               SET logo_url = replace(b.logo_url, v_loser::text, v_winner::text)
             WHERE b.logo_url IS NOT NULL AND position(v_loser::text in b.logo_url) > 0;
            UPDATE businesses b
               SET cover_url = replace(b.cover_url, v_loser::text, v_winner::text)
             WHERE b.cover_url IS NOT NULL AND position(v_loser::text in b.cover_url) > 0;
            UPDATE users u
               SET avatar_url = replace(u.avatar_url, v_loser::text, v_winner::text)
             WHERE u.avatar_url IS NOT NULL AND position(v_loser::text in u.avatar_url) > 0;
        END LOOP;

        DELETE FROM media WHERE id = ANY(v_losers);
        RAISE NOTICE 'media pre-flight: path % -> kept %, removed % duplicate row(s)',
            g.path, v_winner, array_length(v_losers, 1);
    END LOOP;

    PERFORM 1;
END $dedupe_media$;

-- ---------------------------------------------------------------------------
-- 0.3 collections: at most one default per user
--
-- repo/engagement.go DefaultCollection is read-then-insert with no ORDER BY, so
-- today the row the user SEES is whichever Postgres returns first and the other
-- one — possibly the one holding their saves — is invisible. Item count answers
-- "which did they actually use".
--
-- Losers are DEMOTED, never deleted: a public collection URL may already have
-- been shared, and collections.cover_item_id points into their items.
-- is_default = false fully satisfies the partial index and is reversible.
-- ---------------------------------------------------------------------------
WITH ranked AS (
    SELECT c.id,
           row_number() OVER (
               PARTITION BY c.user_id
               ORDER BY (SELECT count(*) FROM collection_items ci
                          WHERE ci.collection_id = c.id) DESC,
                        c.created_at,
                        c.id
           ) AS rn
      FROM collections c
     WHERE c.is_default AND c.deleted_at IS NULL
)
UPDATE collections c
   SET is_default = false,
       updated_at = now()
  FROM ranked r
 WHERE c.id = r.id
   AND r.rn > 1;

-- ---------------------------------------------------------------------------
-- 0.4 trend_snapshots: one row per (period, business_id, taken_at)
--
-- DELETE is safe here, unlike media. A snapshot is a pure materialization with
-- no inbound reference: every reader filters on (period, business_id) or
-- max(taken_at) — repo/analytics.go, service/search.go, service/cities.go — so a
-- removed duplicate cannot dangle. ops.PruneSnapshots would have reclaimed it
-- within 14 days regardless.
--
-- Higher score wins: it is the more-informed computation, and `velocity` (a
-- later run saw more events) breaks the tie.
--
-- The harm of leaving the duplicate is what 0029 originally described:
-- service/search.go joins the latest snapshot set onto every page, so a
-- duplicate returns the SAME BUSINESS TWICE in one page of results.
-- ---------------------------------------------------------------------------
DELETE FROM trend_snapshots t
 WHERE t.id NOT IN (
     SELECT id
       FROM (SELECT id,
                    row_number() OVER (
                        PARTITION BY period, business_id, taken_at
                        ORDER BY score DESC, velocity DESC, id) AS rn
               FROM trend_snapshots) k
      WHERE k.rn = 1);

-- ---------------------------------------------------------------------------
-- 0.5 business_claims: one open claim per listing
--
-- Losers are REJECTED, not deleted: a claim is a person's submission with
-- evidence text, and deleting it destroys a real claim. Rejecting keeps it
-- visible in /me/claims with a decided state.
--
-- decided_by is deliberately NULL. No ADMIN decided this, and inventing an
-- admin id would put a false entry in the audit trail. The note carries the
-- machine-readable reason instead.
--
-- business_id is deliberately left unchanged so the claim stays attached to the
-- listing's history rather than becoming an unlinked new-listing claim.
-- ---------------------------------------------------------------------------
WITH ranked AS (
    SELECT c.id,
           row_number() OVER (PARTITION BY c.business_id ORDER BY c.created_at, c.id) AS rn
      FROM business_claims c
     WHERE c.status = 'open' AND c.business_id IS NOT NULL
)
UPDATE business_claims c
   SET status      = 'rejected',
       decided_at  = now(),
       note        = 'Auto-closed by migration 0029 pre-flight: an older open claim for this listing already existed. Reopen via /admin/claims if this claimant in fact owns the listing.'
  FROM ranked r
 WHERE c.id = r.id
   AND r.rn > 1;

-- ---------------------------------------------------------------------------
-- 1. businesses.description needs a DEFAULT
--
-- 0004 dropped the `char_length(description) >= 50` CHECK but left NOT NULL
-- with no default. Every INSERT that omits the column therefore fails with
-- 23502. service/claims.go hit this: approving a claim for a NEW business
-- always returned 500. The Go side now seeds the column explicitly
-- (draftDescription), and this DEFAULT is the database-level backstop for any
-- other writer — including a future migration or an operator running psql.
--
-- PG 11+ sets a default without a table rewrite, so this is instant on a large
-- table. Deliberately empty rather than a sentence: silently inventing
-- description text for rows that legitimately have none is worse than a short
-- value, and the wizard fills the real text.
-- ---------------------------------------------------------------------------
ALTER TABLE businesses ALTER COLUMN description SET DEFAULT '';

-- ---------------------------------------------------------------------------
-- 2. businesses.owner_id must be nullable for "claim an existing listing"
--
-- owner_id was `uuid NOT NULL REFERENCES users(id)`, which makes the flagship
-- competitor flow structurally unreachable: service/claims.go checks
-- `if b.OwnerID != ""` to detect an unowned listing, and a NOT NULL uuid column
-- can never scan into "" — so 100% of existing-listing claims were rejected
-- with "This listing already has an owner."
--
-- Making it nullable is the only way to represent an unowned listing, and it
-- matches the real world: seed data and any imported directory will contain
-- businesses nobody has claimed yet.
--
-- Blast radius, stated explicitly because NOT NULL -> NULL is a schema
-- relaxation that the Go layer must cope with:
--   * `Business.OwnerID` is `json:"-"` and is compared as a string, so "" is a
--     safe zero value. Authorization goes through CanManageBusiness, which is a
--     positive check, so an unowned business is simply not manageable by anyone
--     — the correct outcome.
--   * `DELETE FROM users` now has to consider a NULL owner_id. The app
--     anonymizes rather than hard-deleting users, so nothing regresses.
-- ---------------------------------------------------------------------------
ALTER TABLE businesses ALTER COLUMN owner_id DROP NOT NULL;

-- Finding unowned listings is a moderation/claim-surface query; without this
-- it is a sequential scan of the whole directory.
DROP INDEX CONCURRENTLY IF EXISTS idx_businesses_unclaimed;
CREATE INDEX CONCURRENTLY idx_businesses_unclaimed
    ON businesses (id) WHERE owner_id IS NULL;

-- ---------------------------------------------------------------------------
-- 3. Soft-delete-aware uniqueness
--
-- `businesses.slug` is globally UNIQUE, but soft-deleting a business
-- (deleted_at = now()) leaves the row, so the slug stays reserved forever.
-- Meanwhile repo/businesses.go `SlugTaken` EXCLUDES deleted rows — so the API
-- reports the slug as free and the UNIQUE constraint then rejects the insert
-- with a 23505 that nothing maps to 409. The user gets a 500 for an action the
-- UI presented as valid.
--
-- Same class of bug on product_variants.sku: SKUTaken is checked per business,
-- the constraint is global, so two owners can both pass validation and the
-- second insert 500s. The constraint is left in place as the single source of
-- truth and the Go check is corrected to match it (see
-- service/products.go SKUTaken) — narrowing the constraint to "unique per
-- business" is not expressible as a partial index, because a partial index
-- predicate cannot contain a subquery joining products, and denormalizing
-- business_id onto product_variants just to widen uniqueness would be worse.
--
-- Fix: replace the table constraint with a partial unique index that only
-- considers live rows. Existing duplicates among deleted rows are irrelevant and
-- ignored by the partial predicate; duplicates among live rows were repaired in
-- pre-flight 0.1.
--
-- ORDERING IS LOAD-BEARING. The two statements below run on autocommit, in
-- file order, and the DROP must come second. Dropping the old constraint first
-- would open a window — potentially minutes on a large table — in which
-- `businesses.slug` has NO uniqueness at all, and a single concurrent insert of
-- a duplicate in that window would wedge the file permanently. A partial unique
-- index and a full unique constraint can legally coexist (the partial one is
-- strictly weaker), so building first and dropping second is safe.
--
-- The `/* autocommit */` tag is what routes the DROP out of the transaction; the
-- marker is a block comment rather than a `--` comment because SplitStatements
-- drops whole-line `--` comments before a statement is ever assembled.
-- ---------------------------------------------------------------------------
DROP INDEX CONCURRENTLY IF EXISTS businesses_slug_live;
CREATE UNIQUE INDEX CONCURRENTLY businesses_slug_live
    ON businesses (slug) WHERE deleted_at IS NULL;
/* autocommit */ ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_slug_key;

-- ---------------------------------------------------------------------------
-- 4. media needs a soft delete
--
-- A report on a chat attachment or review photo had to be "actionable" (PRD
-- §5.8.2 lists attachments as a first-class queue), but there was no way to
-- remove a media row: HideContent handled only review/comment/product/message,
-- so such a report could be dismissed or warned and never removed.
--
-- Soft delete rather than DELETE, for three reasons:
--   * RestoreContent must be able to un-hide it (a reversible moderation
--     action), and a hard DELETE takes the row with it.
--   * moderation_actions references the target by (type, id) with no FK, so a
--     deleted row would leave an unresolvable audit trail.
--   * deleting the row is what the orphan-media sweep keys off, so an immediate
--     DELETE would also race the file's own teardown.
--
-- The bytes on disk are reclaimed later by ops.CleanupOrphanMedia: it keeps a
-- soft-deleted row's file (and thumbnail) for softDeleteReclaimWindow = 30 days
-- so a mistaken "hide" stays reversible, then deletes the row and lets the
-- sweep reclaim the file.
-- ---------------------------------------------------------------------------
ALTER TABLE media ADD COLUMN IF NOT EXISTS deleted_at timestamptz;

-- The nightly sweep scans by deleted_at to find rows past the reclaim window,
-- so it needs an index.
DROP INDEX CONCURRENTLY IF EXISTS idx_media_deleted_at;
CREATE INDEX CONCURRENTLY idx_media_deleted_at ON media (deleted_at);

-- ---------------------------------------------------------------------------
-- 4b. media.path must be unique
--
-- WHY THIS IS OPERATOR-GATED (unlike 0.1, 0.3, 0.4, 0.5)
-- ---------------------------------------------------------------------------
-- The justification this file previously carried claimed that a duplicate path
-- would make ops.CleanupOrphanMedia judge the file a false "orphan" and
-- os.Remove it. That mechanism is NOT REACHABLE and the comment was wrong.
-- service/ops.go builds its on-disk map as a map[string]bool — a SET — so two
-- rows sharing a path collapse to one key, the file is present in the set, and
-- the walk keeps it. The reclaim branch cannot fire either: the surviving live
-- row sets the key regardless of iteration order.
--
-- A wrong justification in a file that gets read during a future incident is
-- itself a defect, so the real reason is below. It is a MODERATION BYPASS, not
-- a housekeeping one:
--
--   service/media.go resolves a media id to bytes through media.path. Two rows
--   on one path means two ids serve identical bytes. admin.go's attachment-hide
--   sets deleted_at on ONE row, and service/media.go refuses to serve a row with
--   deleted_at set — so GET /api/v1/media/{hidden}/file 404s correctly while
--   GET /api/v1/media/{twin}/file keeps serving the hidden file. There is no
--   expiry and no audit trail for the second fetch.
--
-- How duplicates arise: media.go builds path as kind + "/" + uuid4 + ext, so a
-- collision needs the same UUIDv4 twice. That is not reachable through the API.
-- It IS reachable through pg_restore into a non-empty table, a hand-written
-- INSERT, or a data import — which is why pre-flight 0.2 is gated rather than
-- automatic: this migration cannot see the filesystem, so it cannot decide
-- whether to keep one row or delete both when the file is already gone.
--
-- To resolve: run the audit query, confirm each file still exists under
-- MEDIA_DIR, then re-run with `SET bizverse.allow_dedupe = 'on';`
--
--   SELECT path, count(*), array_agg(id ORDER BY created_at) FROM media
--    GROUP BY path HAVING count(*) > 1;
-- ---------------------------------------------------------------------------
DROP INDEX CONCURRENTLY IF EXISTS media_path_key;
CREATE UNIQUE INDEX CONCURRENTLY media_path_key
    ON media (path);

-- ---------------------------------------------------------------------------
-- 5. Exactly one default collection per user
--
-- repo/engagement.go DefaultCollection is read-then-insert with no ON CONFLICT.
-- Two concurrent first-saves both observe no row and both insert, producing two
-- "Favorites" collections; only one is ever returned, and the other is orphaned
-- with items the user cannot see. Pre-flight 0.3 repaired existing damage.
-- ---------------------------------------------------------------------------
DROP INDEX CONCURRENTLY IF EXISTS collections_one_default;
CREATE UNIQUE INDEX CONCURRENTLY collections_one_default
    ON collections (user_id) WHERE is_default AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- 6. trend_snapshots must not duplicate a (period, business, taken_at)
--
-- trending.Compute runs unguarded INSERT...SELECT. The advisory leader lock
-- prevents cross-replica duplication, but nothing prevents a retry after a
-- partial failure, or an operator re-running by hand, from writing a second
-- snapshot for the same key. Pre-flight 0.4 removed existing duplicates.
-- ---------------------------------------------------------------------------
DROP INDEX CONCURRENTLY IF EXISTS trend_snapshots_uniq;
CREATE UNIQUE INDEX CONCURRENTLY trend_snapshots_uniq
    ON trend_snapshots (period, business_id, taken_at);

-- ---------------------------------------------------------------------------
-- 7. One open claim per business
--
-- Nothing prevented two users claiming the same listing; both were queued and
-- both could be approved. The ownership transfer in claims.go is not
-- conditional on the listing still being unowned, so two approvals overwrite
-- each other while BOTH claimants receive an "approved" notification.
-- Pre-flight 0.5 closed the existing duplicates.
-- ---------------------------------------------------------------------------
DROP INDEX CONCURRENTLY IF EXISTS business_claims_open_business;
CREATE UNIQUE INDEX CONCURRENTLY business_claims_open_business
    ON business_claims (business_id) WHERE status = 'open';

-- ---------------------------------------------------------------------------
-- 8. Constrain the enum-like text columns
--
-- These are all `text` with no CHECK, while the Go layer enumerates the legal
-- values. A typo, a bad seed script, or a future migration writing
-- 'verifed' instead of 'verified' would otherwise be accepted silently and only
-- surface as a business that mysteriously never appears in search.
--
-- Checked as explicit IN lists rather than ENUM types: the values are already
-- validated in Go, CHECKs are far cheaper to add a value to, and this avoids an
-- ALTER TYPE that would rewrite the table.
--
-- NOT VALID is deliberate: the constraint is enforced for new/updated rows
-- immediately but is not verified against existing rows yet, so no table
-- scan/lock is taken on a live table. See the operator follow-up block at the
-- bottom for the validation procedure.
--
-- Every ADD CONSTRAINT is preceded by its own DROP CONSTRAINT IF EXISTS. That
-- is not boilerplate: without it, a replay after any later failure in this file
-- aborts at the first constraint with 42710 and never reaches the sections
-- below. The DROP/ADD pair is a single unit inside the migration transaction, so
-- a crash between them rolls back both.
-- ---------------------------------------------------------------------------

ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_type_check;
ALTER TABLE notifications
    ADD CONSTRAINT notifications_type_check CHECK (
        type IN (
            'message_received','reaction_added','helpful_vote','product_review',
            'review_posted','review_replied','comment_on_business','comment_mention',
            'question_asked','question_answered','business_update',
            'verification_result','doc_re_request','business_suspended',
            'business_restored','moderation_warning','appeal_result',
            'category_new_business','back_in_stock','claim_result','trend_anomaly',
            'system_announcement'
        )
    ) NOT VALID;

ALTER TABLE moderation_actions DROP CONSTRAINT IF EXISTS moderation_actions_action_check;
ALTER TABLE moderation_actions
    ADD CONSTRAINT moderation_actions_action_check CHECK (
        action IN (
            'hide','restore','warn','suspend','ban','unban','approve','reject',
            'delete_for_everyone','doc_re_request'
        )
    ) NOT VALID;

ALTER TABLE moderation_actions DROP CONSTRAINT IF EXISTS moderation_actions_target_check;
ALTER TABLE moderation_actions
    ADD CONSTRAINT moderation_actions_target_check CHECK (
        target_type IN (
            'review','comment','message','product','business','user','reaction',
            'attachment','error','support'
        )
    ) NOT VALID;

ALTER TABLE auth_events DROP CONSTRAINT IF EXISTS auth_events_event_check;
ALTER TABLE auth_events
    ADD CONSTRAINT auth_events_event_check CHECK (
        event IN (
            'login','login_fail','register','password_reset','logout','ban',
            'suspend','unban','2fa_enable','2fa_disable','session_revoke',
            'document_view'
        )
    ) NOT VALID;

ALTER TABLE media DROP CONSTRAINT IF EXISTS media_kind_check;
ALTER TABLE media
    ADD CONSTRAINT media_kind_check CHECK (
        kind IN (
            'logo','cover','gallery','product','avatar','chat_image','chat_file',
            'chat_audio','chat_video','document_verification','review_photo'
        )
    ) NOT VALID;

-- reports.target_type is deliberately NOT repeated here: migration 0025
-- already added reports_target_type_check with the same value list (it had to,
-- so that support/error reports could be accepted at all). Adding it again
-- fails with 42710 and aborts the whole migration.

-- ISO 4217. products.currency is independent of the parent business's, so a
-- variant can be priced in a currency with no currency_rates row — which the
-- compare page silently renders as a wrong number rather than an error.
ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_currency_check;
ALTER TABLE businesses
    ADD CONSTRAINT businesses_currency_check CHECK (currency ~ '^[A-Z]{3}$') NOT VALID;
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_currency_check;
ALTER TABLE products
    ADD CONSTRAINT products_currency_check CHECK (currency ~ '^[A-Z]{3}$') NOT VALID;
ALTER TABLE product_variants DROP CONSTRAINT IF EXISTS product_variants_currency_check;
ALTER TABLE product_variants
    ADD CONSTRAINT product_variants_currency_check CHECK (currency ~ '^[A-Z]{3}$') NOT VALID;

-- price_level: the schema allowed 0-4 but service/businesses.go accepts only
-- 1-4, so a row written by SQL or a seed renders as a broken price tier.
ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_price_level_check;
ALTER TABLE businesses
    ADD CONSTRAINT businesses_price_level_check CHECK (price_level BETWEEN 1 AND 4) NOT VALID;

-- businesses.pre_suspend_status mirrors the business_status enum as unconstrained
-- text and is fed straight back into `status = $2` by
-- handlers_admin2.go — a typo becomes 22P02 from an ADMIN action, i.e. a 500.
ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_pre_suspend_status_check;
ALTER TABLE businesses
    ADD CONSTRAINT businesses_pre_suspend_status_check CHECK (
        pre_suspend_status IS NULL OR pre_suspend_status IN (
            'draft','pending_review','verified','rejected','suspended','paused','closed'
        )
    ) NOT VALID;

-- An unknown timezone makes `AT TIME ZONE tz` inside biz_is_open_now RAISE,
-- which aborts the entire search query rather than one row. Validated in Go on
-- write, but a seed script or direct SQL write bypasses that.
--
-- pg_timezone_names cannot be used in a CHECK (subqueries are not immutable),
-- so this is an empty-set assertion: it documents the intent and blocks NULL
-- and whitespace-only values, while correctness is enforced by the Go validator
-- and by 0030's safer open-now implementation.
ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_timezone_not_blank;
ALTER TABLE businesses
    ADD CONSTRAINT businesses_timezone_not_blank CHECK (
        timezone IS NOT NULL AND btrim(timezone) <> ''
    ) NOT VALID;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_timezone_not_blank;
ALTER TABLE users
    ADD CONSTRAINT users_timezone_not_blank CHECK (
        timezone IS NOT NULL AND btrim(timezone) <> ''
    ) NOT VALID;

-- ---------------------------------------------------------------------------
-- 9. verification_documents must not accept an unencrypted doc kind
--
-- A verification document must be sealed at rest. The column has no FK (it is
-- deliberately polymorphic) so nothing prevented pointing at a media row of a
-- different kind, which would be served in the clear to the admin viewer.
-- ---------------------------------------------------------------------------
-- (left to the service layer: the check needs the media row's kind, which is a
--  cross-table condition this form cannot express. It IS expressible as a
--  trigger; tracked as a known gap.)

-- ---------------------------------------------------------------------------
-- Operator follow-up: validating the NOT VALID constraints
-- ---------------------------------------------------------------------------
-- The eleven constraints above are enforced for every new or updated row but
-- have never been proven against existing rows. `NOT VALID` skips the scan
-- precisely so this file does not take a SHARE UPDATE EXCLUSIVE lock on a live
-- table, which means a pre-existing violating row is still out there and will
-- surface at an unpredictable future VALIDATE.
--
-- The procedure is two steps, and the order matters: run every pre-flight query
-- FIRST. Validating a constraint that does hold takes a SHARE UPDATE EXCLUSIVE
-- lock and scans the whole table, so it belongs in a maintenance window, not in
-- the middle of a launch. The queries are read-only and can run any time.
--
-- 1. Pre-flight — read-only, run during business hours:
--
--   SELECT id, type FROM notifications WHERE type NOT IN (
--     'message_received','reaction_added','helpful_vote','product_review',
--     'review_posted','review_replied','comment_on_business','comment_mention',
--     'question_asked','question_answered','business_update',
--     'verification_result','doc_re_request','business_suspended',
--     'business_restored','moderation_warning','appeal_result',
--     'category_new_business','back_in_stock','claim_result','trend_anomaly',
--     'system_announcement');
--   SELECT id, event FROM auth_events WHERE event NOT IN (
--     'login','login_fail','register','password_reset','logout','ban',
--     'suspend','unban','2fa_enable','2fa_disable','session_revoke','document_view');
--   SELECT id, kind FROM media WHERE kind NOT IN (
--     'logo','cover','gallery','product','avatar','chat_image','chat_file',
--     'chat_audio','chat_video','document_verification','review_photo');
--   SELECT id, action, target_type FROM moderation_actions
--     WHERE action NOT IN (
--       'hide','restore','warn','suspend','ban','unban','approve','reject',
--       'delete_for_everyone','doc_re_request')
--        OR target_type NOT IN (
--       'review','comment','message','product','business','user','reaction',
--       'attachment','error','support');
--   SELECT id FROM businesses WHERE currency !~ '^[A-Z]{3}$';
--   SELECT id FROM products WHERE currency !~ '^[A-Z]{3}$';
--   SELECT id FROM product_variants WHERE currency !~ '^[A-Z]{3}$';
--   SELECT id FROM businesses WHERE price_level NOT BETWEEN 1 AND 4;
--   SELECT id FROM businesses
--     WHERE pre_suspend_status IS NOT NULL AND pre_suspend_status NOT IN (
--       'draft','pending_review','verified','rejected','suspended','paused','closed');
--   SELECT id FROM businesses WHERE btrim(coalesce(timezone, '')) = '';
--   SELECT id FROM users WHERE btrim(coalesce(timezone, '')) = '';
--
-- 2. Validation — one at a time, off-peak, checking convalidated between each:
--
--   ALTER TABLE notifications       VALIDATE CONSTRAINT notifications_type_check;
--   ALTER TABLE moderation_actions  VALIDATE CONSTRAINT moderation_actions_action_check;
--   ALTER TABLE moderation_actions  VALIDATE CONSTRAINT moderation_actions_target_check;
--   ALTER TABLE auth_events         VALIDATE CONSTRAINT auth_events_event_check;
--   ALTER TABLE media               VALIDATE CONSTRAINT media_kind_check;
--   ALTER TABLE businesses          VALIDATE CONSTRAINT businesses_currency_check;
--   ALTER TABLE products            VALIDATE CONSTRAINT products_currency_check;
--   ALTER TABLE product_variants    VALIDATE CONSTRAINT product_variants_currency_check;
--   ALTER TABLE businesses          VALIDATE CONSTRAINT businesses_price_level_check;
--   ALTER TABLE businesses          VALIDATE CONSTRAINT businesses_pre_suspend_status_check;
--   ALTER TABLE businesses          VALIDATE CONSTRAINT businesses_timezone_not_blank;
--   ALTER TABLE users               VALIDATE CONSTRAINT users_timezone_not_blank;
--
--   -- after each, confirm it took:
--   SELECT conname, convalidated FROM pg_constraint
--    WHERE conname = 'notifications_type_check';
--
-- A VALIDATE that aborts names the offending row, so repair the data and re-run
-- rather than dropping the constraint. Dropping it reopens the exact
-- silent-bad-write hole section 8 exists to close.
-- ---------------------------------------------------------------------------
