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
	got := Reconcile("bank", lines, open)
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
