package service

import (
	"testing"

	"bizverse/api/internal/domain"
)

func TestDescribeEverySupportedType(t *testing.T) {
	// Every registered type must have an email/notification label.
	for _, ty := range SupportedTypes {
		n := &domain.Notification{Type: ty, Payload: map[string]any{}}
		title, body := describe(n)
		if title == "" || body == "" {
			t.Errorf("type %q: describe returned empty title/body", ty)
		}
	}
}

func TestDescribeUnknownTypeIsEmpty(t *testing.T) {
	n := &domain.Notification{Type: "made_up_type", Payload: map[string]any{}}
	if title, _ := describe(n); title != "" {
		t.Errorf("unknown type should produce no email, got %q", title)
	}
}
