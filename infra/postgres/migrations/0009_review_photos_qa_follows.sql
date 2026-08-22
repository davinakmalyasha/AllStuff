-- B5: review photos + Q&A on business pages.
ALTER TABLE reviews ADD COLUMN image_ids uuid[] NOT NULL DEFAULT '{}';

CREATE TABLE questions (
    id          uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id),
    text        text NOT NULL CHECK (char_length(text) BETWEEN 3 AND 500),
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_questions_business ON questions (business_id, created_at DESC);

CREATE TABLE answers (
    id          uuid PRIMARY KEY,
    question_id uuid NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users(id),
    text        text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 1000),
    is_owner    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_answers_question ON answers (question_id, created_at);

-- B6: follows + owner announcements.
CREATE TABLE follows (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, business_id)
);

CREATE TABLE business_updates (
    id          uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    author_id   uuid NOT NULL REFERENCES users(id),
    title       text NOT NULL CHECK (char_length(title) BETWEEN 3 AND 120),
    body        text NOT NULL CHECK (char_length(body) BETWEEN 10 AND 2000),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_updates_business ON business_updates (business_id, created_at DESC);
