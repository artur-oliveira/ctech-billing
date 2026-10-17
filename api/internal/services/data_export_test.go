package services

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

type expCustomers struct{}

func (expCustomers) GetByUser(_ context.Context, _ string, live bool, user string) (*billing.Customer, error) {
	if user != "alice" || !live {
		return nil, repositories.ErrNotFound
	}
	return &billing.Customer{ID: "cus_1", Name: "Alice", Email: "alice@example.com", TaxID: "12345678901"}, nil
}

type expSubs struct{}

func (expSubs) ListByCustomer(context.Context, string, bool, string, int) ([]billing.Subscription, error) {
	return []billing.Subscription{{ID: "sub_1"}}, nil
}

type expInvoices struct{}

func (expInvoices) ListByCustomer(context.Context, string, bool, string, int) ([]billing.Invoice, error) {
	return []billing.Invoice{{ID: "in_1"}}, nil
}

type expSpaces struct{ read []string }

func (s *expSpaces) Read(_ context.Context, sp space.ResolvedSpace) (map[string]*repositories.SpaceRows, error) {
	s.read = append(s.read, sp.PK())
	if !strings.Contains(sp.PK(), "alice") {
		return nil, nil
	}
	return map[string]*repositories.SpaceRows{
		"bills":           {Items: []map[string]any{{"title": "rent"}}},
		"ledger_accounts": {},
	}, nil
}

func unzip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		out[f.Name] = string(b)
	}
	return out
}

func TestDataExporterPart(t *testing.T) {
	spaces := &expSpaces{}
	e := NewDataExporter(expCustomers{}, expSubs{}, expInvoices{}, spaces, "tenant0", space.ForJob)
	data, err := e.Part(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	files := unzip(t, data)
	cust := files["customer-live.json"]
	if !strings.Contains(cust, "alice@example.com") || !strings.Contains(cust, "12345678901") || !strings.Contains(cust, "sub_1") || !strings.Contains(cust, "in_1") {
		t.Errorf("customer file: %s", cust)
	}
	if _, ok := files["customer-test.json"]; ok {
		t.Error("no test-mode customer exists: no file")
	}
	if !strings.Contains(files["finance-live/bills.json"], "rent") || !strings.Contains(files["finance-test/bills.json"], "rent") {
		t.Errorf("space files missing: %v", keys(files))
	}
	if _, ok := files["finance-live/ledger_accounts.json"]; ok {
		t.Error("an empty table must not produce a file")
	}
	if len(spaces.read) != 2 {
		t.Errorf("both modes of the personal space should be read: %v", spaces.read)
	}
	for _, p := range spaces.read {
		if !strings.Contains(p, "USER#alice") && !strings.Contains(p, "alice") {
			t.Errorf("only the person's own space may be read, got %s", p)
		}
	}
	if !strings.Contains(files["README.txt"], "customer-{live,test}.json") {
		t.Error("README missing")
	}
}

func TestDataExporterForAPersonWithNothing(t *testing.T) {
	e := NewDataExporter(expCustomers{}, expSubs{}, expInvoices{}, &expSpaces{}, "tenant0", space.ForJob)
	files := unzip(t, mustPart(t, e, "nobody"))
	if len(files) != 1 || files["README.txt"] == "" {
		t.Fatalf("files=%v", keys(files))
	}
}

func mustPart(t *testing.T, e *DataExporter, sub string) []byte {
	t.Helper()
	b, err := e.Part(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func keys(m map[string]string) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	return k
}
