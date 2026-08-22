-- Draft businesses start with an empty description; the 50-char minimum is
-- enforced at submission time in the service layer (PRD §8.2), not at insert.
ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_description_check;
