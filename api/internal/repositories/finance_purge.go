package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// financeTables are the tables whose rows live under a finance space key S
// (spec § 4, 6.5, 6.6, BYOK § 12.2). PurgeSpace clears each one's S partition,
// and the child partitions below. TestEveryTableIsClassifiedForThePurge makes a
// new table choose a side.
var financeTables = []string{
	TableLedgerAccounts, TableLedgerTransactions, TableBills, TableRecurrences,
	TableCards, TableImports, TableByok,
}

// batchDeleteAttempts bounds the retries of a BatchWriteItem's unprocessed
// keys. Running out is an error. The saga redelivers, and the purge restarts
// from a fresh read.
const batchDeleteAttempts = 5

// attrHookID is the connection row's pointer to its HOOK#{id} partition.
const attrHookID = "hook_id"

// SpacePurger deletes a finance space (ADR 0026): a personal space when its
// owner's CTech account is deleted, any workspace's space when the workspace
// is erased with its only member.
//
// It is not a LedgerRepository method on purpose. The ledger has no edit path
// (TestLedgerRepositoryHasNoEditPath), and erasure is not an edit. It is the
// end of the space.
type SpacePurger struct {
	bases map[string]Base
}

func NewSpacePurger(db *dynamodb.Client, cfg *config.Config) *SpacePurger {
	bases := map[string]Base{}
	for _, t := range append([]string{TableAudit, TableIdempotency}, financeTables...) {
		bases[t] = NewBase(db, cfg, t)
	}
	return &SpacePurger{bases: bases}
}

// PurgeSpace deletes every row of sp and returns how many it deleted per
// logical table. Idempotent and resumable: every run re-reads from scratch.
// Child partitions (entries, cards, imports, hooks) are deleted before the
// rows that name them, so a run that dies anywhere still finds what is left.
//
// The space's audit rows go too, whatever the space's kind (decision
// 2026-10-13): the workspace itself is gone, so the five-year trail that ADR
// 0009 keeps for a surviving organization has nothing left to explain.
func (p *SpacePurger) PurgeSpace(ctx context.Context, sp space.ResolvedSpace) (map[string]int, error) {
	if sp.IsZero() {
		return nil, space.ErrNoSpace
	}
	counts := map[string]int{}
	s := sp.PK()
	child := func(table, pk string) error {
		n, err := p.deletePartition(ctx, table, pk)
		counts[table] += n
		return err
	}

	// 1. Ledger entries S#ACCOUNT#{id}. The ids come from the chart and from
	// every transaction's legs, so entries are found even after a previous run
	// deleted the chart.
	accounts := map[string]bool{}
	chart, err := queryPrefix(ctx, p.bases[TableLedgerAccounts], s, "ACCOUNT#")
	if err != nil {
		return nil, err
	}
	for _, it := range chart {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			accounts[strings.TrimPrefix(sk.Value, "ACCOUNT#")] = true
		}
	}
	headers, err := queryPrefix(ctx, p.bases[TableLedgerTransactions], s, "TX#")
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
		if err := child(TableLedgerTransactions, LedgerEntryPK(sp, account)); err != nil {
			return counts, err
		}
	}

	// 2. Card partitions S#CARD#{id}: purchases, statements, statement items.
	// The card ids are the S partition's CARD# rows.
	if err := p.eachID(ctx, TableCards, s, "CARD#", func(id string) error {
		return child(TableCards, CardPK(sp, id))
	}); err != nil {
		return counts, err
	}

	// 3. Import partitions S#IMPORT#{id}: the parsed lines. The FITID# locks and
	// CSVMAP# rows are in S itself.
	if err := p.eachID(ctx, TableImports, s, "IMPORT#", func(id string) error {
		return child(TableImports, ImportPK(sp, id))
	}); err != nil {
		return counts, err
	}

	// 4. BYOK hooks HOOK#{id} and HOOK#{id}-payout, named by the connection rows' hook_id.
	conns, err := queryPrefix(ctx, p.bases[TableByok], s, "")
	if err != nil {
		return counts, err
	}
	for _, it := range conns {
		if v, ok := it[attrHookID].(*types.AttributeValueMemberS); ok && v.Value != "" {
			// The receiving hook and the payout hook, each with its own secret.
			for _, hook := range []string{v.Value, v.Value + PayoutHookSuffix} {
				if err := child(TableByok, hookPK(hook)); err != nil {
					return counts, err
				}
			}
		}
	}

	// 5. The space partition S in every table that keys by it, then audit and
	// idempotency.
	tables := append(append([]string(nil), financeTables...), TableIdempotency, TableAudit)
	for _, t := range tables {
		if err := child(t, s); err != nil {
			return counts, err
		}
	}
	return counts, nil
}

// DeleteConnectionHooks deletes the HOOK#{id} and HOOK#{id}-payout partitions a
// bank connection names. The purge calls it before the connection is
// disconnected: Disconnect deletes the connection row, and that row is the only
// thing that names the hooks (PurgeSpace's own step 4 reads it too, so it is
// kept as the safety net for a connection that was never disconnected).
func (p *SpacePurger) DeleteConnectionHooks(ctx context.Context, hookID string) (int, error) {
	if hookID == "" {
		return 0, nil
	}
	total := 0
	for _, id := range []string{hookID, hookID + PayoutHookSuffix} {
		n, err := p.deletePartition(ctx, TableByok, hookPK(id))
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// eachID calls fn with the id of every row under pk whose sort key starts with
// prefix (the sort key is prefix + id).
func (p *SpacePurger) eachID(ctx context.Context, table, pk, prefix string, fn func(id string) error) error {
	rows, err := queryPrefix(ctx, p.bases[table], pk, prefix)
	if err != nil {
		return err
	}
	for _, it := range rows {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			if err := fn(strings.TrimPrefix(sk.Value, prefix)); err != nil {
				return err
			}
		}
	}
	return nil
}

// deletePartition deletes every row under pk.
func (p *SpacePurger) deletePartition(ctx context.Context, logical, pk string) (int, error) {
	b := p.bases[logical]
	items, err := queryPrefix(ctx, b, pk, "")
	if err != nil {
		return 0, err
	}
	return deleteKeys(ctx, b, items)
}

// deleteKeys deletes the given items by key, 25 per BatchWriteItem, retrying
// unprocessed keys with a short backoff.
func deleteKeys(ctx context.Context, b Base, items []map[string]types.AttributeValue) (int, error) {
	deleted := 0
	for start := 0; start < len(items); start += 25 {
		end := min(start+25, len(items))
		reqs := make([]types.WriteRequest, 0, end-start)
		for _, it := range items[start:end] {
			reqs = append(reqs, types.WriteRequest{DeleteRequest: &types.DeleteRequest{
				Key: map[string]types.AttributeValue{"pk": it["pk"], "sk": it["sk"]},
			}})
		}
		pending := map[string][]types.WriteRequest{b.TableName: reqs}
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt == batchDeleteAttempts {
				return deleted, fmt.Errorf("purge %s: keys still unprocessed after %d attempts", b.TableName, attempt)
			}
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return deleted, ctx.Err()
				case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
				}
			}
			out, err := b.BatchWriteItemRaw(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
			if err != nil {
				return deleted, err
			}
			pending = out.UnprocessedItems
		}
		deleted += end - start
	}
	return deleted, nil
}
