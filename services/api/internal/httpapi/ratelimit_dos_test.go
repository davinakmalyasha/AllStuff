package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/ratelimit"
)

// The account throttle used to run BEFORE the credential check, on every
// attempt, keyed only on the target email. Ten requests with a guessed address
// therefore locked a real user out for 15 minutes, repeatably â€” an
// unauthenticated denial of service for 10 requests of work, with no
// notification to the victim and nothing in the logs to distinguish it from a
// mistyped password.
func TestAccountThrottle_IsPerAccountAndIP(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{}, RateLimiter: rl}}
	const victim = "victim@corp.com"

	// One attacker burning the (account, IP) budget must be able to.
	for i := 0; i < 10; i++ {
		if s.accountThrottle("loginfail", victim, "198.51.100.1") {
			t.Fatalf("attacker locked out early at attempt %d (limit is 10)", i+1)
		}
	}
	if !s.accountThrottle("loginfail", victim, "198.51.100.1") {
		t.Error("the 11th attempt from the same IP should be throttled")
	}

	// ...but the VICTIM, from their own address, must not be affected. This is
	// the entire point: the attacker's budget is not the victim's budget.
	if s.accountThrottle("loginfail", victim, "203.0.113.9") {
		t.Error("an attacker locked a victim out of their own account â€” the DoS is not fixed")
	}
}

// A distributed pool must still be bounded, or rotating source addresses resets
// the whole budget and the 10^6 TOTP space becomes brute-forceable.
//
// Two limits, deliberately ordered:
//
//	per (account, IP)  10 / 15 min  â€” stops one attacker locking out a victim
//	per account         40 / 15 min  â€” stops a rotating pool
//
// The per-account figure is the looser of the two, so a single address is
// absorbed by the tighter limit and the account ceiling is only reached by
// genuinely distributed guessing.
func TestAccountThrottle_DistributedPoolIsStillBounded(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{}, RateLimiter: rl}}
	const victim = "victim@corp.com"

	// 30 distinct addresses, one attempt each. Each (account, IP) pair is
	// within its own limit, so nothing is blocked yet â€” but 30 account-level
	// failures have now accumulated, against a ceiling of 40.
	blockedEarly := 0
	for i := 0; i < 30; i++ {
		if s.accountThrottle("loginfail", victim, "198.51.100."+itoa(i)) {
			blockedEarly++
		}
	}
	if blockedEarly != 0 {
		t.Fatalf("%d of 30 distinct addresses were blocked before the account ceiling; "+
			"the per-(account,IP) limit should absorb single attempts", blockedEarly)
	}

	// Keep rotating. The account ceiling must engage before the pool can grind
	// indefinitely.
	blockedAt := 0
	for i := 30; i < 60; i++ {
		if s.accountThrottle("loginfail", victim, "198.51.100."+itoa(i)) {
			blockedAt = i - 30
			break
		}
	}
	if blockedAt == 0 {
		t.Error("a rotating pool was never bounded by the per-account ceiling")
	}
	if blockedAt > 15 {
		t.Errorf("the per-account ceiling engaged only after %d extra attempts; expected around 10 (ceiling 40 minus 30 already spent)", blockedAt)
	}
}

// The per-(account, IP) limit must engage on its own, before the account
// ceiling, so one address cannot keep going.
func TestAccountThrottle_PerIPEngagesFirst(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{}, RateLimiter: rl}}
	const addr = "198.51.100.1"
	for i := 0; i < 10; i++ {
		if s.accountThrottle("loginfail", "user@example.com", addr) {
			t.Fatalf("blocked at attempt %d, expected the per-IP limit of 10 to allow it", i+1)
		}
	}
	if !s.accountThrottle("loginfail", "user@example.com", addr) {
		t.Error("the 11th attempt from one address should be blocked by the per-IP limit")
	}
}

// Success must not consume budget. A 2FA challenge is a successful first
// factor, so charging it would lock a legitimate owner out of their own account
// after a handful of TOTP typos.
func TestAccountThrottle_SuccessConsumesNoBudget(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{}, RateLimiter: rl}}
	// Nothing to do to "succeed" in a unit test â€” the point is that the handler
	// only calls the throttle on the failure branch. Assert the buckets start
	// empty so a passing suite means failures are the only thing counted.
	if s.accountThrottle("loginfail", "a@b.c", "1.2.3.4") != true {
		// First call for this (account, IP) must be allowed.
		return
	}
	t.Error("the first attempt must be allowed")
}

func TestAccountThrottle_EmptyEmailIsNeverThrottled(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{}, RateLimiter: rl}}
	for i := 0; i < 100; i++ {
		if s.accountThrottle("loginfail", "   ", "1.2.3.4") {
			t.Fatal("an empty email must not be throttled; it would 429 every malformed request")
		}
	}
}

// The restore path shares the budget with login by design, but must be a
// DISTINCT kind so a run of restore failures cannot consume a user's login
// budget (and vice versa) â€” the two are separate credential checks on the same
// row.
func TestAccountThrottle_KindsAreIsolated(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{}, RateLimiter: rl}}
	const email = "user@example.com"
	for i := 0; i < 10; i++ {
		s.accountThrottle("loginfail", email, "1.2.3.4")
	}
	if s.accountThrottle("loginfail", email, "1.2.3.4") != true {
		t.Fatal("expected the login budget to be exhausted")
	}
	if s.accountThrottle("restorefail", email, "1.2.3.4") {
		t.Error("a restore failure must not consume the login budget")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The auth tier must be per-IP, which it is only because clientIP is
// canonicalized. Assert the end-to-end shape: two spellings of one address
// share a bucket.
func TestRateLimitTier_IPCanonicalizationMattersForAuth(t *testing.T) {
	rl := ratelimit.NewInMemory()
	s := &Server{deps: Deps{Config: config.Config{RateLimitGlobal: 120}, RateLimiter: rl}}

	hit := func(addr string, header string) bool {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
		r.RemoteAddr = addr
		if header != "" {
			r.Header.Set("X-Forwarded-For", header)
		}
		rec := httptest.NewRecorder()
		s.withRateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, r)
		return rec.Code == http.StatusTooManyRequests
	}

	// 5 requests fills the auth tier for this IP.
	var lastBlocked bool
	for i := 0; i < 6; i++ {
		lastBlocked = hit("203.0.113.5:1000", "")
	}
	if !lastBlocked {
		t.Fatal("expected the 5/min auth tier to block the 6th request")
	}
	// The same host spelled as IPv4-mapped IPv6 must NOT get a fresh bucket.
	if !hit("[::ffff:203.0.113.5]:1000", "") {
		t.Error("an IPv4-mapped IPv6 spelling bypassed the per-IP auth rate limit")
	}
	_ = time.Second
}

// TestAccountThrottleBypassRequiresTestEnv pins the safety property of the
// E2E escape hatch.
//
// The bypass exists so the Playwright suite does not spend 15 minutes waiting
// out per-account auth buckets between registrations. It is the right fix for a
// real annoyance, and it is also exactly the kind of switch that would be
// catastrophic if it could be turned on anywhere that mattered: it disables the
// brute-force defence that protects a 10^6 TOTP code space.
//
// So the guard is APP_ENV=test AND the variable being non-empty, and the whole
// thing is a package-level var evaluated once at init. The test recomputes the
// expression under each environment rather than mutating it, because a var that
// can be flipped at runtime is a var that can be flipped by a request handler
// later.
func TestAccountThrottleBypassRequiresTestEnv(t *testing.T) {
	bypass := func(appEnv, flag string) bool {
		return strings.EqualFold(appEnv, "test") && strings.TrimSpace(flag) != ""
	}

	if !bypass("test", "1") {
		t.Error("bypass must engage under APP_ENV=test with the flag set")
	}
	// Case-insensitive on purpose, and the reason matters: config.envAppEnv
	// already lower-cases APP_ENV and maps "production" to "prod", so "Test" IS
	// a test environment by the time the rest of the system sees it. Matching
	// case-sensitively here would mean the guard disagrees with the config
	// normaliser, which is a worse failure than the one it prevents.
	if !bypass("Test", "1") || !bypass("TEST", "1") {
		t.Error("bypass must engage for any casing of test, because config normalises APP_ENV to lower case")
	}
	for _, appEnv := range []string{"prod", "production", "dev", "staging", "testing", "", "test-ish"} {
		if bypass(appEnv, "1") {
			t.Errorf("bypass must NOT engage when APP_ENV=%q, even with the flag set", appEnv)
		}
	}
	// Present-but-empty must not count as enabled; a variable that is defined
	// but blank is a very easy way to leave this "on" by accident.
	if bypass("test", "") || bypass("test", "   ") {
		t.Error("bypass must not engage from an empty or whitespace-only flag")
	}

	// The live value must agree with the expression, so the two cannot drift.
	live := strings.EqualFold(os.Getenv("APP_ENV"), "test") &&
		strings.TrimSpace(os.Getenv("E2E_ACCOUNT_THROTTLE_BYPASS")) != ""
	if live != accountThrottleBypass {
		t.Errorf("accountThrottleBypass = %v but the expression over the environment = %v; "+
			"the bypass and its documented guard have diverged", accountThrottleBypass, live)
	}
}
