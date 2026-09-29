package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// pgxpool's default of max(4, NumCPU) connections starves the whole API
	// behind a handful of concurrent requests once the N+1-prone endpoints
	// (search, fan-outs) queue up. Size explicitly; per-URL ?pool_max_conns=
	// still overrides for exotic deployments.
	if cfg.MaxConns < 32 {
		cfg.MaxConns = 32
	}
	if cfg.MinConns == 0 {
		cfg.MinConns = 4
	}
	if cfg.MaxConnLifetime <= 0 {
		cfg.MaxConnLifetime = 30 * time.Minute
	}
	if cfg.MaxConnIdleTime <= 0 {
		cfg.MaxConnIdleTime = 5 * time.Minute
	}
	if cfg.HealthCheckPeriod <= 0 {
		cfg.HealthCheckPeriod = time.Minute
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate applies *.sql files in lexical order, tracked in schema_migrations (PRD §10.5).
// A session-level advisory lock guards the whole run: two replicas booting
// together previously raced file application (duplicate-object errors, or
// worse interleaved DDL).
func Migrate(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate lock acquire: %w", err)
	}
	defer conn.Release()
	// pg_advisory_lock returns void and blocks until held — session-scoped,
	// released on the same connection.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('bizverse:migrate'))`); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('bizverse:migrate'))`)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, f := range files {
		version := filepath.Base(f)
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}

		body, err := os.ReadFile(f)
		if err != nil {
			return err
		}

		// Statements that must run OUTSIDE the migration transaction execute
		// after the TX commits, on autocommit connections. Two classes:
		//
		//   1. CREATE/DROP INDEX CONCURRENTLY — Postgres forbids these inside
		//      a transaction. Detected by the literal marker, which is also
		//      what makes them show up in `splitStatements` as their own unit.
		//   2. Anything tagged `/* autocommit */` — see autocommitTag. This
		//      exists because ordering between a replacement constraint and
		//      the constraint it replaces is load-bearing: 0029 builds
		//      businesses_slug_live and only then may drop businesses_slug_key,
		//      because dropping first leaves a window in which `slug` has NO
		//      uniqueness at all. A substring heuristic cannot express "after",
		//      so the ordering has to be explicit in the file.
		stmts := SplitStatements(string(body))
		var txStmts, concStmts []string
		for _, stmt := range stmts {
			if isAutocommit(stmt) {
				concStmts = append(concStmts, stmt)
			} else {
				txStmts = append(txStmts, stmt)
			}
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		for _, stmt := range txStmts {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("%s: %w", version, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		for _, stmt := range concStmts {
			// Autocommit on the pooled connection: no explicit TX wrapper.
			if _, err := conn.Exec(context.WithoutCancel(ctx), stmt); err != nil {
				return fmt.Errorf("%s (concurrent): %w", version, err)
			}
		}
		// Version marker only AFTER concurrent statements succeed: a failed
		// CREATE INDEX CONCURRENTLY leaves no index but must not burn the
		// file — returning here leaves it unrecorded so the next boot
		// retries (the IF NOT EXISTS clauses in 0016/0027 make reruns safe).
		if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return err
		}
	}

	// Every recorded migration is now believed applied. Before returning, prove
	// that one specific way of "applied" is not a lie.
	//
	// A CREATE INDEX CONCURRENTLY that fails leaves an INVALID index behind:
	// pg_class gets a row, pg_index.indisvalid stays false. `IF NOT EXISTS`
	// matches on NAME and does not check validity, so every subsequent boot
	// skips the build and the invariant the index was added to enforce is
	// silently absent forever. 0029's five UNIQUE indexes exist to make
	// invariants unbreakable; an INVALID one makes them merely decorative.
	//
	// This is a hard startup failure rather than a warning on purpose. The
	// alternative — a degraded mode nobody is paged for — is exactly how an
	// index that never got built stays unnoticed until an incident.
	if bad, err := InvalidIndexes(ctx, conn); err != nil {
		return fmt.Errorf("migrate: index validity check: %w", err)
	} else if len(bad) > 0 {
		return fmt.Errorf("migrate: %d invalid index(es) present — an earlier CREATE INDEX CONCURRENTLY "+
			"failed and IF NOT EXISTS has been skipping the rebuild ever since. Drop and rebuild each: %v",
			len(bad), bad)
	}
	return nil
}

// InvalidIndexes returns every index in the public schema whose build did not
// complete. A non-empty result is always an operator-actionable condition; see
// the recovery procedure in docs/RUNBOOK.md.
func InvalidIndexes(ctx context.Context, conn *pgxpool.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT n.nspname || '.' || c.relname
		  FROM pg_index i
		  JOIN pg_class c     ON c.oid = i.indexrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public'
		   AND NOT i.indisvalid
		 ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// autocommitTag marks a statement that must run outside the migration
// transaction even though it is not an INDEX CONCURRENTLY statement.
//
// It is a SQL block comment, not a `--` comment, on purpose: splitStatements
// DROPS whole-line `--` comments before they ever reach a statement, so a
// `--`-based marker would be invisible by the time the router looks for it. A
// block comment survives splitting, is valid SQL to Postgres, and reads as an
// instruction to a human skimming the migration.
//
// Put it on the same line as the statement, BEFORE it:
//
//	/* autocommit */ ALTER TABLE businesses DROP CONSTRAINT IF EXISTS businesses_slug_key;
const autocommitTag = "/* autocommit */"

// isAutocommit reports whether a statement must be executed on autocommit
// rather than inside the migration transaction.
func isAutocommit(stmt string) bool {
	if strings.Contains(stmt, " INDEX CONCURRENTLY ") ||
		strings.Contains(stmt, " INDEX CONCURRENTLY\n") {
		return true
	}
	return strings.Contains(stmt, autocommitTag)
}

// SplitStatements splits SQL into executable statements.
//
// Exported because internal/db's own replay test and the migration linter must
// see exactly the boundaries the runner sees. A checker that re-implemented
// this would be checking a fiction — the whole point is that it is the same
// function.
//
// Rules, which every migration in this repository must obey:
//
//   - A statement ends only when the accumulated line, trimmed, ends with ';'.
//     A ';' anywhere else (inside a string literal, mid-line) does NOT split.
//     Consequence: never end a line with a string literal whose final character
//     is ';'.
//   - Whole-line '--' comments are dropped before parsing. They are for
//     humans; the `-- migrate:` header block is read from the raw file by
//     ParseHeader, not from the split output.
//   - '$$' and '$tag$' bodies are tracked, so a ';' inside plpgsql is safe.
//   - '--' starting mid-line is preserved into the statement, so a trailing
//     '--' comment on a line that also ends in ';' SUPPRESSES the split. Put
//     every comment on its own line.
func SplitStatements(sql string) []string {
	var stmts []string
	var cur strings.Builder
	inDollar := false
	inLineComment := false

	lines := strings.Split(sql, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue // whole-line comment
		}
		inLineComment = false
		for i := 0; i < len(line); i++ {
			ch := line[i]
			switch {
			case inLineComment:
				cur.WriteByte(ch)
			case ch == '-' && i+1 < len(line) && line[i+1] == '-':
				inLineComment = true
				cur.WriteString("--")
				i++
			case ch == '$':
				// $$ or $tag$ opens/closes a dollar-quoted body; $1 is a parameter, not a quote.
				if i+1 < len(line) && line[i+1] == '$' {
					inDollar = !inDollar
					cur.WriteString("$$")
					i++
					continue
				}
				if i+1 < len(line) && isQuoteChar(line[i+1]) {
					end := i + 2
					for end < len(line) && line[end] != '$' {
						end++
					}
					if end < len(line) {
						inDollar = !inDollar
						cur.WriteString(line[i : end+1])
						i = end
						continue
					}
				}
				cur.WriteByte(ch)
			default:
				cur.WriteByte(ch)
			}
		}
		cur.WriteByte('\n')
		if !inDollar && strings.HasSuffix(strings.TrimSpace(cur.String()), ";") {
			stmt := strings.TrimSpace(cur.String())
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
			cur.Reset()
		}
	}
	stmt := strings.TrimSpace(cur.String())
	if stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}

// isQuoteChar: a dollar quote starts with $$ or $tag$ (letters/underscore after $).
func isQuoteChar(c byte) bool {
	return c == '$' || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
