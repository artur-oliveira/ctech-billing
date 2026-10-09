package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// financeActor is the audit actor on everything the daily job posts, matching
// the sweep's "scheduler".
const financeActor = "scheduler"

// jobBatch is how many rows one pass over a work list asks for. The lists are
// date-ordered and a row that keeps failing stays at the head, so the batch is
// large instead of paged: a poison row must not starve the rows behind it.
// ponytail: page the lists with a continuation key if a single day ever holds
// more than this many due rows.
const jobBatch = 5000

// maxDraftsPerRun bounds how many bills one recurrence materialises in one run.
// A recurrence entered with years of catch-up is worked off in batches over
// successive runs (the cursor carries the resume), so one tenant cannot stretch
// the run — and delay every other tenant's auto-settle — for hours.
const maxDraftsPerRun = 60

type billStore interface {
	CreateFromOccurrence(ctx context.Context, sp space.ResolvedSpace, d finance.Draft, recurrenceID string, meta repositories.PostMeta, now time.Time) (bool, finance.Bill, error)
	Settle(ctx context.Context, sp space.ResolvedSpace, id string, paid billing.Cents, differenceCategoryID string, date brcal.Date, meta repositories.PostMeta, now time.Time) (finance.Bill, error)
	DueForAutoSettle(ctx context.Context, livemode bool, today brcal.Date, limit int) ([]repositories.DueBill, int, error)
	ListOpen(ctx context.Context, sp space.ResolvedSpace, dir finance.Direction, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[finance.Bill], error)
}

type recurrenceStore interface {
	DueToMaterialise(ctx context.Context, livemode bool, today brcal.Date, limit int) ([]repositories.DueRecurrence, int, error)
	MarkMaterialised(ctx context.Context, sp space.ResolvedSpace, id string, cursor brcal.Date, now time.Time) error
	ListWithCursors(ctx context.Context, sp space.ResolvedSpace) ([]repositories.DueRecurrence, error)
}

// FinanceJobs is the daily job's logic and the projection read: materialise
// recurrences into bills, auto-settle the bills that ask for it.
type FinanceJobs struct {
	bills billStore
	recs  recurrenceStore
	cards cardCloser
}

func NewFinanceJobs(bills billStore, recs recurrenceStore) *FinanceJobs {
	return &FinanceJobs{bills: bills, recs: recs}
}

// JobResult reports one pass. Done is bills created (Materialise) or settled
// (AutoSettle); Skipped is work that was already done or no longer applies.
type JobResult struct {
	Examined, Done, Skipped, Failed int
	Errors                          []string
}

func (r *JobResult) fail(format string, a ...any) {
	r.Failed++
	r.Errors = append(r.Errors, fmt.Sprintf(format, a...))
}

// Materialise turns every due recurrence's occurrences inside the horizon into
// bills. It is re-runnable by construction: each occurrence is guarded by a lock
// row written in the same transaction as its bill, so a re-run, or a retry after
// a crash before the cursor moved, creates nothing twice.
//
// A recurrence stops at its first failing occurrence and keeps its cursor, so
// the next run resumes there; the other recurrences carry on.
func (j *FinanceJobs) Materialise(ctx context.Context, livemode bool, today brcal.Date, now time.Time) JobResult {
	var res JobResult
	due, skipped, err := j.recs.DueToMaterialise(ctx, livemode, today, jobBatch)
	res.Skipped += skipped
	if err != nil {
		res.fail("reading the materialisation list: %v", err)
		return res
	}
	meta := repositories.PostMeta{Origin: "recurrence", Actor: financeActor}
	_, horizonEnd := finance.Horizon(today)
	for _, d := range due {
		res.Examined++
		rec := d.Recurrence
		drafts, err := rec.Materialise(d.Cursor, today)
		if err != nil {
			res.fail("recurrence %s: %v", rec.ID, err)
			continue
		}
		if len(drafts) > maxDraftsPerRun {
			drafts = drafts[:maxDraftsPerRun]
		}
		cursor, failed := d.Cursor, false
		for _, draft := range drafts {
			created, _, err := j.bills.CreateFromOccurrence(ctx, d.Space, draft, rec.ID, meta, now)
			if errors.Is(err, repositories.ErrRecurrenceChanged) {
				// Archived or retargeted after this run read it: not a failure. The
				// cursor stays, so the next run works from the current rule.
				res.Skipped++
				failed = true
				break
			}
			if err != nil {
				res.fail("recurrence %s occurrence %s: %v", rec.ID, draft.Nominal, err)
				failed = true
				break
			}
			if created {
				res.Done++
			} else {
				res.Skipped++
			}
			cursor = draft.Nominal
		}
		if failed {
			continue
		}
		if len(drafts) == 0 {
			// Due, but nothing falls in (cursor, horizon end]: move the cursor to
			// the horizon end so the row leaves the list instead of being found
			// again every day.
			cursor = horizonEnd
		}
		if err := j.recs.MarkMaterialised(ctx, d.Space, rec.ID, cursor, now); err != nil {
			res.fail("recurrence %s: moving the cursor to %s: %v", rec.ID, cursor, err)
		}
	}
	return res
}

// AutoSettle settles, on each bill's own due date and for its own amount, the
// forecast bills flagged to auto-settle whose due date has arrived. A bill that
// is no longer a forecast (settled or cancelled since the index was read) is a
// skip, not a failure.
func (j *FinanceJobs) AutoSettle(ctx context.Context, livemode bool, today brcal.Date, now time.Time) JobResult {
	var res JobResult
	due, skipped, err := j.bills.DueForAutoSettle(ctx, livemode, today, jobBatch)
	res.Skipped += skipped
	if err != nil {
		res.fail("reading the auto-settle list: %v", err)
		return res
	}
	meta := repositories.PostMeta{Origin: "auto_settle", Actor: financeActor}
	for _, d := range due {
		res.Examined++
		_, err := j.bills.Settle(ctx, d.Space, d.Bill.ID, d.Bill.Amount, "", d.Bill.Due, meta, now)
		switch {
		case err == nil:
			res.Done++
		case errors.Is(err, finance.ErrBillState):
			res.Skipped++
		default:
			res.fail("bill %s: %v", d.Bill.ID, err)
		}
	}
	return res
}

// Projection is F1's forward look: forecast bills by due month, and — apart —
// the net of recurrence occurrences nobody has materialised yet.
type Projection struct {
	Months []ProjectionMonth
}

type ProjectionMonth struct {
	Month      finance.Month
	Receivable billing.Cents // open receivables due this month
	Payable    billing.Cents // open payables due this month
	// Virtual is the net (receivable − payable) of occurrences beyond the
	// materialisation horizon. It is never mixed into the two columns above: a
	// forecast bill exists and can be edited, a virtual occurrence is a rule's
	// promise, and the screen shows them differently.
	Virtual billing.Cents
	// VirtualReceivable and VirtualPayable are the two sides of Virtual, never
	// negative: Virtual == VirtualReceivable - VirtualPayable. The in/out view
	// of the projection needs them apart, and the net alone cannot give them back.
	VirtualReceivable billing.Cents
	VirtualPayable    billing.Cents
}

const maxProjectionMonths = 12

// Project reads every open bill and every recurrence of the space for the next
// `months` months from today's. Bills due before the window (overdue) count in
// the first month. Reads only; nothing is written.
func (j *FinanceJobs) Project(ctx context.Context, sp space.ResolvedSpace, today brcal.Date, months int) (Projection, error) {
	if months < 1 || months > maxProjectionMonths {
		return Projection{}, fmt.Errorf("%w: the projection covers 1..%d months", finance.ErrInvalidRecurrence, maxProjectionMonths)
	}
	first := finance.MonthOf(today)
	window := make([]ProjectionMonth, months)
	index := map[finance.Month]int{}
	for i := range window {
		m := first.Add(i)
		window[i].Month = m
		index[m] = i
	}
	slot := func(d brcal.Date) (int, bool) {
		m := finance.MonthOf(d)
		if m.Compare(first) < 0 {
			return 0, true
		}
		i, ok := index[m]
		return i, ok
	}

	for _, dir := range []finance.Direction{finance.Receivable, finance.Payable} {
		var start map[string]types.AttributeValue
		for {
			page, err := j.bills.ListOpen(ctx, sp, dir, 200, start)
			if err != nil {
				return Projection{}, err
			}
			for _, b := range page.Items {
				if i, ok := slot(b.Due); ok {
					if dir == finance.Receivable {
						window[i].Receivable += b.Amount
					} else {
						window[i].Payable += b.Amount
					}
				}
			}
			if len(page.LastEvaluatedKey) == 0 {
				break
			}
			start = page.LastEvaluatedKey
		}
	}

	cardsEnd := first.Add(months - 1).Last()
	if err := j.projectCards(ctx, sp, cardsEnd, func(due brcal.Date, amount billing.Cents) {
		if i, ok := slot(due); ok {
			window[i].Payable += amount
		}
	}); err != nil {
		return Projection{}, err
	}

	recs, err := j.recs.ListWithCursors(ctx, sp)
	if err != nil {
		return Projection{}, err
	}
	windowEnd := first.Add(months - 1).Last()
	for _, due := range recs {
		r := due.Recurrence
		if r.Archived {
			continue
		}
		// Virtual is everything after the recurrence's own materialisation
		// cursor: what the job has made is already a bill above. Counting from
		// the horizon instead left the first months empty for a recurrence the
		// job had not reached yet (created today, materialised tomorrow).
		from := r.Schedule.Start
		if !due.Cursor.IsZero() {
			from = due.Cursor.AddDays(1)
		}
		occ, err := r.Schedule.Occurrences(from, windowEnd.AddDays(1))
		if err != nil {
			return Projection{}, fmt.Errorf("projecting recurrence %s: %w", r.ID, err)
		}
		for _, o := range occ {
			i, ok := slot(o.Due)
			if !ok {
				continue
			}
			if r.Direction == finance.Receivable {
				window[i].Virtual += r.Amount
				window[i].VirtualReceivable += r.Amount
			} else {
				window[i].Virtual -= r.Amount
				window[i].VirtualPayable += r.Amount
			}
		}
	}
	return Projection{Months: window}, nil
}
