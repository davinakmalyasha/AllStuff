package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// Sender is the email abstraction. Dev (no driver): logs the email (incl.
// links) to the API console. EMAIL_DRIVER=smtp: local/dev SMTP relay such as
// Mailpit. Prod: Resend REST API (PRD D6, §10.1). ctx is honored by drivers
// that do network I/O; detached callers may pass context.Background().
//
// Every message carries a plain-text alternative derived from the HTML here
// in the sender layer, so callers never duplicate content. SendBulk marks a
// message as bulk mail (digest/alerts/notifications): mailbox providers get
// List-Unsubscribe headers instead of treating the send as spam.
type Sender interface {
	Send(ctx context.Context, to, subject, html string) error
	SendBulk(ctx context.Context, to, subject, html string) error
}

type ResendConfig struct {
	APIKey    string
	From      string
	AppEnv    string
	PublicURL string

	// SMTPDriver: when Addr is non-empty, mail goes through SMTP instead of
	// the console log (dev) or Resend (prod). Ideal with Mailpit in compose.
	SMTPAddr string // host:port
	SMTPUser string
	SMTPPass string
}

type resendSender struct {
	cfg ResendConfig
}

func (r *resendSender) Send(ctx context.Context, to, subject, html string) error {
	return r.send(ctx, to, subject, html, nil)
}

func (r *resendSender) SendBulk(ctx context.Context, to, subject, html string) error {
	return r.send(ctx, to, subject, html, bulkHeaders(r.cfg.PublicURL))
}

func (r *resendSender) send(ctx context.Context, to, subject, html string, headers map[string]string) error {
	payload := map[string]any{
		"from":    r.cfg.From,
		"to":      []string{to},
		"subject": subject,
		"html":    html,
		// Plain-text alternative generated here so callers never duplicate
		// content (naive tag strip is enough for our simple templates).
		"text": stripTags(html),
	}
	if len(headers) > 0 {
		payload["headers"] = headers
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend: %s", resp.Status)
	}
	return nil
}

type smtpSender struct {
	cfg ResendConfig
}

// Send delivers via plain SMTP (Mailpit and internal relays; no TLS here by
// design — use Resend for internet-facing delivery). net/smtp has no context
// support, so ctx is accepted for interface parity only.
func (s *smtpSender) Send(_ context.Context, to, subject, html string) error {
	return s.send(to, subject, html, nil)
}

func (s *smtpSender) SendBulk(_ context.Context, to, subject, html string) error {
	return s.send(to, subject, html, bulkHeaders(s.cfg.PublicURL))
}

func (s *smtpSender) send(to, subject, html string, headers map[string]string) error {
	addr := s.cfg.SMTPAddr
	from := s.cfg.From
	msg := buildMessage(from, to, subject, html, headers)
	var auth smtp.Auth
	if s.cfg.SMTPUser != "" {
		host := addr
		if i := strings.Index(addr, ":"); i > 0 {
			host = addr[:i]
		}
		auth = smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, host)
	}
	return smtp.SendMail(addr, auth, from, []string{to}, msg)
}

// buildMessage renders a multipart/alternative MIME message: plain text from
// a naive tag strip, then the HTML part. extra carries raw headers (bulk mail
// adds List-Unsubscribe).
func buildMessage(from, to, subject, html string, extra map[string]string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	for k, v := range extra {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	boundary := mimeBoundary()
	b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + `"` + "\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n\r\n")
	b.WriteString(stripTags(html))
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n\r\n")
	b.WriteString(html)
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String())
}

// mimeBoundary mints a random delimiter so message bodies can never collide
// with it.
func mimeBoundary() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "bizverse-alt-fallback"
	}
	return "bv-" + hex.EncodeToString(b[:])
}

// bulkHeaders marks digest/alert/notification sends as bulk mail with a
// one-click unsubscribe endpoint (RFC 8058).
func bulkHeaders(publicURL string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + publicURL + "/me/security>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

type logSender struct{}

func (l *logSender) Send(_ context.Context, to, subject, html string) error {
	// Strip tags for console readability; links stay visible.
	slog.Info("email (dev)",
		"to", to,
		"subject", subject,
		"body", stripTags(html),
	)
	return nil
}

func (l *logSender) SendBulk(ctx context.Context, to, subject, html string) error {
	slog.Info("email bulk (dev)",
		"to", to,
		"subject", subject,
		"body", stripTags(html),
	)
	return nil
}

func NewSender(cfg ResendConfig) Sender {
	if cfg.APIKey != "" && cfg.AppEnv != "dev" {
		return &resendSender{cfg: cfg}
	}
	if cfg.SMTPAddr != "" {
		return &smtpSender{cfg: cfg}
	}
	return &logSender{}
}

// Branded HTML wrapper (Batch 4): consistent template for all transactional mail.
func WrapHTML(publicURL, title, bodyHTML string) string {
	return `<!doctype html><html><body style="margin:0;background:#f5f5f5;padding:24px;font-family:Arial,sans-serif">
	<div style="max-width:560px;margin:auto;background:#ffffff;border-radius:12px;overflow:hidden">
	<div style="background:#0a0a0a;padding:20px 24px">
	<span style="color:#fafafa;font-size:16px;font-weight:700;letter-spacing:2px">BIZVERSE</span>
	</div>
	<div style="padding:24px">
	<h1 style="font-size:18px;margin:0 0 12px">` + title + `</h1>
	` + bodyHTML + `
	</div>
	<div style="padding:16px 24px;border-top:1px solid #eee;color:#999;font-size:12px">
	` + publicURL + ` · Every business, one place
	</div>
	</div></body></html>`
}

func stripTags(s string) string {
	out := make([]byte, 0, len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '<':
			inTag = true
		case s[i] == '>' && inTag:
			inTag = false
		case inTag:
			// skip
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
