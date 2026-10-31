package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"allstuff/api/internal/domain"
)

// CalendarInvite renders one appointment as an iCalendar (RFC 5545) file.
//
// Hand-written rather than pulled from a library because the file has four
// rules that are easy to get wrong and cheap to get right, and one of them -
// folding on OCTET boundaries - silently produces invalid UTF-8 if done by
// byte-count alone:
//
//   - lines end CRLF, never bare LF;
//   - text values escape backslash, semicolon, comma and newline, in that
//     order (escaping the backslash last would double-escape the others);
//   - a line longer than 75 octets is folded with CRLF + a single space, and a
//     fold never splits a UTF-8 sequence;
//   - times are UTC with a trailing Z.
func (s *Booking) CalendarInvite(ctx context.Context, userID, appointmentID string) (string, error) {
	a, err := s.repos.Bookings.GetAppointment(ctx, appointmentID)
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", domain.ErrNotFound
	}
	if a.CustomerID != userID {
		can, err := s.repos.Businesses.CanManageBusiness(ctx, userID, a.BusinessID)
		if err != nil {
			return "", err
		}
		if !can {
			return "", domain.ErrForbidden
		}
	}

	summary := a.BusinessName
	if a.ProductName != "" {
		summary += " — " + a.ProductName
	}
	location := a.LocationName
	desc := "Booking reference: " + a.ID
	if a.Notes != "" {
		desc = a.Notes + "\n\n" + desc
	}

	var b strings.Builder
	line := func(v string) { writeFolded(&b, v) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//AllStuff//Bookings//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("BEGIN:VEVENT")
	line("UID:" + a.ID + "@allstuff")
	line("DTSTAMP:" + icsTime(time.Now()))
	line("DTSTART:" + icsTime(a.StartsAt))
	line("DTEND:" + icsTime(a.EndsAt))
	line("SUMMARY:" + icsText(summary))
	if location != "" {
		line("LOCATION:" + icsText(location))
	}
	line("DESCRIPTION:" + icsText(desc))
	if a.Status == domain.AppointmentCancelled {
		line("STATUS:CANCELLED")
	} else {
		line("STATUS:CONFIRMED")
	}
	line("END:VEVENT")
	line("END:VCALENDAR")

	return b.String(), nil
}

func icsTime(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// icsText escapes a TEXT value. Backslash FIRST: escaping it after the others
// would turn the backslash introduced by "\," into "\\," and change the meaning.
func icsText(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, ";", `\;`)
	v = strings.ReplaceAll(v, ",", `\,`)
	v = strings.ReplaceAll(v, "\r\n", `\n`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	v = strings.ReplaceAll(v, "\r", `\n`)
	return v
}

// writeFolded emits one content line, folded at 75 OCTETS with CRLF + a single
// space. Folding counts octets because that is what the RFC limits, but it never
// cuts a rune in half: a fold inside a multi-byte character would produce a file
// that is not valid UTF-8, which clients reject without a useful message.
func writeFolded(b *strings.Builder, line string) {
	const limit = 75
	if len(line) <= limit {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}
	first := true
	for len(line) > 0 {
		room := limit
		if !first {
			// Continuation lines start with a space, which counts toward the limit.
			room--
		}
		cut := room
		if cut > len(line) {
			cut = len(line)
		}
		// Step back to a rune boundary. utf8.RuneStart is false for continuation
		// bytes, so this stops on the first byte of the last rune it went past.
		for cut > 0 && cut < len(line) && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if !first {
			b.WriteString(" ")
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n")
		line = line[cut:]
		first = false
	}
}
