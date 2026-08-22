-- Batch 2: trend-based search sort support + SQL open-now filter (fixes
-- pagination: open-now was applied in Go AFTER LIMIT/OFFSET, under-filling
-- pages). Mirrors the Go isOpenNow semantics exactly (PRD §5.3.4).

CREATE OR REPLACE FUNCTION biz_is_open_now(hours jsonb, tz text) RETURNS boolean
LANGUAGE plpgsql STABLE AS $$
DECLARE
  lt timestamptz := now() AT TIME ZONE tz;
  cur int := extract(hour FROM lt) * 60 + extract(minute FROM lt);
  entry jsonb;
  open_t text; close_t text;
  oh int; ch int;
  day text;
BEGIN
  IF hours IS NULL THEN RETURN false; END IF;

  day := lower(to_char(lt, 'Dy'));
  entry := hours -> day;
  IF entry IS NOT NULL AND (entry ->> 'closed') <> 'true' THEN
    open_t := entry ->> 'open'; close_t := entry ->> 'close';
    IF open_t IS NOT NULL AND close_t IS NOT NULL AND length(open_t) = 5 AND length(close_t) = 5 THEN
      oh := substr(open_t,1,2)::int * 60 + substr(open_t,4,2)::int;
      ch := substr(close_t,1,2)::int * 60 + substr(close_t,4,2)::int;
      IF ch <= oh THEN
        IF cur >= oh OR cur < ch THEN RETURN true; END IF; -- evening + early morning
      ELSE
        IF cur >= oh AND cur < ch THEN RETURN true; END IF;
      END IF;
    END IF;
  END IF;

  -- Previous day's overnight window may cover early today.
  day := lower(to_char(lt - interval '1 day', 'Dy'));
  entry := hours -> day;
  IF entry IS NOT NULL AND (entry ->> 'closed') <> 'true' THEN
    open_t := entry ->> 'open'; close_t := entry ->> 'close';
    IF open_t IS NOT NULL AND close_t IS NOT NULL AND length(open_t) = 5 AND length(close_t) = 5 THEN
      oh := substr(open_t,1,2)::int * 60 + substr(open_t,4,2)::int;
      ch := substr(close_t,1,2)::int * 60 + substr(close_t,4,2)::int;
      IF ch <= oh AND cur < ch THEN RETURN true; END IF;
    END IF;
  END IF;

  RETURN false;
END;
$$;
