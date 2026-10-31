//go:build !race

package service

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"allstuff/api/internal/config"
	"allstuff/api/internal/email"
	"allstuff/api/internal/ws"
	"testing"

	"allstuff/api/internal/domain"
	"allstuff/api/internal/testutil"
)

// community.Ask and community.Answer had no coverage. Both sit on a business's
// public page, and the answer path carries a persisted is_owner flag that
// decides how a reply is labelled and how it sorts.

func newTestCommunity(t *testing.T) (*Community, *testutil.H) {
	t.Helper()
	h := testutil.New(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{AppEnv: "test", PublicURL: "https://allstuff.test"}
	notifier := NewNotifier(h.Repos, cfg, email.NewSender(email.ResendConfig{AppEnv: "test"}), ws.NewHub(logger, nil), logger)
	return NewCommunity(h.Repos, notifier), h
}

func TestCommunityAsk(t *testing.T) {
	svc, h := newTestCommunity(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	asker := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Q&A Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})

	if _, err := svc.Ask(ctx, asker.ID, "00000000-0000-0000-0000-000000000000", "hello?"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown business: err = %v, want ErrNotFound", err)
	}
	// A listing that is not accepting activity refuses new questions.
	draft := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Draft Q&A", Category: cat, Owner: owner, Status: domain.BusinessDraft,
	})
	if _, err := svc.Ask(ctx, asker.ID, draft.ID, "hello?"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("a draft business: err = %v, want ErrValidation", err)
	}
	if _, err := svc.Ask(ctx, asker.ID, biz.ID, "no"); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("two characters: err = %v, want ErrValidation", err)
	}
	long := make([]byte, 501)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := svc.Ask(ctx, asker.ID, biz.ID, string(long)); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("501 characters: err = %v, want ErrValidation", err)
	}

	q, err := svc.Ask(ctx, asker.ID, biz.ID, "  Do you take bookings?  ")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if q.Text != "Do you take bookings?" {
		t.Errorf("text = %q, want it trimmed", q.Text)
	}
	if q.BusinessID != biz.ID || q.UserID != asker.ID {
		t.Errorf("question = %#v", q)
	}
}

func TestCommunityAnswer(t *testing.T) {
	svc, h := newTestCommunity(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	asker := testutil.User(t, h)
	coOwner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Answer Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
		Status: domain.BusinessVerified,
	})
	// An accepted co-owner manages the listing, so their answers are the
	// business's own answers - not customer answers filed under the owner.
	inviteAccepted(t, h, biz.ID, owner.ID, coOwner.ID, "co_owner")

	q, err := svc.Ask(ctx, asker.ID, biz.ID, "Is parking available?")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Answer(ctx, owner.ID, "00000000-0000-0000-0000-000000000000", "yes"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown question: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Answer(ctx, owner.ID, q.ID, "   "); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("empty answer: err = %v, want ErrValidation", err)
	}

	// The owner answers as the owner.
	a, err := svc.Answer(ctx, owner.ID, q.ID, "Yes, behind the building.")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !a.IsOwner {
		t.Error("the owner's answer is not marked as the owner's")
	}
	if a.Text != "Yes, behind the building." {
		t.Errorf("text = %q, want it trimmed", a.Text)
	}

	// So does the co-owner.
	co, err := svc.Answer(ctx, coOwner.ID, q.ID, "There is a car park next door.")
	if err != nil {
		t.Fatalf("co-owner answer: %v", err)
	}
	if !co.IsOwner {
		t.Error("a co-owner's answer is not marked as the owner's")
	}

	// A customer answer is not.
	cust, err := svc.Answer(ctx, asker.ID, q.ID, "I found it easily, thank you.")
	if err != nil {
		t.Fatalf("customer answer: %v", err)
	}
	if cust.IsOwner {
		t.Error("a customer's answer is marked as the owner's")
	}
}
