# Contributing

## Before you start

```powershell
# Backing services
docker compose -f infra/docker/docker-compose.yml up -d postgres mailpit

# Migrations — the Go runner is the only supported path
cd services/api
go run ./cmd/api -migrate

# API
go run ./cmd/api            # :8080

# Web
cd ../../apps/web
npm install
npm run dev                 # :5173, proxies /api and /ws to :8080
```

Do **not** apply migrations with `psql -f`. The runner does three things a raw
`psql` does not: it takes an advisory lock so two booting replicas cannot race,
it splits `CREATE INDEX CONCURRENTLY` out of the transaction, and it records
each version only after that version's concurrent statements succeed.

`scripts/seed.sql` loads a small Indonesian/Jakarta fixture set — two verified
businesses, a category tree, and a user per role. The Playwright suite depends
on it.

## Toolchain

| Tool | Version | Source of truth |
|---|---|---|
| Go | 1.26 | `services/api/go.mod`, pinned in `.github/workflows/ci.yml` |
| Node | 24 | `.github/workflows/ci.yml` |
| PostgreSQL | 17 | `infra/docker/docker-compose.yml` |

If `go` fails with `compile: version "go1.X" does not match go tool version
"go1.Y"`, a `GOROOT` environment variable is pointing at a different toolchain
than the `go` binary on your `PATH`. Fix it at the source rather than working
around it:

```powershell
go env -u GOROOT
```

A previous `scripts/go.cmd` shim existed to paper over this. It was deleted: it
pinned absolute paths from one machine and a toolchain version that CI did not
use, so it could not work anywhere else.

## Writing a migration

Every migration from `0028` onward must carry a `-- migrate:` header:

```sql
-- migrate:idempotent  yes
-- migrate:concurrent  true
-- migrate:seed        none
-- migrate:risk        DDL
-- migrate:note        one line: what this file assumes and why replay is safe
```

`-- migrate:note` is required if `idempotent` is `no`.

### The rules the CI will check

Two tests enforce them, and both run in `go test ./...`:

**`TestMigrationsDeclareTheirIdempotencyContract`** checks shape from a fixed
rule table. It will fail your file if you write:

- `CREATE TABLE` / `CREATE INDEX` without `IF NOT EXISTS`
- `CREATE TYPE` (no `IF NOT EXISTS` form exists — use a `DO` block and set
  `idempotent no`)
- `CREATE TRIGGER` with no preceding `DROP TRIGGER IF EXISTS`
- `ALTER TABLE ... ADD COLUMN` without `IF NOT EXISTS`
- `ADD CONSTRAINT` with no `DROP CONSTRAINT IF EXISTS` for the same name in the
  same file
- `DROP` without `IF EXISTS`
- `INSERT` without `ON CONFLICT`
- `CREATE INDEX CONCURRENTLY` with neither a preceding
  `DROP INDEX CONCURRENTLY IF EXISTS` of the same name, nor (preferred) both

**`TestMigrateReplayIsSafe`** proves it. It migrates a database, deletes one
version marker, re-runs, and requires both that the re-run succeeds and that the
schema fingerprint is byte-identical. It needs `TEST_DATABASE_URL` and is the
test that would have caught the three replay defects in `0029` on the day they
were written.

### Why the drop-then-build shape

```sql
DROP INDEX CONCURRENTLY IF EXISTS idx_thing;
CREATE INDEX CONCURRENTLY idx_thing ON thing (col);
```

`CREATE INDEX CONCURRENTLY` that fails leaves a row in `pg_class` with
`pg_index.indisvalid = false`. `IF NOT EXISTS` compares **names** and never
looks at `indisvalid`, so a retried build skips it and the index the migration
exists to create is permanently absent. The query that needed it just gets slow
at some point in the past, with nothing in the logs.

Dropping first makes the retry converge. It costs one index rebuild.

`Migrate()` also fails at boot if any index is invalid, so this state cannot be
reached silently.

### Ordering across a constraint swap

A partial unique index and a full unique constraint can legally coexist, so
build the replacement **before** dropping the original. `0029` does this for
`businesses_slug_live` / `businesses_slug_key`; the reverse order opens a window
in which `businesses.slug` has no uniqueness at all. If a statement must run on
autocommit without being an `INDEX CONCURRENTLY`, tag it:

```sql
/* autocommit */ ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_slug_key;
```

It is a block comment rather than a `--` comment because the statement splitter
drops whole-line `--` comments before assembling a statement, so a `--` marker
would be invisible by the time the router looks for it.

### Comments and the statement splitter

The splitter in `internal/db/migrate.go` is deliberately simple, and migrations
must live within it:

- A statement ends only when the accumulated line, trimmed, ends with `;`.
- **Whole-line `--` comments are dropped.** Put every comment on its own line.
- A **trailing** `--` comment on a line that also ends in `;` suppresses the
  statement boundary and merges two statements. This is the single most likely
  way to break a migration by accident.
- `$$` and `$tag$` bodies are tracked, so a `;` inside plpgsql is safe.
- Single-quoted strings are **not** tracked. Never end a line with a string
  literal whose last character is `;`.
- `DO $tag$ ... $tag$` bodies are opaque to the static linter. It reports the
  count rather than pretending to have checked.

### Foreign keys

Postgres does not index the referencing side of a foreign key. Add the index in
the same migration that creates the column.

## Tests

```powershell
cd services/api
go vet ./...
go test ./...                                   # unit + static migration checks
go test -race ./...                             # required before pushing: the
                                                # concurrency tests cannot detect
                                                # a race without it
go test -cover ./...

# Integration / replay (needs a PostgreSQL you may create and drop databases on)
$env:TEST_DATABASE_URL = "postgres://postgres@localhost:5432/postgres?sslmode=disable"
$env:MIGRATIONS_DIR   = "$PWD/../../infra/postgres/migrations"
go test ./internal/db/ ./internal/repo/ ./internal/service/ ./internal/jobs/ -timeout 30m
```

Without `TEST_DATABASE_URL` the integration suites **skip silently** — they call
`t.Skip`, they do not fail. So a green `go test ./...` on a machine with no
database is a green run of the smaller program, and a coverage gate built on it
silently measures less than it reports. Check the `-v` output for `SKIP` before
believing a result.

The integration harness clones a migrated template database per test and rolls
back, so it needs a server it may `CREATE`/`DROP DATABASE` on. It builds that
template by running the real migration chain, which is why a broken migration
fails every test with one clear message instead of many confusing ones.

### A dedicated local cluster

Use your own instance rather than a shared or system one. Two reasons, both of
which cost real debugging time on this project:

- **Do not put the data directory under `%TEMP%`.** Temp cleanup will delete
  `postmaster.pid` out from under a running server, and PostgreSQL responds by
  shutting itself down: `performing immediate shutdown because data directory
  lock file is invalid`. It looks like a random crash, and it recurs.
- **Do not share a port with another project's database.** If two clusters want
  the same port, whichever starts second loses — and your `TEST_DATABASE_URL`
  then points at *someone else's* server, where `CREATE DATABASE` litters a
  cluster you were not asked to touch.

A local-only cluster, gitignored, on a port you chose:

```powershell
$bin = "D:\laragon\bin\postgresql\pgsql-18.6\bin"   # or your install
& "$bin\initdb.exe" -D .pgdata -U postgres -A trust --encoding=UTF8
& "$bin\postgres.exe" -D "$PWD/.pgdata" -p 55440   # start in another shell
$env:TEST_DATABASE_URL = "postgres://postgres@127.0.0.1:55440/postgres?sslmode=disable"
```

`.pgdata/` is in `.gitignore`. Confirm the port is actually yours before trusting
a test result — `pg_isready -h 127.0.0.1 -p 55440` proves a server is listening,
not that it is the one you started.

The SQL harnesses in `infra/postgres/` run against a migrated, seeded database:

```powershell
$env:PGPASSWORD = "postgres"
psql -h localhost -U postgres -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_integrity.sql
psql -h localhost -U postgres -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_open_now.sql
psql -h localhost -U postgres -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_index_inventory.sql
psql -h localhost -U postgres -d bizverse -v ON_ERROR_STOP=1 -f infra/postgres/test_index_usage_seeded.sql
```

`test_integrity.sql`, `test_index_inventory.sql` and
`test_index_usage_seeded.sql` all run inside a transaction and roll back, so they
are safe against a database holding data. All four RAISE on failure, so
`ON_ERROR_STOP=1` makes them gates rather than reports.

`test_index_usage.sql` is deliberately **not** in that list: it is bare
`EXPLAIN` output for a human to read, it asserts nothing, and on an empty
database the planner has no statistics to choose with. Use the `_seeded` variant
when you want an assertion about a plan.

### Frontend

```powershell
cd apps/web
npm run typecheck
npm run lint
npm run test:unit
npx playwright install chromium
npx playwright test
```

## Style

**Go** — `gofmt`. Comments explain *why*, not *what*, and reference the file and
line of the constraint being satisfied. A comment that asserts a mechanism
should be verifiable; `0029` carried a justification for `media_path_key` that
turned out to describe an unreachable code path, and the wrong reason is worse
than no reason because the next reader trusts it.

**SQL** — match the surrounding migration. `0029`, `0030` and `0031` set the
bar: state the defect, the blast radius, the decision, and what was deliberately
not done.

**TypeScript** — no `any` without a comment saying why. Prefer deriving a type
from the API response shape over restating it, so a DTO change breaks the
compiler.

## Commit messages

Explain the defect or the decision. The repository's history uses the form
`area: what was wrong, and what is now true`:

```
search: two-phase query so hydration subqueries run per page, not per candidate
```

A commit that only says what changed is a commit nobody can review.

## Pull requests

- `go test -race ./...` and `npm run typecheck` pass
- A migration, if any, has a `-- migrate:` header and passes the replay test
- `docs/API.md` updated if you added, removed, or changed a route
- `infra/postgres/migrations/REGISTRY.md` updated if you reserved a new number
- No secrets in tracked files; `.env.example` documents every variable you added
