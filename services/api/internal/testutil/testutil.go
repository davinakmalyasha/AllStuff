// Package testutil provides a real PostgreSQL for integration tests.
//
// WHY THIS EXISTS
// ---------------
// Until now the repository layer had no tests at all: 2,972 lines of SQL across
// thirteen files, zero test files, 0% coverage. CI provisioned a PostgreSQL
// service, applied all 31 migrations, loaded the seed — and then ran a test
// suite in which nothing opened a connection. Every step of that setup was
// ceremony.
//
// That is not a hypothetical gap. The two-phase search rewrite and the N+1
// hydration fix were both defect classes that live in this layer, and neither
// was reachable by a unit test. A wrong bind-parameter count, a
// transaction-scope mistake, or a `NULL` that propagates where a zero was meant
// is a class of bug no pure-function test can catch, because the bug IS the
// interaction with Postgres.
//
// WHY NO NEW DEPENDENCY
// ---------------------
// The backend has three direct dependencies on purpose — JWT, TOTP, Argon2id,
// the Redis RESP client, Stripe, Web Push and the Prometheus exposition are all
// hand-rolled. Adding testcontainers-go (itself a dozen transitive deps) to get
// a database would undercut that. CI already provisions PostgreSQL as a service
// container, and `pgx` is already a dependency, so this package needs neither.
//
// HOW ISOLATION WORKS
// -------------------
// A TEMPLATE database is built once per process by running the real migration
// chain through the real runner. Each test then gets its own database cloned
// from that template, opens a transaction, and rolls it back at the end.
//
// Cloning rather than TRUNCATE is deliberate: TRUNCATE needs a correct
// CASCADE order across fifty-one tables and sixteen foreign keys, and getting
// it wrong leaves a test that fails for a reason unrelated to what it is
// testing. A clone of an EMPTY schema is a file copy, and it makes every test
// independently runnable and parallel-safe.
//
// The transaction on top of the clone means a test that writes needs no cleanup
// at all: the ROLLBACK is the cleanup, and it cannot be forgotten.
//
// WHAT IS NOT SUPPORTED
// ---------------------
// A test bound to a transaction cannot exercise code that commits its own
// transaction or that reaches for the connection pool, because `repo.NewForTx`
// leaves `Repos.pool` nil. Two cases hit this in practice:
//
//   - `Repos.Pool()` returns nil on a tx-bound Repos, so a code path that calls
//     Pool().Begin() will panic.
//   - `Repos.Exec` / `Repos.QueryRow` delegate to that nil pool, so calling them
//     panics too. This is easy to miss because it looks like an ordinary
//     statement helper — the scheduler's claimPeriod calls repos.Exec, so
//     `internal/jobs` tests must use PoolRepos instead.
//
// Use H.PoolRepos() for those paths. The trade is real: pool-bound tests lose
// the rollback cleanup, so a test using it MUST rely on the per-test database
// being dropped rather than on anything it tidies up itself. Isolation still
// holds, one level up, because each test's database is a clone that the harness
// drops afterwards.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bizverse/api/internal/db"
	"bizverse/api/internal/repo"
)

// adminDSN returns the DSN this package may create and drop databases on.
//
// It must point at a database that EXISTS and that the caller is willing to have
// sibling databases created and destroyed next to it — `postgres`, normally.
// Pointing this at a database holding data will not corrupt it directly, but
// every test database is created with it as its parent and the naming is
// deterministic enough to be careless with.
func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the integration test")
	}
	return dsn
}

// MigrationsDir resolves the migration directory, honouring the same env var
// the migration replay test uses.
func MigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := resolveMigrationsDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// resolveMigrationsDir returns the migrations directory, or an error explaining
// why the path it resolved does not hold any migrations.
//
// It used to be a bare os.Getenv with a 4-up default, and a path that pointed
// nowhere failed SILENTLY. The symptom appeared in a completely different test:
//
//	stage invalid index: relation "categories" does not exist
//
// because `go test` runs the binary from the PACKAGE directory, so a CI step
// that set MIGRATIONS_DIR=../../infra/postgres/migrations resolved it against
// services/api/internal/db and got services/api/infra/postgres/migrations,
// which does not exist. Migrate() then applied nothing - an empty directory is a
// supported input, see TestMigrateAppliesAnEmptyDirectory - so the replay suite
// built a pristine but EMPTY template database and passed. Four thousand lines
// away, in an unrelated file, naming a table nobody had touched.
//
// It returns an error rather than calling t.Fatalf so the behaviour is directly
// testable; see migrations_dir_test.go.
func resolveMigrationsDir() (string, error) {
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		// internal/testutil -> internal -> api -> services -> repo root
		dir = filepath.Join("..", "..", "..", "..", "infra", "postgres", "migrations")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(matches) == 0 {
		wd, _ := os.Getwd()
		return "", fmt.Errorf(
			"migrations directory %q holds no .sql files (resolved from the working directory %q). "+
				"If MIGRATIONS_DIR is set, it must be relative to the directory the PROCESS runs in: "+
				"`go test` runs from the package directory, while `go run` inherits the step's "+
				"working-directory. The two need different paths for the same target.",
			dir, wd)
	}
	return dir, nil
}

var (
	tmplOnce sync.Once
	tmplName string
	tmplErr  error
)

// template builds the pristine migrated database once per process.
//
// A separate `postgres`-level connection is required to issue CREATE/DROP
// DATABASE, which cannot run inside the database being created.
func template(t *testing.T) string {
	t.Helper()
	dsn := adminDSN(t)
	tmplOnce.Do(func() {
		ctx := context.Background()
		admin, err := connect(ctx, dsn, "postgres")
		if err != nil {
			tmplErr = fmt.Errorf("connect to postgres: %w", err)
			return
		}
		defer admin.Close()

		tmplName = fmt.Sprintf("bizverse_tmpl_%d", os.Getpid())
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+tmplName+" WITH (FORCE)"); err != nil {
			tmplErr = err
			return
		}
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+tmplName); err != nil {
			tmplErr = fmt.Errorf("create template: %w", err)
			return
		}
		pool, err := connect(ctx, dsn, tmplName)
		if err != nil {
			tmplErr = err
			return
		}
		// The real runner, over the real migrations. If the schema is broken, the
		// whole integration suite fails here with a clear message rather than
		// 200 confusing per-test failures.
		if err := db.Migrate(ctx, pool, MigrationsDir(t)); err != nil {
			pool.Close()
			tmplErr = fmt.Errorf("migrate template: %w", err)
			return
		}
		pool.Close()
	})
	if tmplErr != nil {
		t.Fatalf("building the test template: %v", tmplErr)
	}
	return tmplName
}

func connect(ctx context.Context, dsn, dbname string) (*pgxpool.Pool, error) {
	return connectTraced(ctx, dsn, dbname, nil)
}

// queryCounter counts the statements that actually reach the server.
//
// It is attached as a pgx QueryTracer rather than by wrapping repo.Repos,
// because Repos.pool is a concrete *pgxpool.Pool with no interface to intercept.
// A tracer needs no production code to change, and it counts what was really
// sent, not what the service layer believes it asked for.
type queryCounter struct {
	mu sync.Mutex
	n  int
}

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (c *queryCounter) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *queryCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *queryCounter) reset() {
	c.mu.Lock()
	c.n = 0
	c.mu.Unlock()
}

func connectTraced(ctx context.Context, dsn, dbname string, tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	cfg, err := pgxpool.ParseConfig(dsn + sep + "dbname=" + dbname)
	if err != nil {
		return nil, err
	}
	if tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}
	// Small on purpose: a test uses one connection, and every test that clones a
	// database also holds a slot on the admin connection.
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// H is a per-test database handle.
type H struct {
	// Pool is a connection to this test's own cloned database.
	Pool *pgxpool.Pool
	// Repos is bound to the test's transaction. ROLLBACK at cleanup removes
	// everything the test wrote, so no test can leak state into another.
	Repos *repo.Repos
	// Admin can issue CREATE/DROP DATABASE. Tests should rarely need it.
	Admin *pgxpool.Pool

	tx     pgx.Tx
	dbName string
	tmpl   string
	dsn    string
	// queries counts statements sent on Pool, for asserting query shape.
	queries *queryCounter
}

// New gives the test its own database, cloned from the migrated template, with
// an open transaction bound into a repo.Repos.
func New(t *testing.T) *H {
	t.Helper()
	dsn := adminDSN(t)
	tmpl := template(t)
	ctx := context.Background()

	admin, err := connect(ctx, dsn, "postgres")
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	h := &H{Admin: admin, tmpl: tmpl, dsn: dsn, queries: &queryCounter{}}
	h.dbName = fmt.Sprintf("bizverse_t%d_%s", os.Getpid(), sanitize(t.Name()))

	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+h.dbName+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop stale test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+h.dbName+" TEMPLATE "+tmpl); err != nil {
		t.Fatalf("clone template: %v", err)
	}

	pool, err := connectTraced(ctx, dsn, h.dbName, h.queries)
	if err != nil {
		h.cleanup()
		t.Fatalf("connect to test database: %v", err)
	}
	h.Pool = pool

	tx, err := pool.Begin(ctx)
	if err != nil {
		h.cleanup()
		t.Fatalf("begin: %v", err)
	}
	h.tx = tx
	h.Repos = repo.NewForTx(tx)
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
		h.cleanup()
	})
	return h
}

// UsePool rebinds h.Repos to the connection pool instead of the test
// transaction, for code paths that need a pool-bound Repos.
//
// A trap worth stating plainly, because it produces zero rows rather than an
// error: if a test mixes the two, its FIXTURES become invisible to the code
// under test. Fixtures write through h.Repos inside an uncommitted transaction,
// while the pool-bound code reads on a different connection, so the search sees
// an empty database and every assertion reads "0 of 12" with no hint why.
//
// Call this BEFORE creating fixtures, so the fixtures and the code share one
// connection and see the same rows. The rollback guarantee does not apply after
// this call, but isolation still does: the whole per-test database is dropped at
// cleanup, so nothing outlives the test either way.
func (h *H) UsePool() { h.Repos = repo.New(h.Pool) }

// ExecPool runs a statement on the pool rather than the test transaction, for
// arranging state that pool-bound code must be able to see. Use it with UsePool.
func (h *H) ExecPool(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := h.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", firstLine(sql), err)
	}
}

// QueryCount is how many statements have been sent on this test's Pool since
// construction (or since the last ResetQueryCount).
//
// It exists to assert query SHAPE rather than results. A performance fix such as
// the two-phase search rewrite — or its regression back into per-row hydration —
// returns exactly the same rows either way, so no result assertion can see it.
// Counting statements can.
//
// Statements issued on the admin connection and on the template-migration
// connection are NOT counted, so a test does not have to subtract setup noise.
func (h *H) QueryCount() int { return h.queries.count() }

// ResetQueryCount zeroes the counter, so a test can measure one call rather than
// everything the fixture did first.
func (h *H) ResetQueryCount() { h.queries.reset() }

// Pool returns a repo.Repos bound to the connection pool rather than to a
// transaction.
//
// Needed for the handful of code paths that commit their own transaction, which
// a tx-bound Repos cannot serve because Pool() is nil on it. A test using this
// MUST clean up after itself, because the rollback guarantee does not apply.
func (h *H) PoolRepos() *repo.Repos { return repo.New(h.Pool) }

// ExpectError runs fn inside a SAVEPOINT and requires it to return a non-nil
// error, returning that error for the caller to inspect.
//
// This exists because of a property of Postgres transactions that quietly breaks
// the obvious way to write an integration test. An error inside a transaction
// POISONS it: the transaction enters the aborted state and every subsequent
// command fails with 25P02 "current transaction is aborted", regardless of
// what it is. So a test that asserts "this insert must violate the unique
// constraint" passes its first assertion and then cannot run anything else —
// including the assertions that would tell you whether the error was the right
// kind.
//
// A savepoint scopes the damage: the error aborts to the savepoint, ROLLBACK TO
// restores a usable transaction, and the rest of the test runs normally.
//
// The returned error is the one fn produced, so the caller can assert on its
// SQLSTATE. That distinction matters: a test that only checked "an error
// happened" would also pass when the statement failed for an unrelated reason —
// a missing NOT NULL, a dropped column, a typo in the fixture.
func (h *H) ExpectError(t *testing.T, what string, fn func() error) error {
	t.Helper()
	ctx := context.Background()

	if _, err := h.tx.Exec(ctx, "SAVEPOINT expect_error"); err != nil {
		t.Fatalf("SAVEPOINT: %v", err)
	}
	err := fn()
	// ROLLBACK TO happens whether or not fn failed. Rolling back a savepoint
	// that was never aborted is legal and is a no-op, so this is unconditional
	// rather than branching on the error.
	if _, rbErr := h.tx.Exec(ctx, "ROLLBACK TO SAVEPOINT expect_error"); rbErr != nil {
		t.Fatalf("ROLLBACK TO SAVEPOINT after %s: %v", what, rbErr)
	}
	if _, relErr := h.tx.Exec(ctx, "RELEASE SAVEPOINT expect_error"); relErr != nil {
		t.Fatalf("RELEASE SAVEPOINT after %s: %v", what, relErr)
	}
	if err == nil {
		t.Fatalf("%s: expected an error, got nil", what)
	}
	return err
}

// Ctx is a context bounded to a generous timeout, so a hung query fails the
// test rather than the suite.
func (h *H) Ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// Exec runs a statement directly, for arranging state a repo method cannot.
func (h *H) Exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := h.tx.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", firstLine(sql), err)
	}
}

// Query runs a SELECT inside the test's transaction and hands the rows back for
// scanning. QueryRow covers the single-row case; this is for the rest.
//
// It binds to the same transaction as Repos, so it sees uncommitted fixture rows -
// which h.Pool would not, since those live only inside that transaction.
func (h *H) Query(t *testing.T, sql string, args ...any) (pgx.Rows, error) {
	t.Helper()
	return h.tx.Query(context.Background(), sql, args...)
}

// QueryRow runs a single-row query directly.
func (h *H) QueryRow(t *testing.T, sql string, args ...any) pgx.Row {
	t.Helper()
	return h.tx.QueryRow(context.Background(), sql, args...)
}

func (h *H) cleanup() {
	ctx := context.Background()
	if h.Pool != nil {
		h.Pool.Close()
		h.Pool = nil
	}
	if h.Admin != nil {
		// FORCE because the pool may still hold a socket open for a moment after
		// Close, and a DROP that fails on "being accessed by other users" would
		// leak a database per test.
		_, _ = h.Admin.Exec(ctx, "DROP DATABASE IF EXISTS "+h.dbName+" WITH (FORCE)")
		h.Admin.Close()
		h.Admin = nil
	}
}

// Cleanup removes a database created by a test that needed one by hand.
func Cleanup(t *testing.T, dsn, name string) {
	t.Helper()
	ctx := context.Background()
	p, err := connect(ctx, dsn, "postgres")
	if err != nil {
		return
	}
	defer p.Close()
	_, _ = p.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
}

// sanitize turns a test name into something usable as a database name:
// lowercase, underscores, and short enough to stay under the 63-byte limit.
func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, []rune(strings.ToLower(string(r)))...)
		default:
			out = append(out, '_')
		}
		if len(out) >= 28 {
			break
		}
	}
	return string(out)
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
