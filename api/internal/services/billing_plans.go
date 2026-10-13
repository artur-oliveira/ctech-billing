package services

import (
	"context"
	"errors"
	"strconv"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// PlanLimits are a merchant's BYOK plan limits (spec § 12.5), read from the
// metadata of its prod_billing subscription's fixed price. -1 is unlimited.
type PlanLimits struct {
	Plan            string
	ChargesPerMonth int64
	RefuseOverage   bool
	BankConnections int64
	Users           int64
	APIAccess       bool
	SubscriptionID  string
}

// Unlimited is tenant zero's limits when it holds no plan of its own.
var Unlimited = PlanLimits{Plan: "unlimited", ChargesPerMonth: -1, BankConnections: -1, Users: -1, APIAccess: true}

// BillingPlans resolves a tenant's entitling prod_billing subscription in
// tenant zero and the limits its price carries.
type BillingPlans struct {
	customers  *repositories.CustomerRepository
	subs       *repositories.SubscriptionRepository
	catalog    *repositories.CatalogRepository
	tenantZero string
}

func NewBillingPlans(customers *repositories.CustomerRepository, subs *repositories.SubscriptionRepository,
	cat *repositories.CatalogRepository, tenantZero string) *BillingPlans {
	return &BillingPlans{customers: customers, subs: subs, catalog: cat, tenantZero: tenantZero}
}

// For answers the organization's limits and whether it is entitled at all.
// Tenant zero is always entitled (console spec § 4), unlimited unless
// it holds a plan.
func (p *BillingPlans) For(ctx context.Context, org *billing.Organization) (PlanLimits, bool, error) {
	sub, err := p.Subscription(ctx, org)
	if err != nil || sub == nil {
		if err == nil && p.tenantZero != "" && org.ID == p.tenantZero {
			return Unlimited, true, nil
		}
		return PlanLimits{}, false, err
	}
	items, err := p.subs.ListItems(ctx, p.tenantZero, true, sub.ID)
	if err != nil {
		return PlanLimits{}, false, err
	}
	for _, it := range items {
		price, err := p.catalog.GetPrice(ctx, p.tenantZero, true, it.PriceID)
		if err != nil {
			return PlanLimits{}, false, err
		}
		if price.Type == billing.PriceFixed {
			l := limitsOf(price.Metadata)
			l.SubscriptionID = sub.ID
			return l, true, nil
		}
	}
	return PlanLimits{Plan: "unknown", ChargesPerMonth: -1, BankConnections: -1, Users: -1, SubscriptionID: sub.ID}, true, nil
}

// Subscription is the organization's entitling prod_billing subscription in
// tenant zero, nil when it has none.
func (p *BillingPlans) Subscription(ctx context.Context, org *billing.Organization) (*billing.Subscription, error) {
	if p.tenantZero == "" || org.AccountOrganizationID == "" {
		return nil, nil
	}
	customer, err := p.customers.GetByOrganization(ctx, p.tenantZero, true, org.AccountOrganizationID)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list, err := p.subs.ListByCustomer(ctx, p.tenantZero, true, customer.ID, 100)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].OwnerKey == billing.MerchantOwnerKey && list[i].IsEntitled() {
			return &list[i], nil
		}
	}
	return nil, nil
}

// limitsOf reads a plan's metadata; a missing or malformed quota is
// unlimited, so an older price never blocks anyone.
func limitsOf(m billing.Metadata) PlanLimits {
	quota := func(k string) int64 {
		n, err := strconv.ParseInt(m[k], 10, 64)
		if err != nil {
			return -1
		}
		return n
	}
	return PlanLimits{Plan: m["plan"], ChargesPerMonth: quota("quota_charges_month"), RefuseOverage: m["overage"] == "refuse",
		BankConnections: quota("quota_bank_connections"), Users: quota("quota_users"), APIAccess: m["feature_api_access"] == "true"}
}
