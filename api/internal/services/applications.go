package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// EarlyAccessPriceID is what approval subscribes an organization to (spec § 4.4).
const EarlyAccessPriceID = "price_billing_early_access"

var (
	// ErrCompanyNotInOrganization: the company_id is not one of the
	// organization's companies (a 422 on the field, never a hint of whose it is).
	ErrCompanyNotInOrganization = errors.New("company is not one of the organization's")
	// ErrCompaniesUnavailable: ctech-account could not answer about companies.
	// Not ctech-account's 404 on the organization: that is
	// accountclient.ErrOrganizationNotFound, passed through as itself.
	ErrCompaniesUnavailable = errors.New("companies are unavailable")
)

type CompanyDirectory interface {
	Companies(ctx context.Context, organizationID string) (accountclient.CompanyList, error)
	Company(ctx context.Context, organizationID, companyID string) (*accountclient.Company, error)
}

type ApplicationMailer interface {
	SendApplicationReceived(ctx context.Context, to string, n email.ApplicationReceived) error
	SendApplicationDecided(ctx context.Context, to string, n email.ApplicationDecided) error
}

type ApplyInput struct {
	AccountOrganizationID, OrganizationName, UserID, CompanyID, Description, Website string
	Volume                                                                           billing.MonthlyVolume
	ContactEmail, RequestID                                                          string
}

// Applications is the merchant application lifecycle (spec § 4).
type Applications struct {
	apps       *repositories.ApplicationRepository
	orgs       *repositories.OrganizationRepository
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	subscriber *Subscriber
	companies  CompanyDirectory
	mail       ApplicationMailer
	notifyTo   string
	tenantZero string
}

func NewApplications(apps *repositories.ApplicationRepository, orgs *repositories.OrganizationRepository,
	customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	subscriber *Subscriber, companies CompanyDirectory, tenantZero string) *Applications {
	return &Applications{apps: apps, orgs: orgs, customers: customers, subs: subs, subscriber: subscriber,
		companies: companies, tenantZero: tenantZero}
}

// WithMail turns on the notices. A nil mailer (no EMAIL_FROM) leaves them off.
func (s *Applications) WithMail(m ApplicationMailer, notifyTo string) *Applications {
	s.mail, s.notifyTo = m, notifyTo
	return s
}

// Enabled is false without tenant zero: no route of this lifecycle is mounted.
func (s *Applications) Enabled() bool { return s != nil && s.tenantZero != "" }

// Companies lists the organization's companies for the form's picker, at most
// accountclient.ListLimit with Truncated set when ctech-account cut the list.
// accountclient.ErrOrganizationNotFound when ctech-account does not know the
// organization; ErrCompaniesUnavailable when it could not answer.
func (s *Applications) Companies(ctx context.Context, accountOrgID string) (accountclient.CompanyList, error) {
	list, err := s.companies.Companies(ctx, accountOrgID)
	switch {
	case errors.Is(err, accountclient.ErrOrganizationNotFound):
		return accountclient.CompanyList{}, err
	case err != nil:
		return accountclient.CompanyList{}, fmt.Errorf("%w: %w", ErrCompaniesUnavailable, err)
	}
	return list, nil
}

func (s *Applications) Current(ctx context.Context, acct string) (*billing.Application, error) {
	return s.apps.Current(ctx, acct)
}

func (s *Applications) LatestStatus(ctx context.Context, acct string) (billing.ApplicationStatus, error) {
	return s.apps.LatestStatus(ctx, acct)
}

func (s *Applications) List(ctx context.Context, status billing.ApplicationStatus, limit int, start map[string]types.AttributeValue) (*repositories.Page[billing.Application], error) {
	return s.apps.List(ctx, status, limit, start)
}

// Apply validates the company with ctech-account, then writes everything in one
// transaction (ApplicationRepository.Apply). A lost race is retried: the second
// attempt finds the winner's pending application and returns it.
func (s *Applications) Apply(ctx context.Context, in ApplyInput, now time.Time) (*billing.Application, bool, error) {
	company, err := s.companies.Company(ctx, in.AccountOrganizationID, in.CompanyID)
	switch {
	case errors.Is(err, accountclient.ErrCompanyNotFound):
		return nil, false, ErrCompanyNotInOrganization
	case err != nil:
		return nil, false, fmt.Errorf("%w: %w", ErrCompaniesUnavailable, err)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		app, created, err := s.applyOnce(ctx, in, company, now)
		if errors.Is(err, repositories.ErrTransactionConflict) || errors.Is(err, repositories.ErrConcurrentModification) {
			lastErr = err
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if created {
			s.notifyReceived(ctx, app)
		}
		return app, created, nil
	}
	return nil, false, lastErr
}

func (s *Applications) applyOnce(ctx context.Context, in ApplyInput, company *accountclient.Company, now time.Time) (*billing.Application, bool, error) {
	if _, err := s.orgs.GetByAccountOrganization(ctx, in.AccountOrganizationID, true); err == nil {
		return nil, false, billing.ErrAlreadyMerchant
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, false, err
	}
	var existing *billing.Organization
	test, err := s.orgs.GetByAccountOrganization(ctx, in.AccountOrganizationID, false)
	switch {
	case err == nil:
		existing = test
	case !errors.Is(err, repositories.ErrNotFound):
		return nil, false, err
	}
	var customer *billing.Customer
	if _, err := s.customers.GetByOrganization(ctx, s.tenantZero, true, in.AccountOrganizationID); errors.Is(err, repositories.ErrNotFound) {
		customer = &billing.Customer{
			ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: s.tenantZero, Livemode: true,
			ExternalRef: repositories.OrgRefPrefix + in.AccountOrganizationID,
			Name:        in.OrganizationName, Email: in.ContactEmail, TaxID: company.TaxID,
		}
	} else if err != nil {
		return nil, false, err
	}
	return s.apps.Apply(ctx, repositories.ApplyInput{
		Application: &billing.Application{
			ID: id.NewWithPrefix(id.PrefixApplication), AccountOrganizationID: in.AccountOrganizationID,
			OrganizationName: in.OrganizationName, CompanyID: company.ID, CompanyLegalName: company.LegalName,
			CompanyTaxID: company.TaxID, Description: in.Description, Website: in.Website,
			ExpectedMonthlyVolume: in.Volume, ContactEmail: in.ContactEmail, AppliedBy: in.UserID,
		},
		ExistingTestTenant: existing, Customer: customer,
		Actor: "user:" + in.UserID, RequestID: in.RequestID,
	}, now)
}

const decisionLease = 30 * time.Second

// Approve is idempotent (spec § 4.2): it reuses an entitled prod_billing
// subscription and an existing live tenant, so a retry after a crash finishes
// the job, and an approved application is answered as it is.
func (s *Applications) Approve(ctx context.Context, applicationID, actor, requestID string, now time.Time) (*billing.Application, error) {
	release, err := s.apps.Lock(ctx, applicationID, id.New(), now, decisionLease)
	if err != nil {
		return nil, err
	}
	defer release()
	app, err := s.apps.Get(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	switch app.Status {
	case billing.ApplicationApproved:
		return app, nil
	case billing.ApplicationRejected:
		return nil, billing.ErrApplicationNotPending
	}
	customer, err := s.customers.GetByOrganization(ctx, s.tenantZero, true, app.AccountOrganizationID)
	if err != nil {
		return nil, fmt.Errorf("the application's ORG_ customer: %w", err)
	}
	sub, err := s.entitlement(ctx, customer.ID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		sub, _, err = s.subscriber.Subscribe(ctx, SubscribeInput{
			OrganizationID: s.tenantZero, Livemode: true, CustomerID: customer.ID,
			Items:    []SubscribeItem{{PriceID: EarlyAccessPriceID}},
			Metadata: billing.Metadata{"application_id": app.ID}, Actor: actor,
		}, now)
		if sub == nil {
			return nil, err
		}
		if err != nil {
			// The subscription exists and entitles; only its R$ 0 invoice failed,
			// and its generation key makes the sweep produce it exactly once.
			slog.Warn("applications: the early access invoice was not issued", "subscription", sub.ID, "error", err)
		}
	}
	if err := s.ensureLiveTenant(ctx, app, now); err != nil {
		return nil, err
	}
	if err := s.apps.Decide(ctx, app, billing.ApplicationApproved, sub.ID, "", actor, requestID, now); err != nil {
		return nil, err
	}
	s.notifyDecided(ctx, app)
	return app, nil
}

// entitlement is the customer's entitled prod_billing subscription, if any.
func (s *Applications) entitlement(ctx context.Context, customerID string) (*billing.Subscription, error) {
	subs, err := s.subs.ListByCustomer(ctx, s.tenantZero, true, customerID, 100)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].OwnerKey == billing.MerchantOwnerKey && subs[i].IsEntitled() {
			return &subs[i], nil
		}
	}
	return nil, nil
}

// ensureLiveTenant creates the live tenant with the test tenant's id and name,
// unless it exists. Its issuer block is the APPLICATION's company, the one the
// reviewer approved (spec § 4.1): the test tenant may carry an earlier
// application's company, or none when it was provisioned by hand.
func (s *Applications) ensureLiveTenant(ctx context.Context, app *billing.Application, now time.Time) error {
	if _, err := s.orgs.Get(ctx, app.TenantID, true); err == nil || !errors.Is(err, repositories.ErrNotFound) {
		return err
	}
	test, err := s.orgs.Get(ctx, app.TenantID, false)
	if err != nil {
		return fmt.Errorf("the application's test tenant: %w", err)
	}
	return s.orgs.Create(ctx, &billing.Organization{
		ID: app.TenantID, DisplayName: test.DisplayName, Livemode: true,
		PayoutStatus: billing.PayoutNotConfigured, OwnerUserID: test.OwnerUserID,
		AccountOrganizationID: app.AccountOrganizationID, LegalName: app.CompanyLegalName, IssuerTaxID: app.CompanyTaxID,
	}, now)
}

// Reject is idempotent on a rejected application; an approved one is refused.
// It takes the same lease as Approve: a rejection landing between an approval's
// subscription and its decision would leave an entitled subscription and a live
// tenant behind a rejected application.
func (s *Applications) Reject(ctx context.Context, applicationID, reason, actor, requestID string, now time.Time) (*billing.Application, error) {
	release, err := s.apps.Lock(ctx, applicationID, id.New(), now, decisionLease)
	if err != nil {
		return nil, err
	}
	defer release()
	app, err := s.apps.Get(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	switch app.Status {
	case billing.ApplicationRejected:
		return app, nil
	case billing.ApplicationApproved:
		return nil, billing.ErrApplicationNotPending
	}
	if err := s.apps.Decide(ctx, app, billing.ApplicationRejected, "", reason, actor, requestID, now); err != nil {
		return nil, err
	}
	s.notifyDecided(ctx, app)
	return app, nil
}

// The notices are best effort (spec § 8): logged, retried by nobody.
func (s *Applications) notifyReceived(ctx context.Context, app *billing.Application) {
	if s.mail == nil || s.notifyTo == "" {
		return
	}
	if err := s.mail.SendApplicationReceived(context.WithoutCancel(ctx), s.notifyTo, email.ApplicationReceived{
		ApplicationID: app.ID, OrganizationName: app.OrganizationName, CompanyLegalName: app.CompanyLegalName,
		Description: app.Description, Website: app.Website, ContactEmail: app.ContactEmail,
	}); err != nil {
		slog.Error("applications: the notice to CTech was not sent", "application", app.ID, "error", err)
	}
}

func (s *Applications) notifyDecided(ctx context.Context, app *billing.Application) {
	if s.mail == nil {
		return
	}
	if err := s.mail.SendApplicationDecided(context.WithoutCancel(ctx), app.ContactEmail, email.ApplicationDecided{
		OrganizationName: app.OrganizationName, Approved: app.Status == billing.ApplicationApproved, Reason: app.RejectionReason,
	}); err != nil {
		slog.Error("applications: the decision e-mail was not sent", "application", app.ID, "error", err)
	}
}

// MerchantEntitlement reports a live tenant approved by an entitling
// prod_billing subscription of its ORG_ customer in tenant zero (spec § 7.1,
// R4). Tenant zero itself is always entitled. The BYOK charge gate reads it.
func MerchantEntitlement(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository, tenantZero string) func(context.Context, *billing.Organization) (bool, error) {
	return func(ctx context.Context, org *billing.Organization) (bool, error) {
		if tenantZero != "" && org.ID == tenantZero {
			return true, nil
		}
		if tenantZero == "" || org.AccountOrganizationID == "" {
			return false, nil
		}
		customer, err := customers.GetByOrganization(ctx, tenantZero, true, org.AccountOrganizationID)
		if errors.Is(err, repositories.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		list, err := subs.ListByCustomer(ctx, tenantZero, true, customer.ID, 100)
		if err != nil {
			return false, err
		}
		for i := range list {
			if list[i].OwnerKey == billing.MerchantOwnerKey && list[i].IsEntitled() {
				return true, nil
			}
		}
		return false, nil
	}
}
