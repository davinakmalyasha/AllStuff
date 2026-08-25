package security

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestTOTPValidation(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 {
		t.Errorf("expected 32-char base32 secret, got %d", len(secret))
	}
	code, err := totpCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Errorf("expected 6-digit code, got %q", code)
	}
	if !ValidateTOTP(secret, code) {
		t.Error("valid code rejected")
	}
	if ValidateTOTP(secret, "000000") {
		t.Error("invalid code accepted")
	}
	if ValidateTOTP(secret, strings.Repeat("1", 5)) {
		t.Error("5-digit code accepted")
	}
}

func TestVAPIDKeypair(t *testing.T) {
	kp, err := GenerateVAPIDKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if kp.PublicKey == "" {
		t.Error("public key empty")
	}
	jwt, err := kp.vapidJWT("mailto:test@example.com", "https://fcm.googleapis.com")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("malformed VAPID JWT: %q", jwt)
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), "ES256") {
		t.Errorf("expected ES256 alg in header, got %s", header)
	}
}

func TestCSRFValid(t *testing.T) {
	token := "abc123"
	if !CSRFValid(token, token) {
		t.Error("matching values should validate")
	}
	if CSRFValid(token, "abc124") {
		t.Error("mismatched values should not validate")
	}
	if CSRFValid("", token) || CSRFValid(token, "") {
		t.Error("empty values should not validate")
	}
}
