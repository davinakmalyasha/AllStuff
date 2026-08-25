package email

import (
	"bytes"
	"context"
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
type Sender interface {
	Send(ctx context.Context, to, subject, html string) error
}

type ResendConfig struct {
	APIKey   string
	From     string
	AppEnv   string
	PublicURL string

	// SMTPDriver: when Addr is non-empty, mail goes through SMTP instead of
	// the console log (dev) or Resend (prod). Ideal with Mailpit in compose.
	SMTPAddr   string // host:port
	SMTPUser   string
	SMTPPass   string
}

type resendSender struct {
	cfg ResendConfig
}

func (r *resendSender) Send(ctx context.Context, to, subject, html string) error {
	body, _ := json.Marshal(map[string]any{
		"from":    r.cfg.From,
		"to":      []string{to},
		"subject": subject,
		"html":    html,
	})
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
	addr := s.cfg.SMTPAddr
	from := s.cfg.From
	if i := strings.Index(from, "@"); i > 0 {
		// Mailpit accepts any envelope; keep the local part for realism.
		_ = from[:i]
	}
	msg := buildMessage(from, to, subject, html)
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

func buildMessage(from, to, subject, html string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n\r\n")
	b.WriteString(html)
	return []byte(b.String())
}

type logSender struct {
	cfg ResendConfig
}

func (l *logSender) Send(_ context.Context, to, subject, html string) error {
	// Strip tags for console readability; links stay visible.
	slog.Info("email (dev)",
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
	return &logSender{cfg: cfg}
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
