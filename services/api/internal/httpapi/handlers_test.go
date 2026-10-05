package httpapi

import (
	"testing"
	"time"

	"bizverse/api/internal/domain"
)

// TestDegradedByRefusesBothModerationStates is the regression test for the
// request-path half of moderation.
//
// withAuth used to refuse only `status = 'banned'`. `suspended` was absent, even
// though domain.UserStatusSuspended exists and checkUserStatus handles it
// correctly - because checkUserStatus is only consulted by Login and Refresh,
// neither of which runs per request. A suspended user's unexpired 15-minute
// access token therefore carried full read/write access to every route:
// posting, DMing, reviewing, and for a suspended admin, /admin/*. Moderation only
// took effect once the token aged out on its own.
func TestDegradedByRefusesBothModerationStates(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name string
		user domain.User
		want bool
	}{
		{
			name: "an active user acts",
			user: domain.User{Status: domain.UserStatusActive},
			want: false,
		},
		{
			name: "a banned user does not act",
			user: domain.User{Status: domain.UserStatusBanned},
			want: true,
		},
		{
			// The case that was missing. HideContent sets status=suspended with a
			// NULL suspended_until for an indefinite suspension, so a nil expiry
			// must NOT read as "already expired".
			name: "a suspended user does not act, with no expiry recorded",
			user: domain.User{Status: domain.UserStatusSuspended},
			want: true,
		},
		{
			name: "a suspension with a future expiry does not act",
			user: domain.User{Status: domain.UserStatusSuspended, SuspendedUntil: &future},
			want: true,
		},
		{
			name: "a suspension whose expiry has passed acts again",
			user: domain.User{Status: domain.UserStatusSuspended, SuspendedUntil: &past},
			want: false,
		},
		{
			// Admin.UserAction uses a fixed 7 days. The boundary is exclusive at the
			// instant of expiry and inclusive one nanosecond before it.
			name: "a suspension expiring exactly now does not act",
			user: domain.User{Status: domain.UserStatusSuspended, SuspendedUntil: &now},
			want: false,
		},
		{
			name: "a ban outranks a future suspension expiry",
			user: domain.User{Status: domain.UserStatusBanned, SuspendedUntil: &past},
			want: true,
		},
		{
			// An unknown status must fail CLOSED. If a future migration adds a
			// moderation state and someone forgets this function, the safe default
			// is to refuse access, not to grant it.
			name: "an unrecognised status is refused rather than allowed",
			user: domain.User{Status: domain.UserStatus("something_new")},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			if got := s.degradedBy(&tt.user, now); got != tt.want {
				t.Errorf("degradedBy(%q, until=%v) = %v, want %v",
					tt.user.Status, tt.user.SuspendedUntil, got, tt.want)
			}
		})
	}
}

// TestDegradedByAgreesWithCheckUserStatus is the reason degradedBy exists at all
// rather than being deleted in favour of one shared helper.
//
// The two checks live in different packages and cannot call each other, so they
// can drift. This test pins the httpapi half to the same table the service half
// implements, so a change to one that is not made to the other fails here rather
// than in production - where the symptom would be "moderation is not effective
// until the token expires".
func TestDegradedByAgreesWithCheckUserStatus(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	s := &Server{}

	// The service side is the reference implementation. Recreated here rather
	// than imported so this test states the CONTRACT explicitly: if
	// checkUserStatus changes shape, whoever changes it reads this and updates
	// both sides on purpose.
	reference := func(u *domain.User) error {
		switch u.Status {
		case domain.UserStatusActive:
			return nil
		case domain.UserStatusBanned:
			return domain.ErrAccountBanned
		case domain.UserStatusSuspended:
			// Lapsed suspension returns from INSIDE the case. Falling out of it
			// would reach the default and refuse a user whose suspension has
			// expired - the wrong direction, and the bug this transcription is
			// written to avoid.
			if u.SuspendedUntil == nil || now.Before(*u.SuspendedUntil) {
				return domain.ErrAccountSuspended
			}
			return nil
		default:
			return domain.ErrValidation.WithField("status", "Account status is not recognised.")
		}
	}

	cases := []*domain.User{
		{Status: domain.UserStatusActive},
		{Status: domain.UserStatusBanned},
		{Status: domain.UserStatusSuspended},
		{Status: domain.UserStatusSuspended, SuspendedUntil: ptr(now.Add(time.Hour))},
		{Status: domain.UserStatusSuspended, SuspendedUntil: ptr(now.Add(-time.Hour))},
		// An unrecognised status: both sides must refuse.
		{Status: domain.UserStatus("something_new")},
		// The empty string is what a zero-value User carries, so it is the most
		// likely shape for a row built by a query that forgot to select status.
		{Status: domain.UserStatus("")},
	}
	for _, u := range cases {
		want := reference(u) != nil
		if got := s.degradedBy(u, now); got != want {
			t.Errorf("status %q until %v: httpapi says degraded=%v, the service reference says %v",
				u.Status, u.SuspendedUntil, got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
