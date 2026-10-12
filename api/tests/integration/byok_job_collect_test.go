//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// runJob is cmd/byok's run for this env's mode, at "at". Other tests'
// connections share the job partition (each with its own fake bank): their
// errors are expected; this env's tenant is what the tests check.
func (e *collectEnv) runJob(t *testing.T, at time.Time) {
	t.Helper()
	spaceFor := func(c *byokconn.Connection) (space.ResolvedSpace, error) {
		return space.ForJob(c.AccountOrganizationID, c.Livemode)
	}
	job := services.NewByokJob(e.repo, e.conns, e.statement, nil, alerts.Nop{}, spaceFor)
	e.sweep().Register(job)
	res := job.Run(ctxT(t), e.livemode, at)
	for _, err := range res.Errors {
		if strings.Contains(err.Error(), e.accountOrg) {
			t.Logf("job: %v", err)
		}
	}
}

func (e *collectEnv) sweep() *services.ByokSweep {
	return services.NewByokSweep(e.byok, e.payments, e.orgs, e.setup(), e.repo, e.conns, alerts.Nop{})
}

func (e *collectEnv) setup() *services.ByokCollectionSetup {
	return services.NewByokCollectionSetup(e.conns, e.repo, e.orgs, e.payments, e.byok, e.webhooks, "billing-byok-hooks-test.aoctech.app", e.tz)
}

func saoPaulo(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, brcal.Location)
}

func TestTheSweepConfirmsWhatTheWebhookMissed(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	inv := env.finalizeInvoice(t, 1000)
	env.fake.Pay(env.chargeRefOf(inv), 1000, 0, now())
	env.runJob(t, now().Add(2*time.Hour))
	if env.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatal("the hourly sweep is the safety net")
	}
}

func TestTheSweepRetriesACreationThatFailedAndRespectsTheCadence(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	env.fake.FailNext(byok.OpCreateDueCharge, byok.ErrUnavailable)
	inv := env.finalizeInvoice(t, 1000)
	env.runJob(t, now().Add(10*time.Minute))
	if env.attempt(t, inv).Payable == "" {
		t.Fatal("the sweep completes a charge whose creation failed")
	}
	// The job partition holds other tests' connections too (this env's fake
	// bank answers for all of them), so the cadence is read on the attempt.
	env.runJob(t, now().Add(20*time.Minute))
	checked := env.attempt(t, inv).CheckedAt
	env.runJob(t, now().Add(30*time.Minute))
	if got := env.attempt(t, inv).CheckedAt; !got.Equal(checked) {
		t.Fatalf("a charge checked within the hour is not re-queried: %s → %s", checked, got)
	}
	env.runJob(t, now().Add(90*time.Minute))
	if got := env.attempt(t, inv).CheckedAt; got.Equal(checked) {
		t.Fatal("an hour later it is")
	}
}

func TestTheDailyAuditMarksAnOverwrittenWebhook(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	env.fake.SetWebhookURL(byok.ProductPix, "https://erp.example.com/pix")
	env.runJob(t, saoPaulo(2026, 3, 10, 5, 30))
	if env.connection(t).WebhookStatus != byokconn.WebhookRegistered {
		t.Fatal("not before 06:00")
	}
	env.runJob(t, saoPaulo(2026, 3, 10, 6, 30))
	if env.connection(t).WebhookStatus != byokconn.WebhookOverwritten {
		t.Fatal("overwritten")
	}
}

func TestTheAuditRequeriesChargesWhoseCallbackFailed(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.registerWebhook(t)
	inv := env.finalizeInvoice(t, 1000)
	ref := env.chargeRefOf(inv)
	env.fake.Pay(ref, 1000, 0, now())
	env.fake.AddCallback(byok.CallbackAttempt{ChargeRef: ref, HTTPStatus: 503, Attempt: 1, At: saoPaulo(2026, 3, 10, 3, 0)})
	env.runJob(t, saoPaulo(2026, 3, 10, 6, 30))
	if env.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatal("re-queried at once")
	}
}

func TestRetryCallbacksAsksTheProviderInBatches(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	for i := range 60 {
		env.fake.AddCallback(byok.CallbackAttempt{ChargeRef: "ctb" + string(rune('A'+i%26)) + string(rune('A'+i/26)), HTTPStatus: 500, At: now()})
	}
	slept := 0
	n, err := env.sweep().RetryCallbacks(ctxT(t), env.connection(t), now().Add(-time.Hour), func(time.Duration) { slept++ })
	if err != nil || n != 60 || env.fake.Calls(byok.OpRetryCallbacks) != 2 || slept != 1 {
		t.Fatalf("%d %v calls %d slept %d", n, err, env.fake.Calls(byok.OpRetryCallbacks), slept)
	}
}

func TestTheFirstTestRunRegistersTheSandboxWebhook(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	base := repositories.NewBase(testDB, testCfg, repositories.TableByok)
	_, _ = base.DeleteItem(ctxT(t), "HOOK#sandbox", "HOOK")
	env.fake.SetWebhookURL(byok.ProductPix, "")
	if err := env.sweep().Bootstrap(ctxT(t), env.connection(t), now()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.repo.GetHook(ctxT(t), "sandbox"); err != nil {
		t.Fatal("the sandbox hook row exists")
	}
	if env.fake.WebhookURL(byok.ProductPix) == "" {
		t.Fatal("registered on the sandbox key")
	}
}
