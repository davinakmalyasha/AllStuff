//go:build !race

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// oauth.Exchange had no coverage - 51 statements. It is an account-creation and
// account-linking path reachable without a password, so both halves matter:
// what it refuses, and what identity it hands back.

type oauthRoundTripper func(*http.Request) (*http.Response, error)

func (f oauthRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func oauthJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// stubOAuth replaces the package client for one test. Tests in a package do not
// run in parallel, so the swap is safe as long as it is restored.
func stubOAuth(t *testing.T, fn oauthRoundTripper) {
	t.Helper()
	old := oauthClient
	oauthClient = &http.Client{Transport: fn}
	t.Cleanup(func() { oauthClient = old })
}

func testOAuth(t *testing.T, h *testutil.H) *OAuth {
	t.Helper()
	return NewOAuth(h.Repos, config.Config{
		GoogleOAuthClientID: "client-id",
		GoogleOAuthSecret:   "client-secret",
		PublicURL:           "https://bizverse.test",
	})
}

// happyOAuth answers the token call, then the userinfo call.
func happyOAuth(email string, verified bool) oauthRoundTripper {
	return func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
			return oauthJSON(200, `{"access_token":"at-123"}`), nil
		}
		return oauthJSON(200, fmt.Sprintf(`{"email":%q,"email_verified":%v,"name":"Google Person"}`, email, verified)), nil
	}
}

func TestOAuthExchangeRefusesBadTokenResponses(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	cases := []struct {
		name string
		rt   oauthRoundTripper
	}{
		{"a transport failure", func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset")
		}},
		{"an HTTP error", func(*http.Request) (*http.Response, error) {
			return oauthJSON(400, `{"error":"invalid_grant"}`), nil
		}},
		{"a response without an access token", func(*http.Request) (*http.Response, error) {
			return oauthJSON(200, `{"token_type":"Bearer"}`), nil
		}},
		{"malformed JSON", func(*http.Request) (*http.Response, error) {
			return oauthJSON(200, `{`), nil
		}},
	}
	for _, tc := range cases {
		stubOAuth(t, tc.rt)
		svc := testOAuth(t, h)
		if _, err := svc.Exchange(ctx, "the-code"); !errors.Is(err, domain.ErrTokenInvalid) {
			t.Errorf("%s: err = %v, want ErrTokenInvalid", tc.name, err)
		}
	}
}

func TestOAuthExchangeRefusesBadUserInfo(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	// The token call succeeds; the identity call then returns nothing usable.
	for _, body := range []string{`{}`, `{"email":""}`, `{`} {
		body := body
		stubOAuth(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
				return oauthJSON(200, `{"access_token":"at-123"}`), nil
			}
			return oauthJSON(200, body), nil
		})
		svc := testOAuth(t, h)
		if _, err := svc.Exchange(ctx, "the-code"); !errors.Is(err, domain.ErrTokenInvalid) {
			t.Errorf("userinfo %q: err = %v, want ErrTokenInvalid", body, err)
		}
	}
}

func TestOAuthExchangeSendsTheRightTokenRequest(t *testing.T) {
	h := testutil.New(t)
	var sawForm string
	stubOAuth(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
			if r.Method != http.MethodPost {
				t.Errorf("token request method = %s, want POST", r.Method)
			}
			b, _ := io.ReadAll(r.Body)
			sawForm = string(b)
			return oauthJSON(200, `{"access_token":"at-123"}`), nil
		}
		return oauthJSON(200, `{"email":"fresh@example.test","email_verified":true,"name":"Fresh"}`), nil
	})

	svc := testOAuth(t, h)
	if _, err := svc.Exchange(context.Background(), "the-code"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"grant_type=authorization_code", "code=the-code", "client_id=client-id", "client_secret=client-secret"} {
		if !strings.Contains(sawForm, want) {
			t.Errorf("token form is missing %q: %s", want, sawForm)
		}
	}
}

func TestOAuthExchangeLinksAnExistingAccount(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()

	// Banned: refused before any linking.
	banned := testutil.UserOpts_(t, h, testutil.UserOpts{Banned: true})
	h.Exec(t, `UPDATE users SET email='banned@example.test' WHERE id=$1`, banned.ID)
	stubOAuth(t, happyOAuth("banned@example.test", true))
	if _, err := testOAuth(t, h).Exchange(ctx, "c"); !errors.Is(err, domain.ErrAccountBanned) {
		t.Errorf("banned user: err = %v, want ErrAccountBanned", err)
	}

	// Unverified local account: linking would let whoever controls the Google
	// address take it over, so it is refused rather than merged.
	unverified := testutil.UserOpts_(t, h, testutil.UserOpts{Unverified: true})
	h.Exec(t, `UPDATE users SET email='unverified@example.test' WHERE id=$1`, unverified.ID)
	stubOAuth(t, happyOAuth("unverified@example.test", true))
	if _, err := testOAuth(t, h).Exchange(ctx, "c"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("unverified user: err = %v, want ErrValidation", err)
	}
	if after, _ := h.Repos.Users.GetByEmail(ctx, "unverified@example.test"); after == nil || after.ID != unverified.ID {
		t.Error("the unverified account was modified by a refused link")
	}

	// A verified local account is linked, and the match is case-insensitive:
	// Google may return the address in any case.
	verified := testutil.UserOpts_(t, h, testutil.UserOpts{})
	h.Exec(t, `UPDATE users SET email='Mixed.Case@example.test' WHERE id=$1`, verified.ID)
	stubOAuth(t, happyOAuth("MIXED.CASE@example.test", true))
	got, err := testOAuth(t, h).Exchange(ctx, "c")
	if err != nil {
		t.Fatalf("verified user: %v", err)
	}
	if got.ID != verified.ID {
		t.Errorf("linked to %s, want the existing account %s", got.ID, verified.ID)
	}
}

func TestOAuthExchangeCreatesAnAccount(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	stubOAuth(t, happyOAuth("New.Person@example.test", true))
	svc := testOAuth(t, h)

	got, err := svc.Exchange(ctx, "c")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	// The address is normalised, and because Google already verified it the
	// local account starts verified too.
	if got.Email != "new.person@example.test" {
		t.Errorf("Email = %q, want it lowercased", got.Email)
	}
	if got.EmailVerifiedAt == nil {
		t.Error("a new Google account is not marked verified")
	}
	if got.Username == "" {
		t.Error("no username was generated")
	}
	if got.Name != "Google Person" {
		t.Errorf("Name = %q, want the profile name", got.Name)
	}
	// The account carries an unusable password hash rather than an empty one,
	// so password login stays closed without a NULL dereference later.
	if !strings.HasPrefix(got.PasswordHash, "$argon2id$") {
		t.Errorf("PasswordHash = %.12q, want an argon2id hash", got.PasswordHash)
	}

	// A second exchange for the same address must find the account it created
	// rather than failing on the unique email.
	again, err := svc.Exchange(ctx, "c")
	if err != nil {
		t.Fatalf("second Exchange: %v", err)
	}
	if again.ID != got.ID {
		t.Errorf("second exchange created a second account: %s then %s", got.ID, again.ID)
	}
}

// Two different addresses can share a local part; the generated usernames must
// still be unique or the second sign-up fails on a constraint.
func TestOAuthExchangeUsernamesStayUnique(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	svc := testOAuth(t, h)

	stubOAuth(t, happyOAuth("alice@one.example", true))
	first, err := svc.Exchange(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}

	stubOAuth(t, happyOAuth("alice@two.example", true))
	second, err := svc.Exchange(ctx, "c")
	if err != nil {
		t.Fatalf("second sign-up with the same local part: %v", err)
	}
	if first.Username == second.Username {
		t.Errorf("both accounts got the username %q", first.Username)
	}
}

// The redirect_uri is the one value that must agree between the consent URL and
// the token exchange, and it must name a route the API serves. It used to omit
// /api/v1, so Google returned the browser to a frontend path with no handler.
func TestOAuthRedirectURIUsesTheAPIRoute(t *testing.T) {
	h := testutil.New(t)
	svc := testOAuth(t, h)
	// A trailing slash on PUBLIC_URL must not produce a doubled separator.
	svc.cfg.PublicURL = "https://bizverse.test/"
	const want = "https://bizverse.test/api/v1/auth/oauth/google/callback"

	u, err := url.Parse(svc.AuthURL("state-1"))
	if err != nil {
		t.Fatalf("parse AuthURL: %v", err)
	}
	if got := u.Query().Get("redirect_uri"); got != want {
		t.Errorf("AuthURL redirect_uri = %q, want %q", got, want)
	}

	var form url.Values
	stubOAuth(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "oauth2.googleapis.com") {
			b, _ := io.ReadAll(r.Body)
			form, _ = url.ParseQuery(string(b))
			return oauthJSON(200, `{"access_token":"at-123"}`), nil
		}
		return oauthJSON(200, `{"email":"redirect@example.test","email_verified":true}`), nil
	})
	if _, err := svc.Exchange(context.Background(), "the-code"); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if got := form.Get("redirect_uri"); got != want {
		t.Errorf("token redirect_uri = %q, want %q", got, want)
	}
}

// userinfo carries email_verified. Ignoring it let an unverified provider
// address auto-link to a verified local account with the same email - the local
// half being verified says nothing about Google's half.
func TestOAuthExchangeRefusesUnverifiedGoogleEmail(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	victim := testutil.UserOpts_(t, h, testutil.UserOpts{})
	h.Exec(t, `UPDATE users SET email='victim@example.test' WHERE id=$1`, victim.ID)

	stubOAuth(t, happyOAuth("victim@example.test", false))
	if _, err := testOAuth(t, h).Exchange(ctx, "c"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unverified provider email: err = %v, want ErrValidation", err)
	}
	after, err := h.Repos.Users.GetByEmail(ctx, "victim@example.test")
	if err != nil || after == nil || after.ID != victim.ID {
		t.Errorf("the existing account was modified by a refused link: %+v, %v", after, err)
	}
}
