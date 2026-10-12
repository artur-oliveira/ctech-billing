//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func newByokAttempt(t *testing.T, org *billing.Organization) *billing.PaymentAttempt {
	t.Helper()
	inv := newDraftInvoice(t, org, "")
	a := &billing.PaymentAttempt{ID: id.NewWithPrefix(id.PrefixPaymentAttempt), OrganizationID: org.ID, Livemode: org.Livemode,
		InvoiceID: inv.ID, AttemptNumber: 1, Status: billing.AttemptPending, Method: billing.MethodBYOK, Amount: 4990,
		Provider: "inter", Product: billing.ProductPixDue, CreatedAt: now()}
	a.ChargeRef = billing.InterTxid(a.ID)
	return a
}

func TestABYOKAttemptAndItsPointerAreWrittenTogether(t *testing.T) {
	ctx := ctxT(t)
	payments := repositories.NewPaymentRepository(testDB, testCfg)
	org := newOrg(t, false)
	a := newByokAttempt(t, org)
	if err := payments.CreateByokAttempt(ctx, a, "acct-org", now()); err != nil {
		t.Fatal(err)
	}
	ref, err := payments.ChargeRef(ctx, "inter", a.Livemode, a.ChargeRef)
	if err != nil || ref.AttemptID != a.ID || ref.AccountOrganizationID != "acct-org" || ref.TenantID != org.ID || ref.InvoiceID != a.InvoiceID || ref.AttemptNumber != 1 {
		t.Fatalf("%+v %v", ref, err)
	}
	if _, err := payments.ChargeRef(ctx, "inter", !a.Livemode, a.ChargeRef); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("the pointer is per mode: %v", err)
	}
	if err := payments.CreateByokAttempt(ctx, a, "acct-org", now()); !errors.Is(err, repositories.ErrAttemptExists) {
		t.Fatalf("again: %v", err)
	}
	open, err := payments.OpenByok(ctx, org.ID, false, nil, 50)
	if err != nil || len(open.Items) != 1 || open.Items[0].ID != a.ID {
		t.Fatalf("the open list: %+v %v", open, err)
	}
	if err := payments.SetByokCharge(ctx, a, "000201PIX", brcal.New(2026, 3, 20), brcal.New(2026, 4, 19), now()); err != nil {
		t.Fatal(err)
	}
	got, err := payments.GetAttempt(ctx, org.ID, false, a.InvoiceID, 1)
	if err != nil || got.Payable != "000201PIX" || got.PayableUntil != brcal.New(2026, 4, 19) {
		t.Fatalf("%+v %v", got, err)
	}
	a.PaymentID, a.LateCharges = "E2E1", 300
	if _, err := payments.TransitionAttempt(ctx, a, billing.AttemptSucceeded, billing.CauseByokConfirmed, "service:byok-sweep", "", now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := payments.GetAttempt(ctx, org.ID, false, a.InvoiceID, 1); got.PaymentID != "E2E1" || got.LateCharges != 300 {
		t.Fatalf("the settling payment is recorded: %+v", got)
	}
	if open, _ := payments.OpenByok(ctx, org.ID, false, nil, 50); len(open.Items) != 0 {
		t.Fatal("a settled attempt leaves the open list")
	}

	ex := &billing.ExcessPayment{ID: "ex_" + id.New(), OrganizationID: org.ID, Livemode: false, InvoiceID: a.InvoiceID, AttemptID: a.ID,
		Provider: "inter", PaymentID: "E2", Amount: 100, Status: billing.ExcessOpen, PaidAt: now()}
	created, err := payments.RecordExcess(ctx, ex, now())
	if err != nil || !created {
		t.Fatalf("%v %v", created, err)
	}
	dup := *ex
	dup.ID = "ex_" + id.New()
	if again, err := payments.RecordExcess(ctx, &dup, now()); err != nil || again {
		t.Fatalf("one excess row per payment id: %v %v", again, err)
	}
	list, err := payments.ListExcess(ctx, org.ID, false, a.InvoiceID)
	if err != nil || len(list) != 1 || list[0].ID != ex.ID {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := payments.GetExcess(ctx, "org_other", false, ex.ID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("another tenant never reads it: %v", err)
	}
	if err := payments.SetExcessStatus(ctx, ex, billing.ExcessRefunding, "rf1", now()); err != nil {
		t.Fatal(err)
	}
	got2, err := payments.GetExcess(ctx, org.ID, false, ex.ID)
	if err != nil || got2.Status != billing.ExcessRefunding || got2.RefundID != "rf1" {
		t.Fatalf("%+v %v", got2, err)
	}
	if err := payments.SetExcessStatus(ctx, &billing.ExcessPayment{ID: ex.ID, OrganizationID: org.ID, InvoiceID: a.InvoiceID, PaymentID: "E2", Status: billing.ExcessOpen}, billing.ExcessRefunding, "rf2", now()); !errors.Is(err, repositories.ErrConcurrentModification) {
		t.Fatalf("a status change is conditional on the status read: %v", err)
	}
}
