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
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item), audit))
	if IsConditionFailed(err) {
		return ErrConnectionExists
	}
	return conflictErr(err)
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
