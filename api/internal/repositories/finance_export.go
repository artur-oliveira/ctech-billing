package repositories

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// exportedFinanceTables are the tables of a finance space that go into the
// person's personal-data export (ctech-account docs/specs/2026-10-17-data-export.md),
// and notExported the ones that do not, with the reason. Like the purge's
// classification, TestEveryTableIsClassifiedForTheExport makes a new table choose.
var exportedFinanceTables = []string{
	TableLedgerAccounts, TableLedgerTransactions, TableBills, TableRecurrences, TableCards, TableImports, TableAudit,
}

var notExported = map[string]string{
	TableByok:           "bank connections exist only in workspaces (a personal space is refused one) and hold credentials and hook secrets",
	TableIdempotency:    "request keys and cached responses, no data of the person's own",
	TableOrganizations:  "merchant tenants belong to the workspace, not the person",
	TableCredentials:    "merchant tenant API credentials",
	TableCustomers:      "exported by services.DataExporter (the person as a customer of CTech), not by space",
	TableProducts:       "catalogue, no person",
	TablePrices:         "catalogue, no person",
	TableSubscriptions:  "exported by services.DataExporter with the customer",
	TableInvoices:       "exported by services.DataExporter with the customer",
	TableUsage:          "level counters and usage records without the person's own content",
	TableWebhooks:       "merchant tenant endpoints",
	TableErasureState:   "the deletion saga's own state",
	TableTenantClosures: "merchant tenant closure, not a person",
}

// exportMaxItems bounds one table of one space, across all its partitions. A ledger past it is cut and the
// result says so.
// ponytail: 200k rows covers any personal space; stream pages to the part's zip
// if one ever outgrows memory.
const exportMaxItems = 200_000

// SpaceReader reads a finance space for the personal-data export. Read-only: the
// ledger has no edit path and the export is not one.
type SpaceReader struct {
	bases map[string]Base
	max   int
}

// WithLimit lowers the per-table cap (tests; the default is exportMaxItems).
func (r *SpaceReader) WithLimit(n int) *SpaceReader { r.max = n; return r }

func (r *SpaceReader) limit() int {
	if r.max > 0 {
		return r.max
	}
	return exportMaxItems
}

func NewSpaceReader(db *dynamodb.Client, cfg *config.Config) *SpaceReader {
	bases := map[string]Base{}
	for _, t := range exportedFinanceTables {
		bases[t] = NewBase(db, cfg, t)
	}
	return &SpaceReader{bases: bases}
}

// SpaceRows is one table's rows of a space, as plain maps, and whether the read
// stopped at exportMaxItems.
type SpaceRows struct {
	Items     []map[string]any
	Truncated bool
}

// queryBounded is queryPrefix that stops paging once limit rows are read, so
// the cap bounds memory, not just the output. truncated says it stopped early.
func queryBounded(ctx context.Context, b Base, pk, skPrefix string, limit int) (items []map[string]types.AttributeValue, truncated bool, err error) {
	var start map[string]types.AttributeValue
	for limit > 0 {
		res, err := b.Query(ctx, QueryOpts{
			PK: pk, SKPrefix: skPrefix, ScanIndexForward: true, Limit: min(limit, 100), ExclusiveStartKey: start, ConsistentRead: true,
		})
		if err != nil {
			return nil, false, err
		}
		items = append(items, res.Items...)
		limit -= len(res.Items)
		if len(res.LastEvaluatedKey) == 0 {
			return items, false, nil
		}
		start = res.LastEvaluatedKey
	}
	return items, true, nil
}

// readInto adds one partition's rows to table, within the table's budget of
// exportMaxItems across every partition of the space.
func (r *SpaceReader) readInto(ctx context.Context, rows map[string]*SpaceRows, table, pk, prefix string) error {
	out := rows[table]
	if out == nil {
		out = &SpaceRows{}
		rows[table] = out
	}
	remaining := r.limit() - len(out.Items)
	if remaining <= 0 {
		out.Truncated = true
		return nil
	}
	got, truncated, err := queryBounded(ctx, r.bases[table], pk, prefix, remaining)
	if err != nil {
		return err
	}
	out.Truncated = out.Truncated || truncated
	for _, it := range got {
		var m map[string]any
		if err := attributevalue.UnmarshalMap(it, &m); err != nil {
			return fmt.Errorf("export: decode %s: %w", table, err)
		}
		out.Items = append(out.Items, ScrubSecrets(m).(map[string]any))
	}
	return nil
}

// Read returns the rows of sp per logical table, omitting tables with none. It
// walks the same child partitions PurgeSpace does (ledger entries, card and
// import partitions).
func (r *SpaceReader) Read(ctx context.Context, sp space.ResolvedSpace) (map[string]*SpaceRows, error) {
	if sp.IsZero() {
		return nil, space.ErrNoSpace
	}
	rows := map[string]*SpaceRows{}
	s := sp.PK()

	accounts := map[string]bool{}
	chart, _, err := queryBounded(ctx, r.bases[TableLedgerAccounts], s, "ACCOUNT#", r.limit())
	if err != nil {
		return nil, err
	}
	for _, it := range chart {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			accounts[strings.TrimPrefix(sk.Value, "ACCOUNT#")] = true
		}
	}
	headers, _, err := queryBounded(ctx, r.bases[TableLedgerTransactions], s, "TX#", r.limit())
	if err != nil {
		return nil, err
	}
	txs, err := DecodeItems[txItem](headers)
	if err != nil {
		return nil, err
	}
	for _, tx := range txs {
		for _, leg := range tx.Legs {
			accounts[leg.Account] = true
		}
	}
	for account := range accounts {
		if err := r.readInto(ctx, rows, TableLedgerTransactions, LedgerEntryPK(sp, account), ""); err != nil {
			return nil, err
		}
	}
	for _, c := range []struct{ table, prefix string }{{TableCards, "CARD#"}, {TableImports, "IMPORT#"}} {
		ids, _, err := queryBounded(ctx, r.bases[c.table], s, c.prefix, r.limit())
		if err != nil {
			return nil, err
		}
		for _, it := range ids {
			sk, ok := it["sk"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			id := strings.TrimPrefix(sk.Value, c.prefix)
			pk := CardPK(sp, id)
			if c.table == TableImports {
				pk = ImportPK(sp, id)
			}
			if err := r.readInto(ctx, rows, c.table, pk, ""); err != nil {
				return nil, err
			}
		}
	}
	for _, t := range exportedFinanceTables {
		if err := r.readInto(ctx, rows, t, s, ""); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// secretFragments name attributes that never leave in an export, at any depth: a
// defensive net over the raw rows (hook secrets, tokens, credentials).
var secretFragments = []string{"password", "secret", "token", "credential", "api_key", "cpf_hash"}

// ScrubSecrets returns v without attributes whose name contains a secret fragment.
func ScrubSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			lk := strings.ToLower(k)
			drop := false
			for _, f := range secretFragments {
				if strings.Contains(lk, f) {
					drop = true
					break
				}
			}
			if !drop {
				out[k] = ScrubSecrets(e)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = ScrubSecrets(e)
		}
		return out
	}
	return v
}
