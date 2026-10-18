package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// password.go had no test file of its own in this package - both primitives were
// exercised only indirectly through database-backed service tests, which is
// exactly why the rejection paths below were dark.

// A malformed stored hash must be refused, not acted on.
//
// Four of these used to panic rather than return, which meant a single bad row
// took down the login path for that account: argon2 panics on t=0 and p=0, a
// zero-length key dereferences a nil digest inside blake2b, and the memory
// parameter sizes one allocation, so a large enough value is a terabyte request.
// None of them were reachable through HashPassword; all were reachable from the
// column.
func TestVerifyPasswordRejectsUnusableHashes(t *testing.T) {
	salt := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef"))
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	longKey := base64.RawStdEncoding.EncodeToString(make([]byte, maxArgonKeyLen+1))

	cases := []struct {
		name    string
		encoded string
	}{
		{"empty input", ""},
		{"not a hash at all", "hunter2"},
		{"too few segments", "$argon2id$v=19$m=65536,t=3,p=4$" + salt},
		{"too many segments", "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + key + "$extra"},
		{"wrong algorithm", "$argon2i$v=19$m=65536,t=3,p=4$" + salt + "$" + key},
		{"unparseable version", "$argon2id$v=x$m=65536,t=3,p=4$" + salt + "$" + key},
		{"unparseable parameters", "$argon2id$v=19$nonsense$" + salt + "$" + key},
		{"incomplete parameters", "$argon2id$v=19$m=65536,t=3$" + salt + "$" + key},
		{"salt is not base64", "$argon2id$v=19$m=65536,t=3,p=4$!!!!$" + key},
		{"hash is not base64", "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$!!!!"},

		// The four that reached argon2 and panicked it.
		{"empty hash segment", "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$"},
		{"zero rounds", "$argon2id$v=19$m=65536,t=0,p=4$" + salt + "$" + key},
		{"zero parallelism", "$argon2id$v=19$m=65536,t=3,p=0$" + salt + "$" + key},
		// Just past the cap rather than the extreme value a corrupt row could
		// hold. The guard being exercised is the same `m > maxArgonMemoryKiB`
		// comparison, and stopping one kibibyte past it keeps this test safe to
		// run with the guard removed: the allocation is bounded rather than
		// fatal, so a mutation is reported as a failure instead of an OOM.
		{"memory past the cap", fmt.Sprintf("$argon2id$v=19$m=%d,t=3,p=4$%s$%s", maxArgonMemoryKiB+1, salt, key)},

		// Out of range rather than dangerous, rejected for the same reason: this
		// is not a hash HashPassword could have produced.
		{"rounds past the cap", fmt.Sprintf("$argon2id$v=19$m=65536,t=%d,p=4$%s$%s", maxArgonTime+1, salt, key)},
		{"memory below argon2's own minimum", "$argon2id$v=19$m=1,t=3,p=4$" + salt + "$" + key},
		{"key longer than the cap", "$argon2id$v=19$m=65536,t=3,p=4$" + salt + "$" + longKey},
	}

	for _, tc := range cases {
		// The terabyte case is only safe to write down because the guard exists;
		// without it this test would attempt the allocation rather than assert
		// that it is refused.
		ok, err := VerifyPassword("whatever", tc.encoded)
		if !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%s: err = %v, want ErrInvalidHash", tc.name, err)
		}
		if ok {
			t.Errorf("%s: reported a password match for an unusable hash", tc.name)
		}
	}
}

func TestVerifyPasswordRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"

	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=") {
		t.Errorf("encoded hash has an unexpected shape: %q", encoded)
	}

	ok, err := VerifyPassword(password, encoded)
	if err != nil || !ok {
		t.Errorf("the password it was hashed from did not verify: ok=%v err=%v", ok, err)
	}

	// A wrong password is a false, not an error. The distinction matters: the
	// caller maps an error to a 500 and a false to a 401.
	ok, err = VerifyPassword("wrong password", encoded)
	if err != nil {
		t.Errorf("a wrong password returned an error: %v", err)
	}
	if ok {
		t.Error("a wrong password verified")
	}

	if ok, _ := VerifyPassword(strings.ToUpper(password), encoded); ok {
		t.Error("verification is case-insensitive")
	}
	if ok, _ := VerifyPassword("", encoded); ok {
		t.Error("an empty password verified")
	}

	// Two hashes of the same password must differ, or the salt is not random and
	// identical passwords are linkable across accounts.
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if second == encoded {
		t.Error("two hashes of the same password are identical; the salt is not random")
	}
	if ok, err := VerifyPassword(password, second); err != nil || !ok {
		t.Errorf("the second hash did not verify: ok=%v err=%v", ok, err)
	}
}

// tokenWith signs an arbitrary payload segment, so a test can hold a valid
// signature and still make the payload undecodable. That isolates the payload
// branch from the signature branch, which signing normally couples.
func tokenWith(secret, payloadSegment string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := header + "." + payloadSegment
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestParseJWTRejectsMalformedTokens(t *testing.T) {
	const secret = "test-signing-secret"
	valid, err := signJWT(secret, jwtClaims{Sub: "u1", Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := signJWT(secret, jwtClaims{Sub: "u1", Exp: time.Now().Add(-time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	// Exactly at the boundary: the check is `now > exp`, so this must still pass
	// for another second and is not asserted here.
	parts := strings.Split(valid, ".")

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"one segment", "abc"},
		{"two segments", parts[0] + "." + parts[1]},
		{"four segments", valid + ".extra"},
		{"signature is not base64", parts[0] + "." + parts[1] + ".!!!"},
		{"signature is truncated", parts[0] + "." + parts[1] + "." + parts[2][:8]},
		{"payload is not base64", tokenWith(secret, "!!!")},
		{"payload is not json", tokenWith(secret, base64.RawURLEncoding.EncodeToString([]byte("not json")))},
		{"expired", expired},
	}
	for _, tc := range cases {
		if _, err := parseJWT(secret, tc.token); err == nil {
			t.Errorf("%s: parsed without error", tc.name)
		}
	}

	// A token signed with a different secret must not verify. This is the one
	// that matters most: everything else is a malformed input, and this is a
	// well-formed forgery.
	other, err := signJWT("a-different-secret", jwtClaims{Sub: "u1", Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseJWT(secret, other); err == nil {
		t.Error("a token signed with another secret verified")
	}

	// A tampered payload with the original signature attached.
	tampered := parts[0] + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker","exp":9999999999}`)) +
		"." + parts[2]
	if _, err := parseJWT(secret, tampered); err == nil {
		t.Error("a token with a rewritten payload verified")
	}
}

func TestParseJWTReadsClaimsFromAValidToken(t *testing.T) {
	const secret = "test-signing-secret"
	exp := time.Now().Add(time.Hour).Unix()

	token, err := signJWT(secret, jwtClaims{
		Sub: "user-1", Role: "user", User: "user-1", Typ: "access",
		Jti: "jti-1", Iat: time.Now().Unix(), Exp: exp,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := parseJWT(secret, token)
	if err != nil {
		t.Fatalf("parseJWT: %v", err)
	}
	if got.Sub != "user-1" || got.Role != "user" || got.Typ != "access" || got.Jti != "jti-1" {
		t.Errorf("claims = %#v", got)
	}
	if got.Exp != exp {
		t.Errorf("Exp = %d, want %d", got.Exp, exp)
	}
}
