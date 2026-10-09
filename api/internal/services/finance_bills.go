package services

import (
	"context"
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
