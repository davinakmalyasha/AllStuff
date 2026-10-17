-- Functional test for the 0030_open_now.sql special-hours logic.
--
-- Run with:
--   psql -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_open_now.sql
--
-- WHY THIS FILE ASSERTS INSTEAD OF PRINTING
-- -------------------------------------------
-- The previous version of this file SELECTed each case into a column named
-- `must_be_true` / `must_be_false` and left it to a human to read the output.
-- That is not a test: a regression printed `f` into a log line nobody read, and
-- the harness was wired into nothing, so it was not running in CI at all.
--
-- Every case below now asserts with RAISE EXCEPTION inside a transaction that
-- is rolled back, so a failure is loud and CI can gate on it.
--
-- WHAT CONVERTING IT REVEALED
-- ----------------------------
-- The print-only version passed a MONDAY-ONLY hours object
-- (`{"mon":{"open":...}}`) to nearly every case. `biz_is_open_now` looks up
-- TODAY's day name, so on any day except Monday those objects have no entry for
-- today and the function correctly returns false. Every "must_be_true" case was
-- therefore only ever exercised on Mondays, and every "must_be_false" case
-- passed for the wrong reason — not because the logic was right, but because
-- the fixture was empty. The section-A case that exists specifically to catch a
-- silent timezone-to-UTC fallback was among them.
--
-- That is the argument for assertions over printed output: the bug was in the
-- test, and printing hid it for the life of the file. The `open_all_days()`
-- helper below makes a day-partial fixture impossible to write by accident, and
-- A0 asserts the invariant directly.
--
-- DESIGN NOTE — why these use ABSOLUTE expectations
-- -------------------------------------------------
-- `biz_is_open_now` is STABLE and reads now(), so the "today" branch is
-- exercised by keying every override on to_char(now() AT TIME ZONE tz).
--
-- An earlier version asserted only EQUALITY between the override and the
-- no-override results. That style passes for the wrong reasons: when a bug made
-- the function fall back to UTC on every call, every equality assertion still
-- held while the absolute answer was wrong. Section A is the assertion that
-- catches that, and it compares against an independently computed expectation
-- rather than a hardcoded boolean.

\pset footer off
\echo === 0030 open_now contract ===

BEGIN;

-- ---------------------------------------------------------------------------
-- open_all_days(spec) — the same day entry for all seven days.
--
-- Every case in this file must be day-independent: the function resolves the
-- day from now(), so a fixture naming only "mon" silently turns every
-- must-be-true case into a must-be-false case six days out of seven.
-- ---------------------------------------------------------------------------
CREATE FUNCTION open_all_days(spec jsonb) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
    SELECT (SELECT jsonb_object_agg(d, $1)
              FROM unnest(ARRAY['mon','tue','wed','thu','fri','sat','sun']) AS d)
$$;

-- ===========================================================================
-- A. The zone is actually honoured
-- ===========================================================================
-- Regression guard for the bug this migration was partly written to fix: a
-- draft of the timezone validation used a probe function that ALWAYS raised,
-- so the exception handler silently replaced the zone with UTC on every call.
-- Open-now was then computed in UTC for every non-UTC business, with no error
-- anywhere.
DO $A$
DECLARE
    -- numeric, not int. `extract(...)::int / 60` is INTEGER division in Postgres
    -- and always yields 0, so the minutes in the original expression contributed
    -- nothing at all. The boundaries here are whole hours, so the answers it
    -- produced were accidentally right, but the code did not do what it read as
    -- doing and would have kept being "right" only until a boundary moved.
    v_jkt_hour   numeric;
    v_utc_hour   numeric;
    v_expect     boolean;
    v_utc_expect boolean;
    v_got        boolean;
    v_utc_got    boolean;
BEGIN
    v_jkt_hour := extract(hour FROM now() AT TIME ZONE 'Asia/Jakarta')
                + extract(minute FROM now() AT TIME ZONE 'Asia/Jakarta') / 60.0;
    v_utc_hour := extract(hour FROM now() AT TIME ZONE 'UTC')
                + extract(minute FROM now() AT TIME ZONE 'UTC') / 60.0;

    -- The comparison target: is Jakarta's own wall clock inside 09:00-17:00?
    v_expect := (v_jkt_hour >= 9 AND v_jkt_hour < 17);

    -- A 24h window is open under either zone, so it cannot detect a fallback.
    -- A 09:00-17:00 window CAN, because the answer differs between Jakarta and
    -- UTC for most of the day. If the zone were silently replaced by UTC, the
    -- function's answer would follow UTC's clock and disagree with this.
    v_got := biz_is_open_now(open_all_days('{"open":"09:00","close":"17:00","closed":false}'),
                             '{}'::jsonb, 'Asia/Jakarta');
    IF v_got IS DISTINCT FROM v_expect THEN
        RAISE EXCEPTION
            'A1 FAIL: timezone not honoured. Jakarta local hour is % so the answer should be %, but biz_is_open_now said %',
            v_jkt_hour, v_expect, v_got;
    END IF;

    IF biz_is_open_now(open_all_days('{"open":"00:00","close":"23:59"}'),
                       '{}'::jsonb, 'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'A2 FAIL: an all-day window must always be open; the function is broken outright';
    END IF;

    -- A window that is CLOSED all day must be closed, which is what proves the
    -- function is genuinely reading the hours rather than always answering true.
    IF biz_is_open_now(open_all_days('{"closed":true}'), '{}'::jsonb, 'Asia/Jakarta') IS NOT FALSE THEN
        RAISE EXCEPTION 'A3 FAIL: an all-day-closed window must be closed';
    END IF;

    -- Is the SAME hours object open in UTC? Checked against UTC's own clock, so
    -- this holds at every hour of the day.
    --
    -- This replaced an assertion that the two zones DISAGREE, guarded by a
    -- hand-derived "Jakarta 09:00-17:00 implies UTC is closed". That is false for
    -- Jakarta's 16:00 hour, when UTC is 09:00 and the same window is OPEN there -
    -- so the suite raised A4 and failed CI for one hour of every day, 09:00 to
    -- 09:59 UTC, and was green the rest of the time. A gate whose verdict depends
    -- on when it is pushed is worse than no gate: it teaches people to re-run.
    --
    -- Asserting each zone against its own clock is also strictly stronger than
    -- asserting they differ. Differing was only evidence about the pair; this
    -- pins both readings, so a silent fallback to UTC fails A1, and a silent
    -- fallback to Jakarta fails A4, at every hour rather than during a window
    -- someone chose by hand.
    v_utc_expect := (v_utc_hour >= 9 AND v_utc_hour < 17);
    v_utc_got := biz_is_open_now(open_all_days('{"open":"09:00","close":"17:00","closed":false}'),
                                 '{}'::jsonb, 'UTC');
    IF v_utc_got IS DISTINCT FROM v_utc_expect THEN
        RAISE EXCEPTION
            'A4 FAIL: UTC local hour is % so the answer should be %, but biz_is_open_now said %',
            v_utc_hour, v_utc_expect, v_utc_got;
    END IF;

    RAISE NOTICE 'A ok (Jakarta hour %, expected %, got %; UTC hour %, expected %, got %)',
        v_jkt_hour, v_expect, v_got, v_utc_hour, v_utc_expect, v_utc_got;
END $A$;

-- ===========================================================================
-- B. Special-hours overrides
-- ===========================================================================
DO $B$
DECLARE
    v_today   text := to_char(now() AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD');
    v_open    jsonb := open_all_days('{"open":"00:00","close":"23:59"}');
    v_closed  jsonb := open_all_days('{"closed":true}');
    v_0900    jsonb := open_all_days('{"open":"09:00","close":"17:00","closed":false}');
BEGIN
    -- A closed override must beat an open weekly entry.
    IF biz_is_open_now(v_0900, jsonb_build_object(v_today, '{"closed":true}'::jsonb),
                       'Asia/Jakarta') IS NOT FALSE THEN
        RAISE EXCEPTION 'B1 FAIL: a closed override must beat an open weekly entry';
    END IF;

    -- An override must open a normally-closed day.
    IF biz_is_open_now(v_closed, jsonb_build_object(v_today, '{"open":"00:00","close":"23:59"}'::jsonb),
                       'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'B2 FAIL: an override must open a normally-closed day';
    END IF;

    -- An override outside its own window must not apply.
    IF biz_is_open_now(v_open, jsonb_build_object(v_today, '{"open":"03:00","close":"03:01"}'::jsonb),
                       'Asia/Jakarta') IS NOT FALSE THEN
        RAISE EXCEPTION 'B3 FAIL: an override outside its own window must not apply';
    END IF;

    -- The overnight-rescue guard: an explicit "closed today" is final, so the
    -- previous day's still-open window must not resurrect the business.
    IF biz_is_open_now(v_open, jsonb_build_object(v_today, '{"closed":true}'::jsonb),
                       'Asia/Jakarta') IS NOT FALSE THEN
        RAISE EXCEPTION 'B4 FAIL: a closed override must be final; the previous-day overnight window rescued it';
    END IF;

    -- An override keyed on a different date must not apply today.
    IF biz_is_open_now(v_closed, '{"1999-01-01":{"open":"00:00","close":"23:59"}}'::jsonb,
                       'Asia/Jakarta') IS NOT FALSE THEN
        RAISE EXCEPTION 'B5 FAIL: an override for another date must not apply today';
    END IF;

    RAISE NOTICE 'B ok (5 override cases)';
END $B$;

-- ===========================================================================
-- C. Malformed input degrades safely
-- ===========================================================================
-- Every case here is one where the wrong answer is a business silently
-- disappearing from "Open now" search while it is trading. That is the most
-- expensive class of bug in this function, because it is invisible from the UI.
DO $C$
DECLARE
    v_today text := to_char(now() AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD');
    v_open  jsonb := open_all_days('{"open":"00:00","close":"23:59"}');
BEGIN
    -- An unusable override falls back to the weekly entry rather than to
    -- "closed", so a pre-validator row cannot make a business invisible.
    IF biz_is_open_now(v_open, jsonb_build_object(v_today, '{"open":"nope"}'::jsonb),
                       'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'C1 FAIL: an unusable override must fall back to the weekly entry';
    END IF;

    -- An unknown zone must not RAISE: raising here aborts the entire search
    -- query rather than one row.
    IF biz_is_open_now(v_open, '{}'::jsonb, 'Not/AZone') IS NOT TRUE THEN
        RAISE EXCEPTION 'C2 FAIL: an invalid timezone must not raise or change the answer';
    END IF;

    IF biz_is_open_now('{}'::jsonb, '{}'::jsonb, 'Asia/Jakarta') IS NOT FALSE THEN
        RAISE EXCEPTION 'C3 FAIL: empty hours with no override is closed, not open';
    END IF;

    IF biz_is_open_now(NULL, jsonb_build_object(v_today, '{"open":"00:00","close":"23:59"}'::jsonb),
                       'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'C4 FAIL: NULL weekly hours with a usable override must still be evaluated';
    END IF;

    -- The 2-arg wrapper exists so a half-applied rolling deploy fails loudly at
    -- PLAN time rather than silently computing the wrong thing.
    IF biz_is_open_now(v_open, 'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'C5 FAIL: the 2-arg back-compat wrapper is broken';
    END IF;

    -- A NULL override must behave exactly like an empty one, not like a
    -- malformed one.
    IF biz_is_open_now(v_open, NULL, 'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'C6 FAIL: a NULL override must behave like no override';
    END IF;

    RAISE NOTICE 'C ok (6 malformed-input cases)';
END $C$;

-- ===========================================================================
-- D. The COALESCE regressions
-- ===========================================================================
-- `entry ->> 'closed'` returns NULL when the key is absent. NULL <> 'true' is
-- NULL, and `IF NULL` is false — so WITHOUT the COALESCE in 0030, an entry that
-- omits "closed" is treated as CLOSED and a business that is trading vanishes
-- from open-now search. This is the most valuable fix in 0030, and it is
-- guarded here in both the weekly and the override direction.
DO $D$
DECLARE
    v_today text := to_char(now() AT TIME ZONE 'Asia/Jakarta', 'YYYY-MM-DD');
BEGIN
    IF biz_is_open_now(open_all_days('{"open":"00:00","close":"23:59"}'), '{}'::jsonb, 'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'D1 FAIL: a weekly entry that OMITS "closed" must mean OPEN, not closed';
    END IF;

    IF biz_is_open_now(open_all_days('{"closed":true}'),
                       jsonb_build_object(v_today, '{"open":"00:00","close":"23:59"}'::jsonb),
                       'Asia/Jakarta') IS NOT TRUE THEN
        RAISE EXCEPTION 'D2 FAIL: an override that omits "closed" must also be treated as open';
    END IF;

    RAISE NOTICE 'D ok (2 COALESCE regressions)';
END $D$;

-- ===========================================================================
-- E. Resolver
-- ===========================================================================
DO $E$
DECLARE
    v_entry jsonb;
BEGIN
    v_entry := biz_resolve_day_entry('{"mon":{"open":"09:00","close":"17:00","closed":false}}'::jsonb,
                                      '{"2026-01-01":{"closed":true}}'::jsonb, '2026-01-01', 'mon');
    IF v_entry ->> 'closed' IS DISTINCT FROM 'true' THEN
        RAISE EXCEPTION 'E1 FAIL: the resolver must prefer a usable override, got %', v_entry;
    END IF;

    v_entry := biz_resolve_day_entry('{"mon":{"open":"09:00","close":"17:00","closed":false}}'::jsonb,
                                      '{"2026-01-01":{"open":"garbage"}}'::jsonb, '2026-01-01', 'mon');
    IF v_entry ->> 'open' IS DISTINCT FROM '09:00' THEN
        RAISE EXCEPTION 'E2 FAIL: the resolver must ignore an unusable override and return the weekly row, got %', v_entry;
    END IF;

    IF biz_resolve_day_entry('{}'::jsonb, '{}'::jsonb, '2026-01-01', 'fri') IS NOT NULL THEN
        RAISE EXCEPTION 'E3 FAIL: the resolver must return NULL when neither source has the day';
    END IF;

    RAISE NOTICE 'E ok (3 resolver cases)';
END $E$;

-- All assertions passed. Rolling back so the file leaves no trace on the
-- database it ran against, including the helper function created above.
\echo === all open_now assertions passed (rolling back) ===
ROLLBACK;
