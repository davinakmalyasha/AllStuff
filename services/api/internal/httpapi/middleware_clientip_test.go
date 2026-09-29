package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"bizverse/api/internal/config"
)

// Rate-limit buckets are keyed on clientIP, so any two spellings of one address
// that produce different strings is a bypass. These assertions pin the
// canonicalization, because a regression here is silent: limits keep working,
// they just apply to the wrong granularity.

func TestClientIP_Canonicalizes(t *testing.T) {
	trust := false
	s := &Server{deps: Deps{Config: config.Config{TrustXForwardedFor: trust}}}

	cases := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"plain ipv4", "203.0.113.5:1234", "203.0.113.5"},
		{"ipv4 without port", "203.0.113.5", "203.0.113.5"},
		// The two spellings below are the SAME host. Before canonicalization
		// they were different map keys, so one /64 holder had 2^64 buckets.
		{"ipv6 compressed", "[2001:db8::1]:443", "2001:db8::1"},
		{"ipv6 expanded", "[2001:0db8:0000:0000:0000:0000:0000:0001]:443", "2001:db8::1"},
		// IPv4-mapped IPv6 must fold onto the same key as the plain form, or a
		// dual-stack attacker doubles every bucket for free.
		{"ipv4-mapped ipv6", "[::ffff:203.0.113.5]:443", "203.0.113.5"},
		{"ipv6 uppercase", "[2001:DB8::1]:443", "2001:db8::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		r.RemoteAddr = c.remoteAddr
		if got := s.clientIP(r); got != c.want {
			t.Errorf("%s: clientIP(%q) = %q, want %q", c.name, c.remoteAddr, got, c.want)
		}
	}
}

func TestClientIP_IgnoresXFFWhenNotTrusted(t *testing.T) {
	s := &Server{deps: Deps{Config: config.Config{TrustXForwardedFor: false}}}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r.RemoteAddr = "198.51.100.7:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := s.clientIP(r); got != "198.51.100.7" {
		t.Errorf("clientIP = %q; a spoofed XFF must be ignored entirely", got)
	}
}

func TestClientIP_TakesRightmostXFFEntry(t *testing.T) {
	s := &Server{deps: Deps{Config: config.Config{TrustXForwardedFor: true}}}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r.RemoteAddr = "10.0.0.1:443"
	// Leftmost entries are attacker-supplied; our own proxy appends the last.
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 8.8.8.8, 203.0.113.5")
	if got := s.clientIP(r); got != "203.0.113.5" {
		t.Errorf("clientIP = %q, want the rightmost entry 203.0.113.5", got)
	}
}

// A single-value XFF means no proxy appended to it, so the value is entirely
// client-controlled. Trusting it gave the attacker a fresh bucket per request,
// a total bypass of the 5/min login and 3/15min 2FA tiers.
func TestClientIP_SingleValueXFFIsNotTrusted(t *testing.T) {
	s := &Server{deps: Deps{Config: config.Config{TrustXForwardedFor: true}}}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	r.RemoteAddr = "198.51.100.7:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	if got := s.clientIP(r); got != "198.51.100.7" {
		t.Errorf("clientIP = %q; a single-value XFF is client-controlled and must not be trusted", got)
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                           false,
		"gzip":                       true,
		"GZIP":                       true,
		"deflate, gzip":              true,
		"gzip, deflate, br":          true,
		"gzip;q=0":                   false, // an explicit refusal
		"gzip;q=0.5":                 true,
		"gzip; q=0":                  false,
		"x-gzip":                     false, // substring matching would have matched
		"gzippy":                     false, // substring matching would have matched
		"br":                         false,
		"identity":                   false,
		"deflate, gzip;q=1.0, *;q=0": true,
	}
	for header, want := range cases {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}
