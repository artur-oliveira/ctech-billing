//go:build integration

package integration

import (
	"testing"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// The hourly job syncs every live connection of the job partition on the real
// tables; a second run posts nothing new.
func TestTheHourlyJobSyncsTheConnectionsOfItsMode(t *testing.T) {
	ctx := ctxT(t)
	env := newByokEnv(t)
	env.conn.BackfillDone = true
	if err := env.repo.SaveConnection(ctx, env.conn, "u", "r", "", now()); err != nil {
		t.Fatal(err)
	}
	env.fake.SetBalance(byok.Balance{Available: 700})
	env.fake.AddLines(lineOn(env.today, "J1", true, 700))
	spaceFor := func(c *byokconn.Connection) (space.ResolvedSpace, error) {
		return space.ForJob(c.AccountOrganizationID, c.Livemode)
	}
	job := services.NewByokJob(env.repo, env.connections, env.statement, nil, alerts.Nop{}, spaceFor)
	for range 2 {
		res := job.Run(ctx, true, now())
		// Other tests' live connections share the partition (and this env's
		// fake bank); only this connection's ledger is checked below.
		if res.Connections == 0 {
			t.Fatal("the live partition is empty")
		}
	}
	acct, _ := env.ledger.GetAccount(ctx, env.space, env.conn.LedgerAccountID)
	if acct.Balance != 700 || acct.Provider == nil || acct.Provider.Available != 700 {
		t.Fatalf("ledger %d bank %+v", acct.Balance, acct.Provider)
	}
}
