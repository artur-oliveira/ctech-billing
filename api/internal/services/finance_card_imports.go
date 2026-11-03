package services

import (
	"context"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// UploadCard parses an Inter credit card statement and writes its lines, pending,
// under a new import of the card's open statement `month`.
func (s *FinanceImports) UploadCard(ctx context.Context, sp space.ResolvedSpace, cardID string, month finance.Month, file []byte, idempotencyKey string, now time.Time) (repositories.Import, error) {
	if err := sp.Require(space.Import); err != nil {
		return repositories.Import{}, err
	}
	lines, rejected, err := statement.ParseInterCard(file)
	if err != nil {
		return repositories.Import{}, err
	}
	return s.repo.ImportCard(ctx, sp, cardID, month, lines, rejected, idempotencyKey, now)
}

func (s *FinanceImports) CardSuggestions(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (repositories.Import, []repositories.ImportLine, []statement.CardSuggestion, error) {
	return s.repo.CardSuggestions(ctx, sp, importID, now)
}

func (s *FinanceImports) ConfirmPair(ctx context.Context, sp space.ResolvedSpace, importID string, lines []int, purchaseID, tag string, now time.Time) ([]repositories.ImportLine, error) {
	return s.repo.ConfirmPair(ctx, sp, importID, lines, purchaseID, tag, now)
}

func (s *FinanceImports) ConfirmAll(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (int, error) {
	return s.repo.ConfirmAll(ctx, sp, importID, now)
}

func (s *FinanceImports) CreateFromCardLine(ctx context.Context, sp space.ResolvedSpace, importID string, n int, categoryID, description string, meta repositories.PostMeta, now time.Time) (repositories.ImportLine, error) {
	return s.repo.CreateFromCardLine(ctx, sp, importID, n, categoryID, description, meta, now)
}

func (s *FinanceImports) GetBankCategories(ctx context.Context, sp space.ResolvedSpace, cardID string) (map[string]string, error) {
	return s.repo.GetBankCategories(ctx, sp, cardID)
}

func (s *FinanceImports) PutBankCategories(ctx context.Context, sp space.ResolvedSpace, cardID string, m map[string]string, now time.Time) error {
	return s.repo.PutBankCategories(ctx, sp, cardID, m, now)
}
