-- BizVerse initial schema (PRD §7, Decisions Log D1-D8)
-- Migration 0001: full platform — directory, storefront, engagement, messaging, admin
-- Conventions: UUIDv7 PKs generated in application code; created_at/updated_at everywhere;
-- enums as native types; JSONB for flexible blocks; soft-delete via deleted_at where noted.

CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS cube;
CREATE EXTENSION IF NOT EXISTS earthdistance; -- ll_to_earth for radius queries (PRD §7.6)
-- CREATE EXTENSION IF NOT EXISTS postgis; -- enabled in environments where PostGIS is provisioned

-- =====================================================================
-- Accounts & auth (PRD §5.9, §7.1)
-- =====================================================================

CREATE TYPE user_status AS ENUM ('active', 'suspended', 'banned');
CREATE TYPE user_role AS ENUM ('user', 'admin'); -- owner is derived from businesses (PRD §5.9.3)

CREATE TABLE users (
    id                uuid PRIMARY KEY,            -- UUIDv7 from app
    email             citext NOT NULL UNIQUE,
    password_hash     text,                        -- NULL for OAuth-only accounts (argon2id)
    name              text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    username          text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_]{3,30}$'),
    avatar_url        text,
    bio               text CHECK (bio IS NULL OR char_length(bio) <= 300),
    timezone          text NOT NULL DEFAULT 'UTC',
    profile_links     jsonb NOT NULL DEFAULT '{}'::jsonb, -- {website, instagram, ...}
    email_verified_at timestamptz,
    role              user_role NOT NULL DEFAULT 'user',
    status            user_status NOT NULL DEFAULT 'active',
    suspended_until   timestamptz,
    ban_reason        text,
    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_status ON users (status);
CREATE INDEX idx_users_username_trgm ON users USING gin (username gin_trgm_ops);

CREATE TABLE sessions ( -- refresh-token registry (PRD §5.9.1)
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   text NOT NULL UNIQUE,             -- sha256 of refresh token
    ip           inet,
    user_agent   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz
);

CREATE INDEX idx_sessions_user ON sessions (user_id);

CREATE TABLE user_2fa ( -- TOTP (PRD §5.9.1)
    id                    uuid PRIMARY KEY,
    user_id               uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    totp_secret_encrypted text NOT NULL,           -- encrypted at rest (PRD §9.3)
    enabled_at            timestamptz NOT NULL DEFAULT now(),
    recovery_codes_hash   jsonb NOT NULL,          -- 10 hashed, single-use
    last_used_at          timestamptz
);

CREATE TABLE push_subscriptions ( -- Web Push VAPID (PRD §5.5.3)
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint     text NOT NULL UNIQUE,
    keys         jsonb NOT NULL,                  -- {p256dh, auth}
    user_agent   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_push_subscriptions_user ON push_subscriptions (user_id);

CREATE TABLE auth_events ( -- audit (PRD §9.3)
    id         uuid PRIMARY KEY,
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    event      text NOT NULL, -- login|login_fail|register|password_reset|logout|ban|suspend|unban|2fa_enable|2fa_disable|session_revoke|document_view
    ip         inet,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_auth_events_user ON auth_events (user_id, created_at DESC);

-- =====================================================================
-- Directory core (PRD §7.1)
-- =====================================================================

CREATE TABLE categories (
    id          uuid PRIMARY KEY,
    parent_id   uuid REFERENCES categories(id) ON DELETE CASCADE, -- NULL = top-level
    name        text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
    slug        text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9-]+$'),
    icon        text NOT NULL,
    description text,
    sort_order  int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (parent_id, name)
);

CREATE TYPE business_status AS ENUM ('draft', 'pending_review', 'verified', 'rejected', 'suspended', 'paused', 'closed');
CREATE TYPE verification_level AS ENUM ('verified', 'fully_verified');

CREATE TABLE businesses (
    id                    uuid PRIMARY KEY,
    owner_id              uuid NOT NULL REFERENCES users(id),
    name                  text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 80),
    slug                  text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9-]+$'),
    tagline               text CHECK (tagline IS NULL OR char_length(tagline) <= 120),
    description           text NOT NULL CHECK (char_length(description) >= 50),
    category_id           uuid NOT NULL REFERENCES categories(id),
    status                business_status NOT NULL DEFAULT 'draft',
    rejection_reason      text,
    logo_url              text,
    cover_url             text,
    gallery               jsonb NOT NULL DEFAULT '[]'::jsonb,   -- ordered media ids
    price_level           smallint CHECK (price_level BETWEEN 0 AND 4),
    currency              char(3) NOT NULL DEFAULT 'USD',
    address               text NOT NULL,
    lat                   double precision NOT NULL,
    lng                   double precision NOT NULL,
    city                  text NOT NULL,
    country               text NOT NULL,
    hours                 jsonb NOT NULL DEFAULT '{}'::jsonb,   -- per-day open/close + flags (PRD §8.2)
    contact               jsonb NOT NULL DEFAULT '{}'::jsonb,   -- phone, email, website, whatsapp, socials map (PRD §5.3.5)
    tags                  text[] NOT NULL DEFAULT '{}',
    founded_year          smallint CHECK (founded_year IS NULL OR founded_year BETWEEN 1800 AND 2100),
    is_featured           boolean NOT NULL DEFAULT false,
    featured_order        int,
    theme                 jsonb NOT NULL DEFAULT '{}'::jsonb,   -- draft theme (template, colors, fonts)
    layout                jsonb NOT NULL DEFAULT '{}'::jsonb,   -- draft layout (ordered sections + config)
    highlighted_product_ids uuid[] NOT NULL DEFAULT '{}',
    last_published_at     timestamptz,
    published_snapshot    jsonb,                                -- published theme/layout (PRD §10.5)
    verified_at           timestamptz,
    verification_level    verification_level,
    deleted_at            timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_businesses_slug ON businesses (slug);
CREATE INDEX idx_businesses_category ON businesses (category_id);
CREATE INDEX idx_businesses_status ON businesses (status);
CREATE INDEX idx_businesses_city ON businesses (city);
CREATE INDEX idx_businesses_featured ON businesses (is_featured, featured_order);
CREATE INDEX idx_businesses_geo ON businesses USING gist (ll_to_earth(lat, lng));
CREATE INDEX idx_businesses_name_trgm ON businesses USING gin (name gin_trgm_ops);

CREATE TABLE business_invites ( -- co-owners (PRD §5.9.3)
    id           uuid PRIMARY KEY,
    business_id  uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    email        citext NOT NULL,
    role         text NOT NULL DEFAULT 'co_owner' CHECK (role IN ('co_owner', 'viewer')),
    token        text NOT NULL UNIQUE,
    invited_by   uuid NOT NULL REFERENCES users(id),
    accepted_at  timestamptz,
    expires_at   timestamptz NOT NULL DEFAULT now() + interval '7 days',
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_business_invites_business ON business_invites (business_id);

-- Verification documents (PRD §5.8.1, §7.4) — sensitive: encrypted storage, access-audited
CREATE TABLE verification_documents (
    id          uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('registration', 'license', 'tax_id', 'identity', 'utility')),
    media_id    uuid NOT NULL,                                  -- references media(kind=document_verification)
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    reviewer_id uuid REFERENCES users(id),
    review_note text,
    reviewed_at timestamptz,
    expires_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_verification_documents_business ON verification_documents (business_id, status);

CREATE TABLE verification_document_views ( -- append-only access audit (PRD §9.3)
    id          uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES verification_documents(id) ON DELETE CASCADE,
    admin_id    uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_vdoc_views_document ON verification_document_views (document_id);

-- =====================================================================
-- Products & variants (PRD §5.4.3, §7.1)
-- =====================================================================

CREATE TYPE product_type AS ENUM ('product', 'service');

CREATE TABLE products (
    id             uuid PRIMARY KEY,
    business_id    uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    type           product_type NOT NULL DEFAULT 'product',
    name           text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 100),
    description    text,
    currency       char(3) NOT NULL DEFAULT 'USD',
    base_price     numeric(12,2) CHECK (base_price IS NULL OR base_price >= 0), -- option-less products only
    call_for_price boolean NOT NULL DEFAULT false,             -- services only (PRD §8.3)
    cover_image_id uuid,
    image_ids      uuid[] NOT NULL DEFAULT '{}',               -- ordered, 1-10 to publish
    tags           text[] NOT NULL DEFAULT '{}',
    is_available   boolean NOT NULL DEFAULT true,
    is_featured    boolean NOT NULL DEFAULT false,
    featured_order int,
    sort_order     int NOT NULL DEFAULT 0,
    is_published   boolean NOT NULL DEFAULT false,             -- draft vs live (PRD §8.3)
    badge          text NOT NULL DEFAULT 'none' CHECK (badge IN ('none', 'new', 'popular')),
    seo_title      text,
    deleted_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_business ON products (business_id, is_published);

CREATE TABLE product_options ( -- e.g. Size: S/M/L (PRD §5.4.3)
    id         uuid PRIMARY KEY,
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    values     text[] NOT NULL,                                -- max 20 values
    sort_order int NOT NULL DEFAULT 0
);

CREATE INDEX idx_product_options_product ON product_options (product_id);

CREATE TABLE product_variants ( -- combinations with own price/stock/image (PRD §5.4.3)
    id         uuid PRIMARY KEY,
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    name       text NOT NULL,
    sku        text NOT NULL UNIQUE,
    options    jsonb NOT NULL,                                 -- {optionName: value} — UNIQUE per product
    price      numeric(12,2) CHECK (price IS NULL OR price >= 0),
    currency   char(3) NOT NULL DEFAULT 'USD',
    stock_qty  int CHECK (stock_qty IS NULL OR stock_qty >= 0),
    in_stock   boolean NOT NULL DEFAULT true,
    image_id   uuid,
    sort_order int NOT NULL DEFAULT 0,
    UNIQUE (product_id, options)
);

CREATE INDEX idx_product_variants_product ON product_variants (product_id);

-- =====================================================================
-- Engagement (PRD §5.6, §7.2)
-- =====================================================================

CREATE TABLE reviews (
    id             uuid PRIMARY KEY,
    business_id    uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    product_id     uuid REFERENCES products(id) ON DELETE CASCADE, -- NULL = business review
    user_id        uuid NOT NULL REFERENCES users(id),
    rating         smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    text           text NOT NULL CHECK (char_length(text) BETWEEN 10 AND 2000),
    reply          text,
    reply_at       timestamptz,
    reply_edited_at timestamptz,
    status         text NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden')),
    hidden_by      uuid REFERENCES users(id),
    hidden_reason  text,
    edited_at      timestamptz,
    deleted_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_reviews_unique_business ON reviews (business_id, user_id) WHERE product_id IS NULL;
CREATE UNIQUE INDEX idx_reviews_unique_product ON reviews (product_id, user_id) WHERE product_id IS NOT NULL;

CREATE INDEX idx_reviews_business ON reviews (business_id, status);
CREATE INDEX idx_reviews_product ON reviews (product_id, status);
CREATE INDEX idx_reviews_user ON reviews (user_id);

CREATE TABLE review_helpful_votes ( -- up/down (PRD §5.6.2)
    id         uuid PRIMARY KEY,
    review_id  uuid NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id),
    vote       smallint NOT NULL CHECK (vote IN (1, -1)),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (review_id, user_id)
);

CREATE TABLE comments ( -- nested any depth (PRD §5.6.2)
    id         uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id),
    parent_id  uuid REFERENCES comments(id) ON DELETE CASCADE,
    text       text NOT NULL CHECK (char_length(text) <= 500),
    status     text NOT NULL DEFAULT 'visible' CHECK (status IN ('visible', 'hidden')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_comments_business ON comments (business_id, parent_id);
CREATE INDEX idx_comments_recent ON comments (business_id, created_at DESC);

CREATE TABLE comment_likes (
    id         uuid PRIMARY KEY,
    comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (comment_id, user_id)
);

CREATE TABLE likes (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id),
    target_type text NOT NULL CHECK (target_type IN ('business', 'product')),
    target_id   uuid NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, target_type, target_id)
);

CREATE TABLE recommends (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id),
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, business_id)
);

CREATE TABLE collections ( -- replaces favorites: organized lists (PRD D3, §7.2)
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    slug          text NOT NULL CHECK (slug ~ '^[a-z0-9-]+$'),
    description   text,
    is_public     boolean NOT NULL DEFAULT false,
    cover_item_id uuid,
    sort_order    int NOT NULL DEFAULT 0,
    is_default    boolean NOT NULL DEFAULT false, -- "Favorites" auto-created
    deleted_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, slug)
);

CREATE INDEX idx_collections_user ON collections (user_id);

CREATE TABLE collection_items (
    id            uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    target_type   text NOT NULL CHECK (target_type IN ('business', 'product')),
    target_id     uuid NOT NULL,
    note          text CHECK (note IS NULL OR char_length(note) <= 200),
    sort_order    int NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (collection_id, target_type, target_id)
);

CREATE INDEX idx_collection_items_collection ON collection_items (collection_id);

-- Append-only engagement feed for trending (PRD §5.6.3, §7.2)
CREATE TABLE engagement_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id),
    target_type text NOT NULL CHECK (target_type IN ('business', 'product')),
    target_id   uuid NOT NULL,
    signal      text NOT NULL CHECK (signal IN ('view', 'like', 'recommend', 'comment', 'review', 'collection_save', 'chat_start')),
    weight      int NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    dedupe_key  text NOT NULL UNIQUE, -- user+target+signal+day (PRD §8.6)
    flagged     boolean NOT NULL DEFAULT false -- burst-anomaly flag (PRD §8.6)
);

CREATE INDEX idx_engagement_events_time ON engagement_events (occurred_at);
CREATE INDEX idx_engagement_events_target ON engagement_events (target_type, target_id);

-- =====================================================================
-- Messaging (PRD §5.5, §7.3)
-- =====================================================================

CREATE TYPE thread_type AS ENUM ('direct', 'business');

CREATE TABLE chat_threads (
    id              uuid PRIMARY KEY,
    type            thread_type NOT NULL,
    business_id     uuid REFERENCES businesses(id) ON DELETE CASCADE, -- NULL for direct
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed', 'left')),
    closed_by       uuid REFERENCES users(id),
    closed_at       timestamptz,
    last_message_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_threads_business ON chat_threads (business_id, last_message_at DESC);

CREATE TABLE chat_participants (
    id                    uuid PRIMARY KEY,
    thread_id             uuid NOT NULL REFERENCES chat_threads(id) ON DELETE CASCADE,
    user_id               uuid NOT NULL REFERENCES users(id),
    role                  text NOT NULL CHECK (role IN ('user', 'owner', 'admin')),
    last_read_message_id  bigint,
    muted_until           timestamptz,
    pinned_message_ids    uuid[] NOT NULL DEFAULT '{}', -- max 5 (PRD §5.5.2)
    notification_prefs    jsonb NOT NULL DEFAULT '{}'::jsonb, -- email on/off/throttled
    left_at               timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (thread_id, user_id)
);

CREATE INDEX idx_chat_participants_user ON chat_participants (user_id, last_read_message_id);

CREATE TYPE message_type AS ENUM ('text', 'image', 'file', 'audio', 'video', 'link', 'system');

CREATE TABLE chat_messages (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- monotonic for pagination (PRD §7.3)
    thread_id                uuid NOT NULL REFERENCES chat_threads(id) ON DELETE CASCADE,
    sender_id                uuid NOT NULL REFERENCES users(id),
    sender_role              text NOT NULL CHECK (sender_role IN ('user', 'owner', 'admin')),
    type                     message_type NOT NULL DEFAULT 'text',
    body                     text CHECK (body IS NULL OR char_length(body) <= 4000),
    reply_to_id              bigint REFERENCES chat_messages(id) ON DELETE SET NULL,
    forwarded_from_message_id bigint REFERENCES chat_messages(id) ON DELETE SET NULL,
    media_id                 uuid, -- references media
    link_preview             jsonb, -- {url, title, description, image} (PRD §5.5.2)
    client_msg_id            uuid NOT NULL,
    read_count               int NOT NULL DEFAULT 0,
    edited_at                timestamptz,
    edit_history             jsonb NOT NULL DEFAULT '[]'::jsonb, -- [{text, at}]
    deleted_for              text NOT NULL DEFAULT 'none' CHECK (deleted_for IN ('none', 'me', 'everyone')),
    deleted_at               timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (thread_id, client_msg_id) -- at-least-once dedupe (PRD §5.5.3)
);

CREATE INDEX idx_chat_messages_thread ON chat_messages (thread_id, id DESC);
CREATE INDEX idx_chat_messages_thread_search ON chat_messages USING gin (to_tsvector('simple', coalesce(body, ''))) WHERE deleted_for = 'none';

CREATE TABLE message_reactions ( -- 1 active per user per message (PRD §8.5)
    id         uuid PRIMARY KEY,
    message_id bigint NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id),
    emoji      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (message_id, user_id)
);

CREATE TABLE chat_attachments (
    id          uuid PRIMARY KEY,
    message_id  bigint NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    media_id    uuid NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('image', 'file', 'audio', 'video')),
    width       int,
    height      int,
    duration_ms int,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE quick_replies ( -- business canned replies (PRD §5.5.2)
    id          uuid PRIMARY KEY,
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    text        text NOT NULL CHECK (char_length(text) <= 500),
    sort_order  int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_quick_replies_business ON quick_replies (business_id);

CREATE TABLE blocks ( -- user-blocking (PRD §5.5.4)
    id         uuid PRIMARY KEY,
    blocker_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (blocker_id, blocked_id)
);

-- =====================================================================
-- Platform (PRD §7.4)
-- =====================================================================

CREATE TABLE notifications (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type       text NOT NULL, -- PRD §5.7 types
    payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_read    boolean NOT NULL DEFAULT false,
    channel    text NOT NULL DEFAULT 'in_app' CHECK (channel IN ('in_app', 'email', 'push')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now() + interval '90 days'
);

CREATE INDEX idx_notifications_user ON notifications (user_id, is_read, created_at DESC);

CREATE TABLE reports ( -- PRD §5.8.2
    id          uuid PRIMARY KEY,
    reporter_id uuid NOT NULL REFERENCES users(id),
    target_type text NOT NULL CHECK (target_type IN ('review', 'comment', 'message', 'attachment', 'product', 'business', 'user', 'reaction')),
    target_id   text NOT NULL,
    reason      text NOT NULL,
    evidence    jsonb, -- e.g. last 10 messages (PRD §5.5.4)
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'dismissed')),
    resolved_by uuid REFERENCES users(id),
    resolved_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_reports_unique_open ON reports (reporter_id, target_type, target_id) WHERE status = 'open'; -- one open report per pair

CREATE INDEX idx_reports_status ON reports (status, created_at);

CREATE TABLE moderation_actions ( -- append-only audit trail (PRD §8.7)
    id          uuid PRIMARY KEY,
    admin_id    uuid NOT NULL REFERENCES users(id),
    action      text NOT NULL, -- hide|restore|warn|suspend|ban|unban|approve|reject|delete_for_everyone
    target_type text NOT NULL,
    target_id   text NOT NULL,
    reason      text NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_moderation_actions_target ON moderation_actions (target_type, target_id);

CREATE TABLE media ( -- PRD §7.4
    id               uuid PRIMARY KEY,
    uploader_id      uuid NOT NULL REFERENCES users(id),
    kind             text NOT NULL, -- logo|cover|gallery|product|avatar|chat_image|chat_file|chat_audio|chat_video|document_verification
    original_name    text NOT NULL,
    mime             text NOT NULL, -- validated by magic bytes (PRD §9.3)
    size             bigint NOT NULL,
    width            int,
    height           int,
    duration_ms      int,
    path             text NOT NULL, -- random-named, CDN-ready
    variants         jsonb NOT NULL DEFAULT '{}'::jsonb, -- image/audio/video variants (PRD §9.1)
    virus_scan_status text NOT NULL DEFAULT 'pending' CHECK (virus_scan_status IN ('pending', 'clean', 'infected', 'error')),
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_media_uploader ON media (uploader_id, created_at DESC);

CREATE TABLE trend_snapshots ( -- leaderboard materialization (PRD §5.6.3)
    id            uuid PRIMARY KEY,
    period        text NOT NULL CHECK (period IN ('24h', '7d', '30d')),
    business_id   uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    score         numeric NOT NULL,
    velocity      numeric NOT NULL DEFAULT 0,
    rank_global   int,
    rank_category int,
    rank_city     int,
    is_booming    boolean NOT NULL DEFAULT false,
    is_rising     boolean NOT NULL DEFAULT false,
    taken_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_trend_snapshots ON trend_snapshots (period, taken_at DESC, rank_global);

CREATE TABLE banned_words (
    id         uuid PRIMARY KEY,
    word       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE currency_rates ( -- hourly feed (PRD D5, §10.1)
    id         uuid PRIMARY KEY,
    code       char(3) NOT NULL UNIQUE,
    rate_usd   numeric NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now()
);

-- updated_at triggers
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_updated      BEFORE UPDATE ON users      FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_categories_updated BEFORE UPDATE ON categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_businesses_updated BEFORE UPDATE ON businesses FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_products_updated   BEFORE UPDATE ON products   FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_reviews_updated    BEFORE UPDATE ON reviews    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_comments_updated   BEFORE UPDATE ON comments   FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_collections_updated BEFORE UPDATE ON collections FOR EACH ROW EXECUTE FUNCTION set_updated_at();
