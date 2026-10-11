//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/services"
)

func TestBackfillThenIncrementalEqualsTheBank(t *testing.T) {
	ctx := ctxT(t)
	env := newByokEnv(t) // fake provider + real tables; one live connection with FirstDay = today−100d
	env.fake.SetBalance(byok.Balance{Available: 90000})
	env.fake.AddLines(lineOn(env.today.AddDays(-95), "T1", true, 10000), lineOn(env.today.AddDays(-10), "T2", false, 2500), lineOn(env.today, "T3", true, 500))
	// opening = the bank's balance the day before FirstDay: 90000 − (10000 − 2500 + 500) = 82000.
	env.fake.SetBalanceAt(toByokDate(env.conn.FirstDay.AddDays(-1)), 82000)
	for range 3 { // two backfill windows and the incremental, at most
		if _, err := env.statement.Sync(ctx, env.space, env.conn, now()); err != nil {
			t.Fatal(err)
		}
	}
	acct, _ := env.ledger.GetAccount(ctx, env.space, env.conn.LedgerAccountID)
	if !env.conn.BackfillDone || int64(acct.Balance) != 90000 || acct.Provider == nil || int64(acct.Provider.Available) != 90000 {
		t.Fatalf("done=%v ledger=%d bank=%+v", env.conn.BackfillDone, acct.Balance, acct.Provider)
	}
	before := env.fake.Calls(byok.OpStatementPage)
	if r, _ := env.statement.Sync(ctx, env.space, env.conn, now()); r.Posted != 0 {
		t.Fatalf("a second sync posted %d", r.Posted)
	}
	if env.fake.Calls(byok.OpStatementPage) == before {
		t.Fatal("the incremental re-reads the last 3 days")
	}
}

func TestTenantBNeverSeesTenantAsSandboxLines(t *testing.T) {
	ctx := ctxT(t)
	a, b := newByokSandboxEnv(t), newByokSandboxEnv(t) // two test organizations on the shared sandbox fake
	a.writeChargeRef(t, "ctbAAAAAAAAAAAAAAAAAAAAAAAAAA")
	shared := []byok.StatementLine{
		lineWithRef(a.today, "S1", "ctbAAAAAAAAAAAAAAAAAAAAAAAAAA", 100),
		lineOn(a.today, "S2", true, 999), // someone's typed test data: no ref
	}
	a.fake.AddLines(shared...)
	b.useFake(a.fake)
	ra, _ := a.statement.Sync(ctx, a.space, a.conn, now())
	rb, _ := b.statement.Sync(ctx, b.space, b.conn, now())
	if ra.Posted != 1 || ra.Dropped != 1 || rb.Posted != 0 || rb.Dropped != 2 {
		t.Fatalf("a=%+v b=%+v", ra, rb)
	}
	acct, _ := a.ledger.GetAccount(ctx, a.space, a.conn.LedgerAccountID)
	if acct.Provider == nil || acct.Provider.Available != acct.Balance || acct.Balance != 100 {
		t.Fatalf("a test account's balance is its own lines, never the shared sandbox's: %+v", acct)
	}
}

func TestSyncNowIsRefusedWithinTenMinutes(t *testing.T) {
	env := newByokEnv(t)
	if _, err := env.statement.SyncNow(ctxT(t), env.space, env.conn, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.statement.SyncNow(ctxT(t), env.space, env.conn, now().Add(9*time.Minute)); !errors.Is(err, services.ErrSyncTooSoon) {
		t.Fatalf("%v", err)
	}
}

func TestASyncNeverUsesScroll(t *testing.T) {
	// The fake pages; the provider adapter has no scroll path (Task 4 pins the
	// query), so this asserts the pages advance.
	env := newByokEnv(t)
	env.fake.PageSize = 1
	env.fake.AddLines(lineOn(env.today, "P1", true, 1), lineOn(env.today, "P2", true, 1))
	env.conn.BackfillDone = true
	r, _ := env.statement.Sync(ctxT(t), env.space, env.conn, now())
	if r.Posted != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestAZeroOpeningBalanceStillEndsTheBackfill(t *testing.T) {
	env := newByokEnv(t)
	env.conn.FirstDay = env.today.AddDays(-10)
	env.fake.SetBalance(byok.Balance{Available: 0})
	r, err := env.statement.Sync(ctxT(t), env.space, env.conn, now())
	if err != nil || !r.BackfillDone {
		t.Fatalf("a new bank account with R$ 0 the day before: %+v %v", r, err)
	}
}
