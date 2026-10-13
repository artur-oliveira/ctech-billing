//go:build integration

package integration

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func (e *collectEnv) subscribeMerchantTo(t *testing.T, plan string) *billing.Subscription {
	t.Helper()
	return e.merchant.subscribeTo(t, plan)
}

func monthOf(at time.Time) string { return brcal.FromTime(at).String()[:7] }

// preloadChargesIn sets the tenant's charge counter of a month directly.
func (e *collectEnv) preloadChargesIn(t *testing.T, month string, n int64) {
	t.Helper()
	pk, sk := repositories.ChargeCounterKey(e.tenant.ID, month)
	if _, err := testDB.PutItem(ctxT(t), &dynamodb.PutItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableByok)),
		Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk},
			"n": &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}},
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *collectEnv) preloadCharges(t *testing.T, n int64) { e.preloadChargesIn(t, monthOf(now()), n) }

func (e *collectEnv) chargesThisMonth(t *testing.T) int64 {
	t.Helper()
	n, err := e.repo.ChargesThisMonth(ctxT(t), e.tenant.ID, monthOf(now()))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// voidAt withdraws the invoice's open charge at a given time (the console's
// void, without its wall clock).
func (e *collectEnv) voidAt(t *testing.T, inv *billing.Invoice, at time.Time) {
	t.Helper()
	if _, err := e.byok.Voided(ctxT(t), inv, at); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 1: fifty-one charges at once on Grátis, exactly fifty exist.
func TestGratisNeverPassesFifty(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, "free")
	env.preloadCharges(t, 40)
	before := env.fake.Calls(byok.OpCreateDueCharge)
	var wg sync.WaitGroup
	for range 11 {
		wg.Add(1)
		go func() { defer wg.Done(); env.finalizeInvoice(t, 100) }()
	}
	wg.Wait()
	if n := env.chargesThisMonth(t); n != 50 {
		t.Fatalf("counter %d, want 50", n)
	}
	if n := env.fake.Calls(byok.OpCreateDueCharge) - before; n != 10 {
		t.Fatalf("%d provider charges, want 10", n)
	}
	// The console's "Gerar novo Pix" runs on the wall clock's month.
	env.preloadChargesIn(t, monthOf(time.Now()), 50)
	r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+env.finalizeInvoice(t, 100).ID+"/charge", "{}")
	if r.code() != "byok_quota_exceeded" || !strings.Contains(string(r.body), `"upgrade_plan":true`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// Review Focus 3: a paid plan never refuses.
func TestAPaidPlanNeverRefuses(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, "basic")
	env.preloadCharges(t, 600)
	if a := env.attempt(t, env.finalizeInvoice(t, 100)); a.Payable == "" {
		t.Fatal("charge 601 on Básico is created")
	}
	if n := env.chargesThisMonth(t); n != 601 {
		t.Fatalf("counter %d", n)
	}
}

func TestACancellationWithin24HoursGivesTheUnitBack(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, "free")
	inv := env.finalizeInvoice(t, 100)
	if n := env.chargesThisMonth(t); n != 1 {
		t.Fatalf("after creation %d", n)
	}
	env.voidAt(t, inv, now().Add(23*time.Hour))
	if n := env.chargesThisMonth(t); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestACancellationAfter24HoursKeepsTheUnit(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	env.subscribeMerchantTo(t, "free")
	inv := env.finalizeInvoice(t, 100)
	env.voidAt(t, inv, now().Add(25*time.Hour))
	if n := env.chargesThisMonth(t); n != 1 {
		t.Fatalf("%d", n)
	}
}

// Review Focus 5: test mode counts nothing.
func TestInTestModeNothingCounts(t *testing.T) {
	env := newCollectEnv(t, collectTest)
	env.finalizeInvoice(t, 100)
	if n := env.chargesThisMonth(t); n != 0 {
		t.Fatalf("%d", n)
	}
}
