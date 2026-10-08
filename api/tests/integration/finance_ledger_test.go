//go:build integration

package integration

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/smithy-go/middleware"
	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/api-commons/cache"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	mw "gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// newSpaceOrgID returns a fresh canonical organization id, so no test sees another's
// rows (the tables are shared by the whole run).
func newSpaceOrgID() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(err)
	}
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func ledgerFor(t *testing.T, db *dynamodb.Client) *repositories.LedgerRepository {
	t.Helper()
	return repositories.NewLedgerRepository(db, testCfg)
}

func jobSpace(t *testing.T, owner string, live bool) space.ResolvedSpace {
	t.Helper()
	sp, err := space.ForJob(owner, live)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// seedSpace creates the space and two asset accounts, "bank" and "cash".
func seedSpace(t *testing.T, r *repositories.LedgerRepository, sp space.ResolvedSpace) {
	t.Helper()
	ctx, now := context.Background(), time.Now()
	if err := r.EnsureSpace(ctx, sp, now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bank", "cash"} {
		if err := r.CreateAccount(ctx, sp, finance.LedgerAccount{ID: id, Name: id, Class: finance.ClassAsset}, now); err != nil {
			t.Fatal(err)
		}
	}
}

func transfer(t *testing.T, from, to string, cents billing.Cents, date brcal.Date) finance.Transaction {
	t.Helper()
	tx, err := finance.NewTransaction(finance.KindTransfer, date,
		finance.Leg{AccountID: to, Amount: cents}, finance.Leg{AccountID: from, Amount: -cents})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func balance(t *testing.T, r *repositories.LedgerRepository, sp space.ResolvedSpace, id string) billing.Cents {
	t.Helper()
	a, err := r.GetAccount(context.Background(), sp, id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Balance
}

func TestEnsureSpaceIsIdempotentAndCreatesTheSystemAccounts(t *testing.T) {
	r := ledgerFor(t, testDB)
	sp := jobSpace(t, "USER#ensure", true)
	for i := 0; i < 3; i++ {
		if err := r.EnsureSpace(context.Background(), sp, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	accts, err := r.ListAccounts(context.Background(), sp)
	if err != nil || len(accts) != 3 {
		t.Fatalf("accounts = %+v, %v", accts, err)
	}
}

func TestPostMovesBalancesAndSummariesAtomically(t *testing.T) {
	orgA := newSpaceOrgID()
	r := ledgerFor(t, testDB)
	sp := jobSpace(t, orgA, true)
	seedSpace(t, r, sp)
	date := brcal.New(2026, time.March, 2)
	if _, err := r.Post(context.Background(), sp, transfer(t, "bank", "cash", 1000, date), repositories.PostMeta{Origin: "manual", Actor: "u"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if balance(t, r, sp, "bank") != -1000 || balance(t, r, sp, "cash") != 1000 {
		t.Fatalf("balances bank=%d cash=%d", balance(t, r, sp, "bank"), balance(t, r, sp, "cash"))
	}
	sum, err := r.Summaries(context.Background(), sp, finance.Month{Year: 2026, Month: time.March}, finance.Month{Year: 2026, Month: time.March})
	if err != nil || len(sum) != 2 {
		t.Fatalf("summaries = %+v, %v", sum, err)
	}
	entries, err := r.Statement(context.Background(), sp, "cash", date, date.AddDays(1))
	if err != nil || len(entries) != 1 || entries[0].Amount != 1000 {
		t.Fatalf("statement = %+v, %v", entries, err)
	}
}

// Review Focus 4.
func TestPostRefusesAnAccountFromAnotherSpace(t *testing.T) {
	orgA := newSpaceOrgID()
	orgB := newSpaceOrgID()
	r := ledgerFor(t, testDB)
	a, b := jobSpace(t, orgA, true), jobSpace(t, orgB, true)
	seedSpace(t, r, a)
	if err := r.EnsureSpace(context.Background(), b, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateAccount(context.Background(), b, finance.LedgerAccount{ID: "only-in-b", Name: "b", Class: finance.ClassAsset}, time.Now()); err != nil {
		t.Fatal(err)
	}
	tx := transfer(t, "bank", "only-in-b", 500, brcal.New(2026, time.March, 2))
	before := balance(t, r, a, "bank")
	_, err := r.Post(context.Background(), a, tx, repositories.PostMeta{}, time.Now())
	if !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("err = %v, want ErrUnknownAccount", err)
	}
	if got := balance(t, r, a, "bank"); got != before {
		t.Fatalf("bank moved from %d to %d although the transaction was refused", before, got)
	}
	if bb := balance(t, r, b, "only-in-b"); bb != 0 {
		t.Fatalf("the other space's account moved: %d", bb)
	}
	txs, _ := r.AllTransactions(context.Background(), a)
	if len(txs) != 0 {
		t.Fatalf("a refused transaction left %d header(s)", len(txs))
	}
}

func TestSpacesAndModesAreIsolated(t *testing.T) {
	orgA := newSpaceOrgID()
	orgB := newSpaceOrgID()
	r := ledgerFor(t, testDB)
	live, test, other := jobSpace(t, orgA, true), jobSpace(t, orgA, false), jobSpace(t, orgB, true)
	for _, sp := range []space.ResolvedSpace{live, test, other} {
		seedSpace(t, r, sp)
	}
	if _, err := r.Post(context.Background(), live, transfer(t, "bank", "cash", 300, brcal.New(2026, time.March, 2)), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, sp := range map[string]space.ResolvedSpace{"test mode": test, "other organization": other} {
		if balance(t, r, sp, "cash") != 0 {
			t.Errorf("%s saw live's cash", name)
		}
		if txs, _ := r.AllTransactions(context.Background(), sp); len(txs) != 0 {
			t.Errorf("%s listed %d of live's transactions", name, len(txs))
		}
	}
}

// Review Focus 5.
func TestReverseTwiceIsRefused(t *testing.T) {
	r := ledgerFor(t, testDB)
	sp := jobSpace(t, "USER#reverse", true)
	seedSpace(t, r, sp)
	date := brcal.New(2026, time.March, 2)
	id, err := r.Post(context.Background(), sp, transfer(t, "bank", "cash", 800, date), repositories.PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reverse(context.Background(), sp, id, date.AddDays(1), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if balance(t, r, sp, "bank") != 0 || balance(t, r, sp, "cash") != 0 {
		t.Fatal("a reversal did not return the balances to zero")
	}
	_, err = r.Reverse(context.Background(), sp, id, date.AddDays(2), repositories.PostMeta{}, time.Now())
	if !errors.Is(err, repositories.ErrAlreadyReversed) {
		t.Fatalf("second reversal err = %v, want ErrAlreadyReversed", err)
	}
	if balance(t, r, sp, "bank") != 0 {
		t.Fatal("a second reversal moved a balance")
	}
}

func TestAnIdFromAnotherSpaceIsNotFound(t *testing.T) {
	orgA := newSpaceOrgID()
	orgB := newSpaceOrgID()
	r := ledgerFor(t, testDB)
	a, b := jobSpace(t, orgA, true), jobSpace(t, orgB, true)
	seedSpace(t, r, a)
	seedSpace(t, r, b)
	id, err := r.Post(context.Background(), a, transfer(t, "bank", "cash", 10, brcal.New(2026, time.March, 2)), repositories.PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetTransaction(context.Background(), b, id); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("B read A's transaction: %v", err)
	}
	if _, err := r.Reverse(context.Background(), b, id, brcal.New(2026, time.March, 3), repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("B reversed A's transaction: %v", err)
	}
}

func TestRebuildMatchesARandomHistoryAndFixesCorruption(t *testing.T) {
	r := ledgerFor(t, testDB)
	sp := jobSpace(t, "USER#rebuild", true)
	seedSpace(t, r, sp)
	rng := rand.New(rand.NewPCG(7, 8))
	ids := []string{"bank", "cash"}
	var posted []string
	for i := 0; i < 40; i++ {
		from, to := ids[rng.IntN(2)], ids[rng.IntN(2)]
		if from == to {
			continue
		}
		date := brcal.New(2026, time.Month(1+rng.IntN(12)), 1+rng.IntN(28))
		id, err := r.Post(context.Background(), sp, transfer(t, from, to, billing.Cents(1+rng.IntN(50000)), date), repositories.PostMeta{}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		posted = append(posted, id)
	}
	// Reverse a few so reversals are in the history too.
	for _, id := range posted[:3] {
		if _, err := r.Reverse(context.Background(), sp, id, brcal.New(2026, time.December, 28), repositories.PostMeta{}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	drift, err := r.Rebuild(context.Background(), sp, false, time.Now())
	if err != nil || len(drift) != 0 {
		t.Fatalf("a clean history drifted: %+v, %v", drift, err)
	}

	// Corrupt the cache behind the repository's back, then fix it.
	corruptBalance(t, sp, "bank", 99999)
	drift, err = r.Rebuild(context.Background(), sp, true, time.Now())
	if err != nil || len(drift) == 0 {
		t.Fatalf("corruption not detected: %+v, %v", drift, err)
	}
	drift, err = r.Rebuild(context.Background(), sp, false, time.Now())
	if err != nil || len(drift) != 0 {
		t.Fatalf("drift after -apply: %+v, %v", drift, err)
	}
}

// corruptBalance writes a wrong cached balance directly, as a bug or a manual
// edit would. It uses the raw table because the repository has no edit path.
func corruptBalance(t *testing.T, sp space.ResolvedSpace, account string, value int64) {
	t.Helper()
	base := repositories.NewBase(testDB, testCfg, repositories.TableLedgerAccounts)
	sk := repositories.LedgerAccountSK(account)
	if _, err := base.UpdateItem(context.Background(), sp.PK(), &sk, map[string]any{"balance": value}); err != nil {
		t.Fatal(err)
	}
}

// --- The selector cannot be spoofed: zero table reads -------------------------

type staticMembers map[string]string // "org|user" -> role

func (m staticMembers) Membership(_ context.Context, org, user string) (string, bool, error) {
	role, ok := m[org+"|"+user]
	return role, ok, nil
}

// countingClient returns a DynamoDB client that counts every API call it makes.
func countingClient(n *atomic.Int64) *dynamodb.Client {
	return dynamodb.NewFromConfig(testAWSConf, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(os.Getenv("DYNAMODB_ENDPOINT"))
		o.APIOptions = append(o.APIOptions, func(stack *middleware.Stack) error {
			return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("count",
				func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
					n.Add(1)
					return next.HandleInitialize(ctx, in)
				}), middleware.Before)
		})
	})
}

func TestUserAWithOrganizationBsIdIsA404AndReadsNothing(t *testing.T) {
	orgB := newSpaceOrgID()
	var reads atomic.Int64
	r := ledgerFor(t, countingClient(&reads))
	b := jobSpace(t, orgB, true)
	seedSpace(t, ledgerFor(t, testDB), b) // org B has data

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals(mw.ClaimsKey, &mw.Claims{Sub: "alice", SID: "s", Scope: mw.ScopeFinanceRead})
		return c.Next()
	})
	resolver := space.NewResolver(staticMembers{orgB + "|bob": "owner"}, cache.NewMemoryBackend(10))
	app.Get("/accounts", mw.RequireUserScope(mw.ScopeFinanceRead), mw.ResolveSpace(resolver), mw.RequireVerb(space.Read),
		func(c fiber.Ctx) error {
			rows, err := r.ListAccounts(c.Context(), mw.GetSpace(c))
			if err != nil {
				return err
			}
			return c.JSON(len(rows))
		})

	req := httptest.NewRequest("GET", "/accounts", nil)
	req.Header.Set(mw.ModeHeader, "live")
	req.Header.Set(mw.SpaceHeader, "org:"+orgB)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	if got := reads.Load(); got != 0 {
		t.Fatalf("a refused space caused %d DynamoDB call(s); want 0", got)
	}

	// And the personal selector ignores any id: it reads alice's own (empty) space.
	req = httptest.NewRequest("GET", "/accounts", nil)
	req.Header.Set(mw.ModeHeader, "live")
	req.Header.Set(mw.SpaceHeader, "personal")
	resp, _ = app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("personal status %d", resp.StatusCode)
	}
	if reads.Load() == 0 {
		t.Fatal("the call counter never moved on a successful read, so the zero above proves nothing")
	}
}

// Undoing a settlement is settling: a writer without finance.settle must not be
// able to reverse one (the verb guards the effect, not the endpoint).
func TestReversingASettlementNeedsTheSettleVerb(t *testing.T) {
	r := ledgerFor(t, testDB)
	full := jobSpace(t, "USER#settle-reverse", true)
	seedSpace(t, r, full)
	date := brcal.New(2026, time.March, 2)
	settle, err := finance.NewTransaction(finance.KindSettlement, date,
		finance.Leg{AccountID: "cash", Amount: 100}, finance.Leg{AccountID: "bank", Amount: -100})
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.Post(context.Background(), full, settle, repositories.PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	writer := space.Narrow(full, space.Read|space.Write)
	if _, err := r.Reverse(context.Background(), writer, id, date.AddDays(1), repositories.PostMeta{}, time.Now()); !errors.Is(err, space.ErrDenied) {
		t.Fatalf("a writer without settle reversed a settlement: err = %v", err)
	}
	if balance(t, r, full, "cash") != 100 {
		t.Fatal("the refused reversal moved a balance")
	}
	if _, err := r.Reverse(context.Background(), full, id, date.AddDays(1), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatalf("a holder of settle could not reverse it: %v", err)
	}
}
