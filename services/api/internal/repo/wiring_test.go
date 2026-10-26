package repo

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Every repository field must be wired by BOTH constructors.
//
// This exists because three of them - Bookings, Staff and Locations - were added
// to the struct and to NewForTx (the test harness) but never to New (production).
// Every test passed, because every test builds its repos from a transaction, and
// the first request to a booking endpoint in a real server panicked on a nil
// pointer. The struct and the two constructors have to be checked against each
// other mechanically; no amount of care at the call site catches a missing line.
func TestConstructorsWireEveryRepository(t *testing.T) {
	assertAllWired(t, "New", New(&pgxpool.Pool{}))
	// NewForTx only stores the transaction, so a nil one is enough to check the
	// fields are assigned.
	assertAllWired(t, "NewForTx", NewForTx(nil))
}

func assertAllWired(t *testing.T, name string, r *Repos) {
	t.Helper()
	v := reflect.ValueOf(r).Elem()
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		// `q` is the executor, not a repository: NewForTx stores whatever
		// transaction it is given, and this test passes nil because a real
		// pgx.Tx cannot be fabricated here. Its wiring is exercised by every
		// integration test instead.
		if typ.Field(i).Name == "q" {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Ptr, reflect.Interface:
			if f.IsNil() {
				t.Errorf("%s: %s.%s is nil - the constructor forgot it", name, typ.Name(), typ.Field(i).Name)
			}
		}
	}
}
