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
				if !isPublicIP(ia.IP) {
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
		return isPublicIP(ip)
	}
	// Hostname: resolve and require every address to be public.
	addrs, err := net.LookupIP(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if !isPublicIP(a) {
			return false
		}
	}
	return true
}

// isPublicIP: true only for globally routable, non-special addresses.
// Covers loopback, RFC1918, link-local, CGNAT (100.64.0.0/10), benchmark
// (198.18.0.0/15), documentation ranges, multicast, and reserved blocks.
func isPublicIP(ip net.IP) bool {
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
	ephPub := elliptic.Marshal(elliptic.P256(), ephPriv.PublicKey.X, ephPriv.PublicKey.Y)

	recvPub, err := base64.RawURLEncoding.DecodeString(sub.Keys.P256dh)
	if err != nil {
		return err
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), recvPub)
	if x == nil {
		return fmt.Errorf("invalid receiver key")
	}
	shared, _ := elliptic.P256().ScalarMult(x, y, ephPriv.D.Bytes())
	auth, err := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	if err != nil {
		return err
	}

	// HKDF for auth PRK.
	prk := hkdfSHA256(shared.Bytes(), auth, []byte("Content-Encoding: auth\x00"))
	context := []byte("P-256\x00")
	context = append(context, 0, 65, 0, 65)
	context = append(context, recvPub...)
	context = append(context, ephPub...)
	ikm := hkdfExpand(prk, append([]byte("Content-Encoding: aes128gcm\x00"), context...), 32)

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	block, err := aes.NewCipher(ikm[:16])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// Record header: salt || rs || idlen || keyid.
	recordHeader := make([]byte, 0, 21)
	recordHeader = append(recordHeader, salt...)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, 4096)
	recordHeader = append(recordHeader, rs...)
	recordHeader = append(recordHeader, 0) // empty keyid
	ciphertext := gcm.Seal(nil, ikm[16:], payload, recordHeader)

	// Body = header + ciphertext (+ padding delimiter).
	bodyBuf := bytes.NewBuffer(recordHeader)
	bodyBuf.Write(ciphertext)
	bodyBuf.WriteByte(0x02) // padding delimiter

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

func hkdfSHA256(secret, salt, info []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	if salt != nil {
		mac.Write(salt)
	}
	return mac.Sum(nil)
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
