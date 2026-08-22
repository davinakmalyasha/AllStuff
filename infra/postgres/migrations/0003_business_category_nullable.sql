-- Draft businesses have no category until the wizard completes (PRD §8.2:
-- category is a submission-time requirement, not a creation-time one).
ALTER TABLE businesses ALTER COLUMN category_id DROP NOT NULL;
