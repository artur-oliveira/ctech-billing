package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// CredentialRepository resolves an OAuth client id to the tenant it acts for.
type CredentialRepository struct {
	base Base
}

func NewCredentialRepository(db *dynamodb.Client, cfg *config.Config) *CredentialRepository {
	return &CredentialRepository{base: NewBase(db, cfg, TableCredentials)}
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

// Create registers a credential for a tenant. Provisioning is manual, like the
// organization itself (ADR 0007).
func (r *CredentialRepository) Create(ctx context.Context, cred *billing.APICredential, now time.Time) error {
	if cred.ClientID == "" || cred.OrganizationID == "" {
		return fmt.Errorf("credential needs a client id and an organization")
	}
	item, err := Encode(credentialRow{
		keys: newKeys(
			TenantPK(cred.OrganizationID, cred.Livemode),
			CredentialSK(cred.ClientID),
			RetentionCredential,
			now,
		),
		APICredential: *cred,
		LookupPK:      LookupCredentialPK(cred.ClientID),
	})
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("credential %s already exists", cred.ClientID)
	}
	return err
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
