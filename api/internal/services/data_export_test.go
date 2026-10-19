package services

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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

func (s *expSpaces) Stream(_ context.Context, sp space.ResolvedSpace, sink repositories.TableSink) error {
	s.read = append(s.read, sp.PK())
	if !strings.Contains(sp.PK(), "alice") {
		return nil
	}
	// A table with no rows is never begun, which is why it makes no file.
	if err := sink.Begin("bills"); err != nil {
		return err
	}
	if err := sink.Item(map[string]any{"title": "rent"}); err != nil {
		return err
	}
	return sink.End(false)
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

func TestDataExporterWritePart(t *testing.T) {
	spaces := &expSpaces{}
	e := NewDataExporter(expCustomers{}, expSubs{}, expInvoices{}, spaces, "tenant0", space.ForJob)
	files := unzip(t, mustPart(t, e, "alice"))
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
	var buf bytes.Buffer
	if err := e.WritePart(context.Background(), sub, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func keys(m map[string]string) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	return k
}

// manySpaces streams n rows of one table and ends it as cut or not.
type manySpaces struct {
	n         int
	truncated bool
}

func (m manySpaces) Stream(_ context.Context, sp space.ResolvedSpace, sink repositories.TableSink) error {
	if !strings.Contains(sp.PK(), "big") || !sp.Livemode() {
		return nil
	}
	if err := sink.Begin("ledger_transactions"); err != nil {
		return err
	}
	for i := 0; i < m.n; i++ {
		if err := sink.Item(map[string]any{"id": i, "memo": "line, with \"quotes\" and\nnewline"}); err != nil {
			return err
		}
	}
	return sink.End(m.truncated)
}

// A table streamed row by row is one valid JSON document: {table, items, truncated}.
// Commas, quotes and newlines inside rows must not break it.
func TestStreamedTableIsValidJSONWithItsTruncatedFlag(t *testing.T) {
	for _, cut := range []bool{false, true} {
		e := NewDataExporter(expCustomers{}, expSubs{}, expInvoices{}, manySpaces{n: 5000, truncated: cut}, "", space.ForJob)
		files := unzip(t, mustPart(t, e, "big"))
		var doc struct {
			Table     string           `json:"table"`
			Items     []map[string]any `json:"items"`
			Truncated bool             `json:"truncated"`
		}
		var name string
		for n := range files {
			if strings.HasSuffix(n, "/ledger_transactions.json") {
				name = n
			}
		}
		if name == "" {
			t.Fatalf("no ledger file in %v", keys(files))
		}
		if err := json.Unmarshal([]byte(files[name]), &doc); err != nil {
			t.Fatalf("the streamed table is not valid JSON: %v", err)
		}
		if doc.Table != "ledger_transactions" || len(doc.Items) != 5000 || doc.Truncated != cut {
			t.Fatalf("table=%q items=%d truncated=%v, want 5000 rows and truncated=%v", doc.Table, len(doc.Items), doc.Truncated, cut)
		}
		if got := doc.Items[4999]["memo"]; got != "line, with \"quotes\" and\nnewline" {
			t.Fatalf("a row was altered by the stream: %q", got)
		}
		if cut && !strings.Contains(files["README.txt"], "cut at the size limit") {
			t.Fatal("the README does not say the table was cut")
		}
	}
}
