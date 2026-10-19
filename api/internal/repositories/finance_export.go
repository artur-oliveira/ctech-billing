package repositories

import (
	"context"
	"fmt"
	"slices"
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
// result says so. The export streams, so the bound no longer protects memory; it bounds the time (one
// delivery of the queue message) and the size of the spooled zip.
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

// exportPage is how many rows one round trip asks for (1000 small rows are far
// under the 1 MB response cap).
const exportPage = 1000

// eachPage pages through pk's rows under skPrefix and hands each page to fn, stopping
// once limit rows were read, so the cap bounds the work and fn never holds more than
// one page. truncated says it stopped early.
func eachPage(ctx context.Context, b Base, pk, skPrefix string, limit int, fn func([]map[string]types.AttributeValue) error) (truncated bool, err error) {
	var start map[string]types.AttributeValue
	for limit > 0 {
		res, err := b.Query(ctx, QueryOpts{
			PK: pk, SKPrefix: skPrefix, ScanIndexForward: true, Limit: min(limit, exportPage), ExclusiveStartKey: start, ConsistentRead: true,
		})
		if err != nil {
			return false, err
		}
		if err := fn(res.Items); err != nil {
			return false, err
		}
		limit -= len(res.Items)
		if len(res.LastEvaluatedKey) == 0 {
			return false, nil
		}
		start = res.LastEvaluatedKey
	}
	return true, nil
}

// queryBounded is eachPage collected into a slice, for the small lists (a space's
// chart, its card and import ids) the walk needs to know before it streams.
func queryBounded(ctx context.Context, b Base, pk, skPrefix string, limit int) (items []map[string]types.AttributeValue, truncated bool, err error) {
	truncated, err = eachPage(ctx, b, pk, skPrefix, limit, func(page []map[string]types.AttributeValue) error {
		items = append(items, page...)
		return nil
	})
	return items, truncated, err
}

// TableSink receives the rows of a space one table at a time, in table order. Begin
// is called at a table's first row (a table with no rows is never begun), Item for
// each row, End when the table is done. A zip entry can only be written
// sequentially, which is why the walk groups a table's partitions together.
type TableSink interface {
	Begin(table string) error
	Item(item map[string]any) error
	End(truncated bool) error
}

type exportPartition struct{ pk, prefix string }

// plan lists, per table, the partitions of the space to read: the child
// partitions (ledger entries per account, card and import partitions) and then
// the space's own. Only ids are kept in memory, never rows; the transaction
// headers are paged through just to learn which accounts they touch.
func (r *SpaceReader) plan(ctx context.Context, sp space.ResolvedSpace) (map[string][]exportPartition, error) {
	s := sp.PK()
	parts := map[string][]exportPartition{}

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
	if _, err := eachPage(ctx, r.bases[TableLedgerTransactions], s, "TX#", r.limit(), func(page []map[string]types.AttributeValue) error {
		txs, err := DecodeItems[txItem](page)
		if err != nil {
			return err
		}
		for _, tx := range txs {
			for _, leg := range tx.Legs {
				accounts[leg.Account] = true
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for account := range accounts {
		parts[TableLedgerTransactions] = append(parts[TableLedgerTransactions], exportPartition{LedgerEntryPK(sp, account), ""})
	}
	// A stable order, so two runs of the same export give the same file.
	slices.SortFunc(parts[TableLedgerTransactions], func(a, b exportPartition) int { return strings.Compare(a.pk, b.pk) })

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
			parts[c.table] = append(parts[c.table], exportPartition{pk, ""})
		}
	}
	for _, t := range exportedFinanceTables {
		parts[t] = append(parts[t], exportPartition{s, ""})
	}
	return parts, nil
}

// Stream walks the space table by table (in name order) and feeds sink, holding at
// most one page of rows at a time: the memory of an export no longer grows with
// the person's data. Each table is capped at the reader's limit across all its
// partitions.
func (r *SpaceReader) Stream(ctx context.Context, sp space.ResolvedSpace, sink TableSink) error {
	if sp.IsZero() {
		return space.ErrNoSpace
	}
	parts, err := r.plan(ctx, sp)
	if err != nil {
		return err
	}
	tables := slices.Clone(exportedFinanceTables)
	slices.Sort(tables)
	for _, table := range tables {
		count, begun, truncated := 0, false, false
		for _, p := range parts[table] {
			remaining := r.limit() - count
			if remaining <= 0 {
				truncated = true
				break
			}
			cut, err := eachPage(ctx, r.bases[table], p.pk, p.prefix, remaining, func(page []map[string]types.AttributeValue) error {
				for _, it := range page {
					var m map[string]any
					if err := attributevalue.UnmarshalMap(it, &m); err != nil {
						return fmt.Errorf("export: decode %s: %w", table, err)
					}
					if !begun {
						if err := sink.Begin(table); err != nil {
							return err
						}
						begun = true
					}
					if err := sink.Item(ScrubSecrets(m).(map[string]any)); err != nil {
						return err
					}
					count++
				}
				return nil
			})
			if err != nil {
				return err
			}
			truncated = truncated || cut
		}
		if begun {
			if err := sink.End(truncated); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectSink gathers a stream into memory, for tests and small callers.
type collectSink struct {
	rows map[string]*SpaceRows
	cur  *SpaceRows
}

func (c *collectSink) Begin(table string) error {
	c.cur = &SpaceRows{}
	c.rows[table] = c.cur
	return nil
}
func (c *collectSink) Item(m map[string]any) error { c.cur.Items = append(c.cur.Items, m); return nil }
func (c *collectSink) End(truncated bool) error    { c.cur.Truncated = truncated; return nil }

// Read returns the rows of sp per logical table, omitting tables with none, all in
// memory. It is Stream collected: the export itself uses Stream.
func (r *SpaceReader) Read(ctx context.Context, sp space.ResolvedSpace) (map[string]*SpaceRows, error) {
	c := &collectSink{rows: map[string]*SpaceRows{}}
	if err := r.Stream(ctx, sp, c); err != nil {
		return nil, err
	}
	return c.rows, nil
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
