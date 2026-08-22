package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// TOTP (RFC 6238) — HMAC-SHA1, 30s window, 6 digits, no external deps (PRD §5.9.1).

func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func totpCode(secret string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / 30)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", code), nil
}

// ValidateTOTP checks a code against the secret with ±1 window tolerance.
func ValidateTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	now := time.Now()
	for _, drift := range []int{0, -1, 1} {
		got, err := totpCode(secret, now.Add(time.Duration(drift)*30*time.Second))
		if err == nil && hmac.Equal([]byte(got), []byte(code)) {
			return true
		}
	}
	return false
}

// OtpauthURL builds the standard enrollment URI.
func OtpauthURL(issuer, account, secret string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		urlPathEscape(issuer), urlPathEscape(account), secret, urlPathEscape(issuer))
}

func urlPathEscape(s string) string {
	return strings.NewReplacer(":", "%3A", " ", "%20", "@", "%40").Replace(s)
}
