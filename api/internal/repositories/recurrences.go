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

// ErrRecurrenceChanged is a materialisation that was built from a snapshot of a
// recurrence that has since been archived or retargeted. The job skips it and
// works from the current rule on its next run.
var ErrRecurrenceChanged = errors.New("recurrence changed since it was read")

// ErrRecurrenceWouldEnd is an edit of the end date that leaves the recurrence
// nothing to come (finance.Recurrence.Ended) sent without Archive: the person
// has not confirmed that this ends it, so nothing is saved (UX batch 3).
var ErrRecurrenceWouldEnd = errors.New("this end date leaves the recurrence with no occurrence to come")

// catchUpSlackDays is the month of slack the first materialisation and an edit
// allow on the creation-time bound, so a recurrence created on the last day of a
// month with the oldest allowed Start does not become a poison row when the
// month rolls over.
const catchUpSlackDays = 31

// RecurrenceRepository stores recurrence rules (spec § 3.5). A rule produces
// bills through the daily job; editing one never touches bills that exist.
type RecurrenceRepository struct {
	recs   Base
	ledger *LedgerRepository
}

func NewRecurrenceRepository(db *dynamodb.Client, cfg *config.Config) *RecurrenceRepository {
	return &RecurrenceRepository{recs: NewBase(db, cfg, TableRecurrences), ledger: NewLedgerRepository(db, cfg)}
}

type recurrenceItem struct {
	keys
	ID          string `dynamodbav:"id"`
	Direction   string `dynamodbav:"direction"`
	Amount      int64  `dynamodbav:"amount"`
	CategoryID  string `dynamodbav:"category_id"`
	AccountID   string `dynamodbav:"account_id"`
	Description string `dynamodbav:"description,omitempty"`
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
	return finance.Recurrence{
		ID: i.ID, Direction: finance.Direction(i.Direction), Amount: billing.Cents(i.Amount),
		CategoryID: i.CategoryID, AccountID: i.AccountID, Description: i.Description,
		Schedule:   finance.Schedule{Expression: expr, Start: start, End: end, Adjust: finance.BusinessDayAdjust(i.Adjust)},
		AutoSettle: i.AutoSettle, Archived: i.Archived,
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
		Expression: string(expr), Start: rec.Schedule.Start.String(), End: end, Adjust: string(rec.Schedule.Adjust),
		AutoSettle: rec.AutoSettle, Archived: rec.Archived, LastNominal: last, SchedulePK: pk, ScheduleSK: sk,
	}, nil
}

// Create validates a recurrence (including the bounded catch-up) and stores it.
func (r *RecurrenceRepository) Create(ctx context.Context, sp space.ResolvedSpace, rec finance.Recurrence, meta PostMeta, now time.Time) (finance.Recurrence, error) {
	need := space.Write
	if rec.AutoSettle {
		// auto_settle makes the daily job settle on the user's behalf: choosing
		// it is settling, and needs the same verb.
		need |= space.Settle
	}
	if err := sp.Require(need); err != nil {
		return finance.Recurrence{}, err
	}
	rec.ID, rec.Archived = id.New(), false
	if meta.IdempotencyKey != "" {
		rec.ID = idempotentID(sp, "recurrence", meta.IdempotencyKey)
	}
	if err := rec.ValidateAt(brcal.FromTime(now)); err != nil {
		return finance.Recurrence{}, err
	}
	if err := checkDirectionAccounts(ctx, r.ledger, sp, rec.Direction, rec.CategoryID, rec.AccountID, finance.ErrInvalidRecurrence); err != nil {
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
	items, err := r.ledger.queryPrefix(ctx, r.recs, sp.PK(), "RECURRENCE#")
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
	items, err := r.ledger.queryPrefix(ctx, r.recs, sp.PK(), "RECURRENCE#")
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
	End         *brcal.Date
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
	if p.AccountID != nil {
		rec.AccountID = *p.AccountID
	}
	if p.Description != nil {
		rec.Description = *p.Description
	}
	if p.AutoSettle != nil {
		rec.AutoSettle = *p.AutoSettle
	}
	if p.End != nil {
		rec.Schedule.End = *p.End
	}
	// An auto-settling recurrence is a standing instruction the daily job carries
	// out on the user's behalf; changing what it pays, from where, is settling.
	// Turning auto_settle OFF only removes power and needs no extra verb.
	endExtended := p.End != nil && !before.Schedule.End.IsZero() && (rec.Schedule.End.IsZero() || rec.Schedule.End.After(before.Schedule.End))
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
	if p.End != nil && !p.Archive && !rec.Archived && rec.Ended(cursor, brcal.FromTime(now)) {
		return ErrRecurrenceWouldEnd
	}
	if p.Archive {
		rec.Archived = true
	}
	if err := checkDirectionAccounts(ctx, r.ledger, sp, rec.Direction, rec.CategoryID, rec.AccountID, finance.ErrInvalidRecurrence); err != nil {
		return err
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
	set("account_id", str(rec.AccountID))
	set("description", str(rec.Description))
	set("auto_settle", &types.AttributeValueMemberBOOL{Value: rec.AutoSettle})
	if p.End != nil {
		set("end", str(rec.Schedule.End.String()))
	}
	if p.Archive {
		set("archived", &types.AttributeValueMemberBOOL{Value: true})
	}
	remove := ""
	if pk, sk := scheduleKeysFor(sp, rec, cursor); pk != "" {
		set("schedule_pk", str(pk))
		set("schedule_sk", str(sk))
	} else {
		remove = " REMOVE schedule_pk, schedule_sk"
	}
	cond := "attribute_exists(pk) AND attribute_not_exists(last_nominal)"
	if !cursor.IsZero() {
		cond = "attribute_exists(pk) AND last_nominal = :cursor"
		values[":cursor"] = str(cursor.String())
	}
	sk := RecurrenceSK(recID)
	err = r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET "+strings.Join(sets, ", ")+remove, cond, names, values)))
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
	remove := ""
	values := map[string]types.AttributeValue{":c": str(cursor.String()), ":now": str(now.UTC().Format(time.RFC3339Nano))}
	if pk, sk := scheduleKeysFor(sp, rec, cursor); pk != "" {
		set += ", schedule_pk = :spk, schedule_sk = :ssk"
		values[":spk"], values[":ssk"] = str(pk), str(sk)
	} else {
		remove = " REMOVE schedule_pk, schedule_sk"
	}
	sk := RecurrenceSK(recID)
	err = r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk, set+remove,
		"attribute_exists(pk) AND (attribute_not_exists(last_nominal) OR last_nominal < :c)", nil, values)))
	if err != nil && onlyConditionFailed(err) {
		return nil // already at or past this cursor
	}
	return err
}
