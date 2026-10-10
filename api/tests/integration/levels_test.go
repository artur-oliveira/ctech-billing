//go:build integration

package integration

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func level(org string, v int64, at time.Time, key string) *billing.LevelRecord {
	return &billing.LevelRecord{OrganizationID: org, Livemode: true, CustomerRef: "USER_" + org, Meter: "finance_spaces",
		Value: v, OccurredAt: at, IdempotencyKey: key}
}

func TestALevelReportedTwiceIsOneRecord(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2026, time.March, 5, 14, 3, 0, 0, time.UTC)
	if err := r.Append(ctx, level(org, 4, at, "k1"), now()); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(ctx, level(org, 4, at, "k1"), now()); !errors.Is(err, repositories.ErrDuplicateLevel) {
		t.Fatalf("retry: %v", err)
	}
	got, _ := r.InPeriod(ctx, org, true, "USER_"+org, "finance_spaces", at.Add(-time.Hour), at.Add(time.Hour))
	if len(got) != 1 {
		t.Fatalf("%d records", len(got))
	}
}

// Review Focus 1.
func TestALevelKeyReusedWithAnotherInstantIsRefused(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2026, time.March, 5, 14, 3, 0, 0, time.UTC)
	if err := r.Append(ctx, level(org, 4, at, "k1"), now()); err != nil {
		t.Fatal(err)
	}
	for _, other := range []*billing.LevelRecord{level(org, 4, at.Add(time.Minute), "k1"), level(org, 5, at, "k1")} {
		if err := r.Append(ctx, other, now()); !errors.Is(err, repositories.ErrLevelKeyReused) {
			t.Fatalf("reuse: %v", err)
		}
	}
	got, _ := r.InPeriod(ctx, org, true, "USER_"+org, "finance_spaces", at.Add(-time.Hour), at.Add(time.Hour))
	if len(got) != 1 || got[0].Value != 4 {
		t.Fatalf("records = %+v", got)
	}
}

func TestLatestBeforeIsStrictlyBefore(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	start := time.Date(2026, time.March, 1, 3, 0, 0, 0, time.UTC) // midnight in São Paulo
	_ = r.Append(ctx, level(org, 2, start.Add(-48*time.Hour), "a"), now())
	_ = r.Append(ctx, level(org, 3, start.Add(-time.Hour), "b"), now())
	_ = r.Append(ctx, level(org, 9, start, "c"), now())
	got, err := r.LatestBefore(ctx, org, true, "USER_"+org, "finance_spaces", start)
	if err != nil || got == nil || got.Value != 3 {
		t.Fatalf("latest = %+v, %v", got, err)
	}
	none, err := r.LatestBefore(ctx, org, true, "USER_"+org, "finance_people", start)
	if err != nil || none != nil {
		t.Fatalf("another meter = %+v, %v", none, err)
	}
	in, _ := r.InPeriod(ctx, org, true, "USER_"+org, "finance_spaces", start, start.AddDate(0, 1, 0))
	if len(in) != 1 || in[0].Value != 9 {
		t.Fatalf("in period = %+v", in)
	}
}

// The latest item has no TTL: with every report gone (simulating expiry), the
// level is still known, and each report knows the level before it.
func TestTheLatestLevelOutlivesItsReports(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2025, time.January, 5, 14, 0, 0, 0, time.UTC)
	_ = r.Append(ctx, level(org, 2, at, "a"), now())
	_ = r.Append(ctx, level(org, 4, at.Add(time.Hour), "b"), now())
	_ = r.Append(ctx, level(org, 3, at.Add(-time.Hour), "late"), now()) // late: does not move the latest
	b, _ := r.FirstFrom(ctx, org, true, "USER_"+org, "finance_spaces", at.Add(time.Minute))
	if b == nil || b.Value != 4 || b.Previous != 2 {
		t.Fatalf("b = %+v", b)
	}
	base := repositories.NewBase(testDB, testCfg, repositories.TableUsage)
	for _, k := range []struct {
		at  time.Time
		key string
	}{{at, "a"}, {at.Add(time.Hour), "b"}, {at.Add(-time.Hour), "late"}} {
		if _, err := base.DeleteItem(ctx, repositories.LevelPK(org, true, "USER_"+org, "finance_spaces"), repositories.LevelSK(k.at, k.key)); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := r.Latest(ctx, org, true, "USER_"+org, "finance_spaces")
	if err != nil || latest == nil || latest.Value != 4 {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
}

func TestConcurrentReportsLeaveTheNewestAsLatest(t *testing.T) {
	ctx, r, org := ctxT(t), repositories.NewLevelRepository(testDB, testCfg), "org_"+id.New()
	at := time.Date(2026, time.March, 5, 14, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 1; i <= 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.Append(ctx, level(org, int64(i), at.Add(time.Duration(i)*time.Minute), "k"+strconv.Itoa(i)), now())
		}(i)
	}
	wg.Wait()
	if latest, _ := r.Latest(ctx, org, true, "USER_"+org, "finance_spaces"); latest == nil || latest.Value != 5 {
		t.Fatalf("latest = %+v", latest)
	}
}
