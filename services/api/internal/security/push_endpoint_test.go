package security

import (
	"testing"
)

// endpointAudience derives the VAPID `aud` claim from the subscription's own
// URL. RFC 8292 requires aud to be the origin of the push resource, and a
// hardcoded audience is rejected by the Mozilla-derived and Apple relays - so
// getting this wrong breaks delivery to every endpoint except FCM's.
//
// It had no coverage at all in the last measured profile.
func TestEndpointAudience(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"fcm", "https://fcm.googleapis.com/fcm/send/abc123", "https://fcm.googleapis.com"},
		{"mozilla", "https://updates.push.services.mozilla.com/wpush/v2/xyz", "https://updates.push.services.mozilla.com"},
		{"apple", "https://web.push.apple.com/QGVsbG8", "https://web.push.apple.com"},
		{"bare origin", "https://push.example.net", "https://push.example.net"},
		{"with a query", "https://push.example.net/send?id=1", "https://push.example.net"},

		// The audience is the origin, so credentials and paths are stripped. A
		// userinfo component reaching the aud claim would make it match nothing.
		{"with userinfo", "https://user:pass@push.example.net/send", "https://push.example.net"},
		{"non-default port is part of the origin", "https://push.example.net:8443/send", "https://push.example.net:8443"},

		// Scheme is preserved rather than forced. A subscription URL is https in
		// practice, but silently upgrading the scheme would produce an audience
		// that does not match the endpoint the message is actually sent to.
		{"http is preserved", "http://push.example.net/send", "http://push.example.net"},

		// The fallback. A value that cannot be parsed yields the FCM audience
		// rather than an empty claim, and an empty aud is rejected by every relay.
		{"empty", "", "https://fcm.googleapis.com"},
		{"no scheme", "push.example.net/send", "https://fcm.googleapis.com"},
		{"relative", "/send", "https://fcm.googleapis.com"},
		{"scheme only", "https://", "https://fcm.googleapis.com"},
		{"unparseable", "https://%zz/", "https://fcm.googleapis.com"},
	}
	for _, tc := range cases {
		if got := endpointAudience(tc.endpoint); got != tc.want {
			t.Errorf("%s: endpointAudience(%q) = %q, want %q", tc.name, tc.endpoint, got, tc.want)
		}
	}

	t.Run("the audience is always non-empty", func(t *testing.T) {
		// An empty aud is the failure that matters: it is accepted locally and
		// rejected by every relay, so it would look like a delivery problem
		// rather than a claim problem.
		for _, ep := range []string{"", " ", "://", "file:///etc/passwd", "javascript:alert(1)", "data:,"} {
			if got := endpointAudience(ep); got == "" {
				t.Errorf("endpointAudience(%q) returned an empty audience", ep)
			}
		}
	})
}

// ValidPushEndpoint is the subscribe-time gate on a user-supplied endpoint. A
// subscription is stored and then dialled later by the fan-out, so this is the
// first of two checks; the second is the dial-time address validation.
//
// It had no coverage at all in the last measured profile.
//
// Only inputs that resolve without a network are used here. A hostname that
// needs DNS would make the test depend on the runner's resolver and on a
// timeout, and an unresolvable name is the one case a pure test cannot pin
// deterministically.
func TestValidPushEndpoint(t *testing.T) {
	valid := []string{
		"https://8.8.8.8/push",
		"https://1.1.1.1:8443/push/abc",
		"https://[2001:4860:4860::8888]/push",
	}
	for _, raw := range valid {
		if !ValidPushEndpoint(raw) {
			t.Errorf("%s: want valid", raw)
		}
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"relative", "/push/abc"},
		{"no scheme", "push.example.net/send"},
		{"http is not https", "http://8.8.8.8/push"},
		{"no host", "https://"},
		{"javascript", "javascript:alert(1)"},
		{"data", "data:text/plain,x"},
		{"file", "file:///etc/passwd"},

		// Addresses the dial-time check would also refuse. Refusing at subscribe
		// time matters because the row is stored: a permissive check here leaves
		// a dead endpoint in the table and a dial attempt per notification.
		{"loopback", "https://127.0.0.1/push"},
		{"loopback ipv6", "https://[::1]/push"},
		{"privately addressed", "https://10.0.0.1/push"},
		{"rfc1918 192.168", "https://192.168.1.1/push"},
		{"rfc1918 172.16", "https://172.16.0.1/push"},
		{"link-local", "https://169.254.1.1/push"},
		{"cloud metadata", "https://169.254.169.254/latest/meta-data/"},
		{"cgnat", "https://100.64.0.1/push"},
		{"unspecified", "https://0.0.0.0/push"},
		{"ipv4-mapped loopback", "https://[::ffff:127.0.0.1]/push"},

		// Resolves through the hosts file, so no network is needed and the name
		// is the realistic shape of the attack: a hostname rather than a literal.
		{"localhost by name", "https://localhost/push"},
		{"localhost with a port", "https://localhost:8443/push"},
	}
	for _, tc := range invalid {
		if ValidPushEndpoint(tc.raw) {
			t.Errorf("%s (%s): want invalid", tc.name, tc.raw)
		}
	}
}
