package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A receiver-side decryption, written from RFC 8291 from the opposite direction
// to the sender. The value of a round trip is that it checks the half of the
// contract the RFC's test vector cannot: the vector pins the bytes for one fixed
// input, while this pins that a message built from random keys and a random salt
// is decryptable at all, and that the JSON inside is what the caller passed.
func decryptPush(t *testing.T, body, uaPub []byte, uaPriv *ecdsa.PrivateKey, auth []byte) []byte {
	t.Helper()

	if len(body) < 21+16 {
		t.Fatalf("body is %d octets, too short to hold a header and a tag", len(body))
	}
	salt := body[:16]
	idlen := int(body[20])
	if len(body) < 21+idlen+16 {
		t.Fatalf("body declares a %d-octet keyid but is only %d octets", idlen, len(body))
	}
	asPub := body[21 : 21+idlen]
	ct := body[21+idlen:]

	if idlen != 65 || asPub[0] != 0x04 {
		t.Fatalf("keyid is %d octets starting %#x, want a 65-octet uncompressed point", idlen, asPub[0])
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), asPub)
	if x == nil {
		t.Fatal("the sender key in the header is not a valid P-256 point")
	}
	shared, _ := elliptic.P256().ScalarMult(x, y, uaPriv.D.Bytes())

	prkKey := hkdfExtract(auth, shared.Bytes())
	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, asPub...)
	ikm := hkdfExpand(prkKey, keyInfo, 32)

	prk := hkdfExtract(salt, ikm)
	cek := hkdfExpand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfExpand(prk, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}

	// Empty additional data, per RFC 8188 section 2. If the sender authenticated
	// the header instead, decryption fails here rather than silently.
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("the body is not decryptable with the receiver's key: %v", err)
	}
	for i := len(pt) - 1; i >= 0; i-- {
		if pt[i] == 0 {
			continue
		}
		if pt[i] != 0x02 {
			t.Fatalf("padding delimiter is %#x, want 0x02", pt[i])
		}
		return pt[:i]
	}
	t.Fatal("no padding delimiter in the plaintext")
	return nil
}

// newReceiver builds a subscription keypair and the subscription that goes with
// it, so the test holds the private half and can decrypt what Send produces.
func newReceiver(t *testing.T) (PushSubscription, *ecdsa.PrivateKey, []byte, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y)
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	var sub PushSubscription
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(pub)
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	return sub, priv, pub, auth
}

// Send had never been executed in the last measured profile. It is the point
// where the ciphertext becomes an HTTP request, and where a 404 or 410 has to be
// recognised because that is what prunes a dead subscription.
func TestSendBuildsARequestTheReceiverCanDecrypt(t *testing.T) {
	type captured struct {
		req  *http.Request
		body []byte
	}
	got := make(chan captured, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- captured{req: r.Clone(r.Context()), body: body}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// The real client refuses anything but port 443 and validates every resolved
	// address, so it cannot reach a test server on loopback. Swapping the package
	// variable is the injection; the transport's own rules are covered by
	// ValidPushEndpoint and IsPublicIP.
	orig := pushClient
	pushClient = srv.Client()
	defer func() { pushClient = orig }()

	kp, err := GenerateVAPIDKeypair()
	if err != nil {
		t.Fatal(err)
	}
	sub, priv, pub, auth := newReceiver(t)
	sub.Endpoint = srv.URL + "/push/abc123"

	data := map[string]string{"thread_id": "t-42"}
	if err := kp.Send(sub, "mailto:ops@example.com", "New message", "hello there", data); err != nil {
		t.Fatalf("Send: %v", err)
	}

	c := <-got
	if c.req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", c.req.Method)
	}
	if c.req.URL.Path != "/push/abc123" {
		t.Errorf("path = %s, want /push/abc123", c.req.URL.Path)
	}

	for header, want := range map[string]string{
		"Ttl":              "60",
		"Content-Type":     "application/octet-stream",
		"Content-Encoding": "aes128gcm",
	} {
		if v := c.req.Header.Get(header); v != want {
			t.Errorf("%s = %q, want %q", header, v, want)
		}
	}

	// VAPID: a signed token and the application server key, per RFC 8292.
	authz := c.req.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "vapid t=") {
		t.Errorf("Authorization = %q, want it to start with \"vapid t=\"", authz)
	}
	if !strings.Contains(authz, ", k="+kp.PublicKey) {
		t.Error("Authorization does not carry the VAPID public key; the receiver cannot verify the sender")
	}

	// The body: 86-octet header, the payload, the padding delimiter, a 16-octet
	// tag. The exact length catches a missing keyid or delimiter without needing
	// to decrypt.
	payload, err := json.Marshal(map[string]any{"title": "New message", "body": "hello there", "data": data})
	if err != nil {
		t.Fatal(err)
	}
	if want := 86 + len(payload) + 1 + 16; len(c.body) != want {
		t.Errorf("body is %d octets, want %d (86 header + %d payload + 1 delimiter + 16 tag)",
			len(c.body), want, len(payload))
	}

	plain := decryptPush(t, c.body, pub, priv, auth)

	var out struct {
		Title string            `json:"title"`
		Body  string            `json:"body"`
		Data  map[string]string `json:"data"`
	}
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatalf("the decrypted plaintext is not the expected JSON: %v (%q)", err, plain)
	}
	if out.Title != "New message" || out.Body != "hello there" {
		t.Errorf("title/body = %q/%q", out.Title, out.Body)
	}
	if out.Data["thread_id"] != "t-42" {
		t.Errorf("data = %#v, want thread_id t-42", out.Data)
	}
}

// A 404 or 410 means the subscription is dead, and the caller prunes the row on
// that basis. Anything else must NOT be reported as gone, or a transient outage
// would delete live subscriptions.
func TestSendMapsStatusesToSubscriptionGone(t *testing.T) {
	cases := []struct {
		status int
		gone   bool
		wantOK bool
	}{
		{http.StatusCreated, false, true},
		{http.StatusOK, false, true},
		{http.StatusNoContent, false, true},
		{http.StatusNotFound, true, false},
		{http.StatusGone, true, false},
		{http.StatusBadRequest, false, false},
		{http.StatusUnauthorized, false, false},
		{http.StatusTooManyRequests, false, false},
		{http.StatusInternalServerError, false, false},
		{http.StatusBadGateway, false, false},
	}

	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(tc.status)
		}))

		orig := pushClient
		pushClient = srv.Client()

		kp, err := GenerateVAPIDKeypair()
		if err != nil {
			t.Fatal(err)
		}
		sub, _, _, _ := newReceiver(t)
		sub.Endpoint = srv.URL + "/push"

		err = kp.Send(sub, "mailto:ops@example.com", "t", "b", nil)

		pushClient = orig
		srv.Close()

		switch {
		case tc.wantOK && err != nil:
			t.Errorf("%d: err = %v, want nil", tc.status, err)
		case tc.gone && !errors.Is(err, ErrSubscriptionGone):
			t.Errorf("%d: err = %v, want ErrSubscriptionGone", tc.status, err)
		case !tc.gone && !tc.wantOK && errors.Is(err, ErrSubscriptionGone):
			t.Errorf("%d: reported ErrSubscriptionGone; a live subscription would be pruned", tc.status)
		case !tc.gone && !tc.wantOK && err == nil:
			t.Errorf("%d: err = nil, want an error", tc.status)
		}
	}
}

// A malformed receiver key must be refused before any request is attempted, so a
// broken subscription row cannot become a request to an attacker-chosen host.
func TestSendRejectsUnusableSubscriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was made for a subscription that should have been rejected")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	orig := pushClient
	pushClient = srv.Client()
	defer func() { pushClient = orig }()

	kp, err := GenerateVAPIDKeypair()
	if err != nil {
		t.Fatal(err)
	}

	valid, _, _, _ := newReceiver(t)

	cases := []struct {
		name string
		sub  PushSubscription
	}{
		{"p256dh is not base64", func() PushSubscription {
			s := valid
			s.Endpoint = srv.URL + "/push"
			s.Keys.P256dh = "not base64!!"
			return s
		}()},
		{"p256dh is not a curve point", func() PushSubscription {
			s := valid
			s.Endpoint = srv.URL + "/push"
			s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(append([]byte{0x04}, make([]byte, 64)...))
			return s
		}()},
		{"auth is not base64", func() PushSubscription {
			s := valid
			s.Endpoint = srv.URL + "/push"
			s.Keys.Auth = "!!!"
			return s
		}()},
	}
	for _, tc := range cases {
		if err := kp.Send(tc.sub, "mailto:ops@example.com", "t", "b", nil); err == nil {
			t.Errorf("%s: err = nil, want a rejection", tc.name)
		}
	}
}
