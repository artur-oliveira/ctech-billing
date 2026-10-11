package repositories

import (
	"context"
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// checkCategory refuses a category the reports could not read: a payable's must
// be an active, non-system expense; a receivable's an income.
func checkCategory(ctx context.Context, ledger *LedgerRepository, sp space.ResolvedSpace, dir finance.Direction, categoryID string, invalid error) error {
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
	return nil
}

// checkAssetAccount refuses anything but an active asset account to pay from or into.
func checkAssetAccount(ctx context.Context, ledger *LedgerRepository, sp space.ResolvedSpace, accountID string, invalid error) error {
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

// checkCardTarget refuses a card that is not this space's or is archived. A
// card is a CARD# row in the cards table plus a liability account with the same
// id; the account carries the archived flag.
func checkCardTarget(ctx context.Context, ledger *LedgerRepository, cards Base, sp space.ResolvedSpace, cardID string, invalid error) error {
	raw, err := cards.GetItem(ctx, sp.PK(), CardSK(cardID))
	if err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("%w: card %s", ErrUnknownAccount, cardID)
	}
	acct, err := ledger.getAccount(ctx, sp, cardID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: card %s", ErrUnknownAccount, cardID)
	}
	if err != nil {
		return err
	}
	if acct.Archived {
		return fmt.Errorf("%w: the card is archived; choose another card or an account", invalid)
	}
	return nil
}

// checkTarget is the repository half of a recurrence's validation:
// its category, and the account or the card it is paid with (spec § 3.1).
func (r *RecurrenceRepository) checkTarget(ctx context.Context, sp space.ResolvedSpace, rec finance.Recurrence) error {
	if err := checkCategory(ctx, r.ledger, sp, rec.Direction, rec.CategoryID, finance.ErrInvalidRecurrence); err != nil {
		return err
	}
	if rec.IsCard() {
		return checkCardTarget(ctx, r.ledger, r.cards, sp, rec.CardID, finance.ErrInvalidRecurrence)
	}
	return checkAssetAccount(ctx, r.ledger, sp, rec.AccountID, finance.ErrInvalidRecurrence)
}
