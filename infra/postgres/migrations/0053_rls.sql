-- 0053_rls.sql — row-level security on tenant and owner tables.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        Replay-safe throughout: CREATE OR REPLACE FUNCTION,
--                     DROP POLICY IF EXISTS before each CREATE POLICY, and
--                     ENABLE ROW LEVEL SECURITY (already-enabled is a no-op).
--                     GRANTs are notices on re-run. The replay test proves it.
--
-- WHY THIS EXISTS
-- --------------
-- Until this file every tenant boundary was enforced in Go: one missed WHERE
-- clause in a repository method was a cross-tenant read, not a 500. 0041 took
-- away the owner's DDL; this file makes the database re-check the reader on
-- every row of the tables that hold one user's data next to another's.
--
-- WHAT RLS IS AND IS NOT HERE
-- ---------------------------
-- - A READ backstop, first. Write authorization (validation, state machines,
--   who-may-create-what) stays in the service layer, where the error messages
--   and product rules live. Several INSERT policies are therefore
--   deliberately "any authenticated user" (chat_threads, chat_participants,
--   chat_messages): the acting user is not always the row owner — the other
--   direct-message participant is added by the creator (chat.go
--   GetOrCreateDirect), the business owner is added to a customer thread
--   (GetOrCreateBusiness), and system notices (reopen/close/leave) carry no
--   sender at all. Constraining those writes in SQL would break real flows;
--   constraining the READS is what stops one user seeing another's data.
-- - Deliberately NOT on businesses / products / trend_snapshots / users /
--   sessions / notifications / engagement: search, leaderboard, SSR and auth
--   queries run without a user context or across users by design, and blanket
--   RLS would break the hot path (or the login flow). Those tables keep their
--   code-level authorization, which the service test suite pins.
-- - Jobs, migrations, tests and the notifier run as the table owner and
--   bypass RLS entirely (ENABLE, not FORCE). Only the HTTP request pool
--   (repo.ConfigureRLS → SET ROLE allstuff_app + allstuff_app.user_id per acquisition) is
--   constrained. There is no service-bypass GUC to steal.
-- - billing_webhook_events has no user column: it is reachable only with NO
--   user set (the Stripe-signed webhook) or as admin. Authenticated sessions
--   see nothing — the ledger is not per-user data.
-- - api_keys SELECT stays open with no user set because the API-key login
--   itself runs unauthenticated (lookup by hash precedes identity). Writes
--   stay owner-only. The lookup is by secret hash, not enumerable.
--
-- The helper functions live in public with an rls_ prefix (never bare `app`,
-- which is a role name and would confuse the next reader). Policies call them
-- schema-qualified so a hostile search_path cannot redirect them.

-- ---------------------------------------------------------------------------
-- Helpers
-- ---------------------------------------------------------------------------

-- The requesting user, or NULL for anonymous/service-adjacent contexts
-- (public reads, the signed Stripe webhook). Malformed values fail closed to
-- NULL rather than erroring the query: only the server writes this GUC, and
-- it writes validated UUIDs, but a policy must never turn a bad setting into
-- a 500 on a public page.
CREATE OR REPLACE FUNCTION public.rls_uid() RETURNS uuid
    LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN current_setting('allstuff_app.user_id', true) ~ '^[0-9a-fA-F-]{36}$'
        THEN current_setting('allstuff_app.user_id', true)::uuid
        ELSE NULL
    END
$$;

CREATE OR REPLACE FUNCTION public.rls_is_admin(uid uuid) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM public.users WHERE id = uid AND role = 'admin')
$$;

-- Mirrors Businesses.CanManageBusiness exactly: the owner, or an accepted,
-- non-revoked co_owner grant resolved through accepted_user_id (never the
-- email — migration 0049 exists because that resolution was the bug).
CREATE OR REPLACE FUNCTION public.rls_can_manage(biz uuid, uid uuid) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM public.businesses b WHERE b.id = biz AND b.owner_id = uid
        UNION ALL
        SELECT 1 FROM public.business_invites i
        WHERE i.business_id = biz
          AND i.accepted_at IS NOT NULL AND i.revoked_at IS NULL
          AND i.role = 'co_owner' AND i.accepted_user_id = uid
    )
$$;

-- Mirrors Chat.checkAccess exactly: a participant row whose left_at is NULL.
-- A user who left a thread loses it, same as the service layer.
CREATE OR REPLACE FUNCTION public.rls_thread_member(tid uuid, uid uuid) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM public.chat_participants p
        WHERE p.thread_id = tid AND p.user_id = uid AND p.left_at IS NULL
    )
$$;

-- ---------------------------------------------------------------------------
-- media — public storefront kinds for everyone; the rest by ownership,
-- thread membership (chat kinds), document linkage (verification), or admin
-- (attachment moderation reads reported media by id, and the document viewer
-- serves verification files — both run as the admin user).
-- ---------------------------------------------------------------------------
ALTER TABLE media ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS media_tenant ON media;
CREATE POLICY media_tenant ON media FOR ALL
USING (
    kind IN ('logo', 'cover', 'gallery', 'product', 'avatar')
    OR uploader_id = public.rls_uid()
    OR public.rls_is_admin(public.rls_uid())
    OR (kind IN ('chat_image', 'chat_file', 'chat_audio', 'chat_video') AND EXISTS (
        SELECT 1
        FROM chat_messages m
        JOIN chat_participants p ON p.thread_id = m.thread_id
        WHERE m.media_id = media.id
          AND p.user_id = public.rls_uid() AND p.left_at IS NULL
    ))
    OR (kind = 'document_verification' AND EXISTS (
        SELECT 1 FROM verification_documents d
        WHERE d.media_id = media.id
          AND (public.rls_is_admin(public.rls_uid())
               OR public.rls_can_manage(d.business_id, public.rls_uid()))
    ))
)
WITH CHECK (
    uploader_id = public.rls_uid() OR public.rls_is_admin(public.rls_uid())
);

-- ---------------------------------------------------------------------------
-- verification_documents + verification_document_views — managers and admins.
-- ---------------------------------------------------------------------------
ALTER TABLE verification_documents ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS verification_documents_tenant ON verification_documents;
CREATE POLICY verification_documents_tenant ON verification_documents FOR ALL
USING (
    public.rls_is_admin(public.rls_uid())
    OR public.rls_can_manage(business_id, public.rls_uid())
)
WITH CHECK (
    public.rls_is_admin(public.rls_uid())
    OR public.rls_can_manage(business_id, public.rls_uid())
);

ALTER TABLE verification_document_views ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS verification_document_views_admin ON verification_document_views;
CREATE POLICY verification_document_views_admin ON verification_document_views FOR ALL
USING (public.rls_is_admin(public.rls_uid()))
WITH CHECK (public.rls_is_admin(public.rls_uid()));

-- ---------------------------------------------------------------------------
-- billing_webhook_events — the idempotency ledger. No user column by design:
-- reachable with no user set (the Stripe-signed webhook) or as an admin.
-- Authenticated non-admin sessions see nothing.
-- ---------------------------------------------------------------------------
ALTER TABLE billing_webhook_events ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS billing_webhook_events_tenant ON billing_webhook_events;
CREATE POLICY billing_webhook_events_tenant ON billing_webhook_events FOR ALL
USING (public.rls_uid() IS NULL OR public.rls_is_admin(public.rls_uid()))
WITH CHECK (public.rls_uid() IS NULL OR public.rls_is_admin(public.rls_uid()));

-- ---------------------------------------------------------------------------
-- push_subscriptions, saved_searches, api_keys, business_claims
-- ---------------------------------------------------------------------------
ALTER TABLE push_subscriptions ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS push_subscriptions_tenant ON push_subscriptions;
CREATE POLICY push_subscriptions_tenant ON push_subscriptions FOR ALL
USING (user_id = public.rls_uid())
WITH CHECK (user_id = public.rls_uid());

ALTER TABLE saved_searches ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS saved_searches_tenant ON saved_searches;
CREATE POLICY saved_searches_tenant ON saved_searches FOR ALL
USING (user_id = public.rls_uid())
WITH CHECK (user_id = public.rls_uid());

-- SELECT stays open with no user set: the API-key login runs unauthenticated
-- (lookup by hash precedes identity). Writes stay owner-only.
ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS api_keys_select ON api_keys;
CREATE POLICY api_keys_select ON api_keys FOR SELECT
USING (user_id = public.rls_uid() OR public.rls_uid() IS NULL);
DROP POLICY IF EXISTS api_keys_write ON api_keys;
CREATE POLICY api_keys_write ON api_keys FOR INSERT
WITH CHECK (user_id = public.rls_uid());
DROP POLICY IF EXISTS api_keys_modify ON api_keys;
CREATE POLICY api_keys_modify ON api_keys FOR UPDATE
USING (user_id = public.rls_uid())
WITH CHECK (user_id = public.rls_uid());
DROP POLICY IF EXISTS api_keys_delete ON api_keys;
CREATE POLICY api_keys_delete ON api_keys FOR DELETE
USING (user_id = public.rls_uid());

-- Claimants see their own claims; the moderation queue is admin-only.
ALTER TABLE business_claims ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS business_claims_tenant ON business_claims;
CREATE POLICY business_claims_tenant ON business_claims FOR ALL
USING (user_id = public.rls_uid() OR public.rls_is_admin(public.rls_uid()))
WITH CHECK (user_id = public.rls_uid() OR public.rls_is_admin(public.rls_uid()));

-- ---------------------------------------------------------------------------
-- collections + collection_items — owners, plus the public share links
-- (is_public collections and their items are readable with no user set).
-- ---------------------------------------------------------------------------
ALTER TABLE collections ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS collections_tenant ON collections;
CREATE POLICY collections_tenant ON collections FOR ALL
USING (is_public OR user_id = public.rls_uid())
WITH CHECK (user_id = public.rls_uid());

ALTER TABLE collection_items ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS collection_items_tenant ON collection_items;
CREATE POLICY collection_items_tenant ON collection_items FOR ALL
USING (EXISTS (
    SELECT 1 FROM collections c
    WHERE c.id = collection_id AND (c.is_public OR c.user_id = public.rls_uid())
))
WITH CHECK (EXISTS (
    SELECT 1 FROM collections c
    WHERE c.id = collection_id AND c.user_id = public.rls_uid()
));

-- ---------------------------------------------------------------------------
-- chat_* — readers must be active participants (mirrors checkAccess); admins
-- may read reported messages and media while moderating (mirrors DecideReport
-- reading chat_messages/media/message_reactions by id). Inserts stay
-- permissive-with-authenticated (see header note: the other participant is
-- added by the creator, system notices carry no sender).
-- ---------------------------------------------------------------------------
ALTER TABLE chat_threads ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS chat_threads_read ON chat_threads;
CREATE POLICY chat_threads_read ON chat_threads FOR SELECT
USING (public.rls_thread_member(id, public.rls_uid()));
DROP POLICY IF EXISTS chat_threads_write ON chat_threads;
CREATE POLICY chat_threads_write ON chat_threads FOR INSERT
WITH CHECK (public.rls_uid() IS NOT NULL);
DROP POLICY IF EXISTS chat_threads_modify ON chat_threads;
CREATE POLICY chat_threads_modify ON chat_threads FOR UPDATE
USING (public.rls_thread_member(id, public.rls_uid()))
WITH CHECK (public.rls_thread_member(id, public.rls_uid()));
DROP POLICY IF EXISTS chat_threads_delete ON chat_threads;
CREATE POLICY chat_threads_delete ON chat_threads FOR DELETE
USING (public.rls_thread_member(id, public.rls_uid()));

ALTER TABLE chat_participants ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS chat_participants_read ON chat_participants;
CREATE POLICY chat_participants_read ON chat_participants FOR SELECT
USING (
    user_id = public.rls_uid()
    OR public.rls_thread_member(thread_id, public.rls_uid())
);
DROP POLICY IF EXISTS chat_participants_write ON chat_participants;
CREATE POLICY chat_participants_write ON chat_participants FOR INSERT
WITH CHECK (public.rls_uid() IS NOT NULL);
DROP POLICY IF EXISTS chat_participants_modify ON chat_participants;
CREATE POLICY chat_participants_modify ON chat_participants FOR UPDATE
USING (user_id = public.rls_uid() OR public.rls_thread_member(thread_id, public.rls_uid()))
WITH CHECK (public.rls_uid() IS NOT NULL);
DROP POLICY IF EXISTS chat_participants_delete ON chat_participants;
CREATE POLICY chat_participants_delete ON chat_participants FOR DELETE
USING (user_id = public.rls_uid());

ALTER TABLE chat_messages ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS chat_messages_read ON chat_messages;
CREATE POLICY chat_messages_read ON chat_messages FOR SELECT
USING (
    public.rls_thread_member(thread_id, public.rls_uid())
    OR public.rls_is_admin(public.rls_uid())
);
DROP POLICY IF EXISTS chat_messages_write ON chat_messages;
CREATE POLICY chat_messages_write ON chat_messages FOR INSERT
WITH CHECK (public.rls_uid() IS NOT NULL);
DROP POLICY IF EXISTS chat_messages_modify ON chat_messages;
CREATE POLICY chat_messages_modify ON chat_messages FOR UPDATE
USING (
    public.rls_thread_member(thread_id, public.rls_uid())
    OR public.rls_is_admin(public.rls_uid())
)
WITH CHECK (public.rls_uid() IS NOT NULL);
DROP POLICY IF EXISTS chat_messages_delete ON chat_messages;
CREATE POLICY chat_messages_delete ON chat_messages FOR DELETE
USING (
    public.rls_thread_member(thread_id, public.rls_uid())
    OR public.rls_is_admin(public.rls_uid())
);

-- (No policy on chat_attachments: 0021 dropped that table, superseded by
-- messages.media_id. The media policy above covers chat media through the
-- messages join instead. If the table ever comes back, this migration must
-- grow a matching block — the replay test will not catch its absence, only
-- review will.)

ALTER TABLE message_reactions ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS message_reactions_read ON message_reactions;
CREATE POLICY message_reactions_read ON message_reactions FOR SELECT
USING (EXISTS (
    SELECT 1 FROM chat_messages m
    WHERE m.id = message_id
      AND (public.rls_thread_member(m.thread_id, public.rls_uid())
           OR public.rls_is_admin(public.rls_uid()))
));
DROP POLICY IF EXISTS message_reactions_write ON message_reactions;
CREATE POLICY message_reactions_write ON message_reactions FOR INSERT
WITH CHECK (
    user_id = public.rls_uid()
    AND EXISTS (
        SELECT 1 FROM chat_messages m
        WHERE m.id = message_id
          AND public.rls_thread_member(m.thread_id, public.rls_uid())
    )
);
DROP POLICY IF EXISTS message_reactions_delete ON message_reactions;
CREATE POLICY message_reactions_delete ON message_reactions FOR DELETE
USING (user_id = public.rls_uid());

ALTER TABLE chat_message_hides ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS chat_message_hides_tenant ON chat_message_hides;
CREATE POLICY chat_message_hides_tenant ON chat_message_hides FOR ALL
USING (user_id = public.rls_uid())
WITH CHECK (user_id = public.rls_uid());
