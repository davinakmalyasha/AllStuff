package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/security"
	"bizverse/api/internal/util"
)

// OAuth — Google authorization-code flow (PRD §5.9.1).
type OAuth struct {
	repos *repo.Repos
	cfg   config.Config
}

func NewOAuth(repos *repo.Repos, cfg config.Config) *OAuth { return &OAuth{repos: repos, cfg: cfg} }

func (o *OAuth) Enabled() bool {
	return o.cfg.GoogleOAuthClientID != "" && o.cfg.GoogleOAuthSecret != ""
}

// AuthURL builds the Google consent URL.
func (o *OAuth) AuthURL(state string) string {
	params := url.Values{}
	params.Set("client_id", o.cfg.GoogleOAuthClientID)
	params.Set("redirect_uri", o.cfg.PublicURL+"/auth/oauth/google/callback")
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("state", state)
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

// oauthClient bounds all outbound OAuth calls.
var oauthClient = &http.Client{Timeout: 10 * time.Second}

// Exchange swaps the code for user info and returns the platform user
// (auto-creating an OAuth account if needed).
func (o *OAuth) Exchange(ctx context.Context, code string) (*domain.User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/token", strings.NewReader(url.Values{
			"code":          {code},
			"client_id":     {o.cfg.GoogleOAuthClientID},
			"client_secret": {o.cfg.GoogleOAuthSecret},
			"redirect_uri":  {o.cfg.PublicURL + "/auth/oauth/google/callback"},
			"grant_type":    {"authorization_code"},
		}.Encode()))
	if err != nil {
		return nil, domain.ErrTokenInvalid
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResp, err := oauthClient.Do(req)
	if err != nil {
		return nil, domain.ErrTokenInvalid
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode >= 400 {
		return nil, domain.ErrTokenInvalid
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		return nil, domain.ErrTokenInvalid
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet,
		"https://www.googleapis.com/oauth2/v3/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err := oauthClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := json.Unmarshal(body, &info); err != nil || info.Email == "" {
		return nil, domain.ErrTokenInvalid
	}

	email := strings.ToLower(info.Email)
	user, err := o.repos.Users.GetByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if user != nil {
		if user.Status == domain.UserStatusBanned {
			return nil, domain.ErrAccountBanned
		}
		// Only auto-link when the local account's email is verified;
		// otherwise an attacker verifying a Google address could take over
		// an unverified local account (PRD §5.9.1).
		if user.EmailVerifiedAt == nil {
			return nil, domain.ErrValidation.WithField("_", "Verify your email before signing in with Google.")
		}
		return user, nil
	}

	// Create: verified email, unusable random password.
	name := info.Name
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	username := o.uniqueUsername(ctx, strings.ToLower(strings.Split(email, "@")[0]))
	now := time.Now()
	user = &domain.User{
		ID:              util.NewUUID(),
		Email:           email,
		Name:            name,
		Username:        username,
		Timezone:        "UTC",
		Role:            domain.RoleUser,
		Status:          domain.UserStatusActive,
		EmailVerifiedAt: &now,
	}
	hash, err := randomPasswordHash()
	if err != nil {
		return nil, err
	}
	user.PasswordHash = hash
	if err := o.repos.Users.Create(ctx, user); err != nil {
		return nil, err
	}
	u, err := o.repos.Users.GetByEmail(ctx, email)
	if err != nil || u == nil {
		return nil, domain.ErrInternal
	}
	return u, nil
}

func randomPasswordHash() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return security.HashPassword(base64.RawURLEncoding.EncodeToString(b))
}

func (o *OAuth) uniqueUsername(ctx context.Context, base string) string {
	baseName := sanitizeUsername(base)
	candidate := baseName
	for i := 2; ; i++ {
		taken, err := o.repos.Users.UsernameTaken(ctx, candidate)
		if err != nil || taken {
			// A lookup error is indistinguishable from a conflict: keep
			// suffixing instead of returning an unverified candidate.
			candidate = fmt.Sprintf("%s%d", baseName, i)
			continue
		}
		return candidate
	}
}

const usernamePadAlphabet = "0123456789abcdef"

// sanitizeUsername keeps [a-z0-9_] only, capped at 30 chars. Short results
// (e.g. single-letter email locals) are padded with random hex so every
// generated username satisfies the 3-30 rule before it reaches the DB.
func sanitizeUsername(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 30 {
		out = out[:30]
	}
	if len(out) < 3 {
		pad := make([]byte, 3-len(out))
		if _, err := rand.Read(pad); err != nil {
			for i := range pad {
				pad[i] = 'x'
			}
		} else {
			for i := range pad {
				pad[i] = usernamePadAlphabet[int(pad[i])%len(usernamePadAlphabet)]
			}
		}
		out += string(pad)
	}
	return out
}
