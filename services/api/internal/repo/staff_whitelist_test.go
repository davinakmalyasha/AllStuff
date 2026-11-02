package repo_test

import (
	"context"
	"testing"

	"allstuff/api/internal/testutil"
)

// StaffRepo.Update interpolates keys as column names, so the allowlist is the
// only thing stopping a future caller from writing business_id or user_id and
// moving a staffer across businesses or accounts.
func TestStaffUpdateRejectsUnknownColumns(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	if err := h.Repos.Staff.Update(ctx, "00000000-0000-0000-0000-000000000000",
		map[string]any{"business_id": "00000000-0000-0000-0000-000000000001"}); err == nil {
		t.Error("business_id update: err = nil, want field-not-allowed")
	}
	if err := h.Repos.Staff.Update(ctx, "00000000-0000-0000-0000-000000000000",
		map[string]any{"user_id": "00000000-0000-0000-0000-000000000002"}); err == nil {
		t.Error("user_id update: err = nil, want field-not-allowed")
	}
}
