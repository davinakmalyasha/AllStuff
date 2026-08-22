package httpapi

import (
	"net/http"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/service"
)

// ---- products (PRD §5.4.3) ----

func (s *Server) handleProductList(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	list, err := s.deps.Products.List(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"products": list})
}

func (s *Server) handleProductCreate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.ProductInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := s.deps.Products.Create(r.Context(), user.ID, r.PathValue("id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"product": p})
}

func (s *Server) handleProductUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.ProductInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := s.deps.Products.Update(r.Context(), user.ID, r.PathValue("productId"), in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"product": p})
}

func (s *Server) handleProductReplaceVariants(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in service.ReplaceVariantsInput
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := s.deps.Products.ReplaceVariants(r.Context(), user.ID, r.PathValue("productId"), in)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"product": p})
}

func (s *Server) handleProductPublish(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	p, err := s.deps.Products.Publish(r.Context(), user.ID, r.PathValue("productId"), true)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"product": p})
}

func (s *Server) handleProductUnpublish(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	p, err := s.deps.Products.Publish(r.Context(), user.ID, r.PathValue("productId"), false)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"product": p})
}

func (s *Server) handleProductDelete(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	if err := s.deps.Products.Delete(r.Context(), user.ID, r.PathValue("productId")); err != nil {
		fail(w, err)
		return
	}
	noContent(w)
}

func (s *Server) handleProductDuplicate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	p, err := s.deps.Products.Duplicate(r.Context(), user.ID, r.PathValue("productId"))
	if err != nil {
		fail(w, err)
		return
	}
	created(w, map[string]any{"product": p})
}

// ---- back-in-stock alerts (PRD §5.7) ----

func (s *Server) handleStockAlert(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	productID := r.PathValue("productId")
	if r.Method == http.MethodGet {
		on, err := s.deps.Products.StockAlerted(r.Context(), user.ID, productID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{"alerted": on})
		return
	}
	on := r.Method == http.MethodPut
	if err := s.deps.Products.ToggleStockAlert(r.Context(), user.ID, productID, on); err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"alerted": on})
}

func derefMap(m *map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return *m
}

// ---- storefront (PRD §5.4.2) ----

func (s *Server) handleStorefrontUpdate(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	var in struct {
		Theme  *map[string]any `json:"theme"`
		Layout *map[string]any `json:"layout"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	b, err := s.deps.Businesses.UpdateStorefront(r.Context(), user.ID, r.PathValue("id"), derefMap(in.Theme), derefMap(in.Layout))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessPublish(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Publish(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessUnpublish(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Unpublish(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

// ---- danger zone (PRD §5.4.4) ----

func (s *Server) handleBusinessPause(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Pause(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessReopen(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Reopen(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

func (s *Server) handleBusinessClose(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	b, err := s.deps.Businesses.Close(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, map[string]any{"business": b})
}

// ---- analytics (PRD §5.4.5) ----

func (s *Server) handleBusinessAnalytics(w http.ResponseWriter, r *http.Request) {
	user, found := currentUser(r)
	if !found {
		fail(w, domain.ErrNotAuthenticated)
		return
	}
	period := r.URL.Query().Get("period")
	if period != "7d" && period != "30d" && period != "all" {
		period = "30d"
	}
	out, err := s.deps.Analytics.ForBusiness(r.Context(), user.ID, r.PathValue("id"), period)
	if err != nil {
		fail(w, err)
		return
	}
	ok(w, out)
}
