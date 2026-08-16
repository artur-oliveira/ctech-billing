package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// ErrNotFound reports a row that does not exist. Repositories return it rather
// than a nil value, so a caller cannot forget to check.
var ErrNotFound = errors.New("not found")

// OrganizationRepository stores the minimal tenant record (ADR 0007).
//
// There is no Delete and no role management here, and that is the design: the
// entity exists to be replaced by a reference to ctech-account, not to grow into
// a second RBAC model.
type OrganizationRepository struct {
	base   Base
	audit  Base
	events Base
}

func NewOrganizationRepository(db *dynamodb.Client, cfg *config.Config) *OrganizationRepository {
	return &OrganizationRepository{
		base:   NewBase(db, cfg, TableOrganizations),
		audit:  NewBase(db, cfg, TableAudit),
		events: NewBase(db, cfg, TableWebhooks),
	}
}

// Create writes a new organization. Provisioning is manual by design — there is
// no self-service merchant onboarding in the MVP, and manual admission is the
// cheapest risk control that exists.
//
// A new organization always starts at PayoutNotConfigured: it cannot collect
// money until someone deliberately moves the gate.
func (r *OrganizationRepository) Create(ctx context.Context, org *billing.Organization, now time.Time) error {
	if org.PayoutStatus == "" {
		org.PayoutStatus = billing.PayoutNotConfigured
	}
	row := organizationRow{
		keys:         newKeys(TenantPK(org.ID, org.Livemode), OrganizationSK(), RetentionOrganization, now),
		Organization: *org,
	}
	if org.OwnerUserID != "" {
		row.LookupPK = LookupOrganizationOwnerPK(org.Livemode, org.OwnerUserID)
	}
	item, err := Encode(row)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("organization %s already exists in %s mode", org.ID, Mode(org.Livemode))
	}
	return err
}

// Get reads an organization.
func (r *OrganizationRepository) Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), OrganizationSK())
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: organization %s", ErrNotFound, organizationID)
	}
	row, err := Decode[organizationRow](item)
	if err != nil {
		return nil, err
	}
	return &row.Organization, nil
}

// GetByOwner resolves a console user to the organization they own, in one mode.
//
// This is the console's equivalent of CredentialRepository.Resolve, and it has
// the same property: the tenant is the **answer**, so it cannot also be part of
// the question. Nothing the browser sends names an organization — a session is
// scoped by who the token says you are, not by what the request asks for
// (ADR 0011).
//
// One owner, one organization per mode (ADR 0007). When that stops being true,
// this returns a list and the console grows an organization switcher; until
// then, a switcher would be UI for a state that cannot exist.
func (r *OrganizationRepository) GetByOwner(ctx context.Context, userID string, livemode bool) (*billing.Organization, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupOrganizationOwnerPK(livemode, userID),
		Limit:     1,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, fmt.Errorf("%w: organization owned by %s", ErrNotFound, userID)
	}
	row, err := Decode[organizationRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.Organization, nil
}

// SetPayoutStatus moves the per-merchant charge gate, writing the audit entry in
// the same transaction.
//
// This is the single most consequential field a human can change in this
// service: it is what stands between a merchant existing and a merchant being
// able to collect money. It goes through the audited, conditional path for
// exactly that reason — never a bare update.
func (r *OrganizationRepository) SetPayoutStatus(
	ctx context.Context,
	org *billing.Organization,
	to billing.PayoutStatus,
	actor, requestID string,
	now time.Time,
) error {
	if org.PayoutStatus == to {
		return nil
	}
	change := StatusChange{
		OrganizationID: org.ID,
		Livemode:       org.Livemode,
		PK:             TenantPK(org.ID, org.Livemode),
		SK:             OrganizationSK(),
		From:           string(org.PayoutStatus),
		To:             string(to),
		// The gate lives in payout_status, not status — the one row where the
		// state attribute is not called that.
		Field: "payout_status",
		Audit: AuditEntry{
			Entity:    "ORGANIZATION",
			EntityID:  org.ID,
			Action:    billing.AuditPayoutStatusChanged,
			Cause:     billing.CauseManual,
			Actor:     actor,
			RequestID: requestID,
		},
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return err
	}
	org.PayoutStatus = to
	return nil
}

// tables is the set every transition in this repository writes across.
func (r *OrganizationRepository) tables() Tables {
	return Tables{Rows: r.base, Audit: r.audit, Events: r.events}
}
