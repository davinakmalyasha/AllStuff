package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
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
func Migrate(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
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
		if err := pool.QueryRow(ctx,
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

		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		// Single-statement execution: split on semicolons at line ends, keep comments/DO blocks intact.
		for _, stmt := range splitStatements(string(body)) {
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
