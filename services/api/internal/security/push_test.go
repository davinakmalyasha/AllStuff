package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"net"
	"testing"
)

// Regression (audit S4 hardening): CGNAT and reserved ranges must not pass
// as public — a push endpoint resolving into 100.64.0.0/10 previously passed
// the subscribe-time check.
//
// This predicate now also guards the link-preview fetcher. That path carried its
// own copy, which never received the S4 hardening: it answered true for a nil
// address and let CGNAT through. The duplicate is gone, so these cases cover
// both callers.
func TestIsPublicIP(t *testing.T) {
	private := []string{
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.9",
		"100.64.0.1", "100.127.255.254", // CGNAT
		"169.254.1.1",              // link-local
		"198.18.0.5", "192.0.2.99", // benchmark / documentation
		"224.0.0.5", "240.0.0.1",

		// The cloud metadata endpoint, which is the canonical SSRF target: it
		// serves instance credentials on the major clouds.
		"169.254.169.254",

		// IPv6 and its IPv4-mapped forms. The mapped ones are the usual way past
		// a check that forgets to normalise through To4.
		"::1", "fe80::1", "fc00::1", "fd00::1", "::", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1",
	}
	for _, s := range private {
		if IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be public", s)
		}
	}

	// A nil address is not dialable. The deleted service copy answered true
	// here, so an unparseable host would have been treated as safe.
	if IsPublicIP(nil) {
		t.Error("a nil address must not be public")
	}

	public := []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111",
		// One step outside each blocked range. If the checks are ever widened by
		// accident, the failure lands here rather than in production, where
		// rejecting these would silently break real previews and push endpoints.
		"169.253.255.255", "172.32.0.1", "::ffff:8.8.8.8",
	}
	for _, s := range public {
		if !IsPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should be public", s)
		}
	}
}

// Regression: a VAPID keypair whose advertised public key does not match the
// private key used to fail silently at send time; ParseVAPIDKeypair now
// rejects it at boot.
func TestParseVAPIDKeypairMismatch(t *testing.T) {
	kp, err := GenerateVAPIDKeypair()
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub := elliptic.Marshal(elliptic.P256(), other.PublicKey.X, other.PublicKey.Y)
	privDER, err := x509.MarshalPKCS8PrivateKey(kp.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	privB64 := base64.RawURLEncoding.EncodeToString(privDER)
	if _, err := ParseVAPIDKeypair(base64.RawURLEncoding.EncodeToString(otherPub), privB64); err == nil {
		t.Error("mismatched public/private VAPID keys must be rejected")
	}
	if _, err := ParseVAPIDKeypair(kp.PublicKey, privB64); err != nil {
		t.Errorf("matching keypair rejected: %v", err)
	}
}
