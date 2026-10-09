package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ErrUnknownAccount is a transaction that names an account that does not exist
// in the resolved space. An account id from another space is exactly this: the
// lookup is inside the partition, so it is simply not there (spec § 2.1 rule 5).
var ErrUnknownAccount = errors.New("ledger: unknown account in this space")

// ErrAlreadyReversed is a second reversal of the same transaction.
var ErrAlreadyReversed = errors.New("ledger: transaction already reversed")

// ErrOpeningExists is a second opening balance on the same account. Reverse the
// first one to post a corrected one.
var ErrOpeningExists = errors.New("ledger: the account already has an opening balance")

// ErrNotManual is a reversal asked for a fact that is changed through its bill
// (a recognition, an adjustment) or through undoing the payment (a settlement).
var ErrNotManual = errors.New("ledger: only transfers and opening balances are reversed directly")

// LedgerRepository stores a space's management ledger (ADR 0024).
//
// Every method takes a space.ResolvedSpace and nothing else that names a
// partition. There is deliberately no update or delete of a transaction or an
// entry: a correction is a reversal (TestLedgerRepositoryHasNoEditPath).
type LedgerRepository struct {
	accounts Base
	txs      Base
	audit    Base
}

func NewLedgerRepository(db *dynamodb.Client, cfg *config.Config) *LedgerRepository {
	return &LedgerRepository{
		accounts: NewBase(db, cfg, TableLedgerAccounts),
		txs:      NewBase(db, cfg, TableLedgerTransactions),
		audit:    NewBase(db, cfg, TableAudit),
	}
}

// AccountRow is an account with its cached balance (debit positive).
type AccountRow struct {
	finance.LedgerAccount
	Balance  billing.Cents
	Archived bool
}

type accountItem struct {
	keys
	ID     string               `dynamodbav:"id"`
	Name   string               `dynamodbav:"name"`
	Class  finance.AccountClass `dynamodbav:"class"`
	Group  finance.DREGroup     `dynamodbav:"dre_group,omitempty"`
	System bool                 `dynamodbav:"system,omitempty"`
	// SystemKey marks a default account so clients can translate its name.
	SystemKey string `dynamodbav:"system_key,omitempty"`
	Balance   int64  `dynamodbav:"balance"`
	// Archived hides an account from new activity; it is never deleted, because
	// its entries and reports must keep resolving.
	Archived bool `dynamodbav:"archived,omitempty"`
}

func (i accountItem) row() AccountRow {
	return AccountRow{
		LedgerAccount: finance.LedgerAccount{ID: i.ID, Name: i.Name, Class: i.Class, Group: i.Group, System: i.System, SystemKey: i.SystemKey},
		Balance:       billing.Cents(i.Balance),
		Archived:      i.Archived,
	}
}

func newAccountItem(sp space.ResolvedSpace, a finance.LedgerAccount, now time.Time) accountItem {
	return accountItem{
		keys: newKeys(sp.PK(), LedgerAccountSK(a.ID), RetentionPermanent, now),
		ID:   a.ID, Name: a.Name, Class: a.Class, Group: a.Group, System: a.System, SystemKey: a.SystemKey,
	}
}

// EnsureSpace creates the space's settings row and its three system accounts,
// once. Safe to call on every first write: when the space already exists the
// conditional write fails and that is success.
func (r *LedgerRepository) EnsureSpace(ctx context.Context, sp space.ResolvedSpace, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	settings, err := Encode(struct {
		keys
		Mode string `dynamodbav:"mode"`
	}{newKeys(sp.PK(), LedgerSpaceSK(), RetentionPermanent, now), sp.Mode()})
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{r.accounts.BuildPutTxItemIfAbsent(settings)}
	_, system := finance.DefaultSystemAccounts()
	for _, a := range system {
		item, err := Encode(newAccountItem(sp, a, now))
		if err != nil {
			return err
		}
		items = append(items, r.accounts.BuildPutTxItemIfAbsent(item))
	}
	err = r.accounts.TransactWrite(ctx, items)
	if err != nil && onlyConditionFailed(err) {
		return nil // the space already exists
	}
	return err
}

// EnsureReady makes the space usable: its settings row and system accounts exist
// and its default categories are seeded. It is what the finance routes call, a
// GET included, so the common case must be a read.
//
// One GetItem of the SPACE row decides: when it is there at the current seed
// version nothing is written. The creation transaction (four conditional puts)
// only runs for a space that does not exist yet; running it on every request,
// as EnsureSpace alone did, spent write capacity on puts that were bound to
// fail their condition, and parallel first requests cancelled each other with
// TransactionConflict. A conflict is not a verdict: another request is creating
// the same rows, so look again after a short wait instead of failing the read.
func (r *LedgerRepository) EnsureReady(ctx context.Context, sp space.ResolvedSpace, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	const attempts = 4
	for attempt := 1; ; attempt++ {
		ready, exists, err := r.spaceState(ctx, sp)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if !exists {
			err = r.EnsureSpace(ctx, sp, now)
			if err != nil && retryableCancel(err) && attempt < attempts {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
				}
				continue
			}
			if err != nil {
				return err
			}
		}
		return r.SeedCategories(ctx, sp, now)
	}
}

// spaceState reads the SPACE row: whether it exists and whether it is seeded at
// the current version.
func (r *LedgerRepository) spaceState(ctx context.Context, sp space.ResolvedSpace) (ready, exists bool, err error) {
	raw, err := r.accounts.GetItem(ctx, sp.PK(), LedgerSpaceSK())
	if err != nil || raw == nil {
		return false, false, err
	}
	it, err := Decode[struct {
		Seed int `dynamodbav:"seed_version"`
	}](raw)
	if err != nil {
		return false, true, err
	}
	return it.Seed >= seedVersion, true, nil
}

// seedVersion is the version of finance.DefaultCategories a space was seeded
// with, kept on its SPACE row. Raising it seeds a space again (only what is
// missing is added).
// Version 2 backfills system_key onto the defaults seeded by version 1.
// Version 3 adds the billing categories (6.7).
const seedVersion = 3

// SeedCategories adds the space's default categories (spec § 3.3), once per
// seed version. Each is a conditional put, so one the person archived is never
// brought back, and a default whose name the person already used is skipped
// rather than duplicated.
func (r *LedgerRepository) SeedCategories(ctx context.Context, sp space.ResolvedSpace, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	raw, err := r.accounts.GetItem(ctx, sp.PK(), LedgerSpaceSK())
	if err != nil {
		return err
	}
	if raw != nil {
		it, err := Decode[struct {
			Seed int `dynamodbav:"seed_version"`
		}](raw)
		if err != nil {
			return err
		}
		if it.Seed >= seedVersion {
			return nil
		}
	}
	existing, err := r.ListAccounts(ctx, sp)
	if err != nil {
		return err
	}
	taken := make(map[string]string, len(existing)) // name -> id
	byID := make(map[string]AccountRow, len(existing))
	for _, a := range existing {
		taken[strings.ToLower(strings.TrimSpace(a.Name))] = a.ID
		byID[a.ID] = a
	}
	_, system := finance.DefaultSystemAccounts()
	// A personal workspace is a household's books, like the default personal
	// space: it gets the personal chart (ADR 0027).
	personalChart := sp.Kind() != space.KindOrganization
	for _, a := range append(system, finance.DefaultCategories(personalChart)...) {
		if cur, ok := byID[a.ID]; ok {
			// Already there (seeded by an older version): only the missing key is
			// added; its name, group and archived state are the person's.
			if cur.SystemKey == "" && a.SystemKey != "" {
				if err := r.backfillSystemKey(ctx, sp, a, now); err != nil {
					return err
				}
			}
			continue
		}
		if a.System || taken[strings.ToLower(a.Name)] != "" {
			continue
		}
		item, err := Encode(newAccountItem(sp, a, now))
		if err != nil {
			return err
		}
		err = r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildPutTxItemIfAbsent(item)))
		if err != nil && !onlyConditionFailed(err) {
			return err
		}
	}
	sk := LedgerSpaceSK()
	return r.accounts.UpsertAttrs(ctx, sp.PK(), &sk, map[string]any{"seed_version": seedVersion, "updated_at": now.UTC().Format(time.RFC3339Nano)})
}

// backfillSystemKey sets system_key on an existing default row that lacks it.
// Conditional, so a concurrent process or a later run is a no-op.
func (r *LedgerRepository) backfillSystemKey(ctx context.Context, sp space.ResolvedSpace, a finance.LedgerAccount, now time.Time) error {
	sk := LedgerAccountSK(a.ID)
	err := r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET system_key = :k, updated_at = :now", "attribute_exists(pk) AND attribute_not_exists(system_key)", nil,
		map[string]types.AttributeValue{
			":k":   &types.AttributeValueMemberS{Value: a.SystemKey},
			":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		})))
	if err != nil && !onlyConditionFailed(err) {
		return err
	}
	return nil
}

// ensureCategory makes sure the space holds the default category a posting rule
// names, and returns its id. Seeding normally made it; it is missing when the
// person already had a category of that name when the seed ran (the seed never
// duplicates a name), and then it is created by id anyway: a posting rule must
// find its category, and a second "Assinaturas" is less wrong than revenue with
// nowhere to go. An archived one is still used — archiving hides a category from
// what a person records next, and this fact is billing's, not theirs.
func (r *LedgerRepository) ensureCategory(ctx context.Context, sp space.ResolvedSpace, want finance.LedgerAccount, now time.Time) (string, error) {
	cur, err := r.getAccount(ctx, sp, want.ID)
	switch {
	case err == nil:
		if cur.Class != want.Class || cur.System {
			return "", fmt.Errorf("%w: %s is not a %s category", finance.ErrInvalidAccount, want.ID, want.Class)
		}
		return cur.ID, nil
	case !errors.Is(err, ErrNotFound):
		return "", err
	}
	item, err := Encode(newAccountItem(sp, want, now))
	if err != nil {
		return "", err
	}
	err = r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildPutTxItemIfAbsent(item)))
	if err != nil && !onlyConditionFailed(err) {
		return "", err
	}
	return want.ID, nil
}

// CreateAccount adds an account or category to the space's chart.
func (r *LedgerRepository) CreateAccount(ctx context.Context, sp space.ResolvedSpace, a finance.LedgerAccount, now time.Time) error {
	if err := sp.Require(space.Configure); err != nil {
		return err
	}
	if a.System {
		return fmt.Errorf("%w: system accounts are created with the space", finance.ErrInvalidAccount)
	}
	if err := a.Validate(); err != nil {
		return err
	}
	item, err := Encode(newAccountItem(sp, a, now))
	if err != nil {
		return err
	}
	err = r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildPutTxItemIfAbsent(item)))
	if err != nil && onlyConditionFailed(err) {
		return fmt.Errorf("%w: account %s already exists", finance.ErrInvalidAccount, a.ID)
	}
	return err
}

// GetAccount reads one account inside the space.
func (r *LedgerRepository) GetAccount(ctx context.Context, sp space.ResolvedSpace, id string) (*AccountRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	return r.getAccount(ctx, sp, id)
}

func (r *LedgerRepository) getAccount(ctx context.Context, sp space.ResolvedSpace, id string) (*AccountRow, error) {
	raw, err := r.accounts.GetItem(ctx, sp.PK(), LedgerAccountSK(id))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrNotFound
	}
	it, err := Decode[accountItem](raw)
	if err != nil {
		return nil, err
	}
	row := it.row()
	return &row, nil
}

// ArchiveAccount hides an account or category from new activity. Idempotent; a
// system account cannot be archived; nothing is ever deleted.
func (r *LedgerRepository) ArchiveAccount(ctx context.Context, sp space.ResolvedSpace, id string, now time.Time) error {
	if err := sp.Require(space.Configure); err != nil {
		return err
	}
	acct, err := r.getAccount(ctx, sp, id)
	if err != nil {
		return err
	}
	if acct.System {
		return fmt.Errorf("%w: system accounts cannot be archived", finance.ErrInvalidAccount)
	}
	sk := LedgerAccountSK(id)
	return r.accounts.TransactWrite(ctx, txItems(r.accounts.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET archived = :t, updated_at = :now", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{
			":t":   &types.AttributeValueMemberBOOL{Value: true},
			":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		})))
}

// Settings are the space's own preferences, on its SPACE row.
type Settings struct {
	DefaultReceivingAccountID string
	// PostCTechInvoices is "Lançar minhas faturas da CTech automaticamente neste
	// espaço" (spec § 3.8): when false, a paid CTech invoice writes nothing in
	// this space as the payer. On when the attribute is absent.
	PostCTechInvoices bool
}

// GetSettings reads the space's settings; a space with none yet has the zero value.
func (r *LedgerRepository) GetSettings(ctx context.Context, sp space.ResolvedSpace) (Settings, error) {
	if err := sp.Require(space.Read); err != nil {
		return Settings{}, err
	}
	raw, err := r.accounts.GetItem(ctx, sp.PK(), LedgerSpaceSK())
	if err != nil {
		return Settings{}, err
	}
	if raw == nil {
		return Settings{PostCTechInvoices: true}, nil
	}
	it, err := Decode[struct {
		Default string `dynamodbav:"default_receiving_account_id"`
		Post    *bool  `dynamodbav:"post_ctech_invoices"`
	}](raw)
	if err != nil {
		return Settings{}, err
	}
	return Settings{DefaultReceivingAccountID: it.Default, PostCTechInvoices: it.Post == nil || *it.Post}, nil
}

// SetPostCTechInvoices turns "Lançar minhas faturas da CTech automaticamente
// neste espaço" on or off, with an audit row in the same write. Future postings
// only: turning it off deletes nothing, and turning it back on replays nothing.
func (r *LedgerRepository) SetPostCTechInvoices(ctx context.Context, sp space.ResolvedSpace, on bool, actor, requestID string, now time.Time) error {
	if err := sp.Require(space.Configure); err != nil {
		return err
	}
	if err := r.EnsureReady(ctx, sp, now); err != nil {
		return err
	}
	after := map[bool]string{true: "on", false: "off"}[on]
	audit, err := buildAuditItem(sp.Owner(), sp.Livemode(), AuditEntry{
		Entity: EntityFinanceSettings, EntityID: sp.PK(), Action: "finance.settings.post_ctech_invoices_changed",
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID, After: after,
	}, "", "", now)
	if err != nil {
		return err
	}
	sk := LedgerSpaceSK()
	return r.accounts.TransactWrite(ctx, txItems(
		r.accounts.BuildRawUpdateTxItem(sp.PK(), &sk, "SET post_ctech_invoices = :on, updated_at = :now", "attribute_exists(pk)", nil,
			map[string]types.AttributeValue{
				":on":  &types.AttributeValueMemberBOOL{Value: on},
				":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
			}),
		r.audit.BuildPutTxItem(audit),
	))
}

// SetDefaultReceivingAccount names the asset account receivables settle into by
// default. It must exist in this space, be an asset the user holds (not a system
// account) and not be archived.
func (r *LedgerRepository) SetDefaultReceivingAccount(ctx context.Context, sp space.ResolvedSpace, accountID string, now time.Time) error {
	if err := sp.Require(space.Configure); err != nil {
		return err
	}
	acct, err := r.getAccount(ctx, sp, accountID)
	if errors.Is(err, ErrNotFound) {
		return ErrUnknownAccount
	}
	if err != nil {
		return err
	}
	if acct.System || acct.Archived || acct.Class != finance.ClassAsset {
		return fmt.Errorf("%w: the default receiving account must be an active asset account", finance.ErrInvalidAccount)
	}
	sk := LedgerSpaceSK()
	return r.accounts.UpsertAttrs(ctx, sp.PK(), &sk, map[string]any{
		"default_receiving_account_id": accountID, "updated_at": now.UTC().Format(time.RFC3339Nano),
	})
}

// ListAccounts returns the chart: one Query on the space partition.
func (r *LedgerRepository) ListAccounts(ctx context.Context, sp space.ResolvedSpace) ([]AccountRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.queryPrefix(ctx, r.accounts, sp.PK(), "ACCOUNT#")
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeItems[accountItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]AccountRow, len(decoded))
	for i, it := range decoded {
		out[i] = it.row()
	}
	return out, nil
}

// queryPrefix returns every item under pk whose sort key begins with skPrefix,
// in ascending order, following continuation keys to the end. A space's chart
// and a period's entries are bounded by the user's own data, never by a
// cross-tenant scan.
func (r *LedgerRepository) queryPrefix(ctx context.Context, b Base, pk, skPrefix string) ([]map[string]types.AttributeValue, error) {
	var out []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		res, err := b.Query(ctx, QueryOpts{
			PK: pk, SKPrefix: skPrefix, ScanIndexForward: true, Limit: 100, ExclusiveStartKey: start, ConsistentRead: true,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// PostMeta is the provenance written beside a transaction.
type PostMeta struct {
	Origin    string // e.g. "manual", "bill_settlement", "invoice_paid"
	Actor     string // the token subject
	RequestID string
	// IdempotencyKey, when set on a create, makes the new row's id a function of
	// (space, key): two concurrent requests with one key then collide on the
	// conditional put and make ONE row. The idempotency middleware only replays
	// requests that already finished.
	IdempotencyKey string
	Memo           string // shown on the statement
	Ref            string // "bill:{id}" when a bill produced the fact
	// txID, when set, is the transaction's id instead of a fresh one: a fact
	// whose id comes from its idempotency key collides with its own duplicate.
	txID string
}

type txItem struct {
	keys
	ID      string         `dynamodbav:"id"`
	Kind    finance.TxKind `dynamodbav:"kind"`
	Date    string         `dynamodbav:"date"`
	Legs    []legItem      `dynamodbav:"legs"`
	Adjusts string         `dynamodbav:"adjusts,omitempty"`
	Origin  string         `dynamodbav:"origin,omitempty"`
	Actor   string         `dynamodbav:"actor,omitempty"`
	Memo    string         `dynamodbav:"memo,omitempty"`
	Ref     string         `dynamodbav:"ref,omitempty"`
}

type legItem struct {
	Account string `dynamodbav:"account"`
	Amount  int64  `dynamodbav:"amount"`
	Flow    string `dynamodbav:"flow,omitempty"`
}

// entryItem is one leg on its account's partition. Kind, flow, memo and ref are
// what the statement and the cash flow read without loading the header; a
// reversal's entries carry the ORIGINAL's kind, flow, memo and ref, marked
// Reversal. Entries posted before 6.4 have none of them.
type entryItem struct {
	keys
	TxID     string         `dynamodbav:"tx_id"`
	Date     string         `dynamodbav:"date"`
	Leg      int            `dynamodbav:"leg"`
	Amount   int64          `dynamodbav:"amount"`
	Kind     finance.TxKind `dynamodbav:"kind,omitempty"`
	Flow     string         `dynamodbav:"flow,omitempty"`
	Memo     string         `dynamodbav:"memo,omitempty"`
	Ref      string         `dynamodbav:"ref,omitempty"`
	Reversal bool           `dynamodbav:"reversal,omitempty"`
}

// Post writes one balanced transaction atomically: the header, one entry per
// leg, an ADD on each touched balance and monthly summary, the reversal marker
// when it is a reversal, and the audit row — one TransactWriteItems, so either
// all of it or none. Every leg's account must already exist in THIS partition
// (attribute_exists on the balance update); an account id from another space is
// therefore ErrUnknownAccount and nothing is written.
func (r *LedgerRepository) Post(ctx context.Context, sp space.ResolvedSpace, tx finance.Transaction, meta PostMeta, now time.Time) (string, error) {
	// A reversal is only made by Reverse, which loads the original and checks the
	// verb its kind needs. Accepting one here would let a caller undo a settlement
	// without finance.settle, or burn another transaction's single-use marker.
	if tx.Adjusts != "" || tx.Kind == finance.KindReversal {
		return "", fmt.Errorf("%w: reversals are made with Reverse", finance.ErrInvalidTransaction)
	}
	return r.post(ctx, sp, tx, meta, now)
}

// ledgerPlan is what a fact writes to the ledger tables, built but not sent. The
// bill repository appends its own items to Items so the bill and its ledger
// entries commit together or not at all (spec § 4: one TransactWriteItems).
type ledgerPlan struct {
	TxID  string
	Items []types.TransactWriteItem
	// MarkerIdx is the index of the reversal marker in Items, or -1.
	MarkerIdx int
}

// planPost validates a transaction against the space's verbs and builds its
// write items. It writes nothing.
func (r *LedgerRepository) planPost(sp space.ResolvedSpace, tx finance.Transaction, meta PostMeta, now time.Time) (ledgerPlan, error) {
	return r.planPostAs(sp, tx, meta, now, tx.Kind, false)
}

// planPostAs is planPost with the kind the entries carry: a reversal's entries
// keep the original's kind, so the reports place them where the original was.
func (r *LedgerRepository) planPostAs(sp space.ResolvedSpace, tx finance.Transaction, meta PostMeta, now time.Time, entryKind finance.TxKind, reversal bool) (ledgerPlan, error) {
	need := space.Write
	if tx.Kind == finance.KindSettlement {
		need |= space.Settle
	}
	if err := sp.Require(need); err != nil {
		return ledgerPlan{}, err
	}
	// Revalidate: a Transaction is only trusted if it went through
	// NewTransaction, and a zero or hand-built one must not reach the table.
	checked, err := finance.NewTransaction(tx.Kind, tx.Date, tx.Legs...)
	if err != nil {
		return ledgerPlan{}, err
	}
	checked.Adjusts = tx.Adjusts

	txID := id.New()
	if meta.txID != "" {
		txID = meta.txID
	}
	items, markerIdx, err := r.postItems(sp, txID, checked, meta, now, entryKind, reversal)
	if err != nil {
		return ledgerPlan{}, err
	}
	return ledgerPlan{TxID: txID, Items: items, MarkerIdx: markerIdx}, nil
}

func (r *LedgerRepository) post(ctx context.Context, sp space.ResolvedSpace, tx finance.Transaction, meta PostMeta, now time.Time) (string, error) {
	plan, err := r.planPost(sp, tx, meta, now)
	if err != nil {
		return "", err
	}
	if err := r.txs.TransactWrite(ctx, plan.Items); err != nil {
		return "", classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, nil
}

// cancellationCodes returns the per-item reason codes of a cancelled
// transaction, or nil when err is not one.
func cancellationCodes(err error) []string {
	var tc *types.TransactionCanceledException
	if !errors.As(err, &tc) {
		return nil
	}
	codes := make([]string, len(tc.CancellationReasons))
	for i, r := range tc.CancellationReasons {
		if r.Code != nil {
			codes[i] = *r.Code
		}
	}
	return codes
}

const (
	codeNone            = "None"
	codeConditionFailed = "ConditionalCheckFailed"
	codeConflict        = "TransactionConflict"
)

// retryableCancel reports a cancellation caused by another transaction touching
// the same items: at least one TransactionConflict, and nothing worse among the
// reasons. Whether the write would succeed was not decided, so it can be retried.
func retryableCancel(err error) bool {
	seen := false
	for _, c := range cancellationCodes(err) {
		switch c {
		case "", codeNone, codeConditionFailed:
		case codeConflict:
			seen = true
		default:
			return false
		}
	}
	return seen
}

// onlyConditionFailed reports a cancellation whose every non-trivial reason is a
// failed condition. A conflict or a throttle among the reasons is not "it already
// exists": nothing was decided, and the caller must see the original error.
func onlyConditionFailed(err error) bool {
	codes := cancellationCodes(err)
	seen := false
	for _, c := range codes {
		switch c {
		case "", codeNone:
		case codeConditionFailed:
			seen = true
		default:
			return false
		}
	}
	return seen
}

// classifyPostCancel maps a cancelled Post to the error a caller can act on. Only
// a failed condition is a verdict (the marker already exists, or a leg's account
// is not in this space); a conflict or throttle comes back unchanged so it can be
// retried instead of being reported as a permanent client error.
func classifyPostCancel(err error, markerIdx int) error {
	codes := cancellationCodes(err)
	if !onlyConditionFailed(err) {
		return err
	}
	if markerIdx >= 0 && markerIdx < len(codes) && codes[markerIdx] == codeConditionFailed {
		return ErrAlreadyReversed
	}
	return ErrUnknownAccount
}

func (r *LedgerRepository) postItems(sp space.ResolvedSpace, txID string, tx finance.Transaction, meta PostMeta, now time.Time, entryKind finance.TxKind, reversal bool) (items []types.TransactWriteItem, markerIdx int, err error) {
	markerIdx = -1
	legs := make([]legItem, len(tx.Legs))
	for i, l := range tx.Legs {
		legs[i] = legItem{Account: l.AccountID, Amount: int64(l.Amount), Flow: l.Flow}
	}
	header, err := Encode(txItem{
		keys: newKeys(sp.PK(), LedgerTxSK(txID), RetentionPermanent, now),
		ID:   txID, Kind: tx.Kind, Date: tx.Date.String(), Legs: legs,
		Adjusts: tx.Adjusts, Origin: meta.Origin, Actor: meta.Actor, Memo: meta.Memo, Ref: meta.Ref,
	})
	if err != nil {
		return nil, -1, err
	}
	items = append(items, r.txs.BuildPutTxItemIfAbsent(header))

	month := finance.MonthOf(tx.Date)
	for i, l := range tx.Legs {
		entry, err := Encode(entryItem{
			keys: newKeys(LedgerEntryPK(sp, l.AccountID), LedgerEntrySK(tx.Date, txID, i), RetentionPermanent, now),
			TxID: txID, Date: tx.Date.String(), Leg: i, Amount: int64(l.Amount),
			Kind: entryKind, Flow: l.Flow, Memo: meta.Memo, Ref: meta.Ref, Reversal: reversal,
		})
		if err != nil {
			return nil, -1, err
		}
		items = append(items, r.txs.BuildPutTxItemIfAbsent(entry))

		acctSK := LedgerAccountSK(l.AccountID)
		items = append(items, r.accounts.BuildRawUpdateTxItem(sp.PK(), &acctSK,
			"ADD balance :amt", "attribute_exists(pk)", nil,
			map[string]types.AttributeValue{":amt": numberValue(int64(l.Amount))}))

		var debit, credit int64
		if l.Amount > 0 {
			debit = int64(l.Amount)
		} else {
			credit = int64(-l.Amount)
		}
		sumSK := LedgerSummarySK(month, l.AccountID)
		items = append(items, r.accounts.BuildRawUpdateTxItem(sp.PK(), &sumSK,
			"ADD debits :d, credits :c", "", nil,
			map[string]types.AttributeValue{":d": numberValue(debit), ":c": numberValue(credit)}))
	}

	if tx.Adjusts != "" {
		marker, err := Encode(struct {
			keys
			TxID string `dynamodbav:"tx_id"`
		}{newKeys(sp.PK(), LedgerReversalSK(tx.Adjusts), RetentionPermanent, now), txID})
		if err != nil {
			return nil, -1, err
		}
		markerIdx = len(items)
		items = append(items, r.txs.BuildPutTxItemIfAbsent(marker))
	}

	audit, err := buildAuditItem(sp.Owner(), sp.Livemode(), AuditEntry{
		Entity: EntityLedgerTx, EntityID: txID, Action: "ledger.transaction.posted",
		Actor: meta.Actor, RequestID: meta.RequestID,
	}, "", "", now)
	if err != nil {
		return nil, -1, err
	}
	items = append(items, r.audit.BuildPutTxItem(audit))
	return items, markerIdx, nil
}

func numberValue(n int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}
}

// StoredTx is a transaction as written, with its id.
type StoredTx struct {
	ID        string
	Tx        finance.Transaction
	Origin    string
	Memo, Ref string
}

// GetTransaction reads a transaction inside the space. An id from another space
// is ErrNotFound: the lookup key is built from the resolved space.
func (r *LedgerRepository) GetTransaction(ctx context.Context, sp space.ResolvedSpace, txID string) (*StoredTx, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	raw, err := r.txs.GetItem(ctx, sp.PK(), LedgerTxSK(txID))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: transaction %s", ErrNotFound, txID)
	}
	it, err := Decode[txItem](raw)
	if err != nil {
		return nil, err
	}
	return it.stored()
}

func (i txItem) stored() (*StoredTx, error) {
	date, err := brcal.Parse(i.Date)
	if err != nil {
		return nil, fmt.Errorf("transaction %s has a malformed date: %w", i.ID, err)
	}
	legs := make([]finance.Leg, len(i.Legs))
	for n, l := range i.Legs {
		legs[n] = finance.Leg{AccountID: l.Account, Amount: billing.Cents(l.Amount), Flow: l.Flow}
	}
	tx, err := finance.NewTransaction(i.Kind, date, legs...)
	if err != nil {
		return nil, fmt.Errorf("stored transaction %s is not balanced: %w", i.ID, err)
	}
	tx.Adjusts = i.Adjusts
	return &StoredTx{ID: i.ID, Tx: tx, Origin: i.Origin, Memo: i.Memo, Ref: i.Ref}, nil
}

// Reverse posts the exact opposite of a stored transaction on date. Both the
// mistake and its correction stay in the record (Fowler's Reversal Adjustment).
// A second reversal of the same transaction is ErrAlreadyReversed.
func (r *LedgerRepository) Reverse(ctx context.Context, sp space.ResolvedSpace, originalID string, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	plan, err := r.planReverse(ctx, sp, originalID, date, meta, now)
	if err != nil {
		return "", err
	}
	if err := r.txs.TransactWrite(ctx, plan.Items); err != nil {
		return "", classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, nil
}

// planReverse loads the original, applies the verb its kind needs and builds the
// reversal's write items. It writes nothing; the bill repository composes it
// with a status change.
func (r *LedgerRepository) planReverse(ctx context.Context, sp space.ResolvedSpace, originalID string, date brcal.Date, meta PostMeta, now time.Time) (ledgerPlan, error) {
	if err := sp.Require(space.Write); err != nil {
		return ledgerPlan{}, err
	}
	orig, err := r.GetTransaction(ctx, sp, originalID)
	if err != nil {
		return ledgerPlan{}, err
	}
	// Undoing a settlement is settling: the verb guards the effect, so a writer
	// without finance.settle cannot reopen what only a settler could close.
	if orig.Tx.Kind == finance.KindSettlement {
		if err := sp.Require(space.Settle); err != nil {
			return ledgerPlan{}, err
		}
	}
	rev, err := finance.Reverse(orig.Tx, originalID, date)
	if err != nil {
		return ledgerPlan{}, err
	}
	if meta.Memo == "" {
		meta.Memo = orig.Memo
	}
	if meta.Ref == "" {
		meta.Ref = orig.Ref
	}
	return r.planPostAs(sp, rev, meta, now, orig.Tx.Kind, true)
}

// EntriesFrom returns an account's entries dated from onward, in key order, with
// Reversed set on those whose transaction has been reversed. It is what a
// statement and the cash flow fold: the entries since from give the period's
// rows and, subtracted from the cached balance, the opening balance.
func (r *LedgerRepository) EntriesFrom(ctx context.Context, sp space.ResolvedSpace, accountID string, from brcal.Date) ([]finance.Entry, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.queryRange(ctx, r.txs, LedgerEntryPK(sp, accountID), "ENTRY#"+from.String(), "ENTRY#~")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[entryItem](items)
	if err != nil {
		return nil, err
	}
	reversed, err := r.reversedIDs(ctx, sp)
	if err != nil {
		return nil, err
	}
	out := make([]finance.Entry, len(rows))
	for i, e := range rows {
		d, err := brcal.Parse(e.Date)
		if err != nil {
			return nil, fmt.Errorf("entry %s has a malformed date: %w", e.SK, err)
		}
		out[i] = finance.Entry{
			AccountID: accountID, TxID: e.TxID, Leg: e.Leg, Date: d, Amount: billing.Cents(e.Amount),
			Kind: e.Kind, Flow: e.Flow, Memo: e.Memo, Ref: e.Ref, Reversal: e.Reversal, Reversed: reversed[e.TxID],
		}
	}
	return out, nil
}

// reversedIDs reads the space's reversal markers: one Query, and markers are few.
func (r *LedgerRepository) reversedIDs(ctx context.Context, sp space.ResolvedSpace) (map[string]bool, error) {
	items, err := r.queryPrefix(ctx, r.txs, sp.PK(), "REVERSAL#")
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(items))
	for _, it := range items {
		if v, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			out[strings.TrimPrefix(v.Value, "REVERSAL#")] = true
		}
	}
	return out, nil
}

// activeCash loads an account money can be moved in: an active asset the user
// holds. Anything else, a category or a system account included, is
// ErrUnknownAccount — it is not an account in that sense.
func (r *LedgerRepository) activeCash(ctx context.Context, sp space.ResolvedSpace, id string) error {
	acct, err := r.getAccount(ctx, sp, id)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrUnknownAccount, id)
	}
	if err != nil {
		return err
	}
	if acct.Class != finance.ClassAsset || acct.System || acct.Archived {
		return fmt.Errorf("%w: %s is not an active account", ErrUnknownAccount, id)
	}
	return nil
}

// PostOpeningBalance records what an account held before the ledger started,
// once per account: a conditional OPENING# marker in the same transaction makes
// a second one ErrOpeningExists. Reversing it removes the marker.
func (r *LedgerRepository) PostOpeningBalance(ctx context.Context, sp space.ResolvedSpace, accountID string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	if err := sp.Require(space.Configure); err != nil {
		return "", err
	}
	if err := r.activeCash(ctx, sp, accountID); err != nil {
		return "", err
	}
	sys, _ := finance.DefaultSystemAccounts()
	tx, err := finance.OpeningBalance(sys, accountID, amount, date)
	if err != nil {
		return "", err
	}
	if meta.Memo == "" {
		meta.Memo = "Saldo inicial"
	}
	plan, err := r.planPost(sp, tx, meta, now)
	if err != nil {
		return "", err
	}
	marker, err := Encode(struct {
		keys
		TxID string `dynamodbav:"tx_id"`
	}{newKeys(sp.PK(), LedgerOpeningSK(accountID), RetentionPermanent, now), plan.TxID})
	if err != nil {
		return "", err
	}
	openingIdx := len(plan.Items)
	items := append(plan.Items, r.accounts.BuildPutTxItemIfAbsent(marker))
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		if codes := cancellationCodes(err); onlyConditionFailed(err) && codes[openingIdx] == codeConditionFailed {
			return "", ErrOpeningExists
		}
		return "", classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, nil
}

// PostTransfer moves money between two of the space's own accounts. It is not
// income or spending: both legs carry FlowNone.
func (r *LedgerRepository) PostTransfer(ctx context.Context, sp space.ResolvedSpace, from, to string, amount billing.Cents, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	if err := sp.Require(space.Write); err != nil {
		return "", err
	}
	if from == to {
		return "", fmt.Errorf("%w: a transfer needs two different accounts", finance.ErrInvalidTransaction)
	}
	for _, id := range []string{from, to} {
		if err := r.activeCash(ctx, sp, id); err != nil {
			return "", err
		}
	}
	tx, err := finance.Transfer(from, to, amount, date)
	if err != nil {
		return "", err
	}
	if meta.Memo == "" {
		meta.Memo = "Transferência"
	}
	if meta.IdempotencyKey == "" {
		return r.post(ctx, sp, tx, meta, now)
	}
	// Two overlapping requests with one key (a retry while the first is in
	// flight) build the same header; the second's conditional put fails and it
	// answers with the transfer that exists, so money moves once.
	meta.txID = idempotentID(sp, "transfer", meta.IdempotencyKey)
	plan, err := r.planPost(sp, tx, meta, now)
	if err != nil {
		return "", err
	}
	if err := r.txs.TransactWrite(ctx, plan.Items); err != nil {
		if codes := cancellationCodes(err); onlyConditionFailed(err) && len(codes) > 0 && codes[0] == codeConditionFailed {
			return plan.TxID, nil
		}
		return "", classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, nil
}

// ReverseManual reverses a transfer or an opening balance, the facts a person
// posts directly. Bill facts are changed through the bill. Reversing an opening
// balance also frees its account for a corrected one.
func (r *LedgerRepository) ReverseManual(ctx context.Context, sp space.ResolvedSpace, txID string, date brcal.Date, meta PostMeta, now time.Time) (string, error) {
	orig, err := r.GetTransaction(ctx, sp, txID)
	if err != nil {
		return "", err
	}
	if orig.Tx.Kind != finance.KindTransfer && orig.Tx.Kind != finance.KindOpeningBalance {
		return "", ErrNotManual
	}
	if orig.Tx.Kind == finance.KindOpeningBalance {
		// Posting one needs configure; so does taking it back.
		if err := sp.Require(space.Configure); err != nil {
			return "", err
		}
	}
	plan, err := r.planReverse(ctx, sp, txID, date, meta, now)
	if err != nil {
		return "", err
	}
	items := plan.Items
	if orig.Tx.Kind == finance.KindOpeningBalance {
		for _, l := range orig.Tx.Legs {
			if l.Flow == finance.FlowNone {
				items = append(items, r.accounts.BuildDeleteTxItem(sp.PK(), LedgerOpeningSK(l.AccountID)))
			}
		}
	}
	if err := r.txs.TransactWrite(ctx, items); err != nil {
		return "", classifyPostCancel(err, plan.MarkerIdx)
	}
	return plan.TxID, nil
}

// EntryRow is one leg of a posted transaction on an account.
type EntryRow struct {
	TxID   string
	Date   brcal.Date
	Leg    int
	Amount billing.Cents
}

// Statement returns an account's entries with from <= date < to, in order. It
// is one range Query on the account's entry partition.
func (r *LedgerRepository) Statement(ctx context.Context, sp space.ResolvedSpace, accountID string, from, to brcal.Date) ([]EntryRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	if !from.Before(to) {
		return nil, nil // BETWEEN with lo > hi is a DynamoDB validation error
	}
	items, err := r.queryRange(ctx, r.txs, LedgerEntryPK(sp, accountID),
		"ENTRY#"+from.String(), "ENTRY#"+to.String())
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[entryItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]EntryRow, len(rows))
	for i, e := range rows {
		d, err := brcal.Parse(e.Date)
		if err != nil {
			return nil, fmt.Errorf("entry %s has a malformed date: %w", e.SK, err)
		}
		out[i] = EntryRow{TxID: e.TxID, Date: d, Leg: e.Leg, Amount: billing.Cents(e.Amount)}
	}
	return out, nil
}

// SummaryRow is one account's debits and credits in one month.
type SummaryRow struct {
	Month     finance.Month
	AccountID string
	finance.Totals
}

type summaryItem struct {
	keys
	Debits  int64 `dynamodbav:"debits"`
	Credits int64 `dynamodbav:"credits"`
}

// Summaries returns every SUMMARY row for the months from..to inclusive: one
// range Query, which is what makes a DRE or a cash flow cheap.
func (r *LedgerRepository) Summaries(ctx context.Context, sp space.ResolvedSpace, from, to finance.Month) ([]SummaryRow, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	// "~" sorts after every character an account id or month uses, so the upper
	// bound includes every account of the last month.
	return r.summaryRange(ctx, sp, "SUMMARY#"+from.String(), "SUMMARY#"+to.String()+"#~")
}

func (r *LedgerRepository) allSummaries(ctx context.Context, sp space.ResolvedSpace) ([]SummaryRow, error) {
	return r.summaryRange(ctx, sp, "SUMMARY#", "SUMMARY#~")
}

func (r *LedgerRepository) summaryRange(ctx context.Context, sp space.ResolvedSpace, lo, hi string) ([]SummaryRow, error) {
	items, err := r.queryRange(ctx, r.accounts, sp.PK(), lo, hi)
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[summaryItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]SummaryRow, 0, len(rows))
	for _, s := range rows {
		// SK is SUMMARY#yyyy-mm#account; an account id never contains '#'.
		parts := strings.SplitN(s.SK, "#", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("malformed summary key %q", s.SK)
		}
		d, err := brcal.Parse(parts[1] + "-01")
		if err != nil {
			return nil, fmt.Errorf("malformed summary month in %q: %w", s.SK, err)
		}
		out = append(out, SummaryRow{
			Month: finance.MonthOf(d), AccountID: parts[2],
			Totals: finance.Totals{Debits: billing.Cents(s.Debits), Credits: billing.Cents(s.Credits)},
		})
	}
	return out, nil
}

// AllTransactions returns every transaction in the space, for the rebuild
// command. Not on any request path: it reads the whole history.
func (r *LedgerRepository) AllTransactions(ctx context.Context, sp space.ResolvedSpace) ([]finance.Transaction, error) {
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	items, err := r.queryPrefix(ctx, r.txs, sp.PK(), "TX#")
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[txItem](items)
	if err != nil {
		return nil, err
	}
	out := make([]finance.Transaction, len(rows))
	for i, row := range rows {
		st, err := row.stored()
		if err != nil {
			return nil, err
		}
		out[i] = st.Tx
	}
	return out, nil
}

// queryRange returns the items under pk whose sort key is in [lo, hi], ascending,
// following continuation keys. DynamoDB allows one condition per key, so this is
// BETWEEN, which is inclusive: callers wanting [from, to) rely on no sort key
// being exactly "ENTRY#<to>" (entry keys always carry a #tx#leg suffix).
func (r *LedgerRepository) queryRange(ctx context.Context, b Base, pk, lo, hi string) ([]map[string]types.AttributeValue, error) {
	var out []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		res, err := b.QueryRaw(ctx, &dynamodb.QueryInput{
			KeyConditionExpression: aws.String("pk = :pk AND sk BETWEEN :lo AND :hi"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: pk},
				":lo": &types.AttributeValueMemberS{Value: lo},
				":hi": &types.AttributeValueMemberS{Value: hi},
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}
