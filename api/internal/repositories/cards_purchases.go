package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ErrCardMoved is a write that lost a race with another on the same card (a
// close, a purchase): the client tries again and lands on the open statement.
var ErrCardMoved = errors.New("cards: the card changed while this was written; try again")

// ErrPurchaseRefunded is a refund or an advance of a purchase already refunded.
var ErrPurchaseRefunded = errors.New("cards: the purchase was refunded")

// ErrCardArchived and ErrCategoryUnusable tell the daily job why a card
// occurrence could not post (needs_attention). They are wrapped together with
// ErrUnknownAccount / finance.ErrInvalidTransaction, so the HTTP answers of the
// purchase route do not change.
var (
	ErrCardArchived     = errors.New("cards: the card is archived")
	ErrCategoryUnusable = errors.New("cards: the category cannot take a purchase")
)

// Purchase is one card purchase and where each installment is billed.
type Purchase struct {
	ID, CardID, Description, CategoryID string
	Date                                brcal.Date
	Total                               billing.Cents
	// Installments: on input only its length (the count) is read; the
	// allocation is always the server's.
	Installments []finance.Installment
	TxID         string
	Refunded     bool
	// RecurrenceID and OriginRef are set on a purchase the daily job posted for
	// a card recurrence's occurrence.
	RecurrenceID, OriginRef string
}

// StatementItem is one line of a statement.
type StatementItem struct {
	PurchaseID, Description, CategoryID string
	Date                                brcal.Date
	Number, Of                          int    // 3 of 12; 0 for a credit, an advance or a carry
	Kind                                string // "installment" | "credit" | "advance" | "carry"
	Amount                              billing.Cents
	RecurrenceID                        string // the recurrence that posted it, if any
}

// Statement is one card's statement for one month.
type Statement struct {
	CardID               string
	Month                finance.Month
	Status               string // "open" | "future" | "closed"
	ClosingDate, DueDate brcal.Date
	Total                billing.Cents // frozen when closed; the items' sum otherwise
	BillID               string
	Items                []StatementItem
}

type installmentItem struct {
	Number    int    `dynamodbav:"n"`
	Amount    int64  `dynamodbav:"amount"`
	Statement string `dynamodbav:"statement"`
	// Advanced: moved onto the open statement by an advance, billed there as
	// one "adv" item with the purchase's other advanced installments.
	Advanced bool `dynamodbav:"advanced,omitempty"`
}

type purchaseItem struct {
	keys
	ID           string            `dynamodbav:"id"`
	Description  string            `dynamodbav:"description"`
	CategoryID   string            `dynamodbav:"category_id"`
	Date         string            `dynamodbav:"date"`
	Total        int64             `dynamodbav:"total"`
	Installments []installmentItem `dynamodbav:"installments"`
	TxID         string            `dynamodbav:"tx_id"`
	Refunded     bool              `dynamodbav:"refunded,omitempty"`
	// Rev moves on every change to Installments (an advance) and on every edit
	// (EditPurchase, which may swap TxID), so a refund, an advance or an edit
	// acting on a stale read fails instead of acting on the wrong items or
	// transaction.
	Rev int64 `dynamodbav:"rev,omitempty"`
	// Origin and OriginRef link a purchase the daily job posted to its
	// recurrence occurrence ("recurrence", "{recurrence}@{nominal}").
	Origin       string `dynamodbav:"origin,omitempty"`
	OriginRef    string `dynamodbav:"origin_ref,omitempty"`
	RecurrenceID string `dynamodbav:"recurrence_id,omitempty"`
}

// purchaseGuard is the condition a write acting on a purchase as read carries:
// not refunded, and its installments unchanged since (rev).
func purchaseGuard(read purchaseItem) (string, map[string]types.AttributeValue) {
	if read.Rev == 0 {
		return "attribute_not_exists(refunded) AND attribute_not_exists(rev)", map[string]types.AttributeValue{}
	}
	return "attribute_not_exists(refunded) AND rev = :rev", map[string]types.AttributeValue{":rev": numberValue(read.Rev)}
}

type itemRow struct {
	keys
	PurchaseID   string `dynamodbav:"purchase_id"`
	Description  string `dynamodbav:"description"`
	CategoryID   string `dynamodbav:"category_id,omitempty"`
	Date         string `dynamodbav:"date"`
	Number       int    `dynamodbav:"n,omitempty"`
	Of           int    `dynamodbav:"of,omitempty"`
	Kind         string `dynamodbav:"kind"`
	Amount       int64  `dynamodbav:"amount"`
	RecurrenceID string `dynamodbav:"recurrence_id,omitempty"`
}

type statementRow struct {
	keys
	Total    int64  `dynamodbav:"total"`
	ClosedOn string `dynamodbav:"closed_on"`
	BillID   string `dynamodbav:"bill_id,omitempty"`
}

func (p purchaseItem) purchase(cardID string) (Purchase, error) {
	date, err := brcal.Parse(p.Date)
	if err != nil {
		return Purchase{}, fmt.Errorf("purchase %s has a malformed date: %w", p.ID, err)
	}
	out := Purchase{ID: p.ID, CardID: cardID, Description: p.Description, CategoryID: p.CategoryID, Date: date,
		Total: billing.Cents(p.Total), TxID: p.TxID, Refunded: p.Refunded, RecurrenceID: p.RecurrenceID, OriginRef: p.OriginRef}
	for _, i := range p.Installments {
		m, err := finance.ParseMonth(i.Statement)
		if err != nil {
			return Purchase{}, fmt.Errorf("purchase %s has a malformed statement: %w", p.ID, err)
		}
		out.Installments = append(out.Installments, finance.Installment{Number: i.Number, Amount: billing.Cents(i.Amount), Statement: m})
	}
	return out, nil
}

func installmentItems(plan []finance.Installment, advanced map[int]bool) []installmentItem {
	out := make([]installmentItem, len(plan))
	for i, inst := range plan {
		out[i] = installmentItem{Number: inst.Number, Amount: int64(inst.Amount), Statement: inst.Statement.String(), Advanced: advanced[inst.Number]}
	}
	return out
}

func installmentTag(n int) string { return fmt.Sprintf("%02d", n) }

// cardBump is the guard every write that adds or removes items carries: the
// card's open month is still the one the write allocated against, and its
// version moves, so a close that read the items before this write fails its own
// condition instead of freezing a total without it.
func (r *CardRepository) cardBump(sp space.ResolvedSpace, cardID string, open finance.Month, now time.Time) types.TransactWriteItem {
	sk := CardSK(cardID)
	return r.cards.BuildRawUpdateTxItem(sp.PK(), &sk, "ADD version :one SET updated_at = :now", "open_month = :m", nil,
		map[string]types.AttributeValue{":one": numberValue(1), ":m": str(open.String()), ":now": str(now.UTC().Format(time.RFC3339Nano))})
}

// activeCard loads a card money can still be spent on.
func (r *CardRepository) activeCard(ctx context.Context, sp space.ResolvedSpace, cardID string) (CardRow, error) {
	card, err := r.loadCard(ctx, sp, cardID)
	if errors.Is(err, ErrNotFound) {
		return CardRow{}, fmt.Errorf("%w: card %s", ErrUnknownAccount, cardID)
	}
	if err != nil {
		return CardRow{}, err
	}
	if card.Archived {
		return CardRow{}, fmt.Errorf("%w: %w: card %s", ErrUnknownAccount, ErrCardArchived, cardID)
	}
	return card, nil
}

// AddPurchase records a purchase: the full expense in the ledger on its date
// (finance.CardPurchase) and one item per installment on the statement it is
// billed on, never before the card's open month — a purchase dated in a month
// already closed lands on the open statement (spec § 3.6).
func (r *CardRepository) AddPurchase(ctx context.Context, sp space.ResolvedSpace, cardID string, p Purchase, meta PostMeta, now time.Time) (Purchase, error) {
	return r.addPurchase(ctx, sp, cardID, p, meta, now, nil)
}

// purchaseExtras builds writes a caller adds to a purchase's transaction once
// the purchase's id and plan are known. A cancellation caused by one of them is
// errExtraFailed{index}.
type purchaseExtras func(p Purchase) ([]types.TransactWriteItem, error)

type errExtraFailed struct{ index int }

func (e errExtraFailed) Error() string {
	return fmt.Sprintf("cards: the caller's condition %d failed", e.index)
}

func originOf(p Purchase) string {
	if p.RecurrenceID != "" {
		return "recurrence"
	}
	return ""
}

func (r *CardRepository) addPurchase(ctx context.Context, sp space.ResolvedSpace, cardID string, p Purchase, meta PostMeta, now time.Time, extras purchaseExtras) (Purchase, error) {
	if err := sp.Require(space.Write); err != nil {
		return Purchase{}, err
	}
	card, err := r.activeCard(ctx, sp, cardID)
	if err != nil {
		return Purchase{}, err
	}
	if err := r.expenseCategory(ctx, sp, p.CategoryID); err != nil {
		return Purchase{}, err
	}
	if p.Description == "" {
		cat, err := r.ledger.getAccount(ctx, sp, p.CategoryID)
		if err != nil {
			return Purchase{}, err
		}
		p.Description = cat.Name // a recurrence may have none
	}
	plan, err := finance.PlaceInstallments(p.Total, len(p.Installments), p.Date, card.ClosingDay, card.OpenMonth)
	if err != nil {
		return Purchase{}, err
	}
	tx, err := finance.CardPurchase(card.ID, p.CategoryID, p.Total, p.Date)
	if err != nil {
		return Purchase{}, err
	}
	p.ID, p.CardID, p.Installments = id.New(), card.ID, plan
	if meta.IdempotencyKey != "" {
		p.ID = idempotentID(sp, "purchase", meta.IdempotencyKey)
	}
	meta.Origin, meta.Memo, meta.Ref = "card_purchase", p.Description, "purchase:"+p.ID
	ledgerPlan, err := r.ledger.planPost(sp, tx, meta, now)
	if err != nil {
		return Purchase{}, err
	}
	p.TxID = ledgerPlan.TxID
	pk := CardPK(sp, card.ID)
	row, err := Encode(purchaseItem{
		keys: newKeys(pk, PurchaseSK(p.ID), RetentionPermanent, now),
		ID:   p.ID, Description: p.Description, CategoryID: p.CategoryID, Date: p.Date.String(), Total: int64(p.Total),
		Installments: installmentItems(plan, nil), TxID: p.TxID,
		Origin: originOf(p), OriginRef: p.OriginRef, RecurrenceID: p.RecurrenceID,
	})
	if err != nil {
		return Purchase{}, err
	}
	items := append([]types.TransactWriteItem(nil), ledgerPlan.Items...)
	purchaseIdx := len(items)
	items = append(items, r.cards.BuildPutTxItemIfAbsent(row))
	for _, inst := range plan {
		it, err := Encode(itemRow{
			keys:       newKeys(pk, ItemSK(inst.Statement, p.ID, installmentTag(inst.Number)), RetentionPermanent, now),
			PurchaseID: p.ID, Description: p.Description, CategoryID: p.CategoryID, Date: p.Date.String(),
			Number: inst.Number, Of: len(plan), Kind: "installment", Amount: int64(inst.Amount), RecurrenceID: p.RecurrenceID,
		})
		if err != nil {
			return Purchase{}, err
		}
		items = append(items, r.cards.BuildPutTxItemIfAbsent(it))
	}
	cardIdx := len(items)
	items = append(items, r.cardBump(sp, card.ID, card.OpenMonth, now))
	extraIdx := len(items)
	if extras != nil {
		more, err := extras(p)
		if err != nil {
			return Purchase{}, err
		}
		items = append(items, more...)
	}
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		if onlyConditionFailed(err) {
			for i := extraIdx; i < len(items) && i < len(codes); i++ {
				if codes[i] == codeConditionFailed {
					return Purchase{}, errExtraFailed{index: i - extraIdx}
				}
			}
		}
		switch {
		case !onlyConditionFailed(err):
			return Purchase{}, err
		case cardIdx < len(codes) && codes[cardIdx] == codeConditionFailed:
			return Purchase{}, ErrCardMoved
		case meta.IdempotencyKey != "" && purchaseIdx < len(codes) && codes[purchaseIdx] == codeConditionFailed:
			// The same request again, overlapping the first: answer with it.
			existing, gerr := r.getPurchase(ctx, sp, card.ID, p.ID)
			if gerr != nil {
				return Purchase{}, gerr
			}
			return existing, nil
		}
		return Purchase{}, classifyPostCancel(err, ledgerPlan.MarkerIdx)
	}
	return p, nil
}

// getPurchaseItem reads a purchase strongly consistent: a refund or an advance
// right after another must see its result, or it would act on a plan that no
// longer matches the items.
func (r *CardRepository) getPurchaseItem(ctx context.Context, sp space.ResolvedSpace, cardID, purchaseID string) (purchaseItem, error) {
	res, err := r.cards.Query(ctx, QueryOpts{PK: CardPK(sp, cardID), SKPrefix: PurchaseSK(purchaseID), Limit: 1, ConsistentRead: true})
	if err != nil {
		return purchaseItem{}, err
	}
	var raw map[string]types.AttributeValue
	for _, it := range res.Items {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok && sk.Value == PurchaseSK(purchaseID) {
			raw = it
		}
	}
	if raw == nil {
		return purchaseItem{}, ErrNotFound
	}
	it, err := Decode[purchaseItem](raw)
	if err != nil {
		return purchaseItem{}, err
	}
	return *it, nil
}

func (r *CardRepository) getPurchase(ctx context.Context, sp space.ResolvedSpace, cardID, purchaseID string) (Purchase, error) {
	it, err := r.getPurchaseItem(ctx, sp, cardID, purchaseID)
	if err != nil {
		return Purchase{}, err
	}
	return it.purchase(cardID)
}

// ListPurchases returns a card's purchases, newest first.
func (r *CardRepository) ListPurchases(ctx context.Context, sp space.ResolvedSpace, cardID string) ([]Purchase, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	if _, err := r.loadCard(ctx, sp, cardID); err != nil {
		return nil, err
	}
	raw, err := queryPrefix(ctx, r.cards, CardPK(sp, cardID), "PURCHASE#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[purchaseItem](raw)
	if err != nil {
		return nil, err
	}
	out := make([]Purchase, 0, len(rows))
	for _, row := range rows {
		p, err := row.purchase(cardID)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	slices.SortStableFunc(out, func(a, b Purchase) int { return b.Date.Compare(a.Date) })
	return out, nil
}

// GetStatement returns one month of a card: the frozen statement when closed,
// otherwise its items and their sum. One prefix Query: the statement row and its
// items share the prefix.
func (r *CardRepository) GetStatement(ctx context.Context, sp space.ResolvedSpace, cardID string, m finance.Month) (Statement, error) {
	if err := sp.Require(space.Read); err != nil {
		return Statement{}, err
	}
	card, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return Statement{}, err
	}
	return r.statement(ctx, sp, card, m)
}

func (r *CardRepository) statement(ctx context.Context, sp space.ResolvedSpace, card CardRow, m finance.Month) (Statement, error) {
	raw, err := queryPrefix(ctx, r.cards, CardPK(sp, card.ID), StatementSK(m))
	if err != nil {
		return Statement{}, err
	}
	return buildStatement(card, m, raw)
}

// buildStatement is one month of a card from its rows (the statement row, if
// closed, and the items): the frozen total when closed, the items' sum
// otherwise. Rows of other months are ignored.
func buildStatement(card CardRow, m finance.Month, raw []map[string]types.AttributeValue) (Statement, error) {
	s := Statement{
		CardID: card.ID, Month: m,
		ClosingDate: finance.ClosingDate(m, card.ClosingDay), DueDate: finance.DueDate(m, card.ClosingDay, card.DueDay),
	}
	closed := false
	for _, it := range raw {
		sk, _ := it["sk"].(*types.AttributeValueMemberS)
		if sk != nil && sk.Value == StatementSK(m) {
			row, err := Decode[statementRow](it)
			if err != nil {
				return Statement{}, err
			}
			closed, s.Total, s.BillID = true, billing.Cents(row.Total), row.BillID
			continue
		}
		if sk == nil || !strings.HasPrefix(sk.Value, StatementSK(m)+"#ITEM#") {
			continue
		}
		row, err := Decode[itemRow](it)
		if err != nil {
			return Statement{}, err
		}
		d, err := brcal.Parse(row.Date)
		if err != nil {
			return Statement{}, fmt.Errorf("statement item %s has a malformed date: %w", row.SK, err)
		}
		s.Items = append(s.Items, StatementItem{
			PurchaseID: row.PurchaseID, Description: row.Description, CategoryID: row.CategoryID, Date: d,
			Number: row.Number, Of: row.Of, Kind: row.Kind, Amount: billing.Cents(row.Amount), RecurrenceID: row.RecurrenceID,
		})
	}
	slices.SortStableFunc(s.Items, func(a, b StatementItem) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return strings.Compare(a.PurchaseID, b.PurchaseID)
	})
	switch c := m.Compare(card.OpenMonth); {
	case closed || c < 0:
		s.Status = "closed"
	case c == 0:
		s.Status = "open"
	default:
		s.Status = "future"
	}
	if !closed {
		for _, it := range s.Items {
			s.Total += it.Amount
		}
	}
	return s, nil
}

// Refund (estorno) reverses the purchase in the ledger, dated today, removes
// the installments not yet billed and credits the ones already billed on the
// open statement — a closed statement is never changed (spec § 3.6).
func (r *CardRepository) Refund(ctx context.Context, sp space.ResolvedSpace, cardID, purchaseID string, meta PostMeta, now time.Time) (Purchase, error) {
	if err := sp.Require(space.Write); err != nil {
		return Purchase{}, err
	}
	card, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return Purchase{}, err
	}
	row, err := r.getPurchaseItem(ctx, sp, cardID, purchaseID)
	if err != nil {
		return Purchase{}, err
	}
	if row.Refunded {
		return Purchase{}, ErrPurchaseRefunded
	}
	p, err := row.purchase(cardID)
	if err != nil {
		return Purchase{}, err
	}
	credit, removed := finance.RefundPlan(p.Installments, card.OpenMonth)
	meta.Memo, meta.Ref = "Estorno: "+p.Description, "purchase:"+p.ID
	rev, err := r.ledger.planReverse(ctx, sp, p.TxID, brcal.FromTime(now), meta, now)
	if err != nil {
		return Purchase{}, err
	}
	pk := CardPK(sp, cardID)
	advanced := map[int]bool{}
	for _, i := range row.Installments {
		advanced[i.Number] = i.Advanced
	}
	items := append([]types.TransactWriteItem(nil), rev.Items...)
	deletedAdv := false
	for _, inst := range removed {
		if advanced[inst.Number] {
			if !deletedAdv {
				items = append(items, r.cards.BuildDeleteTxItem(pk, ItemSK(inst.Statement, p.ID, "adv")))
				deletedAdv = true
			}
			continue
		}
		items = append(items, r.cards.BuildDeleteTxItem(pk, ItemSK(inst.Statement, p.ID, installmentTag(inst.Number))))
	}
	if credit > 0 {
		it, err := Encode(itemRow{
			keys:       newKeys(pk, ItemSK(card.OpenMonth, p.ID, "refund"), RetentionPermanent, now),
			PurchaseID: p.ID, Description: "Estorno: " + p.Description, CategoryID: p.CategoryID,
			Date: brcal.FromTime(now).String(), Kind: "credit", Amount: -int64(credit),
		})
		if err != nil {
			return Purchase{}, err
		}
		items = append(items, r.cards.BuildPutTxItemIfAbsent(it))
	}
	sk := PurchaseSK(p.ID)
	purchaseIdx := len(items)
	guard, guardValues := purchaseGuard(row)
	guardValues[":t"], guardValues[":now"] = &types.AttributeValueMemberBOOL{Value: true}, str(now.UTC().Format(time.RFC3339Nano))
	items = append(items, r.cards.BuildRawUpdateTxItem(pk, &sk, "SET refunded = :t, updated_at = :now", guard, nil, guardValues))
	cardIdx := len(items)
	items = append(items, r.cardBump(sp, cardID, card.OpenMonth, now))
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		switch {
		case !onlyConditionFailed(err):
			return Purchase{}, err
		case rev.MarkerIdx >= 0 && rev.MarkerIdx < len(codes) && codes[rev.MarkerIdx] == codeConditionFailed:
			// The transaction read was reversed meanwhile: by a refund, or by a
			// category edit that replaced it (then try again with the new one).
			if again, gerr := r.getPurchaseItem(ctx, sp, cardID, purchaseID); gerr == nil && !again.Refunded {
				return Purchase{}, ErrCardMoved
			}
			return Purchase{}, ErrPurchaseRefunded
		case purchaseIdx < len(codes) && codes[purchaseIdx] == codeConditionFailed:
			// Refunded or advanced since it was read: read again.
			return Purchase{}, ErrCardMoved
		case cardIdx < len(codes) && codes[cardIdx] == codeConditionFailed:
			return Purchase{}, ErrCardMoved
		}
		return Purchase{}, classifyPostCancel(err, rev.MarkerIdx)
	}
	p.Refunded = true
	return p, nil
}

// PurchaseEdit is a change to a purchase's safe fields. Nil keeps; amounts,
// dates and installments never change (a wrong amount is a refund and a new
// purchase).
type PurchaseEdit struct {
	Description *string
	CategoryID  *string
}

// EditPurchase renames or recategorises a purchase. The description is the
// purchase's and every one of its statement lines' (closed statements' too: a
// label, never money). A new category moves the whole expense: the ledger
// transaction is replaced (planReplace: reversed and posted again with the new
// category, both on the purchase date, so the DRE moves it within the purchase's
// month), and the purchase points at the replacement, which a later refund
// reverses. One write: the ledger, the purchase (guarded like a refund, its rev
// moved) and the lines. A description alone does not touch the ledger, whose
// memo keeps the name the purchase was made with. A refunded purchase is
// ErrPurchaseRefunded.
func (r *CardRepository) EditPurchase(ctx context.Context, sp space.ResolvedSpace, cardID, purchaseID string, e PurchaseEdit, meta PostMeta, now time.Time) (Purchase, error) {
	if err := sp.Require(space.Write); err != nil {
		return Purchase{}, err
	}
	if _, err := r.loadCard(ctx, sp, cardID); err != nil {
		return Purchase{}, err
	}
	row, err := r.getPurchaseItem(ctx, sp, cardID, purchaseID)
	if err != nil {
		return Purchase{}, err
	}
	if row.Refunded {
		return Purchase{}, ErrPurchaseRefunded
	}
	p, err := row.purchase(cardID)
	if err != nil {
		return Purchase{}, err
	}
	next := p
	if e.Description != nil {
		next.Description = *e.Description
	}
	if e.CategoryID != nil {
		next.CategoryID = *e.CategoryID
	}
	if next.Description == p.Description && next.CategoryID == p.CategoryID {
		return p, nil
	}
	var items []types.TransactWriteItem
	markerIdx := -1
	if next.CategoryID != p.CategoryID {
		if err := r.expenseCategory(ctx, sp, next.CategoryID); err != nil {
			return Purchase{}, err
		}
		tx, err := finance.CardPurchase(cardID, next.CategoryID, p.Total, p.Date)
		if err != nil {
			return Purchase{}, err
		}
		reverseMeta := meta
		reverseMeta.Origin, reverseMeta.Memo, reverseMeta.Ref = "card_purchase_edit", "", "purchase:"+p.ID
		meta.Origin, meta.Memo, meta.Ref = "card_purchase", next.Description, "purchase:"+p.ID
		plan, err := r.ledger.planReplace(ctx, sp, p.TxID, tx, p.Date, reverseMeta, meta, now)
		if err != nil {
			return Purchase{}, err
		}
		items, markerIdx, next.TxID = plan.Items, plan.MarkerIdx, plan.ReplacementID
	}
	pk := CardPK(sp, cardID)
	sk := PurchaseSK(p.ID)
	purchaseIdx := len(items)
	guard, values := purchaseGuard(row)
	values[":d"], values[":c"], values[":tx"] = str(next.Description), str(next.CategoryID), str(next.TxID)
	values[":now"], values[":one"] = str(now.UTC().Format(time.RFC3339Nano)), numberValue(1)
	items = append(items, r.cards.BuildRawUpdateTxItem(pk, &sk,
		"SET description = :d, category_id = :c, tx_id = :tx, updated_at = :now ADD rev :one", guard, nil, values))
	// Its lines: one per installment billed as such, one "adv" per statement an
	// advance moved installments onto.
	linesIdx := len(items)
	seen := map[string]bool{}
	for _, inst := range row.Installments {
		tag, desc := installmentTag(inst.Number), next.Description
		if inst.Advanced {
			tag, desc = "adv", "Antecipação: "+next.Description
		}
		m, err := finance.ParseMonth(inst.Statement)
		if err != nil {
			return Purchase{}, err
		}
		itemSK := ItemSK(m, p.ID, tag)
		if seen[itemSK] {
			continue
		}
		seen[itemSK] = true
		items = append(items, r.cards.BuildRawUpdateTxItem(pk, &itemSK, "SET description = :d, category_id = :c", "attribute_exists(pk)", nil,
			map[string]types.AttributeValue{":d": str(desc), ":c": str(next.CategoryID)}))
	}
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		switch {
		case !onlyConditionFailed(err):
			return Purchase{}, err
		case purchaseIdx < len(codes) && codes[purchaseIdx] == codeConditionFailed,
			markerIdx >= 0 && markerIdx < len(codes) && codes[markerIdx] == codeConditionFailed:
			// Refunded, advanced or edited since it was read: read again.
			return Purchase{}, ErrCardMoved
		}
		for i := linesIdx; i < len(codes); i++ {
			if codes[i] == codeConditionFailed {
				// A line the purchase's plan names is not there: data drift, not
				// something the person can fix by choosing again.
				return Purchase{}, fmt.Errorf("cards: purchase %s has no statement line %d of its plan", p.ID, i-linesIdx)
			}
		}
		return Purchase{}, classifyPostCancel(err, -1)
	}
	return next, nil
}

// expenseCategory checks an account can take a purchase: an active, non-system
// expense category of this space.
func (r *CardRepository) expenseCategory(ctx context.Context, sp space.ResolvedSpace, id string) error {
	cat, err := r.ledger.getAccount(ctx, sp, id)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %w: category %s", ErrUnknownAccount, ErrCategoryUnusable, id)
	}
	if err != nil {
		return err
	}
	if cat.Class != finance.ClassExpense || cat.System || cat.Archived {
		return fmt.Errorf("%w: %w: the purchase needs an active expense category", finance.ErrInvalidTransaction, ErrCategoryUnusable)
	}
	return nil
}
