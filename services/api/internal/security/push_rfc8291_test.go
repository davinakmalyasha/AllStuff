package security

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
)

// The worked example from RFC 8291 sections 5 and Appendix A.
//
// Every value below is copied from the RFC. The intermediate assertions are the
// point of writing the test this way: a failure names the step that diverged,
// rather than reporting that 144 bytes came out different from 144 other bytes.
//
// Three of these trip the secret scanner's generic-api-key rule on Shannon
// entropy alone, which is what a published cryptographic vector looks like to a
// rule that cannot tell one from a leaked key. They carry inline allows rather
// than a path exception in .gitleaks.toml, for a reason recorded there: the
// top-level allowlist blocks in that file have no effect in this version of
// gitleaks, so a path exception would look like enforcement and not be any.
const (
	rfcPlaintext = "When I grow up, I want to be a watermelon"

	rfcAuthSecret = "BTBZMqHH6r4Tts7J_aSIgg" //gitleaks:allow
	rfcSalt       = "DGv6ra1nlYgDCS1FRnbzlw"

	rfcUAPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcASPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcASPublic  = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"

	rfcECDHSecret = "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs" //gitleaks:allow
	rfcPRKKey     = "Snr3JMxaHVDXHWJn5wdC52WjpCtd2EIEGBykDcZW32k" //gitleaks:allow
	rfcIKM        = "S4lYMb_L0FxCeq0WhDx813KgSYqU26kOyzWUdsXYyrg"
	rfcPRK        = "09_eUZGrsvxChDCGRCdkLiDXrReGOEVeSCdCcPBSJSc"
	rfcCEK        = "oIhVW04MRdy2XN9CiKLxTg"
	rfcNonce      = "4h_95klXJ5E_qnoN"
)

// The complete message body, base64url with the RFC's line wrapping removed.
const rfcBodyB64 = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
	"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
	"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.ReplaceAll(s, "\n", ""))
	if err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return b
}

func rfcSenderKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	priv := new(ecdsa.PrivateKey)
	priv.Curve = elliptic.P256()
	priv.D = new(big.Int).SetBytes(mustB64(t, rfcASPrivate))
	priv.PublicKey.X, priv.PublicKey.Y = priv.Curve.ScalarBaseMult(priv.D.Bytes())
	return priv
}

// Each derivation step against the RFC's own intermediate values. If the final
// body assertion fails, these say which of the four HKDF steps is wrong.
func TestWebPushKeyDerivationMatchesRFC8291(t *testing.T) {
	uaPublic := mustB64(t, rfcUAPublic)
	auth := mustB64(t, rfcAuthSecret)
	salt := mustB64(t, rfcSalt)
	asPriv := rfcSenderKey(t)

	// The key parsed back out of the private scalar must be the public key the
	// RFC prints, or the rest of the ladder is built on a typo.
	derivedPub := elliptic.Marshal(elliptic.P256(), asPriv.PublicKey.X, asPriv.PublicKey.Y)
	if !bytes.Equal(derivedPub, mustB64(t, rfcASPublic)) {
		t.Fatalf("the sender key fixture does not match the RFC's public key")
	}

	x, y := elliptic.Unmarshal(elliptic.P256(), uaPublic)
	if x == nil {
		t.Fatal("the RFC's user-agent key did not parse as a P-256 point")
	}
	shared, _ := elliptic.P256().ScalarMult(x, y, asPriv.D.Bytes())
	if got := base64.RawURLEncoding.EncodeToString(shared.Bytes()); got != rfcECDHSecret {
		t.Fatalf("ECDH secret = %s, want %s", got, rfcECDHSecret)
	}

	// RFC 8291 3.4: PRK_key = HMAC-SHA-256(auth_secret, ecdh_secret).
	prkKey := hkdfExtract(auth, shared.Bytes())
	if got := base64.RawURLEncoding.EncodeToString(prkKey); got != rfcPRKKey {
		t.Errorf("PRK_key = %s, want %s; the auth secret is the HMAC key, not the message", got, rfcPRKKey)
	}

	// key_info = "WebPush: info" || 0x00 || ua_public || as_public
	keyInfo := append([]byte("WebPush: info\x00"), uaPublic...)
	keyInfo = append(keyInfo, derivedPub...)
	ikm := hkdfExpand(prkKey, keyInfo, 32)
	if got := base64.RawURLEncoding.EncodeToString(ikm); got != rfcIKM {
		t.Errorf("IKM = %s, want %s", got, rfcIKM)
	}

	// RFC 8188 2.1.
	prk := hkdfExtract(salt, ikm)
	if got := base64.RawURLEncoding.EncodeToString(prk); got != rfcPRK {
		t.Errorf("PRK = %s, want %s", got, rfcPRK)
	}
	if got := base64.RawURLEncoding.EncodeToString(hkdfExpand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)); got != rfcCEK {
		t.Errorf("CEK = %s, want %s", got, rfcCEK)
	}
	if got := base64.RawURLEncoding.EncodeToString(hkdfExpand(prk, []byte("Content-Encoding: nonce\x00"), 12)); got != rfcNonce {
		t.Errorf("NONCE = %s, want %s", got, rfcNonce)
	}
}

// The whole body, byte for byte, against the RFC's example message.
func TestEncryptWebPushMatchesRFC8291Body(t *testing.T) {
	body, err := encryptWebPush(
		[]byte(rfcPlaintext),
		mustB64(t, rfcUAPublic),
		mustB64(t, rfcAuthSecret),
		rfcSenderKey(t),
		mustB64(t, rfcSalt),
	)
	if err != nil {
		t.Fatalf("encryptWebPush: %v", err)
	}

	want := mustB64(t, rfcBodyB64)
	if !bytes.Equal(body, want) {
		t.Errorf("body does not match RFC 8291 section 5\n got %s\nwant %s",
			base64.RawURLEncoding.EncodeToString(body),
			base64.RawURLEncoding.EncodeToString(want))
	}

	// Shape, asserted independently of the vector, so a future change that
	// happens to still match the bytes is still checked for structure.
	if len(body) != 144 {
		t.Errorf("body is %d octets, want 144 (86 header + 41 plaintext + 1 delimiter + 16 tag)", len(body))
	}
	if !bytes.Equal(body[:16], mustB64(t, rfcSalt)) {
		t.Error("the header must begin with the salt")
	}
	rs := body[16:20]
	if rs[0] != 0 || rs[1] != 0 || rs[2] != 0x10 || rs[3] != 0 {
		t.Errorf("rs = %v, want 4096", rs)
	}
	if body[20] != 65 {
		t.Errorf("idlen = %d, want 65: the sender's public key is mandatory", body[20])
	}
	if !bytes.Equal(body[21:86], mustB64(t, rfcASPublic)) {
		t.Error("the keyid must be the sender's uncompressed public key")
	}
}

// A regression test for the defect that made this function unusable: the nonce
// handed to GCM was 16 octets where GCM requires 12, so Seal panicked. Because
// the caller runs in a detached goroutine with a recover, the panic surfaced
// only as a log line and no push was ever delivered.
func TestEncryptWebPushDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("encryptWebPush panicked: %v", r)
		}
	}()
	priv, err := GenerateVAPIDKeypair()
	if err != nil {
		t.Fatal(err)
	}
	recv := rfcSenderKey(t)
	uaPub := elliptic.Marshal(elliptic.P256(), recv.PublicKey.X, recv.PublicKey.Y)

	if _, err := encryptWebPush([]byte("hello"), uaPub, make([]byte, 16), priv.PrivateKey, make([]byte, 16)); err != nil {
		t.Fatalf("encryptWebPush: %v", err)
	}
}

func TestEncryptWebPushRejectsUnusableInput(t *testing.T) {
	validKey := rfcSenderKey(t)
	uaPub := mustB64(t, rfcUAPublic)
	auth := make([]byte, 16)

	cases := []struct {
		name   string
		salt   []byte
		ua     []byte
		reject string
	}{
		{"short salt", make([]byte, 8), uaPub, "salt must be 16"},
		{"long salt", make([]byte, 32), uaPub, "salt must be 16"},
		{"empty receiver key", make([]byte, 16), nil, "uncompressed point"},
		{"uncompressed key of the wrong length", make([]byte, 16), uaPub[:40], "uncompressed point"},
		{"point not on the curve", make([]byte, 16), append([]byte{0x04}, bytes.Repeat([]byte{0x01}, 64)...), "not a valid P-256 point"},
	}
	for _, tc := range cases {
		_, err := encryptWebPush([]byte("x"), tc.ua, auth, validKey, tc.salt)
		if err == nil {
			t.Errorf("%s: want an error, got none", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.reject) {
			t.Errorf("%s: error = %q, want it to mention %q", tc.name, err, tc.reject)
		}
	}
}

// A payload at the RFC 8291 section 4 ceiling must still fit one record: the push
// service is not required to accept more than 4096 octets, and the header alone
// is 86.
func TestEncryptWebPushStaysWithinOneRecord(t *testing.T) {
	priv := rfcSenderKey(t)
	recv := rfcSenderKey(t)
	uaPub := elliptic.Marshal(elliptic.P256(), recv.PublicKey.X, recv.PublicKey.Y)

	for _, n := range []int{0, 1, 3993} {
		body, err := encryptWebPush(bytes.Repeat([]byte("a"), n), uaPub, make([]byte, 16), priv, make([]byte, 16))
		if err != nil {
			t.Fatalf("%d octets: %v", n, err)
		}
		if want := 86 + n + 1 + 16; len(body) != want {
			t.Errorf("%d octets of plaintext produced a %d-octet body, want %d", n, len(body), want)
		}
	}
}
