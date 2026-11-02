-- 0054_rls_helpers_hardening.sql — make the RLS helpers recursion-proof.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        ALTER FUNCTION (definer flag, search_path) and GRANTs are
--                     naturally re-runnable; CREATE OR REPLACE re-declares the
--                     same bodies. The media predicate change is DROP + CREATE
--                     POLICY, both IF EXISTS-guarded. The replay test proves it.
--
-- WHY THIS EXISTS
-- --------------
-- The helpers in 0053 are plain invoker-rights SQL functions, and the chat
-- policies call them while evaluating reads ON the tables the helpers
-- themselves query (rls_thread_member reads chat_participants, whose own
-- SELECT policy calls rls_thread_member back). It works today — the probe
-- suite proves it, most likely because the planner evaluates the STABLE
-- function once per statement for constant arguments — but "works because of
-- a planner behavior no test pins" is exactly how a future Postgres upgrade
-- turns green into a stack-depth error at 2am.
--
-- SECURITY DEFINER ends the question structurally: the helpers run as the
-- table owner, which bypasses RLS, so a policy can never re-enter itself no
-- matter how the planner shapes the call. Three compensating controls come
-- with it, because a definer function is a privilege boundary:
--   1. SET search_path = public, pg_temp pins name resolution, so a
--      compromised search_path cannot redirect the helper's table references.
--   2. REVOKE ALL FROM PUBLIC + GRANT EXECUTE TO allstuff_app keeps the only
--      caller the request role. (Default PUBLIC execute is revoked here
--      because, unlike table SELECT, function execution has no per-row
--      policy to fall back on.)
--   3. The bodies are unchanged apart from the wrapper attributes.
--
-- ALSO IN THIS FILE
-- -----------------
-- The media chat branch gains the withdrawn-message predicate the serving
-- path already enforces in Go (handlers_business.go authorizeChatMedia):
-- attachments of messages deleted-for-everyone must not be row-readable
-- even where the bytes are still gated one layer up.

ALTER FUNCTION public.rls_uid() SECURITY DEFINER SET search_path = public, pg_temp;
ALTER FUNCTION public.rls_is_admin(uuid) SECURITY DEFINER SET search_path = public, pg_temp;
ALTER FUNCTION public.rls_can_manage(uuid, uuid) SECURITY DEFINER SET search_path = public, pg_temp;
ALTER FUNCTION public.rls_thread_member(uuid, uuid) SECURITY DEFINER SET search_path = public, pg_temp;

REVOKE ALL ON FUNCTION public.rls_uid() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.rls_is_admin(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.rls_can_manage(uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.rls_thread_member(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.rls_uid() TO allstuff_app;
GRANT EXECUTE ON FUNCTION public.rls_is_admin(uuid) TO allstuff_app;
GRANT EXECUTE ON FUNCTION public.rls_can_manage(uuid, uuid) TO allstuff_app;
GRANT EXECUTE ON FUNCTION public.rls_thread_member(uuid, uuid) TO allstuff_app;

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
          AND m.deleted_for <> 'everyone'
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
