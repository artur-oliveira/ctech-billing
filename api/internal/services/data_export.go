package services

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	Read(ctx context.Context, sp space.ResolvedSpace) (map[string]*repositories.SpaceRows, error)
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

// Part is the dataexport.PartFunc of billing. Idempotent: a redelivery builds it again.
func (e *DataExporter) Part(ctx context.Context, sub string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	var files []string
	var truncated []string
	write := func(name string, v any) error {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		files = append(files, name)
		return nil
	}

	for _, live := range []bool{true, false} {
		if e.portalOrg != "" {
			if v, err := e.customerView(ctx, sub, live); err != nil {
				return nil, err
			} else if v != nil {
				if err := write("customer-"+modeName(live)+".json", v); err != nil {
					return nil, err
				}
			}
		}
		sp, err := e.spaceFor("USER#"+sub, live)
		if err != nil {
			continue // a sub that cannot name a space never had one
		}
		rows, err := e.spaces.Read(ctx, sp)
		if err != nil {
			return nil, fmt.Errorf("export: reading space %s: %w", sp.PK(), err)
		}
		tables := make([]string, 0, len(rows))
		for t := range rows {
			tables = append(tables, t)
		}
		sort.Strings(tables)
		for _, t := range tables {
			if len(rows[t].Items) == 0 {
				continue
			}
			name := "finance-" + modeName(live) + "/" + t + ".json"
			if err := write(name, map[string]any{"table": t, "truncated": rows[t].Truncated, "items": rows[t].Items}); err != nil {
				return nil, err
			}
			if rows[t].Truncated {
				truncated = append(truncated, name)
			}
		}
	}

	w, err := zw.Create("README.txt")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(e.readme(files, truncated))); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
