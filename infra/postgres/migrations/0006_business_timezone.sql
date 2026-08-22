-- Open-now must be computed in the business's own timezone (PRD §5.3.4).
ALTER TABLE businesses ADD COLUMN timezone text NOT NULL DEFAULT 'UTC';
