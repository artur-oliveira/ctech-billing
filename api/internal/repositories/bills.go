package repositories

import (
	"context"
	"errors"
	"fmt"
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

// BillRepository stores payables and receivables (spec § 3.4).
//
// Every money-affecting change is ONE TransactWriteItems made of the ledger's
// own write plan plus the bill's item, so a bill never exists without its
// recognition and never changes status without the transaction that explains it.
type BillRepository struct {
	bills Base
	// recs is read only by CreateFromOccurrence's ConditionCheck on the recurrence.
	recs   Base
	ledger *LedgerRepository
}

func NewBillRepository(db *dynamodb.Client, cfg *config.Config) *BillRepository {
	return &BillRepository{bills: NewBase(db, cfg, TableBills), recs: NewBase(db, cfg, TableRecurrences), ledger: NewLedgerRepository(db, cfg)}
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
	Credited       int64    `dynamodbav:"credited,omitempty"`
	// ImportLink is the statement line ({import}#{n}) a paid bill was linked to
	// (6.6 follow-up): set once, conditionally, so two lines never hold one bill.
	ImportLink string `dynamodbav:"import_link,omitempty"`

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
		Credited:       billing.Cents(i.Credited),
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
		Credited: int64(b.Credited),
		OpenPK:   openPK, OpenSK: openSK, SchedulePK: schedPK, ScheduleSK: schedSK,
	}
}

// checkDirectionAccounts loads the accounts a bill or recurrence names and
// refuses combinations the reports could not read: a payable's category must be
// an expense, a receivable's an income, and the account it is paid from or into
// an active asset. The ledger's own balance conditions only prove an account
// exists in the space. invalid is the sentinel the caller's refusals wrap.
func checkDirectionAccounts(ctx context.Context, ledger *LedgerRepository, sp space.ResolvedSpace, dir finance.Direction, categoryID, accountID string, invalid error) error {
	cat, err := ledger.getAccount(ctx, sp, categoryID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: category %s", ErrUnknownAccount, categoryID)
	}
	if err != nil {
		return err
	}
	want := finance.ClassExpense
	if dir == finance.Receivable {
		want = finance.ClassIncome
	}
	if cat.Class != want || cat.System || cat.Archived {
		return fmt.Errorf("%w: a %s needs an active %s category", invalid, dir, want)
	}
	acct, err := ledger.getAccount(ctx, sp, accountID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: account %s", ErrUnknownAccount, accountID)
	}
	if err != nil {
		return err
	}
	if acct.Class != finance.ClassAsset || acct.System || acct.Archived {
		return fmt.Errorf("%w: the account must be an active asset account", invalid)
	}
	return nil
}

func (r *BillRepository) checkBillAccounts(ctx context.Context, sp space.ResolvedSpace, b finance.Bill) error {
	return checkDirectionAccounts(ctx, r.ledger, sp, b.Direction, b.CategoryID, b.AccountID, finance.ErrInvalidBill)
}

func billCancelError(err error) error {
	if err != nil && onlyConditionFailed(err) {
		return finance.ErrBillState
	}
	return err
}

// Create validates a bill and posts its recognition at the competence date, in
// one transaction. The id is assigned here; Status is forced to forecast. With
// meta.IdempotencyKey the id is derived from (space, key), so a concurrent double
// submit collides on the conditional put and returns the one bill.
func (r *BillRepository) Create(ctx context.Context, sp space.ResolvedSpace, b finance.Bill, meta PostMeta, now time.Time) (finance.Bill, error) {
	need := space.Write
	if b.AutoSettle {
		// The daily job will settle this bill on the user's behalf, so choosing
		// it is settling: it needs the same verb.
		need |= space.Settle
	}
	if err := sp.Require(need); err != nil {
		return finance.Bill{}, err
	}
	b.ID, b.Status, b.PaidDate, b.TransactionIDs = id.New(), finance.BillForecast, brcal.Date{}, nil
	if meta.IdempotencyKey != "" {
		b.ID = idempotentID(sp, "bill", meta.IdempotencyKey)
	}
	if err := b.Validate(); err != nil {
		return finance.Bill{}, err
	}
	if err := r.checkBillAccounts(ctx, sp, b); err != nil {
		return finance.Bill{}, err
	}
	b, _, err := r.createWithGuards(ctx, sp, b, nil, nil, meta, now)
	return b, err
}

// createWithGuards is Create after validation. lock, when set, is the occurrence
// lock Put: if that condition is what fails, nothing was written and created is
// false with no error — the occurrence already has its bill. check, when set, is
// a ConditionCheck on the recurrence row: if it fails the recurrence changed
// after the job read it and ErrRecurrenceChanged comes back. A bill whose own
// (idempotent) id already exists is returned as it is, created=false.
func (r *BillRepository) createWithGuards(ctx context.Context, sp space.ResolvedSpace, b finance.Bill, lock, check *types.TransactWriteItem, meta PostMeta, now time.Time) (finance.Bill, bool, error) {
	sys, _ := finance.DefaultSystemAccounts()
	rec, err := finance.RecognizeBill(sys, b.Facts(), b.Competence)
	if err != nil {
		return finance.Bill{}, false, err
	}
	plan, err := r.ledger.planPost(sp, rec, billMeta(meta, b), now)
	if err != nil {
		return finance.Bill{}, false, err
	}
	b.TransactionIDs = []string{plan.TxID}
	item, err := Encode(newBillItem(sp, b, now))
	if err != nil {
		return finance.Bill{}, false, err
	}
	items := append([]types.TransactWriteItem(nil), plan.Items...)
	billIdx := len(items)
	items = append(items, r.bills.BuildPutTxItemIfAbsent(item))
	lockIdx, checkIdx := -1, -1
	if lock != nil {
		lockIdx = len(items)
		items = append(items, *lock)
	}
	if check != nil {
		checkIdx = len(items)
		items = append(items, *check)
	}
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		failed := func(i int) bool { return i >= 0 && i < len(codes) && codes[i] == codeConditionFailed }
		switch {
		case failed(checkIdx):
			return finance.Bill{}, false, ErrRecurrenceChanged
		case failed(lockIdx):
			return finance.Bill{}, false, nil
		case failed(billIdx) && meta.IdempotencyKey != "":
			if existing, gerr := r.get(ctx, sp, b.ID); gerr == nil {
				return *existing, false, nil
			}
		}
		return finance.Bill{}, false, classifyPostCancel(err, plan.MarkerIdx)
	}
	return b, true, nil
}

// recurrenceUnchanged is the ConditionCheck that ties a materialisation to the
// recurrence the job read: still there, not archived, and still paying what the
// draft says from where it says, with the same auto_settle. If a user archived
// or retargeted it in between, the stale snapshot must not become bills.
func (r *BillRepository) recurrenceUnchanged(sp space.ResolvedSpace, recurrenceID string, b finance.Bill) types.TransactWriteItem {
	// DynamoDB refuses values the expression does not use, so :t and :f are added
	// only where the condition names them.
	values := map[string]types.AttributeValue{
		":f":   &types.AttributeValueMemberBOOL{Value: false}, // archived is not true
		":amt": numberValue(int64(b.Amount)), ":acct": str(b.AccountID), ":cat": str(b.CategoryID),
	}
	autoCond := "(attribute_not_exists(#as) OR #as = :f)"
	if b.AutoSettle {
		autoCond = "#as = :t"
		values[":t"] = &types.AttributeValueMemberBOOL{Value: true}
	}
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: aws.String(r.recs.TableName),
		Key: map[string]types.AttributeValue{
			"pk": str(sp.PK()), "sk": str(RecurrenceSK(recurrenceID)),
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND (attribute_not_exists(#arch) OR #arch = :f) AND #amt = :amt AND #acct = :acct AND #cat = :cat AND " + autoCond),
		ExpressionAttributeNames: map[string]string{
			"#arch": "archived", "#as": "auto_settle", "#amt": "amount", "#acct": "account_id", "#cat": "category_id",
		},
		ExpressionAttributeValues: values,
	}}
}

// CreateFromOccurrence creates the bill of one recurrence occurrence, guarded by
// the OCCURRENCE#{recurrence}#{nominal} lock row written in the same
// transaction, and by a check that the recurrence is still what the job read. A
// repeat — a re-run, a retry after a crash — finds the lock and is
// (created=false, nil): one bill per occurrence, however often the job runs.
func (r *BillRepository) CreateFromOccurrence(ctx context.Context, sp space.ResolvedSpace, d finance.Draft, recurrenceID string, meta PostMeta, now time.Time) (bool, finance.Bill, error) {
	need := space.Write
	if d.Bill.AutoSettle {
		need |= space.Settle // an auto-settling bill is settled by the job on the user's behalf
	}
	if err := sp.Require(need); err != nil {
		return false, finance.Bill{}, err
	}
	b := d.Bill
	b.ID, b.Status, b.PaidDate, b.TransactionIDs = id.New(), finance.BillForecast, brcal.Date{}, nil
	b.Origin, b.OriginRef = finance.OriginRecurrence, finance.OccurrenceRef(recurrenceID, d.Nominal)
	if err := b.Validate(); err != nil {
		return false, finance.Bill{}, err
	}
	lockRow, err := Encode(struct {
		keys
		BillID string `dynamodbav:"bill_id"`
	}{newKeys(sp.PK(), OccurrenceSK(recurrenceID, d.Nominal), RetentionPermanent, now), b.ID})
	if err != nil {
		return false, finance.Bill{}, err
	}
	lock := r.bills.BuildPutTxItemIfAbsent(lockRow)
	check := r.recurrenceUnchanged(sp, recurrenceID, b)
	created, ok, err := r.createWithGuards(ctx, sp, b, &lock, &check, meta, now)
	return ok, created, err
}

// DueBill is an auto-settle bill the daily job should settle.
type DueBill struct {
	Space space.ResolvedSpace
	Bill  finance.Bill
}

// DueForAutoSettle returns, oldest first, the forecast bills flagged to
// auto-settle whose due date is on or before today. Like DueToMaterialise it
// counts forged owners and vanished rows in skipped and never acts on them.
func (r *BillRepository) DueForAutoSettle(ctx context.Context, livemode bool, today brcal.Date, limit int) (due []DueBill, skipped int, err error) {
	entries, skipped, err := scanSchedule(ctx, r.bills, AutoSettlePK(livemode), livemode, today, limit)
	if err != nil {
		return nil, skipped, err
	}
	for _, e := range entries {
		b, err := r.get(ctx, e.Space, e.ID)
		if errors.Is(err, ErrNotFound) {
			skipped++
			continue
		}
		if err != nil {
			return due, skipped, err
		}
		due = append(due, DueBill{Space: e.Space, Bill: *b})
	}
	return due, skipped, nil
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

// forecastGuard is the condition every write that acts on a bill as it was read
// carries: still a forecast, with exactly the transaction list it had when read
// (edits that change the recognised amount or category append to it), and the
// same paying account and due date (a settlement posts cash from that account,
// and the job settles on that date). A stale read — including an eventually
// consistent one — therefore cannot commit after anything that changes what the
// write would do.
func forecastGuard(cur *finance.Bill) (cond string, names map[string]string, values map[string]types.AttributeValue) {
	// The :g prefix keeps these placeholders apart from the ones Edit builds for
	// its own SET clauses (":due" there is the NEW due date, here the one read).
	return "#status = :gforecast AND size(#n) = :gn AND account_id = :gacct AND #due = :gdue",
		map[string]string{"#status": "status", "#n": "transaction_ids", "#due": "due"},
		map[string]types.AttributeValue{
			":gforecast": str(string(finance.BillForecast)),
			":gn":        &types.AttributeValueMemberN{Value: fmt.Sprint(len(cur.TransactionIDs))},
			":gacct":     str(cur.AccountID),
			":gdue":      str(cur.Due.String()),
		}
}

func mergeGuard(cur *finance.Bill, values map[string]types.AttributeValue) (cond string, names map[string]string) {
	cond, names, guardValues := forecastGuard(cur)
	for k, v := range guardValues {
		values[k] = v
	}
	return cond, names
}

// billMeta names the bill on every ledger fact it produces, so a statement line
// says what it was for and links back to it.
func billMeta(meta PostMeta, b finance.Bill) PostMeta {
	meta.Memo = b.Description
	if meta.Memo == "" {
		meta.Memo = "Sem descrição"
	}
	meta.Ref = "bill:" + b.ID
	return meta
}

func str(s string) types.AttributeValue { return &types.AttributeValueMemberS{Value: s} }

func (r *BillRepository) stamp(now time.Time) types.AttributeValue {
	return str(now.UTC().Format(time.RFC3339Nano))
}

// changedSince tells a guard failure that is the bill having moved on (a
// concurrent edit, settle or cancel — the caller re-reads) from one that is an
// account having gone missing.
func (r *BillRepository) changedSince(ctx context.Context, sp space.ResolvedSpace, read *finance.Bill) bool {
	cur, err := r.get(ctx, sp, read.ID)
	if err != nil {
		return false
	}
	return cur.Status != read.Status || len(cur.TransactionIDs) != len(read.TransactionIDs) ||
		cur.AccountID != read.AccountID || cur.Due != read.Due
}

// settlePlan is a bill's settlement built but not sent: the ledger's items with
// the bill's guarded update last, so another fact (an import line's
// reconciliation) can commit with it in one TransactWriteItems.
type settlePlan struct {
	bill  *finance.Bill // as read
	txID  string
	items []types.TransactWriteItem
}

// Settle posts the cash movement for a forecast bill and marks it paid, in one
// transaction guarded by forecastGuard: of two concurrent settlements exactly one
// commits and the other is finance.ErrBillState.
func (r *BillRepository) Settle(ctx context.Context, sp space.ResolvedSpace, billID string, paid billing.Cents, differenceCategoryID string, date brcal.Date, meta PostMeta, now time.Time) (finance.Bill, error) {
	if err := sp.Require(space.Write | space.Settle); err != nil {
		return finance.Bill{}, err
	}
	p, err := r.planSettle(ctx, sp, billID, paid, differenceCategoryID, date, meta, now)
	if err != nil {
		return finance.Bill{}, err
	}
	if err := r.bills.TransactWrite(ctx, p.items); err != nil {
		return finance.Bill{}, r.settleError(ctx, sp, p.bill, err)
	}
	return p.paid(date), nil
}

// paid is the bill after its settlement committed. Built in memory, not
// re-read: the re-read is eventually consistent and could show the bill as
// still a forecast right after it was settled, and whatever is returned is
// what the idempotency layer stores and replays.
func (p settlePlan) paid(date brcal.Date) finance.Bill {
	b := *p.bill
	b.Status, b.PaidDate = finance.BillPaid, date
	b.TransactionIDs = append(append([]string(nil), b.TransactionIDs...), p.txID)
	return b
}

// settleError maps a refused settlement: either the bill moved on since it was
// read (a concurrent edit, settle or cancel) or an account is gone; the bill is
// re-read to tell them apart, so a settle that merely lost a race is not
// reported as an unknown account.
func (r *BillRepository) settleError(ctx context.Context, sp space.ResolvedSpace, read *finance.Bill, err error) error {
	if !onlyConditionFailed(err) {
		return err
	}
	if r.changedSince(ctx, sp, read) {
		return finance.ErrBillState
	}
	return ErrUnknownAccount
}

func (r *BillRepository) planSettle(ctx context.Context, sp space.ResolvedSpace, billID string, paid billing.Cents, differenceCategoryID string, date brcal.Date, meta PostMeta, now time.Time) (settlePlan, error) {
	b, err := r.get(ctx, sp, billID)
	if err != nil {
		return settlePlan{}, err
	}
	if err := b.CanSettle(); err != nil {
		return settlePlan{}, err
	}
	if differenceCategoryID != "" {
		// The gap lands on this category: interest or discount, so an income or
		// expense account — never an asset, a liability or a system account.
		cat, err := r.ledger.getAccount(ctx, sp, differenceCategoryID)
		if errors.Is(err, ErrNotFound) {
			return settlePlan{}, fmt.Errorf("%w: category %s", ErrUnknownAccount, differenceCategoryID)
		}
		if err != nil {
			return settlePlan{}, err
		}
		if (cat.Class != finance.ClassIncome && cat.Class != finance.ClassExpense) || cat.System || cat.Archived {
			return settlePlan{}, fmt.Errorf("%w: the difference needs an active income or expense category", finance.ErrInvalidTransaction)
		}
	}
	sys, _ := finance.DefaultSystemAccounts()
	tx, err := finance.SettleBill(sys, b.Facts(), paid, differenceCategoryID, date)
	if err != nil {
		return settlePlan{}, err
	}
	plan, err := r.ledger.planPost(sp, tx, billMeta(meta, *b), now)
	if err != nil {
		return settlePlan{}, err
	}
	sk := BillSK(billID)
	values := map[string]types.AttributeValue{
		":paid": str(string(finance.BillPaid)),
		":d":    str(date.String()), ":now": r.stamp(now),
		":tx": &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}},
	}
	cond, names := mergeGuard(b, values)
	update := r.bills.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET #status = :paid, paid_date = :d, #n = list_append(#n, :tx), updated_at = :now"+removeSparse,
		cond, names, values)
	return settlePlan{bill: b, txID: plan.TxID, items: append(append([]types.TransactWriteItem(nil), plan.Items...), update)}, nil
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
	plan, err := r.ledger.planPost(sp, tx, billMeta(meta, *b), now)
	if err != nil {
		return finance.Bill{}, err
	}
	sk := BillSK(billID)
	values := map[string]types.AttributeValue{
		":canceled": str(string(finance.BillCanceled)), ":now": r.stamp(now),
		":tx": &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}},
	}
	cond, names := mergeGuard(b, values)
	update := r.bills.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET #status = :canceled, #n = list_append(#n, :tx), updated_at = :now"+removeSparse,
		cond, names, values)
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), update)
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		return finance.Bill{}, billCancelError(err)
	}
	b.Status = finance.BillCanceled
	b.TransactionIDs = append(append([]string(nil), b.TransactionIDs...), plan.TxID)
	return *b, nil
}

// BillEdit is a partial change to a forecast bill. Nil fields stay as they are.
type BillEdit struct {
	Amount      *billing.Cents
	CategoryID  *string
	AccountID   *string
	Description *string
	Due         *brcal.Date
	// AutoSettle switches the daily job's settlement on or off for this bill.
	// Off removes power and needs only write; on is settling.
	AutoSettle *bool
}

// Edit changes a forecast bill. Due date, account, description and auto_settle
// have no ledger effect. An amount or category change adjusts what was recognised
// as one net transaction (finance.AdjustBill), in the same write as the bill.
// Two edits racing cannot both win: the update is guarded by forecastGuard.
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
	if cur.Origin == finance.OriginCardStatement &&
		(e.Amount != nil && *e.Amount != cur.Amount || e.CategoryID != nil && *e.CategoryID != cur.CategoryID) {
		// A statement's amount is its purchases', and its "category" is the card.
		return finance.Bill{}, fmt.Errorf("%w: a statement amount comes from its purchases; correct it with a refund", finance.ErrBillState)
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
	if e.AutoSettle != nil {
		next.AutoSettle = *e.AutoSettle
	}
	if err := next.Validate(); err != nil {
		return finance.Bill{}, err
	}
	// An auto-settling bill is a standing instruction to move cash on its due
	// date. Turning it on, or changing the account, amount or date of one that
	// stays on, is settling. Turning it off only removes power.
	if next.AutoSettle && (!cur.AutoSettle || next.AccountID != cur.AccountID || next.Amount != cur.Amount || next.Due != cur.Due) {
		if err := sp.Require(space.Write | space.Settle); err != nil {
			return finance.Bill{}, err
		}
	}
	switch {
	case cur.Origin == finance.OriginCardStatement && next.AccountID != cur.AccountID:
		// Its "category" is the card (a liability), not an expense: only the
		// account that pays it is checked.
		if err := r.ledger.activeCash(ctx, sp, next.AccountID); err != nil {
			return finance.Bill{}, err
		}
	case cur.Origin != finance.OriginCardStatement && (next.CategoryID != cur.CategoryID || next.AccountID != cur.AccountID):
		if err := r.checkBillAccounts(ctx, sp, next); err != nil {
			return finance.Bill{}, err
		}
	}

	values := map[string]types.AttributeValue{":now": r.stamp(now)}
	cond, names := mergeGuard(cur, values)
	sets := []string{"updated_at = :now"}
	var removes []string
	set := func(attr string, v types.AttributeValue) {
		names["#"+attr] = attr
		values[":"+attr] = v
		sets = append(sets, "#"+attr+" = :"+attr)
	}
	var items []types.TransactWriteItem
	newIDs := append([]string(nil), cur.TransactionIDs...)

	if next.Amount != cur.Amount || next.CategoryID != cur.CategoryID {
		set("amount", numberValue(int64(next.Amount)))
		set("category_id", str(next.CategoryID))
		sys, _ := finance.DefaultSystemAccounts()
		tx, err := finance.AdjustBill(sys, cur.Facts(), next.Facts(), cur.Competence)
		if err != nil {
			return finance.Bill{}, err
		}
		plan, err := r.ledger.planPost(sp, tx, billMeta(meta, next), now)
		if err != nil {
			return finance.Bill{}, err
		}
		items = append(items, plan.Items...)
		values[":tx"] = &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}}
		sets = append(sets, "#n = list_append(#n, :tx)")
		newIDs = append(newIDs, plan.TxID)
	}
	if next.AccountID != cur.AccountID {
		set("account_id", str(next.AccountID))
	}
	if next.Description != cur.Description {
		set("description", str(next.Description))
	}
	if next.Due != cur.Due {
		set("due", str(next.Due.String()))
		set("open_sk", str(OpenSK(next.Due, next.ID)))
	}
	if next.AutoSettle != cur.AutoSettle {
		set("auto_settle", &types.AttributeValueMemberBOOL{Value: next.AutoSettle})
	}
	// The auto-settle work-list keys follow (auto_settle, due): present while the
	// bill auto-settles, absent otherwise.
	if next.AutoSettle {
		if next.AutoSettle != cur.AutoSettle || next.Due != cur.Due {
			_, _, schedPK, schedSK := sparseKeys(sp, next)
			set("schedule_pk", str(schedPK))
			set("schedule_sk", str(schedSK))
		}
	} else if cur.AutoSettle {
		removes = append(removes, "schedule_pk", "schedule_sk")
	}
	if len(sets) == 1 && len(removes) == 0 {
		return *cur, nil // nothing changed
	}
	expr := "SET " + strings.Join(sets, ", ")
	if len(removes) > 0 {
		expr += " REMOVE " + strings.Join(removes, ", ")
	}
	sk := BillSK(billID)
	items = append(items, r.bills.BuildRawUpdateTxItem(sp.PK(), &sk, expr, cond, names, values))
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return finance.Bill{}, finance.ErrBillState
		}
		return finance.Bill{}, err
	}
	next.TransactionIDs = newIDs
	return next, nil
}

// Unsettle undoes a payment: the settlement is reversed and the bill goes back
// to the open list, in one transaction. auto_settle is turned off — if the job
// paid it and the person undid that, paying it again tomorrow would be wrong.
func (r *BillRepository) Unsettle(ctx context.Context, sp space.ResolvedSpace, billID string, date brcal.Date, meta PostMeta, now time.Time) (finance.Bill, error) {
	if err := sp.Require(space.Write | space.Settle); err != nil {
		return finance.Bill{}, err
	}
	b, err := r.get(ctx, sp, billID)
	if err != nil {
		return finance.Bill{}, err
	}
	if b.Status != finance.BillPaid || len(b.TransactionIDs) == 0 {
		return finance.Bill{}, fmt.Errorf("%w: only a paid bill can be reopened", finance.ErrBillState)
	}
	settlement := b.TransactionIDs[len(b.TransactionIDs)-1]
	plan, err := r.ledger.planReverse(ctx, sp, settlement, date, billMeta(meta, *b), now)
	if err != nil {
		return finance.Bill{}, err
	}
	reopened := *b
	reopened.Status, reopened.PaidDate, reopened.AutoSettle = finance.BillForecast, brcal.Date{}, false
	openPK, openSK, _, _ := sparseKeys(sp, reopened)
	sk := BillSK(billID)
	values := map[string]types.AttributeValue{
		":forecast": str(string(finance.BillForecast)), ":paid": str(string(finance.BillPaid)),
		":n":  &types.AttributeValueMemberN{Value: fmt.Sprint(len(b.TransactionIDs))},
		":op": str(openPK), ":os": str(openSK), ":f": &types.AttributeValueMemberBOOL{Value: false},
		":now": r.stamp(now),
		":tx":  &types.AttributeValueMemberL{Value: []types.AttributeValue{str(plan.TxID)}},
	}
	names := map[string]string{"#status": "status", "#n": "transaction_ids"}
	update := r.bills.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET #status = :forecast, auto_settle = :f, open_pk = :op, open_sk = :os, #n = list_append(#n, :tx), updated_at = :now REMOVE paid_date",
		"#status = :paid AND size(#n) = :n", names, values)
	items := append(append([]types.TransactWriteItem(nil), plan.Items...), update)
	if err := r.bills.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			return finance.Bill{}, finance.ErrBillState
		}
		return finance.Bill{}, err
	}
	reopened.TransactionIDs = append(append([]string(nil), b.TransactionIDs...), plan.TxID)
	return reopened, nil
}

// statementBillItem is the write that creates a card statement's bill
// (scope decision 6 of the 6.5 plan): the bill row only, conditional on its
// absence — a statement bill is not recognised, because the purchases already
// put the expense in the DRE. Settling it clears the card (BillFacts.Clears).
func (r *BillRepository) statementBillItem(sp space.ResolvedSpace, b finance.Bill, now time.Time) (types.TransactWriteItem, error) {
	if err := b.Validate(); err != nil {
		return types.TransactWriteItem{}, err
	}
	item, err := Encode(newBillItem(sp, b, now))
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.bills.BuildPutTxItemIfAbsent(item), nil
}

// OccurrenceBill is a bill a recurrence made, with the nominal day it was made
// for (the occurrence's identity).
type OccurrenceBill struct {
	Nominal brcal.Date
	Bill    finance.Bill
}

// ForRecurrence returns the latest `limit` bills a recurrence made, oldest
// first: one Query on its OCCURRENCE# lock rows inside the space (newest first,
// limited), then one read per bill. Another space's recurrence id finds no lock
// rows here. A lock whose bill is missing is skipped rather than failing the
// whole history.
// ponytail: a BatchGetItem if a recurrence's history ever needs more than a
// screenful of rows.
func (r *BillRepository) ForRecurrence(ctx context.Context, sp space.ResolvedSpace, recurrenceID string, limit int) ([]OccurrenceBill, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	res, err := r.bills.Query(ctx, QueryOpts{
		PK: sp.PK(), SKPrefix: "OCCURRENCE#" + recurrenceID + "#", ScanIndexForward: false, Limit: limit, ConsistentRead: true,
	})
	if err != nil {
		return nil, err
	}
	locks, err := DecodeItems[struct {
		SK     string `dynamodbav:"sk"`
		BillID string `dynamodbav:"bill_id"`
	}](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]OccurrenceBill, 0, len(locks))
	for i := len(locks) - 1; i >= 0; i-- {
		l := locks[i]
		nominal, err := brcal.Parse(l.SK[strings.LastIndexByte(l.SK, '#')+1:])
		if err != nil {
			return nil, fmt.Errorf("malformed occurrence lock %q: %w", l.SK, err)
		}
		b, err := r.get(ctx, sp, l.BillID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, OccurrenceBill{Nominal: nominal, Bill: *b})
	}
	return out, nil
}

// MadeAfter returns every bill a recurrence made for a nominal date after end,
// oldest first: one range Query on its OCCURRENCE# lock rows inside the space
// (all of them, paginated), then one read per bill. It is what ending the
// recurrence cancels (UX batch 5): the job materialises at most two months
// ahead, so these are few. Another space's recurrence id finds nothing here.
func (r *BillRepository) MadeAfter(ctx context.Context, sp space.ResolvedSpace, recurrenceID string, end brcal.Date) ([]OccurrenceBill, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	prefix := "OCCURRENCE#" + recurrenceID + "#"
	items, err := r.ledger.queryRange(ctx, r.bills, sp.PK(), prefix+end.AddDays(1).String(), prefix+"9999-12-31")
	if err != nil {
		return nil, err
	}
	locks, err := DecodeItems[struct {
		SK     string `dynamodbav:"sk"`
		BillID string `dynamodbav:"bill_id"`
	}](items)
	if err != nil {
		return nil, err
	}
	out := make([]OccurrenceBill, 0, len(locks))
	for _, l := range locks {
		nominal, err := brcal.Parse(strings.TrimPrefix(l.SK, prefix))
		if err != nil {
			return nil, fmt.Errorf("malformed occurrence lock %q: %w", l.SK, err)
		}
		b, err := r.get(ctx, sp, l.BillID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, OccurrenceBill{Nominal: nominal, Bill: *b})
	}
	return out, nil
}
