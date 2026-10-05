package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"bizverse/api/internal/config"
	"bizverse/api/internal/domain"
	mail "bizverse/api/internal/email"
	"bizverse/api/internal/repo"
	"bizverse/api/internal/security"
	"bizverse/api/internal/util"
)

// Auth — accounts, sessions, tokens (PRD §5.9, §8.1).
type Auth struct {
	repos  *repo.Repos
	cfg    config.Config
	email  mail.Sender
	logger *slog.Logger

	// totpGuard enforces single-use TOTP codes. nil disables the replay check
	// entirely, so NewAuth keeps its old signature for tests and single-instance
	// deployments; UseSharedTOTPGuard upgrades it in place.
	totpGuard totpReplayGuard
}

func NewAuth(repos *repo.Repos, cfg config.Config, sender mail.Sender, logger *slog.Logger) *Auth {
	a := &Auth{repos: repos, cfg: cfg, email: sender, logger: logger}
	if cfg.RedisURL != "" {
		// Sharing is the point: with 2-3 replicas a per-process guard lets the
		// same phished code through once per replica.
		if guard, err := newRedisTOTPGuard(cfg.RedisURL, logger); err != nil {
			logger.Warn("totp replay guard unavailable; using process-local guard", "err", err)
			a.totpGuard = newLocalTOTPGuard()
		} else {
			a.totpGuard = guard
		}
	} else {
		a.totpGuard = newLocalTOTPGuard()
	}
	return a
}

var (
	emailRe    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,30}$`)
)

// reservedUsernames cannot be registered (PRD §6.6): they would collide with
// platform routes and system identities.
var reservedUsernames = map[string]bool{
	"admin": true, "administrator": true, "moderator": true, "support": true,
	"help": true, "security": true, "api": true, "apis": true, "root": true,
	"system": true, "bizverse": true, "official": true, "team": true,
	"staff": true, "owner": true, "me": true, "null": true, "undefined": true,
	"about": true, "contact": true, "terms": true, "privacy": true, "login": true,
	"register": true, "signup": true, "settings": true, "profile": true, "user": true,
}

type RegisterInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	SessionID    string
	CSRFToken    string
}

func (a *Auth) Register(ctx context.Context, in RegisterInput) (*domain.User, *TokenPair, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Name = strings.TrimSpace(in.Name)
	in.Username = strings.TrimSpace(in.Username)

	// §8.1 rules
	if !emailRe.MatchString(in.Email) {
		return nil, nil, domain.ErrValidation.WithField("email", "Enter a valid email address.")
	}
	if len(in.Password) < 8 || !strings.ContainsAny(in.Password, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(in.Password, "0123456789") {
		return nil, nil, domain.ErrValidation.WithField("password", "Password must be at least 8 characters and include a letter and a number.")
	}
	if n := len([]rune(in.Name)); n < 1 || n > 60 {
		return nil, nil, domain.ErrValidation.WithField("name", "Name must be 1–60 characters.")
	}
	if !usernameRe.MatchString(in.Username) {
		return nil, nil, domain.ErrValidation.WithField("username", "3–30 chars: lowercase letters, numbers, underscores.")
	}
	if reservedUsernames[in.Username] {
		return nil, nil, domain.ErrValidation.WithField("username", "That username is reserved.")
	}

	exists, err := a.repos.Users.GetByEmail(ctx, in.Email)
	if err != nil {
		return nil, nil, err
	}
	if exists != nil {
		return nil, nil, domain.ErrEmailTaken
	}
	taken, err := a.repos.Users.UsernameTaken(ctx, in.Username)
	if err != nil {
		return nil, nil, err
	}
	if taken {
		return nil, nil, domain.ErrUsernameTaken
	}

	hash, err := security.HashPassword(in.Password)
	if err != nil {
		return nil, nil, err
	}

	user := &domain.User{
		ID:           util.NewUUID(),
		Email:        in.Email,
		PasswordHash: hash,
		Name:         in.Name,
		Username:     in.Username,
		Timezone:     "UTC",
		Role:         domain.RoleUser,
		Status:       domain.UserStatusActive,
	}
	if err := a.repos.Users.Create(ctx, user); err != nil {
		return nil, nil, err
	}
	user, err = a.repos.Users.GetByID(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	user.ProfileLinks = map[string]any{}

	tokens, err := a.createSession(ctx, user, nil)
	if err != nil {
		return nil, nil, err
	}

	if err := a.sendVerificationEmail(ctx, user); err != nil {
		a.logger.Error("verify email send", "err", err, "user", user.ID)
	}

	a.logger.Info("user registered", "user", user.ID, "username", user.Username)
	return user, tokens, nil
}

func (a *Auth) Login(ctx context.Context, in LoginInput, ip net.IP, ua string) (*domain.User, *TokenPair, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	user, err := a.repos.Users.GetByEmail(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	// Constant-time-ish: always run a verify against a dummy hash to resist timing.
	if user == nil {
		_, _ = security.VerifyPassword(in.Password, "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRzYWx0c2FsdA")
		// Pending-deletion accounts are invisible to GetByEmail; tell the
		// legitimate owner about the grace-period restore path instead of a
		// bare "invalid credentials" (PRD §5.9.2).
		if pend, _, derr := a.repos.Users.GetByEmailIncludingDeleted(ctx, email); derr == nil && pend != nil {
			if ok, verr := security.VerifyPassword(in.Password, pend.PasswordHash); verr == nil && ok && pend.PasswordHash != "" {
				return nil, nil, domain.ErrAccountPendingDeletion
			}
		}
		return nil, nil, domain.ErrInvalidCreds
	}
	ok, err := security.VerifyPassword(in.Password, user.PasswordHash)
	if err != nil || !ok {
		return nil, nil, domain.ErrInvalidCreds
	}
	if err := a.checkUserStatus(user); err != nil {
		return nil, nil, err
	}

	// 2FA gate: issue a short-lived challenge token instead of a session (PRD §5.9.1).
	if challenge, gated, err := a.twoFAChallenge(ctx, user); err != nil {
		return nil, nil, err
	} else if gated {
		return user, &TokenPair{RefreshToken: challenge}, domain.Err2FARequired
	}

	tokens, err := a.createSession(ctx, user, &clientMeta{IP: ip, UA: ua})
	if err != nil {
		return nil, nil, err
	}
	a.maybeNewDeviceAlert(ctx, user, ip, ua)
	a.logger.Info("login", "user", user.ID)
	return user, tokens, nil
}

// twoFAChallenge is the single gate in front of session issuance.
//
// It exists as one function because it previously lived inline in Login, and
// IssueSessionForUser - the path the OAuth callback takes - called
// createSession directly. That made a Google login first-factor-equivalent but
// NOT two-factor-equivalent: an account with TOTP enrolled could be taken over
// by anyone who could also drive a Google login, because the enrolled second
// factor was never asked for. A password-only compromise of a 2FA account was
// sufficient.
//
// Every path that mints a session must go through here: Login,
// IssueSessionForUser (OAuth), and anything added later. Returning a challenge
// rather than an error is deliberate - a 2FA challenge is a SUCCESSFUL first
// factor, so callers must not charge it against a failure budget.
func (a *Auth) twoFAChallenge(ctx context.Context, user *domain.User) (challenge string, gated bool, err error) {
	st, err := a.repos.TFA.Get(ctx, user.ID)
	if err != nil {
		return "", false, err
	}
	if st == nil || st.EnabledAt == nil {
		return "", false, nil
	}
	tok, _, err := security.IssueToken(a.cfg.JWTSecret, security.Token2FAChallenge, user, twoFAChallengeTTL)
	if err != nil {
		return "", false, err
	}
	return tok, true, nil
}

// maybeNewDeviceAlert emails the user when a login arrives from an IP with
// no prior session (PRD §5.9.1 / §8.1 "new device" alert). Best-effort.
func (a *Auth) maybeNewDeviceAlert(ctx context.Context, user *domain.User, ip net.IP, ua string) {
	if user.EmailVerifiedAt == nil {
		return
	}
	ipStr := ""
	if ip != nil {
		ipStr = ip.String()
	}
	var known bool
	var err error
	if ipStr == "" {
		err = a.repos.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM sessions WHERE user_id = $1 AND revoked_at IS NULL LIMIT 1)`,
			user.ID).Scan(&known)
	} else {
		err = a.repos.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM sessions WHERE user_id = $1 AND revoked_at IS NULL AND host(ip) = $2 LIMIT 1)`,
			user.ID, ipStr).Scan(&known)
	}
	if err == nil && !known {
		suffix := ""
		if ipStr != "" {
			suffix = " (IP: " + ipStr + ")"
		}
		_ = a.email.Send(ctx, user.Email, "New sign-in to your account",
			mail.WrapHTML(a.cfg.PublicURL, "New sign-in to your account",
				fmt.Sprintf(`<p>Hi %s,</p><p>Your account was just signed in from a new device or location%s.</p>
				<p>If this wasn't you, reset your password and revoke sessions from your security page.</p>`,
					htmlEscape(user.Name), suffix)))
	}
}

type clientMeta struct {
	IP net.IP
	UA string
}

func (a *Auth) Refresh(ctx context.Context, refreshToken string, ip net.IP, ua string) (*domain.User, *TokenPair, error) {
	hash := sha256.Sum256([]byte(refreshToken))
	sess, err := a.repos.Sessions.GetByTokenHash(ctx, hex.EncodeToString(hash[:]))
	if err != nil {
		return nil, nil, err
	}
	if sess == nil {
		return nil, nil, domain.ErrSessionInvalid
	}
	if sess.RevokedAt != nil {
		// Replay of an already-rotated refresh token is the classic theft
		// signal: revoke the whole session family, not just reject.
		//
		// The error is logged rather than discarded. This used to be `_ =`,
		// which combined with the uuid/sentinel bug in RevokeAllExcept to make
		// the anti-theft control a silent no-op with nothing in the logs.
		if err := a.repos.Sessions.RevokeAllExcept(ctx, sess.UserID, ""); err != nil {
			slog.Error("refresh replay: session family revocation failed",
				"user_id", sess.UserID, "session_id", sess.ID, "err", err)
		}
		return nil, nil, domain.ErrSessionInvalid
	}
	user, err := a.repos.Users.GetByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user == nil {
		return nil, nil, domain.ErrSessionInvalid
	}
	if err := a.checkUserStatus(user); err != nil {
		return nil, nil, err
	}
	// Rotation: revoke old, issue new (PRD §5.9.1).
	if err := a.repos.Sessions.Revoke(ctx, sess.ID); err != nil {
		return nil, nil, err
	}
	tokens, err := a.createSession(ctx, user, &clientMeta{IP: ip, UA: ua})
	if err != nil {
		return nil, nil, err
	}
	return user, tokens, nil
}

func (a *Auth) Logout(ctx context.Context, sessionID string) error {
	return a.repos.Sessions.Revoke(ctx, sessionID)
}

func (a *Auth) VerifyEmail(ctx context.Context, token string) error {
	claims, err := security.ParseToken(a.cfg.JWTSecret, token, security.TokenVerifyEmail)
	if err != nil {
		return err
	}
	// Single-use: a captured verification link must not keep working.
	if err := a.consumeToken(ctx, claims.TokenID); err != nil {
		return err
	}
	user, err := a.repos.Users.GetByID(ctx, claims.UserID)
	if err != nil {
		return err
	}
	if user == nil {
		return domain.ErrTokenInvalid
	}
	return a.repos.Users.MarkEmailVerified(ctx, user.ID)
}

// consumeToken records a stateless email-token JTI as used. Reuse returns
// ErrTokenInvalid (the INSERT conflicts → zero rows affected).
func (a *Auth) consumeToken(ctx context.Context, jti string) error {
	tag, err := a.repos.Exec(ctx,
		`INSERT INTO consumed_tokens (jti) VALUES ($1) ON CONFLICT (jti) DO NOTHING`, jti)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTokenInvalid
	}
	return nil
}

func (a *Auth) ForgotPassword(ctx context.Context, email string) error {
	user, err := a.repos.Users.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || user == nil {
		// Never reveal account existence (PRD §9.3).
		return nil
	}
	token, _, err := security.IssueToken(a.cfg.JWTSecret, security.TokenResetPassword, user, 15*time.Minute)
	if err != nil {
		return err
	}
	link := fmt.Sprintf("%s/reset-password?token=%s", a.cfg.PublicURL, url.QueryEscape(token))
	return a.email.Send(ctx, user.Email, "Reset your password",
		mail.WrapHTML(a.cfg.PublicURL, "Reset your password",
			// Human label only; the raw token stays in the href.
			fmt.Sprintf(`<p>Hi %s,</p><p><a href="%s">Reset your password</a>.</p><p>Link expires in 15 minutes.</p>`,
				htmlEscape(user.Name), link)))
}

func (a *Auth) ResetPassword(ctx context.Context, token, password string) error {
	if len(password) < 8 || !strings.ContainsAny(password, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(password, "0123456789") {
		return domain.ErrValidation.WithField("password", "Password must be at least 8 characters and include a letter and a number.")
	}
	claims, err := security.ParseToken(a.cfg.JWTSecret, token, security.TokenResetPassword)
	if err != nil {
		return err
	}
	// Single-use: a captured reset link stops working after the first use —
	// including after the legitimate user has already rotated their password.
	if err := a.consumeToken(ctx, claims.TokenID); err != nil {
		return err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	// Password change and session revocation are one atomic unit. Previously
	// they were two statements: if the revocation failed, the caller got a 500
	// while the password had ALREADY been changed — so the user believed the
	// reset had failed and retried, and any attacker-held session survived the
	// entire incident-response window. A partial ATO recovery must not be
	// possible.
	tx, err := a.repos.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txRepos := repo.NewForTx(tx)
	if err := txRepos.Users.SetPassword(ctx, claims.UserID, hash); err != nil {
		return err
	}
	// Revoke all sessions on password change (PRD §5.9.1).
	if err := txRepos.Sessions.RevokeAllExcept(ctx, claims.UserID, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IssueAccessToken builds a fresh access token for an authenticated user.
func (a *Auth) IssueAccessToken(user *domain.User) (string, error) {
	tok, _, err := security.IssueToken(a.cfg.JWTSecret, security.TokenAccess, user, 15*time.Minute)
	return tok, err
}

// IssueSessionForUser creates a session for an already-authenticated user.
//
// Used by the OAuth callback and by 2FA completion. The 2FA challenge it returns
// in TokenPair.RefreshToken is NOT a session token: the caller must not set
// session cookies from it and must instead route the user to complete the second
// factor. domain.Err2FARequired is returned alongside it, exactly as Login does,
// so there is one contract rather than two.
func (a *Auth) IssueSessionForUser(ctx context.Context, user *domain.User, ua string) (*TokenPair, error) {
	if err := a.checkUserStatus(user); err != nil {
		return nil, err
	}
	// An OAuth login is first-factor-equivalent, so it must clear the same bar a
	// password does: an enrolled second factor is still required. See
	// twoFAChallenge for what this cost when it did not.
	if challenge, gated, err := a.twoFAChallenge(ctx, user); err != nil {
		return nil, err
	} else if gated {
		return &TokenPair{RefreshToken: challenge}, domain.Err2FARequired
	}
	return a.createSession(ctx, user, &clientMeta{UA: ua})
}

func (a *Auth) createSession(ctx context.Context, user *domain.User, meta *clientMeta) (*TokenPair, error) {
	refresh := make([]byte, 32)
	if _, err := rand.Read(refresh); err != nil {
		return nil, err
	}
	refreshToken := base64.RawURLEncoding.EncodeToString(refresh)
	hash := sha256.Sum256([]byte(refreshToken))

	var ipStr, uaStr *string
	if meta != nil {
		if meta.IP != nil {
			s := meta.IP.String()
			ipStr = &s
		}
		if meta.UA != "" {
			s := meta.UA
			uaStr = &s
		}
	}

	sess := &domain.Session{
		ID:        util.NewUUID(),
		UserID:    user.ID,
		TokenHash: hex.EncodeToString(hash[:]),
		IP:        ipStr,
		UserAgent: uaStr,
	}
	if err := a.repos.Sessions.Create(ctx, sess); err != nil {
		return nil, err
	}

	access, err := a.IssueAccessToken(user)
	if err != nil {
		return nil, err
	}
	csrf, err := security.GenerateCSRFToken()
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refreshToken,
		SessionID:    sess.ID,
		CSRFToken:    csrf,
	}, nil
}

func (a *Auth) sendVerificationEmail(ctx context.Context, user *domain.User) error {
	token, _, err := security.IssueToken(a.cfg.JWTSecret, security.TokenVerifyEmail, user, 24*time.Hour)
	if err != nil {
		return err
	}
	link := fmt.Sprintf("%s/verify-email?token=%s", a.cfg.PublicURL, url.QueryEscape(token))
	return a.email.Send(ctx, user.Email, "Verify your email",
		mail.WrapHTML(a.cfg.PublicURL, "Verify your email",
			// Human label only; the raw token stays in the href.
			fmt.Sprintf(`<p>Welcome to BizVerse, %s.</p><p><a href="%s">Verify your email</a>.</p><p>Link expires in 24 hours.</p>`,
				htmlEscape(user.Name), link)))
}

// ResendVerification re-issues the registration-time verify-email token and
// mails it (same token-issue + delivery path as Register). Unknown or already
// verified accounts are indistinguishable from success (PRD §9.3); the
// caller owns the per-account send budget.
func (a *Auth) ResendVerification(ctx context.Context, email string) {
	user, err := a.repos.Users.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || user == nil || user.EmailVerifiedAt != nil {
		return
	}
	if err := a.sendVerificationEmail(ctx, user); err != nil {
		a.logger.Error("verify email resend", "err", err, "user", user.ID)
	}
}

// ---- 2FA (PRD §5.9.1) ----

// stepUp verifies the account password before an operation that changes how the
// account is authenticated.
//
// OWASP ASVS 3.3.1 and 2.5 both require re-authentication before a security
// setting changes. Without it, any 15-minute access token was enough to rewrite
// MFA state - which is the whole risk 2FA exists to bound. An access token can
// come from XSS, a shared or kiosk browser, a logged-out tab that still holds
// one, a Referer, or a proxy log, and none of those should be able to silently
// remove the second factor and leave the account reachable by password alone.
//
// A TOTP code is deliberately NOT accepted as the step-up credential for
// Enroll2FA. Enrollment happens before a factor exists, so requiring one would
// make the feature impossible; and for Disable2FA the caller already supplies a
// fresh TOTP code, which is itself a second factor. What is missing in both
// cases is the PASSWORD, which is the credential an attacker with only a stolen
// token does not have.
func (a *Auth) stepUp(ctx context.Context, userID, password string) error {
	if password == "" {
		return domain.ErrValidation.WithField("current_password", "Confirm your password to continue.")
	}
	u, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil {
		return domain.ErrNotFound
	}
	ok, err := security.VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		// One message for "no such user" and "wrong password" so this cannot
		// become an account-existence oracle.
		return domain.ErrInvalidCreds
	}
	return nil
}

// Enroll2FA generates a TOTP secret for the user (not yet enabled).
//
// currentPassword is a step-up credential, not a session check - see stepUp.
func (a *Auth) Enroll2FA(ctx context.Context, userID, currentPassword string) (secret, otpauthURL string, err error) {
	if err := a.stepUp(ctx, userID, currentPassword); err != nil {
		return "", "", err
	}
	// Re-enrolling over an ENABLED factor would silently replace a working
	// authenticator with an attacker-chosen secret, and it did so with nothing
	// more than a valid access token. Disable first, explicitly.
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if st != nil && st.EnabledAt != nil {
		return "", "", domain.ErrValidation.WithField("_",
			"2FA is already enabled. Disable it before enrolling a new authenticator.")
	}
	secret, err = security.GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	// Encrypt at rest (PRD §9.3): AES-256-GCM under a key derived from the
	// JWT secret. Rows written before this scheme used XOR and are opened
	// transparently until re-enrollment re-seals them.
	enc, err := a.sealSecret(secret)
	if err != nil {
		return "", "", err
	}
	if err := a.repos.TFA.UpsertSecret(ctx, userID, enc); err != nil {
		return "", "", err
	}
	u, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil || u == nil {
		return "", "", domain.ErrNotFound
	}
	return secret, security.OtpauthURL("BizVerse", u.Email, secret), nil
}

// Confirm2FA verifies a code and enables 2FA, issuing 10 recovery codes.
func (a *Auth) Confirm2FA(ctx context.Context, userID, code string) ([]string, error) {
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, domain.ErrValidation.WithField("_", "Start enrollment first.")
	}
	secret, err := a.openSecret(st.SecretEncrypted)
	if err != nil {
		return nil, err
	}
	if !security.ValidateTOTP(secret, code) {
		return nil, domain.ErrValidation.WithField("code", "Code is invalid.")
	}
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		codes[i] = fmt.Sprintf("%X-%X-%X-%X", b[0:2], b[2:4], b[4:6], b[6:8])
		// Recovery codes are passwords: argon2id, not bare SHA-256.
		h, herr := security.HashPassword(codes[i])
		if herr != nil {
			return nil, herr
		}
		hashes[i] = h
	}
	if err := a.repos.TFA.Enable(ctx, userID, hashes); err != nil {
		return nil, err
	}
	_, _ = a.repos.Exec(ctx, `INSERT INTO auth_events (id, user_id, event) VALUES ($1, $2, '2fa_enable')`,
		util.NewUUID(), userID)
	return codes, nil
}

// Disable2FA turns off the second factor.
//
// currentPassword is a step-up credential - see stepUp. The TOTP code is
// required too, and both are needed: the code proves possession of the current
// authenticator, the password proves the caller is the account owner rather than
// someone holding a leaked token and a code they observed.
//
// The enabled-state guard below used to test only `st == nil`, which meant a
// PENDING, never-confirmed enrollment could be "disabled" - a no-op that
// reported success. It now tests the same predicate Confirm2FA will later set.
func (a *Auth) Disable2FA(ctx context.Context, userID, code, currentPassword string) error {
	if err := a.stepUp(ctx, userID, currentPassword); err != nil {
		return err
	}
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return err
	}
	if st == nil || st.EnabledAt == nil {
		return domain.ErrValidation.WithField("_", "2FA is not enabled.")
	}
	secret, serr := a.openSecret(st.SecretEncrypted)
	if serr != nil {
		return serr
	}
	if !a.validateFreshTOTP(ctx, userID, secret, code) {
		return domain.ErrValidation.WithField("code", "Code is invalid.")
	}
	if err := a.repos.TFA.Disable(ctx, userID); err != nil {
		return err
	}
	_, _ = a.repos.Exec(ctx, `INSERT INTO auth_events (id, user_id, event) VALUES ($1, $2, '2fa_disable')`,
		util.NewUUID(), userID)
	return nil
}

func (a *Auth) TFAStatus(ctx context.Context, userID string) (map[string]any, error) {
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	enabled := st != nil && st.EnabledAt != nil
	return map[string]any{"enabled": enabled}, nil
}

// usedTOTPStepTTL is how long a consumed timestep is remembered. It covers the
// full ±1 verification window (90s) with generous headroom, so pruning can never
// re-admit a code that is still inside its validity window.
const usedTOTPStepTTL = 10 * time.Minute

// twoFAChallengeTTL is how long a first-factor success stays redeemable for a
// second factor.
//
// It is deliberately half usedTOTPStepTTL. A challenge should not outlive the
// window in which the code it is redeemed with could still validate, or an
// attacker who intercepted the challenge could spend it against a code captured
// much later. The relationship is asserted by TestChallengeTTLDoesNotOutliveThe
// ReplayWindow so the two cannot drift apart silently.
const twoFAChallengeTTL = 5 * time.Minute

// validateFreshTOTP verifies a code and then claims its timestep, so the same
// code cannot mint a second session inside its validity window.
//
// The claim is best-effort in one direction only. If the shared store is
// unreachable we log and allow: denying would lock every enrolled user out of
// their account whenever Redis is down, which is a far worse outcome than the
// narrow replay it reopens. When the guard is available, a duplicate claim is
// refused — that is the case this exists for.
func (a *Auth) validateFreshTOTP(ctx context.Context, userID, secret, code string) bool {
	step, ok := security.ValidateTOTPStep(secret, code)
	if !ok {
		return false
	}
	if a.totpGuard == nil {
		return true
	}
	fresh, err := a.totpGuard.claim(ctx, userID, step)
	if err != nil {
		if a.logger != nil {
			a.logger.Warn("totp replay guard unavailable; allowing code", "err", err)
		}
		return true
	}
	return fresh
}

// Verify2FA completes a 2FA-gated login with TOTP or a recovery code.
func (a *Auth) Verify2FA(ctx context.Context, challengeToken, code string, ip net.IP, ua string) (*domain.User, *TokenPair, error) {
	claims, err := security.ParseToken(a.cfg.JWTSecret, challengeToken, security.Token2FAChallenge)
	if err != nil {
		return nil, nil, err
	}
	user, err := a.repos.Users.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user == nil {
		return nil, nil, domain.ErrNotFound
	}
	st, err := a.repos.TFA.Get(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	if st == nil {
		return nil, nil, domain.ErrSessionInvalid
	}
	secret, serr := a.openSecret(st.SecretEncrypted)
	if serr != nil {
		return nil, nil, serr
	}
	if a.validateFreshTOTP(ctx, user.ID, secret, code) {
		return a.finishLogin(ctx, user, ip, ua)
	}
	// Recovery code path: codes are argon2id-hashed passwords — verify
	// against each stored hash, then consume the matched one.
	if st.RecoveryCodesHash != nil {
		var stored []string
		if json.Unmarshal(st.RecoveryCodesHash, &stored) == nil {
			for _, h := range stored {
				ok, verr := security.VerifyPassword(code, h)
				if verr != nil || !ok {
					continue
				}
				if err := a.repos.TFA.UseRecoveryCode(ctx, user.ID, h); err != nil {
					return nil, nil, err
				}
				return a.finishLogin(ctx, user, ip, ua)
			}
		}
	}
	return nil, nil, domain.ErrValidation.WithField("code", "2FA code is invalid.")
}

func (a *Auth) finishLogin(ctx context.Context, user *domain.User, ip net.IP, ua string) (*domain.User, *TokenPair, error) {
	if err := a.checkUserStatus(user); err != nil {
		return nil, nil, err
	}
	tokens, err := a.createSession(ctx, user, &clientMeta{IP: ip, UA: ua})
	if err != nil {
		return nil, nil, err
	}
	return user, tokens, nil
}

func xorCipher(data, key string) string {
	out := make([]byte, len(data))
	for i := 0; i < len(data); i++ {
		out[i] = data[i] ^ key[i%len(key)]
	}
	return string(out)
}

// totpKey derives the AES key for TOTP secrets from the JWT secret, keeping
// the two secret classes related but not identical.
func (a *Auth) totpKey() []byte {
	sum := sha256.Sum256([]byte("bizverse:totp:" + a.cfg.JWTSecret))
	return sum[:]
}

// sealSecret encrypts a TOTP secret with AES-256-GCM ("v1:" prefix).
func (a *Auth) sealSecret(plain string) (string, error) {
	block, err := aes.NewCipher(a.totpKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return "v1:" + base64.StdEncoding.EncodeToString(out), nil
}

// openSecret decrypts v1 payloads; legacy XOR rows fall through so existing
// enrollments keep working until re-enrollment re-seals them.
func (a *Auth) openSecret(enc string) (string, error) {
	if b, ok := strings.CutPrefix(enc, "v1:"); ok {
		raw, err := base64.StdEncoding.DecodeString(b)
		if err != nil {
			return "", domain.ErrInternal
		}
		block, err := aes.NewCipher(a.totpKey())
		if err != nil {
			return "", err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return "", err
		}
		if len(raw) < gcm.NonceSize() {
			return "", domain.ErrInternal
		}
		plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
		if err != nil {
			return "", domain.ErrInternal
		}
		return string(plain), nil
	}
	return xorCipher(enc, a.cfg.JWTSecret), nil
}

// ---- sessions & export ----

func (a *Auth) Sessions(ctx context.Context, userID string) ([]*domain.Session, error) {
	return a.repos.Sessions.ListByUser(ctx, userID)
}

func (a *Auth) RevokeSession(ctx context.Context, userID, sessionID string) error {
	list, err := a.repos.Sessions.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, s := range list {
		if s.ID == sessionID {
			return a.repos.Sessions.Revoke(ctx, sessionID)
		}
	}
	return domain.ErrNotFound
}

func (a *Auth) RevokeOthers(ctx context.Context, userID, keepID string) error {
	return a.repos.Sessions.RevokeAllExcept(ctx, userID, keepID)
}

// ExportData builds the user's full data snapshot (PRD §5.9.2).
// ---- account deletion (PRD §5.9.2: 14-day grace) ----

func (a *Auth) RequestDeletion(ctx context.Context, userID, password string) error {
	user, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrNotFound
	}
	ok, err := security.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return domain.ErrInvalidCreds
	}
	if _, err := a.repos.Exec(ctx, `
		UPDATE users SET deleted_at = now(), updated_at = now() WHERE id = $1`, userID); err != nil {
		return err
	}
	// Revoke all sessions.
	return a.repos.Sessions.RevokeAllExcept(ctx, userID, "")
}

func (a *Auth) CancelDeletion(ctx context.Context, userID, password string) error {
	user, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrNotFound
	}
	// Require credential re-entry: a stolen access token (≤15 min window)
	// must not be able to silently resurrect a deleted account.
	ok, err := security.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return domain.ErrInvalidCreds
	}
	if user.DeletedAt == nil {
		return nil
	}
	_, err = a.repos.Exec(ctx, `
		UPDATE users SET deleted_at = NULL, updated_at = now() WHERE id = $1 AND deleted_at IS NOT NULL`, userID)
	return err
}

// RestoreAccount cancels a pending deletion with password proof and signs the
// user back in (grace-period path of PRD §5.9.2). Anonymized accounts (past
// 14 days) have no usable password hash and fail verification naturally.
func (a *Auth) RestoreAccount(ctx context.Context, email, password string, ip net.IP, ua string) (*domain.User, *TokenPair, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	user, deletedAt, err := a.repos.Users.GetByEmailIncludingDeleted(ctx, email)
	if err != nil {
		return nil, nil, err
	}
	if user == nil || deletedAt == nil || user.PasswordHash == "" {
		// Match Login's cost profile so unknown emails are not distinguishable
		// by response time (restore is a password-bearing endpoint too).
		_, _ = security.VerifyPassword(password, "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRzYWx0c2FsdA")
		return nil, nil, domain.ErrInvalidCreds
	}
	ok, err := security.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return nil, nil, domain.ErrInvalidCreds
	}
	if time.Since(*deletedAt) > 14*24*time.Hour {
		return nil, nil, domain.ErrValidation.WithField("_", "The restoration window has closed.")
	}
	if _, err := a.repos.Exec(ctx,
		`UPDATE users SET deleted_at = NULL, updated_at = now() WHERE id = $1`, user.ID); err != nil {
		return nil, nil, err
	}
	fresh, err := a.repos.Users.GetByID(ctx, user.ID)
	if err != nil || fresh == nil {
		return nil, nil, domain.ErrInternal
	}
	if err := a.checkUserStatus(fresh); err != nil {
		return nil, nil, err
	}
	tokens, err := a.createSession(ctx, fresh, &clientMeta{IP: ip, UA: ua})
	if err != nil {
		return nil, nil, err
	}
	a.logger.Info("account restored", "user", fresh.ID)
	return fresh, tokens, nil
}

// PurgeExpiredDeletions anonymizes accounts past the 14-day grace
// (PRD §5.9.2 / E6): every personal identifier is scrubbed while authored
// content (reviews, comments) stays intact as "Deleted User" — the users row
// must survive because engagement tables reference it.
func (a *Auth) PurgeExpiredDeletions(ctx context.Context) (int64, error) {
	tag, err := a.repos.Exec(ctx, `
		UPDATE users SET
			email = 'deleted+' || left(replace(id::text, '-', ''), 24) || '@anon.invalid',
			name = 'Deleted User',
			username = 'u' || left(replace(id::text, '-', ''), 15),
			password_hash = NULL,
			avatar_url = NULL,
			bio = NULL,
			profile_links = '{}'::jsonb,
			notification_prefs = '{}',
			digest_opt_in = false,
			updated_at = now()
		WHERE deleted_at IS NOT NULL AND deleted_at < now() - interval '14 days'
		  AND email NOT LIKE 'deleted+%@anon.invalid'`)
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if n > 0 {
		_, _ = a.repos.Exec(ctx, `
			UPDATE sessions s SET revoked_at = now()
			FROM users u
			WHERE u.id = s.user_id AND s.revoked_at IS NULL
			  AND u.deleted_at IS NOT NULL AND u.deleted_at < now() - interval '14 days'`)
		// Push subscriptions are device credentials tied to the account:
		// they must not outlive the purge.
		_, _ = a.repos.Exec(ctx, `
			DELETE FROM push_subscriptions p USING users u
			WHERE p.user_id = u.id
			  AND u.deleted_at IS NOT NULL AND u.deleted_at < now() - interval '14 days'`)
	}
	return n, nil
}

func (a *Auth) ExportData(ctx context.Context, userID string) (map[string]any, error) {
	user, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	businesses, _ := a.repos.Businesses.ListByOwner(ctx, userID)
	products := []any{}
	for _, b := range businesses {
		list, err := a.repos.Products.ListByBusiness(ctx, b.ID)
		if err == nil {
			for _, p := range list {
				options, _ := a.repos.Products.ListOptions(ctx, p.ID)
				variants, _ := a.repos.Products.ListVariants(ctx, p.ID)
				products = append(products, map[string]any{"product": p, "options": options, "variants": variants})
			}
		}
	}
	collections, _ := a.repos.Engagement.ListCollections(ctx, userID)
	colItems := []any{}
	for _, c := range collections {
		items, _ := a.repos.Engagement.ListItems(ctx, c.ID)
		colItems = append(colItems, map[string]any{"collection": c, "items": items})
	}
	reviews, err := a.repos.Query(ctx, `
		SELECT id, business_id, product_id, rating, text, created_at FROM reviews
		WHERE user_id = $1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return nil, err
	}
	revRows := []any{}
	for reviews.Next() {
		var id, bid, pid *string
		var rating int
		var text string
		var createdAt time.Time
		if err := reviews.Scan(&id, &bid, &pid, &rating, &text, &createdAt); err == nil {
			revRows = append(revRows, map[string]any{"id": id, "business_id": bid, "product_id": pid, "rating": rating, "text": text, "created_at": createdAt})
		}
	}
	reviews.Close()
	comments, err := a.repos.Query(ctx, `
		SELECT id, business_id, text, created_at FROM comments WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	comRows := []any{}
	for comments.Next() {
		var id, bid, text string
		var createdAt time.Time
		if err := comments.Scan(&id, &bid, &text, &createdAt); err == nil {
			comRows = append(comRows, map[string]any{"id": id, "business_id": bid, "text": text, "created_at": createdAt})
		}
	}
	comments.Close()

	// Chats (PRD §5.9.2): threads where the user is an active participant,
	// plus the last 500 messages across them — both hard-capped so the
	// payload stays bounded for long-lived accounts.
	threads := []any{}
	threadIDs := []string{}
	tRows, err := a.repos.Query(ctx, `
		SELECT t.id::text, t.type::text, t.business_id::text, t.status, t.created_at
		FROM chat_threads t
		JOIN chat_participants p ON p.thread_id = t.id
		WHERE p.user_id = $1 AND p.left_at IS NULL
		ORDER BY t.created_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, err
	}
	for tRows.Next() {
		var id, typ, status string
		var businessID *string
		var createdAt time.Time
		if err := tRows.Scan(&id, &typ, &businessID, &status, &createdAt); err != nil {
			return nil, err
		}
		entry := map[string]any{"id": id, "type": typ, "status": status, "created_at": createdAt}
		if businessID != nil && *businessID != "" {
			entry["business_id"] = *businessID
		}
		threads = append(threads, entry)
		threadIDs = append(threadIDs, id)
	}
	if err := tRows.Err(); err != nil {
		return nil, err
	}
	tRows.Close()
	messages := []map[string]any{}
	if len(threadIDs) > 0 {
		mRows, err := a.repos.Query(ctx, `
			SELECT id, thread_id::text, sender_id::text, type::text, body, created_at
			FROM chat_messages
			WHERE thread_id = ANY($1::uuid[]) AND deleted_for <> 'everyone'
			ORDER BY id DESC LIMIT 500`, threadIDs)
		if err != nil {
			return nil, err
		}
		for mRows.Next() {
			var id int64
			var threadID, senderID, typ string
			var body *string
			var createdAt time.Time
			if err := mRows.Scan(&id, &threadID, &senderID, &typ, &body, &createdAt); err != nil {
				return nil, err
			}
			messages = append(messages, map[string]any{"id": id, "thread_id": threadID, "sender_id": senderID, "type": typ, "body": body, "created_at": createdAt})
		}
		if err := mRows.Err(); err != nil {
			return nil, err
		}
		mRows.Close()
		// Collected newest-first; emit oldest-first.
		ordered := make([]map[string]any, len(messages))
		for i, m := range messages {
			ordered[len(messages)-1-i] = m
		}
		messages = ordered
	}

	return map[string]any{
		"exported_at": time.Now(),
		"profile": map[string]any{
			"id": user.ID, "name": user.Name, "username": user.Username,
			"email": user.Email, "bio": user.Bio, "timezone": user.Timezone,
			"created_at": user.CreatedAt,
		},
		"businesses":  businesses,
		"products":    products,
		"collections": colItems,
		"reviews":     revRows,
		"comments":    comRows,
		"threads":     threads,
		"messages":    messages,
	}, nil
}

func (a *Auth) LoginHistory(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	rows, err := a.repos.Query(ctx, `
		SELECT event, ip, user_agent, created_at FROM auth_events
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var event string
		var ip *net.IP
		var ua *string
		var at time.Time
		if err := rows.Scan(&event, &ip, &ua, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"event": event, "ip": ip, "user_agent": ua, "created_at": at})
	}
	return out, rows.Err()
}

// NotificationPrefs — per-type × per-channel matrix + quiet hours (PRD §5.7).
func (a *Auth) NotificationPrefs(ctx context.Context, userID string) (map[string]any, error) {
	var prefs map[string]any
	err := a.repos.QueryRow(ctx,
		`SELECT notification_prefs FROM users WHERE id = $1`, userID).Scan(&prefs)
	if err != nil || prefs == nil {
		prefs = map[string]any{}
	}
	if _, ok := prefs["channels"]; !ok {
		prefs["channels"] = map[string]any{"in_app": true, "push": true, "email": "digest"}
	}
	if _, ok := prefs["quiet_hours"]; !ok {
		prefs["quiet_hours"] = map[string]any{"enabled": false}
	}
	// Weekly digest opt-in lives on the user row (jobs/digest.go reads it).
	var opted bool
	_ = a.repos.QueryRow(ctx,
		`SELECT coalesce(digest_opt_in, false) FROM users WHERE id = $1`, userID).Scan(&opted)
	prefs["digest_opt_in"] = opted
	return prefs, nil
}

func (a *Auth) SetNotificationPrefs(ctx context.Context, userID string, prefs map[string]any) error {
	// digest_opt_in mirrors into the users column the digest job reads.
	if v, ok := prefs["digest_opt_in"]; ok {
		if b, isBool := v.(bool); isBool {
			if _, err := a.repos.Exec(ctx,
				`UPDATE users SET digest_opt_in = $2, updated_at = now() WHERE id = $1`,
				userID, b); err != nil {
				return err
			}
		}
		delete(prefs, "digest_opt_in")
	}
	_, err := a.repos.Exec(ctx,
		`UPDATE users SET notification_prefs = $2, updated_at = now() WHERE id = $1`,
		userID, prefs)
	return err
}

// ---- password & email changes (identity-standard) ----

func (a *Auth) ChangePassword(ctx context.Context, userID, current, newPass, keepSessionID string) error {
	user, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrNotFound
	}
	ok, err := security.VerifyPassword(current, user.PasswordHash)
	if err != nil || !ok {
		return domain.ErrInvalidCreds
	}
	if len(newPass) < 8 || !strings.ContainsAny(newPass, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(newPass, "0123456789") {
		return domain.ErrValidation.WithField("password", "Password must be at least 8 characters and include a letter and a number.")
	}
	hash, err := security.HashPassword(newPass)
	if err != nil {
		return err
	}
	// Atomic with the revocation, for the same reason as ResetPassword: a
	// failed revocation must not leave a changed password alongside live
	// sessions. keepSessionID is "" when the caller has no refresh cookie, and
	// RevokeAllExcept treats that as "revoke everything" rather than passing
	// the empty string into a uuid comparison.
	tx, err := a.repos.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txRepos := repo.NewForTx(tx)
	if err := txRepos.Users.SetPassword(ctx, userID, hash); err != nil {
		return err
	}
	// Revoke all other sessions; keep the caller's own session alive.
	if err := txRepos.Sessions.RevokeAllExcept(ctx, userID, keepSessionID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ChangeEmail requires the current password, a valid TOTP code when 2FA is
// enrolled, and always notifies the OLD address — otherwise a hijacked
// session plus a leaked password could permanently lock the real owner out.
func (a *Auth) ChangeEmail(ctx context.Context, userID, password, newEmail, totpCode string) error {
	newEmail = strings.ToLower(strings.TrimSpace(newEmail))
	if !emailRe.MatchString(newEmail) {
		return domain.ErrValidation.WithField("email", "Enter a valid email address.")
	}
	user, err := a.repos.Users.GetByID(ctx, userID)
	if err != nil || user == nil {
		return domain.ErrNotFound
	}
	ok, err := security.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return domain.ErrInvalidCreds
	}
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return err
	}
	if st != nil && st.EnabledAt != nil {
		secret, serr := a.openSecret(st.SecretEncrypted)
		if serr != nil {
			return serr
		}
		if !a.validateFreshTOTP(ctx, userID, secret, totpCode) {
			return domain.ErrValidation.WithField("code", "Code is invalid.")
		}
	}
	existing, err := a.repos.Users.GetByEmail(ctx, newEmail)
	if err != nil {
		return err
	}
	if existing != nil {
		return domain.ErrEmailTaken
	}
	oldEmail := user.Email
	if err := a.repos.Users.UpdateEmail(ctx, userID, newEmail); err != nil {
		return err
	}
	// Alert the previous address so account takeovers are visible immediately.
	_ = a.email.Send(ctx, oldEmail, "Your email address was changed",
		mail.WrapHTML(a.cfg.PublicURL, "Your email address was changed",
			fmt.Sprintf(`<p>Hi %s,</p><p>The email address on your account was changed to <strong>%s</strong>.
			If this wasn't you, reset your password immediately and contact support.</p>`,
				htmlEscape(user.Name), htmlEscape(newEmail))))
	updated, _ := a.repos.Users.GetByID(ctx, userID)
	if updated != nil {
		_ = a.sendVerificationEmail(ctx, updated)
	}
	return nil
}

// RegenerateRecoveryCodes issues 10 fresh codes (old ones invalidated).
//
// currentPassword is a step-up credential - see stepUp. Regenerating destroys the
// previous set, so it must not be reachable with a leaked token alone: that would
// be a denial-of-service against the account's ability to recover.
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, userID, code, currentPassword string) ([]string, error) {
	if err := a.stepUp(ctx, userID, currentPassword); err != nil {
		return nil, err
	}
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if st == nil || st.EnabledAt == nil {
		return nil, domain.ErrValidation.WithField("_", "2FA is not enabled.")
	}
	secret, serr := a.openSecret(st.SecretEncrypted)
	if serr != nil {
		return nil, serr
	}
	if !a.validateFreshTOTP(ctx, userID, secret, code) {
		return nil, domain.ErrValidation.WithField("code", "Code is invalid.")
	}
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		codes[i] = fmt.Sprintf("%X-%X-%X-%X", b[0:2], b[2:4], b[4:6], b[6:8])
		h, herr := security.HashPassword(codes[i])
		if herr != nil {
			return nil, herr
		}
		hashes[i] = h
	}
	if err := a.repos.TFA.Enable(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// ---- saved searches (search alerts) ----

type SavedSearch struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	Name          string         `json:"name"`
	Query         map[string]any `json:"query"`
	NotifyDaily   bool           `json:"notify_daily"`
	LastSentAt    *time.Time     `json:"last_sent_at,omitempty"`
	LastResultIDs []string       `json:"last_result_ids,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

func (a *Auth) SaveSearch(ctx context.Context, userID, name string, query map[string]any, notifyDaily bool) (*SavedSearch, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return nil, domain.ErrValidation.WithField("name", "Name must be 1–80 characters.")
	}
	s := &SavedSearch{ID: util.NewUUID(), UserID: userID, Name: name, Query: query, NotifyDaily: notifyDaily}
	_, err := a.repos.Exec(ctx, `
		INSERT INTO saved_searches (id, user_id, name, query, notify_daily) VALUES ($1, $2, $3, $4, $5)`,
		s.ID, s.UserID, s.Name, s.Query, s.NotifyDaily)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// SetSearchAlert toggles the daily email alert on a saved search.
func (a *Auth) SetSearchAlert(ctx context.Context, userID, id string, on bool) error {
	_, err := a.repos.Exec(ctx, `
		UPDATE saved_searches SET notify_daily = $3, updated_at = now() WHERE id = $1 AND user_id = $2`,
		id, userID, on)
	return err
}

func (a *Auth) ListSavedSearches(ctx context.Context, userID string) ([]*SavedSearch, error) {
	rows, err := a.repos.Query(ctx, `
		SELECT id, user_id, name, query, notify_daily, last_sent_at, last_result_ids, created_at
		FROM saved_searches WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SavedSearch
	for rows.Next() {
		var s SavedSearch
		var ids []string
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.Query, &s.NotifyDaily, &s.LastSentAt, &ids, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.LastResultIDs = ids
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (a *Auth) DeleteSavedSearch(ctx context.Context, userID, id string) error {
	_, err := a.repos.Exec(ctx,
		`DELETE FROM saved_searches WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

func (a *Auth) checkUserStatus(u *domain.User) error {
	// An ALLOW list, not a deny list. The previous shape was a switch over the
	// two moderated statuses with `return nil` as the default, which means any
	// status added by a future migration would be ALLOWED until someone
	// remembered to add a case here and in httpapi.degradedBy. For an
	// access-control decision the default has to be the safe one: an
	// unrecognised state is refused, and a migration that introduces one gets a
	// loud failure instead of a silent hole.
	//
	// The cost is that adding a status requires editing this function. That is
	// the intended trade - it is a two-line change with a test, rather than a
	// security control that stops working because nobody updated it.
	switch u.Status {
	case domain.UserStatusActive:
		return nil
	case domain.UserStatusBanned:
		return domain.ErrAccountBanned
	case domain.UserStatusSuspended:
		// NULL suspended_until = indefinite suspension; only a past date
		// means the suspension has lapsed.
		if u.SuspendedUntil != nil && time.Now().After(*u.SuspendedUntil) {
			return nil // suspension expired
		}
		return domain.ErrAccountSuspended
	default:
		return domain.ErrValidation.WithField("status", "Account status is not recognised.")
	}
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}
