package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func fixtureBill() finance.Bill {
	return finance.Bill{
		Direction: finance.Payable, Amount: 1000, AccountID: "bank", CategoryID: "rent",
		Competence: brcal.New(2026, time.March, 1), Due: brcal.New(2026, time.March, 10),
		Status: finance.BillForecast, Origin: finance.OriginManual,
	}
}

// Every method refuses before touching a table; a nil client would panic.
func TestEveryBillMethodChecksTheSpaceAndTheVerb(t *testing.T) {
	r := &BillRepository{}
	ctx, now := context.Background(), time.Now()
	var zero space.ResolvedSpace
	full, _ := space.ForJob("USER#u1", true)
	viewer := space.Narrow(full, space.Read)
	writer := space.Narrow(full, space.Read|space.Write) // no settle
	date := brcal.New(2026, time.March, 10)

	calls := map[string]func(sp space.ResolvedSpace) error{
		"Create": func(sp space.ResolvedSpace) error {
			_, err := r.Create(ctx, sp, fixtureBill(), PostMeta{}, now)
			return err
		},
		"Get": func(sp space.ResolvedSpace) error { _, err := r.Get(ctx, sp, "b"); return err },
		"ListOpen": func(sp space.ResolvedSpace) error {
			_, err := r.ListOpen(ctx, sp, finance.Payable, 10, nil)
			return err
		},
		"Settle": func(sp space.ResolvedSpace) error {
			_, err := r.Settle(ctx, sp, "b", 1000, "", date, PostMeta{}, now)
			return err
		},
		"Cancel": func(sp space.ResolvedSpace) error {
			_, err := r.Cancel(ctx, sp, "b", date, PostMeta{}, now)
			return err
		},
		"Edit": func(sp space.ResolvedSpace) error {
			_, err := r.Edit(ctx, sp, "b", BillEdit{}, date, PostMeta{}, now)
			return err
		},
	}
	for name, call := range calls {
		if err := call(zero); !errors.Is(err, space.ErrNoSpace) {
			t.Errorf("%s with the zero space: %v, want ErrNoSpace", name, err)
		}
	}
	for _, name := range []string{"Create", "Settle", "Cancel", "Edit"} {
		if err := calls[name](viewer); !errors.Is(err, space.ErrDenied) {
			t.Errorf("%s as a viewer: %v, want ErrDenied", name, err)
		}
	}
	if err := calls["Settle"](writer); !errors.Is(err, space.ErrDenied) {
		t.Errorf("Settle without finance.settle: %v, want ErrDenied", err)
	}
}

func TestCreateRefusesAnInvalidBillBeforeAnyWrite(t *testing.T) {
	r := &BillRepository{}
	sp, _ := space.ForJob("USER#u1", true)
	b := fixtureBill()
	b.Amount = billing.Cents(0)
	if _, err := r.Create(context.Background(), sp, b, PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidBill) {
		t.Fatalf("err = %v, want ErrInvalidBill", err)
	}
}

func TestABillThatAutoSettlesNeedsTheSettleVerb(t *testing.T) {
	r := &BillRepository{}
	full, _ := space.ForJob("USER#u1", true)
	writer := space.Narrow(full, space.Read|space.Write)
	b := fixtureBill()
	b.AutoSettle = true
	if _, err := r.Create(context.Background(), writer, b, PostMeta{}, time.Now()); !errors.Is(err, space.ErrDenied) {
		t.Fatalf("Create with auto_settle as a writer: %v, want ErrDenied", err)
	}
}

// Settle, Cancel and Edit all act on the bill as it was read; the guard is what
// stops a stale read from committing after anything that changes what a
// settlement or cancellation would do: an edit that appended a transaction
// (amount, category), or one that moved the account or the due date.
func TestTheForecastGuardPinsEverythingASettlementUses(t *testing.T) {
	b := fixtureBill()
	b.TransactionIDs = []string{"t1", "t2", "t3"}
	cond, names, values := forecastGuard(&b)
	want := "#status = :gforecast AND size(#n) = :gn AND account_id = :gacct AND #due = :gdue"
	if cond != want {
		t.Fatalf("cond = %q, want %q", cond, want)
	}
	if names["#n"] != "transaction_ids" || names["#status"] != "status" || names["#due"] != "due" {
		t.Fatalf("names = %v", names)
	}
	if n, ok := values[":gn"].(*types.AttributeValueMemberN); !ok || n.Value != "3" {
		t.Fatalf(":gn = %v, want 3", values[":gn"])
	}
	if a, ok := values[":gacct"].(*types.AttributeValueMemberS); !ok || a.Value != "bank" {
		t.Fatalf(":gacct = %v", values[":gacct"])
	}
	if d, ok := values[":gdue"].(*types.AttributeValueMemberS); !ok || d.Value != "2026-03-10" {
		t.Fatalf(":gdue = %v", values[":gdue"])
	}
}

func TestTheIdempotentIDIsStableAndScopedToTheSpace(t *testing.T) {
	a, _ := space.ForJob("USER#alice", true)
	b, _ := space.ForJob("USER#bob", true)
	x, y := idempotentID(a, "bill", "k1"), idempotentID(a, "bill", "k1")
	if x != y || x == "" {
		t.Fatalf("not stable: %q %q", x, y)
	}
	if idempotentID(b, "bill", "k1") == x {
		t.Error("two spaces share an id for one key")
	}
	if idempotentID(a, "recurrence", "k1") == x {
		t.Error("a bill and a recurrence share an id for one key")
	}
	if idempotentID(a, "bill", "k2") == x {
		t.Error("two keys share an id")
	}
}
