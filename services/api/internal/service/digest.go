package service

import (
	"context"
	"fmt"
	"strings"

	"bizverse/api/internal/email"
	"bizverse/api/internal/repo"
)

// Digest — weekly "what's hot" email for opted-in users (PRD §5.7).
type Digest struct {
	repos   *repo.Repos
	email   email.Sender
	baseURL string
}

func NewDigest(repos *repo.Repos, sender email.Sender, publicURL string) *Digest {
	if publicURL == "" {
		publicURL = "http://localhost:5173"
	}
	return &Digest{repos: repos, email: sender, baseURL: publicURL}
}

// SendWeekly emails the top trending businesses to digest-opted-in users.
func (d *Digest) SendWeekly(ctx context.Context) error {
	users, err := d.recipients(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	entries, err := NewTrending(d.repos, nil).Leaderboard(ctx, "7d", "global", 5)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	var rows strings.Builder
	for i, e := range entries {
		rows.WriteString(fmt.Sprintf(
			`<tr><td style="padding:10px;border-bottom:1px solid #eee;font-family:Arial,sans-serif">
			<b>%d. %s</b><br/><span style="color:#888">%s · %s</span></td>
			<td style="padding:10px;border-bottom:1px solid #eee"><a href="%s/b/%s" style="color:#111">View</a></td></tr>`,
			i+1, xmlEsc(e.Name), esc(e.Category), xmlEsc(e.City), d.publicURL(), e.Slug))
	}
	bodyHTML := fmt.Sprintf(`<p style="color:#666">The businesses everyone is talking about.</p>
		<table style="width:100%%;border-collapse:collapse">%s</table>
		<p style="color:#999;font-size:12px;margin-top:16px">Unsubscribe anytime: <a href="%s/me/security" style="color:#999">manage preferences</a>.</p>`,
		rows.String(), d.publicURL())
	// Branded template for parity with every other sender; bulk headers keep
	// mailbox providers from classifying the weekly blast as spam.
	for _, u := range users {
		_ = d.email.SendBulk(ctx, u, "This week in BizVerse", email.WrapHTML(d.publicURL(), "This week in BizVerse", bodyHTML))
	}
	return nil
}

func (d *Digest) recipients(ctx context.Context) ([]string, error) {
	rows, err := d.repos.Query(ctx, `
		SELECT email FROM users WHERE digest_opt_in = true AND deleted_at IS NULL AND email_verified_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		out = append(out, email)
	}
	return out, rows.Err()
}

func (d *Digest) publicURL() string { return d.baseURL }

func esc(s *string) string {
	if s == nil {
		return ""
	}
	return xmlEsc(*s)
}

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
