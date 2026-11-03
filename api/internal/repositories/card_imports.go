package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	// ErrStatementNotOpen is a card import into a statement that is not the card's
	// open one: a closed statement is reopened first, a future one is not due yet.
	ErrStatementNotOpen = errors.New("imports: the card's statement is not open")
	// ErrStatementNotBegun is a card import into an open statement whose period
	// has not begun (the previous one was closed before its closing day).
	ErrStatementNotBegun = errors.New("imports: the statement period has not begun")
)

// ImportCard stores a card statement file's lines, pending, under a new import
// for the card's open statement `month`. Each line claims a lock scoped to
// (card, month), so the same file again, or an overlapping export, adds every
// purchase once; two real identical purchases stay two lines. A line's key
// includes its installment ("Parcela 2/6"), so an installment and a purchase
// that share date, amount and text are two lines.
func (r *ImportRepository) ImportCard(ctx context.Context, sp space.ResolvedSpace, cardID string, month finance.Month, lines []statement.CardLine, rejected []statement.Rejected, idempotencyKey string, now time.Time) (Import, error) {
	if err := sp.Require(space.Import); err != nil {
		return Import{}, err
	}
	card, err := r.cards.activeCard(ctx, sp, cardID)
	if err != nil {
		return Import{}, err
	}
	if month != card.OpenMonth {
		return Import{}, ErrStatementNotOpen
	}
	if !brcal.FromTime(now).After(finance.ClosingDate(month.Add(-1), card.ClosingDay)) {
		return Import{}, ErrStatementNotBegun
	}
	scope := cardID + "#" + month.String()
	forKeys := make([]statement.Line, len(lines))
	extra := make(map[int]cardLineExtra, len(lines))
	for i, l := range lines {
		forKeys[i] = l.Line
		if l.N > 0 {
			forKeys[i].Description += " [" + strconv.Itoa(l.K) + "/" + strconv.Itoa(l.N) + "]"
		}
		extra[i+1] = cardLineExtra{K: l.K, Of: l.N, BankCategory: l.BankCategory}
	}
	keyed := statement.Keys(scope, forKeys)
	for i := range keyed {
		keyed[i].Description = lines[i].Description // the key hashed the installment; the line shows the bank's text
	}
	imp := Import{AccountID: cardID, Format: statement.FormatInterCard, Kind: "card", StatementMonth: month.String()}
	return r.store(ctx, sp, imp, scope, keyed, rejected, idempotencyKey, extra, now)
}

var (
	// ErrPairTaken is a pairing with a purchase another line already holds.
	ErrPairTaken = errors.New("imports: that purchase is already paired with another line")
	// ErrPaymentLine is a create from the payment of the previous statement.
	ErrPaymentLine = errors.New("imports: the payment of the previous statement is not a purchase")
	// ErrCategoryUnmapped is a create with no category given and none mapped to
	// the line's bank category.
	ErrCategoryUnmapped = errors.New("imports: no category for this line")
	// ErrPairMismatch is a pairing that does not fit: a payment or a credit line, a
	// remaining installment count that differs, or an amount that does not match.
	ErrPairMismatch = errors.New("imports: these lines do not fit that purchase")
	// ErrNotCardImport is a card operation on an account import.
	ErrNotCardImport = errors.New("imports: not a card statement import")
)

// maxPairLines bounds a group (statement.MaxGroupLines) so a transaction stays
// far under DynamoDB's item limit.
const maxPairLines = statement.MaxGroupLines

// CardPairSK is the lock that makes a purchase's installment on a statement
// pair with at most one line (or one group). It is written in the same
// transaction that resolves the line, and read by prefix to leave claimed items
// out of the suggestions, so a purchase typed after the import is simply
// unclaimed.
func CardPairSK(cardID string, month finance.Month, purchaseID, tag string) string {
	return "CARDPAIR#" + cardID + "#" + month.String() + "#" + purchaseID + "#" + tag
}

type pairItem struct {
	keys
	ImportID string `dynamodbav:"import_id"`
	Line     int    `dynamodbav:"line"`
}

// cardImport loads an import and checks it is a card one.
func (r *ImportRepository) cardImport(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (Import, finance.Month, error) {
	imp, err := r.getImport(ctx, sp, importID, now)
	if err != nil {
		return Import{}, finance.Month{}, err
	}
	if imp.Kind != "card" {
		return Import{}, finance.Month{}, ErrNotCardImport
	}
	m, err := finance.ParseMonth(imp.StatementMonth)
	return imp, m, err
}

// cardReview is what a review reads once: the import, its lines, the imported
// statement and the installment items no line has claimed yet.
type cardReview struct {
	imp   Import
	month finance.Month
	lines []ImportLine
	stmt  Statement
	items []statement.CardItem
}

// review reads an import for suggestions or a bulk confirm: one partition read
// for the statement, one prefix read for the claims.
func (r *ImportRepository) review(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (cardReview, error) {
	imp, month, err := r.cardImport(ctx, sp, importID, now)
	if err != nil {
		return cardReview{}, err
	}
	_, lines, err := r.Get(ctx, sp, importID, now)
	if err != nil {
		return cardReview{}, err
	}
	stmt, err := r.cards.GetStatement(ctx, sp, imp.AccountID, month)
	if err != nil {
		return cardReview{}, err
	}
	claimed, err := queryPrefix(ctx, r.imports, sp.PK(), "CARDPAIR#"+imp.AccountID+"#"+month.String()+"#")
	if err != nil {
		return cardReview{}, err
	}
	taken := map[string]bool{}
	for _, it := range claimed {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			taken[sk.Value] = true
		}
	}
	rv := cardReview{imp: imp, month: month, lines: lines, stmt: stmt}
	for _, it := range stmt.Items {
		tag := installmentTag(it.Number)
		if it.Kind != "installment" || taken[CardPairSK(imp.AccountID, month, it.PurchaseID, tag)] {
			continue
		}
		rv.items = append(rv.items, statement.CardItem{PurchaseID: it.PurchaseID, Tag: tag, Description: it.Description,
			Date: it.Date, Amount: it.Amount, Number: it.Number, Of: it.Of})
	}
	return rv, nil
}

func (rv cardReview) suggestions() []statement.CardSuggestion {
	pending := map[int]statement.CardLine{}
	for _, l := range rv.lines {
		if l.Status == LinePending {
			pending[l.N] = cardLineOf(l)
		}
	}
	return statement.SuggestCard(pending, rv.items)
}

func cardLineOf(l ImportLine) statement.CardLine {
	return statement.CardLine{Line: statement.Line{Date: l.Date, Amount: l.Amount, Description: l.Description},
		BankCategory: l.BankCategory, K: l.K, N: l.Of, Payment: statement.IsPaymentLine(l.Description)}
}

// CardSuggestions reads an import and what it offers for each line still
// pending: pairs and groups against the statement's unclaimed installment items,
// and ignore for the previous statement's payment.
func (r *ImportRepository) CardSuggestions(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (Import, []ImportLine, []statement.CardSuggestion, error) {
	if err := sp.Require(space.Read); err != nil {
		return Import{}, nil, nil, err
	}
	rv, err := r.review(ctx, sp, importID, now)
	if err != nil {
		return Import{}, nil, nil, err
	}
	return rv.imp, rv.lines, rv.suggestions(), nil
}

// ConfirmPair links pending lines — one (a pair) or several (a group) — to a
// purchase's installment on the imported statement. It records the link only:
// nothing is posted, the purchase is already in the books. The claim is a lock
// row written with the decisions, so an item pairs with one line or group. The
// lines must fit the purchase (ErrPairMismatch): none a payment or a credit, all
// with the purchase's remaining installment count, summing to its amount.
func (r *ImportRepository) ConfirmPair(ctx context.Context, sp space.ResolvedSpace, importID string, lines []int, purchaseID, tag string, now time.Time) ([]ImportLine, error) {
	if err := sp.Require(space.Import | space.Write); err != nil {
		return nil, err
	}
	if len(lines) == 0 || len(lines) > maxPairLines {
		return nil, ErrLineResolved
	}
	imp, month, err := r.cardImport(ctx, sp, importID, now)
	if err != nil {
		return nil, err
	}
	stmt, err := r.cards.GetStatement(ctx, sp, imp.AccountID, month)
	if err != nil {
		return nil, err
	}
	loaded := make([]ImportLine, len(lines))
	for i, n := range lines {
		_, line, lockSK, err := r.loadLine(ctx, sp, importID, n, now)
		if err != nil {
			return nil, err
		}
		line.LockSK = lockSK
		loaded[i] = line
	}
	return r.confirmPair(ctx, sp, imp, month, stmt, loaded, purchaseID, tag, now)
}

// confirmPair is ConfirmPair on a statement and lines already read.
func (r *ImportRepository) confirmPair(ctx context.Context, sp space.ResolvedSpace, imp Import, month finance.Month, stmt Statement, lines []ImportLine, purchaseID, tag string, now time.Time) ([]ImportLine, error) {
	var target *StatementItem
	for i, it := range stmt.Items {
		if it.Kind == "installment" && it.PurchaseID == purchaseID && installmentTag(it.Number) == tag {
			target = &stmt.Items[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: purchase %s on %s", ErrNotFound, purchaseID, month)
	}
	var sum billing.Cents
	vista := false
	for _, l := range lines {
		if l.Status != LinePending {
			return nil, ErrLineResolved
		}
		cl := cardLineOf(l)
		if cl.Payment || l.Amount <= 0 || cl.Remaining() != target.Of-target.Number+1 {
			return nil, ErrPairMismatch
		}
		vista = vista || l.Of == 0
		sum += l.Amount
	}
	diff := sum - target.Amount
	if diff < 0 {
		diff = -diff
	}
	if (vista && (len(lines) != 1 || diff != 0)) || diff > statement.CardCentsTolerance {
		return nil, ErrPairMismatch
	}
	items := make([]types.TransactWriteItem, 0, 2+2*len(lines))
	pair, err := Encode(pairItem{keys: newKeys(sp.PK(), CardPairSK(imp.AccountID, month, purchaseID, tag), RetentionImportLock, now), ImportID: imp.ID, Line: lines[0].N})
	if err != nil {
		return nil, err
	}
	items = append(items, r.imports.BuildPutTxItemIfAbsent(pair))
	out := make([]ImportLine, len(lines))
	for i, l := range lines {
		d := r.decide(sp, imp.ID, l.N, l.LockSK, LineLinked, purchaseID, now)
		items = append(items, d[0], d[1]) // the line and its lock; the count is one update below
		l.Status, l.BillID = LineLinked, purchaseID
		out[i] = l
	}
	headerSK := ImportSK(imp.ID)
	items = append(items, r.imports.BuildRawUpdateTxItem(sp.PK(), &headerSK, "ADD resolved :n", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":n": numberValue(int64(len(lines)))}))
	if err := r.imports.TransactWrite(ctx, items); err != nil {
		if onlyConditionFailed(err) {
			if codes := cancellationCodes(err); len(codes) > 0 && codes[0] == codeConditionFailed {
				return nil, ErrPairTaken
			}
			return nil, ErrLineResolved
		}
		return nil, err
	}
	return out, nil
}

// ConfirmAll confirms every pair and group the import currently suggests, except
// those that touch a line in `exclude` (what the person said is not that
// purchase), and says how many lines that resolved. It reads the import once. A
// suggestion that lost a race (the item taken, the line decided meanwhile) is
// skipped, so it is safe to repeat.
func (r *ImportRepository) ConfirmAll(ctx context.Context, sp space.ResolvedSpace, importID string, exclude map[int]bool, now time.Time) (int, error) {
	if err := sp.Require(space.Import | space.Write); err != nil {
		return 0, err
	}
	rv, err := r.review(ctx, sp, importID, now)
	if err != nil {
		return 0, err
	}
	byN := make(map[int]ImportLine, len(rv.lines))
	for _, l := range rv.lines {
		byN[l.N] = l
	}
	confirmed := 0
next:
	for _, s := range rv.suggestions() {
		if s.Kind == statement.SuggestIgnore || s.Item == nil {
			continue
		}
		group := make([]ImportLine, len(s.Lines))
		for i, n := range s.Lines {
			if exclude[n] {
				continue next
			}
			group[i] = byN[n]
		}
		lines, err := r.confirmPair(ctx, sp, rv.imp, rv.month, rv.stmt, group, s.Item.PurchaseID, s.Item.Tag, now)
		switch {
		case err == nil:
			confirmed += len(lines)
		case errors.Is(err, ErrPairTaken), errors.Is(err, ErrLineResolved), errors.Is(err, ErrPairMismatch):
		default:
			return confirmed, err
		}
	}
	return confirmed, nil
}

// CreateFromCardLine turns a pending line into what it is, in one transaction
// with the decision: a purchase for a positive line — à vista as the file says,
// an installment ("Parcela k/n") the way a person keys an old one, as the
// remaining n−k+1 installments starting on this statement — or a standalone
// credit for a negative one. The previous statement's payment is refused. The
// category is the one given. The purchase's id comes from (import, line), so a
// retry cannot make two.
func (r *ImportRepository) CreateFromCardLine(ctx context.Context, sp space.ResolvedSpace, importID string, n int, categoryID, description string, meta PostMeta, now time.Time) (ImportLine, error) {
	if err := sp.Require(space.Import | space.Write); err != nil {
		return ImportLine{}, err
	}
	imp, line, lockSK, err := r.loadLine(ctx, sp, importID, n, now)
	if err != nil {
		return ImportLine{}, err
	}
	if imp.Kind != "card" {
		return ImportLine{}, ErrNotCardImport
	}
	if line.Status != LinePending {
		return ImportLine{}, ErrLineResolved
	}
	if statement.IsPaymentLine(line.Description) {
		return ImportLine{}, ErrPaymentLine
	}
	if categoryID == "" {
		mapped, err := r.GetBankCategories(ctx, sp, imp.AccountID)
		if err != nil {
			return ImportLine{}, err
		}
		if categoryID = mapped[line.BankCategory]; categoryID == "" {
			return ImportLine{}, ErrCategoryUnmapped
		}
	}
	month, err := finance.ParseMonth(imp.StatementMonth)
	if err != nil {
		return ImportLine{}, err
	}
	card, err := r.cards.activeCard(ctx, sp, imp.AccountID)
	if err != nil {
		return ImportLine{}, err
	}
	if card.OpenMonth != month {
		return ImportLine{}, ErrStatementNotOpen
	}
	if description == "" {
		description = line.Description
	}
	closing := finance.ClosingDate(month, card.ClosingDay)
	meta.IdempotencyKey = "card-import-line:" + importID + "#" + strconv.Itoa(n)
	decision := func(id string, claim bool) purchaseExtras {
		return func(Purchase) ([]types.TransactWriteItem, error) {
			items := r.decide(sp, importID, n, lockSK, LineCreated, id, now)
			if !claim {
				return items, nil
			}
			// The purchase belongs to the line that made it: no other line is
			// offered it as a pair (two real rides of R$ 6,50 stay two purchases).
			pair, err := Encode(pairItem{keys: newKeys(sp.PK(), CardPairSK(imp.AccountID, month, id, installmentTag(1)), RetentionImportLock, now), ImportID: importID, Line: n})
			if err != nil {
				return nil, err
			}
			return append(items, r.imports.BuildPutTxItemIfAbsent(pair)), nil
		}
	}
	var createdID string
	switch {
	case line.Amount < 0:
		c, err := r.cards.addCredit(ctx, sp, imp.AccountID, Credit{Description: description, CategoryID: categoryID, Date: line.Date, Amount: -line.Amount},
			meta, now, func(p Purchase) ([]types.TransactWriteItem, error) { return decision(p.ID, false)(p) })
		if err != nil {
			return ImportLine{}, createErr(err)
		}
		createdID = c.ID
	default:
		p := Purchase{Description: description, CategoryID: categoryID, Date: minDate(line.Date, closing), Total: line.Amount, Installments: make([]finance.Installment, 1)}
		if line.Of > 0 {
			remaining := line.Of - line.K + 1
			p.Total = line.Amount * billing.Cents(remaining)
			p.Installments = make([]finance.Installment, remaining)
			p.Date = minDate(brcal.FromTime(now), closing)
			p.Description = installmentNote(description, line)
		}
		created, err := r.cards.addPurchase(ctx, sp, imp.AccountID, p, meta, now,
			func(p Purchase) ([]types.TransactWriteItem, error) { return decision(p.ID, true)(p) })
		if err != nil {
			return ImportLine{}, createErr(err)
		}
		createdID = created.ID
	}
	line.Status, line.BillID = LineCreated, createdID
	return line, nil
}

func createErr(err error) error {
	var e errExtraFailed
	if errors.As(err, &e) {
		return ErrLineResolved
	}
	return err
}

func minDate(a, b brcal.Date) brcal.Date {
	if b.Before(a) {
		return b
	}
	return a
}

// installmentNote keeps the bank's "Parcela k/n" and the original purchase date
// in the description of a purchase made from an installment line, within the
// description limit.
func installmentNote(description string, l ImportLine) string {
	note := fmt.Sprintf(" (parcela %d/%d, compra de %02d/%02d/%d)", l.K, l.Of, l.Date.Day, int(l.Date.Month), l.Date.Year)
	for len(description)+len(note) > limits.Description {
		r := []rune(description)
		description = string(r[:len(r)-1])
	}
	return description + note
}
