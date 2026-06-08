package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func seedDelegationForPayments(t *testing.T, c *store.Client, conferenceID string) domain.Delegation {
	t.Helper()
	d, err := c.CreateDelegation(context.Background(), domain.Delegation{
		ConferenceID: conferenceID,
		School:       "Test School",
	})
	if err != nil {
		t.Fatalf("seed delegation: %v", err)
	}
	return d
}

// signedCharge returns the ledger-shape (signed) amount for a charge of
// (units, cents). Mirrors what the handler does given PAYMENT_KIND_CHARGE.
func signedCharge(units int64, cents int32) (int64, int32) { return -units, -cents }

// signedPayment returns the ledger-shape (signed) amount for a payment.
func signedPayment(units int64, cents int32) (int64, int32) { return units, cents }

func TestRecordPaymentChargeThenPayment(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	del := seedDelegationForPayments(t, c, conferenceID)
	actor := newID(t)

	// $100.00 charge → balanceDue 100.00.
	cu, cc := signedCharge(100, 0)
	charge, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: del.ID,
		AmountUnits:  cu,
		AmountCents:  cc,
		Kind:         domain.PaymentKindCharge,
		Method:       domain.PaymentMethodOther,
		RecordedBy:   actor,
		CreatedBy:    actor,
	})
	if err != nil {
		t.Fatalf("record charge: %v", err)
	}
	if charge.ID == "" || charge.Version != 1 {
		t.Fatalf("charge fields: %+v", charge)
	}

	got, err := c.GetDelegation(ctx, conferenceID, del.ID)
	if err != nil {
		t.Fatalf("get delegation after charge: %v", err)
	}
	if got.BalanceDueUnits != 100 || got.BalanceDueCents != 0 || got.PaidInFull {
		t.Fatalf("after charge want balanceDue=100.00 paidInFull=false, got %+v", got)
	}

	// $40.00 payment → balanceDue 60.00.
	pu, pc := signedPayment(40, 0)
	if _, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: del.ID,
		AmountUnits:  pu,
		AmountCents:  pc,
		Kind:         domain.PaymentKindPayment,
		Method:       domain.PaymentMethodCheck,
		Reference:    "check #1234",
		RecordedBy:   actor,
	}); err != nil {
		t.Fatalf("record payment #1: %v", err)
	}
	got, err = c.GetDelegation(ctx, conferenceID, del.ID)
	if err != nil {
		t.Fatalf("get delegation after payment: %v", err)
	}
	if got.BalanceDueUnits != 60 || got.BalanceDueCents != 0 || got.PaidInFull {
		t.Fatalf("after payment want balanceDue=60.00, got %+v", got)
	}

	// $60.00 payment → balanceDue 0, paidInFull true.
	pu, pc = signedPayment(60, 0)
	if _, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: del.ID,
		AmountUnits:  pu,
		AmountCents:  pc,
		Kind:         domain.PaymentKindPayment,
		Method:       domain.PaymentMethodWire,
		RecordedBy:   actor,
	}); err != nil {
		t.Fatalf("record final payment: %v", err)
	}
	got, err = c.GetDelegation(ctx, conferenceID, del.ID)
	if err != nil {
		t.Fatalf("get delegation after paid-in-full: %v", err)
	}
	if got.BalanceDueUnits != 0 || got.BalanceDueCents != 0 || !got.PaidInFull {
		t.Fatalf("want paidInFull=true balanceDue=0, got %+v", got)
	}
}

func TestRecordPaymentMissingDelegation(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	if _, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: newID(t),
		DelegationID: newID(t),
		AmountUnits:  -100,
		Kind:         domain.PaymentKindCharge,
		Method:       domain.PaymentMethodOther,
		RecordedBy:   newID(t),
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing delegation: want ErrNotFound, got %v", err)
	}
}

func TestListAllPaymentsByDelegationNewestFirst(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	del := seedDelegationForPayments(t, c, conferenceID)
	actor := newID(t)

	t0 := time.Now().UTC().Add(-2 * time.Hour)
	t1 := time.Now().UTC().Add(-time.Hour)
	t2 := time.Now().UTC()

	for _, when := range []time.Time{t0, t1, t2} {
		if _, err := c.RecordPayment(ctx, domain.PaymentRecord{
			ConferenceID: conferenceID,
			DelegationID: del.ID,
			AmountUnits:  -10,
			Kind:         domain.PaymentKindCharge,
			Method:       domain.PaymentMethodOther,
			RecordedBy:   actor,
			RecordedAt:   when,
		}); err != nil {
			t.Fatalf("record payment at %s: %v", when, err)
		}
	}

	rows, err := c.ListAllPaymentsByDelegation(ctx, del.ID)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if !rows[0].RecordedAt.After(rows[1].RecordedAt) || !rows[1].RecordedAt.After(rows[2].RecordedAt) {
		t.Fatalf("expected newest-first ordering, got %+v", rows)
	}
}

func TestUpdatePaymentNotesAndReference(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	del := seedDelegationForPayments(t, c, conferenceID)
	actor := newID(t)

	recorded, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: del.ID,
		AmountUnits:  -50,
		Kind:         domain.PaymentKindCharge,
		Method:       domain.PaymentMethodOther,
		Reference:    "old-ref",
		Notes:        "old-notes",
		RecordedBy:   actor,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	sk := "PAYMENT#" + recorded.RecordedAt.Format(time.RFC3339Nano) + "#" + recorded.ID
	newRef := "new-ref-9876"
	newNotes := "updated notes"

	updated, err := c.UpdatePayment(ctx, del.ID, sk, recorded.ID, 1, store.UpdatePaymentPatch{
		Reference: &newRef,
		Notes:     &newNotes,
		UpdatedBy: actor,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 || updated.Reference != newRef || updated.Notes != newNotes {
		t.Fatalf("after update: %+v", updated)
	}
	// Amount/Kind/Method must not change.
	if updated.AmountUnits != recorded.AmountUnits || updated.Kind != recorded.Kind || updated.Method != recorded.Method {
		t.Fatalf("update changed immutable fields: %+v vs %+v", updated, recorded)
	}

	// Stale version triggers ErrVersionMismatch.
	if _, err := c.UpdatePayment(ctx, del.ID, sk, recorded.ID, 1, store.UpdatePaymentPatch{Notes: &newNotes}); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale update: want ErrVersionMismatch, got %v", err)
	}
}

func TestSoftDeletePaymentReversesBalance(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	del := seedDelegationForPayments(t, c, conferenceID)
	actor := newID(t)

	// $100 charge → balanceDue 100.
	charge, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: del.ID,
		AmountUnits:  -100,
		Kind:         domain.PaymentKindCharge,
		Method:       domain.PaymentMethodOther,
		RecordedBy:   actor,
	})
	if err != nil {
		t.Fatalf("record charge: %v", err)
	}

	// $30 payment → balanceDue 70.
	pmt, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: del.ID,
		AmountUnits:  30,
		Kind:         domain.PaymentKindPayment,
		Method:       domain.PaymentMethodCash,
		RecordedBy:   actor,
	})
	if err != nil {
		t.Fatalf("record payment: %v", err)
	}

	got, err := c.GetDelegation(ctx, conferenceID, del.ID)
	if err != nil || got.BalanceDueUnits != 70 {
		t.Fatalf("setup mismatch: want 70, got %+v err=%v", got, err)
	}

	// Soft-delete the $30 payment → balanceDue should go back to 100.
	pmtSK := "PAYMENT#" + pmt.RecordedAt.Format(time.RFC3339Nano) + "#" + pmt.ID
	if err := c.SoftDeletePayment(ctx, del.ID, pmtSK, 1, actor); err != nil {
		t.Fatalf("soft delete payment: %v", err)
	}
	got, err = c.GetDelegation(ctx, conferenceID, del.ID)
	if err != nil {
		t.Fatalf("get delegation after delete: %v", err)
	}
	if got.BalanceDueUnits != 100 || got.BalanceDueCents != 0 || got.PaidInFull {
		t.Fatalf("after delete-payment want 100.00 not-paid, got %+v", got)
	}

	// Charge soft-delete also reverses → balanceDue 0, paidInFull true.
	chargeSK := "PAYMENT#" + charge.RecordedAt.Format(time.RFC3339Nano) + "#" + charge.ID
	if err := c.SoftDeletePayment(ctx, del.ID, chargeSK, 1, actor); err != nil {
		t.Fatalf("soft delete charge: %v", err)
	}
	got, err = c.GetDelegation(ctx, conferenceID, del.ID)
	if err != nil {
		t.Fatalf("get delegation after charge delete: %v", err)
	}
	if got.BalanceDueUnits != 0 || got.BalanceDueCents != 0 || !got.PaidInFull {
		t.Fatalf("after delete-charge want 0 paidInFull=true, got %+v", got)
	}
}

func TestFindPaymentByIDAcrossDelegations(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	delA := seedDelegationForPayments(t, c, conferenceID)
	delB := seedDelegationForPayments(t, c, conferenceID)
	actor := newID(t)

	pA, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: delA.ID,
		AmountUnits:  -10,
		Kind:         domain.PaymentKindCharge,
		Method:       domain.PaymentMethodOther,
		RecordedBy:   actor,
	})
	if err != nil {
		t.Fatalf("record A: %v", err)
	}
	if _, err := c.RecordPayment(ctx, domain.PaymentRecord{
		ConferenceID: conferenceID,
		DelegationID: delB.ID,
		AmountUnits:  -20,
		Kind:         domain.PaymentKindCharge,
		Method:       domain.PaymentMethodOther,
		RecordedBy:   actor,
	}); err != nil {
		t.Fatalf("record B: %v", err)
	}

	found, err := c.FindPaymentByID(ctx, pA.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if found.DelegationID != delA.ID {
		t.Fatalf("find by id returned wrong delegation: %+v", found)
	}

	// Composite lookup.
	got, err := c.GetPayment(ctx, delA.ID, pA.ID)
	if err != nil {
		t.Fatalf("get payment: %v", err)
	}
	if got.ID != pA.ID || got.AmountUnits != -10 {
		t.Fatalf("get payment mismatch: %+v", got)
	}
}
