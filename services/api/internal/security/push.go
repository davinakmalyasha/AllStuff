package security

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Web Push (RFC 8291) with VAPID (RFC 8292) — dependency-free implementation
// (PRD §5.5.3). Payload is encrypted with the receiver's P-256 public key.

// VAPIDKeypair holds the application server key (base64url of raw 65-byte key).
type VAPIDKeypair struct {
	PublicKey  string // base64url(raw uncompressed point)
	PrivateKey *ecdsa.PrivateKey
}

func GenerateVAPIDKeypair() (*VAPIDKeypair, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y)
	return &VAPIDKeypair{
		PublicKey:  base64.RawURLEncoding.EncodeToString(pub),
		PrivateKey: priv,
	}, nil
}

// ErrSubscriptionGone: the push service answered 404/410 — the subscription
// is dead and must be deleted (RFC 8030 §5). Senders treat this as a prune
// signal, not a success.
var ErrSubscriptionGone = errors.New("push subscription expired")

func (k *VAPIDKeypair) vapidJWT(subject, audience string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{"aud": audience, "exp": now + 12*3600, "sub": subject})
	body := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(body))
	r, s, err := ecdsa.Sign(rand.Reader, k.PrivateKey, hash[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ParseVAPIDKeypair restores a keypair from base64url public (65-byte raw)
// and DER-encoded private key (PKCS8).
func ParseVAPIDKeypair(pubB64, privB64 string) (*VAPIDKeypair, error) {
	pubRaw, err := base64.RawURLEncoding.DecodeString(pubB64)
	if err != nil {
		return nil, err
	}
	privDER, err := base64.RawURLEncoding.DecodeString(privB64)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKCS8PrivateKey(privDER)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, fmt.Errorf("VAPID private key must be P-256")
	}
	// Verify the advertised public key actually belongs to the private key —
	// a mismatched pair boots fine here but every push then fails signature
	// validation at the relay.
	if len(pubRaw) != 65 || pubRaw[0] != 0x04 {
		return nil, fmt.Errorf("VAPID public key must be a 65-byte uncompressed P-256 point")
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), pubRaw)
	if x == nil || priv.PublicKey.X.Cmp(x) != 0 || priv.PublicKey.Y.Cmp(y) != 0 {
		return nil, fmt.Errorf("VAPID public key does not match the private key")
	}
	return &VAPIDKeypair{PublicKey: pubB64, PrivateKey: priv}, nil
}

// PushSubscription mirrors the stored row (endpoint + keys).
type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// pushClient is bounded so a hostile subscription endpoint cannot stall the
// request path or probe internal networks indefinitely. Redirects are off:
// an endpoint could otherwise bounce requests past subscribe-time SSRF checks.
// The dial layer re-resolves DNS and validates every address on EVERY dial,
// closing the subscribe-time TOCTOU / DNS-rebinding hole (subscribe-time
// checks alone see only the first resolution).
var pushClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if port != "443" {
				return nil, fmt.Errorf("push endpoint: port %s not allowed", port)
			}
			if net.ParseIP(host) == nil && !strings.Contains(host, ".") {
				return nil, fmt.Errorf("push endpoint: bare hostname rejected")
			}
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ia := range addrs {
				if !IsPublicIP(ia.IP) {
					return nil, fmt.Errorf("push endpoint: non-public address blocked")
				}
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ValidPushEndpoint enforces an https URL on a public host — subscription
// endpoints are user-supplied, so without this check Send becomes an SSRF
// primitive (internal services, cloud metadata).
func ValidPushEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return IsPublicIP(ip)
	}
	// Hostname: resolve and require every address to be public.
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !IsPublicIP(a) {
			return false
		}
	}
	return true
}

// IsPublicIP: true only for globally routable, non-special addresses.
// Covers loopback, RFC1918, link-local, CGNAT (100.64.0.0/10), benchmark
// (198.18.0.0/15), documentation ranges, multicast, and reserved blocks.
func IsPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// CGNAT 100.64.0.0/10
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		// 0.0.0.0/8, 192.0.0.0/24, 192.0.2.0/24, 198.51.100.0/24,
		// 203.0.113.0/24, 198.18.0.0/15, 240.0.0.0/4, 255.255.255.255
		switch {
		case v4[0] == 0:
			return false
		case v4[0] == 192 && v4[1] == 0 && (v4[2] == 0 || v4[2] == 2):
			return false
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19):
			return false
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100:
			return false
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113:
			return false
		case v4[0] >= 240:
			return false
		}
	}
	return ip.IsGlobalUnicast()
}

// Send pushes an encrypted payload to a subscription (best-effort).
func (k *VAPIDKeypair) Send(sub PushSubscription, subject, title, body string, data map[string]string) error {
	plain := map[string]any{"title": title, "body": body, "data": data}
	payload, _ := json.Marshal(plain)

	// Ephemeral sender keypair.
	ephPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	recvPub, err := base64.RawURLEncoding.DecodeString(sub.Keys.P256dh)
	if err != nil {
		return err
	}
	auth, err := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	encrypted, err := encryptWebPush(payload, recvPub, auth, ephPriv, salt)
	if err != nil {
		return err
	}
	bodyBuf := bytes.NewReader(encrypted)

	jwt, err := k.vapidJWT(subject, endpointAudience(sub.Endpoint))
	if err != nil {
		return err
	}
	authHeader := "vapid t=" + jwt + ", k=" + k.PublicKey

	req, err := http.NewRequest(http.MethodPost, sub.Endpoint, bodyBuf)
	if err != nil {
		return err
	}
	req.Header.Set("TTL", "60")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Authorization", authHeader)

	resp, err := pushClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return ErrSubscriptionGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("push: %s", resp.Status)
	}
	return nil
}

// endpointAudience derives the VAPID aud claim from the push service origin.
// A hardcoded FCM audience fails validation at Mozilla/Mozilla-derived and
// Apple relays (RFC 8292: aud MUST be the origin of the subscription URI).
func endpointAudience(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "https://fcm.googleapis.com"
	}
	return u.Scheme + "://" + u.Host
}

// hkdfExtract is HKDF-Extract from RFC 5869: HMAC-SHA-256 with the salt as the
// HMAC key and the input keying material as the message.
//
// The direction is the opposite of what the parameter names invite, and that is
// the point of writing it down. This file used to carry a function named
// hkdfSHA256 that computed HMAC(key=secret, msg=salt) and dropped its info
// argument on the floor - neither Extract nor Expand, and used as if it were
// both.
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// encryptWebPush builds the body of a single-record aes128gcm push message, per
// RFC 8291 on top of RFC 8188.
//
// The randomness is a parameter rather than drawn in here, so the derivation can
// be checked against the worked example in RFC 8291 Appendix A. That is the only
// way to be confident it is right without a live push service, and the previous
// version of this code was wrong in five separate ways, so confidence is the
// whole point.
func encryptWebPush(plaintext, uaPublic, authSecret []byte, asPrivate *ecdsa.PrivateKey, salt []byte) ([]byte, error) {
	if len(salt) != 16 {
		return nil, fmt.Errorf("push: salt must be 16 octets, got %d", len(salt))
	}
	if len(uaPublic) != 65 || uaPublic[0] != 0x04 {
		return nil, fmt.Errorf("push: receiver key must be a 65-octet uncompressed point, got %d octets", len(uaPublic))
	}
	// RFC 8291 section 7 requires validating that the public key is a point on
	// P-256. Unmarshal does exactly that - it rejects a point not on the curve
	// and the point at infinity - and omitting it would let a subscription
	// holder mount a small-subgroup attack against the ephemeral private key.
	x, y := elliptic.Unmarshal(elliptic.P256(), uaPublic)
	if x == nil {
		return nil, fmt.Errorf("push: receiver key is not a valid P-256 point")
	}
	shared, _ := elliptic.P256().ScalarMult(x, y, asPrivate.D.Bytes())

	// RFC 8291 section 3.4, first half. PRK_key = HMAC-SHA-256(auth_secret,
	// ecdh_secret): the AUTHENTICATION SECRET is the HMAC key.
	prkKey := hkdfExtract(authSecret, shared.Bytes())

	asPublic := elliptic.Marshal(elliptic.P256(), asPrivate.PublicKey.X, asPrivate.PublicKey.Y)
	keyInfo := make([]byte, 0, 14+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm := hkdfExpand(prkKey, keyInfo, 32)

	// Second half, from RFC 8188 section 2.1: the record's key and nonce come
	// from the IKM combined with the salt that is also written into the header.
	prk := hkdfExtract(salt, ikm)
	cek := hkdfExpand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfExpand(prk, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Header: salt || rs || idlen || keyid, 86 octets for the keyid alone.
	// The keyid is the sender's public key and is mandatory (section 4): the
	// receiver has no other way to obtain it, so an empty keyid - which is what
	// this used to write - makes the message undecryptable even if everything
	// else were correct.
	header := make([]byte, 0, 16+4+1+len(asPublic))
	header = append(header, salt...)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, 4096)
	header = append(header, rs...)
	header = append(header, byte(len(asPublic)))
	header = append(header, asPublic...)

	// The padding delimiter is the final octet of the PLAINTEXT (RFC 8188
	// section 2), so it is encrypted along with the rest. This was previously
	// written as a byte appended after the ciphertext.
	record := make([]byte, 0, len(plaintext)+1)
	record = append(record, plaintext...)
	record = append(record, 0x02)

	body := make([]byte, 0, len(header)+len(record)+16)
	body = append(body, header...)
	// The additional data is a ZERO-LENGTH sequence (RFC 8188 section 2), so the
	// nil here is deliberate. Passing the header instead is the intuitive mistake
	// - it is the obvious candidate, it is right there, and it produces a message
	// that looks fine - but it yields a tag no conforming receiver accepts.
	//
	// This was wrong when first written and the RFC 8291 example is what caught
	// it: with the header as additional data the derived tag disagreed with the
	// published one, and every other intermediate value agreed. The header is
	// still transmitted, unauthenticated, as part of the body.
	return gcm.Seal(body, nonce, record, nil), nil
}

func hkdfExpand(prk, info []byte, length int) []byte {
	out := make([]byte, 0, length)
	var t []byte
	counter := byte(1)
	for len(out) < length {
		mac := hmac.New(sha256.New, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{counter})
		t = mac.Sum(nil)
		out = append(out, t...)
		counter++
	}
	return out[:length]
}
