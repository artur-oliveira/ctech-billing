package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// EntitlementHook contributes writes to a subscription's own transaction when
// the subscription starts or stops granting service. An error fails the
// subscription change: it never commits without what the hook decided.
type EntitlementHook interface {
	EntitlementWrites(ctx context.Context, sub *billing.Subscription, entitled bool, cause billing.Cause,
		actor, requestID string, now time.Time) ([]types.TransactWriteItem, error)
}

// TenantSuspension suspends a merchant's live tenant when its prod_billing
// entitlement in tenant zero ends, and reinstates it when one is entitled
// again (spec § 4.3, D3). The flag is on the tenant row the request path
// already holds; no entitlement is read per request.
type TenantSuspension struct {
	tenantZero string
	customers  *CustomerRepository
	orgs       *OrganizationRepository
	subs       *SubscriptionRepository
}

func NewTenantSuspension(tenantZero string, customers *CustomerRepository, orgs *OrganizationRepository, subs *SubscriptionRepository) *TenantSuspension {
	return &TenantSuspension{tenantZero: tenantZero, customers: customers, orgs: orgs, subs: subs}
}

func (h *TenantSuspension) EntitlementWrites(ctx context.Context, sub *billing.Subscription, entitled bool,
	cause billing.Cause, actor, requestID string, now time.Time) ([]types.TransactWriteItem, error) {
	if h.tenantZero == "" || sub.OrganizationID != h.tenantZero || !sub.Livemode || sub.OwnerKey != billing.MerchantOwnerKey {
		return nil, nil
	}
	customer, err := h.customers.Get(ctx, h.tenantZero, true, sub.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("suspension: reading the customer of %s: %w", sub.ID, err)
	}
	acct, ok := OrganizationOfRef(customer.ExternalRef)
	if !ok {
		return nil, nil // a prod_billing subscription of a non-organization customer: nothing to suspend
	}
	live, err := h.orgs.GetByAccountOrganization(ctx, acct, true)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, nil // never approved: no live tenant
	case err != nil:
		return nil, fmt.Errorf("suspension: reading the live tenant of %s: %w", acct, err)
	case live.ID == h.tenantZero:
		return nil, nil // tenant zero is never suspended (Review Focus 2)
	}
	if actor == "" {
		actor = "subscription:" + sub.ID
	}
	if entitled {
		if live.SuspendedAt == "" {
			return nil, nil
		}
		return h.orgs.suspensionWrites(live, false, cause, actor, requestID, now)
	}
	if live.SuspendedAt != "" {
		return nil, nil
	}
	others, err := h.subs.ListByCustomer(ctx, h.tenantZero, true, sub.CustomerID, 100)
	if err != nil {
		return nil, fmt.Errorf("suspension: listing %s's subscriptions: %w", customer.ID, err)
	}
	for i := range others {
		if others[i].ID != sub.ID && others[i].OwnerKey == billing.MerchantOwnerKey && others[i].IsEntitled() {
			return nil, nil
		}
	}
	return h.orgs.suspensionWrites(live, true, cause, actor, requestID, now)
}
