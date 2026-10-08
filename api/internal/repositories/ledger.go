package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ErrUnknownAccount is a transaction that names an account that does not exist
// in the resolved space. An account id from another space is exactly this: the
// lookup is inside the partition, so it is simply not there (spec § 2.1 rule 5).
var ErrUnknownAccount = errors.New("ledger: unknown account in this space")

// ErrAlreadyReversed is a second reversal of the same transaction.
var ErrAlreadyReversed = errors.New("ledger: transaction already reversed")

// LedgerRepository stores a space's management ledger (ADR 0024).
//
// Every method takes a space.ResolvedSpace and nothing else that names a
// partition. There is deliberately no update or delete of a transaction or an
// entry: a correction is a reversal (TestLedgerRepositoryHasNoEditPath).
type LedgerRepository struct {
	accounts Base
	txs      Base
	audit    Base
}

func NewLedgerRepository(db *dynamodb.Client, cfg *config.Config) *LedgerRepository {
	return &LedgerRepository{
		accounts: NewBase(db, cfg, TableLedgerAccounts),
		txs:      NewBase(db, cfg, TableLedgerTransactions),
		audit:    NewBase(db, cfg, TableAudit),
	}
}

// AccountRow is an account with its cached balance (debit positive).
type AccountRow struct {
	finance.LedgerAccount
	Balance billing.Cents
}

type accountItem struct {
	keys
	ID      string               `dynamodbav:"id"`
	Name    string               `dynamodbav:"name"`
	Class   finance.AccountClass `dynamodbav:"class"`
	Group   finance.DREGroup     `dynamodbav:"dre_group,omitempty"`
	System  bool                 `dynamodbav:"system,omitempty"`
	Balance int64                `dynamodbav:"balance"`
}

func (i accountItem) row() AccountRow {
	return AccountRow{
		LedgerAccount: finance.LedgerAccount{ID: i.ID, Name: i.Name, Class: i.Class, Group: i.Group, System: i.System},
		Balance:       billing.Cents(i.Balance),
	}
}

func newAccountItem(sp space.ResolvedSpace, a finance.LedgerAccount, now time.Time) accountItem {
	return accountItem{
		keys: newKeys(sp.PK(), LedgerAccountSK(a.ID), RetentionPermanent, now),
		ID:   a.ID, Name: a.Name, Class: a.Class, Group: a.Group, System: a.System,
	}
}

// EnsureSpace creates the space's settings row and its three system accounts,
// once. Safe to call on every first write: when the space already exists the
// conditional write fails and that is success.
func (r *LedgerRepository) EnsureSpace(ctx context.Context, sp space.ResolvedSpace, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	settings, err := Encode(struct {
		keys
		Mode string `dynamodbav:"mode"`
	}{newKeys(sp.PK(), LedgerSpaceSK(), RetentionPermanent, now), sp.Mode()})
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{r.accounts.BuildPutTxItemIfAbsent(settings)}
	_, system := finance.DefaultSystemAccounts()
	for _, a := range system {
		item, err := Encode(newAccountItem(sp, a, now))
		if err != nil {
			return err
		}
		items = append(items, r.accounts.BuildPutTxItemIfAbsent(item))
	}
	err = r.accounts.TransactWrite(ctx, items)
	if IsConditionFailed(err) {
		return nil
	}
	return err
}

// CreateAccount adds an account or category to the space's chart.
func (r *LedgerRepository) CreateAccount(ctx context.Context, sp space.ResolvedSpace, a finance.LedgerAccount, now time.Time) error {
	if err := sp.Require(space.Configure); err != nil {
		return err
	}
	if a.System {
		return fmt.Errorf("%w: system accounts are created with the space", finance.ErrInvalidAccount)
	}
	if err := a.Validate(); err != nil {
		return err
	}
	item, err := Encode(newAccountItem(sp, a, now))
	if err != nil {
		return err
	}
	err = r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: account %s already exists", finance.ErrInvalidAccount, a.ID)
	}
	return err
}

// GetAccount reads one account inside the space.
func (r *LedgerRepository) GetAccount(ctx context.Context, sp space.ResolvedSpace, id string) (*AccountRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	raw, err := r.accounts.GetItem(ctx, sp.PK(), LedgerAccountSK(id))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrNotFound
	}
	it, err := Decode[accountItem](raw)
	if err != nil {
		return nil, err
	}
	row := it.row()
	return &row, nil
}

// ListAccounts returns the chart: one Query on the space partition.
func (r *LedgerRepository) ListAccounts(ctx context.Context, sp space.ResolvedSpace) ([]AccountRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.queryPrefix(ctx, r.accounts, sp.PK(), "ACCOUNT#")
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeItems[accountItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]AccountRow, len(decoded))
	for i, it := range decoded {
		out[i] = it.row()
	}
	return out, nil
}

// queryPrefix returns every item under pk whose sort key begins with skPrefix,
// in ascending order, following continuation keys to the end. A space's chart
// and a period's entries are bounded by the user's own data, never by a
// cross-tenant scan.
func (r *LedgerRepository) queryPrefix(ctx context.Context, b Base, pk, skPrefix string) ([]map[string]types.AttributeValue, error) {
	var out []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		res, err := b.Query(ctx, QueryOpts{
			PK: pk, SKPrefix: skPrefix, ScanIndexForward: true, Limit: 100, ExclusiveStartKey: start, ConsistentRead: true,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// PostMeta is the provenance written beside a transaction.
type PostMeta struct {
	Origin    string // e.g. "manual", "bill_settlement", "invoice_paid"
	Actor     string // the token subject
	RequestID string
}

type txItem struct {
	keys
	ID      string         `dynamodbav:"id"`
	Kind    finance.TxKind `dynamodbav:"kind"`
	Date    string         `dynamodbav:"date"`
	Legs    []legItem      `dynamodbav:"legs"`
	Adjusts string         `dynamodbav:"adjusts,omitempty"`
	Origin  string         `dynamodbav:"origin,omitempty"`
	Actor   string         `dynamodbav:"actor,omitempty"`
}

type legItem struct {
	Account string `dynamodbav:"account"`
	Amount  int64  `dynamodbav:"amount"`
}

type entryItem struct {
	keys
	TxID   string `dynamodbav:"tx_id"`
	Date   string `dynamodbav:"date"`
	Leg    int    `dynamodbav:"leg"`
	Amount int64  `dynamodbav:"amount"`
}

// Post writes one balanced transaction atomically: the header, one entry per
// leg, an ADD on each touched balance and monthly summary, the reversal marker
// when it is a reversal, and the audit row — one TransactWriteItems, so either
// all of it or none. Every leg's account must already exist in THIS partition
// (attribute_exists on the balance update); an account id from another space is
// therefore ErrUnknownAccount and nothing is written.
func (r *LedgerRepository) Post(ctx context.Context, sp space.ResolvedSpace, tx finance.Transaction, meta PostMeta, now time.Time) (string, error) {
	need := space.Write
	if tx.Kind == finance.KindSettlement {
		need |= space.Settle
	}
	if err := sp.Require(need); err != nil {
		return "", err
	}
	// Revalidate: a Transaction is only trusted if it went through
	// NewTransaction, and a zero or hand-built one must not reach the table.
	checked, err := finance.NewTransaction(tx.Kind, tx.Date, tx.Legs...)
	if err != nil {
		return "", err
	}
	checked.Adjusts = tx.Adjusts

	txID := id.New()
	items, markerIdx, err := r.postItems(sp, txID, checked, meta, now)
	if err != nil {
		return "", err
	}
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if IsConditionFailed(err) {
			return "", classifyPostCancel(err, checked, markerIdx)
		}
		return "", err
	}
	return txID, nil
}

// classifyPostCancel tells a repeated reversal from an unknown account. The
// per-item cancellation reasons say which condition failed; when the SDK error
// is not reachable, only a reversal carries a marker condition, and Reverse
// loads the original (and so its accounts) first, so the marker is the likely
// cause.
func classifyPostCancel(err error, tx finance.Transaction, markerIdx int) error {
	var tc *types.TransactionCanceledException
	if errors.As(err, &tc) && markerIdx >= 0 && markerIdx < len(tc.CancellationReasons) {
		if code := tc.CancellationReasons[markerIdx].Code; code != nil && *code == "ConditionalCheckFailed" {
			return ErrAlreadyReversed
		}
		return ErrUnknownAccount
	}
	if tx.Adjusts != "" {
		return ErrAlreadyReversed
	}
	return ErrUnknownAccount
}

func (r *LedgerRepository) postItems(sp space.ResolvedSpace, txID string, tx finance.Transaction, meta PostMeta, now time.Time) (items []types.TransactWriteItem, markerIdx int, err error) {
	markerIdx = -1
	legs := make([]legItem, len(tx.Legs))
	for i, l := range tx.Legs {
		legs[i] = legItem{Account: l.AccountID, Amount: int64(l.Amount)}
	}
	header, err := Encode(txItem{
		keys: newKeys(sp.PK(), LedgerTxSK(txID), RetentionPermanent, now),
		ID:   txID, Kind: tx.Kind, Date: tx.Date.String(), Legs: legs,
		Adjusts: tx.Adjusts, Origin: meta.Origin, Actor: meta.Actor,
	})
	if err != nil {
		return nil, -1, err
	}
	items = append(items, r.txs.BuildPutTxItemIfAbsent(header))

	month := finance.MonthOf(tx.Date)
	for i, l := range tx.Legs {
		entry, err := Encode(entryItem{
			keys: newKeys(LedgerEntryPK(sp, l.AccountID), LedgerEntrySK(tx.Date, txID, i), RetentionPermanent, now),
			TxID: txID, Date: tx.Date.String(), Leg: i, Amount: int64(l.Amount),
		})
		if err != nil {
			return nil, -1, err
		}
		items = append(items, r.txs.BuildPutTxItemIfAbsent(entry))

		acctSK := LedgerAccountSK(l.AccountID)
		items = append(items, r.accounts.BuildRawUpdateTxItem(sp.PK(), &acctSK,
			"ADD balance :amt", "attribute_exists(pk)", nil,
			map[string]types.AttributeValue{":amt": numberValue(int64(l.Amount))}))

		var debit, credit int64
		if l.Amount > 0 {
			debit = int64(l.Amount)
		} else {
			credit = int64(-l.Amount)
		}
		sumSK := LedgerSummarySK(month, l.AccountID)
		items = append(items, r.accounts.BuildRawUpdateTxItem(sp.PK(), &sumSK,
			"ADD debits :d, credits :c", "", nil,
			map[string]types.AttributeValue{":d": numberValue(debit), ":c": numberValue(credit)}))
	}

	if tx.Adjusts != "" {
		marker, err := Encode(struct {
			keys
			TxID string `dynamodbav:"tx_id"`
		}{newKeys(sp.PK(), LedgerReversalSK(tx.Adjusts), RetentionPermanent, now), txID})
		if err != nil {
			return nil, -1, err
		}
		markerIdx = len(items)
		items = append(items, r.txs.BuildPutTxItemIfAbsent(marker))
	}

	audit, err := buildAuditItem(sp.Owner(), sp.Livemode(), AuditEntry{
		Entity: EntityLedgerTx, EntityID: txID, Action: "ledger.transaction.posted",
		Actor: meta.Actor, RequestID: meta.RequestID,
	}, "", "", now)
	if err != nil {
		return nil, -1, err
	}
	items = append(items, r.audit.BuildPutTxItem(audit))
	return items, markerIdx, nil
}

func numberValue(n int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}
}

// StoredTx is a transaction as written, with its id.
type StoredTx struct {
	ID     string
	Tx     finance.Transaction
	Origin string
}

// GetTransaction reads a transaction inside the space. An id from another space
// is ErrNotFound: the lookup key is built from the resolved space.
func (r *LedgerRepository) GetTransaction(ctx context.Context, sp space.ResolvedSpace, txID string) (*StoredTx, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	raw, err := r.txs.GetItem(ctx, sp.PK(), LedgerTxSK(txID))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: transaction %s", ErrNotFound, txID)
	}
	it, err := Decode[txItem](raw)
	if err != nil {
		return nil, err
	}
	return it.stored()
}

func (i txItem) stored() (*StoredTx, error) {
	date, err := brcal.Parse(i.Date)
	if err != nil {
		return nil, fmt.Errorf("transaction %s has a malformed date: %w", i.ID, err)
	}
	legs := make([]finance.Leg, len(i.Legs))
	for n, l := range i.Legs {
		legs[n] = finance.Leg{AccountID: l.Account, Amount: billing.Cents(l.Amount)}
	}
	tx, err := finance.NewTransaction(i.Kind, date, legs...)
	if err != nil {
		return nil, fmt.Errorf("stored transaction %s is not balanced: %w", i.ID, err)
	}
	tx.Adjusts = i.Adjusts
	return &StoredTx{ID: i.ID, Tx: tx, Origin: i.Origin}, nil
}

// Reverse posts the exact opposite of a stored transaction on date. Both the
// mistake and its correction stay in the record (Fowler's Reversal Adjustment).
// A second reversal of the same transaction is ErrAlreadyReversed.
func (r *LedgerRepository) Reverse(ctx context.Context, sp space.ResolvedSpace, originalID string, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	if err := sp.Require(space.Write); err != nil {
		return "", err
	}
	orig, err := r.GetTransaction(ctx, sp, originalID)
	if err != nil {
		return "", err
	}
	rev, err := finance.Reverse(orig.Tx, originalID, date)
	if err != nil {
		return "", err
	}
	return r.Post(ctx, sp, rev, meta, now)
}

// EntryRow is one leg of a posted transaction on an account.
type EntryRow struct {
	TxID   string
	Date   brcal.Date
	Leg    int
	Amount billing.Cents
}

// Statement returns an account's entries with from <= date < to, in order. It
// is one range Query on the account's entry partition.
func (r *LedgerRepository) Statement(ctx context.Context, sp space.ResolvedSpace, accountID string, from, to brcal.Date) ([]EntryRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.queryRange(ctx, r.txs, LedgerEntryPK(sp, accountID),
		"ENTRY#"+from.String(), "ENTRY#"+to.String(), false)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[entryItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]EntryRow, len(rows))
	for i, e := range rows {
		d, err := brcal.Parse(e.Date)
		if err != nil {
			return nil, fmt.Errorf("entry %s has a malformed date: %w", e.SK, err)
		}
		out[i] = EntryRow{TxID: e.TxID, Date: d, Leg: e.Leg, Amount: billing.Cents(e.Amount)}
	}
	return out, nil
}

// SummaryRow is one account's debits and credits in one month.
type SummaryRow struct {
	Month     finance.Month
	AccountID string
	finance.Totals
}

type summaryItem struct {
	keys
	Debits  int64 `dynamodbav:"debits"`
	Credits int64 `dynamodbav:"credits"`
}

// Summaries returns every SUMMARY row for the months from..to inclusive: one
// range Query, which is what makes a DRE or a cash flow cheap.
func (r *LedgerRepository) Summaries(ctx context.Context, sp space.ResolvedSpace, from, to finance.Month) ([]SummaryRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	// "~" sorts after every character an account id or month uses, so the upper
	// bound includes every account of the last month.
	return r.summaryRange(ctx, sp, "SUMMARY#"+from.String(), "SUMMARY#"+to.String()+"#~")
}

func (r *LedgerRepository) allSummaries(ctx context.Context, sp space.ResolvedSpace) ([]SummaryRow, error) {
	return r.summaryRange(ctx, sp, "SUMMARY#", "SUMMARY#~")
}

func (r *LedgerRepository) summaryRange(ctx context.Context, sp space.ResolvedSpace, lo, hi string) ([]SummaryRow, error) {
	items, err := r.queryRange(ctx, r.accounts, sp.PK(), lo, hi, true)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[summaryItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]SummaryRow, 0, len(rows))
	for _, s := range rows {
		// SK is SUMMARY#yyyy-mm#account; an account id never contains '#'.
		parts := strings.SplitN(s.SK, "#", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("malformed summary key %q", s.SK)
		}
		d, err := brcal.Parse(parts[1] + "-01")
		if err != nil {
			return nil, fmt.Errorf("malformed summary month in %q: %w", s.SK, err)
		}
		out = append(out, SummaryRow{
			Month: finance.MonthOf(d), AccountID: parts[2],
			Totals: finance.Totals{Debits: billing.Cents(s.Debits), Credits: billing.Cents(s.Credits)},
		})
	}
	return out, nil
}

// AllTransactions returns every transaction in the space, for the rebuild
// command. Not on any request path: it reads the whole history.
func (r *LedgerRepository) AllTransactions(ctx context.Context, sp space.ResolvedSpace) ([]finance.Transaction, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.queryPrefix(ctx, r.txs, sp.PK(), "TX#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[txItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]finance.Transaction, len(rows))
	for i, row := range rows {
		st, err := row.stored()
		if err != nil {
			return nil, err
		}
		out[i] = st.Tx
	}
	return out, nil
}

// queryRange returns the items under pk whose sort key is in [lo, hi) — or
// [lo, hi] when inclusive — ascending, following continuation keys.
func (r *LedgerRepository) queryRange(ctx context.Context, b Base, pk, lo, hi string, inclusive bool) ([]map[string]types.AttributeValue, error) {
	op := "<"
	if inclusive {
		op = "<="
	}
	var out []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		res, err := b.QueryRaw(ctx, &dynamodb.QueryInput{
			KeyConditionExpression: aws.String("pk = :pk AND sk >= :lo AND sk " + op + " :hi"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: pk},
				":lo": &types.AttributeValueMemberS{Value: lo},
				":hi": &types.AttributeValueMemberS{Value: hi},
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}
