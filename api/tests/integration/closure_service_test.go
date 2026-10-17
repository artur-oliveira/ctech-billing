//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/closure"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

type closureFakes struct {
	owner                 bool
	notOwnerNow           bool // the live check says the requester lost the role
	tenants               []*billing.Organization
	subs                  int64
	rawConfirm, rawCancel string
	blockedMail           []string
	cancelledMail         int
	purged                int
}

func (f *closureFakes) IsOwner(context.Context, string, string) (bool, error) {
	return !f.notOwnerNow, nil
}
func (f *closureFakes) TenantsOf(context.Context, string) ([]*billing.Organization, error) {
	return f.tenants, nil
}
func (f *closureFakes) ActiveSubscriptions(context.Context, string) (int64, error) {
	return f.subs, nil
}
func (f *closureFakes) PaymentsInFlight(context.Context, string) (int64, error)  { return 0, nil }
func (f *closureFakes) PayoutsProcessing(context.Context, string) (int64, error) { return 0, nil }
func (f *closureFakes) RefundsOpen(context.Context, string) (int64, error)       { return 0, nil }
func (f *closureFakes) SendClosureConfirm(_ context.Context, _, _, token string) error {
	f.rawConfirm = token
	return nil
}
func (f *closureFakes) SendClosureScheduled(_ context.Context, _, _, token string, _ time.Time) error {
	f.rawCancel = token
	return nil
}
func (f *closureFakes) SendClosureBlocked(_ context.Context, _, _, token string, blockers []string) error {
	f.rawCancel, f.blockedMail = token, blockers
	return nil
}
func (f *closureFakes) SendClosureCancelled(context.Context, string) error {
	f.cancelledMail++
	return nil
}
func (f *closureFakes) Purge(context.Context, *closure.Closure, []*billing.Organization) error {
	f.purged++
	return nil
}

func newClosureService(t *testing.T) (*services.TenantClosures, *closureFakes, *repositories.ClosureRepository, *time.Time) {
	t.Helper()
	live := &billing.Organization{ID: "ten_" + newSpaceOrgID(), Livemode: true}
	test := &billing.Organization{ID: "ten_" + newSpaceOrgID(), Livemode: false}
	f := &closureFakes{owner: true, tenants: []*billing.Organization{live, test}}
	repo := repositories.NewClosureRepository(testDB, testCfg)
	clock := seedNow
	svc := services.NewTenantClosures(repo, f, f, f, "ten_zero").WithOwners(f).WithClock(func() time.Time { return clock })
	return svc, f, repo, &clock
}

func TestOnlyAnOwnerCanRequestAClosureAndTenantZeroNever(t *testing.T) {
	ctx := ctxT(t)
	svc, f, _, _ := newClosureService(t)
	f.owner = false
	if _, err := svc.Request(ctx, "usr_x", newSpaceOrgID(), f.owner); !errors.Is(err, closure.ErrNotOwner) {
		t.Fatalf("not an owner: %v", err)
	}
	f.owner = true
	f.tenants[0].ID = "ten_zero"
	if _, err := svc.Request(ctx, "usr_x", newSpaceOrgID(), f.owner); !errors.Is(err, closure.ErrTenantZero) {
		t.Fatalf("tenant zero: %v, want ErrTenantZero", err)
	}
}

func TestActiveSubscriptionsBlockTheRequestButATestTenantDoesNot(t *testing.T) {
	ctx := ctxT(t)
	svc, f, _, _ := newClosureService(t)
	f.subs = 3
	_, err := svc.Request(ctx, "usr_x", newSpaceOrgID(), f.owner)
	var blocked *services.ClosureBlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) != 1 || blocked.Blockers[0].Code != services.BlockerClosureSubscriptions || blocked.Blockers[0].Count != 3 {
		t.Fatalf("err = %v, want one subscriptions blocker of 3 (live tenant only)", err)
	}
}

func TestTheWholeLifecycleWithAReadOnlyGrace(t *testing.T) {
	ctx := ctxT(t)
	svc, f, repo, clock := newClosureService(t)
	acct := newSpaceOrgID()
	c, err := svc.Request(ctx, "usr_x", acct, true)
	if err != nil || f.rawConfirm == "" {
		t.Fatalf("request: %v token %q", err, f.rawConfirm)
	}
	for _, tn := range f.tenants {
		if on, _ := repo.ReadOnly(ctx, tn.ID); on {
			t.Fatal("nothing is read-only before the confirmation")
		}
	}
	if _, err := svc.Confirm(ctx, c.ID, "wrong"); !errors.Is(err, closure.ErrInvalidToken) {
		t.Fatalf("a wrong token: %v", err)
	}
	*clock = clock.Add(time.Hour)
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); err != nil {
		t.Fatal(err)
	}
	for _, tn := range f.tenants {
		if on, _ := repo.ReadOnly(ctx, tn.ID); !on {
			t.Fatalf("tenant %s must be read-only during the grace", tn.ID)
		}
	}
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); err != nil { // a second click repairs, never fails
		t.Fatalf("second click: %v", err)
	}

	svc.ProcessDue(ctx) // grace not over: nothing happens
	if got, _ := svc.Status(ctx, acct); got.State != closure.Closing {
		t.Fatalf("state = %s, want closing", got.State)
	}

	*clock = clock.Add(closure.GracePeriod)
	svc.ProcessDue(ctx)
	if got, _ := svc.Status(ctx, acct); got.State != closure.ReadyToPurge {
		t.Fatalf("after grace: %s, want ready_to_purge (no purger wired, so it waits)", got.State)
	}
	if on, _ := repo.ReadOnly(ctx, f.tenants[0].ID); !on {
		t.Fatal("still read-only while it waits for the purge")
	}

	if _, err := svc.Cancel(ctx, c.ID, f.rawCancel); err != nil {
		t.Fatal(err)
	}
	if on, _ := repo.ReadOnly(ctx, f.tenants[0].ID); on {
		t.Fatal("a cancel lifts the read-only flag")
	}
	if _, err := svc.Status(ctx, acct); !errors.Is(err, closure.ErrNotFound) {
		t.Fatalf("after cancel: %v, want no open closure", err)
	}
	if f.cancelledMail != 1 {
		t.Fatalf("cancelled e-mails = %d", f.cancelledMail)
	}
}

func TestABlockerAtTheEndOfGraceBlocksTheClosureAndMailsTheOwner(t *testing.T) {
	ctx := ctxT(t)
	svc, f, _, clock := newClosureService(t)
	acct := newSpaceOrgID()
	c, _ := svc.Request(ctx, "usr_x", acct, true)
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); err != nil {
		t.Fatal(err)
	}
	f.subs = 2 // a customer subscribed during the grace
	*clock = clock.Add(closure.GracePeriod)
	svc.ProcessDue(ctx)
	got, _ := svc.Status(ctx, acct)
	if got.State != closure.Blocked || len(got.Blockers) != 1 || len(f.blockedMail) != 1 {
		t.Fatalf("state %s blockers %v mail %v", got.State, got.Blockers, f.blockedMail)
	}
	if _, err := svc.Cancel(ctx, c.ID, f.rawCancel); err != nil {
		t.Fatalf("cancel from the blocked e-mail: %v", err)
	}
}

func TestAnUnconfirmedRequestExpiresAndTheLinkStopsWorking(t *testing.T) {
	ctx := ctxT(t)
	svc, f, _, clock := newClosureService(t)
	acct := newSpaceOrgID()
	c, _ := svc.Request(ctx, "usr_x", acct, true)
	*clock = clock.Add(closure.ConfirmWindow + time.Minute)
	svc.ProcessDue(ctx)
	if _, err := svc.Status(ctx, acct); !errors.Is(err, closure.ErrNotFound) {
		t.Fatalf("expired request still open: %v", err)
	}
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); err == nil {
		t.Fatal("an expired request must not confirm")
	}
}

func TestAReadyClosureIsPurgedOnceWhenAPurgerIsWired(t *testing.T) {
	ctx := ctxT(t)
	svc, f, _, clock := newClosureService(t)
	svc = svc.WithPurger(f)
	acct := newSpaceOrgID()
	c, _ := svc.Request(ctx, "usr_x", acct, true)
	_, _ = svc.Confirm(ctx, c.ID, f.rawConfirm)
	*clock = clock.Add(closure.GracePeriod)
	svc.ProcessDue(ctx) // grace over -> ready
	svc.ProcessDue(ctx) // ready -> purging -> closed
	svc.ProcessDue(ctx)
	if f.purged != 1 {
		t.Fatalf("purge ran %d times, want once", f.purged)
	}
	if _, err := svc.Status(ctx, acct); !errors.Is(err, closure.ErrNotFound) {
		t.Fatalf("a closed closure holds no open marker: %v", err)
	}
}

// The whole closure over real tenants: apply (creates the test tenant, the
// application and the customer), request, confirm, grace, purge, closed.
func TestAClosureErasesTheTenantsTheApplicationAndLeavesATombstone(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+newSpaceOrgID(), newSpaceOrgID()
	apps := appRepo(t, tz)
	if _, _, err := apply(t, apps, tz, acct, true); err != nil {
		t.Fatal(err)
	}
	orgs := repositories.NewOrganizationRepository(testDB, testCfg)
	test, err := orgs.GetByAccountOrganization(ctx, acct, false)
	if err != nil {
		t.Fatalf("the application must create a test tenant: %v", err)
	}
	f := &closureFakes{owner: true}
	repo := repositories.NewClosureRepository(testDB, testCfg)
	clock := seedNow
	cfg := *testCfg
	cfg.PortalOrganizationID = tz
	svc := services.NewTenantClosures(repo, services.NewOrganizationTenants(orgs), f, f, tz).
		WithClock(func() time.Time { return clock }).
		WithPurger(services.NewTenantPurge(repositories.NewTenantPurger(testDB, &cfg), repositories.NewApplicationRepository(testDB, &cfg), repo))

	c, err := svc.Request(ctx, "usr_x", acct, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); err != nil {
		t.Fatal(err)
	}
	if on, _ := repo.ReadOnly(ctx, test.ID); !on {
		t.Fatal("the test tenant must be read-only during the grace")
	}
	clock = clock.Add(closure.GracePeriod)
	svc.ProcessDue(ctx) // ready
	svc.ProcessDue(ctx) // purge

	if _, err := orgs.GetByAccountOrganization(ctx, acct, false); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("the tenant row survived: %v (billing.merchant_tenant would still block)", err)
	}
	if cur, _ := apps.Current(ctx, acct); cur != nil {
		t.Fatalf("the application survived: %+v", cur)
	}
	if closed, err := repo.Closed(ctx, test.ID); err != nil || !closed {
		t.Fatalf("tombstone: %v %v", closed, err)
	}
	if on, _ := repo.ReadOnly(ctx, test.ID); on {
		t.Fatal("the flag must be lifted once the tenant is gone")
	}
	if got, err := repo.Get(ctx, c.ID); err != nil || got.State != closure.Closed {
		t.Fatalf("closure = %+v, %v", got, err)
	}
}

func TestTheClosureFactsCountWhatBlocks(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	facts := services.NewClosureFacts(repositories.NewSubscriptionRepository(testDB, testCfg), repositories.NewPaymentRepository(testDB, testCfg))
	other := newPortal(t)

	if n, err := facts.PaymentsInFlight(ctx, e.org.ID); err != nil || n != 0 {
		t.Fatalf("before: %d %v", n, err)
	}
	pk := repositories.TenantPK(e.org.ID, true)
	putRaw(t, "invoices", map[string]string{"pk": pk, "sk": "INVOICE#inv_1#ATTEMPT#0001", "status": "PENDING"})
	putRaw(t, "invoices", map[string]string{"pk": pk, "sk": "INVOICE#inv_1#ATTEMPT#0002", "status": "SUCCEEDED"})
	putRaw(t, "invoices", map[string]string{"pk": pk, "sk": "INVOICE#inv_2", "status": "OPEN"})
	if n, err := facts.PaymentsInFlight(ctx, e.org.ID); err != nil || n != 1 {
		t.Fatalf("in flight = %d, %v; want exactly the PENDING attempt", n, err)
	}
	if n, _ := facts.PaymentsInFlight(ctx, other.org.ID); n != 0 {
		t.Fatalf("another tenant's attempts counted: %d", n)
	}

	newSubscriptionIn(t, e.org, e.customer.ID, e.priceID, billing.SubscriptionActive)
	if n, err := facts.ActiveSubscriptions(ctx, e.org.ID); err != nil || n < 1 {
		t.Fatalf("active subscriptions = %d, %v", n, err)
	}
	if n, err := facts.PayoutsProcessing(ctx, e.org.ID); err != nil || n != 0 {
		t.Fatalf("payouts = %d, %v", n, err)
	}
	if n, err := facts.RefundsOpen(ctx, e.org.ID); err != nil || n != 0 {
		t.Fatalf("refunds = %d, %v", n, err)
	}
}

// The role the console resolved can be a minute stale: the request, the
// confirmation and the purge each ask ctech-account again.
func TestALiveOwnerCheckGuardsTheRequestTheConfirmationAndThePurge(t *testing.T) {
	ctx := ctxT(t)

	svc, f, _, _ := newClosureService(t)
	f.notOwnerNow = true
	if _, err := svc.Request(ctx, "usr_x", newSpaceOrgID(), true); !errors.Is(err, closure.ErrNotOwner) {
		t.Fatalf("request with a stale owner role: %v, want ErrNotOwner", err)
	}

	svc, f, _, _ = newClosureService(t)
	acct := newSpaceOrgID()
	c, err := svc.Request(ctx, "usr_x", acct, true)
	if err != nil {
		t.Fatal(err)
	}
	f.notOwnerNow = true // demoted after asking
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); !errors.Is(err, closure.ErrNotOwner) {
		t.Fatalf("confirm after losing the role: %v, want ErrNotOwner", err)
	}

	svc, f, repo, clock := newClosureService(t)
	svc = svc.WithPurger(f)
	acct = newSpaceOrgID()
	c, _ = svc.Request(ctx, "usr_x", acct, true)
	if _, err := svc.Confirm(ctx, c.ID, f.rawConfirm); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(closure.GracePeriod)
	svc.ProcessDue(ctx)  // grace over -> ready
	f.notOwnerNow = true // demoted during the grace
	svc.ProcessDue(ctx)  // would purge
	if f.purged != 0 {
		t.Fatal("the purge ran for a requester who is no longer an owner")
	}
	if _, err := svc.Status(ctx, acct); !errors.Is(err, closure.ErrNotFound) {
		t.Fatalf("the closure must be cancelled, not left open: %v", err)
	}
	if on, _ := repo.ReadOnly(ctx, f.tenants[0].ID); on {
		t.Fatal("a cancelled closure lifts the read-only flag")
	}
}
