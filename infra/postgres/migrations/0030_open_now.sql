-- 0030_open_now.sql — special-hours support in the SQL open-now filter.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        CREATE OR REPLACE on both functions plus a DROP/CREATE pair for the 3-arg overload, so a replay converges even if the signature set is already correct.
--
-- WHY THIS FILE EXISTS NOW, RATHER THAN WITH THE REST OF THE PHASE 2 WORK
-- ---------------------------------------------------------------------------
-- `isOpenNow` in services/api/internal/service/search.go was changed to honour
-- `businesses.special_hours` in the same change set. That function computes the
-- `is_open_now` BADGE on each search result, while `biz_is_open_now` powers the
-- `open_now=true` FILTER. If only the Go side is updated, the two disagree: the
-- filter would still include a business that is shut for a public holiday and
-- the badge on that very result would read "Closed". Shipping the Go change
-- without this migration trades one wrong answer for a more confusing one, so
-- the two must land together.
--
-- WHAT WAS WRONG
-- ---------------------------------------------------------------------------
--  * `special_hours` (migration 0012) was read into the domain model,
--    serialised to JSON and typed on the client, and consulted by NOTHING — not
--    by the Go badge and not by the SQL filter. A business marked closed on
--    Christmas was reported open by both.
--  * The SettingsPage editor that writes it was itself a no-op, because
--    `Businesses.Update` built its field map from a hardcoded list of 18 keys
--    that did not include `amenities` or `special_hours`. The repo whitelist
--    accepted the columns, so nothing errored: the values were dropped and the
--    owner saw a success toast. Both halves are fixed (see
--    `validateSpecialHours` in service/businesses.go).
--
-- SEMANTICS (identical in Go and here)
-- ---------------------------------------------------------------------------
--   * The override is keyed on the business's LOCAL date (`lt` in its timezone),
--     never on the UTC date. A business in Auckland must not inherit yesterday's
--     Auckland holiday.
--   * An override REPLACES the weekly entry for that date, in both directions:
--     a `closed` override shuts a normally-open day, and a timed override opens
--     a normally-closed one.
--   * An override that is present but UNUSABLE (bad or missing open/close times)
--     FALLS BACK to the weekly entry. The validator rejects those at write time,
--     but rows predating it must not make a business invisible for a whole day:
--     the open_now filter would drop it and the badge would read "Closed" while
--     the owner is trading.
--   * An overnight window (close <= open) that started on a day with its own
--     override is evaluated against THAT override for the early-morning carry-
--     over. A Friday 22:00–02:00 window whose Friday is marked closed must not
--     leave the business showing open at 01:00 Saturday.
--
-- COMPATIBILITY
-- ---------------------------------------------------------------------------
-- The 2-arg `biz_is_open_now(hours, tz)` is KEPT as a wrapper. Several queries
-- in service/search.go still call the 2-arg form, and replacing the signature
-- outright would break the currently-deployed code during a rolling deploy
-- (old replicas must keep working against the new schema). The 3-arg overload
-- is the real implementation; the wrapper delegates with a NULL override, which
-- reproduces the previous behaviour exactly.

-- ---------------------------------------------------------------------------
-- helper: resolve the entry to use for a given local date
--
-- Returns the special-hours override when one exists AND is usable, otherwise
-- the weekly entry for that day, otherwise NULL.
--
-- A single jsonb return value rather than an OUT-parameter row: a multi-OUT
-- plpgsql function assigned into a scalar variable resolves as a record, and
-- plpgsql then fails to cast it (SQLSTATE 22P02 "Token "(" is invalid"), which
-- is a compile-time-shaped trap that only fires at execution.
--
-- Declared IMMUTABLE: it reads only its arguments.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION biz_resolve_day_entry(
    hours      jsonb,
    special    jsonb,
    day_key    text,
    day_name   text
) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE
    -- Usable override: either it explicitly closes the day, or it carries a
    -- parseable open/close pair.
    WHEN special IS NOT NULL
     AND jsonb_typeof(special) = 'object'
     AND special -> day_key IS NOT NULL
     AND jsonb_typeof(special -> day_key) = 'object'
     AND (
          COALESCE(special -> day_key ->> 'closed', 'false') = 'true'
          OR (
               special -> day_key ->> 'open'  IS NOT NULL
           AND special -> day_key ->> 'close' IS NOT NULL
           AND length(special -> day_key ->> 'open')  = 5
           AND length(special -> day_key ->> 'close') = 5
          )
     )
      THEN special -> day_key
    -- Present but unusable (bad/missing times): fall through to the weekly row.
    WHEN hours IS NOT NULL AND jsonb_typeof(hours) = 'object'
      THEN hours -> day_name
    ELSE NULL
  END;
$$;

-- ---------------------------------------------------------------------------
-- biz_is_open_now(hours, special, tz) — the real implementation
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION biz_is_open_now(hours jsonb, special jsonb, tz text)
RETURNS boolean
LANGUAGE plpgsql STABLE AS $$
DECLARE
  -- lt / cur are deliberately NOT given DECLARE defaults. A default in DECLARE
  -- is evaluated before the BEGIN block, so it runs BEFORE the timezone
  -- validation below and `now() AT TIME ZONE 'Bad/Zone'` raises — aborting the
  -- entire search query rather than one row. Assigning after validation is what
  -- makes the guard meaningful.
  lt timestamptz;
  cur int;
  entry jsonb;
  open_t text; close_t text;
  oh int; ch int;
  day_name text;
  day_key  text;
  prev_key text;
BEGIN
  -- Validate the zone BY TRYING THE CONVERSION, not by calling a probe
  -- function. An earlier draft used `PERFORM timezone(tz)`; on a Postgres build
  -- without `timezone(text)` that call raises EVERY time, so the handler
  -- silently replaced the zone with UTC on every call — meaning open-now was
  -- computed in UTC for every business in a non-UTC timezone, with no error
  -- anywhere. Guarding the actual `AT TIME ZONE` expression is both simpler and
  -- exactly right: it validates the only operation that can fail.
  BEGIN
    lt := now() AT TIME ZONE tz;
  EXCEPTION WHEN OTHERS THEN
    lt := now() AT TIME ZONE 'UTC';
  END;
  cur := extract(hour FROM lt) * 60 + extract(minute FROM lt);

  IF hours IS NULL AND special IS NULL THEN RETURN false; END IF;

  day_name := lower(to_char(lt, 'Dy'));
  day_key  := to_char(lt, 'YYYY-MM-DD');
  prev_key := to_char(lt - interval '1 day', 'YYYY-MM-DD');

  -- --- today ---
  entry := biz_resolve_day_entry(hours, special, day_key, day_name);
  IF entry IS NOT NULL THEN
    -- COALESCE is load-bearing. `entry ->> 'closed'` returns NULL when the key
    -- is ABSENT, and `NULL <> 'true'` is NULL — which `IF` treats as false — so
    -- without this an entry that simply omits "closed" would be treated as
    -- CLOSED and the business would vanish from open_now search while trading.
    -- The Go twin does `if closed, _ := entry["closed"].(bool); closed`, i.e.
    -- absent means open. The original 0014 function had this bug; it only went
    -- unnoticed because every writer happened to emit the key explicitly.
    IF COALESCE(entry ->> 'closed', 'false') <> 'true' THEN
      open_t := entry ->> 'open'; close_t := entry ->> 'close';
      IF open_t IS NOT NULL AND close_t IS NOT NULL
         AND length(open_t) = 5 AND length(close_t) = 5 THEN
        oh := substr(open_t,1,2)::int * 60 + substr(open_t,4,2)::int;
        ch := substr(close_t,1,2)::int * 60 + substr(close_t,4,2)::int;
        IF ch <= oh THEN
          IF cur >= oh OR cur < ch THEN RETURN true; END IF; -- evening + early morning
        ELSE
          IF cur >= oh AND cur < ch THEN RETURN true; END IF;
        END IF;
      END IF;
    ELSE
      -- Explicitly closed today. A previous-day overnight window must NOT
      -- resurrect it: "closed" is final for the date it applies to.
      RETURN false;
    END IF;
  END IF;

  -- --- previous day: only its overnight window can cover early today ---
  -- The day NAME comes from the shifted timestamp; the override KEY must be the
  -- previous LOCAL date, not the previous day-name.
  entry := biz_resolve_day_entry(
             hours, special, prev_key, lower(to_char(lt - interval '1 day', 'Dy')));

  IF entry IS NOT NULL AND COALESCE(entry ->> 'closed', 'false') <> 'true' THEN
    open_t := entry ->> 'open'; close_t := entry ->> 'close';
    IF open_t IS NOT NULL AND close_t IS NOT NULL
       AND length(open_t) = 5 AND length(close_t) = 5 THEN
      oh := substr(open_t,1,2)::int * 60 + substr(open_t,4,2)::int;
      ch := substr(close_t,1,2)::int * 60 + substr(close_t,4,2)::int;
      IF ch <= oh AND cur < ch THEN RETURN true; END IF;
    END IF;
  END IF;

  RETURN false;
END;
$$;

-- ---------------------------------------------------------------------------
-- 2-arg back-compat wrapper.
--
-- Signature overloading requires the new function to have a different argument
-- list, which it does. This wrapper keeps every currently-deployed 2-arg call
-- working and reproduces the old semantics exactly (no override).
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION biz_is_open_now(hours jsonb, tz text)
RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT biz_is_open_now($1, NULL, $2);
$$;

-- ---------------------------------------------------------------------------
-- Rewrite the search predicate to pass special_hours through.
--
-- Only the function's arguments change; the surrounding predicate shape is
-- untouched, so this is a textual swap on the call site rather than a
-- statement rewrite.
-- ---------------------------------------------------------------------------
--
-- EXPECTED FOLLOW-UP (in services/api/internal/service/search.go, not applied
-- here because it must land with the Go badge change reviewed together):
--
--   -  WHERE biz_is_open_now(b.hours, b.timezone)
--   +  WHERE biz_is_open_now(b.hours, b.special_hours, b.timezone)
--
-- A 3-arg call against a 2-arg schema fails at plan time, not silently, so a
-- half-applied change is loud rather than wrong.
