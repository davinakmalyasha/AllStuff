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
func TestIsPublicIP(t *testing.T) {
	private := []string{
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.9",
		"100.64.0.1", "100.127.255.254", // CGNAT
		"169.254.1.1",            // link-local
		"198.18.0.5", "192.0.2.99", // benchmark / documentation
		"224.0.0.5", "240.0.0.1",
	}
	for _, s := range private {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"}
	for _, s := range public {
		if !isPublicIP(net.ParseIP(s)) {
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
