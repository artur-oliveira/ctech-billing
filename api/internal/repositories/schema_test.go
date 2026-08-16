package repositories

import (
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// schema.json is read by two things that cannot check each other at compile
// time: this package embeds it, and terraform/billing/dynamodb.tf jsondecodes
// it. There is no drift to test for — it is one file — but there is still a way
// to write a schema that is internally wrong, and these tests are that check.

// allTables is every logical table the Go code addresses. A constant missing
// here is a table nothing would notice was absent from schema.json.
var allTables = []string{
	TableOrganizations,
	TableCredentials,
	TableCustomers,
	TableProducts,
	TablePrices,
	TableSubscriptions,
	TableInvoices,
	TableUsage,
	TableAudit,
	TableWebhooks,
	TableIdempotency,
}

func TestEveryTableConstantHasASchema(t *testing.T) {
	for _, name := range allTables {
		if _, ok := Schemas()[name]; !ok {
			t.Errorf("table %q is a Go constant with no entry in schema.json", name)
		}
	}
	for name := range Schemas() {
		if !slices.Contains(allTables, name) {
			t.Errorf("table %q is in schema.json but no Go constant names it — nothing can read or write it", name)
		}
	}
}

// TestEveryKeyAttributeIsDeclared catches the mistake DynamoDB reports at
// CreateTable time and Terraform reports at apply time: a key that names an
// attribute the table never declares. Finding it here costs a second.
func TestEveryKeyAttributeIsDeclared(t *testing.T) {
	for name, s := range Schemas() {
		declared := func(attr, where string) {
			if attr == "" {
				return
			}
			if _, ok := s.Attributes[attr]; !ok {
				t.Errorf("%s: %s names attribute %q, which is not declared", name, where, attr)
			}
		}
		declared(s.HashKey, "the table key")
		declared(s.RangeKey, "the table key")
		for _, idx := range s.Indexes {
			declared(idx.HashKey, idx.Name)
			declared(idx.RangeKey, idx.Name)
		}
	}
}

// TestNoAttributeIsDeclaredWithoutAKeyUsingIt is the other direction, and
// DynamoDB rejects it outright: an AttributeDefinition no key schema
// references. It is worth a test because the natural way to add an index is to
// add the attributes first, and then not add the index.
func TestNoAttributeIsDeclaredWithoutAKeyUsingIt(t *testing.T) {
	for name, s := range Schemas() {
		used := map[string]bool{s.HashKey: true}
		if s.RangeKey != "" {
			used[s.RangeKey] = true
		}
		for _, idx := range s.Indexes {
			used[idx.HashKey] = true
			if idx.RangeKey != "" {
				used[idx.RangeKey] = true
			}
		}
		for attr := range s.Attributes {
			if !used[attr] {
				t.Errorf("%s declares attribute %q that no key uses — DynamoDB rejects the table", name, attr)
			}
		}
	}
}

// TestIndexNamesAreTheOnesTheQueriesUse pins the seam between the schema and
// the query code. A table that declares "period_index" while every query asks
// for "period-index" creates an index nothing reads, and the queries fall back
// to nothing at all — a runtime error, after a deploy.
func TestIndexNamesAreTheOnesTheQueriesUse(t *testing.T) {
	known := []string{IndexPeriod, IndexSchedule, IndexLookup}
	seen := map[string]bool{}
	for name, s := range Schemas() {
		for _, idx := range s.Indexes {
			if !slices.Contains(known, idx.Name) {
				t.Errorf("%s declares index %q, which no query code names", name, idx.Name)
			}
			seen[idx.Name] = true
		}
	}
	for _, want := range known {
		if !seen[want] {
			t.Errorf("index %q is named by the query code but declared on no table", want)
		}
	}
}

// TestTheSweepIndexIsOnlyWhereASweepReads guards ADR 0002's one asymmetry.
// schedule-index is the single key that does not start with a tenant, so every
// table carrying it is a table a cross-tenant job can read. Two tables have a
// sweep; a third acquiring one silently is exactly the change that deserves to
// be noticed in review.
func TestTheSweepIndexIsOnlyWhereASweepReads(t *testing.T) {
	var withSweep []string
	for name, s := range Schemas() {
		for _, idx := range s.Indexes {
			if idx.Name == IndexSchedule {
				withSweep = append(withSweep, name)
			}
		}
	}
	slices.Sort(withSweep)
	// `webhooks` is the third, added with outbound delivery (ADR 0016). Its two
	// queues are read by cmd/deliver, which is cross-tenant for the same reason
	// the sweep is: a delivery backlog spans every tenant that has one, and the
	// job holds no credential to scope it by.
	want := []string{TableInvoices, TableSubscriptions, TableWebhooks}
	slices.Sort(want)
	if !slices.Equal(withSweep, want) {
		t.Errorf("tables with %s: got %v, want %v", IndexSchedule, withSweep, want)
	}
}

func TestTableDefinitionRendersEveryTable(t *testing.T) {
	for _, name := range allTables {
		in, err := TableDefinition(name, "test_"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if aws.ToString(in.TableName) != "test_"+name {
			t.Errorf("%s: physical name is %q", name, aws.ToString(in.TableName))
		}
		if len(in.KeySchema) == 0 {
			t.Errorf("%s: no key schema", name)
		}
		if len(in.AttributeDefinitions) != len(Schemas()[name].Attributes) {
			t.Errorf("%s: %d attribute definitions for %d declared attributes",
				name, len(in.AttributeDefinitions), len(Schemas()[name].Attributes))
		}
	}
}

func TestTableDefinitionRejectsAnUnknownTable(t *testing.T) {
	if _, err := TableDefinition("nope", "x_nope"); err == nil {
		t.Fatal("expected an error for a table that is not in schema.json")
	}
}

func TestPhysicalNameFollowsTheCompanyStandard(t *testing.T) {
	if got := PhysicalName("prod_billing", TableInvoices); got != "prod_billing_invoices" {
		t.Errorf("got %q, want prod_billing_invoices", got)
	}
}
