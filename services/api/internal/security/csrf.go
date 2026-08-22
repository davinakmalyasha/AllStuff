package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// Double-submit CSRF token (PRD §5.9.1, §9.3): random value in cookie,
// must match X-CSRF-Token header on mutating requests.
const CSRFHeader = "X-CSRF-Token"

func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func CSRFValid(cookieValue, headerValue string) bool {
	if cookieValue == "" || headerValue == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieValue), []byte(headerValue)) == 1
}
