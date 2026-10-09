package repositories

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// CardRepository stores a space's credit cards (spec § 3.6). A card's money is
// a liability account in the ledger with the same id; this table holds when its
// statements close and are due, which account pays them, the open month, and
// per card its purchases, closed statements and statement items.
type CardRepository struct {
	cards  Base
	ledger *LedgerRepository
	bills  *BillRepository
}

func NewCardRepository(db *dynamodb.Client, cfg *config.Config) *CardRepository {
	return &CardRepository{cards: NewBase(db, cfg, TableCards), ledger: NewLedgerRepository(db, cfg), bills: NewBillRepository(db, cfg)}
}

// CardRow is a card with its account's name and state.
type CardRow struct {
	finance.Card
	Name     string
	Archived bool
	Balance  int64
	// Version moves on every write that adds or removes statement items, so a
	// close that summed the items before such a write fails instead of freezing
	// a total without it.
	Version int64
}

type cardItem struct {
	keys
	ID              string `dynamodbav:"id"`
	ClosingDay      int    `dynamodbav:"closing_day"`
	DueDay          int    `dynamodbav:"due_day"`
	PayingAccountID string `dynamodbav:"paying_account_id"`
	OpenMonth       string `dynamodbav:"open_month"`
	// Brand and Last4 identify the card to the person (UX batch 3); optional.
	Brand      string `dynamodbav:"brand,omitempty"`
	Last4      string `dynamodbav:"last4,omitempty"`
	Version    int64  `dynamodbav:"version"`
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

func (i cardItem) card() (finance.Card, error) {
	open, err := finance.ParseMonth(i.OpenMonth)
	if err != nil {
		return finance.Card{}, fmt.Errorf("card %s has a malformed open month: %w", i.ID, err)
	}
	return finance.Card{ID: i.ID, ClosingDay: i.ClosingDay, DueDay: i.DueDay, PayingAccountID: i.PayingAccountID, OpenMonth: open,
		Brand: finance.CardBrand(i.Brand), Last4: i.Last4}, nil
}

func closeScheduleSK(sp space.ResolvedSpace, c finance.Card) string {
	return ScheduleSK(finance.ClosingDate(c.OpenMonth, c.ClosingDay), sp.Owner(), c.ID)
}

// CreateCard adds a card: its liability account in the ledger and its settings
// row here, in one transaction across the two tables. The first open statement
// is the month of `now`.
func (r *CardRepository) CreateCard(ctx context.Context, sp space.ResolvedSpace, name string, c finance.Card, now time.Time) (CardRow, error) {
	if err := sp.Require(space.Configure); err != nil {
		return CardRow{}, err
	}
	c.ID = id.New()
	c.OpenMonth = finance.MonthOf(brcal.FromTime(now))
	if err := c.Validate(); err != nil {
		return CardRow{}, err
	}
	if err := r.ledger.activeCash(ctx, sp, c.PayingAccountID); err != nil {
		return CardRow{}, err
	}
	acct := finance.LedgerAccount{ID: c.ID, Name: name, Class: finance.ClassLiability}
	if err := acct.Validate(); err != nil {
		return CardRow{}, err
	}
	accountRow, err := Encode(newAccountItem(sp, acct, now))
	if err != nil {
		return CardRow{}, err
	}
	cardRow, err := Encode(cardItem{
		keys: newKeys(sp.PK(), CardSK(c.ID), RetentionPermanent, now),
		ID:   c.ID, ClosingDay: c.ClosingDay, DueDay: c.DueDay, PayingAccountID: c.PayingAccountID,
		OpenMonth: c.OpenMonth.String(), Brand: string(c.Brand), Last4: c.Last4, SchedulePK: ClosePK(sp.Livemode()), ScheduleSK: closeScheduleSK(sp, c),
	})
	if err != nil {
		return CardRow{}, err
	}
	if err := r.cards.TransactWrite(ctx, []types.TransactWriteItem{
		r.ledger.accounts.BuildPutTxItemIfAbsent(accountRow),
		r.cards.BuildPutTxItemIfAbsent(cardRow),
	}); err != nil {
		return CardRow{}, err
	}
	return CardRow{Card: c, Name: name}, nil
}

func (r *CardRepository) loadCard(ctx context.Context, sp space.ResolvedSpace, cardID string) (CardRow, error) {
	raw, err := r.cards.GetItem(ctx, sp.PK(), CardSK(cardID))
	if err != nil {
		return CardRow{}, err
	}
	if raw == nil {
		return CardRow{}, ErrNotFound
	}
	it, err := Decode[cardItem](raw)
	if err != nil {
		return CardRow{}, err
	}
	c, err := it.card()
	if err != nil {
		return CardRow{}, err
	}
	row := CardRow{Card: c, Version: it.Version}
	acct, err := r.ledger.getAccount(ctx, sp, cardID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return CardRow{}, err
	}
	if acct != nil {
		row.Name, row.Archived, row.Balance = acct.Name, acct.Archived, int64(acct.Balance)
	}
	return row, nil
}

// GetCard reads one card of the space; another space's id is ErrNotFound.
func (r *CardRepository) GetCard(ctx context.Context, sp space.ResolvedSpace, cardID string) (*CardRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	row, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListCards returns the space's cards: one prefix Query here and one on the
// chart for their names and balances.
func (r *CardRepository) ListCards(ctx context.Context, sp space.ResolvedSpace) ([]CardRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.ledger.queryPrefix(ctx, r.cards, sp.PK(), "CARD#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[cardItem](items)
	if err != nil {
		return nil, err
	}
	accounts, err := r.ledger.ListAccounts(ctx, sp)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]AccountRow, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	out := make([]CardRow, 0, len(rows))
	for _, it := range rows {
		c, err := it.card()
		if err != nil {
			return nil, err
		}
		a := byID[c.ID]
		out = append(out, CardRow{Card: c, Name: a.Name, Archived: a.Archived, Balance: int64(a.Balance), Version: it.Version})
	}
	return out, nil
}

// CardPatch is a partial change to a card's settings.
type CardPatch struct {
	ClosingDay, DueDay *int
	PayingAccountID    *string
	// Brand and Last4: nil keeps, "" clears.
	Brand, Last4 *string
}

// UpdateCard changes when the card closes and is due and which account pays
// it. Statements already closed keep their dates; the open one closes on the
// new day.
func (r *CardRepository) UpdateCard(ctx context.Context, sp space.ResolvedSpace, cardID string, p CardPatch, now time.Time) (CardRow, error) {
	if err := sp.Require(space.Configure); err != nil {
		return CardRow{}, err
	}
	row, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return CardRow{}, err
	}
	if p.ClosingDay != nil {
		row.ClosingDay = *p.ClosingDay
	}
	if p.DueDay != nil {
		row.DueDay = *p.DueDay
	}
	if p.PayingAccountID != nil && *p.PayingAccountID != row.PayingAccountID {
		if err := r.ledger.activeCash(ctx, sp, *p.PayingAccountID); err != nil {
			return CardRow{}, err
		}
		row.PayingAccountID = *p.PayingAccountID
	}
	if p.Brand != nil {
		row.Brand = finance.CardBrand(*p.Brand)
	}
	if p.Last4 != nil {
		row.Last4 = *p.Last4
	}
	if err := row.Card.Validate(); err != nil {
		return CardRow{}, err
	}
	values := map[string]types.AttributeValue{
		":c": numberValue(int64(row.ClosingDay)), ":d": numberValue(int64(row.DueDay)), ":p": str(row.PayingAccountID),
		":ssk": str(closeScheduleSK(sp, row.Card)), ":now": str(now.UTC().Format(time.RFC3339Nano)),
	}
	set := "SET closing_day = :c, due_day = :d, paying_account_id = :p, schedule_sk = :ssk, updated_at = :now"
	var remove []string
	// An attribute cleared is removed, never stored as "", so the row reads the
	// same as a card that never had it.
	for attr, v := range map[string]string{"brand": string(row.Brand), "last4": row.Last4} {
		if v == "" {
			remove = append(remove, attr)
			continue
		}
		set += ", " + attr + " = :" + attr
		values[":"+attr] = str(v)
	}
	if len(remove) > 0 {
		sort.Strings(remove)
		set += " REMOVE " + strings.Join(remove, ", ")
	}
	sk := CardSK(cardID)
	err = r.cards.TransactWrite(ctx, txItems(r.cards.BuildRawUpdateTxItem(sp.PK(), &sk, set, "attribute_exists(pk)", nil, values)))
	if err != nil {
		return CardRow{}, err
	}
	return row, nil
}
