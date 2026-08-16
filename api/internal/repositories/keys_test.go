package repositories

import (
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestEveryTenantKeyStartsWithTheTenant(t *testing.T) {
	// The property ADR 0003 exists for: a query without a tenant must not be
	// expressible. Every partition key builder is checked here so a new one
	// cannot quietly break it.
	const org = "org_123"
	prefix := org + "#live"

	keys := map[string]string{
		"TenantPK":                  TenantPK(org, true),
		"UsagePK":                   UsagePK(org, true, "si_1", brcal.New(2026, time.March, 1)),
		"LookupCustomerRefPK":       LookupCustomerRefPK(org, true, "ref"),
		"LookupInvoiceGenerationPK": LookupInvoiceGenerationPK(org, true, "si_1:2026-03-01"),
		"PeriodPK":                  PeriodPK(org, true, EntityInvoice),
	}
	for name, key := range keys {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			t.Errorf("%s = %q, must start with %q", name, key, prefix)
		}
	}
}

func TestModeSeparatesPartitions(t *testing.T) {
	if TenantPK("org_1", true) == TenantPK("org_1", false) {
		t.Fatal("live and test must be different partitions, not a flag on the row")
	}
	if Mode(true) != "live" || Mode(false) != "test" {
		t.Fatal("mode rendering changed; every existing key depends on it")
	}
}

// TestSchedulePKIsTheOnlyCrossTenantKey documents the single deliberate
// exception, so it is a decision in a test rather than an oversight in a file.
func TestSchedulePKIsTheOnlyCrossTenantKey(t *testing.T) {
	key := SchedulePK(true, JobSubscriptionDue, brcal.New(2026, time.March, 10))
	if key != "live#SUB_DUE#2026-03-10" {
		t.Fatalf("SchedulePK = %q", key)
	}
	if SchedulePK(true, JobSubscriptionDue, brcal.New(2026, time.March, 10)) ==
		SchedulePK(false, JobSubscriptionDue, brcal.New(2026, time.March, 10)) {
		t.Fatal("a test sweep must not be able to reach live rows")
	}
}

func TestPeriodSortKeyIsZeroPaddedAndPrefixQueryable(t *testing.T) {
	// Without padding, "3" sorts after "12" and every month query silently
	// returns the wrong range.
	attrs := NewPeriodAttrs("org_1", true, EntityInvoice, brcal.New(2026, time.March, 5), "in_x")
	if attrs.PeriodSK != "2026#03#05#in_x" {
		t.Fatalf("period_sk = %q", attrs.PeriodSK)
	}

	// Ordering.
	march := NewPeriodAttrs("org_1", true, EntityInvoice, brcal.New(2026, time.March, 5), "a").PeriodSK
	december := NewPeriodAttrs("org_1", true, EntityInvoice, brcal.New(2026, time.December, 5), "a").PeriodSK
	twentieth := NewPeriodAttrs("org_1", true, EntityInvoice, brcal.New(2026, time.March, 20), "a").PeriodSK
	if !(march < december) {
		t.Fatal("March must sort before December")
	}
	if !(march < twentieth) {
		t.Fatal("the 5th must sort before the 20th")
	}

	// A query prefix must match what a write produced — this is the property that
	// makes one constructor and one prefix renderer non-negotiable.
	for _, prefix := range []string{
		PeriodPrefix(2026, 0, 0),
		PeriodPrefix(2026, 3, 0),
		PeriodPrefix(2026, 3, 5),
	} {
		if !strings.HasPrefix(attrs.PeriodSK, prefix) {
			t.Fatalf("%q does not match the row's key %q", prefix, attrs.PeriodSK)
		}
	}
	// And a neighbouring month must not match.
	if strings.HasPrefix(attrs.PeriodSK, PeriodPrefix(2026, 4, 0)) {
		t.Fatal("an April prefix matched a March row")
	}
}

func TestPeriodPKSeparatesEntities(t *testing.T) {
	if PeriodPK("org_1", true, EntityInvoice) == PeriodPK("org_1", true, EntityAudit) {
		t.Fatal("a period query must not mix entity types")
	}
}

func TestSortKeysAreOrderedWithinTheirParent(t *testing.T) {
	// Attempts and lines are zero-padded so attempt 2 sorts before attempt 10.
	if !(PaymentAttemptSK("in_1", 2) < PaymentAttemptSK("in_1", 10)) {
		t.Fatal("payment attempts must sort numerically")
	}
	if !(InvoiceItemSK("in_1", 2) < InvoiceItemSK("in_1", 10)) {
		t.Fatal("invoice lines must sort numerically")
	}
	// Lines and attempts live under their invoice, so one Query with a prefix
	// returns the invoice's whole detail view.
	if got := InvoiceItemSK("in_1", 0); got[:len(InvoiceSK("in_1"))] != InvoiceSK("in_1") {
		t.Fatalf("%q is not nested under its invoice", got)
	}
}

func TestTenantOf(t *testing.T) {
	cases := []struct {
		pk       string
		org      string
		livemode bool
		ok       bool
	}{
		{TenantPK("org_1", true), "org_1", true, true},
		{TenantPK("org_1", false), "org_1", false, true},
		{UsagePK("org_1", true, "si_1", brcal.New(2026, time.March, 1)), "org_1", true, true},
		{"garbage", "", false, false},
		{"org_1#staging", "", false, false},
		{"#live", "", false, false},
	}
	for _, tc := range cases {
		org, livemode, ok := TenantOf(tc.pk)
		if org != tc.org || livemode != tc.livemode || ok != tc.ok {
			t.Errorf("TenantOf(%q) = (%q, %v, %v), want (%q, %v, %v)", tc.pk, org, livemode, ok, tc.org, tc.livemode, tc.ok)
		}
	}
}

func TestInvoiceCounterIsPerYear(t *testing.T) {
	if InvoiceCounterSK(2026) == InvoiceCounterSK(2027) {
		t.Fatal("numbering restarts per year, so the counters must differ")
	}
	if InvoiceCounterSK(2026) != "COUNTER#INVOICE#2026" {
		t.Fatalf("got %q", InvoiceCounterSK(2026))
	}
}
