//go:build !race

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"bizverse/api/internal/domain"
	"bizverse/api/internal/testutil"
)

// The iCalendar file is small and the rules are unforgiving: clients reject a
// malformed file with no useful message, and the two ways to get it wrong are
// folding (octets, not bytes-of-a-rune) and escaping order.

func icsFixture(t *testing.T) (*Booking, *testutil.H, *domain.Appointment, *domain.User) {
	t.Helper()
	svc, h := newTestBooking(t)
	b, p, owner := bookableFixture(t, h)
	loc, err := svc.CreateLocation(context.Background(), owner.ID, b.ID, domain.Location{
		Name: "Main Branch", Address: "Jl. Satu 1", City: "Jakarta", Country: "ID",
		Lat: -6.2, Lng: 106.8, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	customer := testutil.User(t, h)
	day := nextDay()
	a, err := svc.BookAt(context.Background(), customer.ID, b.ID, p.ID, "", loc.ID, day.Add(9*time.Hour), "")
	if err != nil {
		t.Fatalf("BookAt: %v", err)
	}
	return svc, h, a, customer
}

func TestCalendarInviteStructure(t *testing.T) {
	svc, h, a, customer := icsFixture(t)
	ics, err := svc.CalendarInvite(context.Background(), customer.ID, a.ID)
	if err != nil {
		t.Fatalf("CalendarInvite: %v", err)
	}

	// CRLF only: a bare LF is not a line break in iCalendar.
	if strings.Contains(strings.ReplaceAll(ics, "\r\n", ""), "\n") {
		t.Error("the file contains a bare LF")
	}
	for _, want := range []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:", "CALSCALE:GREGORIAN",
		"BEGIN:VEVENT", "END:VEVENT", "END:VCALENDAR",
		"UID:" + a.ID + "@bizverse",
		"STATUS:CONFIRMED",
		"LOCATION:Main Branch",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Times are UTC with a trailing Z, matching the stored instant.
	if !strings.Contains(ics, "DTSTART:"+a.StartsAt.UTC().Format("20060102T150405Z")) {
		t.Errorf("DTSTART is wrong: %s", ics)
	}
	if !strings.Contains(ics, "DTEND:"+a.EndsAt.UTC().Format("20060102T150405Z")) {
		t.Errorf("DTEND is wrong: %s", ics)
	}

	// Every line is at most 75 octets (the continuation space included) and is
	// valid UTF-8 on its own.
	for _, line := range strings.Split(strings.TrimSuffix(ics, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line is %d octets: %q", len(line), line)
		}
		if !utf8.ValidString(line) {
			t.Errorf("line is not valid UTF-8: %q", line)
		}
	}
	_ = h
}

// A long multi-byte SUMMARY is the case that breaks naive folding: cutting at 75
// BYTES can land inside a rune and produce invalid UTF-8.
func TestCalendarInviteFoldsOnRuneBoundaries(t *testing.T) {
	svc, h, a, customer := icsFixture(t)
	// 80 two-byte characters = 160 octets of summary.
	longName := strings.Repeat("é", 80)
	h.Exec(t, `UPDATE businesses SET name=$2 WHERE id=$1`, a.BusinessID, longName)

	ics, err := svc.CalendarInvite(context.Background(), customer.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(ics) {
		t.Fatal("the file is not valid UTF-8")
	}
	var unfolded []string
	for _, line := range strings.Split(strings.TrimSuffix(ics, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Fatalf("line is %d octets: %q", len(line), line)
		}
		if strings.HasPrefix(line, " ") && len(unfolded) > 0 {
			unfolded[len(unfolded)-1] += line[1:]
			continue
		}
		unfolded = append(unfolded, line)
	}
	joined := strings.Join(unfolded, "\n")
	if !strings.Contains(joined, "SUMMARY:"+longName) {
		// The em dash separator is part of the summary, so match on the name.
		if !strings.Contains(joined, longName) {
			t.Errorf("unfolding did not restore the summary; got %q", joined)
		}
	}
}

func TestCalendarInviteEscaping(t *testing.T) {
	svc, h, a, customer := icsFixture(t)
	// A note with every character the RFC requires escaping.
	note := `line one; with comma, and backslash \ and` + "\n" + `newline`
	h.Exec(t, `UPDATE appointments SET notes=$2 WHERE id=$1`, a.ID, note)

	ics, err := svc.CalendarInvite(context.Background(), customer.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ics, `DESCRIPTION:line one\; with comma\, and backslash \\ and\nnewline`) {
		t.Errorf("the description was not escaped correctly:\n%s", ics)
	}
	// A raw newline inside a value would split the content line.
	for _, line := range strings.Split(strings.TrimSuffix(ics, "\r\n"), "\r\n") {
		if strings.HasPrefix(line, "newline") {
			t.Error("the note's newline ended the content line")
		}
	}
}

func TestCalendarInviteAuthorization(t *testing.T) {
	svc, h, a, customer := icsFixture(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	_ = owner
	stranger := testutil.User(t, h)

	// The customer may fetch their own booking.
	if _, err := svc.CalendarInvite(ctx, customer.ID, a.ID); err != nil {
		t.Errorf("customer: %v", err)
	}
	// A stranger may not.
	if _, err := svc.CalendarInvite(ctx, stranger.ID, a.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("stranger: err = %v, want ErrForbidden", err)
	}
	// Unknown appointment.
	if _, err := svc.CalendarInvite(ctx, customer.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown: err = %v, want ErrNotFound", err)
	}

	// A cancelled booking still renders, with STATUS:CANCELLED.
	if err := svc.Cancel(ctx, customer.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	ics, err := svc.CalendarInvite(ctx, customer.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ics, "STATUS:CANCELLED") {
		t.Error("a cancelled booking is not marked CANCELLED")
	}
}
