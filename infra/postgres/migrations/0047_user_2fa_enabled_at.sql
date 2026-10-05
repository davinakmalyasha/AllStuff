-- 0047_user_2fa_enabled_at.sql — stop `user_2fa.enabled_at` from meaning "now"
-- on every insert.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        No CONCURRENTLY: ALTER TABLE ... ALTER COLUMN takes an ACCESS EXCLUSIVE lock, but it is a catalogue-only change with no table rewrite, so it is brief and cannot leave a half-built index behind.

-- WHY THIS EXISTS
-- --------------
-- 0001 declared:
--
--     enabled_at timestamptz NOT NULL DEFAULT now(),
--
-- and the DEFAULT is the bug. Every row INSERTed into user_2fa was therefore
-- marked enabled at insert time, before the user had done anything to confirm
-- they had actually saved their authenticator.
--
-- The pending/enabled distinction the entire 2FA flow is built on did not exist
-- on the database. `TFARepo.UpsertSecret` looks correct at a glance:
--
--     ON CONFLICT (user_id) DO UPDATE SET ... enabled_at = NULL
--
-- which does clear the flag — but only on the CONFLICT branch, i.e. only when a
-- row already existed. A first enrollment took the INSERT branch and inherited
-- DEFAULT now(). So re-enrolling produced a correctly pending factor and
-- enrolling for the first time produced an enabled one, which is the opposite of
-- what anyone reading the repository layer would conclude.
--
-- WHAT THAT BROKE
-- ---------------
-- 1. `Confirm2FA` refuses when the factor is already enabled, so a first-time
--    enrollment could never be confirmed: the user generated a secret, scanned
--    it, and was then rejected as "2FA is already enabled".
--
-- 2. Abandoning enrollment halfway - closing the tab after the QR code, a failed
--    Confirm2FA, a user who never got as far as scanning - left 2FA genuinely
--    ACTIVE with a secret that was never confirmed and may never have been
--    stored. That account could no longer be reached by password alone, and
--    recovery depended entirely on codes the user had never been shown.
--
-- This was found by the 2FA integration tests, which had never run in CI or on
-- this machine because TEST_DATABASE_URL was unset everywhere they were
-- introduced. The behaviour was wrong from the first commit of the schema.

-- The flag is now written only by TFARepo.Enable, which Confirm2FA calls with a
-- fresh TOTP code. NOT NULL has to go as well as the DEFAULT: an INSERT that
-- omits the column under a NOT NULL constraint and no default fails outright,
-- which would break enrollment rather than fix it.
ALTER TABLE user_2fa ALTER COLUMN enabled_at DROP DEFAULT;
ALTER TABLE user_2fa ALTER COLUMN enabled_at DROP NOT NULL;

-- REPAIR EXISTING ROWS
-- --------------------
-- Every row written while the DEFAULT existed carries enabled_at = its insert
-- time. The ones that are genuinely enabled are identifiable without guessing:
-- Confirm2FA sets `enabled_at` and `recovery_codes_hash` in the same UPDATE, so a
-- factor that was never confirmed has no recovery codes.
--
-- Only those rows are cleared. Clearing all of them would disable 2FA for every
-- enrolled account, which is a far worse outage than the bug being fixed.
UPDATE user_2fa
   SET enabled_at = NULL
 WHERE enabled_at IS NOT NULL
   AND (recovery_codes_hash IS NULL OR recovery_codes_hash = '[]'::jsonb);

-- Assert the repair, because a silently mis-scoped UPDATE here is invisible until
-- a locked-out user reports it. Every still-enabled factor must have recovery
-- codes; every factor without them must now read as pending.
DO $assertions$
DECLARE
    v_orphan int;
    v_total  int;
    v_on     int;
BEGIN
    SELECT count(*) INTO v_total  FROM user_2fa;
    SELECT count(*) INTO v_on     FROM user_2fa WHERE enabled_at IS NOT NULL;
    SELECT count(*) INTO v_orphan FROM user_2fa
     WHERE enabled_at IS NOT NULL
       AND (recovery_codes_hash IS NULL OR recovery_codes_hash = '[]'::jsonb);

    IF v_orphan > 0 THEN
        RAISE EXCEPTION
            '0047 FAIL: % of % enabled factors have no recovery codes, so they were never confirmed and should not be enabled',
            v_orphan, v_total;
    END IF;

    RAISE NOTICE '0047: % factors, % now correctly read as pending, % still enabled',
        v_total, v_total - v_on, v_on;
END $assertions$;