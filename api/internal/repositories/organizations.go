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
	// Only a linked tenant is reachable from the console (ADR 0028). The owner
	// is stored, never indexed: it no longer grants access.
	if org.AccountOrganizationID != "" {
		if err := r.ensureAccountOrganizationFree(ctx, org.ID, org.AccountOrganizationID, org.Livemode); err != nil {
			return err
		}
	}
	put, err := organizationPut(r.base, org, now)
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(put))
	if IsConditionFailed(err) {
		return fmt.Errorf("organization %s already exists in %s mode", org.ID, Mode(org.Livemode))
	}
	return err
}

// organizationPut is the conditional put of a new tenant row with its console
// lookup key (part 1: only a linked tenant gets one). It does not check the
// key is free: Create reads first, and the application transaction is guarded
// by its pointer row instead.
func organizationPut(base Base, org *billing.Organization, now time.Time) (types.TransactWriteItem, error) {
	if org.PayoutStatus == "" {
		org.PayoutStatus = billing.PayoutNotConfigured
	}
	row := organizationRow{
		keys:         newKeys(TenantPK(org.ID, org.Livemode), OrganizationSK(), RetentionOrganization, now),
		Organization: *org,
	}
	if org.AccountOrganizationID != "" {
		row.LookupPK = LookupOrganizationAccountPK(org.Livemode, org.AccountOrganizationID)
	}
	item, err := Encode(row)
	if err != nil {
		return types.TransactWriteItem{}, err
	}
	return base.BuildPutTxItemIfAbsent(item), nil
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

// ErrAccountOrganizationAmbiguous is two tenant rows carrying one account
// organization's key in one mode. Answering with whichever row the index
// returned first would hand one merchant another's data, so nothing is chosen.
var ErrAccountOrganizationAmbiguous = errors.New("more than one tenant is linked to the account organization")

// ErrAccountOrganizationTaken is a create or link naming an account
// organization another tenant already answers for in that mode.
var ErrAccountOrganizationTaken = errors.New("account organization is already linked to another tenant")

// GetByAccountOrganization resolves the selected ctech-account organization to
// its tenant in one mode (ADR 0028). Like
// CredentialRepository.Resolve, the tenant is the answer, never part of the
// question: the caller passes an organization the resolver has already
// confirmed the person belongs to.
func (r *OrganizationRepository) GetByAccountOrganization(ctx context.Context, accountOrgID string, livemode bool) (*billing.Organization, error) {
	if accountOrgID == "" {
		return nil, fmt.Errorf("%w: no account organization", ErrNotFound)
	}
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupOrganizationAccountPK(livemode, accountOrgID),
		// Two, not one: a second row is a fault to refuse, not a row to skip.
		Limit: 2,
	})
	if err != nil {
		return nil, err
	}
	switch len(res.Items) {
	case 0:
		return nil, fmt.Errorf("%w: tenant of account organization %s in %s mode", ErrNotFound, accountOrgID, Mode(livemode))
	case 1:
	default:
		return nil, fmt.Errorf("%w: %s in %s mode", ErrAccountOrganizationAmbiguous, accountOrgID, Mode(livemode))
	}
	row, err := Decode[organizationRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	// The row must agree with the key it was found by; a hand-edited key on
	// somebody else's row is not a tenant of this organization.
	if row.AccountOrganizationID != accountOrgID || row.Livemode != livemode {
		return nil, fmt.Errorf("%w: lookup row %s disagrees with its own link", ErrNotFound, row.ID)
	}
	return &row.Organization, nil
}

// ensureAccountOrganizationFree refuses a create or link onto a key another
// tenant holds. A read before the write: both callers are the operator's seed,
// and part 2's self-service creation adds a conditional guard of its own.
func (r *OrganizationRepository) ensureAccountOrganizationFree(ctx context.Context, tenantID, accountOrgID string, livemode bool) error {
	existing, err := r.GetByAccountOrganization(ctx, accountOrgID, livemode)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	case existing.ID == tenantID:
		return nil
	default:
		return fmt.Errorf("%w: %s answers for %s in %s mode", ErrAccountOrganizationTaken, existing.ID, accountOrgID, Mode(livemode))
	}
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

// ErrAccountOrganizationLinked is a tenant already linked to another
// ctech-account organization. Moving a link moves where revenue is recorded;
// that is an operator decision with its own review, not a plan re-applied.
var ErrAccountOrganizationLinked = errors.New("organization is already linked to another account organization")

// LinkAccountOrganization records which ctech-account organization this tenant
// is (spec § 3.8: the finance space its paid invoices are recorded in). Once:
// the update is conditional on there being no link yet, so two plans applied at
// once cannot both win, and it is audited. Linking to the same id again is a
// no-op. The caller validates the id's shape (space.IsOrganizationID).
func (r *OrganizationRepository) LinkAccountOrganization(
	ctx context.Context,
	org *billing.Organization,
	accountOrgID, actor, requestID string,
	now time.Time,
) error {
	if org.AccountOrganizationID == accountOrgID {
		return nil
	}
	if org.AccountOrganizationID != "" {
		return fmt.Errorf("%w: %s is linked to %s", ErrAccountOrganizationLinked, org.ID, org.AccountOrganizationID)
	}
	if actor == "" {
		return fmt.Errorf("repositories: linking %s needs an actor", org.ID)
	}
	if err := r.ensureAccountOrganizationFree(ctx, org.ID, accountOrgID, org.Livemode); err != nil {
		return err
	}
	auditItem, err := buildAuditItem(org.ID, org.Livemode, AuditEntry{
		Entity:    EntityOrganization,
		EntityID:  org.ID,
		Action:    billing.AuditAccountOrganizationLinked,
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		After:     accountOrgID,
	}, "", "", now)
	if err != nil {
		return err
	}
	update := r.base.BuildRawUpdateTxItem(
		TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		"SET account_organization_id = :a, lookup_pk = :l, updated_at = :now",
		"attribute_exists(pk) AND attribute_not_exists(account_organization_id)", nil,
		map[string]types.AttributeValue{
			":a":   &types.AttributeValueMemberS{Value: accountOrgID},
			":l":   &types.AttributeValueMemberS{Value: LookupOrganizationAccountPK(org.Livemode, accountOrgID)},
			":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
		},
	)
	err = r.base.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem)))
	if onlyConditionFailed(err) {
		return fmt.Errorf("%w: %s was linked while this plan was being applied", ErrAccountOrganizationLinked, org.ID)
	}
	if err != nil {
		return err
	}
	org.AccountOrganizationID = accountOrgID
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

// ErrOrganizationOwned is a tenant plan naming a different owner than the one
// the tenant already has. Changing who reaches a tenant's console is a
// deliberate act, not something a seed does.
var ErrOrganizationOwned = errors.New("organization already has another owner")

// SetOwner gives a tenant with no owner (one provisioned API-only) the person
// who asked for it, once, with its audit entry. The owner is a record of who
// asked for the tenant; it is not indexed and grants no access (ADR 0028).
func (r *OrganizationRepository) SetOwner(
	ctx context.Context,
	org *billing.Organization,
	ownerUserID, actor, requestID string,
	now time.Time,
) error {
	if org.OwnerUserID == ownerUserID {
		return nil
	}
	if org.OwnerUserID != "" {
		return fmt.Errorf("%w: %s is owned by %s", ErrOrganizationOwned, org.ID, org.OwnerUserID)
	}
	if actor == "" {
		return fmt.Errorf("repositories: setting the owner of %s needs an actor", org.ID)
	}
	auditItem, err := buildAuditItem(org.ID, org.Livemode, AuditEntry{
		Entity:    EntityOrganization,
		EntityID:  org.ID,
		Action:    billing.AuditOrganizationOwnerSet,
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		After:     ownerUserID,
	}, "", "", now)
	if err != nil {
		return err
	}
	// lookup_pk is never touched: a linked tenant is found by its account
	// organization (ADR 0028), and the owner grants nothing.
	expr := "SET owner_user_id = :o, updated_at = :now"
	cond := "attribute_exists(pk) AND (attribute_not_exists(owner_user_id) OR owner_user_id = :empty)"
	values := map[string]types.AttributeValue{
		":o":     &types.AttributeValueMemberS{Value: ownerUserID},
		":empty": &types.AttributeValueMemberS{Value: ""},
		":now":   &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	// An API-only tenant stores the owner as an empty string, not as absent.
	update := r.base.BuildRawUpdateTxItem(
		TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		expr, cond, nil, values,
	)
	err = r.base.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem)))
	if onlyConditionFailed(err) {
		return fmt.Errorf("%w: %s was given an owner while this plan was being applied", ErrOrganizationOwned, org.ID)
	}
	if err != nil {
		return err
	}
	org.OwnerUserID = ownerUserID
	return nil
}

// suspensionWrites are the live tenant's suspended_at change and its audit row,
// for the transaction of the subscription change that caused them. The update
// is conditional only on the row existing: a concurrent writer of suspended_at
// must not cancel a subscription's cancellation.
func (r *OrganizationRepository) suspensionWrites(org *billing.Organization, suspend bool, cause billing.Cause,
	actor, requestID string, now time.Time) ([]types.TransactWriteItem, error) {
	stamp := now.UTC().Format(time.RFC3339Nano)
	expr, action, before, after := "SET suspended_at = if_not_exists(suspended_at, :now), updated_at = :now",
		billing.AuditOrganizationSuspended, "", stamp
	if !suspend {
		expr, action, before, after = "SET updated_at = :now REMOVE suspended_at",
			billing.AuditOrganizationReinstated, org.SuspendedAt, ""
	}
	auditItem, err := buildAuditItem(org.ID, true, AuditEntry{
		Entity: EntityOrganization, EntityID: org.ID, Action: action, Cause: cause,
		Actor: actor, RequestID: requestID, Before: before, After: after,
	}, "", "", now)
	if err != nil {
		return nil, err
	}
	update := r.base.BuildRawUpdateTxItem(TenantPK(org.ID, true), new(OrganizationSK()), expr, "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":now": &types.AttributeValueMemberS{Value: stamp}})
	return txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem)), nil
}

// SetPayoutRail moves the tenant's collection rail, provider and payout gate
// together (BYOK spec § 7.1), conditional on the gate still being the one read,
// with one audit row (payout.rail_changed) naming before and after. An empty
// rail clears rail and provider (the wallet default).
func (r *OrganizationRepository) SetPayoutRail(
	ctx context.Context,
	org *billing.Organization,
	rail billing.PayoutRail,
	provider string,
	status billing.PayoutStatus,
	actor, requestID string,
	now time.Time,
) error {
	if actor == "" {
		return fmt.Errorf("repositories: changing the payout rail of %s needs an actor", org.ID)
	}
	describe := func(rail billing.PayoutRail, provider string, status billing.PayoutStatus) string {
		s := string(rail)
		if s == "" {
			s = string(billing.RailWallet)
		}
		if provider != "" {
			s += ":" + provider
		}
		return s + "/" + string(status)
	}
	names := map[string]string{"#ps": "payout_status", "#ua": "updated_at", "#pr": "payout_rail", "#pp": "payout_provider"}
	values := map[string]types.AttributeValue{
		":to":  &types.AttributeValueMemberS{Value: string(status)},
		":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
	}
	expr := "SET #ps = :to, #ua = :now"
	var remove []string
	if rail == "" {
		remove = append(remove, "#pr", "#pp")
	} else {
		expr += ", #pr = :rail"
		values[":rail"] = &types.AttributeValueMemberS{Value: string(rail)}
		if provider == "" {
			remove = append(remove, "#pp")
		} else {
			expr += ", #pp = :prov"
			values[":prov"] = &types.AttributeValueMemberS{Value: provider}
		}
	}
	if len(remove) > 0 {
		expr += " REMOVE " + strings.Join(remove, ", ")
	}
	cond := "attribute_exists(pk) AND #ps = :from"
	values[":from"] = &types.AttributeValueMemberS{Value: string(org.PayoutStatus)}
	auditItem, err := buildAuditItem(org.ID, org.Livemode, AuditEntry{
		Entity: EntityOrganization, EntityID: org.ID, Action: "payout.rail_changed", Cause: billing.CauseManual,
		Actor: actor, RequestID: requestID,
		Before: describe(org.PayoutRail, org.PayoutProvider, org.PayoutStatus), After: describe(rail, provider, status),
	}, "", "", now)
	if err != nil {
		return err
	}
	update := r.base.BuildRawUpdateTxItem(TenantPK(org.ID, org.Livemode), new(OrganizationSK()), expr, cond, names, values)
	if err := r.base.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem))); err != nil {
		if IsConditionFailed(err) {
			return fmt.Errorf("%w: the payout gate of %s changed meanwhile", ErrConcurrentModification, org.ID)
		}
		return conflictErr(err)
	}
	org.PayoutRail, org.PayoutProvider, org.PayoutStatus = rail, provider, status
	if rail == "" {
		org.PayoutProvider = ""
	}
	return nil
}

// SetCollection stores the tenant's late fees for BYOK charges (spec § 7.2),
// audited. Invoices already issued keep the terms they were finalized with.
func (r *OrganizationRepository) SetCollection(ctx context.Context, org *billing.Organization, s billing.CollectionSettings, actor, requestID string, now time.Time) error {
	if err := s.Validate(); err != nil {
		return err
	}
	cv, err := attributevalue.Marshal(s)
	if err != nil {
		return err
	}
	describe := func(s billing.CollectionSettings) string {
		return fmt.Sprintf("fine_bp=%d monthly_interest_bp=%d", s.FineBasisPoints, s.MonthlyInterestBasisPoints)
	}
	auditItem, err := buildAuditItem(org.ID, org.Livemode, AuditEntry{
		Entity: EntityOrganization, EntityID: org.ID, Action: "organization.collection_changed", Cause: billing.CauseManual,
		Actor: actor, RequestID: requestID, Before: describe(org.Collection), After: describe(s),
	}, "", "", now)
	if err != nil {
		return err
	}
	update := r.base.BuildRawUpdateTxItem(TenantPK(org.ID, org.Livemode), new(OrganizationSK()),
		"SET #c = :c, #ua = :now", "attribute_exists(pk)", map[string]string{"#c": "collection", "#ua": "updated_at"},
		map[string]types.AttributeValue{":c": cv, ":now": &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)}})
	if err := r.base.TransactWrite(ctx, txItems(update, r.audit.BuildPutTxItemIfAbsent(auditItem))); err != nil {
		return conflictErr(err)
	}
	org.Collection = s
	return nil
}
