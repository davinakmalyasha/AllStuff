-- 0049_business_invite_accepted_user.sql — bind an accepted co-owner grant to a
-- user id instead of re-resolving it through an email address.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: ADD COLUMN, CREATE TABLE and CREATE INDEX on a new table, all catalogue-only with no table rewrite. The partial unique index is built non-concurrently because it must be VALID before the assertion below reads it.

-- WHY THIS EXISTS
-- --------------
-- business_invites stores `email`, and both authorisation predicates resolved the
-- holder by joining it back to the user:
--
--   CanManageBusiness:  JOIN users u ON u.email = i.email
--   IsBusinessViewer:   JOIN users u ON u.email = i.email
--
-- An email is not an identity, and POST /api/v1/me/email lets a user change theirs
-- (correctly - it re-verifies and alerts the old address). So changing your address
-- silently revoked every co-owner and viewer grant you held, while the owner still
-- saw you listed as an accepted collaborator with the old address.
--
-- Verified against a database rather than reasoned about: a user with an accepted
-- co_owner invite goes from able to manage the listing to unable to, purely by
-- changing their email. The grant is stored in a row nobody deleted.
--
-- The fix is to bind the grant at the moment it is accepted. AcceptInvite already
-- knows the accepting user, so accepted_user_id is recorded then and every later
-- authorisation reads it directly - no join, no email comparison on a hot path, and
-- no dependence on an address the holder may legitimately change again.
ALTER TABLE business_invites
  ADD COLUMN IF NOT EXISTS accepted_user_id uuid REFERENCES users(id) ON DELETE CASCADE;

-- One accepted grant per (business, user, role). Without it the same person could
-- hold several accepted co_owner rows for one listing, which makes "revoke this
-- collaborator" ambiguous and inflates the team_seats count.
--
-- Partial on accepted_user_id IS NOT NULL so pending invites - which have no holder
-- yet - are unconstrained: inviting the same address twice before either is accepted
-- is normal and must not raise.
CREATE UNIQUE INDEX IF NOT EXISTS uq_business_invites_accepted_user_role
  ON business_invites (business_id, accepted_user_id, role)
  WHERE accepted_user_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- BACKFILL
-- ---------------------------------------------------------------------------
-- Existing accepted invites are attributed to the account that currently holds
-- their address. This is the best available mapping and it is exact whenever the
-- address has not changed since acceptance - which is the case for every grant
-- created before this migration, since changing the address is what broke them.
--
-- Invites whose address matches no account stay NULL. They are genuinely orphaned:
-- nobody holds that address, so there is nobody to attribute the grant to, and
-- attributing it to a guess would grant management to the wrong person.
-- UPDATE, not INSERT ... SELECT. This reads from the same relation it writes, and
-- in Postgres that is evaluated against the snapshot taken at the start of the
-- statement - so an INSERT ... SELECT from business_invites into business_invites
-- sees pre-statement rows and would append a DUPLICATE of every eligible invite
-- rather than binding it. The first draft did exactly that, and ON CONFLICT (id)
-- DO NOTHING silently absorbed every duplicate, leaving the column NULL and the
-- assertion below correctly reporting that nothing had been bound.
--
-- The duplicate would also have been a unique-index violation the moment the index
-- existed, so the symptom would have moved rather than disappeared.
UPDATE business_invites i
   SET accepted_user_id = u.id
  FROM users u
 WHERE u.email = i.email
   AND i.accepted_at IS NOT NULL
   AND i.accepted_user_id IS NULL;

-- Orphans are reported rather than rejected: an accepted invite pointing at an
-- address nobody holds conferred nothing before this change and confers nothing
-- after it. Asserted separately below, against the property that actually matters -
-- that every grant which DID resolve is still bound.
DO $assertions$
DECLARE
    v_orphan    int;
    v_backfill  int;
    v_resolvable int;
    v_total     int;
BEGIN
    SELECT count(*) INTO v_total
      FROM business_invites WHERE accepted_at IS NOT NULL;

    SELECT count(*) INTO v_orphan
      FROM business_invites WHERE accepted_at IS NOT NULL AND accepted_user_id IS NULL;

    SELECT count(*) INTO v_backfill
      FROM business_invites WHERE accepted_user_id IS NOT NULL;

    -- Count the grants that were RESOLVABLE before this migration. A grant whose
    -- address matched no account never conferred access, so it is not something the
    -- change can have broken, and demanding it be attributed would mean inventing a
    -- holder.
    SELECT count(*) INTO v_resolvable
      FROM business_invites i
      JOIN users u ON u.email = i.email
     WHERE i.accepted_at IS NOT NULL;

    -- The property this whole change exists to preserve: nobody who could manage or
    -- view a listing before it has lost that ability. Every grant that resolved to
    -- an account must now be bound to that account.
    --
    -- Stated as "resolvable minus backfilled" rather than "orphans must be zero",
    -- because orphaning is a legitimate pre-existing state - an accepted invite
    -- pointing at an address nobody holds conferred nothing to begin with. The first
    -- draft asserted orphan-free and rejected any database with an unclaimed
    -- address, which is a data condition unrelated to this fix and would have
    -- blocked the deploy on a cleanup that belongs in a separate, deliberate pass.
    IF v_backfill < v_resolvable THEN
        RAISE EXCEPTION
            '0049 FAIL: % accepted invite(s) resolved to an account before this migration but only % were bound to a user id. A resolvable grant must never be left unbound, or its holder silently loses access.',
            v_resolvable, v_backfill;
    END IF;

    RAISE NOTICE '0049: % accepted invite(s); % resolved to an account and are now bound by id; % orphaned (address matches no account, conferred nothing before or after)',
        v_total, v_backfill, v_orphan;
END $assertions$;