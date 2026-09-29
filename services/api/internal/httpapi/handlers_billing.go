package httpapi

import (
	"io"
	"net/http"
	"strings"

	"bizverse/api/internal/domain"
)

// ---- billing / subscriptions (Phase 7.1) ----
//
// Public: the pricing catalogue.
// Authenticated: the owner's subscription, checkout, invoices, portal.
// Webhook: signature-verified, no session, no CSRF (see csrfExemptPaths).

func (s *Server) handlePlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.deps.Billing.Plans(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"plans": plans, "enabled": s.deps.Billing.Enabled()})
}

// handleBillingSubscription returns the state an owner dashboard renders:
// the effective plan, the subscription row, and the resolved entitlements.
func (s *Server) handleBillingSubscription(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID, authorized := s.requireManagedBusiness(w, r, user.ID)
	if !authorized {
		return
	}
	state, err := s.deps.Billing.SubscriptionFor(r.Context(), businessID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, state)
}

// handleBillingCheckout starts a Stripe Checkout session and returns its URL.
// The business is re-authorised here rather than trusting a body-supplied id.
func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID, authorized := s.requireManagedBusiness(w, r, user.ID)
	if !authorized {
		return
	}
	var in struct {
		PlanID string `json:"plan_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	checkoutURL, err := s.deps.Billing.StartCheckout(r.Context(), businessID, user.Email, in.PlanID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"url": checkoutURL})
}

// handleBillingPortal opens the Stripe customer portal.
func (s *Server) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID, authorized := s.requireManagedBusiness(w, r, user.ID)
	if !authorized {
		return
	}
	portalURL, err := s.deps.Billing.OpenPortal(r.Context(), businessID)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"url": portalURL})
}

// handleBillingInvoices lists billing history for the managed business.
func (s *Server) handleBillingInvoices(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	businessID, authorized := s.requireManagedBusiness(w, r, user.ID)
	if !authorized {
		return
	}
	invoices, err := s.deps.Repos.Billing.ListInvoices(r.Context(), businessID, parsePositiveInt(r.URL.Query().Get("limit"), 20))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"invoices": invoices})
}

// handleBillingWebhook receives Stripe deliveries.
//
// Three deliberate deviations from the normal handler shape:
//  1. No session and no CSRF — Stripe is not a browser. Authenticity comes from
//     the signature over the raw body.
//  2. The body is read raw and bounded by a small limit. Stripe payloads are a
//     few KB; 256 KiB is generous. Signature verification is over these exact
//     bytes, so it must happen before any parsing.
//  3. A duplicate event returns 200. Stripe retries until it sees a 2xx, and the
//     idempotency ledger is what makes the duplicate safe.
func (s *Server) handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		fail(w, domain.ErrValidation.WithField("_", "Could not read webhook body."))
		return
	}
	sig := r.Header.Get("Stripe-Signature")
	if err := s.deps.Billing.HandleWebhook(r.Context(), raw, sig); err != nil {
		// A signature failure is a 400 and must not be retried by Stripe; the
		// handler wraps it as a validation error. Everything else is a 500 so
		// Stripe retries, which is what a transient DB failure needs.
		if de := domain.FromError(err); de.Status < 500 {
			fail(w, de)
			return
		}
		fail(w, err)
		return
	}
	ok(w, map[string]any{"received": true})
}

// requireManagedBusiness resolves the business a billing action applies to and
// verifies the caller may manage it.
//
// Order is deliberate: authenticate, then resolve the target, then authorise.
// Authorising before resolving would let an unauthorised caller use the
// endpoint as a business-id oracle, and resolving before authenticating has
// nothing to authorise against.
func (s *Server) requireManagedBusiness(w http.ResponseWriter, r *http.Request, userID string) (string, bool) {
	businessID := strings.TrimSpace(r.PathValue("businessId"))
	if businessID == "" {
		fail(w, domain.ErrValidation.WithField("business_id", "Business is required."))
		return "", false
	}
	// Reuses the repo predicate every other co-owner-aware path uses
	// (CanManageBusiness), so billing cannot drift from storefront/analytics/
	// chat permissions: a co-owner who may edit the catalog may also pay for it.
	can, err := s.deps.Repos.Businesses.CanManageBusiness(r.Context(), userID, businessID)
	if err != nil {
		fail(w, err)
		return "", false
	}
	if !can {
		fail(w, domain.ErrForbidden)
		return "", false
	}
	return businessID, true
}
