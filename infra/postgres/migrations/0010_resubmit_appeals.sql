-- B1/B3: resubmission cap (PRD §8.2 max 3 attempts), user appeals.
ALTER TABLE businesses ADD COLUMN resubmit_count int NOT NULL DEFAULT 0;

CREATE TABLE appeals (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason     text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 2000),
    status     text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    decided_by uuid REFERENCES users(id),
    decision   text CHECK (decision IN ('approve', 'reject')),
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);

CREATE INDEX idx_appeals_status ON appeals (status, created_at);
