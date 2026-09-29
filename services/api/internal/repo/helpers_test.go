package repo_test

import (
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// nowUTC matches what service.Businesses.Close writes, so a test that retires a
// listing directly through the repository produces the same row state the
// product produces through the service.
func nowUTC() time.Time { return time.Now().UTC() }

// isUniqueViolation reports whether an error is Postgres 23505.
//
// Asserting the specific SQLSTATE rather than "some error" is what turns a test
// from "the insert failed" into "the invariant is enforced". A bare non-nil check
// also passes when the insert fails for an unrelated reason — a missing NOT
// NULL, a dropped column, a typo in the fixture — and those are exactly the
// failures that are hard to diagnose later.
func isUniqueViolation(err error) bool {
	return pgErrCode(err) == "23505"
}

func isForeignKeyViolation(err error) bool {
	return pgErrCode(err) == "23503"
}

func isCheckViolation(err error) bool {
	return pgErrCode(err) == "23514"
}

func isNotNullViolation(err error) bool {
	return pgErrCode(err) == "23502"
}

func isInvalidTextRepresentation(err error) bool {
	return pgErrCode(err) == "22P02"
}

func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	// pgx sometimes wraps the driver error in a way that loses the typed
	// pointer when the error crosses a package boundary; fall back to the
	// message, which always carries the code.
	if err == nil {
		return ""
	}
	if i := strings.Index(err.Error(), "SQLSTATE "); i >= 0 {
		rest := err.Error()[i+len("SQLSTATE "):]
		if len(rest) >= 5 {
			return rest[:5]
		}
	}
	return ""
}
