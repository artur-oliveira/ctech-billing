package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

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

// SetDunningPolicy replaces the organization's default dunning schedule, with
// the audit row that says who changed it.
//
// It does not touch a single invoice, and that absence is the design: the
// schedule is copied onto an invoice when it is finalized, so this changes what
// happens to invoices issued afterwards and nothing about the ones already
// being chased. An operator who shortened the policy has not just moved
// everybody's write-off date forward by three weeks.
//
// An empty schedule restores the built-in default rather than disabling dunning:
// there is no "never chase this" state, because an invoice that is never chased
// and never written off sits OPEN forever looking like revenue.
func (r *OrganizationRepository) SetDunningPolicy(
	ctx context.Context,
	org *billing.Organization,
	policy billing.DunningSchedule,
	actor, requestID string,
	now time.Time,
) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if actor == "" {
		return fmt.Errorf("repositories: changing the dunning policy of %s needs an actor", org.ID)
	}

	names := map[string]string{"#dp": "dunning_policy", "#ua": "updated_at"}
	values := map[string]types.AttributeValue{
		":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	expr := "SET #ua = :now REMOVE #dp"
	if len(policy) > 0 {
		encoded, err := attributevalue.Marshal(policy)
		if err != nil {
			return err
		}
		values[":dp"] = encoded
		expr = "SET #dp = :dp, #ua = :now"
	}

	auditItem, err := buildAuditItem(org.ID, org.Livemode, AuditEntry{
		Entity:    EntityOrganization,
		EntityID:  org.ID,
		Action:    "organization.dunning_policy_changed",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		Before:    describeSchedule(org.DunningPolicy),
		After:     describeSchedule(policy),
	}, "", "", now)
	if err != nil {
		return err
	}

	update := r.base.BuildRawUpdateTxItem(
		TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		expr, "attribute_exists(pk)", names, values,
	)
	if err := r.base.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem))); err != nil {
		return err
	}
	org.DunningPolicy = policy.Clone()
	return nil
}

// SetIssuer records who the invoice PDF says is charging.
//
// It writes the four fields as one block rather than patching them
// individually: they are read together, printed together, and a partial update
// is how a document ends up headed by a company name with somebody else's CNPJ
// under it. An empty string clears the field.
//
// The audit row names the legal name only. The others are on every document the
// organization issues anyway, and a trail that copied an address into itself on
// every edit would be storing the same personal data twice.
func (r *OrganizationRepository) SetIssuer(
	ctx context.Context,
	org *billing.Organization,
	legalName, taxID, address, email string,
	actor, requestID string,
	now time.Time,
) error {
	if actor == "" {
		return fmt.Errorf("repositories: changing the issuer of %s needs an actor", org.ID)
	}

	fields := map[string]string{
		"legal_name":     strings.TrimSpace(legalName),
		"issuer_tax_id":  strings.TrimSpace(taxID),
		"issuer_address": strings.TrimSpace(address),
		"issuer_email":   strings.TrimSpace(email),
	}
	names := map[string]string{"#ua": "updated_at"}
	values := map[string]types.AttributeValue{
		":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	sets := []string{"#ua = :now"}
	var removes []string

	i := 0
	for _, attr := range sortedStrings(fields) {
		n, v := fmt.Sprintf("#f%d", i), fmt.Sprintf(":f%d", i)
		names[n] = attr
		if fields[attr] == "" {
			removes = append(removes, n)
		} else {
			values[v] = &types.AttributeValueMemberS{Value: fields[attr]}
			sets = append(sets, n+" = "+v)
		}
		i++
	}
	expr := "SET " + strings.Join(sets, ", ")
	if len(removes) > 0 {
		expr += " REMOVE " + strings.Join(removes, ", ")
	}

	auditItem, err := buildAuditItem(org.ID, org.Livemode, AuditEntry{
		Entity:    EntityOrganization,
		EntityID:  org.ID,
		Action:    "organization.issuer_changed",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		Before:    org.LegalName,
		After:     fields["legal_name"],
	}, "", "", now)
	if err != nil {
		return err
	}

	update := r.base.BuildRawUpdateTxItem(
		TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		expr, "attribute_exists(pk)", names, values,
	)
	if err := r.base.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem))); err != nil {
		return err
	}
	org.LegalName = fields["legal_name"]
	org.IssuerTaxID = fields["issuer_tax_id"]
	org.IssuerAddress = fields["issuer_address"]
	org.IssuerEmail = fields["issuer_email"]
	return nil
}

// sortedStrings keeps the generated expression deterministic, and so diffable
// in a log — the same reason sortedKeys exists for a status change.
func sortedStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// describeSchedule renders a policy for the audit trail.
//
// The steps themselves, not a count: "who changed the policy" is only half the
// question, and the other half — what it was before — is unanswerable from a
// row that says "6 steps" became "5 steps".
func describeSchedule(p billing.DunningSchedule) string {
	if len(p) == 0 {
		return "padrão"
	}
	parts := make([]string, 0, len(p))
	for _, step := range p {
		parts = append(parts, fmt.Sprintf("%+d:%s", step.Offset, step.Action))
	}
	return strings.Join(parts, " ")
}

// tables is the set every transition in this repository writes across.
func (r *OrganizationRepository) tables() Tables {
	return Tables{Rows: r.base, Audit: r.audit, Events: r.events}
}
