//go:build integration

package integration

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// capsEnv is a live BYOK merchant selling its own monthly price.
type capsEnv struct {
	*collectEnv
	price      *billing.Price
	subs       *repositories.SubscriptionRepository
	subscriber *services.Subscriber
}

func newCapsEnv(t *testing.T, plan string) *capsEnv {
	t.Helper()
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, plan)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	product := &billing.Product{ID: id.NewWithPrefix(id.PrefixProduct), OrganizationID: env.tenant.ID, Livemode: true, Name: "Mensalidade", Active: true}
	if err := cat.CreateProduct(ctxT(t), product, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	price := &billing.Price{ID: id.NewWithPrefix(id.PrefixPrice), OrganizationID: env.tenant.ID, Livemode: true, ProductID: product.ID,
		Type: billing.PriceFixed, Currency: billing.CurrencyBRL, UnitAmount: 5000,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1}, Timing: billing.BillAdvance}
	if err := cat.CreatePrice(ctxT(t), price, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	plans := services.NewBillingPlans(repositories.NewCustomerRepository(testDB, testCfg), hookedSubs(env.tz), cat, env.tz)
	return &capsEnv{collectEnv: env, price: price, subs: subs,
		subscriber: services.NewSubscriber(subs, cat, env.invoicer).WithByokCaps(env.orgs, plans)}
}

func (e *capsEnv) subscribeCustomer(t *testing.T) (*billing.Subscription, error) {
	t.Helper()
	sub, _, err := e.subscriber.Subscribe(ctxT(t), services.SubscribeInput{OrganizationID: e.tenant.ID, Livemode: true,
		CustomerID: e.customer.ID, Items: []services.SubscribeItem{{PriceID: e.price.ID}}, Actor: "test"}, now())
	return sub, err
}

func (e *capsEnv) preloadSubscriptions(t *testing.T, n int64) {
	t.Helper()
	pk, sk := repositories.SubscriptionCounterKey(e.tenant.ID)
	if _, err := testDB.PutItem(ctxT(t), &dynamodb.PutItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableByok)),
		Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk},
			"n": &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}},
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *capsEnv) activeSubscriptions(t *testing.T) int64 {
	t.Helper()
	n, err := e.repo.SubscriptionCount(ctxT(t), e.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Review Focus 4: never more active subscriptions than the quota.
func TestTheSubscriptionCapHoldsUnderConcurrency(t *testing.T) {
	env := newCapsEnv(t, "free") // 50
	env.preloadSubscriptions(t, 48)
	var wg sync.WaitGroup
	results := make(chan error, 5)
	created := make(chan *billing.Subscription, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, err := env.subscribeCustomer(t)
			results <- err
			if err == nil {
				created <- sub
			}
		}()
	}
	wg.Wait()
	close(results)
	close(created)
	refused := 0
	for err := range results {
		var le *billing.LimitError
		switch {
		case errors.As(err, &le) && errors.Is(err, billing.ErrSubscriptionLimitReached) && le.Quota == 50:
			refused++
		case err != nil && !errors.Is(err, repositories.ErrTransactionConflict):
			t.Fatalf("unexpected: %v", err)
		}
	}
	if n := env.activeSubscriptions(t); n != 50 || refused < 3 {
		t.Fatalf("refused %d, active %d", refused, n)
	}
	sub := <-created
	if _, err := env.subs.Transition(ctxT(t), sub, billing.SubscriptionCanceled, billing.CauseManual, "test", "", now()); err != nil {
		t.Fatal(err)
	}
	if n := env.activeSubscriptions(t); n != 49 {
		t.Fatalf("a cancellation frees a slot: %d", n)
	}
	if _, err := env.subscribeCustomer(t); err != nil {
		t.Fatalf("a slot freed by a cancellation: %v", err)
	}
}

// A paid plan caps subscriptions at its charge quota too (spec § 12.5);
// unlimited counts without refusing.
func TestUnlimitedPlansCountWithoutRefusing(t *testing.T) {
	env := newCapsEnv(t, "early_access")
	env.preloadSubscriptions(t, 100000)
	if _, err := env.subscribeCustomer(t); err != nil {
		t.Fatal(err)
	}
	if n := env.activeSubscriptions(t); n != 100001 {
		t.Fatalf("%d", n)
	}
}

// Moving onto the BYOK rail counts the subscriptions the tenant already has.
func TestTheSwitchToByokCountsExistingSubscriptions(t *testing.T) {
	env := newCapsEnv(t, "free")
	ctx := ctxT(t)
	if err := env.orgs.SetPayoutRail(ctx, env.tenantNow(t), billing.RailWallet, "", billing.PayoutEnabled, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := env.subscribeCustomer(t); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := env.subscribeCustomer(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.subs.Transition(ctx, sub, billing.SubscriptionCanceled, billing.CauseManual, "test", "", now()); err != nil {
		t.Fatal(err)
	}
	if n := env.activeSubscriptions(t); n != 0 {
		t.Fatalf("the wallet rail counts nothing: %d", n)
	}
	if err := env.setup().WithSubscriptions(env.subs).UseForCollection(ctx, env.sp, env.tenantNow(t), "inter", billing.CollectionSettings{},
		"user:test", "req", now()); err != nil {
		t.Fatal(err)
	}
	if n := env.activeSubscriptions(t); n != 3 {
		t.Fatalf("%d, want 3", n)
	}
}

// Grátis has one bank connection: a second provider is refused.
func TestASecondProviderPastTheQuotaIsRefused(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, "free")
	other := &byokconn.Connection{AccountOrganizationID: env.accountOrg, Livemode: true, Provider: "other", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, Version: 1, Sealed: "b1.fake",
		Capabilities: []byok.Capability{byok.CapabilityStatement}}
	if err := env.repo.CreateConnection(ctxT(t), other, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	r := env.connectLive(t, "s")
	if r.status != 409 || r.code() != "bank_connection_limit_reached" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestAPlanWithRoomConnectsASecondProvider(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, "growth") // 2 connections
	other := &byokconn.Connection{AccountOrganizationID: env.accountOrg, Livemode: true, Provider: "other", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, Version: 1, Sealed: "b1.fake",
		Capabilities: []byok.Capability{byok.CapabilityStatement}}
	if err := env.repo.CreateConnection(ctxT(t), other, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	if r := env.connectLive(t, "s"); r.code() == "bank_connection_limit_reached" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// The connection cap is checked against a count read before the bank call; a
// connection created meanwhile moves the guard, so the stale create loses
// instead of passing the cap.
func TestAConnectionCheckedAgainstAStaleCountLoses(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	ctx := ctxT(t)
	read, err := env.repo.ConnectionGuard(ctx, env.accountOrg, true)
	if err != nil {
		t.Fatal(err)
	}
	conn := func(provider string) *byokconn.Connection {
		return &byokconn.Connection{AccountOrganizationID: env.accountOrg, Livemode: true, Provider: provider, ID: id.New(),
			Kind: byokconn.KindMerchant, State: byokconn.StateActive, Version: 1, Sealed: "b1.fake",
			Capabilities: []byok.Capability{byok.CapabilityStatement}}
	}
	if err := env.repo.CreateConnection(ctx, conn("other"), "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	if err := env.repo.CreateConnection(ctx, conn("third"), "u", "r", now(), read); !errors.Is(err, repositories.ErrTransactionConflict) {
		t.Fatalf("a stale guard must lose: %v", err)
	}
	if fresh, err := env.repo.ConnectionGuard(ctx, env.accountOrg, true); err != nil || fresh != read+1 {
		t.Fatalf("%d %v", fresh, err)
	}
	if err := env.repo.CreateConnection(ctx, conn("third"), "u", "r", now(), read+1); err != nil {
		t.Fatalf("a fresh guard passes: %v", err)
	}
}

// A tenant switched to BYOK before the counter existed has no counter row:
// the first subscription counts the open ones instead of starting at zero.
func TestAMissingSubscriptionCounterIsRebuilt(t *testing.T) {
	env := newCapsEnv(t, "free") // 50
	for range 3 {
		if _, err := env.subscribeCustomer(t); err != nil {
			t.Fatal(err)
		}
	}
	pk, sk := repositories.SubscriptionCounterKey(env.tenant.ID)
	if _, err := testDB.DeleteItem(ctxT(t), &dynamodb.DeleteItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableByok)),
		Key:       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.subscribeCustomer(t); err != nil {
		t.Fatal(err)
	}
	if n := env.activeSubscriptions(t); n != 4 {
		t.Fatalf("%d, want 4", n)
	}
}
