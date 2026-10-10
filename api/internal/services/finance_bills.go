package services

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// FinanceBills is the console's bill use cases. The rules live in the domain
// (finance.Bill, the posting rules) and the atomicity in BillRepository; this
// only fixes who the audit says did it. The actor is the token's subject, never
// something the request body supplies.
type FinanceBills struct {
	repo *repositories.BillRepository
}

func NewFinanceBills(repo *repositories.BillRepository) *FinanceBills {
	return &FinanceBills{repo: repo}
}

func meta(origin, actor, requestID string) repositories.PostMeta {
	return repositories.PostMeta{Origin: origin, Actor: actor, RequestID: requestID}
}

// Create makes a manual bill. idempotencyKey, when the request carried one,
// names the bill, so a double submit racing the first request makes one bill.
func (s *FinanceBills) Create(ctx context.Context, sp space.ResolvedSpace, b finance.Bill, actor, requestID, idempotencyKey string, now time.Time) (finance.Bill, error) {
	m := meta("manual", actor, requestID)
	m.IdempotencyKey = idempotencyKey
	return s.repo.Create(ctx, sp, b, m, now)
}

func (s *FinanceBills) Get(ctx context.Context, sp space.ResolvedSpace, id string) (*finance.Bill, error) {
	return s.repo.Get(ctx, sp, id)
}

func (s *FinanceBills) ListOpen(ctx context.Context, sp space.ResolvedSpace, dir finance.Direction, limit int, start map[string]types.AttributeValue) (*repositories.Page[finance.Bill], error) {
	return s.repo.ListOpen(ctx, sp, dir, limit, start)
}

func (s *FinanceBills) Edit(ctx context.Context, sp space.ResolvedSpace, id string, e repositories.BillEdit, actor, requestID string, now time.Time) (finance.Bill, error) {
	return s.repo.Edit(ctx, sp, id, e, brcal.Date{}, meta("manual", actor, requestID), now)
}

// Settle pays or receives a bill. paid and date default to the bill's own amount
// and today when zero.
func (s *FinanceBills) Settle(ctx context.Context, sp space.ResolvedSpace, id string, paid billing.Cents, differenceCategoryID string, date brcal.Date, actor, requestID string, now time.Time) (finance.Bill, error) {
	if paid == 0 || date.IsZero() {
		b, err := s.repo.Get(ctx, sp, id)
		if err != nil {
			return finance.Bill{}, err
		}
		if paid == 0 {
			paid = b.Amount
		}
		if date.IsZero() {
			date = brcal.FromTime(now)
		}
	}
	return s.repo.Settle(ctx, sp, id, paid, differenceCategoryID, date, meta("manual", actor, requestID), now)
}

func (s *FinanceBills) Cancel(ctx context.Context, sp space.ResolvedSpace, id string, actor, requestID string, now time.Time) (finance.Bill, error) {
	return s.repo.Cancel(ctx, sp, id, brcal.Date{}, meta("manual", actor, requestID), now)
}

// Unsettle undoes a bill's payment, dated today, and returns it to the open list.
func (s *FinanceBills) Unsettle(ctx context.Context, sp space.ResolvedSpace, id string, actor, requestID string, now time.Time) (finance.Bill, error) {
	return s.repo.Unsettle(ctx, sp, id, brcal.FromTime(now), meta("unsettle", actor, requestID), now)
}

// ForRecurrence is the latest bills a recurrence made, oldest first (F4's detail).
func (s *FinanceBills) ForRecurrence(ctx context.Context, sp space.ResolvedSpace, recurrenceID string, limit int) ([]repositories.OccurrenceBill, error) {
	return s.repo.ForRecurrence(ctx, sp, recurrenceID, limit)
}

// EndRecurrence is the second half of "Encerrar e arquivar" (UX batch 5, spec
// § 3.5), run after the end and the archive were written (one conditional
// UpdateItem, RecurrenceRepository.Update): it cancels the bills the recurrence
// made for dates after the new end that are not paid. A paid one stays — real
// money moved and the statement shows it. It returns the ids it cancelled.
//
// Ruling: an ordered, re-runnable sequence, not one transaction. Each
// cancellation is the ordinary cancel path (the bill's guarded update plus its
// ledger reversal, ~10 items) and a recurrence can owe several bills, so one
// TransactWriteItems would approach the 100-item limit and would fail as a
// whole when any single bill moved. Archiving first means nothing new is made
// for those dates while the bills are cancelled; the OCCURRENCE# locks stay, so
// nothing is ever made again for them; a retry (the same PATCH) re-runs this
// and finds only what is still open.
func (s *FinanceBills) EndRecurrence(ctx context.Context, sp space.ResolvedSpace, recurrenceID string, end brcal.Date, actor, requestID string, now time.Time) ([]string, error) {
	made, err := s.repo.MadeAfter(ctx, sp, recurrenceID, end)
	if err != nil {
		return nil, err
	}
	return s.CancelOccurrences(ctx, sp, made, actor, requestID, now)
}

// CancelOccurrences cancels each listed bill that is still a forecast, through
// the ordinary cancel path. The list may be stale: Cancel re-reads the bill and
// its guard refuses one that was paid or cancelled meanwhile (finance.ErrBillState),
// which is skipped, never an error. A bill edited between the read and the
// write (same refusal, still a forecast) is tried once more.
func (s *FinanceBills) CancelOccurrences(ctx context.Context, sp space.ResolvedSpace, made []repositories.OccurrenceBill, actor, requestID string, now time.Time) ([]string, error) {
	canceled := []string{}
	for _, m := range made {
		if m.Bill.Status != finance.BillForecast {
			continue
		}
		for attempt := 1; ; attempt++ {
			_, err := s.repo.Cancel(ctx, sp, m.Bill.ID, brcal.Date{}, meta("recurrence_end", actor, requestID), now)
			if err == nil {
				canceled = append(canceled, m.Bill.ID)
				break
			}
			if !errors.Is(err, finance.ErrBillState) {
				return canceled, err
			}
			cur, gerr := s.repo.Get(ctx, sp, m.Bill.ID)
			if gerr != nil {
				return canceled, gerr
			}
			if cur.Status != finance.BillForecast || attempt == 2 {
				break // paid or cancelled meanwhile: it stays as it is
			}
		}
	}
	return canceled, nil
}
