package repositories

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// The repository must offer no way to edit what was written (spec § 10,
// "entries are never edited"). Corrections are Reverse.
// settingsWriters are the methods that write the space's own preferences on its
// SPACE row. They never touch a transaction, an entry or a balance, which is
// what "no edit path" protects; naming them here keeps the exception reviewable.
var settingsWriters = map[string]bool{"SetDefaultReceivingAccount": true, "SetPostCTechInvoices": true}

func TestLedgerRepositoryHasNoEditPath(t *testing.T) {
	rt := reflect.TypeOf(&LedgerRepository{})
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		if settingsWriters[name] {
			continue
		}
		for _, banned := range []string{"Update", "Delete", "Edit", "Set", "Remove", "Patch", "Put", "Overwrite"} {
			if strings.HasPrefix(name, banned) {
				t.Errorf("LedgerRepository.%s: the ledger has no edit path; use Reverse", name)
			}
		}
	}
}

// A repository handed the zero space must refuse before touching a table. A nil
// client would panic on any call, so reaching it would fail this test loudly.
func TestEveryLedgerMethodRefusesTheZeroSpace(t *testing.T) {
	r := &LedgerRepository{}
	ctx := context.Background()
	var zero space.ResolvedSpace
	tx, _ := finance.NewTransaction(finance.KindTransfer, brcal.New(2026, time.March, 2),
		finance.Leg{AccountID: "a", Amount: 1}, finance.Leg{AccountID: "b", Amount: -1})

	checks := map[string]error{}
	checks["EnsureSpace"] = r.EnsureSpace(ctx, zero, time.Now())
	_, checks["Post"] = r.Post(ctx, zero, tx, PostMeta{}, time.Now())
	_, checks["Reverse"] = r.Reverse(ctx, zero, "x", brcal.New(2026, time.March, 3), PostMeta{}, time.Now())
	_, err := r.ListAccounts(ctx, zero)
	checks["ListAccounts"] = err
	_, err = r.GetAccount(ctx, zero, "a")
	checks["GetAccount"] = err
	_, err = r.Statement(ctx, zero, "a", brcal.New(2026, 1, 1), brcal.New(2026, 2, 1))
	checks["Statement"] = err
	_, err = r.Summaries(ctx, zero, finance.Month{Year: 2026, Month: 1}, finance.Month{Year: 2026, Month: 2})
	checks["Summaries"] = err
	_, err = r.GetTransaction(ctx, zero, "x")
	checks["GetTransaction"] = err
	_, err = r.AllTransactions(ctx, zero)
	checks["AllTransactions"] = err
	checks["ArchiveAccount"] = r.ArchiveAccount(ctx, zero, "a", time.Now())
	checks["SetDefaultReceivingAccount"] = r.SetDefaultReceivingAccount(ctx, zero, "a", time.Now())
	checks["SetPostCTechInvoices"] = r.SetPostCTechInvoices(ctx, zero, false, "a", "", time.Now())
	_, err = r.GetSettings(ctx, zero)
	checks["GetSettings"] = err
	checks["CreateAccount"] = r.CreateAccount(ctx, zero, finance.LedgerAccount{ID: "a", Name: "a", Class: finance.ClassAsset}, time.Now())

	for name, err := range checks {
		if !errors.Is(err, space.ErrNoSpace) {
			t.Errorf("%s with the zero space: err = %v, want ErrNoSpace", name, err)
		}
	}
}

func TestPostRefusesWithoutTheVerb(t *testing.T) {
	r := &LedgerRepository{}
	full, _ := space.ForJob("USER#u1", true)
	viewer := space.Narrow(full, space.Read)
	tx, _ := finance.NewTransaction(finance.KindTransfer, brcal.New(2026, time.March, 2),
		finance.Leg{AccountID: "a", Amount: 1}, finance.Leg{AccountID: "b", Amount: -1})
	if _, err := r.Post(context.Background(), viewer, tx, PostMeta{}, time.Now()); !errors.Is(err, space.ErrDenied) {
		t.Fatalf("viewer Post = %v, want ErrDenied", err)
	}
	settlement, _ := finance.NewTransaction(finance.KindSettlement, brcal.New(2026, time.March, 2),
		finance.Leg{AccountID: "a", Amount: 1}, finance.Leg{AccountID: "b", Amount: -1})
	writer := space.Narrow(full, space.Read|space.Write)
	if _, err := r.Post(context.Background(), writer, settlement, PostMeta{}, time.Now()); !errors.Is(err, space.ErrDenied) {
		t.Fatalf("a writer without finance.settle posted a settlement: %v", err)
	}
}

func TestPostRefusesAZeroTransaction(t *testing.T) {
	r := &LedgerRepository{}
	full, _ := space.ForJob("USER#u1", true)
	if _, err := r.Post(context.Background(), full, finance.Transaction{}, PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidTransaction) {
		t.Fatalf("zero transaction: %v", err)
	}
}
