package service

import (
	"context"
	"log/slog"
	"time"

	"bizverse/api/internal/repo"
)

// Reliability is the Booking Reliability Pack: reminders that keep customers
// from forgetting, and a review nudge after a completed visit.
//
// Both are timing problems over data the API already owns, so they run on the
// existing in-process scheduler - no queue, no external cron. Idempotency lives
// in appointment_events: each (appointment, kind) is claimed once, before the
// notification is created.
type Reliability struct {
	repos    *repo.Repos
	notifier *Notifier
	logger   *slog.Logger
}

func NewReliability(repos *repo.Repos, notifier *Notifier, logger *slog.Logger) *Reliability {
	return &Reliability{repos: repos, notifier: notifier, logger: logger}
}

// Reminder kinds. The job ticks hourly, so a one-hour-wide window means each
// appointment is seen exactly once; the claim in appointment_events still
// protects against a late or repeated tick.
const (
	reminder24hKind = "reminder_24h"
	reminder2hKind  = "reminder_2h"
	reviewNudgeKind = "review_nudge"
)

// Run sends every currently due reminder and review nudge. Errors are logged
// per stage and the remaining stages still run: one broken reminder class must
// not suppress the others.
func (r *Reliability) Run(ctx context.Context) {
	now := time.Now()
	r.sendReminders(ctx, reminder24hKind, now.Add(24*time.Hour), now.Add(25*time.Hour), "tomorrow")
	r.sendReminders(ctx, reminder2hKind, now.Add(2*time.Hour), now.Add(3*time.Hour), "in 2 hours")
	r.sendReviewNudges(ctx, now.Add(-48*time.Hour), now.Add(-2*time.Hour))
}

func (r *Reliability) sendReminders(ctx context.Context, kind string, from, to time.Time, when string) {
	due, err := r.repos.Bookings.DueReminders(ctx, kind, from, to)
	if err != nil {
		r.logger.Warn("booking reminders scan", "kind", kind, "err", err)
		return
	}
	for _, a := range due {
		claimed, err := r.repos.Bookings.ClaimAppointmentEvent(ctx, a.ID, kind)
		if err != nil {
			r.logger.Warn("booking reminder claim", "kind", kind, "appointment", a.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		// Transactional: the reminder belongs to the booking the customer made,
		// so it is not gated on marketing email preferences.
		r.notifier.CreateTransactional(ctx, a.CustomerID, "appointment_reminder", map[string]any{
			"appointment_id": a.ID,
			"business_id":    a.BusinessID,
			"business_name":  a.BusinessName,
			"business_slug":  a.BusinessSlug,
			"product":        a.ProductName,
			"starts_at":      a.StartsAt,
			"when":           when,
			"kind":           kind,
		})
	}
}

func (r *Reliability) sendReviewNudges(ctx context.Context, from, to time.Time) {
	due, err := r.repos.Bookings.DueReviewNudges(ctx, from, to)
	if err != nil {
		r.logger.Warn("review nudge scan", "err", err)
		return
	}
	for _, a := range due {
		claimed, err := r.repos.Bookings.ClaimAppointmentEvent(ctx, a.ID, reviewNudgeKind)
		if err != nil {
			r.logger.Warn("review nudge claim", "appointment", a.ID, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		// Opt-in channels: a review request is not part of the transaction, so
		// it follows the user's notification preferences like any other nudge.
		r.notifier.Create(ctx, a.CustomerID, "review_nudge", map[string]any{
			"appointment_id": a.ID,
			"business_id":    a.BusinessID,
			"business_name":  a.BusinessName,
			"business_slug":  a.BusinessSlug,
			"product":        a.ProductName,
		})
	}
}
