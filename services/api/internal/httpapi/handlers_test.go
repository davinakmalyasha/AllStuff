package httpapi

import "testing"

func TestSlugSanity(t *testing.T) {
	// Used by slugs in compare URLs etc.
	if !validIDish("5845a5db-02b0-4ce8-a6d1-95f2b4b16396") {
		t.Error("uuid should be valid")
	}
	if validIDish("") {
		t.Error("empty should be invalid")
	}
}

func validIDish(s string) bool {
	return len(s) == 36
}
