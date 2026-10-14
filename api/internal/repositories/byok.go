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

// sealedRow holds a connection's sealed bundle apart from the connection row:
// the hot row (health, cursors) is rewritten every hour and is small; the
// bundle is written only on connect and on renewal (final review I6: about
// 4 WRU per connection-hour instead of 40).
type sealedRow struct {
	keys
	Sealed  string `dynamodbav:"sealed"`
	Version int    `dynamodbav:"version"`
}

// ByokSealedSK keys a connection's sealed bundle beside its row.
func ByokSealedSK(provider string) string { return "SEALED#" + provider }

func (r *ByokRepository) sealedPut(c *byokconn.Connection, now time.Time) (types.TransactWriteItem, error) {
	item, err := Encode(sealedRow{keys: newKeys(ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokSealedSK(c.Provider), RetentionByokConnection, now),
		Sealed: c.Sealed, Version: c.Version})
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return r.base.BuildPutTxItem(item), nil
}

// LoadSealed reads the sealed bundle into a connection read from the job index
// (which carries the hot row only). The shared sandbox has none.
func (r *ByokRepository) LoadSealed(ctx context.Context, c *byokconn.Connection) error {
	if c.Kind != byokconn.KindMerchant || c.Sealed != "" {
		return nil
	}
	item, err := r.base.GetItem(ctx, ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokSealedSK(c.Provider))
	if err != nil || item == nil {
		return err
	}
	row, err := Decode[sealedRow](item)
	if err != nil {
		return err
	}
	c.Sealed = row.Sealed
	return nil
}

func (r *ByokRepository) row(c *byokconn.Connection, now time.Time) connectionRow {
	hot := *c
	hot.Sealed = "" // its own row: sealedRow
	row := connectionRow{keys: newKeys(ByokConnectionPK(c.AccountOrganizationID, c.Livemode), ByokConnectionSK(c.Provider), RetentionByokConnection, now), Connection: hot}
	if c.State != byokconn.StateDisconnected {
		row.SchedulePK, row.ScheduleSK = byokJobPK(c.Livemode), c.AccountOrganizationID+"#"+c.Provider
	}
	return row
}

// GetConnection reads the connection and its sealed bundle: one key-condition
// query of the organization's small partition (a few rows per provider).
func (r *ByokRepository) GetConnection(ctx context.Context, accountOrgID string, livemode bool, provider string) (*byokconn.Connection, error) {
	res, err := r.base.Query(ctx, QueryOpts{PK: ByokConnectionPK(accountOrgID, livemode), Limit: 50})
	if err != nil {
		return nil, err
	}
	var conn *byokconn.Connection
	sealed := ""
	for _, item := range res.Items {
		sk, _ := item["sk"].(*types.AttributeValueMemberS)
		switch {
		case sk == nil:
		case sk.Value == ByokConnectionSK(provider):
			row, err := Decode[connectionRow](item)
			if err != nil {
				return nil, err
			}
			conn = &row.Connection
		case sk.Value == ByokSealedSK(provider):
			row, err := Decode[sealedRow](item)
			if err != nil {
				return nil, err
			}
			sealed = row.Sealed
		}
	}
	if conn == nil {
		return nil, fmt.Errorf("%w: connection %s", ErrNotFound, provider)
	}
	conn.Sealed = sealed
	return conn, nil
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

// ByokGuardSK is an organization's connection guard: every created connection
// moves it, so a create checked against a count read before the bank call
// loses to a connection created meanwhile (the bank-connection cap).
const ByokGuardSK = "CONNGUARD"

// ConnectionGuard is the guard's version; a missing row is zero.
func (r *ByokRepository) ConnectionGuard(ctx context.Context, accountOrgID string, livemode bool) (int64, error) {
	return r.counter(ctx, ByokConnectionPK(accountOrgID, livemode), ByokGuardSK)
}

// guardItem moves the guard, conditional on the version read when there is one.
func (r *ByokRepository) guardItem(c *byokconn.Connection, read []int64) types.TransactWriteItem {
	u := &types.Update{
		TableName: aws.String(r.base.TableName),
		Key: map[string]types.AttributeValue{"pk": str(ByokConnectionPK(c.AccountOrganizationID, c.Livemode)),
			"sk": str(ByokGuardSK)},
		UpdateExpression:          aws.String("ADD n :one"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": numberValue(1)},
	}
	switch {
	case len(read) == 0:
	case read[0] == 0:
		u.ConditionExpression = aws.String("attribute_not_exists(n)")
	default:
		u.ConditionExpression = aws.String("n = :read")
		u.ExpressionAttributeValues[":read"] = numberValue(read[0])
	}
	return types.TransactWriteItem{Update: u}
}

// CreateConnection writes a new connection. guard is the ConnectionGuard
// version the caller counted against; a connection created since makes this
// one ErrTransactionConflict (retried, the count is read again).
func (r *ByokRepository) CreateConnection(ctx context.Context, c *byokconn.Connection, actor, requestID string, now time.Time, guard ...int64) error {
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
	items := txItems(put, audit, r.guardItem(c, guard))
	if c.Sealed != "" {
		sp, err := r.sealedPut(c, now)
		if err != nil {
			return err
		}
		items = append(items, sp)
	}
	err = r.base.TransactWrite(ctx, items)
	if codes := cancellationCodes(err); len(codes) > 2 && codes[2] == codeConditionFailed {
		return fmt.Errorf("%w: connection guard moved", ErrTransactionConflict)
	}
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
	cond := aws.String("revision = :r")
	vals := map[string]types.AttributeValue{":r": &types.AttributeValueMemberN{Value: strconv.Itoa(read)}}
	if action == "" {
		// The hourly save: one plain conditional put of the small hot row, no
		// transaction (a transaction costs twice the write units).
		_, err := r.base.PutItemRaw(ctx, &dynamodb.PutItemInput{Item: item, ConditionExpression: cond, ExpressionAttributeValues: vals})
		if err != nil {
			if IsConditionFailed(err) {
				return ErrConnectionChanged
			}
			return err
		}
		*c = next
		return nil
	}
	put := types.TransactWriteItem{Put: &types.Put{
		TableName: aws.String(r.base.TableName), Item: item, ConditionExpression: cond, ExpressionAttributeValues: vals,
	}}
	items := txItems(put)
	// An audited change may carry a new bundle (a renewal): the sealed row is
	// written with it. A plain save never changes the bundle.
	if c.Sealed != "" {
		sp, err := r.sealedPut(&next, now)
		if err != nil {
			return err
		}
		items = append(items, sp)
	}
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
	pk := ByokConnectionPK(c.AccountOrganizationID, c.Livemode)
	del := r.base.BuildDeleteTxItem(pk, ByokConnectionSK(c.Provider))
	delSealed := r.base.BuildDeleteTxItem(pk, ByokSealedSK(c.Provider))
	return conflictErr(r.base.TransactWrite(ctx, txItems(del, delSealed, audit)))
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

// SandboxHookID is the shared sandbox's one webhook (spec § 9): the
// CHARGEREF#…#test# pointer, which only billing writes, decides the tenant.
const SandboxHookID = "sandbox"

// Hook is a webhook's row (spec § 12.2): HOOK#{id} → HOOK. Only the SHA-256
// of the path secret is kept; a rotated secret stays valid until
// PreviousValidUntil (the provider's retries carry the old URL).
type Hook struct {
	ID                    string    `dynamodbav:"id"`
	Provider              string    `dynamodbav:"provider"`
	AccountOrganizationID string    `dynamodbav:"account_organization_id,omitempty"`
	Livemode              bool      `dynamodbav:"livemode"`
	SecretHash            string    `dynamodbav:"secret_hash"`
	PreviousSecretHash    string    `dynamodbav:"previous_secret_hash,omitempty"`
	PreviousValidUntil    time.Time `dynamodbav:"previous_valid_until,omitempty"`
}

type hookRow struct {
	keys
	Hook
}

func hookPK(id string) string { return "HOOK#" + id }

func (r *ByokRepository) PutHook(ctx context.Context, h *Hook, now time.Time) error {
	item, err := Encode(hookRow{keys: newKeys(hookPK(h.ID), "HOOK", RetentionByokConnection, now), Hook: *h})
	if err != nil {
		return err
	}
	return r.base.PutItem(ctx, item)
}

func (r *ByokRepository) GetHook(ctx context.Context, id string) (*Hook, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: hook", ErrNotFound)
	}
	item, err := r.base.GetItem(ctx, hookPK(id), "HOOK")
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: hook", ErrNotFound)
	}
	row, err := Decode[hookRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Hook, nil
}

func (r *ByokRepository) DeleteHook(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := r.base.DeleteItem(ctx, hookPK(id), "HOOK")
	return err
}
