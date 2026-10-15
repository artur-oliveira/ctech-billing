package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/crypto"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// CustomerRepository stores invoice addressees.
//
// There is no Delete. A deletion request anonymizes the record instead
// (ADR 0009): an issued invoice is a document and cannot lose its addressee
// because someone asked to be forgotten, but nothing identifying has to remain.
type CustomerRepository struct {
	base   Base
	audit  Base
	events Base
	rt     Realtime
	// seal encrypts the one stored field that is personal data on its own. It
	// lives here rather than in the domain because it is a storage concern: a
	// billing.Customer in memory holds a readable tax id, and only the row does
	// not. Handlers, services and tests are unaffected by its existence.
	seal *crypto.Sealer
}

func NewCustomerRepository(db *dynamodb.Client, cfg *config.Config) *CustomerRepository {
	return &CustomerRepository{
		base:   NewBase(db, cfg, TableCustomers),
		audit:  NewBase(db, cfg, TableAudit),
		events: NewBase(db, cfg, TableWebhooks),
		seal:   crypto.NewSealer(cfg.FieldEncryptionKey),
		rt:     realtimeDefault(),
	}
}

// WithRealtime makes a customer created or anonymized write a change to the
// realtime log and publish it.
func (r *CustomerRepository) WithRealtime(rt Realtime) *CustomerRepository {
	r.rt = rt
	return r
}

func (r *CustomerRepository) change(c *billing.Customer, status string) *Change {
	return r.rt.tenant(c.OrganizationID, c.Livemode, "customer", c.ID, "customer.updated", status)
}

func (r *CustomerRepository) customerRowOf(c *billing.Customer, now time.Time) (customerRow, error) {
	row := customerRow{
		keys:        newKeys(TenantPK(c.OrganizationID, c.Livemode), CustomerSK(c.ID), RetentionCustomer, now),
		PeriodAttrs: NewPeriodAttrs(c.OrganizationID, c.Livemode, EntityCustomer, brcal.FromTime(now), c.ID),
		Customer:    *c,
	}
	if c.ExternalRef != "" {
		row.LookupPK = LookupCustomerRefPK(c.OrganizationID, c.Livemode, c.ExternalRef)
	}
	sealed, err := r.seal.Seal(c.TaxID)
	if err != nil {
		return customerRow{}, fmt.Errorf("sealing tax id: %w", err)
	}
	// The copy in the row is replaced, never the caller's struct. A Create that
	// left the caller holding ciphertext would be a caller that renders it.
	row.Customer.TaxID = sealed
	return row, nil
}

// open reverses the sealing on the way out.
//
// Every read path in this repository goes through it, which is the property that
// matters: one that did not would return ciphertext, and ciphertext rendered on
// an invoice looks like a corrupted record rather than a missing decryption.
func (r *CustomerRepository) open(row *customerRow) (*billing.Customer, error) {
	c := row.Customer
	c.Since = row.CreatedAt
	plain, err := r.seal.Open(c.TaxID)
	if err != nil {
		return nil, fmt.Errorf("opening tax id of customer %s: %w", c.ID, err)
	}
	c.TaxID = plain
	return &c, nil
}

// ErrUserAlreadyCustomer reports a ctech-account subject already claimed by
// another customer in the same organization.
var ErrUserAlreadyCustomer = errors.New("user is already a customer of this organization")

// ErrOrganizationAlreadyCustomer reports an ORG_ customer whose ctech-account
// organization already has one in this tenant.
var ErrOrganizationAlreadyCustomer = errors.New("organization is already a customer of this organization")

// createWrites is Create's transaction without committing it, so a caller in
// this package can make a customer part of a larger one (the application,
// spec § 4.1). isOrg reports an ORG_ customer, for the caller's error message.
func (r *CustomerRepository) createWrites(c *billing.Customer, actor, requestID string, now time.Time) (writes []types.TransactWriteItem, isOrg bool, err error) {
	if err := c.Metadata.Validate(); err != nil {
		return nil, false, err
	}
	if actor == "" {
		// Every other write in this repository records who did it; a customer
		// that appeared with no author is the row support cannot explain.
		return nil, false, fmt.Errorf("repositories: creating customer %s needs an actor", c.ID)
	}
	row, err := r.customerRowOf(c, now)
	if err != nil {
		return nil, false, err
	}
	item, err := Encode(row)
	if err != nil {
		return nil, false, err
	}
	writes = txItems(r.base.BuildPutTxItemIfAbsent(item))

	if c.UserID != "" {
		pointer, err := Encode(customerUserRow{
			keys:       newKeys(TenantPK(c.OrganizationID, c.Livemode), CustomerUserSK(c.UserID), RetentionCustomer, now),
			UserID:     c.UserID,
			CustomerID: c.ID,
		})
		if err != nil {
			return nil, false, err
		}
		// Conditional: one account is one customer per organization. Without it
		// the second write wins and one person's portal silently starts showing
		// somebody else's invoices.
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(pointer))
	}
	orgID, isOrg := OrganizationOfRef(c.ExternalRef)
	if isOrg {
		pointer, err := Encode(customerOrgRow{
			keys:                  newKeys(TenantPK(c.OrganizationID, c.Livemode), CustomerOrgSK(orgID), RetentionCustomer, now),
			AccountOrganizationID: orgID,
			CustomerID:            c.ID,
		})
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, r.base.BuildPutTxItemIfAbsent(pointer))
	}

	// The name, not the tax id: the audit table is not the place to make a
	// second copy of the one field this repository encrypts.
	auditItem, err := buildAuditItem(c.OrganizationID, c.Livemode, AuditEntry{
		Entity:    EntityCustomer,
		EntityID:  c.ID,
		Action:    "customer.created",
		Cause:     billing.CauseManual,
		Actor:     actor,
		RequestID: requestID,
		After:     c.Name,
	}, "", "", now)
	if err != nil {
		return nil, false, err
	}
	writes = append(writes, r.audit.BuildPutTxItemIfAbsent(auditItem))
	return writes, isOrg, nil
}

// Create writes a new customer, failing if the id is taken.
//
// When the customer carries a ctech-account subject, the pointer row that lets
// them sign in to the portal is written in the **same transaction** (ADR 0012).
// Writing it afterwards would leave a window where the customer exists and their
// own invoices are unreachable to them, and a failure in that window is silent.
func (r *CustomerRepository) Create(ctx context.Context, c *billing.Customer, actor, requestID string, now time.Time) error {
	writes, isOrg, err := r.createWrites(c, actor, requestID, now)
	if err != nil {
		return err
	}
	change := r.change(c, "")
	changes, err := changeWrites(r.events, change, now)
	if err != nil {
		return err
	}
	writes = append(writes, changes...)
	err = r.base.TransactWrite(ctx, writes)
	if err == nil {
		r.rt.publish(ctx, change)
	}
	if IsConditionFailed(err) {
		// The transaction cancels as a unit, so either the id or the subject was
		// taken. Naming the subject is the useful answer: a caller that generated
		// the id knows it is unique, and a caller that reused one is retrying.
		if isOrg {
			orgID, _ := OrganizationOfRef(c.ExternalRef)
			return fmt.Errorf("%w: %s", ErrOrganizationAlreadyCustomer, orgID)
		}
		if c.UserID != "" {
			return fmt.Errorf("%w: %s", ErrUserAlreadyCustomer, c.UserID)
		}
		return fmt.Errorf("customer %s already exists", c.ID)
	}
	return conflictErr(err)
}

// GetByOrganization resolves a ctech-account organization to its customer
// through the CUSTOMER_ORG# pointer.
func (r *CustomerRepository) GetByOrganization(ctx context.Context, organizationID string, livemode bool, accountOrganizationID string) (*billing.Customer, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), CustomerOrgSK(accountOrganizationID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: no customer for organization %s", ErrNotFound, accountOrganizationID)
	}
	pointer, err := Decode[customerOrgRow](item)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, organizationID, livemode, pointer.CustomerID)
}

// GetByUser resolves a signed-in person to their customer record in one
// organization — the portal's only identity lookup (ADR 0012).
//
// Two reads rather than a denormalized copy: the pointer row holds an id, not a
// customer. A copy would be a second version of a record that is edited,
// anonymized on request, and legally required to be one thing.
func (r *CustomerRepository) GetByUser(ctx context.Context, organizationID string, livemode bool, userID string) (*billing.Customer, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), CustomerUserSK(userID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: no customer for user %s", ErrNotFound, userID)
	}
	pointer, err := Decode[customerUserRow](item)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, organizationID, livemode, pointer.CustomerID)
}

// List pages the tenant's customers, newest first.
//
// It reads the period index with no date prefix, which is the whole partition
// for this entity in creation order. That is a Query on a tenant-scoped
// partition, not a Scan — the distinction ADR 0002 cares about is whether a
// cross-tenant read is expressible, and here it is not.
func (r *CustomerRepository) List(
	ctx context.Context,
	organizationID string,
	livemode bool,
	limit int,
	startKey map[string]types.AttributeValue,
) (*Page[billing.Customer], error) {
	page, err := pagePeriod(ctx, r.base, organizationID, livemode, EntityCustomer,
		"", limit, startKey,
		func(row customerRow) customerRow { return row })
	if err != nil {
		return nil, err
	}
	out := &Page[billing.Customer]{
		Items:            make([]billing.Customer, 0, len(page.Items)),
		LastEvaluatedKey: page.LastEvaluatedKey,
	}
	for i := range page.Items {
		c, err := r.open(&page.Items[i])
		if err != nil {
			// One unreadable row fails the page rather than being dropped from it.
			// A listing quietly missing a customer is how a support agent concludes
			// somebody was never billed.
			return nil, err
		}
		out.Items = append(out.Items, *c)
	}
	return out, nil
}

// Get reads a customer by id.
func (r *CustomerRepository) Get(ctx context.Context, organizationID string, livemode bool, customerID string) (*billing.Customer, error) {
	item, err := r.base.GetItem(ctx, TenantPK(organizationID, livemode), CustomerSK(customerID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: customer %s", ErrNotFound, customerID)
	}
	row, err := Decode[customerRow](item)
	if err != nil {
		return nil, err
	}
	return r.open(row)
}

// GetByExternalRef resolves a customer from the caller's own identifier, so a
// consuming product does not have to keep a mapping table of its own.
//
// It queries the sparse lookup index rather than filtering, because filtering
// would mean reading the tenant's whole partition to find one row.
func (r *CustomerRepository) GetByExternalRef(ctx context.Context, organizationID string, livemode bool, externalRef string) (*billing.Customer, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupCustomerRefPK(organizationID, livemode, externalRef),
		Limit:     1,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, fmt.Errorf("%w: customer with external ref %q", ErrNotFound, externalRef)
	}
	row, err := Decode[customerRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return r.open(row)
}

// Anonymize erases the identifying content in place and drops the customer out
// of the external-reference index.
//
// Removing lookup_pk is not cosmetic: leaving it would let anyone who already
// knows the external reference confirm the person was a customer, which is
// precisely what the erasure was asked for.
func (r *CustomerRepository) Anonymize(ctx context.Context, c *billing.Customer, actor, requestID string, now time.Time) error {
	anonymized := *c
	anonymized.Anonymize()

	updates := map[string]any{
		"name":         anonymized.Name,
		"email":        nil,
		"tax_id":       nil,
		"external_ref": nil,
		"metadata":     nil,
		"anonymized":   true,
		"lookup_pk":    nil,
		"updated_at":   now.UTC().Format(time.RFC3339Nano),
	}
	if _, err := r.base.UpdateItem(ctx, TenantPK(c.OrganizationID, c.Livemode), new(CustomerSK(c.ID)), updates); err != nil {
		return err
	}

	if err := r.appendAudit(ctx, c, actor, requestID, now); err != nil {
		return err
	}
	*c = anonymized
	r.rt.announce(ctx, r.events, r.change(c, "anonymized"), now)
	return nil
}

// AcceptTerms stamps the terms version in force as agreed to, now.
//
// Audited, and that is the point of the method rather than a detail of it:
// consent whose time and actor were never written down is consent nobody can
// evidence later, which is the only situation where it matters.
//
// Idempotent by construction — the same version written twice is the same row.
// A second audit entry is not, so a repeat is refused early rather than
// producing a trail that suggests somebody accepted twice.
func (r *CustomerRepository) AcceptTerms(ctx context.Context, c *billing.Customer, actor, requestID string, now time.Time) error {
	if c.AcceptedCurrentTerms() {
		return nil
	}
	updates := map[string]any{
		"terms_version": billing.CurrentTermsVersion,
		"updated_at":    now.UTC().Format(time.RFC3339Nano),
	}
	if _, err := r.base.UpdateItem(ctx, TenantPK(c.OrganizationID, c.Livemode), new(CustomerSK(c.ID)), updates); err != nil {
		return err
	}
	if err := r.appendTermsAudit(ctx, c, actor, requestID, now); err != nil {
		return err
	}
	c.TermsVersion = billing.CurrentTermsVersion
	return nil
}

// SetTaxID gives a customer that has none a tax id. It never replaces one: the
// tax id is the NF tomador and the payer check, so changing it is not something
// a service client may do. The caller has already refused a different value.
func (r *CustomerRepository) SetTaxID(ctx context.Context, c *billing.Customer, taxID, actor, requestID string, now time.Time) error {
	sealed, err := r.seal.Seal(taxID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"tax_id":     sealed,
		"updated_at": now.UTC().Format(time.RFC3339Nano),
	}
	if _, err := r.base.UpdateItem(ctx, TenantPK(c.OrganizationID, c.Livemode), new(CustomerSK(c.ID)), updates); err != nil {
		return err
	}
	item, err := buildAuditItem(c.OrganizationID, c.Livemode, AuditEntry{
		Entity: EntityCustomer, EntityID: c.ID, Action: "customer.tax_id_set", Cause: billing.CauseCustomer,
		Actor: actor, RequestID: requestID, Before: "none", After: "set",
	}, "none", "set", now)
	if err != nil {
		return err
	}
	if err := r.audit.TransactWrite(ctx, txItems(r.audit.BuildPutTxItemIfAbsent(item))); err != nil {
		return err
	}
	c.TaxID = taxID
	return nil
}

// appendTermsAudit records who agreed to what, and when.
func (r *CustomerRepository) appendTermsAudit(ctx context.Context, c *billing.Customer, actor, requestID string, now time.Time) error {
	before := c.TermsVersion
	if before == "" {
		before = "none"
	}
	item, err := buildAuditItem(c.OrganizationID, c.Livemode, AuditEntry{
		Entity:    EntityCustomer,
		EntityID:  c.ID,
		Action:    "customer.terms_accepted",
		Cause:     billing.CauseCustomer,
		Actor:     actor,
		RequestID: requestID,
		Before:    before,
		After:     billing.CurrentTermsVersion,
	}, before, billing.CurrentTermsVersion, now)
	if err != nil {
		return err
	}
	return r.audit.TransactWrite(ctx, txItems(r.audit.BuildPutTxItemIfAbsent(item)))
}

// appendAudit records the erasure. It is a separate write rather than part of a
// transaction because the update above is a multi-attribute REMOVE that the
// shared transaction builder does not express — and losing the audit row here
// would be far worse than the erasure being retried, so a failure is returned
// rather than swallowed.
func (r *CustomerRepository) appendAudit(ctx context.Context, c *billing.Customer, actor, requestID string, now time.Time) error {
	item, err := buildAuditItem(c.OrganizationID, c.Livemode, AuditEntry{
		Entity:    EntityCustomer,
		EntityID:  c.ID,
		Action:    "customer.anonymized",
		Cause:     billing.CauseCustomer,
		Actor:     actor,
		RequestID: requestID,
		Before:    "identified",
		After:     "anonymized",
	}, "identified", "anonymized", now)
	if err != nil {
		return err
	}
	return r.audit.TransactWrite(ctx, txItems(r.audit.BuildPutTxItemIfAbsent(item)))
}

// RecordTaxIDAccess writes the audit entry for revealing a masked tax id.
//
// Reading PII is audited, not only writing it. Without this, a data-subject
// request asking "who has seen my CPF" cannot be answered honestly, and the
// masking in the UI would be theatre.
func (r *CustomerRepository) RecordTaxIDAccess(ctx context.Context, c *billing.Customer, actor, requestID, ip string, now time.Time) error {
	item, err := buildAuditItem(c.OrganizationID, c.Livemode, AuditEntry{
		Entity:    EntityCustomer,
		EntityID:  c.ID,
		Action:    billing.AuditTaxIDRevealed,
		Actor:     actor,
		RequestID: requestID,
		IP:        ip,
	}, "masked", "revealed", now)
	if err != nil {
		return err
	}
	return r.audit.TransactWrite(ctx, txItems(r.audit.BuildPutTxItemIfAbsent(item)))
}

// ErrPlanBusy reports a customer whose plan another request is changing.
var ErrPlanBusy = errors.New("another plan choice for this customer is in progress")

const skPlanLock = "PLAN_LOCK#"

// LockPlan takes a short lease on a customer's plan choice, so two first
// choices (two tabs, a double click) cannot both find no subscription and both
// subscribe. A lease rather than a lock row that must be removed: one that a
// crashed request never released expires on its own. The returned func releases
// it, only if it is still this request's.
func (r *CustomerRepository) LockPlan(ctx context.Context, organizationID string, livemode bool, customerID, token string, now time.Time, lease time.Duration) (func(), error) {
	pk, sk := TenantPK(organizationID, livemode), skPlanLock+customerID
	until := now.Add(lease)
	acquire := r.base.BuildRawUpdateTxItem(pk, &sk,
		"SET lease_until = :until, lease_token = :tok, expires_at = :exp",
		"attribute_not_exists(pk) OR lease_until < :now", nil,
		map[string]types.AttributeValue{
			":until": &types.AttributeValueMemberN{Value: strconv.FormatInt(until.UnixMilli(), 10)},
			":now":   &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UnixMilli(), 10)},
			":tok":   &types.AttributeValueMemberS{Value: token},
			":exp":   &types.AttributeValueMemberN{Value: strconv.FormatInt(until.Add(24*time.Hour).Unix(), 10)},
		})
	err := r.base.TransactWrite(ctx, txItems(acquire))
	if IsConditionFailed(err) {
		return nil, ErrPlanBusy
	}
	if err != nil {
		return nil, conflictErr(err)
	}
	return func() {
		release := r.base.BuildRawUpdateTxItem(pk, &sk, "SET lease_until = :zero", "lease_token = :tok", nil,
			map[string]types.AttributeValue{
				":zero": &types.AttributeValueMemberN{Value: "0"},
				":tok":  &types.AttributeValueMemberS{Value: token},
			})
		_ = r.base.TransactWrite(context.WithoutCancel(ctx), txItems(release))
	}, nil
}
