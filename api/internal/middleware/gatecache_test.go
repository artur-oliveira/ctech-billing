package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

type countingOrgs struct {
	reads int
	err   error
	org   billing.Organization
}

func (c *countingOrgs) Get(context.Context, string, bool) (*billing.Organization, error) {
	c.reads++
	if c.err != nil {
		return nil, c.err
	}
	o := c.org
	return &o, nil
}

// The suspension gate reads the tenant once per window per tenant and mode, an
// error is never remembered, and a caller cannot change what the next one gets.
func TestCachedTenantRowsReadsOncePerWindowAndHandsOutCopies(t *testing.T) {
	inner := &countingOrgs{org: billing.Organization{ID: "org_1"}}
	rows := CachedTenantRows(inner, time.Minute)
	for range 20 {
		o, err := rows.Get(context.Background(), "org_1", true)
		if err != nil || o.ID != "org_1" {
			t.Fatalf("Get = %+v, %v", o, err)
		}
		o.ID = "changed by a handler"
	}
	if inner.reads != 1 {
		t.Fatalf("%d reads for 20 requests, want 1", inner.reads)
	}
	if o, _ := rows.Get(context.Background(), "org_1", true); o.ID != "org_1" {
		t.Fatalf("a handler's change leaked into the cache: %q", o.ID)
	}
	rows.Get(context.Background(), "org_1", false) // the other mode is its own entry
	if inner.reads != 2 {
		t.Fatalf("test mode shared live mode's entry: %d reads", inner.reads)
	}

	failing := &countingOrgs{err: errors.New("dynamodb down")}
	frows := CachedTenantRows(failing, time.Minute)
	for range 3 {
		if _, err := frows.Get(context.Background(), "org_1", true); err == nil {
			t.Fatal("an outage was swallowed")
		}
	}
	if failing.reads != 3 {
		t.Fatalf("an error was remembered: %d reads for 3 failing requests", failing.reads)
	}
}

type countingFlags struct {
	reads int
	err   error
	ro    bool
}

func (c *countingFlags) ReadOnly(context.Context, string) (bool, error) {
	c.reads++
	return c.ro, c.err
}

func TestCachedClosingFlagsReadsOncePerWindowAndKeepsNilNil(t *testing.T) {
	inner := &countingFlags{ro: true}
	flags := CachedClosingFlags(inner, time.Minute)
	for range 10 {
		if ro, err := flags.ReadOnly(context.Background(), "org_1"); err != nil || !ro {
			t.Fatalf("ReadOnly = %v, %v", ro, err)
		}
	}
	if inner.reads != 1 {
		t.Fatalf("%d reads for 10 requests, want 1", inner.reads)
	}
	failing := &countingFlags{err: errors.New("down")}
	cf := CachedClosingFlags(failing, time.Minute)
	cf.ReadOnly(context.Background(), "org_1")
	cf.ReadOnly(context.Background(), "org_1")
	if failing.reads != 2 {
		t.Fatalf("an error was remembered: %d reads", failing.reads)
	}
	// The feature off (nil) must stay nil: RefuseClosingWrites tests for it.
	if CachedClosingFlags(nil, time.Minute) != nil {
		t.Fatal("a nil ClosingFlags became non-nil")
	}
	// A zero ttl is a passthrough.
	if CachedClosingFlags(inner, 0) != ClosingFlags(inner) {
		t.Fatal("ttl 0 did not return the inner flags")
	}
}
