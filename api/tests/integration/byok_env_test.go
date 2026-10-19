//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/byok"
	"gopkg.aoctech.app/api-commons/byok/byoktest"
	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/ratelimit"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// unlimited lets these tests run several hourly syncs inside one minute; the
// banking and sandbox limiters themselves are pinned by internal/byokrpc tests.
type unlimited struct{}

func (unlimited) Incr(context.Context, string, time.Duration) (int64, error) { return 1, nil }
func (unlimited) Count(context.Context, string) (int64, error)               { return 0, nil }

type byokEnv struct {
	fake        *byoktest.Fake
	inv         *fakeInvoker
	repo        *repositories.ByokRepository
	ledger      *repositories.LedgerRepository
	statement   *services.ByokStatement
	connections *services.ByokConnections
	space       space.ResolvedSpace
	conn        *byokconn.Connection
	today       brcal.Date
}

func byokServices(t *testing.T, inv *fakeInvoker) (*repositories.ByokRepository, *repositories.LedgerRepository, *services.ByokStatement, *services.ByokConnections) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c := cache.NewMemoryBackend(10000)
	counter := ratelimit.NewMemoryCounter()
	repo := repositories.NewByokRepository(testDB, testCfg)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	tokens := byokrpc.NewTokenManager(c, counter, crypto.NewSealer(base64.StdEncoding.EncodeToString(k)))
	var conns *services.ByokConnections
	st := services.NewByokStatement(repo, ledger, c,
		func(conn *byokconn.Connection) byokrpc.Session { return conns.Session(conn) },
		func(ctx context.Context, conn *byokconn.Connection, err error, at time.Time) {
			conns.RecordOutcome(ctx, conn, err, at)
		})
	conns = services.NewByokConnections(repo, ledger, byokrpc.NewOutbound(inv, "fn"), tokens, byokrpc.NewLimits(unlimited{}), st)
	return repo, ledger, st, conns
}

// newByokEnv is one live connection on a fake bank, FirstDay = today − 100 days.
func newByokEnv(t *testing.T) *byokEnv {
	t.Helper()
	ctx := ctxT(t)
	f := byoktest.New()
	inv := newFakeInvoker(f)
	repo, ledger, st, conns := byokServices(t, inv)
	sp, err := space.ForJob(newSpaceOrgID(), true)
	if err != nil {
		t.Fatal(err)
	}
	today := brcal.FromTime(now())
	conn := &byokconn.Connection{AccountOrganizationID: sp.OrganizationID(), Livemode: true, Provider: "inter", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, SyncState: byokconn.SyncActive, Version: 1, Sealed: "b1.fake",
		AccountNumber: "1234567", Capabilities: []byok.Capability{byok.CapabilityStatement},
		FirstDay: today.AddDays(-100), BackfillFrom: today}
	conn.LedgerAccountID = services.LedgerAccountID("inter", conn.AccountNumber)
	if _, err := ledger.EnsureByokAccount(ctx, sp, conn.LedgerAccountID, "Inter ···4567", "inter", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateConnection(ctx, conn, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	return &byokEnv{fake: f, inv: inv, repo: repo, ledger: ledger, statement: st, connections: conns, space: sp, conn: conn, today: today}
}

// newByokSandboxEnv is a test organization that turned the shared sandbox on.
func newByokSandboxEnv(t *testing.T) *byokEnv {
	t.Helper()
	f := byoktest.New()
	inv := newFakeInvoker(f)
	repo, ledger, st, conns := byokServices(t, inv)
	sp, err := space.ForJob(newSpaceOrgID(), false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := conns.EnableSandbox(ctxT(t), sp, "inter", "u", "r", now())
	if err != nil {
		t.Fatal(err)
	}
	return &byokEnv{fake: f, inv: inv, repo: repo, ledger: ledger, statement: st, connections: conns, space: sp, conn: conn,
		today: brcal.FromTime(now())}
}

func (e *byokEnv) useFake(f *byoktest.Fake) { e.fake = f; e.inv.fake.Store(f) }

// writeChargeRef puts the CHARGEREF# pointer part B writes with the attempt.
func (e *byokEnv) writeChargeRef(t *testing.T, ref string) {
	t.Helper()
	base := repositories.NewBase(testDB, testCfg, repositories.TableByok)
	mode := repositories.Mode(e.conn.Livemode)
	if err := base.PutItem(ctxT(t), map[string]types.AttributeValue{
		"pk":                      &types.AttributeValueMemberS{Value: "CHARGEREF#" + e.conn.Provider + "#" + mode + "#" + ref},
		"sk":                      &types.AttributeValueMemberS{Value: "CHARGEREF"},
		"account_organization_id": &types.AttributeValueMemberS{Value: e.conn.AccountOrganizationID},
	}); err != nil {
		t.Fatal(err)
	}
}

func toByokDate(d brcal.Date) byok.Date { return byok.Date{Year: d.Year, Month: d.Month, Day: d.Day} }

func lineOn(d brcal.Date, lineID string, credit bool, amount int64) byok.StatementLine {
	dir := byok.Debit
	if credit {
		dir = byok.Credit
	}
	return byok.StatementLine{ID: lineID, Date: toByokDate(d), Direction: dir, Amount: amount, Title: "linha " + lineID, Description: "detalhe"}
}

func lineWithRef(d brcal.Date, lineID, ref string, amount int64) byok.StatementLine {
	l := lineOn(d, lineID, true, amount)
	l.ChargeRef = ref
	return l
}
