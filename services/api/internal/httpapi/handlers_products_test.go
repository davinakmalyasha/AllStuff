//go:build !race

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
	"allstuff/api/internal/util"
)

// The product and storefront handlers had no HTTP test. Two of them open their
// own pool transaction (Publish and ReplaceVariants/Duplicate), so this suite
// runs on the pool harness rather than the rollback transaction.
//
// The service layer has its own tests; what can only break here is the wiring -
// request decoding, which status a rejected write produces, and whether the
// path id is passed through unchanged.

func TestProductRoutes(t *testing.T) {
	s, h, td := newDBTestServerPool(t)
	hdl := s.Handler()
	owner := testutil.User(t, h)
	stranger := testutil.User(t, h)
	cat := testutil.Category(t, h, "Product Category")
	b := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Product Coffee", Slug: "product-coffee", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Status: domain.BusinessVerified,
	})

	do := func(t *testing.T, method, path string, body any, u *domain.User) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw = jsonBody(t, body)
		}
		var req *http.Request
		if u == nil {
			req = authReq(t, method, path, raw)
		} else {
			req = td.authed(t, method, path, raw, u)
		}
		w := httptest.NewRecorder()
		hdl.ServeHTTP(w, req)
		return w
	}
	decode := func(t *testing.T, w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if w.Code < 200 || w.Code >= 300 {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatalf("not JSON: %v: %s", err, w.Body.String())
		}
	}
	type product struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Currency    string   `json:"currency"`
		IsPublished bool     `json:"is_published"`
		ImageIDs    []string `json:"image_ids"`
		Variants    []struct {
			ID  string `json:"id"`
			SKU string `json:"sku"`
		} `json:"variants"`
	}
	type productRes struct {
		Product product `json:"product"`
	}

	productsPath := "/api/v1/businesses/" + b.ID + "/products"
	if w := do(t, http.MethodGet, productsPath, nil, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous product list: status = %d, want 401", w.Code)
	}
	if w := do(t, http.MethodGet, productsPath, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger product list: status = %d, want 403: %s", w.Code, w.Body.String())
	}

	// ---- create ----
	var res productRes
	w := do(t, http.MethodPost, productsPath, map[string]any{
		"name": "Pour Over", "base_price": 4.5, "currency": "idr",
	}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("create product: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &res)
	productID := res.Product.ID
	if productID == "" || res.Product.Name != "Pour Over" {
		t.Fatalf("created product = %+v", res.Product)
	}
	// A 3-letter currency is normalised; the database CHECK expects uppercase.
	if res.Product.Currency != "IDR" {
		t.Errorf("currency = %q, want IDR", res.Product.Currency)
	}
	if res.Product.IsPublished {
		t.Error("a new product was created published")
	}
	// A one-character name is rejected rather than stored.
	if w := do(t, http.MethodPost, productsPath, map[string]any{"name": "x"}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("short name: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// An omitted name gets the documented default, not an empty row.
	w = do(t, http.MethodPost, productsPath, map[string]any{}, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("unnamed product: status = %d: %s", w.Code, w.Body.String())
	}
	var unnamed productRes
	decode(t, w, &unnamed)
	if unnamed.Product.Name != "Untitled product" {
		t.Errorf("unnamed product name = %q", unnamed.Product.Name)
	}

	var list struct {
		Products []product `json:"products"`
	}
	decode(t, do(t, http.MethodGet, productsPath, nil, owner), &list)
	if len(list.Products) != 2 {
		t.Errorf("product list = %d rows, want 2", len(list.Products))
	}

	// ---- update ----
	productPath := "/api/v1/products/" + productID
	for name, body := range map[string]map[string]any{
		"empty":        {},
		"bad badge":    {"badge": "gold"},
		"negative":     {"base_price": -1},
		"long name":    {"name": string(make([]rune, 101))},
		"bad currency": {"currency": "RUPEES"},
	} {
		if w := do(t, http.MethodPatch, productPath, body, owner); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", name, w.Code, w.Body.String())
		}
	}
	if w := do(t, http.MethodPatch, productPath, map[string]any{"name": "Renamed by a stranger"}, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger update: status = %d, want 403", w.Code)
	}
	w = do(t, http.MethodPatch, productPath, map[string]any{
		"name": "Pour Over V2", "base_price": 5.0, "tags": []string{"coffee"},
	}, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("update product: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &res)
	if res.Product.Name != "Pour Over V2" {
		t.Errorf("updated name = %q", res.Product.Name)
	}

	// ---- variants ----
	variantsPath := productPath + "/variants"
	if w := do(t, http.MethodPut, variantsPath, map[string]any{
		"options":  []map[string]any{{"name": "Size", "values": []string{"S", "M"}}},
		"variants": []map[string]any{{"name": "Small", "price": 4.0}},
	}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("variant without SKU: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	w = do(t, http.MethodPut, variantsPath, map[string]any{
		"options": []map[string]any{{"name": "Size", "values": []string{"S", "M"}}},
		"variants": []map[string]any{{
			"name": "Small", "sku": "POUR-S", "price": 4.0, "stock_qty": 2, "in_stock": true,
			"options": map[string]any{"Size": "S"},
		}},
	}, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("replace variants: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &res)
	if len(res.Product.Variants) != 1 || res.Product.Variants[0].SKU != "POUR-S" {
		t.Errorf("variants = %+v", res.Product.Variants)
	}
	if w := do(t, http.MethodPut, variantsPath, map[string]any{
		"variants": []map[string]any{{"name": "Duplicate", "sku": "POUR-S", "price": 4.0}},
	}, owner); w.Code != http.StatusBadRequest {
		t.Errorf("duplicate SKU: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// ---- publish ----
	// No image yet: publishing must be refused, because a card without one is
	// not renderable.
	if w := do(t, http.MethodPost, productPath+"/publish", nil, owner); w.Code != http.StatusBadRequest {
		t.Errorf("publish without an image: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPatch, productPath, map[string]any{
		"image_ids": []string{util.NewUUID()},
	}, owner); w.Code != http.StatusOK {
		t.Fatalf("set product image: status = %d: %s", w.Code, w.Body.String())
	}
	w = do(t, http.MethodPost, productPath+"/publish", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("publish: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &res)
	if !res.Product.IsPublished {
		t.Error("publish did not set is_published")
	}
	if w := do(t, http.MethodPost, productPath+"/unpublish", nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger unpublish: status = %d, want 403", w.Code)
	}
	w = do(t, http.MethodPost, productPath+"/unpublish", nil, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("unpublish: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &res)
	if res.Product.IsPublished {
		t.Error("unpublish did not clear is_published")
	}

	// ---- duplicate ----
	w = do(t, http.MethodPost, productPath+"/duplicate", nil, owner)
	if w.Code != http.StatusCreated {
		t.Fatalf("duplicate: status = %d: %s", w.Code, w.Body.String())
	}
	var dup productRes
	decode(t, w, &dup)
	if dup.Product.ID == "" || dup.Product.ID == productID {
		t.Errorf("duplicate id = %q, want a new row", dup.Product.ID)
	}

	// ---- stock alerts ----
	alertPath := "/api/v1/products/" + productID + "/stock-alert"
	var alert struct {
		Alerted bool `json:"alerted"`
	}
	decode(t, do(t, http.MethodPut, alertPath, nil, stranger), &alert)
	if !alert.Alerted {
		t.Error("PUT stock alert did not report alerted=true")
	}
	decode(t, do(t, http.MethodGet, alertPath, nil, stranger), &alert)
	if !alert.Alerted {
		t.Error("GET stock alert lost the subscription")
	}
	decode(t, do(t, http.MethodDelete, alertPath, nil, stranger), &alert)
	if alert.Alerted {
		t.Error("DELETE stock alert did not report alerted=false")
	}
	// A product id that does not exist is a 404, not the FK violation the
	// insert would otherwise raise.
	if w := do(t, http.MethodPut, "/api/v1/products/"+util.NewUUID()+"/stock-alert", nil, stranger); w.Code != http.StatusNotFound {
		t.Errorf("stock alert for an unknown product: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// ---- storefront ----
	storefrontPath := "/api/v1/businesses/" + b.ID + "/storefront"
	w = do(t, http.MethodPut, storefrontPath, map[string]any{
		"theme":  map[string]any{"accent": "#123456"},
		"layout": map[string]any{"hero": true, "sections": []string{"about", "products"}},
	}, owner)
	if w.Code != http.StatusOK {
		t.Fatalf("storefront update: status = %d: %s", w.Code, w.Body.String())
	}
	var biz struct {
		Business struct {
			Theme map[string]any `json:"theme"`
		} `json:"business"`
	}
	decode(t, w, &biz)
	if biz.Business.Theme["accent"] != "#123456" {
		t.Errorf("storefront theme = %+v", biz.Business.Theme)
	}
	if w := do(t, http.MethodPut, storefrontPath, map[string]any{
		"theme": map[string]any{"accent": "#000000"},
	}, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger storefront update: status = %d, want 403", w.Code)
	}

	// ---- business lifecycle ----
	lifecycle := func(action string) *httptest.ResponseRecorder {
		return do(t, http.MethodPost, "/api/v1/businesses/"+b.ID+"/"+action, nil, owner)
	}
	var businessRes struct {
		Business struct {
			Status string `json:"status"`
		} `json:"business"`
	}
	if w := lifecycle("publish"); w.Code != http.StatusOK {
		t.Fatalf("publish business: status = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, http.MethodPost, "/api/v1/businesses/"+b.ID+"/pause", nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger pause: status = %d, want 403", w.Code)
	}
	w = lifecycle("pause")
	if w.Code != http.StatusOK {
		t.Fatalf("pause: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &businessRes)
	if businessRes.Business.Status != "paused" {
		t.Errorf("status after pause = %q", businessRes.Business.Status)
	}
	w = lifecycle("reopen")
	if w.Code != http.StatusOK {
		t.Fatalf("reopen: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &businessRes)
	if businessRes.Business.Status != "verified" {
		t.Errorf("status after reopen = %q", businessRes.Business.Status)
	}
	// Reopening a business that is not paused is a validation error, not a
	// silent no-op.
	if w := lifecycle("reopen"); w.Code != http.StatusBadRequest {
		t.Errorf("reopen twice: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// ---- analytics ----
	analyticsPath := "/api/v1/businesses/" + b.ID + "/analytics"
	if w := do(t, http.MethodGet, analyticsPath, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger analytics: status = %d, want 403", w.Code)
	}
	// An unknown period falls back to the default instead of reaching SQL.
	if w := do(t, http.MethodGet, analyticsPath+"?period=99y", nil, owner); w.Code != http.StatusOK {
		t.Errorf("analytics with a bad period: status = %d: %s", w.Code, w.Body.String())
	}

	// ---- close ----
	// Close is last: it soft-deletes the listing.
	w = lifecycle("close")
	if w.Code != http.StatusOK {
		t.Fatalf("close: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, w, &businessRes)
	if businessRes.Business.Status != "closed" {
		t.Errorf("status after close = %q", businessRes.Business.Status)
	}
	// The public page stops resolving the retired slug.
	if w := do(t, http.MethodGet, "/api/v1/b/"+b.Slug, nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("a closed business is still public: status = %d", w.Code)
	}

	// ---- delete ----
	if w := do(t, http.MethodDelete, productPath, nil, stranger); w.Code != http.StatusForbidden {
		t.Errorf("stranger delete: status = %d, want 403", w.Code)
	}
	if w := do(t, http.MethodDelete, productPath, nil, owner); w.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d: %s", w.Code, w.Body.String())
	}
	decode(t, do(t, http.MethodGet, productsPath, nil, owner), &list)
	for _, p := range list.Products {
		if p.ID == productID {
			t.Error("a deleted product is still listed")
		}
	}
}
