package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// BillRepository stores payables and receivables (spec § 3.4).
//
// Every money-affecting change is ONE TransactWriteItems made of the ledger's
// own write plan plus the bill's item, so a bill never exists without its
// recognition and never changes status without the transaction that explains it.
type BillRepository struct {
	bills  Base
	ledger *LedgerRepository
}

func NewBillRepository(db *dynamodb.Client, cfg *config.Config) *BillRepository {
	return &BillRepository{bills: NewBase(db, cfg, TableBills), ledger: NewLedgerRepository(db, cfg)}
}

type billItem struct {
	keys
	ID             string   `dynamodbav:"id"`
	Direction      string   `dynamodbav:"direction"`
	Amount         int64    `dynamodbav:"amount"`
	AccountID      string   `dynamodbav:"account_id"`
	CategoryID     string   `dynamodbav:"category_id"`
	Description    string   `dynamodbav:"description,omitempty"`
	Competence     string   `dynamodbav:"competence"`
	Due            string   `dynamodbav:"due"`
	PaidDate       string   `dynamodbav:"paid_date,omitempty"`
	Status         string   `dynamodbav:"status"`
	Origin         string   `dynamodbav:"origin"`
	OriginRef      string   `dynamodbav:"origin_ref,omitempty"`
	AutoSettle     bool     `dynamodbav:"auto_settle,omitempty"`
	PaymentGroup   string   `dynamodbav:"payment_group,omitempty"`
	TransactionIDs []string `dynamodbav:"transaction_ids"`

	// Sparse index keys, present only while the bill is a forecast (open-index)
	// and, when it auto-settles, until it is settled (schedule-index).
	OpenPK     string `dynamodbav:"open_pk,omitempty"`
	OpenSK     string `dynamodbav:"open_sk,omitempty"`
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

func (i billItem) bill() (finance.Bill, error) {
	parse := func(field, s string) (brcal.Date, error) {
		if s == "" {
			return brcal.Date{}, nil
		}
		d, err := brcal.Parse(s)
		if err != nil {
			return brcal.Date{}, fmt.Errorf("bill %s has a malformed %s: %w", i.ID, field, err)
		}
		return d, nil
	}
	competence, err := parse("competence", i.Competence)
	if err != nil {
		return finance.Bill{}, err
	}
	due, err := parse("due", i.Due)
	if err != nil {
		return finance.Bill{}, err
	}
	paid, err := parse("paid_date", i.PaidDate)
	if err != nil {
		return finance.Bill{}, err
	}
	return finance.Bill{
		ID: i.ID, Direction: finance.Direction(i.Direction), Amount: billing.Cents(i.Amount),
		AccountID: i.AccountID, CategoryID: i.CategoryID, Description: i.Description,
		Competence: competence, Due: due, PaidDate: paid,
		Status: finance.BillStatus(i.Status), Origin: finance.BillOrigin(i.Origin), OriginRef: i.OriginRef,
		AutoSettle: i.AutoSettle, PaymentGroup: i.PaymentGroup,
		TransactionIDs: append([]string(nil), i.TransactionIDs...),
	}, nil
}

// sparseKeys are the index keys a forecast bill carries.
func sparseKeys(sp space.ResolvedSpace, b finance.Bill) (openPK, openSK, schedPK, schedSK string) {
	if b.Status != finance.BillForecast {
		return "", "", "", ""
	}
	openPK, openSK = OpenPK(sp, b.Direction), OpenSK(b.Due, b.ID)
	if b.AutoSettle {
		schedPK, schedSK = AutoSettlePK(sp.Livemode()), ScheduleSK(b.Due, sp.Owner(), b.ID)
	}
	return
}

func newBillItem(sp space.ResolvedSpace, b finance.Bill, now time.Time) billItem {
	openPK, openSK, schedPK, schedSK := sparseKeys(sp, b)
	paid := ""
	if !b.PaidDate.IsZero() {
		paid = b.PaidDate.String()
	}
	return billItem{
		keys: newKeys(sp.PK(), BillSK(b.ID), RetentionPermanent, now),
		ID:   b.ID, Direction: string(b.Direction), Amount: int64(b.Amount),
		AccountID: b.AccountID, CategoryID: b.CategoryID, Description: b.Description,
		Competence: b.Competence.String(), Due: b.Due.String(), PaidDate: paid,
		Status: string(b.Status), Origin: string(b.Origin), OriginRef: b.OriginRef,
		AutoSettle: b.AutoSettle, PaymentGroup: b.PaymentGroup, TransactionIDs: b.TransactionIDs,
		OpenPK: openPK, OpenSK: openSK, SchedulePK: schedPK, ScheduleSK: schedSK,
	}
}

// checkBillAccounts loads the accounts a bill names and refuses combinations the
// reports could not read: a payable's category must be an expense, a receivable's
// an income, and the account it is paid from or into an active asset. The
// ledger's own balance conditions only prove an account exists in the space.
func (r *BillRepository) checkBillAccounts(ctx context.Context, sp space.ResolvedSpace, b finance.Bill) error {
	cat, err := r.ledger.getAccount(ctx, sp, b.CategoryID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: category %s", ErrUnknownAccount, b.CategoryID)
	}
	if err != nil {
		return err
	}
	want := finance.ClassExpense
	if b.Direction == finance.Receivable {
		want = finance.ClassIncome
	}
	if cat.Class != want || cat.System || cat.Archived {
		return fmt.Errorf("%w: a %s needs an active %s category", finance.ErrInvalidBill, b.Direction, want)
	}
	acct, err := r.ledger.getAccount(ctx, sp, b.AccountID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: account %s", ErrUnknownAccount, b.AccountID)
	}
	if err != nil {
		return err
	}
	if acct.Class != finance.ClassAsset || acct.System || acct.Archived {
		return fmt.Errorf("%w: the account must be an active asset account", finance.ErrInvalidBill)
	}
	return nil
}

func billCancelError(err error) error {
	if err != nil && onlyConditionFailed(err) {
		return finance.ErrBillState
	}
	return err
}

// Create validates a bill and posts its recognition at the competence date, in
// one transaction. The id is assigned here; Status is forced to forecast.
func (r *BillRepository) Create(ctx context.Context, sp space.ResolvedSpace, b finance.Bill, meta PostMeta, now time.Time) (finance.Bill, error) {
	if err := sp.Require(space.Write); err != nil {
		return finance.Bill{}, err
	}
	b.ID, b.Status, b.PaidDate, b.TransactionIDs = id.New(), finance.BillForecast, brcal.Date{}, nil
	if err := b.Validate(); err != nil {
		return finance.Bill{}, err
	}
	if err := r.checkBillAccounts(ctx, sp, b); err != nil {
		return finance.Bill{}, err
	}
	return r.create(ctx, sp, b, nil, meta, now)
}

// create is Create after validation. extra are items appended to the same
// transaction (the occurrence lock).
func (r *BillRepository) create(ctx context.Context, sp space.ResolvedSpace, b finance.Bill, extra []types.TransactWriteItem, meta PostMeta, now time.Time) (finance.Bill, error) {
	sys, _ := finance.DefaultSystemAccounts()
	rec, err := finance.RecognizeBill(sys, b.Facts(), b.Competence)
	if err != nil {
		return finance.Bill{}, err
	}
	plan, err := r.ledger.planPost(sp, rec, meta, now)
	if err != nil {
		return finance.Bill{}, err
	}
	b.TransactionIDs = []string{plan.TxID}
	item, err := Encode(newBillItem(sp, b, now))
	if err != nil {
		return finance.Bill{}, err
	}
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), r.bills.BuildPutTxItemIfAbsent(item))
	items = append(items, extra...)
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		return finance.Bill{}, classifyPostCancel(err, plan.MarkerIdx)
	}
	return b, nil
}

// Get reads a bill inside the space; an id from another space is ErrNotFound.
func (r *BillRepository) Get(ctx context.Context, sp space.ResolvedSpace, billID string) (*finance.Bill, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	return r.get(ctx, sp, billID)
}

func (r *BillRepository) get(ctx context.Context, sp space.ResolvedSpace, billID string) (*finance.Bill, error) {
	raw, err := r.bills.GetItem(ctx, sp.PK(), BillSK(billID))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: bill %s", ErrNotFound, billID)
	}
	it, err := Decode[billItem](raw)
	if err != nil {
		return nil, err
	}
	b, err := it.bill()
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListOpen returns a direction's forecast bills, earliest due date first, from
// the sparse open-index: history is never read.
func (r *BillRepository) ListOpen(ctx context.Context, sp space.ResolvedSpace, dir finance.Direction, limit int, startKey map[string]types.AttributeValue) (*Page[finance.Bill], error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	if dir != finance.Payable && dir != finance.Receivable {
		return nil, fmt.Errorf("%w: direction must be payable or receivable", finance.ErrInvalidBill)
	}
	res, err := r.bills.Query(ctx, QueryOpts{
		IndexName: IndexOpen, PKField: "open_pk", SKField: "open_sk", PK: OpenPK(sp, dir),
		ScanIndexForward: true, Limit: limit, ExclusiveStartKey: startKey,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[billItem](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]finance.Bill, len(rows))
	for i, row := range rows {
		if out[i], err = row.bill(); err != nil {
			return nil, err
		}
	}
	return &Page[finance.Bill]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

const removeSparse = " REMOVE open_pk, open_sk, schedule_pk, schedule_sk"

func str(s string) types.AttributeValue { return &types.AttributeValueMemberS{Value: s} }

func (r *BillRepository) stamp(now time.Time) types.AttributeValue {
	return str(now.UTC().Format(time.RFC3339Nano))
}

// Settle posts the cash movement for a forecast bill and marks it paid, in one
// transaction guarded by "still a forecast": of two concurrent settlements
// exactly one commits and the other is finance.ErrBillState.
func (r *BillRepository) Settle(ctx context.Context, sp space.ResolvedSpace, billID string, paid billing.Cents, differenceCategoryID string, date brcal.Date, meta PostMeta, now time.Time) (finance.Bill, error) {
	if err := sp.Require(space.Write | space.Settle); err != nil {
		return finance.Bill{}, err
	}
	b, err := r.get(ctx, sp, billID)
	if err != nil {
		return finance.Bill{}, err
	}
	if err := b.CanSettle(); err != nil {
		return finance.Bill{}, err
	}
	if differenceCategoryID != "" {
		// The gap lands on this category: interest or discount, so an income or
		// expense account — never an asset, a liability or a system account.
		cat, err := r.ledger.getAccount(ctx, sp, differenceCategoryID)
		if errors.Is(err, ErrNotFound) {
			return finance.Bill{}, fmt.Errorf("%w: category %s", ErrUnknownAccount, differenceCategoryID)
		}
		if err != nil {
			return finance.Bill{}, err
		}
		if (cat.Class != finance.ClassIncome && cat.Class != finance.ClassExpense) || cat.System || cat.Archived {
			return finance.Bill{}, fmt.Errorf("%w: the difference needs an active income or expense category", finance.ErrInvalidTransaction)
		}
	}
	sys, _ := finance.DefaultSystemAccounts()
	tx, err := finance.SettleBill(sys, b.Facts(), paid, differenceCategoryID, date)
	if err != nil {
		return finance.Bill{}, err
	}
	plan, err := r.ledger.planPost(sp, tx, meta, now)
	if err != nil {
		return finance.Bill{}, err
	}
	sk := BillSK(billID)
	update := r.bills.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET #status = :paid, paid_date = :d, transaction_ids = list_append(transaction_ids, :tx), updated_at = :now"+removeSparse,
		"#status = :forecast", map[string]string{"#status": "status"},
		map[string]types.AttributeValue{
			":paid": str(string(finance.BillPaid)), ":forecast": str(string(finance.BillForecast)),
			":d": str(date.String()), ":now": r.stamp(now),
			":tx": &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}},
		})
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), update)
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			// Either the bill left the forecast state (a concurrent settle or
			// cancel) or an account is gone. The bill is re-read to tell them apart.
			if cur, gerr := r.get(ctx, sp, billID); gerr == nil && cur.Status != finance.BillForecast {
				return finance.Bill{}, finance.ErrBillState
			}
			return finance.Bill{}, ErrUnknownAccount
		}
		return finance.Bill{}, err
	}
	return *mustGet(r.get(ctx, sp, billID)), nil
}

func mustGet(b *finance.Bill, err error) *finance.Bill {
	if err != nil || b == nil {
		return &finance.Bill{}
	}
	return b
}

// Cancel removes a forecast bill: its recognition is negated at the competence
// date (so nothing of it stays in the DRE) and it leaves the open list. A paid
// bill is refused with finance.ErrBillState.
func (r *BillRepository) Cancel(ctx context.Context, sp space.ResolvedSpace, billID string, date brcal.Date, meta PostMeta, now time.Time) (finance.Bill, error) {
	if err := sp.Require(space.Write); err != nil {
		return finance.Bill{}, err
	}
	b, err := r.get(ctx, sp, billID)
	if err != nil {
		return finance.Bill{}, err
	}
	if err := b.CanCancel(); err != nil {
		return finance.Bill{}, err
	}
	if date.IsZero() {
		date = b.Competence
	}
	sys, _ := finance.DefaultSystemAccounts()
	tx, err := finance.CancelBill(sys, b.Facts(), date)
	if err != nil {
		return finance.Bill{}, err
	}
	plan, err := r.ledger.planPost(sp, tx, meta, now)
	if err != nil {
		return finance.Bill{}, err
	}
	sk := BillSK(billID)
	update := r.bills.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET #status = :canceled, transaction_ids = list_append(transaction_ids, :tx), updated_at = :now"+removeSparse,
		"#status = :forecast", map[string]string{"#status": "status"},
		map[string]types.AttributeValue{
			":canceled": str(string(finance.BillCanceled)), ":forecast": str(string(finance.BillForecast)),
			":now": r.stamp(now),
			":tx":  &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}},
		})
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), update)
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		return finance.Bill{}, billCancelError(err)
	}
	return *mustGet(r.get(ctx, sp, billID)), nil
}

// BillEdit is a partial change to a forecast bill. Nil fields stay as they are.
type BillEdit struct {
	Amount      *billing.Cents
	CategoryID  *string
	AccountID   *string
	Description *string
	Due         *brcal.Date
}

// Edit changes a forecast bill. Due date, account and description have no
// ledger effect. An amount or category change adjusts what was recognised as one
// net transaction (finance.AdjustBill), in the same write as the bill. Two edits
// racing cannot both win: the update is conditioned on the bill still being a
// forecast with the transaction list it was read with.
func (r *BillRepository) Edit(ctx context.Context, sp space.ResolvedSpace, billID string, e BillEdit, date brcal.Date, meta PostMeta, now time.Time) (finance.Bill, error) {
	if err := sp.Require(space.Write); err != nil {
		return finance.Bill{}, err
	}
	cur, err := r.get(ctx, sp, billID)
	if err != nil {
		return finance.Bill{}, err
	}
	if !cur.CanEdit() {
		return finance.Bill{}, fmt.Errorf("%w: only a forecast bill can be edited", finance.ErrBillState)
	}
	next := *cur
	if e.Amount != nil {
		next.Amount = *e.Amount
	}
	if e.CategoryID != nil {
		next.CategoryID = *e.CategoryID
	}
	if e.AccountID != nil {
		next.AccountID = *e.AccountID
	}
	if e.Description != nil {
		next.Description = *e.Description
	}
	if e.Due != nil {
		next.Due = *e.Due
	}
	if err := next.Validate(); err != nil {
		return finance.Bill{}, err
	}
	if next.CategoryID != cur.CategoryID || next.AccountID != cur.AccountID {
		if err := r.checkBillAccounts(ctx, sp, next); err != nil {
			return finance.Bill{}, err
		}
	}

	names := map[string]string{"#status": "status", "#n": "transaction_ids"}
	values := map[string]types.AttributeValue{
		":forecast": str(string(finance.BillForecast)), ":now": r.stamp(now),
		":n": &types.AttributeValueMemberN{Value: fmt.Sprint(len(cur.TransactionIDs))},
	}
	sets := []string{"updated_at = :now"}
	set := func(attr string, v types.AttributeValue) {
		names["#"+attr] = attr
		values[":"+attr] = v
		sets = append(sets, "#"+attr+" = :"+attr)
	}
	var items []types.TransactWriteItem

	if next.Amount != cur.Amount || next.CategoryID != cur.CategoryID {
		set("amount", numberValue(int64(next.Amount)))
		set("category_id", str(next.CategoryID))
		sys, _ := finance.DefaultSystemAccounts()
		tx, err := finance.AdjustBill(sys, cur.Facts(), next.Facts(), cur.Competence)
		if err != nil {
			return finance.Bill{}, err
		}
		plan, err := r.ledger.planPost(sp, tx, meta, now)
		if err != nil {
			return finance.Bill{}, err
		}
		items = append(items, plan.Items...)
		values[":tx"] = &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}}
		sets = append(sets, "#n = list_append(#n, :tx)")
	}
	if next.AccountID != cur.AccountID {
		set("account_id", str(next.AccountID))
	}
	if next.Description != cur.Description {
		set("description", str(next.Description))
	}
	if next.Due != cur.Due {
		set("due", str(next.Due.String()))
		openPK, openSK, schedPK, schedSK := sparseKeys(sp, next)
		set("open_pk", str(openPK))
		set("open_sk", str(openSK))
		if schedPK != "" {
			set("schedule_pk", str(schedPK))
			set("schedule_sk", str(schedSK))
		}
	}
	if len(sets) == 1 {
		return *cur, nil // nothing changed
	}
	sk := BillSK(billID)
	items = append(items, r.bills.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET "+strings.Join(sets, ", "), "#status = :forecast AND size(#n) = :n", names, values))
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return finance.Bill{}, finance.ErrBillState
		}
		return finance.Bill{}, err
	}
	return *mustGet(r.get(ctx, sp, billID)), nil
}
