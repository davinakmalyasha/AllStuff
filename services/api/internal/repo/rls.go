package repo

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// rls.go — request identity for row-level security (migrations 0041+0053).
//
// The allstuff_app authenticates to Postgres as the schema owner (one DSN everywhere),
// but the request path must not wield the owner's power: 0041 created the
// least-privilege `allstuff_app` role and 0053 constrained it with RLS policies keyed
// on the `allstuff_app.user_id` GUC. This file bridges the two: the HTTP middleware
// stamps the authenticated user into the request context (WithUserID), and
// the pool hooks below translate it into session state on exactly the
// connection that will serve the request.
//
// Why hooks and not SET LOCAL in a query: SET LOCAL only lives inside a
// transaction, and the repo layer issues bare-pool queries outside of one.
// SET (session scope) would leak across checkouts, so every set is paired
// with a reset in AfterRelease. A pooled connection is held exclusively
// between acquire and release, which is what makes the pairing airtight —
// provided EVERY acquire flows through these hooks (ConfigureRLS) and no
// code path issues SQL on a connection it did not acquire through the pool.
// Both hold: the pool is the only connection source, and AfterRelease always
// runs, even after errors.
//
// What runs WITHOUT the hooks (owner, RLS bypassed by design):
//   - migrations, the background jobs, the notifier, tests (testutil pools),
//     and runDevLogin. Jobs read across users constantly (digest fan-out,
//     search alerts, billing reconcile, media cleanup); forcing them through
//     per-user policies would break every fan-out. The split is by pool, not
//     by flag: main.go builds an owner Repos for jobs and a hooked Repos for
//     HTTP. There is no GUC an attacker can set to become the owner.

// ctxKeyRLSUser carries the authenticated user's id for RLS. It is separate
// from httpapi's ctxKeyUser on purpose: the pool hook lives below the HTTP
// layer and must not import it.
type ctxKeyRLSUser struct{}

// WithUserID stamps the authenticated user into ctx for the RLS hooks. Call
// it wherever the request identity is established (withAuth, alongside the
// existing user value). An empty id stamps nothing.
func WithUserID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyRLSUser{}, id)
}

// UserIDFromContext returns the stamped user, or "" for anonymous contexts
// (public reads, the signed Stripe webhook, health checks).
func UserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(ctxKeyRLSUser{}).(string); ok {
		return id
	}
	return ""
}

// validUserID guards the GUC interpolation below: SET takes no parameters,
// so only a-shaped-like-a-UUID value is ever spliced in. Authenticated ids
// come from the users table (uuid type) and always pass; anything else fails
// closed to anonymous rather than erroring the query.
func validUserID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		case c == '-' && (i == 8 || i == 13 || i == 18 || i == 23):
		default:
			return false
		}
	}
	return true
}

// ConfigureRLS installs the privilege-drop hooks on a pool config. It must
// run before pgxpool.NewWithConfig (see db.ConnectRLS). The pool still
// authenticates as the owner — SET ROLE allstuff_app is what constrains it — so no
// new DSN, password, or env var is needed anywhere.
func ConfigureRLS(cfg *pgxpool.Config) {
	cfg.BeforeAcquire = func(ctx context.Context, conn *pgx.Conn) bool {
		// Every acquisition drops to the least-privilege role first. If the
		// role or the SET fails, reject the acquisition loudly (the request
		// 500s) rather than serving it as the owner: silent bypass is the
		// failure this file exists to prevent.
		if _, err := conn.Exec(ctx, `SET ROLE allstuff_app`); err != nil {
			return false
		}
		if uid := UserIDFromContext(ctx); validUserID(uid) {
			if _, err := conn.Exec(ctx, `SET allstuff_app.user_id = '`+uid+`'`); err != nil {
				return false
			}
		}
		return true
	}
	cfg.AfterRelease = func(conn *pgx.Conn) bool {
		// Paired reset: the next holder must never observe this request's
		// role or identity. Background context — release has none to offer.
		ctx := context.Background()
		// RESET ROLE first: it restores the session user, under which the
		// GUC reset then runs.
		if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
			return false
		}
		if _, err := conn.Exec(ctx, `RESET allstuff_app.user_id`); err != nil {
			return false
		}
		return true
	}
}

// RLSUserID is a convenience for services that need the id outside a query
// (audit fields, ownership checks that predate the policy). Prefer relying on
// the policy; use this only where Go must know.
func RLSUserID(ctx context.Context) string {
	return UserIDFromContext(ctx)
}
