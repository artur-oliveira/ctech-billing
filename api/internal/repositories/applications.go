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
	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

// ErrNoTenantZero is an application where no tenant zero is configured.
var ErrNoTenantZero = errors.New("no tenant zero is configured")

// ErrApplicationBusy is a decision already in progress on the application.
var ErrApplicationBusy = errors.New("another decision on this application is in progress")

// ApplicationRepository stores merchant applications in tenant zero's live
// partition of the organizations table (spec § 4, § 6.2).
type ApplicationRepository struct {
	rows       Base // organizations: tenants, applications, pointers, leases
	audit      Base
	customers  *CustomerRepository
	seal       *crypto.Sealer
	tenantZero string
}

func NewApplicationRepository(db *dynamodb.Client, cfg *config.Config) *ApplicationRepository {
	return &ApplicationRepository{
		rows:       NewBase(db, cfg, TableOrganizations),
		audit:      NewBase(db, cfg, TableAudit),
		customers:  NewCustomerRepository(db, cfg),
		seal:       crypto.NewSealer(cfg.FieldEncryptionKey),
		tenantZero: cfg.PortalOrganizationID,
	}
}

type ApplyInput struct {
	Application        *billing.Application
	ExistingTestTenant *billing.Organization
	Customer           *billing.Customer
	Actor, RequestID   string
}

func (r *ApplicationRepository) pk() string { return TenantPK(r.tenantZero, true) }

func (r *ApplicationRepository) pointer(ctx context.Context, accountOrgID string) (*applicationOrgRow, error) {
	item, err := r.rows.GetItem(ctx, r.pk(), ApplicationOrgSK(accountOrgID))
	if err != nil || item == nil {
		return nil, err
	}
	return Decode[applicationOrgRow](item)
}

// LatestStatus is the organization's current application status, "" when it
// never applied: one GetItem, for the console session (part 1's merchantStatus).
func (r *ApplicationRepository) LatestStatus(ctx context.Context, accountOrgID string) (billing.ApplicationStatus, error) {
	if r.tenantZero == "" {
		return "", nil
	}
	p, err := r.pointer(ctx, accountOrgID)
	if err != nil || p == nil {
		return "", err
	}
	return p.Status, nil
}

// Current is the organization's current application.
func (r *ApplicationRepository) Current(ctx context.Context, accountOrgID string) (*billing.Application, error) {
	if r.tenantZero == "" {
		return nil, ErrNoTenantZero
	}
	p, err := r.pointer(ctx, accountOrgID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("%w: no application for %s", ErrNotFound, accountOrgID)
	}
	return r.Get(ctx, p.ApplicationID)
}

func (r *ApplicationRepository) Get(ctx context.Context, applicationID string) (*billing.Application, error) {
	if r.tenantZero == "" {
		return nil, ErrNoTenantZero
	}
	item, err := r.rows.GetItem(ctx, r.pk(), ApplicationSK(applicationID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, fmt.Errorf("%w: application %s", ErrNotFound, applicationID)
	}
	row, err := Decode[applicationRow](item)
	if err != nil {
		return nil, err
	}
	return r.open(row)
}

func (r *ApplicationRepository) open(row *applicationRow) (*billing.Application, error) {
	a := row.Application
	plain, err := r.seal.Open(a.CompanyTaxID)
	if err != nil {
		return nil, fmt.Errorf("opening the company document of %s: %w", a.ID, err)
	}
	a.CompanyTaxID = plain
	return &a, nil
}

// Apply writes, in ONE conditional transaction (spec § 4.1): the test tenant
// (unless one exists), the application, the pointer, the audit row and the ORG_
// customer (when given). A pending application is returned as it is (created
// false); a lost race returns the winner's when it is pending, else
// ErrConcurrentModification, which the service retries.
func (r *ApplicationRepository) Apply(ctx context.Context, in ApplyInput, now time.Time) (*billing.Application, bool, error) {
	if r.tenantZero == "" {
		return nil, false, ErrNoTenantZero
	}
	app := in.Application
	prev, err := r.pointer(ctx, app.AccountOrganizationID)
	if err != nil {
		return nil, false, err
	}
	if prev != nil {
		switch prev.Status {
		case billing.ApplicationPending:
			cur, err := r.Get(ctx, prev.ApplicationID)
			return cur, false, err
		case billing.ApplicationApproved:
			return nil, false, billing.ErrAlreadyMerchant
		}
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	app.Status, app.AppliedAt = billing.ApplicationPending, stamp

	var writes []types.TransactWriteItem
	switch {
	case prev != nil:
		app.TenantID = prev.TenantID // a re-application: the test tenant stays
	case in.ExistingTestTenant != nil:
		app.TenantID = in.ExistingTestTenant.ID
	default:
		app.TenantID = id.NewWithPrefix(id.PrefixOrganization)
		put, err := organizationPut(r.rows, &billing.Organization{
			ID: app.TenantID, DisplayName: app.OrganizationName, Livemode: false,
			PayoutStatus: billing.PayoutNotConfigured, OwnerUserID: app.AppliedBy,
			AccountOrganizationID: app.AccountOrganizationID,
			LegalName:             app.CompanyLegalName, IssuerTaxID: app.CompanyTaxID,
		}, now)
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, put)
	}

	sealed, err := r.seal.Seal(app.CompanyTaxID)
	if err != nil {
		return nil, false, fmt.Errorf("sealing the company document: %w", err)
	}
	row := applicationRow{
		keys:        newKeys(r.pk(), ApplicationSK(app.ID), RetentionOrganization, now),
		Application: *app,
		LookupPK:    LookupApplicationsPK(r.tenantZero, billing.ApplicationPending),
	}
	row.Application.CompanyTaxID = sealed
	item, err := Encode(row)
	if err != nil {
		return nil, false, err
	}
	writes = append(writes, r.rows.BuildPutTxItemIfAbsent(item))

	if prev == nil {
		pointer, err := Encode(applicationOrgRow{
			keys:                  newKeys(r.pk(), ApplicationOrgSK(app.AccountOrganizationID), RetentionOrganization, now),
			AccountOrganizationID: app.AccountOrganizationID, TenantID: app.TenantID,
			ApplicationID: app.ID, Status: billing.ApplicationPending,
		})
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, r.rows.BuildPutTxItemIfAbsent(pointer))
	} else {
		sk := ApplicationOrgSK(app.AccountOrganizationID)
		writes = append(writes, r.rows.BuildRawUpdateTxItem(r.pk(), &sk,
			"SET application_id = :a, #s = :pending, updated_at = :now",
			"application_id = :prev AND #s = :rejected",
			map[string]string{"#s": "status"},
			map[string]types.AttributeValue{
				":a":        &types.AttributeValueMemberS{Value: app.ID},
				":prev":     &types.AttributeValueMemberS{Value: prev.ApplicationID},
				":pending":  &types.AttributeValueMemberS{Value: string(billing.ApplicationPending)},
				":rejected": &types.AttributeValueMemberS{Value: string(billing.ApplicationRejected)},
				":now":      &types.AttributeValueMemberS{Value: stamp},
			}))
	}

	auditItem, err := buildAuditItem(r.tenantZero, true, AuditEntry{
		Entity: EntityApplication, EntityID: app.ID, Action: billing.AuditApplicationSubmitted,
		Cause: billing.CauseManual, Actor: in.Actor, RequestID: in.RequestID, After: app.AccountOrganizationID,
	}, "", "", now)
	if err != nil {
		return nil, false, err
	}
	writes = append(writes, r.audit.BuildPutTxItemIfAbsent(auditItem))

	if in.Customer != nil {
		cw, _, err := r.customers.createWrites(in.Customer, in.Actor, in.RequestID, now)
		if err != nil {
			return nil, false, err
		}
		writes = append(writes, cw...)
	}

	err = r.rows.TransactWrite(ctx, writes)
	if IsConditionFailed(err) {
		cur, cerr := r.Current(ctx, app.AccountOrganizationID)
		if cerr == nil && cur.Status == billing.ApplicationPending {
			return cur, false, nil
		}
		return nil, false, fmt.Errorf("%w: applying for %s", ErrConcurrentModification, app.AccountOrganizationID)
	}
	if err != nil {
		return nil, false, conflictErr(err)
	}
	return app, true, nil
}

// List pages one status: pending oldest first (a queue), the others newest first.
func (r *ApplicationRepository) List(ctx context.Context, status billing.ApplicationStatus, limit int, start map[string]types.AttributeValue) (*Page[billing.Application], error) {
	if r.tenantZero == "" {
		return nil, ErrNoTenantZero
	}
	res, err := r.rows.Query(ctx, QueryOpts{
		IndexName: IndexLookup, PKField: "lookup_pk", PK: LookupApplicationsPK(r.tenantZero, status),
		SKPrefix: skApplication, ScanIndexForward: status == billing.ApplicationPending,
		Limit: limit, ExclusiveStartKey: start,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[applicationRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := &Page[billing.Application]{Items: make([]billing.Application, 0, len(rows)), LastEvaluatedKey: res.LastEvaluatedKey}
	for i := range rows {
		a, err := r.open(&rows[i])
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *a)
	}
	return out, nil
}

// Lock is a short lease on deciding one application (the LockPlan pattern): a
// crashed decision's lease expires on its own.
func (r *ApplicationRepository) Lock(ctx context.Context, applicationID, token string, now time.Time, lease time.Duration) (func(), error) {
	pk, sk := r.pk(), skApplicationLock+applicationID
	until := now.Add(lease)
	acquire := r.rows.BuildRawUpdateTxItem(pk, &sk,
		"SET lease_until = :until, lease_token = :tok, expires_at = :exp",
		"attribute_not_exists(pk) OR lease_until < :now", nil,
		map[string]types.AttributeValue{
			":until": &types.AttributeValueMemberN{Value: strconv.FormatInt(until.UnixMilli(), 10)},
			":now":   &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UnixMilli(), 10)},
			":tok":   &types.AttributeValueMemberS{Value: token},
			":exp":   &types.AttributeValueMemberN{Value: strconv.FormatInt(until.Add(24*time.Hour).Unix(), 10)},
		})
	err := r.rows.TransactWrite(ctx, txItems(acquire))
	if IsConditionFailed(err) {
		return nil, ErrApplicationBusy
	}
	if err != nil {
		return nil, conflictErr(err)
	}
	return func() {
		release := r.rows.BuildRawUpdateTxItem(pk, &sk, "SET lease_until = :zero", "lease_token = :tok", nil,
			map[string]types.AttributeValue{
				":zero": &types.AttributeValueMemberN{Value: "0"},
				":tok":  &types.AttributeValueMemberS{Value: token},
			})
		_ = r.rows.TransactWrite(context.WithoutCancel(ctx), txItems(release))
	}, nil
}

// Decide moves a pending application to approved or rejected, with the pointer
// and the audit row, conditional on it still being pending.
func (r *ApplicationRepository) Decide(ctx context.Context, app *billing.Application, to billing.ApplicationStatus,
	subscriptionID, reason, actor, requestID string, now time.Time) error {
	if to != billing.ApplicationApproved && to != billing.ApplicationRejected {
		return fmt.Errorf("repositories: %q is not a decision", to)
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	names := map[string]string{"#s": "status"}
	values := map[string]types.AttributeValue{
		":to":      &types.AttributeValueMemberS{Value: string(to)},
		":pending": &types.AttributeValueMemberS{Value: string(billing.ApplicationPending)},
		":lk":      &types.AttributeValueMemberS{Value: LookupApplicationsPK(r.tenantZero, to)},
		":by":      &types.AttributeValueMemberS{Value: actor},
		":at":      &types.AttributeValueMemberS{Value: stamp},
	}
	expr := "SET #s = :to, lookup_pk = :lk, decided_by = :by, decided_at = :at, updated_at = :at"
	if subscriptionID != "" {
		expr += ", subscription_id = :sub"
		values[":sub"] = &types.AttributeValueMemberS{Value: subscriptionID}
	}
	if reason != "" {
		expr += ", rejection_reason = :why"
		values[":why"] = &types.AttributeValueMemberS{Value: reason}
	}
	sk, psk := ApplicationSK(app.ID), ApplicationOrgSK(app.AccountOrganizationID)
	update := r.rows.BuildRawUpdateTxItem(r.pk(), &sk, expr, "#s = :pending", names, values)
	pointer := r.rows.BuildRawUpdateTxItem(r.pk(), &psk, "SET #s = :to, updated_at = :at", "application_id = :id", names,
		map[string]types.AttributeValue{
			":to": values[":to"], ":at": values[":at"], ":id": &types.AttributeValueMemberS{Value: app.ID},
		})
	action := billing.AuditApplicationApproved
	if to == billing.ApplicationRejected {
		action = billing.AuditApplicationRejected
	}
	auditItem, err := buildAuditItem(r.tenantZero, true, AuditEntry{
		Entity: EntityApplication, EntityID: app.ID, Action: action, Cause: billing.CauseManual,
		Actor: actor, RequestID: requestID, After: reason,
	}, string(billing.ApplicationPending), string(to), now)
	if err != nil {
		return err
	}
	err = r.rows.TransactWrite(ctx, txItems(update, pointer, r.audit.BuildPutTxItemIfAbsent(auditItem)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s", billing.ErrApplicationNotPending, app.ID)
	}
	if err != nil {
		return conflictErr(err)
	}
	app.Status, app.DecidedBy, app.DecidedAt = to, actor, stamp
	if subscriptionID != "" {
		app.SubscriptionID = subscriptionID
	}
	if reason != "" {
		app.RejectionReason = reason
	}
	return nil
}
