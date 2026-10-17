package repositories

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/closure"
)

// Key shapes of the tenant_closures table:
//
//	CLOSURE_REQ#{id}        META   the request itself (carries schedule_pk/sk while it has a next action due)
//	CLOSURE#{accountOrgID}  OPEN   the tenant's single open-closure marker: closure id and state
//	CLOSING#{tenantOrgID}   FLAG   read-only flag of one billing tenant row (live or test), one GetItem per write
const (
	closureMetaSK = "META"
	closureOpenSK = "OPEN"
	closureFlagSK = "FLAG"
)

func closureReqPK(id string) string        { return "CLOSURE_REQ#" + id }
func closureOpenPK(acct string) string     { return "CLOSURE#" + acct }
func closureFlagPK(tenantID string) string { return "CLOSING#" + tenantID }

type closureRow struct {
	keys
	ID                    string   `dynamodbav:"id"`
	AccountOrganizationID string   `dynamodbav:"account_organization_id"`
	RequestedBy           string   `dynamodbav:"requested_by"`
	State                 string   `dynamodbav:"closure_state"`
	ConfirmTokenHash      string   `dynamodbav:"confirm_token_hash,omitempty"`
	CancelTokenHashes     []string `dynamodbav:"cancel_token_hashes,omitempty"`
	Blockers              []string `dynamodbav:"blockers,omitempty"`
	RequestedAt           string   `dynamodbav:"requested_at"`
	ConfirmBy             string   `dynamodbav:"confirm_by"`
	ConfirmedAt           string   `dynamodbav:"confirmed_at,omitempty"`
	GraceUntil            string   `dynamodbav:"grace_until,omitempty"`
	CancelledAt           string   `dynamodbav:"cancelled_at,omitempty"`
	ClosedAt              string   `dynamodbav:"closed_at,omitempty"`
	SchedulePK            string   `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK            string   `dynamodbav:"schedule_sk,omitempty"`
}

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// closureDue is when a closure's next action is due, and whether it has one.
// A blocked closure waits for the owner (cancel) and a closed one is done.
func closureDue(c *closure.Closure, now time.Time) (time.Time, bool) {
	switch c.State {
	case closure.AwaitingConfirmation:
		return c.ConfirmBy, true
	case closure.Closing:
		return c.GraceUntil, true
	case closure.ReadyToPurge, closure.Purging:
		return now, true
	}
	return time.Time{}, false
}

func (r *ClosureRepository) toRow(c *closure.Closure, now time.Time) closureRow {
	row := closureRow{
		keys: newKeys(closureReqPK(c.ID), closureMetaSK, RetentionAuditLog, now),
		ID:   c.ID, AccountOrganizationID: c.AccountOrganizationID, RequestedBy: c.RequestedBy,
		State: string(c.State), ConfirmTokenHash: c.ConfirmTokenHash, CancelTokenHashes: c.CancelTokenHashes, Blockers: c.Blockers,
		RequestedAt: ts(c.RequestedAt), ConfirmBy: ts(c.ConfirmBy), ConfirmedAt: ts(c.ConfirmedAt),
		GraceUntil: ts(c.GraceUntil), CancelledAt: ts(c.CancelledAt), ClosedAt: ts(c.ClosedAt),
	}
	if due, ok := closureDue(c, now); ok {
		row.SchedulePK, row.ScheduleSK = JobTenantClosures, ts(due)+"#"+c.ID
	}
	return row
}

func (row *closureRow) closure() *closure.Closure {
	return &closure.Closure{
		ID: row.ID, AccountOrganizationID: row.AccountOrganizationID, RequestedBy: row.RequestedBy,
		State: closure.State(row.State), ConfirmTokenHash: row.ConfirmTokenHash, CancelTokenHashes: row.CancelTokenHashes,
		Blockers: row.Blockers, RequestedAt: parseTS(row.RequestedAt), ConfirmBy: parseTS(row.ConfirmBy),
		ConfirmedAt: parseTS(row.ConfirmedAt), GraceUntil: parseTS(row.GraceUntil), CancelledAt: parseTS(row.CancelledAt),
		ClosedAt: parseTS(row.ClosedAt), UpdatedAt: parseTS(row.UpdatedAt),
	}
}

// ClosureRepository stores merchant-tenant closures.
type ClosureRepository struct {
	base Base
}

func NewClosureRepository(db *dynamodb.Client, cfg *config.Config) *ClosureRepository {
	return &ClosureRepository{base: NewBase(db, cfg, TableTenantClosures)}
}

func (r *ClosureRepository) put(item map[string]types.AttributeValue, cond string, names map[string]string, values map[string]types.AttributeValue) types.TransactWriteItem {
	p := &types.Put{TableName: aws.String(r.base.TableName), Item: item}
	if cond != "" {
		p.ConditionExpression = aws.String(cond)
		p.ExpressionAttributeNames = names
		p.ExpressionAttributeValues = values
	}
	return types.TransactWriteItem{Put: p}
}

func (r *ClosureRepository) markerItem(c *closure.Closure, now time.Time) (map[string]types.AttributeValue, error) {
	return Encode(struct {
		keys
		ClosureID string `dynamodbav:"closure_id"`
		State     string `dynamodbav:"closure_state"`
	}{newKeys(closureOpenPK(c.AccountOrganizationID), closureOpenSK, RetentionPermanent, now), c.ID, string(c.State)})
}

// Create stores a new request and takes the tenant's open marker in one
// transaction; ErrOpenClosure when the tenant already has an open closure.
func (r *ClosureRepository) Create(ctx context.Context, c *closure.Closure, now time.Time) error {
	row, err := Encode(r.toRow(c, now))
	if err != nil {
		return err
	}
	marker, err := r.markerItem(c, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(
		r.put(row, "attribute_not_exists(pk)", nil, nil),
		r.put(marker, "attribute_not_exists(pk)", nil, nil),
	))
	if err != nil {
		if IsTransactionConflict(err) || cancellationCodes(err) != nil {
			return closure.ErrOpenClosure
		}
		return err
	}
	return nil
}

func (r *ClosureRepository) Get(ctx context.Context, id string) (*closure.Closure, error) {
	item, err := r.base.GetItem(ctx, closureReqPK(id), closureMetaSK)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, closure.ErrNotFound
	}
	row, err := Decode[closureRow](item)
	if err != nil {
		return nil, err
	}
	return row.closure(), nil
}

// Open returns the tenant's open closure, or closure.ErrNotFound.
func (r *ClosureRepository) Open(ctx context.Context, accountOrgID string) (*closure.Closure, error) {
	item, err := r.base.GetItem(ctx, closureOpenPK(accountOrgID), closureOpenSK)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, closure.ErrNotFound
	}
	m, err := Decode[struct {
		ClosureID string `dynamodbav:"closure_id"`
	}](item)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, m.ClosureID)
}

// Save writes c if its stored state is still from. A state that is no longer
// open releases the tenant's marker, in the same transaction.
func (r *ClosureRepository) Save(ctx context.Context, c *closure.Closure, from closure.State, now time.Time) error {
	row, err := Encode(r.toRow(c, now))
	if err != nil {
		return err
	}
	names := map[string]string{"#s": "closure_state"}
	values := map[string]types.AttributeValue{":from": &types.AttributeValueMemberS{Value: string(from)}}
	items := txItems(r.put(row, "#s = :from", names, values))
	if c.State.Open() {
		marker, err := r.markerItem(c, now)
		if err != nil {
			return err
		}
		items = append(items, r.put(marker, "closure_id = :id", nil, map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: c.ID}}))
	} else {
		items = append(items, r.base.BuildDeleteTxItem(closureOpenPK(c.AccountOrganizationID), closureOpenSK))
	}
	if err := r.base.TransactWrite(ctx, items); err != nil {
		if cancellationCodes(err) != nil {
			return closure.ErrWrongState
		}
		return err
	}
	return nil
}

// SetReadOnly raises or lifts the read-only flag of the given billing tenant
// rows. Idempotent.
func (r *ClosureRepository) SetReadOnly(ctx context.Context, tenantIDs []string, closureID string, on bool, now time.Time) error {
	for _, id := range tenantIDs {
		if !on {
			if _, err := r.base.DeleteItem(ctx, closureFlagPK(id), closureFlagSK); err != nil {
				return err
			}
			continue
		}
		item, err := Encode(struct {
			keys
			ClosureID string `dynamodbav:"closure_id"`
		}{newKeys(closureFlagPK(id), closureFlagSK, RetentionPermanent, now), closureID})
		if err != nil {
			return err
		}
		if err := r.base.PutItem(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

// ReadOnly reports whether the billing tenant row is read-only because of a
// closure: one GetItem, called on the write path.
func (r *ClosureRepository) ReadOnly(ctx context.Context, tenantOrgID string) (bool, error) {
	item, err := r.base.GetItem(ctx, closureFlagPK(tenantOrgID), closureFlagSK)
	if err != nil {
		return false, err
	}
	return item != nil, nil
}

// DueClosure is a closure with a next action and when it is due.
type DueClosure struct {
	Closure *closure.Closure
	Due     time.Time
}

// Due lists the closures whose next action is due at now. The queue is a few
// rows, so the whole partition is read.
func (r *ClosureRepository) Due(ctx context.Context, now time.Time) ([]DueClosure, error) {
	var out []DueClosure
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
			PK: JobTenantClosures, ScanIndexForward: true, Limit: 100, ExclusiveStartKey: start})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[closureRow](res.Items)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			c := rows[i].closure()
			if due, ok := closureDue(c, now); ok && !due.After(now) {
				out = append(out, DueClosure{Closure: c, Due: due})
			}
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// Tombstone records that a billing tenant was closed and erased: no personal
// data, only the id and the closure, so late webhooks and retries can be
// recognised and dropped. It never expires and never blocks a new tenant for the
// same workspace (a new tenant has a new id).
func (r *ClosureRepository) Tombstone(ctx context.Context, tenantID, closureID string, now time.Time) error {
	item, err := Encode(struct {
		keys
		ClosureID string `dynamodbav:"closure_id"`
		ClosedAt  string `dynamodbav:"closed_at"`
	}{newKeys("TENANT#"+tenantID, "TOMBSTONE", RetentionPermanent, now), closureID, ts(now)})
	if err != nil {
		return err
	}
	return r.base.PutItem(ctx, item)
}

// Closed reports whether a billing tenant id was closed and erased.
func (r *ClosureRepository) Closed(ctx context.Context, tenantID string) (bool, error) {
	item, err := r.base.GetItem(ctx, "TENANT#"+tenantID, "TOMBSTONE")
	return item != nil, err
}
