package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// CredentialRepository resolves an OAuth client id to the tenant it acts for.
type CredentialRepository struct {
	base  Base
	audit Base
}

func NewCredentialRepository(db *dynamodb.Client, cfg *config.Config) *CredentialRepository {
	return &CredentialRepository{base: NewBase(db, cfg, TableCredentials), audit: NewBase(db, cfg, TableAudit)}
}

type credentialRow struct {
	keys
	billing.APICredential
	// LookupPK resolves the credential before the tenant is known — which is the
	// whole point, since the tenant is what it returns. The row still lives in
	// the tenant's partition, so a merchant's credentials are listable with the
	// rest of their data.
	LookupPK string `dynamodbav:"lookup_pk"`
}

// skCredentialGuard is the sort key of the row that claims a client id for one
// tenant. Resolve is a query on a global index, and an index cannot hold a
// condition: two tenants admitting the same client at once would both find
// nothing and both write. The guard is a plain item keyed by the client id, so
// "absent" is a condition DynamoDB checks inside the transaction. It carries
// no lookup_pk, so it never enters the lookup index.
const skCredentialGuard = "GUARD"

// ErrCredentialTaken reports a client id already admitted to a tenant.
var ErrCredentialTaken = errors.New("client already admitted to a tenant")

// ErrCredentialProvisioned reports a credential the tenant plan created, which
// no console route removes.
var ErrCredentialProvisioned = errors.New("credential provisioned by the tenant plan")

type credentialGuardRow struct {
	keys
	ClientID string `dynamodbav:"client_id"`
}

func (r *CredentialRepository) admitItems(cred *billing.APICredential, now time.Time) ([]types.TransactWriteItem, error) {
	if cred.ClientID == "" || cred.OrganizationID == "" {
		return nil, fmt.Errorf("credential needs a client id and an organization")
	}
	item, err := Encode(credentialRow{
		keys:          newKeys(TenantPK(cred.OrganizationID, cred.Livemode), CredentialSK(cred.ClientID), RetentionCredential, now),
		APICredential: *cred,
		LookupPK:      LookupCredentialPK(cred.ClientID),
	})
	if err != nil {
		return nil, err
	}
	guard, err := Encode(credentialGuardRow{
		keys:     newKeys(LookupCredentialPK(cred.ClientID), skCredentialGuard, RetentionCredential, now),
		ClientID: cred.ClientID,
	})
	if err != nil {
		return nil, err
	}
	return txItems(r.base.BuildPutTxItemIfAbsent(item), r.base.BuildPutTxItemIfAbsent(guard)), nil
}

// Create registers a credential for a tenant from the tenant plan (ADR 0007).
func (r *CredentialRepository) Create(ctx context.Context, cred *billing.APICredential, now time.Time) error {
	items, err := r.admitItems(cred, now)
	if err != nil {
		return err
	}
	return r.admit(ctx, cred.ClientID, items)
}

// admit writes the admission. Two admissions of one client racing on the
// guard can cancel each other with a TransactionConflict rather than a
// condition failure; retrying turns the loser's answer into the condition
// failure it really is ("taken").
func (r *CredentialRepository) admit(ctx context.Context, clientID string, items []types.TransactWriteItem) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = r.base.TransactWrite(ctx, items)
		if !IsTransactionConflict(err) {
			break
		}
	}
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", ErrCredentialTaken, clientID)
	}
	return conflictErr(err)
}

// Connect admits a merchant's own client from the console: the credential,
// the guard and the audit row, in one transaction. The caller has already
// checked the client with ctech-account (services.CredentialConnector).
func (r *CredentialRepository) Connect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error {
	if cred.ConnectedBy == "" || actor == "" {
		return fmt.Errorf("repositories: connecting credential %s needs who connected it", cred.ClientID)
	}
	items, err := r.admitItems(cred, now)
	if err != nil {
		return err
	}
	audit, err := buildAuditItem(cred.OrganizationID, cred.Livemode, AuditEntry{
		Entity: EntityCredential, EntityID: cred.ClientID, Action: billing.AuditCredentialConnected,
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID,
	}, "", "connected", now)
	if err != nil {
		return err
	}
	return r.admit(ctx, cred.ClientID, append(items, r.audit.BuildPutTxItemIfAbsent(audit)))
}

// Resolve returns the tenant a client id acts for.
//
// The lookup is global by necessity: the tenant is the answer, so it cannot be
// part of the question. Every read *after* this one is tenant-scoped, and the
// tenant comes from here rather than from anything the caller sent — a request
// cannot name an organization it does not hold a credential for.
func (r *CredentialRepository) Resolve(ctx context.Context, clientID string) (*billing.APICredential, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupCredentialPK(clientID),
		Limit:     1,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, fmt.Errorf("%w: credential %s", ErrNotFound, clientID)
	}
	row, err := Decode[credentialRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	if err := row.APICredential.Validate(); err != nil {
		return nil, err
	}
	return &row.APICredential, nil
}

// Deactivate flips billing's own kill switch for a credential.
func (r *CredentialRepository) Deactivate(ctx context.Context, cred *billing.APICredential, now time.Time) error {
	updates := map[string]any{
		"active":     false,
		"updated_at": now.UTC().Format(time.RFC3339Nano),
	}
	_, err := r.base.UpdateItem(ctx,
		TenantPK(cred.OrganizationID, cred.Livemode),
		new(CredentialSK(cred.ClientID)),
		updates,
	)
	if err == nil {
		cred.Active = false
	}
	return err
}

// ErrCredentialOwnerSet reports a credential that already has an owner.
var ErrCredentialOwnerSet = errors.New("credential already has an owner")

// SetOwnerKey scopes an unscoped credential to one owner, once. Changing an
// owner is not a seed operation: it changes what an integration can read.
func (r *CredentialRepository) SetOwnerKey(ctx context.Context, cred *billing.APICredential, ownerKey string, now time.Time) error {
	update := r.base.BuildRawUpdateTxItem(
		TenantPK(cred.OrganizationID, cred.Livemode), new(CredentialSK(cred.ClientID)),
		"SET owner_key = :o, updated_at = :u",
		"attribute_exists(pk) AND attribute_not_exists(owner_key)",
		nil,
		map[string]types.AttributeValue{
			":o": &types.AttributeValueMemberS{Value: ownerKey},
			":u": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		})
	err := r.base.TransactWrite(ctx, txItems(update))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", ErrCredentialOwnerSet, cred.ClientID)
	}
	if err != nil {
		return conflictErr(err)
	}
	cred.OwnerKey = ownerKey
	return nil
}

// Get reads one credential of a tenant.
func (r *CredentialRepository) Get(ctx context.Context, organizationID string, livemode bool, clientID string) (*billing.APICredential, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), CredentialSK(clientID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: credential %s", ErrNotFound, clientID)
	}
	row, err := Decode[credentialRow](item)
	if err != nil {
		return nil, err
	}
	return &row.APICredential, nil
}

// ListForTenant returns every credential of one tenant and mode, provisioned
// and inactive ones included, in client id order. Every page is read: a
// merchant's credential list is short, and a silently truncated one would hide
// the credential somebody is looking for.
func (r *CredentialRepository) ListForTenant(ctx context.Context, organizationID string, livemode bool) ([]billing.APICredential, error) {
	out := []billing.APICredential{}
	var start map[string]types.AttributeValue
	for {
		res, err := r.base.Query(ctx, QueryOpts{
			PK: TenantPK(organizationID, livemode), SKPrefix: skCredential,
			ScanIndexForward: true, Limit: 100, ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[credentialRow](res.Items)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, row.APICredential)
		}
		if len(res.LastEvaluatedKey) == 0 {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// Disconnect removes a console-connected credential and its guard, with the
// audit row. A provisioned credential is refused here as well as in the
// handler: the condition on connected_by is what holds if a handler forgets.
func (r *CredentialRepository) Disconnect(ctx context.Context, cred *billing.APICredential, actor, requestID string, now time.Time) error {
	if cred.Provisioned() {
		return fmt.Errorf("%w: %s", ErrCredentialProvisioned, cred.ClientID)
	}
	sk := CredentialSK(cred.ClientID)
	del := types.TransactWriteItem{Delete: &types.Delete{
		TableName: aws.String(r.base.TableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: TenantPK(cred.OrganizationID, cred.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(connected_by)"),
	}}
	audit, err := buildAuditItem(cred.OrganizationID, cred.Livemode, AuditEntry{
		Entity: EntityCredential, EntityID: cred.ClientID, Action: billing.AuditCredentialDisconnected,
		Cause: billing.CauseManual, Actor: actor, RequestID: requestID,
	}, "connected", "disconnected", now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(
		del,
		r.base.BuildDeleteTxItem(LookupCredentialPK(cred.ClientID), skCredentialGuard),
		r.audit.BuildPutTxItemIfAbsent(audit),
	))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: credential %s", ErrNotFound, cred.ClientID)
	}
	return err
}
