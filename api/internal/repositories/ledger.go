package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
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
