package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

type listDTO struct {
	ID    string   `json:"id"`
	Tags  []string `json:"tags"`
	Notes []int    `json:"notes"`
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, v)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// The regression: a business with no comments must not crash the page that
// renders it. `{"comments":null}` made the frontend read `.length` on null.
func TestNilSlicesMarshalAsEmptyArrays(t *testing.T) {
	body := mustJSON(t, map[string]any{
		"comments": nil,
		"products": []*listDTO{},
		"nested":   map[string]any{"similar": nil},
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if got["comments"] != nil {
		t.Errorf("comments = %v, want [] (got %s)", got["comments"], body)
	}
	if arr, ok := got["products"].([]any); !ok || len(arr) != 0 {
		t.Errorf("products = %#v, want []", got["products"])
	}
	nested, _ := got["nested"].(map[string]any)
	if nested["similar"] != nil {
		t.Errorf("nested.similar = %v, want []", nested["similar"])
	}
}

// The exact shape a handler builds: a typed nil slice stored in an `any`. The
// interface is non-nil while the slice inside it is nil, so a nil-check on the
// interface does not catch it — this was the case that kept `products: null`
// reaching the client after the first fix.
func TestTypedNilSliceInAnyBecomesEmptyArray(t *testing.T) {
	var nilProducts []*listDTO
	body := mustJSON(t, map[string]any{"products": nilProducts})
	if !contains(body, `"products":[]`) {
		t.Errorf("typed nil slice in any must marshal as [], got %s", body)
	}
}

func TestNestedTypedNilSlicesAreReplaced(t *testing.T) {
	var nilTags []string
	var nilRows []*listDTO
	body := mustJSON(t, map[string]any{
		"business": map[string]any{"tags": nilTags},
		"rows":     nilRows,
	})
	var got struct {
		Business struct{ Tags []string } `json:"business"`
		Rows     []listDTO               `json:"rows"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if got.Business.Tags == nil {
		t.Errorf("nested tags = nil, want [] (%s)", body)
	}
	if got.Rows == nil {
		t.Errorf("rows = nil, want [] (%s)", body)
	}
}

// A struct with nil slice fields must be normalized too — this is the shape the
// repo layer actually returns.
func TestNilStructFieldsMarshalAsEmptyArrays(t *testing.T) {
	body := mustJSON(t, listDTO{ID: "x"})
	if !contains(body, `"tags":[]`) || !contains(body, `"notes":[]`) {
		t.Errorf("want empty arrays, got %s", body)
	}
}

// Non-empty slices must survive untouched, and pointer fields must stay null
// rather than being coerced — clients distinguish absent from empty.
func TestNormalizationPreservesValuesAndNullPointers(t *testing.T) {
	type row struct {
		Name    *string   `json:"name"`
		Tags    []string  `json:"tags"`
		Missing *[]string `json:"missing"`
	}
	body := mustJSON(t, map[string]any{
		"rows": []row{{Name: nil, Tags: []string{"a", "b"}, Missing: nil}},
	})
	var got struct {
		Rows []struct {
			Name    *string   `json:"name"`
			Tags    []string  `json:"tags"`
			Missing *[]string `json:"missing"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %#v", got.Rows)
	}
	if got.Rows[0].Name != nil {
		t.Error("Name pointer should stay null")
	}
	if got.Rows[0].Missing != nil {
		t.Error("Missing pointer-to-slice should stay null, not become []")
	}
	if len(got.Rows[0].Tags) != 2 || got.Rows[0].Tags[0] != "a" {
		t.Errorf("Tags = %#v, want [a b]", got.Rows[0].Tags)
	}
}

// time.Time is a struct with unexported fields; the normalizer must not corrupt
// or infinite-loop on it.
func TestNormalizationHandlesUnexportedStructFields(t *testing.T) {
	type payload struct {
		At   any      `json:"at"`
		List []string `json:"list"`
	}
	body := mustJSON(t, payload{At: nil, List: nil})
	if !contains(body, `"list":[]`) {
		t.Errorf("got %s", body)
	}
	if !contains(body, `"at":null`) {
		t.Errorf("nil interface must stay null, got %s", body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
