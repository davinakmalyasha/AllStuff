package service

import (
	"context"
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
)

// Auth — accounts, sessions, tokens (PRD §5.9, §8.1).
type Auth struct {
	repos  *repo.Repos
	cfg    config.Config
	email  mail.Sender
	logger *slog.Logger
}

func NewAuth(repos *repo.Repos, cfg config.Config, sender mail.Sender, logger *slog.Logger) *Auth {
	return &Auth{repos: repos, cfg: cfg, email: sender, logger: logger}
}

var (
	emailRe   = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,30}$`)
)

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
		ID:         newUUID(),
		Email:      in.Email,
		PasswordHash: hash,
		Name:       in.Name,
		Username:   in.Username,
		Timezone:   "UTC",
		Role:       domain.RoleUser,
		Status:     domain.UserStatusActive,
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
	user, err := a.repos.Users.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil {
		return nil, nil, err
	}
	// Constant-time-ish: always run a verify against a dummy hash to resist timing.
	if user == nil {
		_, _ = security.VerifyPassword(in.Password, "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2FsdA$c2FsdHNhbHRzYWx0c2FsdHNhbHRzYWx0c2FsdA")
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
	st, err := a.repos.TFA.Get(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	if st != nil && st.EnabledAt != nil {
		challenge, _, err := security.IssueToken(a.cfg.JWTSecret, security.Token2FAChallenge, user, 5*time.Minute)
		if err != nil {
			return nil, nil, err
		}
		return user, &TokenPair{RefreshToken: challenge}, domain.Err2FARequired
	}

	tokens, err := a.createSession(ctx, user, &clientMeta{IP: ip, UA: ua})
	if err != nil {
		return nil, nil, err
	}
	a.logger.Info("login", "user", user.ID)
	return user, tokens, nil
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
	if sess == nil || sess.RevokedAt != nil {
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
	user, err := a.repos.Users.GetByID(ctx, claims.UserID)
	if err != nil {
		return err
	}
	if user == nil {
		return domain.ErrTokenInvalid
	}
	return a.repos.Users.MarkEmailVerified(ctx, user.ID)
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
	return a.email.Send(user.Email, "Reset your password",
		mail.WrapHTML(a.cfg.PublicURL, "Reset your password",
			fmt.Sprintf(`<p>Hi %s,</p><p>Reset your password: <a href="%s">%s</a>.</p><p>Link expires in 15 minutes.</p>`,
				htmlEscape(user.Name), link, link)))
}

func (a *Auth) ResetPassword(ctx context.Context, token, password string) error {
	if len(password) < 8 || !strings.ContainsAny(password, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") || !strings.ContainsAny(password, "0123456789") {
		return domain.ErrValidation.WithField("password", "Password must be at least 8 characters and include a letter and a number.")
	}
	claims, err := security.ParseToken(a.cfg.JWTSecret, token, security.TokenResetPassword)
	if err != nil {
		return err
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	if err := a.repos.Users.SetPassword(ctx, claims.UserID, hash); err != nil {
		return err
	}
	// Revoke all sessions on password change (PRD §5.9.1).
	return a.repos.Sessions.RevokeAllExcept(ctx, claims.UserID, "")
}

// IssueAccessToken builds a fresh access token for an authenticated user.
func (a *Auth) IssueAccessToken(user *domain.User) (string, error) {
	tok, _, err := security.IssueToken(a.cfg.JWTSecret, security.TokenAccess, user, 15*time.Minute)
	return tok, err
}

// IssueSessionForUser creates a session for an already-authenticated user
// (used by OAuth flows and 2FA completion).
func (a *Auth) IssueSessionForUser(ctx context.Context, user *domain.User, ua string) (*TokenPair, error) {
	if err := a.checkUserStatus(user); err != nil {
		return nil, err
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
		ID:        newUUID(),
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
	return a.email.Send(user.Email, "Verify your email",
		mail.WrapHTML(a.cfg.PublicURL, "Verify your email",
			fmt.Sprintf(`<p>Welcome to BizVerse, %s.</p><p>Verify your email: <a href="%s">%s</a>.</p><p>Link expires in 24 hours.</p>`,
				htmlEscape(user.Name), link, link)))
}

// ---- 2FA (PRD §5.9.1) ----

// Enroll2FA generates a TOTP secret for the user (not yet enabled).
func (a *Auth) Enroll2FA(ctx context.Context, userID string) (secret, otpauthURL string, err error) {
	secret, err = security.GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	// Encrypt at rest (PRD §9.3) — XOR with a key derived from the JWT secret.
	enc := xorCipher(secret, a.cfg.JWTSecret)
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
	secret := xorCipher(st.SecretEncrypted, a.cfg.JWTSecret)
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
		sum := sha256.Sum256([]byte("rc:" + userID + ":" + codes[i]))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	if err := a.repos.TFA.Enable(ctx, userID, hashes); err != nil {
		return nil, err
	}
	_, _ = a.repos.Exec(ctx, `INSERT INTO auth_events (id, user_id, event) VALUES ($1, $2, '2fa_enable')`,
		newUUID(), userID)
	return codes, nil
}

func (a *Auth) Disable2FA(ctx context.Context, userID, code string) error {
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return err
	}
	if st == nil {
		return domain.ErrValidation.WithField("_", "2FA is not enabled.")
	}
	secret := xorCipher(st.SecretEncrypted, a.cfg.JWTSecret)
	if !security.ValidateTOTP(secret, code) {
		return domain.ErrValidation.WithField("code", "Code is invalid.")
	}
	if err := a.repos.TFA.Disable(ctx, userID); err != nil {
		return err
	}
	_, _ = a.repos.Exec(ctx, `INSERT INTO auth_events (id, user_id, event) VALUES ($1, $2, '2fa_disable')`,
		newUUID(), userID)
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
	secret := xorCipher(st.SecretEncrypted, a.cfg.JWTSecret)
	if security.ValidateTOTP(secret, code) {
		return a.finishLogin(ctx, user, ip, ua)
	}
	// Recovery code path: hash and compare against stored.
	sum := sha256.Sum256([]byte("rc:" + user.ID + ":" + code))
	hash := hex.EncodeToString(sum[:])
	if st.RecoveryCodesHash != nil && containsJSON(st.RecoveryCodesHash, hash) {
		if err := a.repos.TFA.UseRecoveryCode(ctx, user.ID, hash); err != nil {
			return nil, nil, err
		}
		return a.finishLogin(ctx, user, ip, ua)
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

func containsJSON(arr []byte, target string) bool {
	var items []string
	if err := json.Unmarshal(arr, &items); err != nil {
		return false
	}
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
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

func (a *Auth) CancelDeletion(ctx context.Context, userID string) error {
	_, err := a.repos.Exec(ctx, `
		UPDATE users SET deleted_at = NULL, updated_at = now() WHERE id = $1`, userID)
	return err
}

// PurgeExpiredDeletions hard-deletes users past the 14-day grace (job).
func (a *Auth) PurgeExpiredDeletions(ctx context.Context) (int64, error) {
	tag, err := a.repos.Exec(ctx, `
		DELETE FROM users WHERE deleted_at IS NOT NULL AND deleted_at < now() - interval '14 days'`)
	return tag.RowsAffected(), err
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
	reviews, _ := a.repos.Query(ctx, `
		SELECT id, business_id, product_id, rating, text, created_at FROM reviews
		WHERE user_id = $1 AND deleted_at IS NULL`, userID)
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
	comments, _ := a.repos.Query(ctx, `
		SELECT id, business_id, text, created_at FROM comments WHERE user_id = $1`, userID)
	comRows := []any{}
	for comments.Next() {
		var id, bid, text string
		var createdAt time.Time
		if err := comments.Scan(&id, &bid, &text, &createdAt); err == nil {
			comRows = append(comRows, map[string]any{"id": id, "business_id": bid, "text": text, "created_at": createdAt})
		}
	}
	comments.Close()

	return map[string]any{
		"exported_at": time.Now(),
		"profile": map[string]any{
			"id": user.ID, "name": user.Name, "username": user.Username,
			"email": user.Email, "bio": user.Bio, "timezone": user.Timezone,
			"created_at": user.CreatedAt,
		},
		"businesses": businesses,
		"products":   products,
		"collections": colItems,
		"reviews":    revRows,
		"comments":   comRows,
	}, nil
}

func (a *Auth) LoginHistory(ctx context.Context, userID string, limit int) ([]map[string]any, error) {	rows, err := a.repos.Query(ctx, `
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
	if err := a.repos.Users.SetPassword(ctx, userID, hash); err != nil {
		return err
	}
	// Revoke all other sessions; keep the caller's own session alive.
	return a.repos.Sessions.RevokeAllExcept(ctx, userID, keepSessionID)
}

func (a *Auth) ChangeEmail(ctx context.Context, userID, password, newEmail string) error {
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
	existing, err := a.repos.Users.GetByEmail(ctx, newEmail)
	if err != nil {
		return err
	}
	if existing != nil {
		return domain.ErrEmailTaken
	}
	if err := a.repos.Users.UpdateEmail(ctx, userID, newEmail); err != nil {
		return err
	}
	updated, _ := a.repos.Users.GetByID(ctx, userID)
	if updated != nil {
		_ = a.sendVerificationEmail(ctx, updated)
	}
	return nil
}

// RegenerateRecoveryCodes issues 10 fresh codes (old ones invalidated).
func (a *Auth) RegenerateRecoveryCodes(ctx context.Context, userID, code string) ([]string, error) {
	st, err := a.repos.TFA.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if st == nil || st.EnabledAt == nil {
		return nil, domain.ErrValidation.WithField("_", "2FA is not enabled.")
	}
	secret := xorCipher(st.SecretEncrypted, a.cfg.JWTSecret)
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
		sum := sha256.Sum256([]byte("rc:" + userID + ":" + codes[i]))
		hashes[i] = hex.EncodeToString(sum[:])
	}
	if err := a.repos.TFA.Enable(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// ---- saved searches (search alerts) ----

type SavedSearch struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	Name         string         `json:"name"`
	Query        map[string]any `json:"query"`
	NotifyDaily  bool           `json:"notify_daily"`
	LastSentAt   *time.Time     `json:"last_sent_at,omitempty"`
	LastResultIDs []string      `json:"last_result_ids,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

func (a *Auth) SaveSearch(ctx context.Context, userID, name string, query map[string]any, notifyDaily bool) (*SavedSearch, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return nil, domain.ErrValidation.WithField("name", "Name must be 1–80 characters.")
	}
	s := &SavedSearch{ID: newUUID(), UserID: userID, Name: name, Query: query, NotifyDaily: notifyDaily}
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

var _ = net.IP{}

func (a *Auth) checkUserStatus(u *domain.User) error {
	switch u.Status {
	case domain.UserStatusBanned:
		return domain.ErrAccountBanned
	case domain.UserStatusSuspended:
		if u.SuspendedUntil == nil || time.Now().After(*u.SuspendedUntil) {
			return nil // suspension expired
		}
		return domain.ErrAccountSuspended
	}
	return nil
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("rand: " + err.Error())
	}
	// RFC 4122 v4
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;").Replace(s)
}
