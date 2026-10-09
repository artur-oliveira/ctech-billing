package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

var (
	// ErrLineResolved is a reconciliation of a line that is no longer pending:
	// it was matched, created or ignored already (or, for a reopen, is not
	// ignored).
	ErrLineResolved = errors.New("this statement line was already reconciled")
	// ErrLineMismatch is a match against a bill of another account or of the
	// other direction.
	ErrLineMismatch = errors.New("the bill does not fit this statement line")
)

// LineStatus is where a statement line stands in the reconciliation.
type LineStatus string

const (
	LinePending LineStatus = "pending"
	LineMatched LineStatus = "matched" // settled an open bill
	LineCreated LineStatus = "created" // became a bill, created and settled at once
	LineIgnored LineStatus = "ignored" // kept, so it is not offered again
)

// maxStoredRejections bounds the rejected lines kept on an import's row: the
// count is exact, the list is a sample to show.
const maxStoredRejections = 50

// importChunk is how many lines one TransactWriteItems claims: a lock and a
// line each, 100 items.
const importChunk = 50

// ImportRepository stores statement imports (spec § 3.7, § 4 `imports`):
//
//   - S → IMPORT#{id}: the import (account, format, counts), TTL 90 days;
//   - S#IMPORT#{id} → LINE#{n}: its parsed lines, TTL 90 days;
//   - S → FITID#{account}#{key}: the lock that makes a transaction import once,
//     with NO TTL (ADR 0026: expiring it would let an old file in again);
//   - S → CSVMAP#{account}: the account's saved CSV column mapping.
//
// The raw file never reaches this layer.
type ImportRepository struct {
	imports Base
	bills   *BillRepository
	ledger  *LedgerRepository
}

func NewImportRepository(db *dynamodb.Client, cfg *config.Config) *ImportRepository {
	return &ImportRepository{imports: NewBase(db, cfg, TableImports), bills: NewBillRepository(db, cfg), ledger: NewLedgerRepository(db, cfg)}
}

// Import is one uploaded file, after parsing.
type Import struct {
	ID         string
	AccountID  string
	Format     statement.Format
	CreatedAt  time.Time
	From, To   brcal.Date // the period its lines cover
	Lines      int        // lines this import added
	Duplicates int        // lines another import already holds
	// RejectedCount is exact; Rejected is the first maxStoredRejections of them.
	RejectedCount int
	Rejected      []statement.Rejected
	Resolved      int // lines matched, created or ignored
}

// Pending is how many lines still wait for a decision.
func (i Import) Pending() int { return i.Lines - i.Resolved }

// ImportLine is one parsed transaction and where it stands.
type ImportLine struct {
	N           int
	Date        brcal.Date
	Amount      billing.Cents // signed from the account's side
	Description string
	Status      LineStatus
	BillID      string // the bill it settled or became
}

type rejectedItem struct {
	Line   int    `dynamodbav:"line"`
	Reason string `dynamodbav:"reason"`
}

type importItem struct {
	keys
	ID            string         `dynamodbav:"id"`
	AccountID     string         `dynamodbav:"account_id"`
	Format        string         `dynamodbav:"format"`
	From          string         `dynamodbav:"from,omitempty"`
	To            string         `dynamodbav:"to,omitempty"`
	Lines         int            `dynamodbav:"lines"`
	Duplicates    int            `dynamodbav:"duplicates"`
	RejectedCount int            `dynamodbav:"rejected_count"`
	Rejected      []rejectedItem `dynamodbav:"rejected,omitempty"`
	Resolved      int            `dynamodbav:"resolved"`
}

func (i importItem) entity() (Import, error) {
	created, err := time.Parse(time.RFC3339Nano, i.CreatedAt)
	if err != nil {
		return Import{}, fmt.Errorf("import %s has a malformed created_at: %w", i.ID, err)
	}
	out := Import{ID: i.ID, AccountID: i.AccountID, Format: statement.Format(i.Format), CreatedAt: created,
		Lines: i.Lines, Duplicates: i.Duplicates, RejectedCount: i.RejectedCount, Resolved: i.Resolved}
	if i.From != "" {
		if out.From, err = brcal.Parse(i.From); err != nil {
			return Import{}, err
		}
	}
	if i.To != "" {
		if out.To, err = brcal.Parse(i.To); err != nil {
			return Import{}, err
		}
	}
	for _, r := range i.Rejected {
		out.Rejected = append(out.Rejected, statement.Rejected{Line: r.Line, Reason: r.Reason})
	}
	return out, nil
}

type lineItem struct {
	keys
	N           int    `dynamodbav:"n"`
	Date        string `dynamodbav:"date"`
	Amount      int64  `dynamodbav:"amount"`
	Description string `dynamodbav:"description,omitempty"`
	LockSK      string `dynamodbav:"lock_sk"`
	Status      string `dynamodbav:"status"`
	BillID      string `dynamodbav:"bill_id,omitempty"`
}

func (i lineItem) entity() (ImportLine, error) {
	d, err := brcal.Parse(i.Date)
	if err != nil {
		return ImportLine{}, fmt.Errorf("import line %d has a malformed date: %w", i.N, err)
	}
	return ImportLine{N: i.N, Date: d, Amount: billing.Cents(i.Amount), Description: i.Description,
		Status: LineStatus(i.Status), BillID: i.BillID}, nil
}

// lockItem is a transaction's claim in its account. It names the line that
// holds it and when that line expires: a line nobody reconciled for 90 days
// expires with its import, and its lock may then be claimed again by a new
// upload — otherwise the transaction could never be imported again.
//
// It also records the transaction's date and amount: a FITID lock claimed for
// one transaction does not swallow a later, different one under a reused FITID
// (claim falls back to the content key).
type lockItem struct {
	keys
	ImportID    string `dynamodbav:"import_id"`
	Line        int    `dynamodbav:"line"`
	LineExpires int64  `dynamodbav:"line_expires"`
	Resolved    bool   `dynamodbav:"resolved,omitempty"`
	Date        string `dynamodbav:"date"`
	Amount      int64  `dynamodbav:"amount"`
}

// sameTransaction reports whether a held lock (its old item) was claimed for
// this date and amount. A lock written without them is taken as the same.
func sameTransaction(old map[string]types.AttributeValue, l statement.Line) bool {
	d, okD := old["date"].(*types.AttributeValueMemberS)
	a, okA := old["amount"].(*types.AttributeValueMemberN)
	if !okD || !okA {
		return true
	}
	return d.Value == l.Date.String() && a.Value == strconv.FormatInt(int64(l.Amount), 10)
}

// alive reports whether a row with this TTL still exists for a reader. DynamoDB
// deletes expired items up to days late and returns them until it does.
func alive(ttl *int64, now time.Time) bool { return ttl == nil || *ttl > now.Unix() }

// Import writes a parsed file's lines under a new import, each claiming its
// lock in the account; a line whose lock is already held (the same file again,
// an overlapping export) is a duplicate and is not written. With an idempotency
// key the import's id is derived from it, so a retry after a failure resumes
// the same import and counts its own lines as added, not as duplicates. An
// import that adds nothing leaves no row and comes back with no id.
func (r *ImportRepository) Import(ctx context.Context, sp space.ResolvedSpace, accountID string, format statement.Format, parsed statement.Parsed, idempotencyKey string, now time.Time) (Import, error) {
	if err := sp.Require(space.Import); err != nil {
		return Import{}, err
	}
	if err := r.ledger.activeCash(ctx, sp, accountID); err != nil {
		return Import{}, err
	}
	imp := Import{ID: id.New(), AccountID: accountID, Format: format, CreatedAt: now.UTC(), RejectedCount: len(parsed.Rejected)}
	if idempotencyKey != "" {
		imp.ID = idempotentID(sp, "import", idempotencyKey)
	}
	imp.Rejected = parsed.Rejected[:min(len(parsed.Rejected), maxStoredRejections)]
	for _, l := range parsed.Lines {
		if imp.From.IsZero() || l.Date.Before(imp.From) {
			imp.From = l.Date
		}
		if l.Date.After(imp.To) {
			imp.To = l.Date
		}
	}

	// The row first: an import that fails halfway is still listed, and its
	// lines are reachable. A retry finds it and keeps its resolved count.
	header, err := Encode(r.newImportItem(sp, imp, now))
	if err != nil {
		return Import{}, err
	}
	if err := r.imports.TransactWrite(ctx, []types.TransactWriteItem{r.imports.BuildPutTxItemIfAbsent(header)}); err != nil && !onlyConditionFailed(err) {
		return Import{}, err
	}

	keyed := statement.Keys(accountID, parsed.Lines)
	lineTTL := *RetentionImportLine.ExpiresAt(now)
	for start := 0; start < len(keyed); start += importChunk {
		end := min(start+importChunk, len(keyed))
		added, dup, err := r.claim(ctx, sp, imp.ID, accountID, keyed[start:end], start, lineTTL, now)
		if err != nil {
			return Import{}, err
		}
		imp.Lines += added
		imp.Duplicates += dup
	}

	if imp.Lines == 0 {
		// Nothing new: the row would be a list entry with nothing in it.
		if _, err := r.imports.DeleteItem(ctx, sp.PK(), ImportSK(imp.ID)); err != nil {
			return Import{}, err
		}
		imp.ID = ""
		return imp, nil
	}
	sk := ImportSK(imp.ID)
	rejected, err := Encode(struct {
		R []rejectedItem `dynamodbav:"r"`
	}{rejectedItems(imp.Rejected)})
	if err != nil {
		return Import{}, err
	}
	values := map[string]types.AttributeValue{
		":l": numberValue(int64(imp.Lines)), ":d": numberValue(int64(imp.Duplicates)),
		":rc": numberValue(int64(imp.RejectedCount)), ":r": rejected["r"],
		":f": str(imp.From.String()), ":t": str(imp.To.String()), ":now": str(now.UTC().Format(time.RFC3339Nano)),
	}
	update := r.imports.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET #lines = :l, duplicates = :d, rejected_count = :rc, rejected = :r, #from = :f, #to = :t, updated_at = :now",
		"attribute_exists(pk)", map[string]string{"#lines": "lines", "#from": "from", "#to": "to"}, values)
	if err := r.imports.TransactWrite(ctx, []types.TransactWriteItem{update}); err != nil {
		return Import{}, err
	}
	// The resolved count is the row's (a retry keeps it); read it back only then.
	if cur, err := r.getImport(ctx, sp, imp.ID, now); err == nil {
		imp.Resolved = cur.Resolved
	}
	return imp, nil
}

func rejectedItems(rs []statement.Rejected) []rejectedItem {
	out := make([]rejectedItem, len(rs))
	for i, r := range rs {
		out[i] = rejectedItem{Line: r.Line, Reason: r.Reason}
	}
	return out
}

func (r *ImportRepository) newImportItem(sp space.ResolvedSpace, imp Import, now time.Time) importItem {
	it := importItem{
		keys: newKeys(sp.PK(), ImportSK(imp.ID), RetentionImportLine, now),
		ID:   imp.ID, AccountID: imp.AccountID, Format: string(imp.Format),
		Lines: 0, Duplicates: 0, RejectedCount: imp.RejectedCount, Rejected: rejectedItems(imp.Rejected),
	}
	if !imp.From.IsZero() {
		it.From, it.To = imp.From.String(), imp.To.String()
	}
	return it
}

// claim writes one chunk of lines: per line, its lock (conditional) and the line
// itself, in one transaction. A lock that is already held cancels the
// transaction; the reasons say which, those lines are dropped — as duplicates,
// or as already added when the lock is this import's own — and the rest is
// sent again. Each round drops at least one line, so it ends.
func (r *ImportRepository) claim(ctx context.Context, sp space.ResolvedSpace, importID, accountID string, chunk []statement.Keyed, offset int, lineTTL int64, now time.Time) (added, duplicates int, err error) {
	type pending struct {
		statement.Keyed
		n int
	}
	todo := make([]pending, len(chunk))
	for i, k := range chunk {
		todo[i] = pending{Keyed: k, n: offset + i + 1}
	}
	conflicts := 0
	for len(todo) > 0 {
		items := make([]types.TransactWriteItem, 0, 2*len(todo))
		for _, p := range todo {
			lockSK := ImportLockSK(accountID, p.Key)
			lock, err := Encode(lockItem{keys: newKeys(sp.PK(), lockSK, RetentionImportLock, now), ImportID: importID, Line: p.n, LineExpires: lineTTL,
				Date: p.Date.String(), Amount: int64(p.Amount)})
			if err != nil {
				return 0, 0, err
			}
			line, err := Encode(lineItem{
				keys: newKeys(ImportPK(sp, importID), ImportLineSK(p.n), RetentionImportLine, now),
				N:    p.n, Date: p.Date.String(), Amount: int64(p.Amount), Description: p.Description,
				LockSK: lockSK, Status: string(LinePending),
			})
			if err != nil {
				return 0, 0, err
			}
			items = append(items,
				types.TransactWriteItem{Put: &types.Put{
					TableName: aws.String(r.imports.TableName), Item: lock,
					// Free, or held by a line that expired unreconciled.
					ConditionExpression:                 aws.String("attribute_not_exists(pk) OR (attribute_not_exists(resolved) AND line_expires < :now)"),
					ExpressionAttributeValues:           map[string]types.AttributeValue{":now": numberValue(now.Unix())},
					ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
				}},
				r.imports.BuildPutTxItem(line))
		}
		err := r.imports.TransactWrite(ctx, items)
		if err == nil {
			return added + len(todo), duplicates, nil
		}
		if retryableCancel(err) && conflicts < 3 {
			conflicts++
			continue
		}
		var tc *types.TransactionCanceledException
		if !onlyConditionFailed(err) || !errors.As(err, &tc) {
			return 0, 0, err
		}
		kept := todo[:0]
		for i, p := range todo {
			reason := tc.CancellationReasons[2*i]
			if aws.ToString(reason.Code) != codeConditionFailed {
				kept = append(kept, p)
				continue
			}
			owner, _ := reason.Item["import_id"].(*types.AttributeValueMemberS)
			switch {
			case owner != nil && owner.Value == importID:
				added++ // a retry of this import: the line is already there
			case p.Fallback != "" && !sameTransaction(reason.Item, p.Line):
				// The bank reused this FITID for another transaction: key it by
				// its content and try again.
				p.Key, p.Fallback = p.Fallback, ""
				kept = append(kept, p)
			default:
				duplicates++
			}
		}
		todo = kept
	}
	return added, duplicates, nil
}

// List returns the account's imports still kept, newest first. accountID ""
// lists every account's.
func (r *ImportRepository) List(ctx context.Context, sp space.ResolvedSpace, accountID string, now time.Time) ([]Import, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	raw, err := r.ledger.queryPrefix(ctx, r.imports, sp.PK(), "IMPORT#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[importItem](raw)
	if err != nil {
		return nil, err
	}
	out := make([]Import, 0, len(rows))
	for _, row := range rows {
		if !alive(row.TTL, now) || (accountID != "" && row.AccountID != accountID) {
			continue
		}
		imp, err := row.entity()
		if err != nil {
			return nil, err
		}
		out = append(out, imp)
	}
	slices.Reverse(out) // ids are ULIDs: ascending is oldest first
	return out, nil
}

// Get returns an import and its lines, in file order.
func (r *ImportRepository) Get(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (Import, []ImportLine, error) {
	if err := sp.Require(space.Read); err != nil {
		return Import{}, nil, err
	}
	imp, err := r.getImport(ctx, sp, importID, now)
	if err != nil {
		return Import{}, nil, err
	}
	raw, err := r.ledger.queryPrefix(ctx, r.imports, ImportPK(sp, importID), "LINE#")
	if err != nil {
		return Import{}, nil, err
	}
	rows, err := DecodeItems[lineItem](raw)
	if err != nil {
		return Import{}, nil, err
	}
	lines := make([]ImportLine, 0, len(rows))
	for _, row := range rows {
		if !alive(row.TTL, now) {
			continue
		}
		l, err := row.entity()
		if err != nil {
			return Import{}, nil, err
		}
		lines = append(lines, l)
	}
	return imp, lines, nil
}

func (r *ImportRepository) getImport(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (Import, error) {
	raw, err := r.imports.GetItem(ctx, sp.PK(), ImportSK(importID))
	if err != nil {
		return Import{}, err
	}
	if raw == nil {
		return Import{}, fmt.Errorf("%w: import %s", ErrNotFound, importID)
	}
	row, err := Decode[importItem](raw)
	if err != nil {
		return Import{}, err
	}
	if !alive(row.TTL, now) {
		return Import{}, fmt.Errorf("%w: import %s", ErrNotFound, importID)
	}
	return row.entity()
}

// loadLine reads an import and one of its lines, with the line's lock key.
func (r *ImportRepository) loadLine(ctx context.Context, sp space.ResolvedSpace, importID string, n int, now time.Time) (Import, ImportLine, string, error) {
	imp, err := r.getImport(ctx, sp, importID, now)
	if err != nil {
		return Import{}, ImportLine{}, "", err
	}
	raw, err := r.imports.GetItem(ctx, ImportPK(sp, importID), ImportLineSK(n))
	if err != nil {
		return Import{}, ImportLine{}, "", err
	}
	if raw == nil {
		return Import{}, ImportLine{}, "", fmt.Errorf("%w: line %d", ErrNotFound, n)
	}
	row, err := Decode[lineItem](raw)
	if err != nil {
		return Import{}, ImportLine{}, "", err
	}
	if !alive(row.TTL, now) {
		return Import{}, ImportLine{}, "", fmt.Errorf("%w: line %d", ErrNotFound, n)
	}
	l, err := row.entity()
	return imp, l, row.LockSK, err
}

// decide is the writes that record a decision on a pending line: the line
// (only while still pending), its lock (only while it still names this
// import) marked resolved, and the import's resolved count.
func (r *ImportRepository) decide(sp space.ResolvedSpace, importID string, n int, lockSK string, status LineStatus, billID string, now time.Time) []types.TransactWriteItem {
	stamp := str(now.UTC().Format(time.RFC3339Nano))
	lineSK := ImportLineSK(n)
	values := map[string]types.AttributeValue{":s": str(string(status)), ":p": str(string(LinePending)), ":now": stamp}
	set := "SET #status = :s, updated_at = :now"
	if billID != "" {
		values[":b"] = str(billID)
		set += ", bill_id = :b"
	}
	line := r.imports.BuildRawUpdateTxItem(ImportPK(sp, importID), &lineSK, set, "#status = :p",
		map[string]string{"#status": "status"}, values)
	lock := r.imports.BuildRawUpdateTxItem(sp.PK(), &lockSK, "SET resolved = :t, updated_at = :now", "import_id = :imp",
		nil, map[string]types.AttributeValue{":t": &types.AttributeValueMemberBOOL{Value: true}, ":imp": str(importID), ":now": stamp})
	headerSK := ImportSK(importID)
	header := r.imports.BuildRawUpdateTxItem(sp.PK(), &headerSK, "ADD resolved :one", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":one": numberValue(1)})
	return []types.TransactWriteItem{line, lock, header}
}

// decisionFailed reports whether the cancellation was the line or its lock
// having moved on (decided by another request), given where decide's three
// items start.
func decisionFailed(err error, at int) bool {
	codes := cancellationCodes(err)
	for i := at; i < at+2 && i < len(codes); i++ {
		if codes[i] == codeConditionFailed {
			return true
		}
	}
	return false
}

// Match settles an open bill with a pending line's date and amount, and marks
// the line matched — one transaction, so a bill is never paid by a line that
// still looks pending, and two lines cannot both pay one bill (the settlement's
// forecast guard refuses the second). The bill must be in the import's account
// and of the line's direction; a different amount needs differenceCategoryID,
// as any settlement does.
func (r *ImportRepository) Match(ctx context.Context, sp space.ResolvedSpace, importID string, n int, billID, differenceCategoryID string, meta PostMeta, now time.Time) (ImportLine, finance.Bill, error) {
	if err := sp.Require(space.Import | space.Write | space.Settle); err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	imp, line, lockSK, err := r.loadLine(ctx, sp, importID, n, now)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	if line.Status != LinePending {
		return ImportLine{}, finance.Bill{}, ErrLineResolved
	}
	amount := line.Amount
	if amount < 0 {
		amount = -amount
	}
	meta.Origin = "import"
	p, err := r.bills.planSettle(ctx, sp, billID, amount, differenceCategoryID, line.Date, meta, now)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	if p.bill.AccountID != imp.AccountID || p.bill.Direction != statement.DirectionOf(line.Amount) {
		return ImportLine{}, finance.Bill{}, ErrLineMismatch
	}
	at := len(p.items)
	items := append(p.items, r.decide(sp, importID, n, lockSK, LineMatched, billID, now)...)
	if err := r.imports.TransactWrite(ctx, items); err != nil {
		if decisionFailed(err, at) {
			return ImportLine{}, finance.Bill{}, ErrLineResolved
		}
		return ImportLine{}, finance.Bill{}, r.bills.settleError(ctx, sp, p.bill, err)
	}
	line.Status, line.BillID = LineMatched, billID
	return line, p.paid(line.Date), nil
}

// Create turns a pending line into a bill created and settled at once, under
// the category the person chose (and, optionally, a cleaner description):
// recognition and settlement on the line's date, the bill already paid, the
// line marked created — one transaction. Recognition and settlement both touch
// payables (or receivables) and its month's summary, so their ADDs are folded
// (mergeAdds). The bill's id comes from (import, line), so a retry cannot make
// two.
func (r *ImportRepository) Create(ctx context.Context, sp space.ResolvedSpace, importID string, n int, categoryID, description string, meta PostMeta, now time.Time) (ImportLine, finance.Bill, error) {
	if err := sp.Require(space.Import | space.Write | space.Settle); err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	imp, line, lockSK, err := r.loadLine(ctx, sp, importID, n, now)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	if line.Status != LinePending {
		return ImportLine{}, finance.Bill{}, ErrLineResolved
	}
	amount := line.Amount
	if amount < 0 {
		amount = -amount
	}
	if description == "" {
		description = line.Description
	}
	ref := importID + "#" + strconv.Itoa(n)
	b := finance.Bill{
		ID: idempotentID(sp, "import-line", ref), Direction: statement.DirectionOf(line.Amount), Amount: amount,
		AccountID: imp.AccountID, CategoryID: categoryID, Description: description,
		Competence: line.Date, Due: line.Date, Status: finance.BillForecast,
		Origin: finance.OriginImport, OriginRef: ref,
	}
	if err := b.Validate(); err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	if err := checkDirectionAccounts(ctx, r.ledger, sp, b.Direction, b.CategoryID, b.AccountID, finance.ErrInvalidBill); err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	sys, _ := finance.DefaultSystemAccounts()
	rec, err := finance.RecognizeBill(sys, b.Facts(), line.Date)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	settle, err := finance.SettleBill(sys, b.Facts(), amount, "", line.Date)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	meta.Origin = "import"
	recPlan, err := r.ledger.planPost(sp, rec, billMeta(meta, b), now)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	setPlan, err := r.ledger.planPost(sp, settle, billMeta(meta, b), now)
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	b.Status, b.PaidDate, b.TransactionIDs = finance.BillPaid, line.Date, []string{recPlan.TxID, setPlan.TxID}
	billRow, err := Encode(newBillItem(sp, b, now))
	if err != nil {
		return ImportLine{}, finance.Bill{}, err
	}
	items := mergeAdds(append(append([]types.TransactWriteItem(nil), recPlan.Items...), setPlan.Items...))
	billIdx := len(items)
	items = append(items, r.bills.bills.BuildPutTxItemIfAbsent(billRow))
	items = append(items, r.decide(sp, importID, n, lockSK, LineCreated, b.ID, now)...)
	if err := r.imports.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		if decisionFailed(err, billIdx+1) || (billIdx < len(codes) && codes[billIdx] == codeConditionFailed) {
			return ImportLine{}, finance.Bill{}, ErrLineResolved
		}
		return ImportLine{}, finance.Bill{}, classifyPostCancel(err, -1)
	}
	line.Status, line.BillID = LineCreated, b.ID
	return line, b, nil
}

// Ignore keeps a pending line out of the reconciliation: it is not offered
// again, and its lock stays, so the same transaction is not imported again.
func (r *ImportRepository) Ignore(ctx context.Context, sp space.ResolvedSpace, importID string, n int, now time.Time) (ImportLine, error) {
	if err := sp.Require(space.Import); err != nil {
		return ImportLine{}, err
	}
	_, line, lockSK, err := r.loadLine(ctx, sp, importID, n, now)
	if err != nil {
		return ImportLine{}, err
	}
	if line.Status != LinePending {
		return ImportLine{}, ErrLineResolved
	}
	if err := r.imports.TransactWrite(ctx, r.decide(sp, importID, n, lockSK, LineIgnored, "", now)); err != nil {
		if decisionFailed(err, 0) {
			return ImportLine{}, ErrLineResolved
		}
		return ImportLine{}, err
	}
	line.Status = LineIgnored
	return line, nil
}

// Reopen undoes an ignore: the line is pending again. Matched and created lines
// are undone through their bill (undo the payment), not here.
func (r *ImportRepository) Reopen(ctx context.Context, sp space.ResolvedSpace, importID string, n int, now time.Time) (ImportLine, error) {
	if err := sp.Require(space.Import); err != nil {
		return ImportLine{}, err
	}
	_, line, lockSK, err := r.loadLine(ctx, sp, importID, n, now)
	if err != nil {
		return ImportLine{}, err
	}
	if line.Status != LineIgnored {
		return ImportLine{}, ErrLineResolved
	}
	stamp := str(now.UTC().Format(time.RFC3339Nano))
	lineSK, headerSK := ImportLineSK(n), ImportSK(importID)
	items := []types.TransactWriteItem{
		r.imports.BuildRawUpdateTxItem(ImportPK(sp, importID), &lineSK, "SET #status = :p, updated_at = :now", "#status = :i",
			map[string]string{"#status": "status"},
			map[string]types.AttributeValue{":p": str(string(LinePending)), ":i": str(string(LineIgnored)), ":now": stamp}),
		r.imports.BuildRawUpdateTxItem(sp.PK(), &lockSK, "SET updated_at = :now REMOVE resolved", "import_id = :imp",
			nil, map[string]types.AttributeValue{":imp": str(importID), ":now": stamp}),
		r.imports.BuildRawUpdateTxItem(sp.PK(), &headerSK, "ADD resolved :one", "attribute_exists(pk)", nil,
			map[string]types.AttributeValue{":one": numberValue(-1)}),
	}
	if err := r.imports.TransactWrite(ctx, items); err != nil {
		if decisionFailed(err, 0) {
			return ImportLine{}, ErrLineResolved
		}
		return ImportLine{}, err
	}
	line.Status = LinePending
	return line, nil
}

type mappingItem struct {
	keys
	AccountID   string `dynamodbav:"account_id"`
	Delimiter   string `dynamodbav:"delimiter"`
	Decimal     string `dynamodbav:"decimal"`
	DateFormat  string `dynamodbav:"date_format"`
	SkipRows    int    `dynamodbav:"skip_rows"`
	Date        int    `dynamodbav:"date_column"`
	Description int    `dynamodbav:"description_column"`
	Amount      int    `dynamodbav:"amount_column"`
	Debit       int    `dynamodbav:"debit_column,omitempty"`
}

// GetMapping returns the account's saved CSV mapping, or ErrNotFound.
func (r *ImportRepository) GetMapping(ctx context.Context, sp space.ResolvedSpace, accountID string) (statement.Mapping, error) {
	if err := sp.Require(space.Read); err != nil {
		return statement.Mapping{}, err
	}
	raw, err := r.imports.GetItem(ctx, sp.PK(), CSVMappingSK(accountID))
	if err != nil {
		return statement.Mapping{}, err
	}
	if raw == nil {
		return statement.Mapping{}, fmt.Errorf("%w: no CSV mapping for %s", ErrNotFound, accountID)
	}
	m, err := Decode[mappingItem](raw)
	if err != nil {
		return statement.Mapping{}, err
	}
	return statement.Mapping{Delimiter: m.Delimiter, Decimal: m.Decimal, DateFormat: m.DateFormat, SkipRows: m.SkipRows,
		Date: m.Date, Description: m.Description, Amount: m.Amount, Debit: m.Debit}, nil
}

// PutMapping saves how the account's CSV export is read. It is a setting of
// the space, kept as long as the space (no TTL).
func (r *ImportRepository) PutMapping(ctx context.Context, sp space.ResolvedSpace, accountID string, m statement.Mapping, now time.Time) error {
	if err := sp.Require(space.Import); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if err := r.ledger.activeCash(ctx, sp, accountID); err != nil {
		return err
	}
	item, err := Encode(mappingItem{
		keys:      newKeys(sp.PK(), CSVMappingSK(accountID), RetentionPermanent, now),
		AccountID: accountID, Delimiter: m.Delimiter, Decimal: m.Decimal, DateFormat: m.DateFormat, SkipRows: m.SkipRows,
		Date: m.Date, Description: m.Description, Amount: m.Amount, Debit: m.Debit,
	})
	if err != nil {
		return err
	}
	return r.imports.PutItem(ctx, item)
}
