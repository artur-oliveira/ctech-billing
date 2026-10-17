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

	"gopkg.aoctech.app/api-commons/patch"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// removeClause is an update expression's REMOVE part, or nothing.
func removeClause(attrs []string) string {
	if len(attrs) == 0 {
		return ""
	}
	return " REMOVE " + strings.Join(attrs, ", ")
}

// ErrRecurrenceChanged is a materialisation that was built from a snapshot of a
// recurrence that has since been archived or retargeted. The job skips it and
// works from the current rule on its next run.
var ErrRecurrenceChanged = errors.New("recurrence changed since it was read")

// ErrRecurrenceWouldEnd is an edit of the end date that leaves the recurrence
// nothing to come (finance.Recurrence.Ended) sent without Archive: the person
// has not confirmed that this ends it, so nothing is saved (UX batch 3).
var ErrRecurrenceWouldEnd = errors.New("this end date leaves the recurrence with no occurrence to come")

// ErrEndCancelsBills is an end date with unpaid bills made after it, sent
// without cancel_after_end: ending there cancels them, so the person confirms
// first and nothing is saved until then (UX batch 5 review, I2).
var ErrEndCancelsBills = errors.New("this end date cancels unpaid bills the recurrence made after it")

// ErrEndIncomplete is an end whose cancellations did not all happen: the end is
// saved (and the rule archived, if asked) and some unpaid bill after it is
// still open. The same request again finishes it (UX batch 5 review, I3/M2).
var ErrEndIncomplete = errors.New("the recurrence's end was saved, but some bills after it are still open")

// catchUpSlackDays is the month of slack the first materialisation and an edit
// allow on the creation-time bound, so a recurrence created on the last day of a
// month with the oldest allowed Start does not become a poison row when the
// month rolls over.
const catchUpSlackDays = 31

// RecurrenceRepository stores recurrence rules (spec § 3.5). A rule produces
// bills through the daily job; editing one never touches bills that exist.
type RecurrenceRepository struct {
	recs   Base
	bills  Base
	ledger *LedgerRepository
	// cards is read only to check a card target exists in the space.
	cards Base
}

func NewRecurrenceRepository(db *dynamodb.Client, cfg *config.Config) *RecurrenceRepository {
	return &RecurrenceRepository{recs: NewBase(db, cfg, TableRecurrences), bills: NewBase(db, cfg, TableBills), ledger: NewLedgerRepository(db, cfg), cards: NewBase(db, cfg, TableCards)}
}

type PendingOccurrence struct {
	RecurrenceID string
	Nominal      brcal.Date
	Due          brcal.Date
	Direction    finance.Direction
	Expected     billing.Cents
	AccountID    string
	CardID       string
	CategoryID   string
	Description  string
	Installments int
	State        string
}

var ErrOccurrenceAlreadyHandled = errors.New("recurrence occurrence is no longer pending")

type pendingOccurrenceItem struct {
	keys
	RecurrenceID string `dynamodbav:"recurrence_id"`
	Nominal      string `dynamodbav:"nominal"`
	Due          string `dynamodbav:"due"`
	Direction    string `dynamodbav:"direction"`
	Expected     int64  `dynamodbav:"expected_amount"`
	AccountID    string `dynamodbav:"account_id,omitempty"`
	CardID       string `dynamodbav:"card_id,omitempty"`
	CategoryID   string `dynamodbav:"category_id"`
	Description  string `dynamodbav:"description,omitempty"`
	Installments int    `dynamodbav:"installments,omitempty"`
	State        string `dynamodbav:"state"`
}

func (i pendingOccurrenceItem) occurrence() (PendingOccurrence, error) {
	nominal, err := brcal.Parse(i.Nominal)
	if err != nil {
		return PendingOccurrence{}, fmt.Errorf("pending occurrence has malformed nominal date: %w", err)
	}
	due, err := brcal.Parse(i.Due)
	if err != nil {
		return PendingOccurrence{}, fmt.Errorf("pending occurrence has malformed due date: %w", err)
	}
	return PendingOccurrence{RecurrenceID: i.RecurrenceID, Nominal: nominal, Due: due, Direction: finance.Direction(i.Direction), Expected: billing.Cents(i.Expected), AccountID: i.AccountID, CardID: i.CardID, CategoryID: i.CategoryID, Description: i.Description, Installments: i.Installments, State: i.State}, nil
}

func pendingItem(sp space.ResolvedSpace, rec finance.Recurrence, nominal, due brcal.Date, now time.Time) (pendingOccurrenceItem, error) {
	return pendingOccurrenceItem{keys: newKeys(sp.PK(), PendingOccurrenceSK(rec.ID, nominal), RetentionPermanent, now), RecurrenceID: rec.ID, Nominal: nominal.String(), Due: due.String(), Direction: string(rec.Direction), Expected: int64(rec.Amount), AccountID: rec.AccountID, CardID: rec.CardID, CategoryID: rec.CategoryID, Description: rec.Description, Installments: rec.Installments, State: "pending_confirmation"}, nil
}

func (r *RecurrenceRepository) CreatePendingOccurrence(ctx context.Context, sp space.ResolvedSpace, rec finance.Recurrence, nominal, due brcal.Date, now time.Time) (bool, error) {
	if err := sp.Require(space.Write); err != nil {
		return false, err
	}
	row, err := pendingItem(sp, rec, nominal, due, now)
	if err != nil {
		return false, err
	}
	item, err := Encode(row)
	if err != nil {
		return false, err
	}
	values := map[string]types.AttributeValue{":f": &types.AttributeValueMemberBOOL{Value: false}, ":amt": numberValue(int64(rec.Amount)), ":cat": str(rec.CategoryID), ":mode": str(string(finance.AmountExpected)), ":nominal": str(nominal.String())}
	condition := "attribute_exists(pk) AND (attribute_not_exists(archived) OR archived = :f) AND (attribute_not_exists(end) OR end >= :nominal) AND amount = :amt AND category_id = :cat AND amount_mode = :mode"
	if rec.CardID != "" {
		condition += " AND card_id = :target"
		values[":target"] = str(rec.CardID)
	} else {
		condition += " AND account_id = :target"
		values[":target"] = str(rec.AccountID)
	}
	check := types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{TableName: aws.String(r.recs.TableName), Key: map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(RecurrenceSK(rec.ID))}, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}}
	if err = r.recs.TransactWrite(ctx, txItems(r.recs.BuildPutTxItemIfAbsent(item), check)); err == nil {
		return true, nil
	}
	if !onlyConditionFailed(err) {
		return false, err
	}
	if _, getErr := r.GetPendingOccurrence(ctx, sp, rec.ID, nominal); getErr == nil {
		return false, nil
	}
	return false, ErrRecurrenceChanged
}

func (r *RecurrenceRepository) GetPendingOccurrence(ctx context.Context, sp space.ResolvedSpace, recID string, nominal brcal.Date) (PendingOccurrence, error) {
	if err := sp.Require(space.Read); err != nil {
		return PendingOccurrence{}, err
	}
	raw, err := r.recs.GetItem(ctx, sp.PK(), PendingOccurrenceSK(recID, nominal))
	if err != nil {
		return PendingOccurrence{}, err
	}
	if raw == nil {
		return PendingOccurrence{}, fmt.Errorf("%w: pending occurrence", ErrNotFound)
	}
	row, err := Decode[pendingOccurrenceItem](raw)
	if err != nil {
		return PendingOccurrence{}, err
	}
	return row.occurrence()
}

func (r *RecurrenceRepository) ListPendingOccurrences(ctx context.Context, sp space.ResolvedSpace, limit int) ([]PendingOccurrence, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 5000 {
		limit = 50
	}
	page, err := r.recs.Query(ctx, QueryOpts{PK: sp.PK(), SKPrefix: "PENDING#", ScanIndexForward: true, Limit: limit, ConsistentRead: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[pendingOccurrenceItem](page.Items)
	if err != nil {
		return nil, err
	}
	out := make([]PendingOccurrence, 0, len(rows))
	for _, row := range rows {
		if row.State != "pending_confirmation" {
			continue
		}
		occurrence, err := row.occurrence()
		if err != nil {
			return nil, err
		}
		out = append(out, occurrence)
	}
	return out, nil
}

func (r *RecurrenceRepository) ListPendingOccurrencesForRecurrence(ctx context.Context, sp space.ResolvedSpace, recID string, limit int) ([]PendingOccurrence, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 50 {
		limit = 12
	}
	page, err := r.recs.Query(ctx, QueryOpts{PK: sp.PK(), SKPrefix: "PENDING#" + recID + "#", ScanIndexForward: false, Limit: limit, ConsistentRead: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[pendingOccurrenceItem](page.Items)
	if err != nil {
		return nil, err
	}
	out := make([]PendingOccurrence, 0, len(rows))
	for _, row := range rows {
		if row.State != "pending_confirmation" {
			continue
		}
		item, err := row.occurrence()
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *RecurrenceRepository) pendingCompletionTxItem(sp space.ResolvedSpace, recID string, nominal brcal.Date) types.TransactWriteItem {
	return pendingDeleteTxItem(r.recs, sp, recID, nominal)
}

func pendingDeleteTxItem(table Base, sp space.ResolvedSpace, recID string, nominal brcal.Date) types.TransactWriteItem {
	return types.TransactWriteItem{Delete: &types.Delete{TableName: aws.String(table.TableName), Key: map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(PendingOccurrenceSK(recID, nominal))}, ConditionExpression: aws.String("#state = :pending"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: map[string]types.AttributeValue{":pending": str("pending_confirmation")}}}
}

func (r *RecurrenceRepository) SkipPendingOccurrence(ctx context.Context, sp space.ResolvedSpace, recID string, nominal brcal.Date, now time.Time) (bool, error) {
	if err := sp.Require(space.Write); err != nil {
		return false, err
	}
	pending, err := r.GetPendingOccurrence(ctx, sp, recID, nominal)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return false, err
		}
		if r.occurrenceWasSkipped(ctx, sp, recID, nominal) {
			return false, nil
		}
		return false, ErrOccurrenceAlreadyHandled
	}
	lock, err := Encode(occurrenceLockRow{keys: newKeys(sp.PK(), OccurrenceSK(recID, nominal), RetentionPermanent, now), State: "skipped", ExpectedAmount: int64(pending.Expected)})
	if err != nil {
		return false, err
	}
	err = r.recs.TransactWrite(ctx, txItems(r.pendingCompletionTxItem(sp, recID, nominal), r.bills.BuildPutTxItemIfAbsent(lock)))
	if err == nil {
		return true, nil
	}
	if !onlyConditionFailed(err) {
		return false, err
	}
	if r.occurrenceWasSkipped(ctx, sp, recID, nominal) {
		return false, nil
	}
	return false, ErrOccurrenceAlreadyHandled
}

func (r *RecurrenceRepository) occurrenceWasSkipped(ctx context.Context, sp space.ResolvedSpace, recID string, nominal brcal.Date) bool {
	raw, err := r.bills.GetItem(ctx, sp.PK(), OccurrenceSK(recID, nominal))
	if err != nil || raw == nil {
		return false
	}
	row, err := Decode[occurrenceLockRow](raw)
	return err == nil && row.State == "skipped"
}

type recurrenceItem struct {
	keys
	ID         string `dynamodbav:"id"`
	Direction  string `dynamodbav:"direction"`
	Amount     int64  `dynamodbav:"amount"`
	AmountMode string `dynamodbav:"amount_mode,omitempty"`
	CategoryID string `dynamodbav:"category_id"`
	AccountID  string `dynamodbav:"account_id,omitempty"`
	CardID     string `dynamodbav:"card_id,omitempty"`
	// Installments is written on every card recurrence (>= 1), never omitted there,
	// so the job's ConditionCheck can compare it.
	Installments   int    `dynamodbav:"installments,omitempty"`
	NeedsAttention string `dynamodbav:"needs_attention,omitempty"`
	Description    string `dynamodbav:"description,omitempty"`
	// Expression is the 6.1 tagged JSON, parsed back through ParseSchedule so a
	// stored row is held to the same rules as a request.
	Expression string `dynamodbav:"expression"`
	Start      string `dynamodbav:"start"`
	End        string `dynamodbav:"end,omitempty"`
	Adjust     string `dynamodbav:"adjust"`
	AutoSettle bool   `dynamodbav:"auto_settle,omitempty"`
	Archived   bool   `dynamodbav:"archived,omitempty"`
	// LastNominal is the materialisation cursor: the nominal day of the last
	// occurrence turned into a bill. Empty until the first run.
	LastNominal string `dynamodbav:"last_nominal,omitempty"`

	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

func (i recurrenceItem) recurrence() (finance.Recurrence, error) {
	expr, err := finance.ParseSchedule([]byte(i.Expression))
	if err != nil {
		return finance.Recurrence{}, fmt.Errorf("recurrence %s has an invalid stored expression: %w", i.ID, err)
	}
	start, err := brcal.Parse(i.Start)
	if err != nil {
		return finance.Recurrence{}, fmt.Errorf("recurrence %s has a malformed start: %w", i.ID, err)
	}
	var end brcal.Date
	if i.End != "" {
		if end, err = brcal.Parse(i.End); err != nil {
			return finance.Recurrence{}, fmt.Errorf("recurrence %s has a malformed end: %w", i.ID, err)
		}
	}
	mode := finance.AmountMode(i.AmountMode)
	if mode == "" {
		mode = finance.AmountFixed
	}
	return finance.Recurrence{
		ID: i.ID, Direction: finance.Direction(i.Direction), Amount: billing.Cents(i.Amount),
		AmountMode: mode,
		CategoryID: i.CategoryID, AccountID: i.AccountID, Description: i.Description,
		Schedule:   finance.Schedule{Expression: expr, Start: start, End: end, Adjust: finance.BusinessDayAdjust(i.Adjust)},
		AutoSettle: i.AutoSettle, Archived: i.Archived,
		CardID: i.CardID, Installments: i.Installments, NeedsAttention: i.NeedsAttention,
	}, nil
}

func (i recurrenceItem) cursor() (brcal.Date, error) {
	if i.LastNominal == "" {
		return brcal.Date{}, nil
	}
	return brcal.Parse(i.LastNominal)
}

// scheduleKeys are the job-index keys of a recurrence at a cursor: present while
// there is a next occurrence to materialise.
func scheduleKeysFor(sp space.ResolvedSpace, rec finance.Recurrence, cursor brcal.Date) (pk, sk string) {
	next, ok := rec.NextMaterialiseDate(cursor)
	if !ok {
		return "", ""
	}
	return MaterialisePK(sp.Livemode()), ScheduleSK(next, sp.Owner(), rec.ID)
}

func newRecurrenceItem(sp space.ResolvedSpace, rec finance.Recurrence, cursor brcal.Date, now time.Time) (recurrenceItem, error) {
	expr, err := finance.MarshalExpression(rec.Schedule.Expression)
	if err != nil {
		return recurrenceItem{}, err
	}
	end, last := "", ""
	if !rec.Schedule.End.IsZero() {
		end = rec.Schedule.End.String()
	}
	if !cursor.IsZero() {
		last = cursor.String()
	}
	pk, sk := scheduleKeysFor(sp, rec, cursor)
	return recurrenceItem{
		keys: newKeys(sp.PK(), RecurrenceSK(rec.ID), RetentionPermanent, now),
		ID:   rec.ID, Direction: string(rec.Direction), Amount: int64(rec.Amount),
		CategoryID: rec.CategoryID, AccountID: rec.AccountID, Description: rec.Description,
		AmountMode: string(rec.AmountMode),
		Expression: string(expr), Start: rec.Schedule.Start.String(), End: end, Adjust: string(rec.Schedule.Adjust),
		AutoSettle: rec.AutoSettle, Archived: rec.Archived, LastNominal: last, SchedulePK: pk, ScheduleSK: sk,
		CardID: rec.CardID, Installments: rec.Installments,
	}, nil
}

// Create validates a recurrence (including the bounded catch-up) and stores it.
func (r *RecurrenceRepository) Create(ctx context.Context, sp space.ResolvedSpace, rec finance.Recurrence, meta PostMeta, now time.Time) (finance.Recurrence, error) {
	if rec.AmountMode == "" {
		rec.AmountMode = finance.AmountFixed
	}
	need := space.Write
	if rec.AutoSettle {
		// auto_settle makes the daily job settle on the user's behalf: choosing
		// it is settling, and needs the same verb.
		need |= space.Settle
	}
	if err := sp.Require(need); err != nil {
		return finance.Recurrence{}, err
	}
	rec.ID, rec.Archived, rec.NeedsAttention = id.New(), false, ""
	if meta.IdempotencyKey != "" {
		rec.ID = idempotentID(sp, "recurrence", meta.IdempotencyKey)
	}
	if err := rec.ValidateAt(brcal.FromTime(now)); err != nil {
		return finance.Recurrence{}, err
	}
	if err := r.checkTarget(ctx, sp, rec); err != nil {
		return finance.Recurrence{}, err
	}
	row, err := newRecurrenceItem(sp, rec, brcal.Date{}, now)
	if err != nil {
		return finance.Recurrence{}, err
	}
	item, err := Encode(row)
	if err != nil {
		return finance.Recurrence{}, err
	}
	if err := r.recs.TransactWrite(ctx, txItems(r.recs.BuildPutTxItemIfAbsent(item))); err != nil {
		if meta.IdempotencyKey != "" && onlyConditionFailed(err) {
			// The same request already created it (a concurrent double submit).
			if existing, gerr := r.Get(ctx, sp, rec.ID); gerr == nil {
				return *existing, nil
			}
		}
		return finance.Recurrence{}, err
	}
	return rec, nil
}

func (r *RecurrenceRepository) load(ctx context.Context, sp space.ResolvedSpace, recID string) (*recurrenceItem, error) {
	raw, err := r.recs.GetItem(ctx, sp.PK(), RecurrenceSK(recID))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: recurrence %s", ErrNotFound, recID)
	}
	return Decode[recurrenceItem](raw)
}

// Get reads a recurrence inside the space.
func (r *RecurrenceRepository) Get(ctx context.Context, sp space.ResolvedSpace, recID string) (*finance.Recurrence, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	row, err := r.load(ctx, sp, recID)
	if err != nil {
		return nil, err
	}
	rec, err := row.recurrence()
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// List returns every recurrence of the space, archived ones included (the
// screen filters; history stays visible).
func (r *RecurrenceRepository) List(ctx context.Context, sp space.ResolvedSpace) ([]finance.Recurrence, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := queryPrefix(ctx, r.recs, sp.PK(), "RECURRENCE#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[recurrenceItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]finance.Recurrence, len(rows))
	for i, row := range rows {
		if out[i], err = row.recurrence(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetWithCursor is Get with the materialisation cursor (zero: nothing made yet).
func (r *RecurrenceRepository) GetWithCursor(ctx context.Context, sp space.ResolvedSpace, recID string) (finance.Recurrence, brcal.Date, error) {
	if err := sp.Require(space.Read); err != nil {
		return finance.Recurrence{}, brcal.Date{}, err
	}
	row, err := r.load(ctx, sp, recID)
	if err != nil {
		return finance.Recurrence{}, brcal.Date{}, err
	}
	rec, err := row.recurrence()
	if err != nil {
		return finance.Recurrence{}, brcal.Date{}, err
	}
	cursor, err := row.cursor()
	return rec, cursor, err
}

// ListWithCursors is List with each recurrence's materialisation cursor: the
// projection counts as virtual only what the job has not made into a bill yet.
func (r *RecurrenceRepository) ListWithCursors(ctx context.Context, sp space.ResolvedSpace) ([]DueRecurrence, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := queryPrefix(ctx, r.recs, sp.PK(), "RECURRENCE#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[recurrenceItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]DueRecurrence, len(rows))
	for i, row := range rows {
		rec, err := row.recurrence()
		if err != nil {
			return nil, err
		}
		cursor, err := row.cursor()
		if err != nil {
			return nil, err
		}
		out[i] = DueRecurrence{Space: sp, Recurrence: rec, Cursor: cursor}
	}
	return out, nil
}

// RecurrencePatch is a partial change. Nil fields stay as they are. The
// expression and the start are not editable: end the recurrence and make a new
// one, so the bills it made keep pointing at a rule that still means the same.
type RecurrencePatch struct {
	Amount      *billing.Cents
	CategoryID  *string
	AccountID   *string
	Description *string
	AutoSettle  *bool
	// CardID / AccountID switch the target (spec § 3.1): a non-empty CardID moves
	// the recurrence to that card (account and auto_settle cleared, installments 1
	// unless sent); a non-empty AccountID moves it back. The switch applies to
	// occurrences after the cursor; what was made or posted stays.
	CardID       *string
	Installments *int
	// End: absent keeps it, null removes it (the rule has no end again and the
	// job picks it up from its cursor), a date replaces it.
	End patch.Optional[brcal.Date]
	// Archive confirms that an End leaving nothing to come ends the recurrence:
	// the end and the archive are then one conditional write. Without it, such
	// an End is ErrRecurrenceWouldEnd.
	Archive bool
}

// Update changes a recurrence for the occurrences not yet materialised; bills
// already made are untouched (spec § 3.5). It is conditioned on the cursor the
// row was read with, so an edit racing the daily job cannot leave a stale
// schedule key.
func (r *RecurrenceRepository) Update(ctx context.Context, sp space.ResolvedSpace, recID string, p RecurrencePatch, now time.Time) error {
	need := space.Write
	if p.AutoSettle != nil && *p.AutoSettle {
		need |= space.Settle // see Create
	}
	if err := sp.Require(need); err != nil {
		return err
	}
	row, err := r.load(ctx, sp, recID)
	if err != nil {
		return err
	}
	rec, err := row.recurrence()
	if err != nil {
		return err
	}
	before := rec
	if p.Amount != nil {
		rec.Amount = *p.Amount
	}
	if p.CategoryID != nil {
		rec.CategoryID = *p.CategoryID
	}
	if p.CardID != nil && p.AccountID != nil && *p.CardID != "" && *p.AccountID != "" {
		return fmt.Errorf("%w: pay with an account or a card, not both", finance.ErrInvalidRecurrence)
	}
	if p.CardID != nil && *p.CardID != "" {
		rec.CardID, rec.AccountID, rec.AutoSettle = *p.CardID, "", false
		if rec.Installments == 0 {
			rec.Installments = 1
		}
	}
	if p.AccountID != nil && *p.AccountID != "" {
		rec.AccountID, rec.CardID, rec.Installments = *p.AccountID, "", 0
	}
	if p.Installments != nil {
		rec.Installments = *p.Installments
	}
	if p.Description != nil {
		rec.Description = *p.Description
	}
	if p.AutoSettle != nil {
		rec.AutoSettle = *p.AutoSettle
	}
	if p.End.Present() {
		// Null leaves the zero date: no end.
		rec.Schedule.End, _ = p.End.Get()
	}
	// An auto-settling recurrence is a standing instruction the daily job carries
	// out on the user's behalf; changing what it pays, from where, is settling.
	// Turning auto_settle OFF only removes power and needs no extra verb.
	endExtended := p.End.Present() && !before.Schedule.End.IsZero() && (rec.Schedule.End.IsZero() || rec.Schedule.End.After(before.Schedule.End))
	if rec.AutoSettle && (rec.Amount != before.Amount || rec.AccountID != before.AccountID || rec.CategoryID != before.CategoryID || endExtended) {
		if err := sp.Require(space.Write | space.Settle); err != nil {
			return err
		}
	}
	cursor, err := row.cursor()
	if err != nil {
		return err
	}
	// The catch-up bound only matters before the first materialisation.
	if cursor.IsZero() {
		err = rec.ValidateAt(brcal.FromTime(now).AddDays(-catchUpSlackDays))
	} else {
		err = rec.Validate()
	}
	if err != nil {
		return err
	}
	if p.End.Present() && !p.Archive && !rec.Archived && rec.Ended(cursor, brcal.FromTime(now)) {
		return ErrRecurrenceWouldEnd
	}
	if p.Archive {
		rec.Archived = true
	}
	if err := r.checkTarget(ctx, sp, rec); err != nil {
		return err
	}
	// Re-opening (an end cleared or moved later) resumes the rule from the
	// current horizon, never from a cursor left behind when the old end passed:
	// the job's catch-up would otherwise make every missed date as an overdue
	// bill and, with auto_settle, settle each one back-dated. The dates between
	// the old end and this month were not owed while the rule had ended. A rule
	// still running (its cursor already at or past the horizon) is untouched.
	next := cursor
	if endExtended && !cursor.IsZero() {
		from, _ := finance.Horizon(brcal.FromTime(now))
		if floor := from.AddDays(-1); cursor.Before(floor) {
			next = floor
		}
	}

	names := map[string]string{}
	values := map[string]types.AttributeValue{":now": str(now.UTC().Format(time.RFC3339Nano))}
	sets := []string{"updated_at = :now"}
	set := func(attr string, v types.AttributeValue) {
		names["#"+attr] = attr
		values[":"+attr] = v
		sets = append(sets, "#"+attr+" = :"+attr)
	}
	set("amount", numberValue(int64(rec.Amount)))
	set("category_id", str(rec.CategoryID))
	set("description", str(rec.Description))
	set("auto_settle", &types.AttributeValueMemberBOOL{Value: rec.AutoSettle})
	var remove []string
	// The target: one of the two is set, the other removed, never stored as "".
	if rec.IsCard() {
		set("card_id", str(rec.CardID))
		set("installments", numberValue(int64(rec.Installments)))
		names["#account_id"] = "account_id"
		remove = append(remove, "#account_id")
	} else {
		set("account_id", str(rec.AccountID))
		names["#card_id"], names["#installments"] = "card_id", "installments"
		remove = append(remove, "#card_id", "#installments")
	}
	// An edit is the person's answer to needs_attention: the next run re-checks.
	names["#needs_attention"] = "needs_attention"
	remove = append(remove, "#needs_attention")
	switch {
	case p.End.IsNull():
		// Removed, never stored as "": the row reads like one that never had an end.
		names["#end"] = "end"
		remove = append(remove, "#end")
	case p.End.Present():
		set("end", str(rec.Schedule.End.String()))
	}
	if p.Archive {
		set("archived", &types.AttributeValueMemberBOOL{Value: true})
	}
	// The job-index keys follow the rule as edited: an end cleared on a rule the
	// job had finished puts it back on the list at its next date after the
	// (re-opened) cursor; an end that leaves nothing after the cursor takes it
	// off. The cursor only ever moves forward here, so nothing already made is
	// made again — and the OCCURRENCE# lock would refuse it if it were.
	if next != cursor {
		set("last_nominal", str(next.String()))
	}
	if pk, sk := scheduleKeysFor(sp, rec, next); pk != "" {
		set("schedule_pk", str(pk))
		set("schedule_sk", str(sk))
	} else {
		remove = append(remove, "schedule_pk", "schedule_sk")
	}
	cond := "attribute_exists(pk) AND attribute_not_exists(last_nominal)"
	if !cursor.IsZero() {
		cond = "attribute_exists(pk) AND last_nominal = :cursor"
		values[":cursor"] = str(cursor.String())
	}
	sk := RecurrenceSK(recID)
	err = r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET "+strings.Join(sets, ", ")+removeClause(remove), cond, names, values)))
	if err != nil && onlyConditionFailed(err) {
		return ErrConcurrentModification
	}
	return err
}

// Archive stops a recurrence from materialising. It is never deleted: the bills
// it made keep pointing at it.
func (r *RecurrenceRepository) Archive(ctx context.Context, sp space.ResolvedSpace, recID string, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	sk := RecurrenceSK(recID)
	err := r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET archived = :t, updated_at = :now REMOVE schedule_pk, schedule_sk", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{
			":t": &types.AttributeValueMemberBOOL{Value: true}, ":now": str(now.UTC().Format(time.RFC3339Nano)),
		})))
	if err != nil && onlyConditionFailed(err) {
		return fmt.Errorf("%w: recurrence %s", ErrNotFound, recID)
	}
	return err
}

// FlagAttention records why the job stopped on a recurrence (card_archived,
// category_archived). The cursor and the schedule keys are untouched, so the job
// finds it again every day and posts the pending occurrences once it is fixed.
func (r *RecurrenceRepository) FlagAttention(ctx context.Context, sp space.ResolvedSpace, recID, reason string, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	sk := RecurrenceSK(recID)
	err := r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET needs_attention = :r, updated_at = :now", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":r": str(reason), ":now": str(now.UTC().Format(time.RFC3339Nano))})))
	if err != nil && onlyConditionFailed(err) {
		return fmt.Errorf("%w: recurrence %s", ErrNotFound, recID)
	}
	return err
}

// DueRecurrence is a recurrence the daily job should materialise.
type DueRecurrence struct {
	Space      space.ResolvedSpace
	Recurrence finance.Recurrence
	Cursor     brcal.Date
}

// DueToMaterialise returns, oldest first, the recurrences whose next occurrence
// has entered the horizon by today. Rows whose schedule owner is not a valid
// space, and rows that vanished since the index was written, are counted in
// skipped and never acted on.
func (r *RecurrenceRepository) DueToMaterialise(ctx context.Context, livemode bool, today brcal.Date, limit int) (due []DueRecurrence, skipped int, err error) {
	entries, skipped, err := scanSchedule(ctx, r.recs, MaterialisePK(livemode), livemode, today, limit)
	if err != nil {
		return nil, skipped, err
	}
	for _, e := range entries {
		row, err := r.load(ctx, e.Space, e.ID)
		if errors.Is(err, ErrNotFound) {
			skipped++
			continue
		}
		if err != nil {
			return due, skipped, err
		}
		rec, err := row.recurrence()
		if err != nil {
			return due, skipped, err
		}
		cursor, err := row.cursor()
		if err != nil {
			return due, skipped, err
		}
		due = append(due, DueRecurrence{Space: e.Space, Recurrence: rec, Cursor: cursor})
	}
	return due, skipped, nil
}

// MarkMaterialised moves the cursor forward and recomputes the schedule key. It
// never moves it back: a second call with the same or an older cursor is a
// successful no-op, which is what makes a re-run harmless.
func (r *RecurrenceRepository) MarkMaterialised(ctx context.Context, sp space.ResolvedSpace, recID string, cursor brcal.Date, now time.Time) error {
	if cursor.IsZero() {
		return nil // nothing made yet: writing it would store "0000-00-00", which no read can parse
	}
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	row, err := r.load(ctx, sp, recID)
	if err != nil {
		return err
	}
	rec, err := row.recurrence()
	if err != nil {
		return err
	}
	set := "SET last_nominal = :c, updated_at = :now"
	remove := " REMOVE needs_attention"
	values := map[string]types.AttributeValue{":c": str(cursor.String()), ":now": str(now.UTC().Format(time.RFC3339Nano))}
	if pk, sk := scheduleKeysFor(sp, rec, cursor); pk != "" {
		set += ", schedule_pk = :spk, schedule_sk = :ssk"
		values[":spk"], values[":ssk"] = str(pk), str(sk)
	} else {
		remove = " REMOVE needs_attention, schedule_pk, schedule_sk"
	}
	sk := RecurrenceSK(recID)
	err = r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk, set+remove,
		"attribute_exists(pk) AND (attribute_not_exists(last_nominal) OR last_nominal < :c)", nil, values)))
	if err != nil && onlyConditionFailed(err) {
		return nil // already at or past this cursor
	}
	return err
}
