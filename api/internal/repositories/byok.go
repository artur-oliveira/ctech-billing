package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
)

var (
	ErrConnectionExists  = errors.New("byok: the organization already has a connection to this provider in this mode")
	ErrConnectionChanged = errors.New("byok: the connection changed since it was read")
)

// EntityByokConnection names connection audit rows.
const EntityByokConnection Entity = "BYOK_CONNECTION"

type connectionRow struct {
	keys
	byokconn.Connection
	SchedulePK string `dynamodbav:"schedule_pk,omitempty"`
	ScheduleSK string `dynamodbav:"schedule_sk,omitempty"`
}

type ByokRepository struct {
	base  Base
	audit Base
}

func NewByokRepository(db *dynamodb.Client, cfg *config.Config) *ByokRepository {
	return &ByokRepository{base: NewBase(db, cfg, TableByok), audit: NewBase(db, cfg, TableAudit)}
}

func (r *ByokRepository) row(c *byokconn.Connection, now time.Time) connectionRow {
	row := connectionRow{keys: newKeys(ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokConnectionSK(c.Provider), RetentionByokConnection, now), Connection: *c}
	if c.State != byokconn.StateDisconnected {
		row.SchedulePK, row.ScheduleSK = byokJobPK(c.Livemode), c.AccountOrganizationID+"#"+c.Provider
	}
	return row
}

func (r *ByokRepository) GetConnection(ctx context.Context, accountOrgID string, livemode bool, provider string) (*byokconn.Connection, error) {
	item, err := r.base.GetItem(ctx, ByokConnectionPK(accountOrgID, livemode), ByokConnectionSK(provider))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: connection %s", ErrNotFound, provider)
	}
	row, err := Decode[connectionRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Connection, nil
}

func (r *ByokRepository) ListConnections(ctx context.Context, accountOrgID string, livemode bool) ([]byokconn.Connection, error) {
	res, err := r.base.Query(ctx, QueryOpts{PK: ByokConnectionPK(accountOrgID, livemode), SKPrefix: "CONNECTION#", Limit: 20})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[connectionRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]byokconn.Connection, len(rows))
	for i := range rows {
		out[i] = rows[i].Connection
	}
	return out, nil
}

func (r *ByokRepository) auditItem(c *byokconn.Connection, action, actor, requestID string, now time.Time) (types.TransactWriteItem, error) {
	// The audit partition is the organization's space key: the connection is the
	// organization's, not a billing tenant's (an organization may connect with
	// no tenant at all).
	item, err := buildAuditItem(c.AccountOrganizationID, c.Livemode, AuditEntry{
		Entity: EntityByokConnection, EntityID: c.ID, Action: action, Actor: actor, RequestID: requestID,
	}, "", string(c.State), now)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.audit.BuildPutTxItem(item), nil
}

func (r *ByokRepository) CreateConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time) error {
	c.Revision, c.CreatedAt = 1, now.UTC()
	item, err := Encode(r.row(c, now))
	if err != nil {
		return err
	}
	audit, err := r.auditItem(c, "byok.connected", actor, requestID, now)
	if err != nil {
		return err
	}
	// Conditional on no connection: an onboarding-only row (no state yet,
	// PutOnboarding) is replaced, a connected one is not.
	put := types.TransactWriteItem{Put: &types.Put{
		TableName:                aws.String(r.base.TableName),
		Item:                     item,
		ConditionExpression:      aws.String("attribute_not_exists(#state)"),
		ExpressionAttributeNames: map[string]string{"#state": "state"},
	}}
	err = r.base.TransactWrite(ctx, txItems(put, audit))
	if IsConditionFailed(err) {
		return ErrConnectionExists
	}
	return conflictErr(err)
}

// PutOnboarding records where the person stopped in the guided onboarding
// (spec § 11.1, display only). Before connecting, the row has no state: the
// job index never sees it (no schedule keys) and CreateConnection replaces it.
func (r *ByokRepository) PutOnboarding(ctx context.Context, accountOrgID string, livemode bool, provider, step string, now time.Time) error {
	sk := ByokConnectionSK(provider)
	_, err := r.base.UpdateItemRaw(ctx, &dynamodb.UpdateItemInput{
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: ByokConnectionPK(accountOrgID, livemode)},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression: aws.String("SET onboarding_step = :s, onboarding_since = :now, provider = :p, " +
			"account_organization_id = :o, livemode = :l, created_at = if_not_exists(created_at, :now)"),
		// The time is kept while the step is unchanged ("waiting since").
		ConditionExpression: aws.String("attribute_not_exists(onboarding_step) OR onboarding_step <> :s"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberS{Value: step}, ":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
			":p": &types.AttributeValueMemberS{Value: provider}, ":o": &types.AttributeValueMemberS{Value: accountOrgID},
			":l": &types.AttributeValueMemberBOOL{Value: livemode},
		},
	})
	if IsConditionFailed(err) {
		return nil // the same step again: nothing to change
	}
	return err
}

// SaveConnection replaces the row if its revision is still the one read.
func (r *ByokRepository) SaveConnection(ctx context.Context, c *byokconn.Connection, actor, requestID, action string, now time.Time) error {
	read := c.Revision
	next := *c
	next.Revision = read + 1
	row := r.row(&next, now)
	item, err := Encode(row)
	if err != nil {
		return err
	}
	put := types.TransactWriteItem{Put: &types.Put{
		TableName:                 aws.String(r.base.TableName),
		Item:                      item,
		ConditionExpression:       aws.String("revision = :r"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":r": &types.AttributeValueMemberN{Value: strconv.Itoa(read)}},
	}}
	items := txItems(put)
	if action != "" {
		audit, err := r.auditItem(c, action, actor, requestID, now)
		if err != nil {
			return err
		}
		items = append(items, audit)
	}
	if err := r.base.TransactWrite(ctx, items); err != nil {
		if IsConditionFailed(err) {
			return ErrConnectionChanged
		}
		return conflictErr(err)
	}
	*c = next
	return nil
}

func (r *ByokRepository) DeleteConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time) error {
	audit, err := r.auditItem(c, "byok.disconnected", actor, requestID, now)
	if err != nil {
		return err
	}
	del := r.base.BuildDeleteTxItem(ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokConnectionSK(c.Provider))
	return conflictErr(r.base.TransactWrite(ctx, txItems(del, audit)))
}

// ConnectionsForJob is cross-organization by design (ADR 0002): cmd/byok only.
func (r *ByokRepository) ConnectionsForJob(ctx context.Context, livemode bool, startKey map[string]types.AttributeValue, limit int) (*Page[byokconn.Connection], error) {
	res, err := r.base.Query(ctx, QueryOpts{IndexName: IndexSchedule, PKField: "schedule_pk", SKField: "schedule_sk",
		PK: byokJobPK(livemode), Limit: limit, ExclusiveStartKey: startKey, ScanIndexForward: true})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[connectionRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]byokconn.Connection, len(rows))
	for i := range rows {
		out[i] = rows[i].Connection
	}
	return &Page[byokconn.Connection]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// ChargeRefPK is the pointer from a provider's charge reference to its tenant
// (spec § 12.2): CHARGEREF#{provider}#{mode}#{ref}. Part B writes it with the
// payment attempt; the statement sync reads it to filter the shared sandbox.
func ChargeRefPK(provider string, livemode bool, ref string) string {
	return "CHARGEREF#" + provider + "#" + Mode(livemode) + "#" + ref
}

// ChargeRefOwner answers which organization a charge reference belongs to. An
// empty reference is not found, with no read.
func (r *ByokRepository) ChargeRefOwner(ctx context.Context, provider string, livemode bool, ref string) (string, bool, error) {
	if ref == "" {
		return "", false, nil
	}
	item, err := r.base.GetItem(ctx, ChargeRefPK(provider, livemode, ref), "CHARGEREF")
	if err != nil || item == nil {
		return "", false, err
	}
	row, err := Decode[struct {
		AccountOrganizationID string `dynamodbav:"account_organization_id"`
	}](item)
	if err != nil {
		return "", false, err
	}
	return row.AccountOrganizationID, row.AccountOrganizationID != "", nil
}
