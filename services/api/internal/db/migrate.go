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

		// Statements using CREATE/DROP INDEX CONCURRENTLY must run OUTSIDE
		// the migration transaction (Postgres forbids it inside one). They
		// execute after the TX commits, on autocommit connections; a failure
		// aborts the migration run.
		stmts := splitStatements(string(body))
		var txStmts, concStmts []string
		for _, stmt := range stmts {
			if strings.Contains(stmt, " INDEX CONCURRENTLY ") ||
				strings.Contains(stmt, " INDEX CONCURRENTLY\n") {
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
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
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
	}
	return nil
}

// splitStatements splits SQL on ';' at end-of-line, ignoring semicolons inside
// dollar-quoted bodies ($$ or $tag$, e.g. the plpgsql trigger function).
func splitStatements(sql string) []string {
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
