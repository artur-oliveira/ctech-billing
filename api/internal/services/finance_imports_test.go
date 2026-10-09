package services

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func TestReconcileOffersCandidatesToPendingLinesOnly(t *testing.T) {
	d := brcal.New(2026, time.March, 10)
	open := []finance.Bill{{ID: "rent", Direction: finance.Payable, Amount: 1000, AccountID: "bank", Due: d, Status: finance.BillForecast}}
	lines := []repositories.ImportLine{
		{N: 1, Date: d, Amount: -1000, Status: repositories.LinePending},
		{N: 2, Date: d, Amount: -1000, Status: repositories.LineIgnored},
		{N: 3, Date: d, Amount: 1000, Status: repositories.LinePending},
	}
	got := Reconcile("bank", lines, open, nil)
	if len(got[0].Candidates) != 1 || got[0].Candidates[0].ID != "rent" {
		t.Fatalf("pending payment: %+v", got[0].Candidates)
	}
	if got[1].Candidates != nil {
		t.Fatal("an ignored line is not offered anything")
	}
	if len(got[2].Candidates) != 0 {
		t.Fatal("money in does not pay a payable")
	}
}

// A bill auto-settle already paid is offered after the open bills, and only to
// a pending line of its exact amount.
func TestReconcileOffersAutoPaidBillsAfterTheOpenOnes(t *testing.T) {
	d := brcal.New(2026, time.March, 10)
	open := []finance.Bill{{ID: "open", Direction: finance.Payable, Amount: 1000, AccountID: "bank", Due: d, Status: finance.BillForecast}}
	autoPaid := []finance.Bill{
		{ID: "auto", Direction: finance.Payable, Amount: 1000, AccountID: "bank", Due: d, PaidDate: d, Status: finance.BillPaid},
		{ID: "auto-interest", Direction: finance.Payable, Amount: 1090, AccountID: "bank", Due: d, PaidDate: d, Status: finance.BillPaid},
	}
	lines := []repositories.ImportLine{
		{N: 1, Date: d, Amount: -1000, Status: repositories.LinePending},
		{N: 2, Date: d, Amount: -1000, Status: repositories.LineIgnored},
		{N: 3, Date: d, Amount: -1050, Status: repositories.LinePending},
	}
	got := Reconcile("bank", lines, open, autoPaid)
	if len(got[0].Candidates) != 2 || got[0].Candidates[0].ID != "open" || got[0].Candidates[1].ID != "auto" {
		t.Fatalf("pending payment: %+v", got[0].Candidates)
	}
	if got[1].Candidates != nil {
		t.Fatal("an ignored line is not offered anything")
	}
	if len(got[2].Candidates) != 0 {
		t.Fatalf("a different amount is not offered: %+v", got[2].Candidates)
	}
}
