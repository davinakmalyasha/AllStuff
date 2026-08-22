package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"bizverse/api/internal/domain"
)

// Compact JWT (HS256) — no external dependency; strictly typed claims.
// Access tokens: 15 min. verify_email: 24h. reset_password: 15 min (PRD §5.9.1).

type jwtClaims struct {
	Sub   string `json:"sub"`
	Role  string `json:"role"`
	User  string `json:"user,omitempty"`
	Typ   string `json:"typ"`
	Jti   string `json:"jti"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

func signJWT(secret string, c jwtClaims) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func parseJWT(secret, token string) (jwtClaims, error) {
	var c jwtClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return c, errors.New("bad signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, err
	}
	if time.Now().Unix() > c.Exp {
		return c, errors.New("expired")
	}
	return c, nil
}

type TokenKind string

const (
	TokenAccess        TokenKind = "access"
	TokenVerifyEmail   TokenKind = "verify_email"
	TokenResetPassword TokenKind = "reset_password"
	Token2FAChallenge  TokenKind = "2fa_challenge"
)

func IssueToken(secret string, kind TokenKind, user *domain.User, ttl time.Duration) (string, string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", "", err
	}
	now := time.Now()
	c := jwtClaims{
		Sub:  user.ID,
		Role: string(user.Role),
		User: user.Username,
		Typ:  string(kind),
		Jti:  base64.RawURLEncoding.EncodeToString(jti),
		Iat:  now.Unix(),
		Exp:  now.Add(ttl).Unix(),
	}
	tok, err := signJWT(secret, c)
	return tok, c.Jti, err
}

func ParseToken(secret, token string, kind TokenKind) (*domain.Claims, error) {
	c, err := parseJWT(secret, token)
	if err != nil {
		return nil, domain.ErrTokenInvalid
	}
	if c.Typ != string(kind) || c.Sub == "" {
		return nil, domain.ErrTokenInvalid
	}
	return &domain.Claims{
		UserID:    c.Sub,
		Role:      domain.UserRole(c.Role),
		Username:  c.User,
		TokenType: c.Typ,
		ExpiresAt: time.Unix(c.Exp, 0),
		TokenID:   c.Jti,
	}, nil
}
