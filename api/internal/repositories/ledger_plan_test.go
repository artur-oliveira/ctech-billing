package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// The plan is what Post writes and what the bill repository extends. A
// two-leg transaction is header + 2*(entry, balance, summary) + audit, and a
// reversal adds its marker last.
func TestPlanPostItemCounts(t *testing.T) {
	r := &LedgerRepository{}
	sp, _ := space.ForJob("USER#u1", true)
	date := brcal.New(2026, time.March, 2)
	tx, _ := finance.NewTransaction(finance.KindTransfer, date,
		finance.Leg{AccountID: "a", Amount: 5}, finance.Leg{AccountID: "b", Amount: -5})
	plan, err := r.planPost(sp, tx, PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1+2*3+1 || plan.MarkerIdx != -1 || plan.TxID == "" {
		t.Fatalf("items %d marker %d id %q", len(plan.Items), plan.MarkerIdx, plan.TxID)
	}

	rev, _ := finance.Reverse(tx, "orig", date.AddDays(1))
	plan, err = r.planPost(sp, rev, PostMeta{Actor: "u"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.MarkerIdx != len(plan.Items)-2 { // marker is just before the audit row
		t.Fatalf("marker at %d of %d", plan.MarkerIdx, len(plan.Items))
	}
}
