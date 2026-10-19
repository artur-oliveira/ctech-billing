package services

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ExportCustomers, ExportSubscriptions, ExportInvoices and ExportSpaces are what
// DataExporter reads. The repositories satisfy them.
type ExportCustomers interface {
	GetByUser(ctx context.Context, organizationID string, livemode bool, userID string) (*billing.Customer, error)
}

type ExportSubscriptions interface {
	ListByCustomer(ctx context.Context, organizationID string, livemode bool, customerID string, limit int) ([]billing.Subscription, error)
}

type ExportInvoices interface {
	ListByCustomer(ctx context.Context, organizationID string, livemode bool, customerID string, limit int) ([]billing.Invoice, error)
}

type ExportSpaces interface {
	Stream(ctx context.Context, sp space.ResolvedSpace, sink repositories.TableSink) error
}

// DataExporter builds billing's part of the personal-data export (LGPD art. 18,
// V; ctech-account docs/specs/2026-10-17-data-export.md): what CTech Billing
// holds about the person as a data subject.
//
//   - the person as a customer of CTech itself (tenant zero): customer record,
//     subscriptions and invoices, live and test mode;
//   - their personal finance space: ledger, bills, recurrences, cards, imports
//     and the space's audit trail, live and test mode.
//
// Not included: workspaces' data (the workspace's, and a workspace with a merchant
// tenant blocks deletion anyway), other people's data (a customer of a
// merchant is the merchant's), and credentials. Every table is classified by a
// test (repositories.TestEveryTableIsClassifiedForTheExport).
type DataExporter struct {
	customers ExportCustomers
	subs      ExportSubscriptions
	invoices  ExportInvoices
	spaces    ExportSpaces
	portalOrg string
	spaceFor  func(owner string, livemode bool) (space.ResolvedSpace, error)
	now       func() time.Time
}

func NewDataExporter(c ExportCustomers, s ExportSubscriptions, i ExportInvoices, sp ExportSpaces, portalOrganizationID string,
	spaceFor func(owner string, livemode bool) (space.ResolvedSpace, error)) *DataExporter {
	return &DataExporter{customers: c, subs: s, invoices: i, spaces: sp, portalOrg: portalOrganizationID, spaceFor: spaceFor, now: time.Now}
}

// WritePart is the dataexport.PartStreamFunc of billing: it writes the zip to w as
// it reads, one page of rows at a time, so a person with a large ledger no longer
// costs that much memory inside the API process. Idempotent: a redelivery writes
// it again.
func (e *DataExporter) WritePart(ctx context.Context, sub string, w io.Writer) error {
	zw := zip.NewWriter(w)
	var files, truncated []string

	for _, live := range []bool{true, false} {
		if e.portalOrg != "" {
			if v, err := e.customerView(ctx, sub, live); err != nil {
				return err
			} else if v != nil {
				name := "customer-" + modeName(live) + ".json"
				if err := writeJSONFile(zw, name, v); err != nil {
					return err
				}
				files = append(files, name)
			}
		}
		sp, err := e.spaceFor("USER#"+sub, live)
		if err != nil {
			continue // a sub that cannot name a space never had one
		}
		sink := &zipTableSink{zw: zw, dir: "finance-" + modeName(live) + "/"}
		if err := e.spaces.Stream(ctx, sp, sink); err != nil {
			return fmt.Errorf("export: reading space %s: %w", sp.PK(), err)
		}
		files = append(files, sink.files...)
		truncated = append(truncated, sink.truncated...)
	}

	rw, err := zw.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err := rw.Write([]byte(e.readme(files, truncated))); err != nil {
		return err
	}
	return zw.Close()
}

// writeJSONFile writes one small value as an indented JSON file.
func writeJSONFile(zw *zip.Writer, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// zipTableSink writes each table of a space as {"table","items":[...],"truncated"}
// into one zip entry, a row at a time. The entry cannot be rewritten, so what the
// table turned out to be (cut or not) is the last field.
type zipTableSink struct {
	zw        *zip.Writer
	dir       string
	w         io.Writer
	table     string
	n         int
	files     []string
	truncated []string
}

func (s *zipTableSink) Begin(table string) error {
	name := s.dir + table + ".json"
	w, err := s.zw.Create(name)
	if err != nil {
		return err
	}
	s.w, s.table, s.n = w, table, 0
	s.files = append(s.files, name)
	_, err = io.WriteString(w, `{"table":`+strconv.Quote(table)+`,"items":[`)
	return err
}

func (s *zipTableSink) Item(item map[string]any) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	sep := "\n"
	if s.n > 0 {
		sep = ",\n"
	}
	s.n++
	if _, err := io.WriteString(s.w, sep); err != nil {
		return err
	}
	_, err = s.w.Write(data)
	return err
}

func (s *zipTableSink) End(truncated bool) error {
	if truncated {
		s.truncated = append(s.truncated, s.dir+s.table+".json")
	}
	_, err := io.WriteString(s.w, "\n],\"truncated\":"+strconv.FormatBool(truncated)+"}\n")
	return err
}

// customerView is the person as a customer of CTech: the record (with their own
// tax id, which the API never shows in full), subscriptions and invoices.
func (e *DataExporter) customerView(ctx context.Context, sub string, live bool) (map[string]any, error) {
	c, err := e.customers.GetByUser(ctx, e.portalOrg, live, sub)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	subs, err := e.subs.ListByCustomer(ctx, e.portalOrg, live, c.ID, 0)
	if err != nil {
		return nil, err
	}
	invoices, err := e.invoices.ListByCustomer(ctx, e.portalOrg, live, c.ID, 0)
	if err != nil {
		return nil, err
	}
	return map[string]any{"customer": c, "tax_id": c.TaxID, "subscriptions": subs, "invoices": invoices}, nil
}

func (e *DataExporter) readme(files, truncated []string) string {
	var b strings.Builder
	b.WriteString("CTech Billing — a copy of your personal data\nGenerated at " + e.now().UTC().Format(time.RFC3339) + "\n\nFiles:\n")
	for _, f := range files {
		b.WriteString("  " + f + "\n")
	}
	b.WriteString("\ncustomer-{live,test}.json: you as a customer of CTech: your record (including your tax id), subscriptions and invoices.\n")
	b.WriteString("finance-{live,test}/*.json: your personal finance space, one file per table: ledger accounts and transactions, bills,\nrecurrences, cards, imports and the space's audit trail. Each file is {table, truncated, items}.\n")
	b.WriteString("\"live\" is real money, \"test\" is sandbox data.\n")
	b.WriteString("Not included: data of workspaces (it belongs to the workspace), data of customers of merchants (the merchant's), bank connection credentials.\n")
	if len(truncated) > 0 {
		b.WriteString("\nThese files were cut at the size limit: " + strings.Join(truncated, ", ") + "\n")
	}
	return b.String()
}
