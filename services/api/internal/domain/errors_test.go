package domain

import (
	"errors"
	"fmt"
	"testing"
)

// errors.Is on a cloned domain error.
//
// WithField returns a clone rather than mutating the shared sentinel, which is
// correct - mutating it caused concurrent map writes across requests. But a clone
// is a different pointer, so errors.Is compared pointer identity and reported
// false for the very sentinel the clone was derived from.
//
// The failure mode is quiet and systemic: a handler branching on
// `errors.Is(err, domain.ErrValidation)` falls through to its default case and
// answers 500 for what is a 400. It was found by an integration test asserting
// that Disable2FA rejects a pending enrollment with ErrValidation - the service
// returned exactly that error and the assertion failed.
func TestErrorsIsMatchesClonedSentinels(t *testing.T) {
	t.Parallel()

	clone := ErrValidation.WithField("_", "2FA is already enabled.")

	if !errors.Is(clone, ErrValidation) {
		t.Errorf("errors.Is(clone, ErrValidation) = false, want true: a clone is the same condition as its sentinel")
	}
	if !errors.Is(ErrValidation, clone) {
		t.Errorf("errors.Is(ErrValidation, clone) = false, want true: the relation must be symmetric")
	}
}

// WithField must not mutate the sentinel, or every concurrent request sharing the
// package global would append its own fields to it.
func TestWithFieldDoesNotMutateTheSentinel(t *testing.T) {
	t.Parallel()

	before := len(ErrValidation.Fields)
	_ = ErrValidation.WithField("a", "1")
	_ = ErrValidation.WithField("b", "2")

	if got := len(ErrValidation.Fields); got != before {
		t.Errorf("sentinel Fields grew from %d to %d; WithField must clone", before, got)
	}

	// The clones must also not alias each other.
	first := ErrValidation.WithField("x", "1")
	second := ErrValidation.WithField("y", "2")
	if _, leaked := first.Fields["y"]; leaked {
		t.Error("clones share their Fields map: a field added to one appeared in another")
	}
	if _, leaked := second.Fields["x"]; leaked {
		t.Error("clones share their Fields map: a field added to one appeared in another")
	}
	// Each clone keeps its own field, which is the point of copying the map.
	if first.Fields["x"] != "1" || second.Fields["y"] != "2" {
		t.Error("clones did not retain their own fields")
	}
}

// Different codes must stay distinguishable, or Is has simply become
// "everything is every error".
func TestErrorsIsDistinguishesCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		sent error
		want bool
	}{
		{ErrValidation, ErrValidation, true},
		{ErrValidation.WithField("f", "m"), ErrValidation, true},
		{ErrValidation, ErrNotFound, false},
		{ErrNotFound, ErrValidation, false},
		{ErrInvalidCreds.WithField("f", "m"), ErrValidation, false},
	}
	for _, tc := range cases {
		if got := errors.Is(tc.err, tc.sent); got != tc.want {
			t.Errorf("errors.Is(%v, %v) = %v, want %v", tc.err, tc.sent, got, tc.want)
		}
	}
}

// errors.Is must still see through fmt.Errorf("%w"), which is how services wrap
// errors with context.
func TestErrorsIsSeesThroughWrapping(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("disable 2fa: %w", ErrValidation.WithField("_", "nope"))
	if !errors.Is(wrapped, ErrValidation) {
		t.Error("errors.Is must match a wrapped cloned sentinel")
	}
}

// A non-domain target must never match, including a nil target.
func TestErrorsIsIgnoresForeignTargets(t *testing.T) {
	t.Parallel()

	if errors.Is(ErrValidation, errors.New("validation_error: Validation failed.")) {
		t.Error("a plain error with the same text must not match a domain sentinel")
	}
	if errors.Is(ErrValidation, nil) {
		t.Error("errors.Is(err, nil) must be false")
	}
}
