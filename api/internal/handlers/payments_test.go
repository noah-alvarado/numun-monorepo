package handlers_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/handlers"
)

func TestRecordPaymentAmounts(t *testing.T) {
	st := testStore(t)
	svc := &handlers.PaymentService{Store: st, Scoper: auth.NewScoper(st)}
	advisor := seedUser(t, st, domain.RoleAdvisor, "")
	del := seedDelegation(t, st, newUUID(t), advisor.ID)
	admin := as("admin", domain.RoleStaffAdmin)

	record := func(kind v1.PaymentKind, currency string, units int64) error {
		_, err := svc.RecordPayment(admin, connect.NewRequest(&v1.RecordPaymentRequest{
			DelegationId: del.ID,
			Amount:       &v1.Money{Currency: currency, Units: units},
			Kind:         kind,
			Method:       v1.PaymentMethod_PAYMENT_METHOD_OTHER,
		}))
		return err
	}
	balance := func() int64 {
		d, err := st.FindDelegationByID(context.Background(), del.ID)
		if err != nil {
			t.Fatalf("find delegation: %v", err)
		}
		return d.BalanceDueUnits
	}

	if err := record(v1.PaymentKind_PAYMENT_KIND_CHARGE, "USD", 1000); err != nil {
		t.Fatalf("charge: %v", err)
	}
	wantCode(t, record(v1.PaymentKind_PAYMENT_KIND_PAYMENT, "EUR", 5), connect.CodeInvalidArgument)
	wantCode(t, record(v1.PaymentKind_PAYMENT_KIND_PAYMENT, "USD", 0), connect.CodeInvalidArgument)
	if got := balance(); got != 1000 {
		t.Fatalf("balance after rejected entries = %d, want 1000", got)
	}

	// A positive adjustment is a credit; a negative one adds to the balance.
	if err := record(v1.PaymentKind_PAYMENT_KIND_ADJUSTMENT, "usd", 100); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if got := balance(); got != 900 {
		t.Fatalf("balance after credit = %d, want 900", got)
	}
	if err := record(v1.PaymentKind_PAYMENT_KIND_ADJUSTMENT, "", -50); err != nil {
		t.Fatalf("debit: %v", err)
	}
	if got := balance(); got != 950 {
		t.Fatalf("balance after debit = %d, want 950", got)
	}
}
