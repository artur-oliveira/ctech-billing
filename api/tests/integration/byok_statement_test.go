//go:build integration

package integration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
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

// Final review I3: the shared sandbox token belongs to every test tenant; one
// tenant's "Testar conexão" must not throw it away (nor spend the shared limit).
func TestTestingTheSandboxKeepsTheSharedToken(t *testing.T) {
	env := newByokSandboxEnv(t)
	if _, err := env.statement.Sync(ctxT(t), env.space, env.conn, now()); err != nil {
		t.Fatal(err)
	}
	before := env.fake.Calls(byok.OpToken)
	if _, err := env.connections.Test(ctxT(t), env.space, "inter", now()); err != nil {
		t.Fatal(err)
	}
	if got := env.fake.Calls(byok.OpToken); got != before {
		t.Fatalf("%d token fetches after Test, want %d", got, before)
	}
}

// Final review I4: reconnecting the same bank account resumes on its ledger
// account (active again) and never posts lines its opening already counts.
func TestReconnectingTheSameAccountResumesWithoutDoubleCounting(t *testing.T) {
	ctx := ctxT(t)
	env := newByokEnv(t) // only for the fake bank and the services
	sp, _ := space.ForJob(newSpaceOrgID(), true)
	today := env.today
	env.fake.SetBalance(byok.Balance{Available: 1000})
	env.fake.AddLines(lineOn(today.AddDays(-150), "OLD", true, 400), lineOn(today.AddDays(-10), "NEW", true, 600))
	firstOpening := toByokDate(today.AddMonths(-3).AddDays(-1))
	env.fake.SetBalanceAt(firstOpening, 400) // the 400 line is before the first window: it is in the opening
	certPEM, keyPEM := testPair(t, time.Now().AddDate(1, 0, 0))
	in := services.ConnectInput{Provider: "inter", ClientID: "c", ClientSecret: "s", CertPEM: certPEM, KeyPEM: keyPEM,
		Account: "7654321", NotifyEmail: "a@example.com", BackfillMonths: 3, Authorized: true}
	syncAll := func() {
		for range 20 { // the first runs may meet the connect goroutine holding the lock
			conn, err := env.repo.GetConnection(ctx, sp.OrganizationID(), true, "inter")
			if err != nil {
				t.Fatal(err)
			}
			_, err = env.statement.Sync(ctx, sp, conn, now())
			if err != nil && !errors.Is(err, repositories.ErrConnectionChanged) {
				t.Fatal(err)
			}
		}
	}
	if _, err := env.connections.Connect(ctx, sp, in, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	syncAll()
	if err := env.connections.Disconnect(ctx, sp, "inter", "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	in.BackfillMonths = 12 // further back than the opening: must not post OLD again
	if _, err := env.connections.Connect(ctx, sp, in, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	syncAll()
	conn, _ := env.repo.GetConnection(ctx, sp.OrganizationID(), true, "inter")
	if !conn.BackfillDone {
		t.Fatal("the reconnected backfill did not finish")
	}
	acct, _ := env.ledger.GetAccount(ctx, sp, services.LedgerAccountID("inter", "7654321"))
	if acct.Balance != 1000 || acct.SyncState != "active" {
		t.Fatalf("balance %d (bank 1000), sync_state %q", acct.Balance, acct.SyncState)
	}
}

// Final review I6: an hourly sync writes the connection once, and the row it
// writes is small (the sealed bundle lives in its own row, written only on
// connect and renewal), so the hourly cost stays near the plan's budget.
func TestAnHourlySyncWritesOneSmallRow(t *testing.T) {
	ctx := ctxT(t)
	env := newByokEnv(t)
	env.conn.BackfillDone = true
	if err := env.repo.SaveConnection(ctx, env.conn, "u", "r", "", now()); err != nil {
		t.Fatal(err)
	}
	var calls, records atomic.Int64
	counted := repositories.NewByokRepository(countingByokClient(&calls), testCfg)
	st := services.NewByokStatement(counted, env.ledger, cache.NewMemoryBackend(100), env.connections.Session,
		func(ctx context.Context, c *byokconn.Connection, err error, at time.Time) {
			// RecordOutcome writes the row too: a success must not cost a second write.
			records.Add(1)
			env.connections.RecordOutcome(ctx, c, err, at)
		})
	if _, err := st.Sync(ctx, env.space, env.conn, now()); err != nil {
		t.Fatal(err)
	}
	if calls.Load()+records.Load() != 1 {
		t.Fatalf("%d row writes and %d health writes in one sync, want one write in all", calls.Load(), records.Load())
	}
	item := repositories.NewBase(testDB, testCfg, repositories.TableByok)
	raw, _ := item.GetItem(ctx, env.conn.AccountOrganizationID+"#live", "CONNECTION#inter")
	if _, has := raw["sealed"]; has {
		t.Fatal("the hot connection row carries the sealed bundle")
	}
}

// countingByokClient counts the DynamoDB calls a repository makes.
func countingByokClient(n *atomic.Int64) *dynamodb.Client {
	return countingClient(n)
}
