package service

import "testing"

func TestCanonicalPaymentMethods(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"cash":                  "cash",
		"qris,cash":             "cash,qris",
		" QRIS , Cash , qris ":  "cash,qris",
		"transfer,ewallet,cash": "cash,transfer,ewallet",
		"bitcoin":               "",
		"cash,bitcoin,qris":     "cash,qris",
	}
	for in, want := range cases {
		if got := canonicalPaymentMethods(in); got != want {
			t.Errorf("canonicalPaymentMethods(%q) = %q, want %q", in, got, want)
		}
	}
}
