package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.aoctech.app/billing/api/internal/space"
)

// ErrInvalidBankCategories wraps every reason a bank-category mapping is refused.
var ErrInvalidBankCategories = errors.New("invalid bank category mapping")

const (
	maxBankCategories   = 50
	maxBankCategoryName = 60
)

// CardBankCategoriesSK is a card's saved mapping from the bank's own categories
// ("SUPERMERCADO") to the person's expense categories.
func CardBankCategoriesSK(cardID string) string { return "CARDBANKCAT#" + cardID }

type bankCategoriesItem struct {
	keys
	CardID     string            `dynamodbav:"card_id"`
	Categories map[string]string `dynamodbav:"categories"`
}

// GetBankCategories returns the card's mapping, empty when none was saved.
func (r *ImportRepository) GetBankCategories(ctx context.Context, sp space.ResolvedSpace, cardID string) (map[string]string, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	raw, err := r.imports.GetItem(ctx, sp.PK(), CardBankCategoriesSK(cardID))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return map[string]string{}, nil
	}
	it, err := Decode[bankCategoriesItem](raw)
	if err != nil {
		return nil, err
	}
	if it.Categories == nil {
		it.Categories = map[string]string{}
	}
	return it.Categories, nil
}

// PutBankCategories replaces the card's mapping. It is a setting of the space,
// kept as long as the space (no TTL); every target must be an active expense
// category.
func (r *ImportRepository) PutBankCategories(ctx context.Context, sp space.ResolvedSpace, cardID string, m map[string]string, now time.Time) error {
	if err := sp.Require(space.Import); err != nil {
		return err
	}
	if len(m) > maxBankCategories {
		return fmt.Errorf("%w: at most %d entries", ErrInvalidBankCategories, maxBankCategories)
	}
	if _, err := r.cards.loadCard(ctx, sp, cardID); err != nil {
		return err
	}
	for bank, id := range m {
		if bank == "" || len(bank) > maxBankCategoryName {
			return fmt.Errorf("%w: a bank category is 1..%d bytes", ErrInvalidBankCategories, maxBankCategoryName)
		}
		if err := r.cards.expenseCategory(ctx, sp, id); err != nil {
			return err
		}
	}
	item, err := Encode(bankCategoriesItem{keys: newKeys(sp.PK(), CardBankCategoriesSK(cardID), RetentionPermanent, now), CardID: cardID, Categories: m})
	if err != nil {
		return err
	}
	return r.imports.PutItem(ctx, item)
}
