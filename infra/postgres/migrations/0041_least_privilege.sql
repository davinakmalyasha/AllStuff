-- 0041_least_privilege.sql — the app stops connecting as the schema owner.
--
-- migrate:idempotent  yes
-- migrate:concurrent  false
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        Every statement is naturally re-runnable: GRANT re-issue is a
--                     notice, ALTER DEFAULT PRIVILEGES re-declares the same defaults, and
--                     role creation is guarded by a pg_roles check inside a DO block
--                     (CREATE ROLE has no IF NOT EXISTS form), with attributes
--                     enforced unconditionally afterwards. The replay test proves it.
--
-- WHY THIS EXISTS
-- --------------
-- Until this file, the API connected as the user that owns every table: a SQL
-- injection (there is none — parameterized queries throughout) or a compromised
-- dependency with pool access could DROP, TRUNCATE, or CREATE anything, and row
-- security (0053) cannot constrain the table owner at all. After this file the
-- request path runs as `allstuff_app`, which owns nothing and holds only the
-- DML it needs; DDL stays with the migration/owner role.
--
-- WHY `allstuff_app` AND NOT `app`
-- --------------------------------
-- Roles are cluster-scoped, not database-scoped. A bare `app` role already
-- exists on shared developer clusters (it is the obvious name — another
-- project on this machine owns one, WITH LOGIN and a password), and a DO
-- block that only creates-when-missing would silently adopt whatever
-- attributes a stranger gave it. The prefixed name is fully owned by this
-- migration, and the ALTER below enforces NOLOGIN even if the name ever
-- collides again.
--
-- WHAT THIS DELIBERATELY DOES NOT DO
-- ----------------------------------
-- - No REVOKE FROM PUBLIC. There is no direct database user in any topology
--   (dev, CI, Railway): every connection comes from the API pools. A PUBLIC
--   revoke is cosmetic here and risks breaking extensions; the protection
--   comes from the non-owner role plus RLS in 0053.
-- - No password rotation: `allstuff_app` is NOLOGIN. Nothing ever
--   authenticates as it; the runtime pool authenticates as the owner and
--   drops privileges per acquisition with SET ROLE (see repo/rls.go). There
--   is no credential to leak, rotate, or put in an env file.
-- - Default privileges are declared FOR ROLE postgres, the role that runs
--   migrations in every environment (dev compose, CI service, Railway
--   plugin). A later migration applied by a different owner would need its
--   own ALTER DEFAULT PRIVILEGES line; the replay test does not cover that,
--   the lint does not check it, so this note is the check.

-- The runtime role. NOLOGIN on purpose (see above): SET ROLE from the owner
-- pool is the only way in, and it never crosses a process boundary.
-- Attributes are enforced even when the role already exists, so a pre-existing
-- same-named role can never smuggle LOGIN or a password in.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'allstuff_app') THEN
        CREATE ROLE allstuff_app WITH NOLOGIN;
    END IF;
END
$$;
ALTER ROLE allstuff_app WITH NOLOGIN PASSWORD NULL;

-- Whoever runs migrations must be able to SET ROLE allstuff_app at runtime.
-- The runtime uses the same DSN as migrations in every documented topology,
-- so granting to CURRENT_USER covers exactly the right principal in each
-- environment (dev, CI, prod) without naming one. Re-granting is a notice.
GRANT allstuff_app TO CURRENT_USER;

-- Connect + use the schema. The database name differs per environment
-- (allstuff, allstuff_e2e, per-test clones), so GRANT ON DATABASE names the
-- current one dynamically instead of hardcoding it.
DO $$
BEGIN
    EXECUTE format('GRANT CONNECT, TEMPORARY ON DATABASE %I TO allstuff_app', current_database());
END
$$;

GRANT USAGE ON SCHEMA public TO allstuff_app;

-- DML on everything that exists today...
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO allstuff_app;

-- ...identity/serial sequences (chat_messages.id and friends) ...
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO allstuff_app;

-- ...and everything later migrations create. FOR ROLE postgres is the
-- migrating owner in every environment; see the header note for the caveat.
ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO allstuff_app;

ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO allstuff_app;
