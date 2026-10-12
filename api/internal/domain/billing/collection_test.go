package billing

import (
	"strings"
	"testing"
	"time"
)

func alnum(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func TestInterTxidFollowsBacen(t *testing.T) {
	got := InterTxid("pa_01JABCDEFGHJKMNPQRSTVWXYZ0")
	if len(got) < 26 || len(got) > 35 || !alnum(got) || !strings.HasPrefix(got, "ctb") {
		t.Fatalf("%q", got)
	}
	if len(got) != 29 {
		t.Fatalf("ctb + a 26-character ULID: %d", len(got))
	}
}

func TestRefundIDIsShortAlphanumeric(t *testing.T) {
	got := NewRefundID()
	if len(got) != 28 || !strings.HasPrefix(got, "rf") || !alnum(got) {
		t.Fatalf("%q", got)
	}
}

func TestCollectionSettingsBounds(t *testing.T) {
	if (CollectionSettings{FineBasisPoints: 2001}).Validate() == nil || (CollectionSettings{MonthlyInterestBasisPoints: -1}).Validate() == nil ||
		(CollectionSettings{MonthlyInterestBasisPoints: 1001}).Validate() == nil {
		t.Fatal("out of range accepted")
	}
	if (CollectionSettings{FineBasisPoints: 200, MonthlyInterestBasisPoints: 100}).Validate() != nil || (CollectionSettings{}).Validate() != nil {
		t.Fatal("2 % and 1 %/month refused")
	}
}

func TestTheRefundWindowIsNinetyDays(t *testing.T) {
	paid := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := ExcessPayment{PaidAt: paid}
	if !e.Refundable(paid.AddDate(0, 0, 90)) || e.Refundable(paid.AddDate(0, 0, 91)) {
		t.Fatal("90 days")
	}
}

func TestByokAttemptsSettleOnlyFromTheProvider(t *testing.T) {
	a := &PaymentAttempt{Status: AttemptPending, Method: MethodBYOK}
	if _, err := a.Transition(AttemptSucceeded, CauseByokConfirmed); err != nil {
		t.Fatal(err)
	}
	b := &PaymentAttempt{Status: AttemptPending, Method: MethodBYOK}
	if _, err := b.Transition(AttemptFailed, CauseByokDisconnected); err != nil {
		t.Fatal(err)
	}
}

func TestTheChargeStaysPayableAsLongAsDunningChases(t *testing.T) {
	inv := &Invoice{Policy: DunningSchedule{{Offset: -3, Action: DunningRemind}, {Offset: 10, Action: DunningRemind}, {Offset: 30, Action: DunningRemind}}}
	if got := inv.ValidityDays(); got != 30 {
		t.Fatalf("%d", got)
	}
	if got := (&Invoice{Policy: DunningSchedule{{Offset: -3, Action: DunningRemind}}}).ValidityDays(); got != 1 {
		t.Fatalf("at least one day: %d", got)
	}
}
